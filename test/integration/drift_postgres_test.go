//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"saxbase/internal/releases"
)

func TestPostgresDrift(t *testing.T) {
	raw := os.Getenv("SAXBASE_TEST_POSTGRES_DSN")
	if raw == "" {
		t.Fatal("set SAXBASE_TEST_POSTGRES_DSN to a PostgreSQL maintenance database URL")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		t.Fatal("SAXBASE_TEST_POSTGRES_DSN must be a postgres:// URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	admin, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal("open PostgreSQL connection failed")
	}
	t.Cleanup(func() { admin.Close() })
	waitForPostgres(t, ctx, admin)

	name := fmt.Sprintf("saxbase_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})
	u.Path = "/" + name
	dsn := u.String()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("open test database failed")
	}
	t.Cleanup(func() { db.Close() })

	workDir := t.TempDir()
	writePostgresFixture(t, workDir)
	commitFixture(t, workDir)
	objectDir := filepath.Join(workDir, "database/objects")
	files, err := releases.CommittedFiles(ctx, objectDir)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(workDir, "database/release.json")
	manifest, err := releases.New("1.0.0", 2, files)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(manifestPath); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(t.TempDir(), "saxbase")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	execute := func(command ...string) (string, error) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, command...)
		cmd.Dir = workDir
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "GOOSE_") && !strings.HasPrefix(value, "SAXBASE_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "SAXBASE_DIALECT=postgres", "SAXBASE_DSN="+dsn)
		output, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	run := func(command ...string) string {
		t.Helper()
		output, err := execute(command...)
		if err != nil {
			t.Fatalf("saxbase %v: %v\n%s", command, err, output)
		}
		return output
	}

	run("apply")
	if output := strings.TrimSpace(run("drift")); output != "No drift detected." {
		t.Fatalf("clean drift: %s", output)
	}

	// A hand-added table is not managed by the release: not drift.
	if _, err := db.ExecContext(ctx, "CREATE TABLE public.handmade(id int)"); err != nil {
		t.Fatal(err)
	}
	if output := strings.TrimSpace(run("drift")); output != "No drift detected." {
		t.Fatalf("hand-added table must not be drift: %s", output)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE public.handmade"); err != nil {
		t.Fatal(err)
	}

	// A structural change to a managed table (dropped column) is drift.
	if _, err := db.ExecContext(ctx, "ALTER TABLE public.customers DROP COLUMN nickname;"); err != nil {
		t.Fatal(err)
	}
	output, err := execute("drift")
	squeezed := strings.Join(strings.Fields(output), " ")
	if err == nil || !strings.Contains(squeezed, "Schema fingerprint: DRIFTED") || !strings.Contains(squeezed, "column public.customers.nickname") {
		t.Fatalf("schema drift not detected: err=%v output=%s", err, output)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE public.customers ADD COLUMN nickname text;"); err != nil {
		t.Fatal(err)
	}

	// A direct object edit is drift too. (Same columns: CREATE OR REPLACE VIEW
	// cannot drop columns, and correct re-applies the file that way.)
	if _, err := db.ExecContext(ctx, "CREATE OR REPLACE VIEW public.saxbase_customers AS SELECT id, name FROM public.customers WHERE id > 0;"); err != nil {
		t.Fatal(err)
	}
	output, err = execute("drift")
	squeezed = strings.Join(strings.Fields(output), " ")
	if err == nil || !strings.Contains(squeezed, "views/customers.sql: drifted") || !strings.Contains(squeezed, "definition changed") {
		t.Fatalf("object drift not detected: err=%v output=%s", err, output)
	}

	// Correction re-applies the pinned object; drift must report clean again.
	run("correct")
	if output := strings.TrimSpace(run("drift")); output != "No drift detected." {
		t.Fatalf("drift after correct: %s", output)
	}
}

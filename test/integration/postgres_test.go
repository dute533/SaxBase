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

func TestPostgresReleaseLifecycle(t *testing.T) {
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
	base, err := releases.New("1.0.0", 1, files)
	if err != nil {
		t.Fatal(err)
	}
	if err := base.Write(manifestPath); err != nil {
		t.Fatal(err)
	}
	second, err := releases.NewDelta("1.1.0", 2, files, files)
	if err != nil {
		t.Fatal(err)
	}
	if err := releases.Append(manifestPath, second); err != nil {
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
		for _, arg := range command {
			if arg == "create" {
				commitFixture(t, workDir)
				break
			}
		}
		output, err := execute(command...)
		if err != nil {
			t.Fatalf("saxbase %v: %v\n%s", command, err, output)
		}
		return output
	}

	plan := run("plan")
	for _, want := range []string{"00001_create_customers.sql", "functions/customer_count.sql", "procedures/add_customer.sql", "views/customers.sql"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("plan missing %q:\n%s", want, plan)
		}
	}
	run("apply")
	run("status")
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM public.customers WHERE name='Ada' AND nickname IS NULL", 1)
	assertPostgresCount(t, ctx, db, "SELECT public.saxbase_customer_count()", 1)
	if _, err := db.ExecContext(ctx, "CALL public.saxbase_add_customer('Grace')"); err != nil {
		t.Fatal(err)
	}
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM public.saxbase_customers", 2)
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM public.saxbase_releases", 2)
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM public.saxbase_schema_version WHERE version_id IN (1,2) AND is_applied", 2)

	updatedFunction := `CREATE OR REPLACE FUNCTION public.saxbase_customer_count()
RETURNS bigint
LANGUAGE sql
AS $function$
    SELECT COUNT(*) + 10 FROM public.customers;
$function$;
`
	updatedProcedure := `CREATE OR REPLACE PROCEDURE public.saxbase_add_customer(IN customer_name text)
LANGUAGE sql
AS $procedure$
    INSERT INTO public.customers(name) VALUES (upper(customer_name));
$procedure$;
`
	writeFixtureFile(t, filepath.Join(objectDir, "functions/customer_count.sql"), updatedFunction)
	writeFixtureFile(t, filepath.Join(objectDir, "procedures/add_customer.sql"), updatedProcedure)
	run("release", "create", "1.1.1")
	run("apply")
	assertPostgresCount(t, ctx, db, "SELECT public.saxbase_customer_count()", 12)
	if _, err := db.ExecContext(ctx, "CALL public.saxbase_add_customer('linus')"); err != nil {
		t.Fatal(err)
	}
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM public.customers WHERE name='LINUS'", 1)
	if err := os.Remove(filepath.Join(objectDir, "functions/customer_count.sql")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(objectDir, "procedures/add_customer.sql")); err != nil {
		t.Fatal(err)
	}
	run("release", "create", "1.1.2")
	run("apply")
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('saxbase_customer_count','saxbase_add_customer')", 0)
	run("rollback", "1.1.1")
	assertPostgresCount(t, ctx, db, "SELECT public.saxbase_customer_count()", 13)
	if _, err := db.ExecContext(ctx, "CALL public.saxbase_add_customer('barbara')"); err != nil {
		t.Fatal(err)
	}
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM public.customers WHERE name='BARBARA'", 1)

	run("rollback", "1.1.0")
	assertPostgresCount(t, ctx, db, "SELECT public.saxbase_customer_count()", 4)
	if _, err := db.ExecContext(ctx, "CALL public.saxbase_add_customer('Margaret')"); err != nil {
		t.Fatal(err)
	}
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM public.customers WHERE name='Margaret'", 1)

	writeFixtureFile(t, filepath.Join(objectDir, "functions/customer_count.sql"), updatedFunction)
	writeFixtureFile(t, filepath.Join(objectDir, "procedures/add_customer.sql"), updatedProcedure)
	broken := "CREATE OR REPLACE VIEW public.saxbase_broken AS SELECT missing FROM public.does_not_exist;\n"
	writeFixtureFile(t, filepath.Join(objectDir, "views/broken.sql"), broken)
	run("release", "create", "1.1.3")
	if output, err := execute("apply"); err == nil || !strings.Contains(output, "does_not_exist") {
		t.Fatalf("broken release: %v\n%s", err, output)
	}
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM public.saxbase_releases WHERE version='1.1.3'", 0)
	writeFixtureFile(t, filepath.Join(objectDir, "views/broken.sql"), "CREATE OR REPLACE VIEW public.saxbase_broken AS SELECT 1 AS value;\n")
	run("release", "create", "1.1.3")
	run("apply")
	assertPostgresCount(t, ctx, db, "SELECT value FROM public.saxbase_broken", 1)
	run("rollback", "1.1.0")
	assertPostgresCount(t, ctx, db, "SELECT COUNT(*) FROM pg_views WHERE schemaname='public' AND viewname='saxbase_broken'", 0)
}

func writePostgresFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"database/migrations/00001_create_customers.sql": `-- +goose Up
CREATE TABLE public.customers (
    id integer GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    name text NOT NULL
);
INSERT INTO public.customers(name) VALUES ('Ada');
-- +goose Down
DROP TABLE public.customers;
`,
		"database/migrations/00002_add_nickname.sql": `-- +goose Up
ALTER TABLE public.customers ADD COLUMN nickname text;
-- +goose Down
ALTER TABLE public.customers DROP COLUMN nickname;
`,
		"database/objects/views/customers.sql": `CREATE OR REPLACE VIEW public.saxbase_customers AS
SELECT id, name FROM public.customers;
`,
		"database/objects/functions/customer_count.sql": `CREATE OR REPLACE FUNCTION public.saxbase_customer_count()
RETURNS bigint
LANGUAGE sql
AS $function$
    SELECT COUNT(*) FROM public.customers;
$function$;
`,
		"database/objects/procedures/add_customer.sql": `CREATE OR REPLACE PROCEDURE public.saxbase_add_customer(IN customer_name text)
LANGUAGE sql
AS $procedure$
    INSERT INTO public.customers(name) VALUES (customer_name);
$procedure$;
`,
	}
	for path, content := range files {
		writeFixtureFile(t, filepath.Join(root, path), content)
	}
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertPostgresCount(t *testing.T, ctx context.Context, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
		t.Fatalf("%s: got=%d want=%d err=%v", query, got, want, err)
	}
}

func waitForPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := db.PingContext(pingCtx)
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("PostgreSQL did not become ready: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

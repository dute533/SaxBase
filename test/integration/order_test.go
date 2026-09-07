//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saxbase/internal/releases"
)

func TestSQLServerManifestOrder(t *testing.T) {
	ctx, db, workDir, execute, run := integrationDatabase(t)
	root := filepath.Join(workDir, "database/objects")
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"z_base.sql", "a_dependent.sql"} {
		data, err := os.ReadFile(filepath.Join("../../examples/sqlserver/ordering", name))
		if err != nil {
			t.Fatal(err)
		}
		write("views/"+name, string(data))
	}
	manifest := func(version, filename string, reverse bool) {
		t.Helper()
		commitFixture(t, workDir)
		files, err := releases.CommittedFiles(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		m, err := releases.New(version, files)
		if err != nil {
			t.Fatal(err)
		}
		var rest []releases.Object
		var base, dependent releases.Object
		for _, o := range m.Objects {
			switch o.Path {
			case "database/objects/views/z_base.sql":
				base = o
			case "database/objects/views/a_dependent.sql":
				dependent = o
			default:
				rest = append(rest, o)
			}
		}
		m.Objects = append([]releases.Object{base, dependent}, rest...)
		if reverse {
			m.Objects[0], m.Objects[1] = m.Objects[1], m.Objects[0]
		}
		if err := m.Write(filepath.Join(workDir, filename)); err != nil {
			t.Fatal(err)
		}
	}
	manifest("2", "ordered.json", false)
	plan := run("-manifest", "ordered.json", "plan")
	if strings.Index(plan, "views/z_base.sql") > strings.Index(plan, "views/a_dependent.sql") {
		t.Fatal(plan)
	}
	run("-manifest", "ordered.json", "apply")
	manifest("2", "reordered.json", true)
	for _, command := range [][]string{{"plan"}, {"apply"}} {
		args := append([]string{"-manifest", "reordered.json"}, command...)
		out, err := execute(args...)
		if err == nil || !strings.Contains(out, "immutable") {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	// Both objects must change in dependency order on update and rollback.
	write("views/z_base.sql", "CREATE OR ALTER VIEW dbo.ordered_base AS SELECT 2 AS replacement_value;")
	write("views/a_dependent.sql", "CREATE OR ALTER VIEW dbo.ordered_dependent AS SELECT replacement_value FROM dbo.ordered_base;")
	manifest("2.1", "updated.json", false)
	run("-manifest", "updated.json", "apply")
	var value int
	if err := db.QueryRowContext(ctx, "SELECT replacement_value FROM dbo.ordered_dependent").Scan(&value); err != nil || value != 2 {
		t.Fatalf("value=%d err=%v", value, err)
	}
	run("-manifest", "ordered.json", "-source-manifest", "updated.json", "rollback", "2")
	if err := db.QueryRowContext(ctx, "SELECT original_value FROM dbo.ordered_dependent").Scan(&value); err != nil || value != 1 {
		t.Fatalf("value=%d err=%v", value, err)
	}
}

func TestSQLServerNoSnapshotTable(t *testing.T) {
	ctx, db, _, _, run := integrationDatabase(t)
	run("apply")
	run("plan")
	run("release", "history")
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.tables WHERE name='saxbase_release_objects'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("snapshot table: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.tables WHERE name='saxbase_release_state'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("release-state table: %d %v", count, err)
	}
}

func TestSQLServerLegacyReleaseMetadata(t *testing.T) {
	ctx, db, _, execute, run := integrationDatabase(t)
	run("apply")
	if _, err := db.ExecContext(ctx, "ALTER TABLE dbo.saxbase_releases DROP COLUMN fingerprint"); err != nil {
		t.Fatal(err)
	}
	run("release", "show", "2")
	if out, err := execute("plan"); err == nil || !strings.Contains(out, "immutable") {
		t.Fatalf("legacy plan: %v %s", err, out)
	}
	var absent int
	if err := db.QueryRowContext(ctx, "SELECT CASE WHEN COL_LENGTH('dbo.saxbase_releases','fingerprint') IS NULL THEN 1 ELSE 0 END").Scan(&absent); err != nil || absent != 1 {
		t.Fatalf("read modified legacy metadata: %d %v", absent, err)
	}
	run("-manifest", "database/new.json", "release", "create", "2.1")
	run("-manifest", "database/new.json", "apply")
	if out, err := execute("-manifest", "database/release.json", "-source-manifest", "database/new.json", "rollback", "2"); err == nil || !strings.Contains(out, "fingerprint") {
		t.Fatalf("legacy rollback: %v %s", err, out)
	}
	if got := run("release", "current"); got != "2.1" {
		t.Fatal(got)
	}
}

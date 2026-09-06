//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saxbase/internal/objects"
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
		files, err := objects.Scan(root)
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
			case "views/z_base.sql":
				base = o
			case "views/a_dependent.sql":
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
	run("-manifest", "ordered.json", "deploy")
	var snapshot objects.Snapshot
	if err := json.Unmarshal([]byte(run("release", "show", "2")), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Objects[0].Path != "views/z_base.sql" || snapshot.Objects[1].Path != "views/a_dependent.sql" {
		t.Fatal(snapshot.Objects)
	}
	manifest("2", "reordered.json", true)
	for _, command := range [][]string{{"plan"}, {"deploy"}, {"objects", "apply"}} {
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
	run("-manifest", "updated.json", "objects", "apply")
	var value int
	if err := db.QueryRowContext(ctx, "SELECT replacement_value FROM dbo.ordered_dependent").Scan(&value); err != nil || value != 2 {
		t.Fatalf("value=%d err=%v", value, err)
	}
	run("release", "rollback", "2")
	if err := db.QueryRowContext(ctx, "SELECT original_value FROM dbo.ordered_dependent").Scan(&value); err != nil || value != 1 {
		t.Fatalf("value=%d err=%v", value, err)
	}
}

func TestSQLServerLegacySnapshotOrder(t *testing.T) {
	ctx, db, _, _, run := integrationDatabase(t)
	run("deploy")
	// Emulate metadata written before snapshot order was introduced.
	if _, err := db.ExecContext(ctx, "ALTER TABLE dbo.saxbase_release_objects DROP COLUMN deployment_order"); err != nil {
		t.Fatal(err)
	}
	run("plan")
	run("release", "show", "2")
	var absent int
	if err := db.QueryRowContext(ctx, "SELECT CASE WHEN COL_LENGTH('dbo.saxbase_release_objects','deployment_order') IS NULL THEN 1 ELSE 0 END").Scan(&absent); err != nil || absent != 1 {
		t.Fatalf("read upgraded metadata: %v", err)
	}
	run("deploy")
	run("plan")
}

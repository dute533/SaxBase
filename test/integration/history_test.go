//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saxbase/internal/releases"
)

func TestSQLServerManifestHistoryWorkflow(t *testing.T) {
	ctx, db, workDir, _, run := integrationDatabase(t)
	manifestPath := filepath.Join(workDir, "database", "release.json")
	viewPath := filepath.Join(workDir, "database", "objects", "views", "value.sql")
	writeView := func(value string) {
		t.Helper()
		sql := "CREATE OR ALTER VIEW dbo.saxbase_value AS SELECT " + value + " AS value;\n"
		if err := os.WriteFile(viewPath, []byte(sql), 0600); err != nil {
			t.Fatal(err)
		}
	}
	assertValue := func(want int) {
		t.Helper()
		var got int
		if err := db.QueryRowContext(ctx, "SELECT value FROM dbo.saxbase_value").Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("view value = %d, want %d", got, want)
		}
	}
	assertView := func(name string, want int) {
		t.Helper()
		var got int
		query := "SELECT COUNT(*) FROM sys.views WHERE object_id=OBJECT_ID(N'dbo." + name + "')"
		if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
			t.Fatalf("view %s count = %d, want %d: %v", name, got, want, err)
		}
	}

	// Append a delta before provisioning this fresh database. Apply must start
	// with the oldest release rather than trying to deploy the delta directly.
	writeView("2")
	run("release", "create", "2.1")
	history, err := releases.LoadAll(manifestPath)
	if err != nil || len(history) != 2 || len(history[1].Objects) != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if output := run("plan"); !strings.Contains(output, "-> 2") {
		t.Fatalf("fresh plan did not select release 2: %s", output)
	}
	run("apply")
	assertValue(1)
	if output := run("plan"); !strings.Contains(output, "2 -> 2.1") {
		t.Fatalf("next plan did not select release 2.1: %s", output)
	}
	run("apply")
	assertValue(2)
	if output := run("apply"); !strings.Contains(output, "unchanged") {
		t.Fatalf("repeat apply was not idempotent: %s", output)
	}

	// Forward deletion is derived from the preceding release, without a database
	// object-checksum table.
	extraPath := filepath.Join(workDir, "database", "objects", "views", "temporary.sql")
	if err := os.WriteFile(extraPath, []byte("CREATE VIEW dbo.saxbase_temporary AS SELECT 1 AS value;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("release", "create", "2.2")
	run("apply")
	assertView("saxbase_temporary", 1)
	if err := os.Remove(extraPath); err != nil {
		t.Fatal(err)
	}
	run("release", "create", "2.3")
	if output := run("apply"); !strings.Contains(output, "deleted") {
		t.Fatalf("deletion was not reported: %s", output)
	}
	assertView("saxbase_temporary", 0)

	// A newer, undeployed entry must not be mistaken for the rollback source.
	writeView("3")
	run("release", "create", "2.4")
	run("rollback", "2")
	assertValue(1)
}

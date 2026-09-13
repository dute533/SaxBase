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

	// Append a delta before provisioning this fresh database. Apply must start
	// with the oldest release rather than trying to deploy the delta directly.
	writeView("2")
	run("release", "create", "2.1")
	history, err := releases.LoadAll(manifestPath)
	if err != nil || len(history) != 2 || history[1].Parent != "2" || len(history[1].Objects) != 1 {
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

	// A newer, undeployed entry must not be mistaken for the rollback source.
	writeView("3")
	run("release", "create", "2.2")
	run("rollback", "2")
	assertValue(1)
}

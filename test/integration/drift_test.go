//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestSQLServerDrift(t *testing.T) {
	ctx, db, _, execute, run := integrationDatabase(t)
	run("apply")
	if output := run("drift"); output != "" {
		t.Fatalf("clean drift must be silent, got: %s", output)
	}

	// A direct object edit is drift.
	if _, err := db.ExecContext(ctx, "CREATE OR ALTER VIEW dbo.saxbase_value AS SELECT id AS value, N'x' AS extra FROM dbo.customers;"); err != nil {
		t.Fatal(err)
	}
	output, err := execute("drift")
	if err == nil || !strings.Contains(strings.Join(strings.Fields(output), " "), "drifted") || !strings.Contains(strings.Join(strings.Fields(output), " "), "drift detected") {
		t.Fatalf("object drift not detected: err=%v output=%s", err, output)
	}

	// A direct structural edit (table column) is drift too.
	if _, err := db.ExecContext(ctx, "ALTER TABLE dbo.customers ADD extra int NULL;"); err != nil {
		t.Fatal(err)
	}
	output, err = execute("drift")
	if err == nil || !strings.Contains(strings.Join(strings.Fields(output), " "), "Schema fingerprint: DRIFTED") {
		t.Fatalf("schema drift not detected: err=%v output=%s", err, output)
	}

	// Heal manually; drift must report clean again.
	if _, err := db.ExecContext(ctx, "ALTER TABLE dbo.customers DROP COLUMN extra;"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE OR ALTER VIEW dbo.saxbase_value AS SELECT id AS value FROM dbo.customers;"); err != nil {
		t.Fatal(err)
	}
	if output := run("drift"); output != "" {
		t.Fatalf("drift after manual heal must be silent, got: %s", output)
	}
}

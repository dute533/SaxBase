//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestSQLServerDrift(t *testing.T) {
	ctx, db, _, execute, run := integrationDatabase(t)
	run("apply")
	if output := strings.TrimSpace(run("drift")); output != "No drift detected." {
		t.Fatalf("clean drift: %s", output)
	}

	// A direct object edit is drift.
	if _, err := db.ExecContext(ctx, "CREATE OR ALTER VIEW dbo.saxbase_value AS SELECT id AS value, N'x' AS extra FROM dbo.customers;"); err != nil {
		t.Fatal(err)
	}
	output, err := execute("drift")
	squeezed := strings.Join(strings.Fields(output), " ")
	if err == nil || !strings.Contains(squeezed, "value.sql: drifted") || !strings.Contains(squeezed, "~ dbo.saxbase_value v definition changed") {
		t.Fatalf("object drift not detected: err=%v output=%s", err, output)
	}

	// A direct structural edit (table column) is drift too.
	if _, err := db.ExecContext(ctx, "ALTER TABLE dbo.customers ADD extra int NULL;"); err != nil {
		t.Fatal(err)
	}
	output, err = execute("drift")
	squeezed = strings.Join(strings.Fields(output), " ")
	if err == nil || !strings.Contains(squeezed, "Schema fingerprint: DRIFTED") || !strings.Contains(squeezed, "+ column dbo.customers.extra") {
		t.Fatalf("schema drift not detected: err=%v output=%s", err, output)
	}

	// Heal manually; drift must report clean again.
	if _, err := db.ExecContext(ctx, "ALTER TABLE dbo.customers DROP COLUMN extra;"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE OR ALTER VIEW dbo.saxbase_value AS SELECT id AS value FROM dbo.customers;"); err != nil {
		t.Fatal(err)
	}
	if output := strings.TrimSpace(run("drift")); output != "No drift detected." {
		t.Fatalf("drift after manual heal: %s", output)
	}
}

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

	// A hand-added table is not managed by the release: not drift.
	if _, err := db.ExecContext(ctx, "CREATE TABLE dbo.handmade(id int);"); err != nil {
		t.Fatal(err)
	}
	if output := strings.TrimSpace(run("drift")); output != "No drift detected." {
		t.Fatalf("hand-added table must not be drift: %s", output)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE dbo.handmade;"); err != nil {
		t.Fatal(err)
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

	// Correction re-applies the pinned object; drift must report clean again.
	run("drift", "correct")
	if output := strings.TrimSpace(run("drift")); output != "No drift detected." {
		t.Fatalf("drift after correct: %s", output)
	}

	// A structural modification of a managed table (column type) is drift too.
	if _, err := db.ExecContext(ctx, "ALTER TABLE dbo.customers ALTER COLUMN nickname nvarchar(200) NULL;"); err != nil {
		t.Fatal(err)
	}
	output, err = execute("drift")
	squeezed = strings.Join(strings.Fields(output), " ")
	if err == nil || !strings.Contains(squeezed, "Schema fingerprint: DRIFTED") || !strings.Contains(squeezed, "column dbo.customers.nickname") {
		t.Fatalf("schema drift not detected: err=%v output=%s", err, output)
	}

	// Heal manually; drift must report clean again.
	if _, err := db.ExecContext(ctx, "ALTER TABLE dbo.customers ALTER COLUMN nickname nvarchar(100) NULL;"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE OR ALTER VIEW dbo.saxbase_value AS SELECT id AS value FROM dbo.customers;"); err != nil {
		t.Fatal(err)
	}
	if output := strings.TrimSpace(run("drift")); output != "No drift detected." {
		t.Fatalf("drift after manual heal: %s", output)
	}
}

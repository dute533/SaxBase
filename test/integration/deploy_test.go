//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLServerDeploy(t *testing.T) {
	ctx, db, workDir, execute, run := integrationDatabase(t)
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(workDir, path), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	count := func(query string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s: got %d, want %d", query, got, want)
		}
	}
	fail := func(want string, args ...string) {
		t.Helper()
		out, err := execute(args...)
		if err == nil || !strings.Contains(out, want) {
			t.Fatalf("%v: error=%v output=%s", args, err, out)
		}
	}
	manifest := func(version string) {
		if err := os.Remove(filepath.Join(workDir, "database/target.json")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		run("-manifest", "database/target.json", "release", "create", version)
	}
	deploy := func() string { return run("-manifest", "database/target.json", "deploy") }
	write("database/migrations/00003_next.sql", "-- +goose Up\nCREATE TABLE dbo.next_table(id INT);\n-- +goose Down\nDROP TABLE dbo.next_table;")
	// Invalid local state must fail before even initializing Goose metadata.
	write("database/objects/views/extra.sql", "CREATE OR ALTER VIEW dbo.extra AS SELECT 1 AS value;")
	fail("manifest", "deploy")
	count("SELECT COUNT(*) FROM sys.tables WHERE is_ms_shipped=0", 0)
	manifest("2")
	deploy()
	if got := run("version"); got != "2" {
		t.Fatal(got)
	}
	count("SELECT COUNT(*) FROM sys.tables WHERE name='next_table'", 0)
	count("SELECT COUNT(*) FROM dbo.customers WHERE name='Ada' AND nickname IS NULL", 1)
	if got := run("release", "current"); got != "2" {
		t.Fatal(got)
	}
	if out := deploy(); strings.Count(out, "unchanged") != 4 {
		t.Fatal(out)
	}
	count("SELECT COUNT(*) FROM dbo.saxbase_releases", 1)
	// An object-only revision does not apply migration 3.
	write("database/objects/views/extra.sql", "CREATE OR ALTER VIEW dbo.extra AS SELECT 2 AS value;")
	manifest("2.1")
	results := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() { _, err := execute("-manifest", "database/target.json", "deploy"); results <- err }()
	}
	for i := 0; i < 3; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	count("SELECT COUNT(*) FROM dbo.saxbase_releases WHERE version='2.1'", 1)
	count("SELECT value FROM dbo.extra", 2)
	count("SELECT COUNT(*) FROM dbo.goose_db_version WHERE version_id=3", 0)
	// Conflicting immutable release and absent target both fail before Goose.
	write("database/objects/views/extra.sql", "CREATE OR ALTER VIEW dbo.extra AS SELECT 3 AS value;")
	manifest("2.1")
	fail("immutable", "-manifest", "database/target.json", "deploy")
	manifest("9")
	fail("no migration file", "-manifest", "database/target.json", "deploy")
	count("SELECT value FROM dbo.extra", 2)
	// Goose commits before objects. A failure rolls back all object changes,
	// records no release, invalidates current, and can be retried at schema 3.
	write("database/objects/zz_broken.sql", "THROW 51000, 'intentional deploy failure', 1;")
	manifest("3")
	fail("intentional deploy failure", "-manifest", "database/target.json", "deploy")
	if got := run("version"); got != "3" {
		t.Fatal(got)
	}
	count("SELECT value FROM dbo.extra", 2)
	count("SELECT COUNT(*) FROM dbo.saxbase_releases WHERE version='3'", 0)
	count("SELECT COUNT(*) FROM dbo.saxbase_release_state WHERE version IS NOT NULL", 0)
	if err := os.Remove(filepath.Join(workDir, "database/objects/zz_broken.sql")); err != nil {
		t.Fatal(err)
	}
	manifest("3")
	deploy()
	if got := run("release", "current"); got != "3" {
		t.Fatal(got)
	}
	count("SELECT value FROM dbo.extra", 3)
	// Structural failure leaves the old object state intact and releases the lock.
	write("database/migrations/00004_failure.sql", "-- +goose Up\nTHROW 51000, 'intentional migration failure', 1;\n-- +goose Down\nSELECT 1;")
	manifest("4")
	fail("intentional migration failure", "-manifest", "database/target.json", "deploy")
	count("SELECT COUNT(*) FROM dbo.saxbase_releases WHERE version='4'", 0)
	count("SELECT value FROM dbo.extra", 3)
	write("database/migrations/00004_failure.sql", "-- +goose Up\nCREATE TABLE dbo.recovered(id INT);\n-- +goose Down\nDROP TABLE dbo.recovered;")
	deploy()
	count("SELECT COUNT(*) FROM dbo.saxbase_releases WHERE version='4'", 1)
	manifest("3")
	fail("rollback", "-manifest", "database/target.json", "deploy")
}

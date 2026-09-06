//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"saxbase/internal/objects"
)

// integrationDatabase builds the CLI and creates an isolated example database.
// The supplied login must be able to create and drop databases on a test server.
func integrationDatabase(t *testing.T) (context.Context, *sql.DB, string, func(...string) (string, error), func(...string) string) {
	t.Helper()
	raw := os.Getenv("SAXBASE_TEST_SQLSERVER_DSN")
	if raw == "" {
		t.Fatal("set SAXBASE_TEST_SQLSERVER_DSN to a SQL Server test instance URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "sqlserver" || u.Host == "" {
		t.Fatal("SAXBASE_TEST_SQLSERVER_DSN must be a sqlserver:// URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	binary := filepath.Join(t.TempDir(), "saxbase")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	query := u.Query()
	query.Set("database", "master")
	u.RawQuery = query.Encode()
	admin, err := sql.Open("sqlserver", u.String())
	if err != nil {
		t.Fatal("open SQL Server connection failed")
	}
	t.Cleanup(func() { admin.Close() })
	waitForSQLServer(t, ctx, admin)

	// The name is generated internally and contains only letters, digits and '_'.
	name := fmt.Sprintf("saxbase_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, err := admin.ExecContext(cleanupCtx, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE ["+name+"];")
		if err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})
	query.Set("database", name)
	u.RawQuery = query.Encode()
	dsn := u.String()
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatal("open test database failed")
	}
	t.Cleanup(func() { db.Close() })

	// Use the documented example with the CLI's default database/ layout.
	// Copy it so change/failure tests never modify the example in the checkout.
	workDir := t.TempDir()
	exampleDir := filepath.Join("..", "..", "examples", "sqlserver", "database")
	if err := filepath.WalkDir(exampleDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(exampleDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(workDir, "database", rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	execute := func(command ...string) (string, error) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, command...)
		cmd.Dir = workDir
		// Keep developer Goose settings from changing this test's target.
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "GOOSE_") && !strings.HasPrefix(value, "SAXBASE_OBJECTS_DIR=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "GOOSE_DRIVER=mssql", "GOOSE_DBSTRING="+dsn)
		output, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	run := func(command ...string) string {
		t.Helper()
		output, err := execute(command...)
		if err != nil {
			t.Fatalf("saxbase %s: %v\n%s", command, err, output)
		}
		return output
	}
	return ctx, db, workDir, execute, run
}

func TestSQLServerMigration(t *testing.T) {
	ctx, db, workDir, execute, run := integrationDatabase(t)
	objectDir := filepath.Join(workDir, "database", "objects")
	assertVersion := func(want string) {
		t.Helper()
		if got := run("version"); got != want {
			t.Fatalf("version = %q, want %q", got, want)
		}
	}
	assertCount := func(query string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("query %q returned %d, want %d", query, got, want)
		}
	}
	assertStatus := func(first, second string) {
		t.Helper()
		got := strings.Fields(run("status"))
		want := strings.Fields("VERSION STATE FILE 1 " + first + " 00001_create_customers.sql 2 " + second + " 00002_add_nickname.sql")
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("status = %v, want %v", got, want)
		}
	}

	futureMigration := filepath.Join(workDir, "database/migrations/00099_future.sql")
	if err := os.WriteFile(futureMigration, []byte("-- +goose Up\nCREATE TABLE dbo.future_table(id INT);\n-- +goose Down\nDROP TABLE dbo.future_table;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	planOutput := strings.Join(strings.Fields(run("plan")), " ")
	for _, want := range []string{"Goose: 0 -> 2", "1 apply", "2 apply", "99 deferred", "Ready:"} {
		if !strings.Contains(planOutput, want) {
			t.Fatalf("plan missing %q: %s", want, planOutput)
		}
	}
	assertCount("SELECT COUNT(*) FROM sys.tables WHERE is_ms_shipped=0", 0)
	if err := os.Remove(futureMigration); err != nil {
		t.Fatal(err)
	}
	assertVersion("0")
	run("release", "history")
	assertCount("SELECT COUNT(*) FROM sys.tables WHERE object_id=OBJECT_ID(N'dbo.saxbase_releases')", 0)
	assertStatus("pending", "pending")
	run("up")
	assertVersion("2")
	assertStatus("applied", "applied")
	assertCount("SELECT COUNT(*) FROM sys.columns WHERE object_id = OBJECT_ID(N'dbo.customers') AND name = N'nickname' AND TYPE_NAME(user_type_id) = N'nvarchar' AND max_length = 200 AND is_nullable = 1", 1)
	assertCount("SELECT COUNT(*) FROM dbo.customers WHERE id = 1 AND name = N'Ada' AND nickname IS NULL", 1)
	assertCount("SELECT COUNT(*) FROM dbo.goose_db_version WHERE version_id IN (1, 2) AND is_applied = 1", 2)

	// The insert and CREATE TABLE would fail if Goose reran the first migration.
	run("up")
	assertVersion("2")
	assertCount("SELECT COUNT(*) FROM dbo.customers", 1)
	assertCount("SELECT COUNT(*) FROM dbo.goose_db_version WHERE version_id IN (1, 2) AND is_applied = 1", 2)

	// Object status is read-only, including before its metadata table exists.
	run("release", "validate")
	run("-manifest", "database/wrong-schema.json", "release", "create", "3")
	if output, err := execute("-manifest", "database/wrong-schema.json", "objects", "apply"); err == nil {
		t.Fatalf("schema mismatch accepted: %s", output)
	}
	if output := run("objects", "status"); strings.Count(output, "new") != 3 {
		t.Fatalf("initial objects: %s", output)
	}
	assertCount("SELECT COUNT(*) FROM sys.tables WHERE object_id=OBJECT_ID(N'dbo.saxbase_objects')", 0)
	if output := run("-manifest", "database/release.json", "objects", "apply"); strings.Count(output, "applied") != 3 {
		t.Fatalf("apply objects: %s", output)
	}
	assertCount("SELECT value FROM dbo.saxbase_value", 1)
	assertCount("EXEC dbo.saxbase_get_value", 1)
	assertCount("SELECT dbo.saxbase_function()", 1)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_objects", 3)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_releases WHERE version='2' AND schema_version=2 AND revision=0 AND object_count=3", 1)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_release_objects", 3)
	var originalSnapshot objects.Snapshot
	if err := json.Unmarshal([]byte(run("release", "show", "2")), &originalSnapshot); err != nil {
		t.Fatal(err)
	}
	if originalSnapshot.Version != "2" || len(originalSnapshot.Objects) != 3 {
		t.Fatalf("snapshot: %+v", originalSnapshot)
	}
	for _, object := range originalSnapshot.Objects {
		data, err := os.ReadFile(filepath.Join(objectDir, object.Path))
		if err != nil {
			t.Fatal(err)
		}
		if object.SQL != string(data) || object.Checksum != fmt.Sprintf("%x", sha256.Sum256(data)) {
			t.Fatalf("snapshot differs from file %s", object.Path)
		}
	}
	run("-manifest", "database/release.json", "objects", "apply")
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_releases", 1)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_release_objects", 3)
	assertVersion("2")
	var originalTime time.Time
	if err := db.QueryRowContext(ctx, "SELECT deployed_at FROM dbo.saxbase_objects WHERE path=N'views/value.sql'").Scan(&originalTime); err != nil {
		t.Fatal(err)
	}
	if output := run("objects", "apply"); strings.Count(output, "unchanged") != 3 {
		t.Fatalf("repeat objects: %s", output)
	}
	var unchangedTime time.Time
	if err := db.QueryRowContext(ctx, "SELECT deployed_at FROM dbo.saxbase_objects WHERE path=N'views/value.sql'").Scan(&unchangedTime); err != nil {
		t.Fatal(err)
	}
	if !originalTime.Equal(unchangedTime) {
		t.Fatal("unchanged view was redeployed")
	}
	updated := []byte("CREATE OR ALTER VIEW dbo.saxbase_value AS SELECT 2 AS value;\n")
	if err := os.WriteFile(filepath.Join(objectDir, "views/value.sql"), updated, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := execute("-manifest", "database/release.json", "objects", "apply"); err == nil {
		t.Fatalf("stale manifest accepted: %s", output)
	}
	if output, err := execute("plan"); err == nil || !strings.Contains(output, "checksum mismatch") {
		t.Fatalf("stale plan: %v %s", err, output)
	}
	assertCount("SELECT value FROM dbo.saxbase_value", 1)
	run("-manifest", "database/release-2.1.json", "release", "create", "2.1")
	run("-manifest", "database/release-2.1.json", "release", "validate")
	if output := run("objects", "status"); strings.Count(output, "changed") != 3 || strings.Count(output, "unchanged") != 2 {
		t.Fatalf("changed status: %s", output)
	}
	if output := run("-manifest", "database/release-2.1.json", "objects", "apply"); strings.Count(output, "applied") != 1 || strings.Count(output, "unchanged") != 2 {
		t.Fatalf("changed apply: %s", output)
	}
	assertCount("SELECT value FROM dbo.saxbase_value", 2)
	var storedChecksum string
	if err := db.QueryRowContext(ctx, "SELECT checksum FROM dbo.saxbase_objects WHERE path=N'views/value.sql'").Scan(&storedChecksum); err != nil {
		t.Fatal(err)
	}
	if storedChecksum != fmt.Sprintf("%x", sha256.Sum256(updated)) {
		t.Fatal("stored checksum does not match file bytes")
	}
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_releases WHERE version='2.1' AND schema_version=2 AND revision=1 AND object_count=3", 1)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_release_objects", 6)
	if output := run("-manifest", "database/release-2.1.json", "plan"); !strings.Contains(output, "Ready:") || strings.Count(output, "unchanged") != 3 {
		t.Fatalf("unchanged plan: %s", output)
	}
	if output := run("release", "history"); !strings.Contains(output, "2.1") {
		t.Fatalf("history: %s", output)
	}
	var retained objects.Snapshot
	if err := json.Unmarshal([]byte(run("release", "show", "2")), &retained); err != nil {
		t.Fatal(err)
	}
	for i := range originalSnapshot.Objects {
		if originalSnapshot.Objects[i] != retained.Objects[i] {
			t.Fatal("old release snapshot was overwritten")
		}
	}
	// Concurrent retries serialize and must not create duplicate release records.
	retries := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := execute("-manifest", "database/release-2.1.json", "objects", "apply")
			retries <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-retries; err != nil {
			t.Fatalf("concurrent release retry: %v", err)
		}
	}
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_releases", 2)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_release_objects", 6)

	// An error in a later batch must roll back earlier definitions AND metadata.
	if err := os.WriteFile(filepath.Join(objectDir, "views/value.sql"), []byte("CREATE OR ALTER VIEW dbo.saxbase_value AS SELECT 3 AS value;"), 0600); err != nil {
		t.Fatal(err)
	}
	run("-manifest", "database/conflicting-2.1.json", "release", "create", "2.1")
	if output, err := execute("-manifest", "database/conflicting-2.1.json", "plan"); err == nil || !strings.Contains(output, "immutable") {
		t.Fatalf("immutable plan: %v %s", err, output)
	}
	if output, err := execute("-manifest", "database/conflicting-2.1.json", "objects", "apply"); err == nil || !strings.Contains(output, "immutable") {
		t.Fatalf("release identity conflict: %v %s", err, output)
	}
	assertCount("SELECT value FROM dbo.saxbase_value", 2)
	broken := filepath.Join(objectDir, "zz_broken.sql")
	if err := os.WriteFile(broken, []byte("CREATE OR ALTER VIEW dbo.saxbase_broken AS SELECT missing FROM dbo.saxbase_nonexistent;"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := execute("objects", "apply"); err == nil {
		t.Fatalf("invalid object succeeded: %s", output)
	}
	run("-manifest", "database/failed-2.2.json", "release", "create", "2.2")
	if output, err := execute("-manifest", "database/failed-2.2.json", "objects", "apply"); err == nil {
		t.Fatalf("invalid release succeeded: %s", output)
	}
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_releases", 2)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_release_objects", 6)
	assertCount("SELECT value FROM dbo.saxbase_value", 2)
	var afterFailure string
	if err := db.QueryRowContext(ctx, "SELECT checksum FROM dbo.saxbase_objects WHERE path=N'views/value.sql'").Scan(&afterFailure); err != nil {
		t.Fatal(err)
	}
	if afterFailure != storedChecksum {
		t.Fatal("failed transaction changed checksum")
	}
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_objects", 3)
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(objectDir, "views/value.sql")); err != nil {
		t.Fatal(err)
	}
	if output := run("objects", "status"); !strings.Contains(output, "missing") {
		t.Fatalf("missing status: %s", output)
	}
	run("-manifest", "database/incomplete-2.2.json", "release", "create", "2.2")
	if output, err := execute("-manifest", "database/incomplete-2.2.json", "objects", "apply"); err == nil || !strings.Contains(output, "omits tracked object") {
		t.Fatalf("incomplete release accepted: %v %s", err, output)
	}
	run("objects", "apply")
	assertCount("SELECT value FROM dbo.saxbase_value", 2)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_objects", 3)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_releases", 2)

	// Rollback uses stored SQL even when local definitions were edited or removed.
	if _, err := db.ExecContext(ctx, "CREATE ROLE saxbase_reader; GRANT SELECT ON dbo.saxbase_value TO saxbase_reader;"); err != nil {
		t.Fatal(err)
	}
	run("release", "rollback", "2")
	assertCount("SELECT value FROM dbo.saxbase_value", 1)
	assertCount("SELECT COUNT(*) FROM sys.database_permissions WHERE major_id=OBJECT_ID(N'dbo.saxbase_value') AND grantee_principal_id=DATABASE_PRINCIPAL_ID(N'saxbase_reader') AND permission_name='SELECT'", 1)
	if got := run("release", "current"); got != "2" {
		t.Fatalf("current release: %s", got)
	}
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_releases", 2)
	for _, object := range retained.Objects {
		if err := os.WriteFile(filepath.Join(objectDir, object.Path), []byte(object.SQL), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(objectDir, "views/value.sql"), updated, 0600); err != nil {
		t.Fatal(err)
	}
	extraPath := filepath.Join(objectDir, "zz_later.sql")
	if err := os.WriteFile(extraPath, []byte("CREATE VIEW dbo.saxbase_later AS SELECT value FROM dbo.saxbase_value;"), 0600); err != nil {
		t.Fatal(err)
	}
	run("-manifest", "database/release-2.2.json", "release", "create", "2.2")
	run("-manifest", "database/release-2.2.json", "objects", "apply")
	assertCount("SELECT value FROM dbo.saxbase_later", 2)
	// A historical plain CREATE definition can be restored over an existing view.
	run("release", "rollback", "2.2")
	assertCount("SELECT value FROM dbo.saxbase_later", 2)
	run("release", "rollback", "2")
	assertCount("SELECT COUNT(*) FROM sys.views WHERE object_id=OBJECT_ID(N'dbo.saxbase_later')", 0)
	assertCount("SELECT value FROM dbo.saxbase_value", 1)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_objects", 3)
	run("-manifest", "database/release-2.2.json", "objects", "apply")

	// A newer structural release is rolled back through Goose, with a failed
	// Down migration first to prove progress survives and retries can finish.
	noteMigration, err := os.ReadFile("../../examples/sqlserver/rollback/00003_add_customer_note.sql")
	if err != nil {
		t.Fatal(err)
	}
	migrationPath := filepath.Join(workDir, "database/migrations/00003_add_customer_note.sql")
	brokenMigration := strings.Replace(string(noteMigration), "ALTER TABLE dbo.customers DROP COLUMN rollback_note;", "THROW 51000, 'intentional rollback test failure', 1;", 1)
	if err := os.WriteFile(migrationPath, []byte(brokenMigration), 0600); err != nil {
		t.Fatal(err)
	}
	run("up")
	if err := os.WriteFile(filepath.Join(objectDir, "views/value.sql"), []byte("CREATE OR ALTER VIEW dbo.saxbase_value AS SELECT id+2 AS value,rollback_note FROM dbo.customers;"), 0600); err != nil {
		t.Fatal(err)
	}
	run("-manifest", "database/release-3.json", "release", "create", "3")
	run("-manifest", "database/release-3.json", "objects", "apply")
	if output, err := execute("release", "rollback", "99"); err == nil {
		t.Fatalf("unknown release accepted: %s", output)
	}
	if err := os.Rename(migrationPath, migrationPath+".disabled"); err != nil {
		t.Fatal(err)
	}
	if output, err := execute("release", "rollback", "2.1"); err == nil || !strings.Contains(output, "migration file") {
		t.Fatalf("missing migration preflight: %v %s", err, output)
	}
	assertCount("SELECT COUNT(*) FROM sys.views WHERE object_id=OBJECT_ID(N'dbo.saxbase_later')", 1)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_rollbacks WHERE status <> 'completed'", 0)
	if err := os.Rename(migrationPath+".disabled", migrationPath); err != nil {
		t.Fatal(err)
	}
	if output, err := execute("release", "rollback", "2.1"); err == nil || !strings.Contains(output, "intentional rollback test failure") {
		t.Fatalf("expected Goose rollback failure: %v %s", err, output)
	}
	assertVersion("3")
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_rollbacks WHERE status='failed' AND phase='objects_removed'", 1)
	for _, args := range [][]string{{"up"}, {"down"}, {"objects", "apply"}, {"release", "current"}, {"-manifest", "database/release-3.json", "plan"}, {"-manifest", "database/release-3.json", "deploy"}} {
		if output, err := execute(args...); err == nil || !strings.Contains(output, "incomplete") {
			t.Fatalf("pending rollback did not block %v: %v %s", args, err, output)
		}
	}
	if output := run("release", "rollbacks"); !strings.Contains(output, "failed") {
		t.Fatalf("missing failure history: %s", output)
	}
	if err := os.WriteFile(migrationPath, noteMigration, 0600); err != nil {
		t.Fatal(err)
	}
	run("release", "rollback", "2.1")
	assertVersion("2")
	assertCount("SELECT value FROM dbo.saxbase_value", 2)
	assertCount("SELECT COUNT(*) FROM sys.columns WHERE object_id=OBJECT_ID(N'dbo.customers') AND name=N'rollback_note'", 0)
	assertCount("SELECT COUNT(*) FROM sys.views WHERE object_id=OBJECT_ID(N'dbo.saxbase_later')", 0)
	if got := run("release", "current"); got != "2.1" {
		t.Fatalf("current release: %s", got)
	}
	if output, err := execute("release", "rollback", "3"); err == nil {
		t.Fatalf("forward rollback accepted: %s", output)
	}

	// Object-only rollback is atomic when a DDL error occurs during restore.
	if _, err := db.ExecContext(ctx, "CREATE TRIGGER saxbase_block_restore ON DATABASE FOR ALTER_VIEW AS THROW 51001, 'intentional restore test failure', 1;"); err != nil {
		t.Fatal(err)
	}
	if output, err := execute("release", "rollback", "2"); err == nil || !strings.Contains(output, "intentional restore test failure") {
		t.Fatalf("expected restore failure: %v %s", err, output)
	}
	assertCount("SELECT value FROM dbo.saxbase_value", 2)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_rollbacks WHERE status='failed' AND phase='started'", 1)
	if _, err := db.ExecContext(ctx, "DROP TRIGGER saxbase_block_restore ON DATABASE"); err != nil {
		t.Fatal(err)
	}
	run("release", "rollback", "2")
	assertCount("SELECT value FROM dbo.saxbase_value", 1)
	assertCount("EXEC dbo.saxbase_get_value", 1)
	assertCount("SELECT dbo.saxbase_function()", 1)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_rollbacks WHERE status <> 'completed'", 0)
	assertCount("SELECT COUNT(*) FROM dbo.saxbase_releases", 4)
	if got := run("release", "current"); got != "2" {
		t.Fatalf("current release: %s", got)
	}

	run("down")
	assertVersion("1")
	if output := run("status"); !strings.Contains(output, "00003_add_customer_note.sql") {
		t.Fatalf("missing schema migration status: %s", output)
	}
	assertCount("SELECT COUNT(*) FROM sys.columns WHERE object_id = OBJECT_ID(N'dbo.customers') AND name = N'nickname'", 0)
	assertCount("SELECT COUNT(*) FROM dbo.customers WHERE id = 1 AND name = N'Ada'", 1)
	run("down")
	assertVersion("0")
	if got := run("version"); got != "0" {
		t.Fatalf("version after final down: %s", got)
	}
	assertCount("SELECT COUNT(*) FROM sys.tables WHERE object_id = OBJECT_ID(N'dbo.customers')", 0)
}

func waitForSQLServer(t *testing.T, parent context.Context, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	for {
		pingCtx, pingCancel := context.WithTimeout(ctx, 3*time.Second)
		err := db.PingContext(pingCtx)
		pingCancel()
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("SQL Server did not become ready: %v", err)
		case <-time.After(time.Second):
		}
	}
}

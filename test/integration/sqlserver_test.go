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

	_ "github.com/microsoft/go-mssqldb"
)

// TestSQLServerMigration runs the actual CLI against a newly created database.
// The supplied login must be able to create and drop databases on a test server.
func TestSQLServerMigration(t *testing.T) {
	raw := os.Getenv("SAXBASE_TEST_SQLSERVER_DSN")
	if raw == "" {
		t.Fatal("set SAXBASE_TEST_SQLSERVER_DSN to a SQL Server test instance URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "sqlserver" || u.Host == "" {
		t.Fatal("SAXBASE_TEST_SQLSERVER_DSN must be a sqlserver:// URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
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

	run := func(command string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, "-dir", "testdata/migrations", command)
		// Keep developer Goose settings from changing this test's target.
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "GOOSE_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "GOOSE_DRIVER=mssql", "GOOSE_DBSTRING="+dsn)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("saxbase %s: %v\n%s", command, err, output)
		}
		return strings.TrimSpace(string(output))
	}
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

	assertVersion("0")
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

	run("down")
	assertVersion("1")
	assertStatus("applied", "pending")
	assertCount("SELECT COUNT(*) FROM sys.columns WHERE object_id = OBJECT_ID(N'dbo.customers') AND name = N'nickname'", 0)
	assertCount("SELECT COUNT(*) FROM dbo.customers WHERE id = 1 AND name = N'Ada'", 1)
	run("down")
	assertVersion("0")
	assertStatus("pending", "pending")
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

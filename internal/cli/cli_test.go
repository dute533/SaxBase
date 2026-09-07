package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

type fakeEngine struct {
	command  string
	ctx      context.Context
	closed   bool
	err      error
	closeErr error
}

func (f *fakeEngine) call(ctx context.Context, command string) error {
	f.ctx, f.command = ctx, command
	return f.err
}
func (f *fakeEngine) DownTo(ctx context.Context, _ int64) error   { return f.call(ctx, "down-to") }
func (f *fakeEngine) ValidateDownTo(context.Context, int64) error { return f.err }
func (f *fakeEngine) Inspect(context.Context) (migrations.Inspection, error) {
	return migrations.Inspection{Version: 30, Migrations: []migrations.Status{{Version: 30, Path: "00030.sql", State: "applied"}}}, f.err
}
func (f *fakeEngine) Version(ctx context.Context) (int64, error) {
	return 20260905123456, f.call(ctx, "version")
}
func (f *fakeEngine) Status(ctx context.Context) ([]migrations.Status, error) {
	return []migrations.Status{{Version: 30, State: "applied", Path: "00030_schema.sql"}, {Version: 31, State: "pending", Path: "00031_next.sql"}}, f.call(ctx, "status")
}
func (f *fakeEngine) Close() error { f.closed = true; return f.closeErr }

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestUnifiedStatus(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	goose, db := &fakeEngine{}, &fakeObjects{}
	var out bytes.Buffer
	err := run(context.Background(), []string{"-objects-dir", dir, "status"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return goose, nil },
		func(string) (objects.Engine, error) { return db, nil })
	if err != nil || !goose.closed || !db.closed || db.command != "status" {
		t.Fatalf("status: %v goose=%+v db=%+v", err, goose, db)
	}
	for _, value := range []string{"Goose version:", "Release:", "MIGRATION", "OBJECT", "view.sql"} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("status output missing %q: %s", value, out.String())
		}
	}
}

func TestConfigurationPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want migrations.Config
	}{
		{"environment", []string{"status"}, migrations.Config{Driver: "sqlserver", DSN: "env-dsn", Dir: "env-dir"}},
		{"arguments", []string{"-dir", "custom", "status"}, migrations.Config{Driver: "sqlserver", DSN: "env-dsn", Dir: "custom"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			err := run(context.Background(), append([]string{"-objects-dir", dir}, tc.args...), env(map[string]string{"GOOSE_DRIVER": "sqlserver", "GOOSE_DBSTRING": "env-dsn", "GOOSE_MIGRATION_DIR": "env-dir"}), &bytes.Buffer{}, func(cfg migrations.Config) (migrations.Engine, error) {
				if cfg != tc.want {
					t.Fatalf("config = %+v, want %+v", cfg, tc.want)
				}
				return &fakeEngine{}, nil
			}, func(string) (objects.Engine, error) { return &fakeObjects{}, nil })
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidationBeforeOpeningDatabase(t *testing.T) {
	for _, args := range [][]string{{"deploy"}, {"migration", "status"}, {"migration", "version"}, {"objects", "apply"}, {"mssql", "dsn", "apply"}, {"-unknown"}, {"-dir"}, {"migration", "up", "extra"}, {"migration", "up", "-dir", "custom"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := Run(context.Background(), args, env(nil), &bytes.Buffer{}, func(migrations.Config) (migrations.Engine, error) {
				t.Fatal("opened database for invalid input")
				return nil, nil
			})
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestHelpWithoutDatabase(t *testing.T) {
	for _, args := range [][]string{nil, {"-h"}, {"--help"}} {
		var out bytes.Buffer
		err := Run(context.Background(), args, env(nil), &out, func(migrations.Config) (migrations.Engine, error) {
			t.Fatal("opened database for help")
			return nil, nil
		})
		if err != nil || !strings.Contains(out.String(), "SaxBase") {
			t.Fatalf("help: %q, %v", out.String(), err)
		}
	}
}

func TestErrorsAndCleanup(t *testing.T) {
	failure := errors.New("migration failed")
	closeFailure := errors.New("close failed")
	dir := t.TempDir()
	f := &fakeEngine{err: failure, closeErr: closeFailure}
	err := run(context.Background(), []string{"-objects-dir", dir, "status"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &bytes.Buffer{},
		func(migrations.Config) (migrations.Engine, error) { return f, nil },
		func(string) (objects.Engine, error) { return &fakeObjects{}, nil })
	if !errors.Is(err, failure) || !errors.Is(err, closeFailure) || !f.closed {
		t.Fatalf("error %v, closed %v", err, f.closed)
	}
}

func (f *fakeEngine) UpTo(ctx context.Context, version int64) error { return f.call(ctx, "up-to") }

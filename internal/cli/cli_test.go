package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"saxbase/internal/migrations"
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
func (f *fakeEngine) Up(ctx context.Context) error                { return f.call(ctx, "up") }
func (f *fakeEngine) Down(ctx context.Context) error              { return f.call(ctx, "down") }
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

func TestCommands(t *testing.T) {
	for _, command := range []string{"up", "down", "status", "version"} {
		t.Run(command, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fakeEngine{}
			var out bytes.Buffer
			err := Run(ctx, []string{command}, env(map[string]string{"GOOSE_DBSTRING": "test-dsn"}), &out, func(cfg migrations.Config) (migrations.Engine, error) {
				want := migrations.Config{Driver: "mssql", DSN: "test-dsn", Dir: "database/migrations"}
				if cfg != want {
					t.Fatalf("config = %+v, want %+v", cfg, want)
				}
				return f, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if f.command != command || f.ctx != ctx || !f.closed {
				t.Fatalf("incorrect dispatch or cleanup: %+v", f)
			}
			if command == "version" && out.String() != "20260905123456\n" {
				t.Fatalf("version: %q", out.String())
			}
			if command == "status" {
				want := strings.Fields("VERSION STATE FILE 30 applied 00030_schema.sql 31 pending 00031_next.sql")
				if !reflect.DeepEqual(strings.Fields(out.String()), want) {
					t.Fatalf("status: %q", out.String())
				}
			}
		})
	}
}

func TestConfigurationPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want migrations.Config
	}{
		{"environment", []string{"up"}, migrations.Config{Driver: "sqlserver", DSN: "env-dsn", Dir: "env-dir"}},
		{"arguments", []string{"-dir", "custom", "mssql", "arg-dsn", "up"}, migrations.Config{Driver: "mssql", DSN: "arg-dsn", Dir: "custom"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Run(context.Background(), tc.args, env(map[string]string{"GOOSE_DRIVER": "sqlserver", "GOOSE_DBSTRING": "env-dsn", "GOOSE_MIGRATION_DIR": "env-dir"}), &bytes.Buffer{}, func(cfg migrations.Config) (migrations.Engine, error) {
				if cfg != tc.want {
					t.Fatalf("config = %+v, want %+v", cfg, tc.want)
				}
				return &fakeEngine{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidationBeforeOpeningDatabase(t *testing.T) {
	for _, args := range [][]string{{"deploy"}, {"up"}, {"-unknown"}, {"-dir"}, {"up", "extra"}, {"postgres", "dsn", "up"}, {"up", "-dir", "custom"}} {
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
	for _, command := range []string{"up", "down", "status", "version"} {
		f := &fakeEngine{err: failure, closeErr: closeFailure}
		var out bytes.Buffer
		err := Run(context.Background(), []string{command}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &out, func(migrations.Config) (migrations.Engine, error) { return f, nil })
		if !errors.Is(err, failure) || !errors.Is(err, closeFailure) || !f.closed {
			t.Fatalf("%s: error %v, closed %v", command, err, f.closed)
		}
		if out.Len() != 0 {
			t.Fatalf("output after failure: %q", out.String())
		}
	}
	err := Run(context.Background(), []string{"up"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &bytes.Buffer{}, func(migrations.Config) (migrations.Engine, error) { return nil, failure })
	if !errors.Is(err, failure) {
		t.Fatalf("open error: %v", err)
	}
}

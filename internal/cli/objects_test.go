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

type fakeObjects struct {
	command  string
	files    []objects.File
	closed   bool
	err      error
	schema   int64
	revision int64
}

func (f *fakeObjects) Apply(_ context.Context, files []objects.File) ([]objects.Status, error) {
	f.command = "apply"
	f.files = files
	return []objects.Status{{Path: files[0].Path, State: "applied", Checksum: files[0].Checksum}}, f.err
}
func (f *fakeObjects) Status(_ context.Context, files []objects.File) ([]objects.Status, error) {
	f.command = "status"
	f.files = files
	return []objects.Status{{Path: files[0].Path, State: "new", Checksum: files[0].Checksum}}, f.err
}
func (f *fakeObjects) Close() error { f.closed = true; return nil }
func (f *fakeObjects) Inspect(context.Context) (objects.Inspection, error) {
	return objects.Inspection{Current: "30"}, f.err
}
func (f *fakeObjects) Rollback(_ context.Context, target, source objects.Snapshot, _ migrations.Engine) (objects.Rollback, error) {
	f.command = "rollback"
	return objects.Rollback{SourceVersion: "30.2", TargetVersion: target.Version, Status: "completed"}, f.err
}
func (f *fakeObjects) Rollbacks(context.Context) ([]objects.Rollback, error) {
	f.command = "rollbacks"
	return []objects.Rollback{}, f.err
}
func (f *fakeObjects) Current(context.Context) (string, error) {
	f.command = "current"
	return "30.1", f.err
}

func (f *fakeObjects) ApplyRelease(ctx context.Context, files []objects.File, schema, revision int64) ([]objects.Status, error) {
	f.schema, f.revision = schema, revision
	rows, err := f.Apply(ctx, files)
	f.command = "apply-release"
	return rows, err
}
func (f *fakeObjects) History(context.Context) ([]objects.Release, error) {
	f.command = "history"
	return []objects.Release{{Version: "30.1", SchemaVersion: 30, Revision: 1, ObjectCount: 1}}, f.err
}
func (f *fakeObjects) Snapshot(_ context.Context, version string) (objects.Snapshot, error) {
	f.command = "show"
	return objects.Snapshot{Release: objects.Release{Version: version}, Objects: []objects.SnapshotObject{{Path: "view.sql", SQL: "SELECT 1;"}}}, f.err
}

func TestObjectCommands(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("CREATE OR ALTER VIEW dbo.v AS SELECT 1 AS n;"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"apply", "status"} {
		for _, positional := range []bool{false, true} {
			f := &fakeObjects{}
			var out bytes.Buffer
			args := []string{"objects", command}
			values := map[string]string{"GOOSE_DBSTRING": "dsn", "SAXBASE_OBJECTS_DIR": dir}
			if positional {
				args = []string{"-objects-dir", dir, "mssql", "dsn", "objects", command}
				values = map[string]string{"SAXBASE_OBJECTS_DIR": "missing"}
			}
			err := run(context.Background(), args, env(values), &out, func(migrations.Config) (migrations.Engine, error) {
				t.Fatal("opened Goose for objects")
				return nil, nil
			}, func(dsn string) (objects.Engine, error) {
				if dsn != "dsn" {
					t.Fatalf("dsn=%q", dsn)
				}
				return f, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if f.command != command || !f.closed || len(f.files) != 1 || !strings.Contains(out.String(), f.files[0].Checksum) {
				t.Fatalf("unexpected object dispatch/output: %+v, %s", f, out.String())
			}
		}
	}
	failure := errors.New("object failed")
	f := &fakeObjects{err: failure}
	err := run(context.Background(), []string{"objects", "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn", "SAXBASE_OBJECTS_DIR": dir}), &bytes.Buffer{}, nil, func(string) (objects.Engine, error) { return f, nil })
	if !errors.Is(err, failure) || !f.closed {
		t.Fatalf("error/cleanup: %v %+v", err, f)
	}
}

func TestInvalidObjectCommand(t *testing.T) {
	for _, args := range [][]string{{"objects", "drop"}, {"mssql", "dsn", "objects", "drop"}, {"objects", "apply"}} {
		err := run(context.Background(), args, env(nil), &bytes.Buffer{}, nil, func(string) (objects.Engine, error) { t.Fatal("opened database on invalid input"); return nil, nil })
		if err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func (f *fakeObjects) Deploy(ctx context.Context, files []objects.File, schema, revision int64, goose migrations.Engine, preflight func(context.Context) error) ([]objects.Status, error) {
	if err := preflight(ctx); err != nil {
		return nil, err
	}
	return f.ApplyRelease(ctx, files, schema, revision)
}

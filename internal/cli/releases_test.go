package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

func TestReleaseCommandsOffline(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "objects")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "a.sql")
	if err := os.WriteFile(file, []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := committedFixture(t, dir); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "release.json")
	args := []string{"-objects-dir", dir, "-manifest", manifest, "release"}
	for _, suffix := range [][]string{{"create", "30.1"}, {"validate"}} {
		var out bytes.Buffer
		if err := run(context.Background(), append(append([]string{}, args...), suffix...), env(nil), &out, nil, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "30.1") {
			t.Fatal(out.String())
		}
	}
	if err := os.WriteFile(file, []byte("SELECT 2;"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), append(args, "validate"), env(nil), &bytes.Buffer{}, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestManifestApplyGuards(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"30.1", "20260905123456.1"} {
		m, err := releases.New(version, files)
		if err != nil {
			t.Fatal(err)
		}
		filename := filepath.Join(t.TempDir(), "release.json")
		if err := m.Write(filename); err != nil {
			t.Fatal(err)
		}
		goose := &fakeEngine{}
		objectEngine := &fakeObjects{}
		opened := false
		err = run(context.Background(), []string{"-objects-dir", dir, "-manifest", filename, "objects", "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &bytes.Buffer{}, func(migrations.Config) (migrations.Engine, error) { return goose, nil }, func(string) (objects.Engine, error) { opened = true; return objectEngine, nil })
		if version == "30.1" {
			if err == nil || opened {
				t.Fatal("schema mismatch did not block apply")
			}
		} else {
			if err != nil || !objectEngine.closed || objectEngine.command != "apply-release" || objectEngine.schema != 20260905123456 || objectEngine.revision != 1 {
				t.Fatalf("matching release failed: %v", err)
			}
		}
		if !goose.closed {
			t.Fatal("Goose connection leaked")
		}
		if err := os.WriteFile(filename, []byte(`{"version":"30","objects":[{"path":"a.sql","commit":"bad"}]}`), 0600); err != nil {
			t.Fatal(err)
		}
		err = run(context.Background(), []string{"-objects-dir", dir, "-manifest", filename, "objects", "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &bytes.Buffer{}, nil, nil)
		if err == nil {
			t.Fatal("file mismatch accepted")
		}
	}
}

func TestReleaseDatabaseCommands(t *testing.T) {
	for _, args := range [][]string{{"release", "history"}, {"release", "rollbacks"}} {
		f := &fakeObjects{}
		var out bytes.Buffer
		err := run(context.Background(), args, env(map[string]string{"GOOSE_DBSTRING": "dsn", "GOOSE_MIGRATION_DIR": "missing", "SAXBASE_OBJECTS_DIR": "missing"}), &out, nil, func(string) (objects.Engine, error) { return f, nil })
		if err != nil || !f.closed || (args[1] == "history" && !strings.Contains(out.String(), "30.1")) {
			t.Fatalf("%v: %v, %s", args, err, out.String())
		}
	}
	for _, args := range [][]string{{"release", "show"}, {"release", "show", "30.0"}, {"release", "current"}, {"release", "history", "extra"}, {"release", "history"}} {
		err := run(context.Background(), args, env(nil), &bytes.Buffer{}, nil, func(string) (objects.Engine, error) { t.Fatal("opened database on invalid input"); return nil, nil })
		if err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

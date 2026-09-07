package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saxbase/internal/objects"
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

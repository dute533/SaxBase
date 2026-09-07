package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"saxbase/internal/releases"
	"strings"
	"testing"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

func TestRollbackCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("CREATE OR ALTER VIEW dbo.v AS SELECT 1 AS n;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := releases.New("30.1", files)
	source, _ := releases.New("30.2", files)
	targetPath, sourcePath := filepath.Join(dir, "target.json"), filepath.Join(dir, "source.json")
	if err := target.Write(targetPath); err != nil {
		t.Fatal(err)
	}
	if err := source.Write(sourcePath); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{nil, errors.New("rollback failed")} {
		goose := &fakeEngine{}
		store := &fakeObjects{err: failure}
		var out bytes.Buffer
		err := run(context.Background(), []string{"-manifest", targetPath, "-source-manifest", sourcePath, "release", "rollback", "30.1"}, env(map[string]string{"GOOSE_DBSTRING": "dsn", "SAXBASE_OBJECTS_DIR": "absent"}), &out, func(migrations.Config) (migrations.Engine, error) { return goose, nil }, func(string) (objects.Engine, error) { return store, nil })
		if !errors.Is(err, failure) || !goose.closed || !store.closed || store.command != "rollback" {
			t.Fatalf("result: %v %+v %+v", err, goose, store)
		}
		if failure == nil && !strings.Contains(out.String(), "30.2 to 30.1") {
			t.Fatal(out.String())
		}
		if failure != nil && out.Len() != 0 {
			t.Fatal("printed success after failure")
		}
	}
}

func TestRollbackValidationBeforeOpening(t *testing.T) {
	for _, args := range [][]string{{"release", "rollback"}, {"release", "rollback", "30.0"}, {"release", "rollback", "30"}, {"-manifest", "some.json", "release", "rollback", "30"}} {
		if err := run(context.Background(), args, env(nil), &bytes.Buffer{}, nil, nil); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

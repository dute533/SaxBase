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
	source, _ := releases.NewDelta("30.2", files, files)
	path := filepath.Join(dir, "release.json")
	if err := target.Write(path); err != nil {
		t.Fatal(err)
	}
	if err := releases.Append(path, source); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{nil, errors.New("rollback failed")} {
		goose := &fakeEngine{}
		store := &fakeObjects{rollbackErr: failure, current: "30.2", currentSet: true}
		var out bytes.Buffer
		err := run(context.Background(), []string{"-manifest", path, "rollback", "30.1"}, env(map[string]string{"GOOSE_DBSTRING": "dsn", "SAXBASE_OBJECTS_DIR": "absent"}), &out, func(migrations.Config) (migrations.Engine, error) { return goose, nil }, func(string) (objects.Engine, error) { return store, nil })
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

func TestRollbackSelectsCurrentReleaseFromHistory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("CREATE OR ALTER VIEW dbo.v AS SELECT 1 AS n;"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "release.json")
	base := releases.Manifest{Version: "30", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}}
	if err := base.Write(path); err != nil {
		t.Fatal(err)
	}
	for _, manifest := range []releases.Manifest{
		{Version: "30.1", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}},
		{Version: "30.2", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}},
	} {
		if err := releases.Append(path, manifest); err != nil {
			t.Fatal(err)
		}
	}
	for _, store := range []*fakeObjects{
		{current: "30.1", currentSet: true},
		{currentSet: true, rollbacks: []objects.Rollback{{SourceVersion: "30.1", TargetVersion: "30", Status: "failed"}}},
	} {
		var out bytes.Buffer
		err := run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "rollback", "30"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &out,
			func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
			func(string) (objects.Engine, error) { return store, nil })
		if err != nil || store.rollbackSource != "30.1" || store.rollbackTarget != "30" {
			t.Fatalf("rollback source=%q target=%q err=%v output=%s", store.rollbackSource, store.rollbackTarget, err, out.String())
		}
	}
}

func TestRollbackValidationBeforeOpening(t *testing.T) {
	for _, args := range [][]string{{"rollback"}, {"rollback", "30.0"}, {"rollback", "30"}, {"-manifest", "some.json", "rollback", "30"}} {
		if err := run(context.Background(), args, env(nil), &bytes.Buffer{}, nil, nil); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

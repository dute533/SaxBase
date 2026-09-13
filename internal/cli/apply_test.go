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

func TestApplyCLI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"30.2", "29"} {
		manifest, err := releases.New(version, files)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "release.json")
		if err := manifest.Write(path); err != nil {
			t.Fatal(err)
		}
		goose, db := &fakeEngine{}, &fakeObjects{}
		var out bytes.Buffer
		err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &out,
			func(migrations.Config) (migrations.Engine, error) { return goose, nil },
			func(string) (objects.Engine, error) { return db, nil })
		if !goose.closed || !db.closed {
			t.Fatal("engines were not closed")
		}
		if version == "29" {
			if err == nil || !strings.Contains(err.Error(), "apply blocked") || db.command != "inspect" {
				t.Fatalf("blocked apply: %v %+v", err, db)
			}
		} else if err != nil || db.schema != 30 || db.revision != 2 || !strings.Contains(out.String(), "Applied release 30.2") {
			t.Fatalf("apply: %v %+v %s", err, db, out.String())
		}
	}
}

func TestApplyAdvancesManifestHistoryAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "release.json")
	base := releases.Manifest{Version: "30", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}}
	if err := base.Write(path); err != nil {
		t.Fatal(err)
	}
	delta := releases.Manifest{Version: "30.1", Parent: "30", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}}
	if err := releases.Append(path, delta); err != nil {
		t.Fatal(err)
	}

	goose := &fakeEngine{}
	db := &fakeObjects{currentSet: true}
	apply := func(want string) {
		t.Helper()
		var out bytes.Buffer
		err := run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &out,
			func(migrations.Config) (migrations.Engine, error) { return goose, nil },
			func(string) (objects.Engine, error) { return db, nil })
		if err != nil || db.current != want || !strings.Contains(out.String(), "Applied release "+want) {
			t.Fatalf("apply %s: err=%v current=%q output=%s", want, err, db.current, out.String())
		}
	}
	apply("30")
	apply("30.1")
	apply("30.1")
}

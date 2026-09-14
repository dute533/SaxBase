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
		goose, db := &fakeEngine{}, &fakeObjects{currentSet: true}
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
		} else if err != nil || db.version != "30.2" || !strings.Contains(out.String(), "Applied release 30.2") {
			t.Fatalf("apply: %v %+v %s", err, db, out.String())
		}
	}
}

func TestApplyAdvancesEntireManifestHistoryAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "release.json")
	base := releases.Manifest{Version: "30", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}}
	if err := base.Write(path); err != nil {
		t.Fatal(err)
	}
	delta := releases.Manifest{Version: "30.1", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}}
	if err := releases.Append(path, delta); err != nil {
		t.Fatal(err)
	}

	apply := func(db *fakeObjects) string {
		t.Helper()
		var out bytes.Buffer
		err := run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &out,
			func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
			func(string) (objects.Engine, error) { return db, nil })
		if err != nil {
			t.Fatalf("apply: err=%v current=%q output=%s", err, db.current, out.String())
		}
		return out.String()
	}

	fresh := &fakeObjects{currentSet: true}
	output := apply(fresh)
	if fresh.current != "30.1" || strings.Join(fresh.appliedVersions, ",") != "30,30.1" ||
		!strings.Contains(output, "Applied release 30") || !strings.Contains(output, "Applied release 30.1") {
		t.Fatalf("fresh apply: current=%q applied=%v output=%s", fresh.current, fresh.appliedVersions, output)
	}

	current := &fakeObjects{current: "30", currentSet: true}
	output = apply(current)
	if current.current != "30.1" || strings.Join(current.appliedVersions, ",") != "30.1" || !strings.Contains(output, "Applied release 30.1") {
		t.Fatalf("partial apply: current=%q applied=%v output=%s", current.current, current.appliedVersions, output)
	}

	current.appliedVersions = nil
	output = apply(current)
	if strings.Join(current.appliedVersions, ",") != "30.1" || !strings.Contains(output, "Applied release 30.1") {
		t.Fatalf("idempotent apply: applied=%v output=%s", current.appliedVersions, output)
	}
}

func TestApplyStopsAtFailedRelease(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "release.json")
	base := releases.Manifest{Version: "30", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}}
	if err := base.Write(path); err != nil {
		t.Fatal(err)
	}
	if err := releases.Append(path, releases.Manifest{Version: "30.1", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}}); err != nil {
		t.Fatal(err)
	}

	failure := errors.New("object deployment failed")
	db := &fakeObjects{currentSet: true, applyErrAt: "30.1", applyErr: failure}
	var out bytes.Buffer
	err := run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(string) (objects.Engine, error) { return db, nil })
	if !errors.Is(err, failure) || db.current != "30" || strings.Join(db.appliedVersions, ",") != "30" {
		t.Fatalf("failed apply: err=%v current=%q applied=%v", err, db.current, db.appliedVersions)
	}
	if !strings.Contains(out.String(), "Applied release 30") || strings.Contains(out.String(), "Applied release 30.1") {
		t.Fatalf("failed apply output: %s", out.String())
	}
}

func TestForceApplyReappliesCurrentRelease(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "release.json")
	manifest := releases.Manifest{Version: "30", Objects: []releases.Object{{Path: "view.sql", Commit: "latest"}}}
	if err := manifest.Write(path); err != nil {
		t.Fatal(err)
	}
	db := &fakeObjects{}
	var out bytes.Buffer
	err := run(context.Background(), []string{"-f", "-manifest", path, "-objects-dir", dir, "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(string) (objects.Engine, error) { return db, nil })
	if err != nil || !db.force || !strings.Contains(out.String(), "Force-applied release 30") {
		t.Fatalf("force apply: err=%v force=%v output=%s", err, db.force, out.String())
	}
}

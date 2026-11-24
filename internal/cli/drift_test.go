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

func TestDriftCLI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "release.json")
	manifest := releases.Manifest{Version: "30.0.0", MigrationVersion: migrationPointer(30), Objects: []releases.Object{{Path: "view.sql", Commit: files[0].Commit}}}
	if err := manifest.Write(path); err != nil {
		t.Fatal(err)
	}

	db := &fakeObjects{current: "30.0.0", currentSet: true}
	oldReplay := replayDescribed
	replayDescribed = func(context.Context, migrations.Config, resolvedRelease, OpenFunc, OpenObjectsFunc) (string, error) {
		return "abc", nil // same as fakeObjects.Drift SchemaCurrent
	}
	defer func() { replayDescribed = oldReplay }()
	var out bytes.Buffer
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "drift"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	if err != nil || strings.TrimSpace(out.String()) != "No drift detected." {
		t.Fatalf("clean drift: err=%v output=%s", err, out.String())
	}

	// Drift against the release description (scratch replay) is reported too.
	replayDescribed = func(context.Context, migrations.Config, resolvedRelease, OpenFunc, OpenObjectsFunc) (string, error) {
		return "described", nil
	}
	db = &fakeObjects{current: "30.0.0", currentSet: true}
	out.Reset()
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "drift"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	squeezed := strings.Join(strings.Fields(out.String()), " ")
	if err == nil || !strings.Contains(squeezed, "Release-described schema (30.0.0): DRIFTED") || !strings.Contains(squeezed, "+ abc") || !strings.Contains(squeezed, "- described") {
		t.Fatalf("described drift: err=%v output=%s", err, out.String())
	}

	replayDescribed = oldReplay
	db = &fakeObjects{currentSet: true}
	out.Reset()
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "drift"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	if err == nil || !strings.Contains(err.Error(), "no release is recorded") {
		t.Fatalf("unversioned drift: err=%v output=%s", err, out.String())
	}
}

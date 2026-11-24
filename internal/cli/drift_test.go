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

	// Drift against the release description (scratch replay) is reported too:
	// the described column differs from the live one (modification).
	replayDescribed = func(context.Context, migrations.Config, resolvedRelease, OpenFunc, OpenObjectsFunc) (string, error) {
		return "column\tdbo.t.c\tint\t1\n", nil
	}
	db = &fakeObjects{current: "30.0.0", currentSet: true, schemaStored: "column\tdbo.t.c\tint\t2\n", schemaCurrent: "column\tdbo.t.c\tint\t2\n"}
	out.Reset()
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "drift"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	squeezed := strings.Join(strings.Fields(out.String()), " ")
	if err == nil || !strings.Contains(squeezed, "Release-described schema (30.0.0): DRIFTED") || !strings.Contains(squeezed, "+ column dbo.t.c int 2") || !strings.Contains(squeezed, "- column dbo.t.c int 1") {
		t.Fatalf("described drift: err=%v output=%s", err, out.String())
	}

	// A purely added object (not managed by the release) is not drift.
	db = &fakeObjects{current: "30.0.0", currentSet: true, schemaStored: "column\tdbo.t.c\tint\t2\ntable\tdbo.handmade\n", schemaCurrent: "column\tdbo.t.c\tint\t2\ntable\tdbo.handmade\n"}
	replayDescribed = func(context.Context, migrations.Config, resolvedRelease, OpenFunc, OpenObjectsFunc) (string, error) {
		return "column\tdbo.t.c\tint\t2\n", nil
	}
	out.Reset()
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "drift"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	if err != nil || strings.TrimSpace(out.String()) != "No drift detected." {
		t.Fatalf("hand-added object must not be drift: err=%v output=%s", err, out.String())
	}

	// A purely added live object on top of a modified described one: only the
	// modification is drift.
	db = &fakeObjects{current: "30.0.0", currentSet: true, schemaStored: "column\tdbo.t.c\tint\t2\ntable\tdbo.handmade\n", schemaCurrent: "column\tdbo.t.c\tint\t2\ntable\tdbo.handmade\n"}
	replayDescribed = func(context.Context, migrations.Config, resolvedRelease, OpenFunc, OpenObjectsFunc) (string, error) {
		return "column\tdbo.t.c\tint\t1\n", nil
	}
	out.Reset()
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "drift"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	squeezed = strings.Join(strings.Fields(out.String()), " ")
	if err == nil || !strings.Contains(squeezed, "Release-described schema (30.0.0): DRIFTED") || strings.Contains(squeezed, "handmade") {
		t.Fatalf("added object must be hidden, modification kept: err=%v output=%s", err, out.String())
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

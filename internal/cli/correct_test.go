package cli

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

func TestCorrectCLI(t *testing.T) {
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

	db := &fakeObjects{current: "30.0.0", currentSet: true, driftStates: map[string]string{"view.sql": "drifted"}}
	oldReplay := replayScratch
	replayScratch = func(context.Context, migrations.Config, resolvedRelease, OpenFunc, OpenObjectsFunc) (*sql.DB, string, func(), error) {
		return nil, "abc", func() {}, nil // described == live: no schema diff
	}
	defer func() { replayScratch = oldReplay }()
	var out bytes.Buffer
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "correct"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	squeezed := strings.Join(strings.Fields(out.String()), " ")
	if err != nil || !strings.Contains(squeezed, "correcting view.sql") || !strings.Contains(squeezed, "Corrected 1 item(s)") || len(db.corrected) != 1 {
		t.Fatalf("correct: err=%v output=%s corrected=%v", err, out.String(), db.corrected)
	}

	// Nothing drifted: no correction.
	db = &fakeObjects{current: "30.0.0", currentSet: true}
	out.Reset()
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "correct"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	if err != nil || strings.TrimSpace(out.String()) != "No drift detected." || len(db.corrected) != 0 {
		t.Fatalf("clean correct: err=%v output=%s corrected=%v", err, out.String(), db.corrected)
	}

	// Dry run prints the plan but applies nothing.
	db = &fakeObjects{current: "30.0.0", currentSet: true, driftStates: map[string]string{"view.sql": "drifted"}}
	out.Reset()
	err = run(context.Background(), []string{"-n", "-manifest", path, "-objects-dir", dir, "correct"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	squeezed = strings.Join(strings.Fields(out.String()), " ")
	if err != nil || !strings.Contains(squeezed, "Dry run:") || !strings.Contains(squeezed, "correcting view.sql") || !strings.Contains(squeezed, "OR ALTER 1;") || len(db.corrected) != 0 {
		t.Fatalf("dry run: err=%v output=%s corrected=%v", err, out.String(), db.corrected)
	}
}

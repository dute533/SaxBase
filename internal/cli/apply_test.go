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
	for _, version := range []string{"30.2.0", "29.0.0"} {
		migrationVersion := int64(30)
		if version == "29.0.0" {
			migrationVersion = 29
		}
		manifest, err := releases.New(version, migrationVersion, files)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "release.json")
		if err := manifest.Write(path); err != nil {
			t.Fatal(err)
		}
		goose, db := &fakeEngine{}, &fakeObjects{currentSet: true}
		var out bytes.Buffer
		err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "apply"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
			func(migrations.Config) (migrations.Engine, error) { return goose, nil },
			func(migrations.Config) (objects.Engine, error) { return db, nil })
		if !goose.closed || !db.closed {
			t.Fatal("engines were not closed")
		}
		if version == "29.0.0" {
			if err == nil || !strings.Contains(err.Error(), "apply blocked") || db.command != "inspect" {
				t.Fatalf("blocked apply: %v %+v", err, db)
			}
		} else if err != nil || db.version != "30.2.0" || !strings.Contains(out.String(), "Applied release 30.2.0") {
			t.Fatalf("apply: %v %+v %s", err, db, out.String())
		}
	}
}

func TestApplyAdvancesEntireManifestHistoryAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	commit := files[0].Commit
	path := filepath.Join(dir, "release.json")
	base := releases.Manifest{Version: "30.0.0", MigrationVersion: migrationPointer(30), Objects: []releases.Object{{Path: "view.sql", Commit: commit}}}
	if err := base.Write(path); err != nil {
		t.Fatal(err)
	}
	delta := releases.Manifest{Version: "30.1.0", MigrationVersion: migrationPointer(30), Objects: []releases.Object{{Path: "view.sql", Commit: commit}}}
	if err := releases.Append(path, delta); err != nil {
		t.Fatal(err)
	}

	apply := func(db *fakeObjects) string {
		t.Helper()
		var out bytes.Buffer
		err := run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "apply"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
			func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
			func(migrations.Config) (objects.Engine, error) { return db, nil })
		if err != nil {
			t.Fatalf("apply: err=%v current=%q output=%s", err, db.current, out.String())
		}
		return out.String()
	}

	fresh := &fakeObjects{currentSet: true}
	output := apply(fresh)
	if fresh.current != "30.1.0" || strings.Join(fresh.appliedVersions, ",") != "30.0.0,30.1.0" ||
		!strings.Contains(output, "Applied release 30.0.0") || !strings.Contains(output, "Applied release 30.1.0") {
		t.Fatalf("fresh apply: current=%q applied=%v output=%s", fresh.current, fresh.appliedVersions, output)
	}

	current := &fakeObjects{current: "30.0.0", currentSet: true}
	output = apply(current)
	if current.current != "30.1.0" || strings.Join(current.appliedVersions, ",") != "30.1.0" || !strings.Contains(output, "Applied release 30.1.0") {
		t.Fatalf("partial apply: current=%q applied=%v output=%s", current.current, current.appliedVersions, output)
	}

	current.appliedVersions = nil
	output = apply(current)
	if strings.Join(current.appliedVersions, ",") != "30.1.0" || !strings.Contains(output, "Release 30.1.0 is already applied") || strings.Contains(output, "Next:") {
		t.Fatalf("idempotent apply: applied=%v output=%s", current.appliedVersions, output)
	}
}

func TestApplyStopsAtFailedRelease(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	commit := files[0].Commit
	path := filepath.Join(dir, "release.json")
	base := releases.Manifest{Version: "30.0.0", MigrationVersion: migrationPointer(30), Objects: []releases.Object{{Path: "view.sql", Commit: commit}}}
	if err := base.Write(path); err != nil {
		t.Fatal(err)
	}
	if err := releases.Append(path, releases.Manifest{Version: "30.1.0", MigrationVersion: migrationPointer(30), Objects: []releases.Object{{Path: "view.sql", Commit: commit}}}); err != nil {
		t.Fatal(err)
	}

	failure := errors.New("object deployment failed")
	db := &fakeObjects{currentSet: true, applyErrAt: "30.1.0", applyErr: failure}
	var out bytes.Buffer
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "apply"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	if !errors.Is(err, failure) || db.current != "30.0.0" || strings.Join(db.appliedVersions, ",") != "30.0.0" {
		t.Fatalf("failed apply: err=%v current=%q applied=%v", err, db.current, db.appliedVersions)
	}
	if !strings.Contains(out.String(), "Applied release 30.0.0") || strings.Contains(out.String(), "Applied release 30.1.0") {
		t.Fatalf("failed apply output: %s", out.String())
	}
}

func TestApplyUpToVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	commit := files[0].Commit
	path := filepath.Join(dir, "release.json")
	base := releases.Manifest{Version: "30.0.0", MigrationVersion: migrationPointer(30), Objects: []releases.Object{{Path: "view.sql", Commit: commit}}}
	if err := base.Write(path); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"30.1.0", "30.2.0"} {
		if err := releases.Append(path, releases.Manifest{Version: version, MigrationVersion: migrationPointer(30), Objects: []releases.Object{{Path: "view.sql", Commit: commit}}}); err != nil {
			t.Fatal(err)
		}
	}

	apply := func(args ...string) (string, error) {
		t.Helper()
		db := &fakeObjects{currentSet: true}
		var out bytes.Buffer
		full := append([]string{"-manifest", path, "-objects-dir", dir}, args...)
		err := run(context.Background(), full, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
			func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
			func(migrations.Config) (objects.Engine, error) { return db, nil })
		return out.String(), err
	}

	// A version argument stops the chain after that release.
	output, err := apply("apply", "30.1.0")
	if err != nil {
		t.Fatalf("apply 30.1.0: %v", err)
	}
	if !strings.Contains(output, "Applied release 30.0.0") || !strings.Contains(output, "Applied release 30.1.0") ||
		strings.Contains(output, "Applied release 30.2.0") || !strings.Contains(output, "saxbase apply 30.2.0") {
		t.Fatalf("apply 30.1.0 output: %s", output)
	}

	// Without a version the rest of the chain applies.
	output, err = apply("apply")
	if err != nil || !strings.Contains(output, "Applied release 30.2.0") {
		t.Fatalf("apply rest: err=%v output=%s", err, output)
	}

	// An unknown version and a non-SemVer version are rejected.
	if _, err = apply("apply", "99.0.0"); err == nil || !strings.Contains(err.Error(), "not found in manifest history") {
		t.Fatalf("unknown version: %v", err)
	}
	if _, err = apply("apply", "not-a-version"); err == nil || !strings.Contains(err.Error(), "invalid release version") {
		t.Fatalf("invalid version: %v", err)
	}

	// A version older than the current release points at rollback.
	db := &fakeObjects{current: "30.2.0", currentSet: true}
	var out bytes.Buffer
	err = run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "apply", "30.1.0"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	if err == nil || !strings.Contains(err.Error(), "older than the current release") || !strings.Contains(err.Error(), "saxbase rollback 30.1.0") {
		t.Fatalf("older version: %v", err)
	}

	// The current version is rechecked idempotently, and -f still works with a version.
	output, err = apply("-f", "apply", "30.1.0")
	if err != nil || !strings.Contains(output, "Force-applied release 30.1.0") {
		t.Fatalf("force apply with version: err=%v output=%s", err, output)
	}
}

func TestForceApplyReappliesCurrentRelease(t *testing.T) {
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
	db := &fakeObjects{}
	var out bytes.Buffer
	err = run(context.Background(), []string{"-f", "-manifest", path, "-objects-dir", dir, "apply"}, env(map[string]string{"SAXBASE_DIALECT": "sqlserver", "SAXBASE_DSN": "dsn"}), &out,
		func(migrations.Config) (migrations.Engine, error) { return &fakeEngine{}, nil },
		func(migrations.Config) (objects.Engine, error) { return db, nil })
	if err != nil || !db.force || !strings.Contains(out.String(), "Force-applied release 30.0.0") {
		t.Fatalf("force apply: err=%v force=%v output=%s", err, db.force, out.String())
	}
}

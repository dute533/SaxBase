package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	for _, suffix := range [][]string{{"create", "30.1.0"}, {"validate"}} {
		var out bytes.Buffer
		if err := run(context.Background(), append(append([]string{}, args...), suffix...), env(nil), &out, nil, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "30.1.0") {
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

func TestReleaseCreateAppendsReleaseHistory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "objects")
	migrationDir := filepath.Join(root, "migrations")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(migrationDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"00001_first.sql", "00002_second.sql"} {
		if err := os.WriteFile(filepath.Join(migrationDir, name), []byte("-- +goose Up\nSELECT 1;\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := committedFixture(t, dir); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "release.json")
	args := []string{"-dir", migrationDir, "-objects-dir", dir, "-manifest", manifest, "release", "create"}
	if err := run(context.Background(), append(append([]string{}, args...), "1.0.0"), env(nil), &bytes.Buffer{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 2;"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", dir, "add", "view.sql")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "release 2")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	if err := run(context.Background(), append(append([]string{}, args...), "1.1.0"), env(nil), &bytes.Buffer{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	m, err := releases.Load(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.1.0" {
		t.Fatalf("manifest version = %q, want 1.1.0", m.Version)
	}
	history, err := releases.LoadAll(manifest)
	if err != nil || len(history) != 2 || history[0].Version != "1.0.0" || history[1].Version != "1.1.0" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if len(history[1].Objects) != 1 {
		t.Fatalf("release 1.1.0=%+v", history[1])
	}
	oldFiles, err := releases.ResolveStateVersion(context.Background(), manifest, dir, "1.0.0")
	if err != nil || len(oldFiles) != 1 || oldFiles[0].SQL != "SELECT 1;" {
		t.Fatalf("release 1 state=%+v err=%v", oldFiles, err)
	}
	currentFiles, err := releases.ResolveState(context.Background(), manifest, dir)
	if err != nil || len(currentFiles) != 1 || currentFiles[0].SQL != "SELECT 2;" {
		t.Fatalf("latest state=%+v err=%v", currentFiles, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 3;"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "-C", dir, "add", "view.sql")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "refresh release 2")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	var out bytes.Buffer
	if err := run(context.Background(), append(append([]string{}, args...), "1.1.0"), env(nil), &out, nil, nil); err != nil || !strings.Contains(out.String(), "Updated release 1.1.0") {
		t.Fatalf("refresh release: %v %s", err, out.String())
	}
	history, err = releases.LoadAll(manifest)
	if err != nil || len(history) != 2 || history[0].Version != "1.0.0" || history[1].Version != "1.1.0" {
		t.Fatalf("refreshed history=%+v err=%v", history, err)
	}
	currentFiles, err = releases.ResolveState(context.Background(), manifest, dir)
	if err != nil || len(currentFiles) != 1 || currentFiles[0].SQL != "SELECT 3;" {
		t.Fatalf("refreshed state=%+v err=%v", currentFiles, err)
	}
	var noChange bytes.Buffer
	if err := run(context.Background(), append(append([]string{}, args...), "1.1.0"), env(nil), &noChange, nil, nil); err != nil || !strings.Contains(noChange.String(), "No committed object changes found") {
		t.Fatalf("no-change release: %v %s", err, noChange.String())
	}
	history, err = releases.LoadAll(manifest)
	if err != nil || len(history) != 2 {
		t.Fatalf("no-change history=%+v err=%v", history, err)
	}
	if err := run(context.Background(), append(append([]string{}, args...), "1.0.0"), env(nil), &bytes.Buffer{}, nil, nil); err == nil {
		t.Fatal("duplicate release version was accepted")
	}
}

func TestReleaseCreateSemanticVersionsTrackMigrations(t *testing.T) {
	root := t.TempDir()
	objectDir := filepath.Join(root, "objects")
	migrationDir := filepath.Join(root, "migrations")
	if err := os.Mkdir(objectDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(migrationDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeMigration := func(version, name string) {
		t.Helper()
		filename := filepath.Join(migrationDir, version+"_"+name+".sql")
		if err := os.WriteFile(filename, []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeMigration("00001", "initial")
	view := filepath.Join(objectDir, "view.sql")
	if err := os.WriteFile(view, []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := committedFixture(t, objectDir); err != nil {
		t.Fatal(err)
	}
	commitView := func(message string) {
		t.Helper()
		for _, args := range [][]string{{"add", "view.sql"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", message}} {
			cmd := exec.Command("git", append([]string{"-C", objectDir}, args...)...)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git commit: %v %s", err, output)
			}
		}
	}
	manifestPath := filepath.Join(root, "release.json")
	create := func(version string) {
		t.Helper()
		args := []string{"-dir", migrationDir, "-objects-dir", objectDir, "-manifest", manifestPath, "release", "create", version}
		if err := run(context.Background(), args, env(nil), &bytes.Buffer{}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}

	create("1.0.0")
	if err := os.WriteFile(view, []byte("SELECT 2;"), 0600); err != nil {
		t.Fatal(err)
	}
	commitView("object patch")
	create("1.0.1")
	writeMigration("00002", "structural_change")
	create("1.1.0")

	history, err := releases.LoadAll(manifestPath)
	if err != nil || len(history) != 3 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	wantVersions := []string{"1.0.0", "1.0.1", "1.1.0"}
	wantMigrations := []int64{1, 1, 2}
	for i := range history {
		target, targetErr := history[i].TargetMigration()
		if targetErr != nil || history[i].MigrationVersion == nil || history[i].Version != wantVersions[i] || target != wantMigrations[i] {
			t.Fatalf("history[%d]=%+v target=%d err=%v", i, history[i], target, targetErr)
		}
	}
	args := []string{"-dir", migrationDir, "-objects-dir", objectDir, "-manifest", manifestPath, "release", "create"}
	err = run(context.Background(), args, env(nil), &bytes.Buffer{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "expected release create VERSION") {
		t.Fatalf("missing version was accepted: %v", err)
	}
}

func TestEffectiveCurrentRecoversNewestRecordedManifestRelease(t *testing.T) {
	history := []resolvedRelease{
		{manifest: releases.Manifest{Version: "2.0.0"}},
		{manifest: releases.Manifest{Version: "2.1.0"}},
		{manifest: releases.Manifest{Version: "3.0.0"}},
	}
	state := objects.Inspection{History: []objects.Release{{Version: "2.0.0"}, {Version: "2.1.0"}}}
	got, err := effectiveCurrent(history, state)
	if err != nil || got != "2.1.0" {
		t.Fatalf("current=%q err=%v", got, err)
	}
	state.Current = "2.0.0"
	if got, err = effectiveCurrent(history, state); err != nil || got != "2.0.0" {
		t.Fatalf("explicit current=%q err=%v", got, err)
	}
}

func TestEffectiveCurrentRejectsUnrelatedRecordedHistory(t *testing.T) {
	history := []resolvedRelease{{manifest: releases.Manifest{Version: "2.0.0"}}}
	state := objects.Inspection{History: []objects.Release{{Version: "1.0.0"}}}
	if _, err := effectiveCurrent(history, state); err == nil {
		t.Fatal("accepted unrelated database history")
	}
}

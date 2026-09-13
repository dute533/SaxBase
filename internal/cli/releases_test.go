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

func TestReleaseCreateAppendsReleaseHistory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "objects")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := committedFixture(t, dir); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "release.json")
	args := []string{"-objects-dir", dir, "-manifest", manifest, "release", "create"}
	if err := run(context.Background(), append(append([]string{}, args...), "1"), env(nil), &bytes.Buffer{}, nil, nil); err != nil {
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
	if err := run(context.Background(), append(append([]string{}, args...), "2"), env(nil), &bytes.Buffer{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	m, err := releases.Load(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "2" {
		t.Fatalf("manifest version = %q, want 2", m.Version)
	}
	history, err := releases.LoadAll(manifest)
	if err != nil || len(history) != 2 || history[0].Version != "1" || history[1].Version != "2" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if len(history[1].Objects) != 1 {
		t.Fatalf("release 2=%+v", history[1])
	}
	oldFiles, err := releases.ResolveStateVersion(context.Background(), manifest, dir, "1")
	if err != nil || len(oldFiles) != 1 || oldFiles[0].SQL != "SELECT 1;" {
		t.Fatalf("release 1 state=%+v err=%v", oldFiles, err)
	}
	currentFiles, err := releases.ResolveState(context.Background(), manifest, dir)
	if err != nil || len(currentFiles) != 1 || currentFiles[0].SQL != "SELECT 2;" {
		t.Fatalf("latest state=%+v err=%v", currentFiles, err)
	}

	if err := run(context.Background(), append(append([]string{}, args...), "2"), env(nil), &bytes.Buffer{}, nil, nil); err == nil {
		t.Fatal("same release version was accepted twice")
	}
}

func TestEffectiveCurrentRecoversNewestRecordedManifestRelease(t *testing.T) {
	history := []resolvedRelease{
		{manifest: releases.Manifest{Version: "2"}},
		{manifest: releases.Manifest{Version: "2.1"}},
		{manifest: releases.Manifest{Version: "3"}},
	}
	state := objects.Inspection{History: []objects.Release{{Version: "2"}, {Version: "2.1"}}}
	got, err := effectiveCurrent(history, state)
	if err != nil || got != "2.1" {
		t.Fatalf("current=%q err=%v", got, err)
	}
	state.Current = "2"
	if got, err = effectiveCurrent(history, state); err != nil || got != "2" {
		t.Fatalf("explicit current=%q err=%v", got, err)
	}
}

func TestEffectiveCurrentRejectsUnrelatedRecordedHistory(t *testing.T) {
	history := []resolvedRelease{{manifest: releases.Manifest{Version: "2"}}}
	state := objects.Inspection{History: []objects.Release{{Version: "1"}}}
	if _, err := effectiveCurrent(history, state); err == nil {
		t.Fatal("accepted unrelated database history")
	}
}

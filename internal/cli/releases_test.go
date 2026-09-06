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
	if err := run(context.Background(), append(args, "validate"), env(nil), &bytes.Buffer{}, nil, nil); err == nil {
		t.Fatal("accepted stale manifest")
	}
}

func TestManifestApplyGuards(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := objects.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"30.1", "20260905123456.1"} {
		m, err := releases.New(version, files)
		if err != nil {
			t.Fatal(err)
		}
		filename := filepath.Join(t.TempDir(), "release.json")
		if err := m.Write(filename); err != nil {
			t.Fatal(err)
		}
		goose := &fakeEngine{}
		objectEngine := &fakeObjects{}
		opened := false
		err = run(context.Background(), []string{"-objects-dir", dir, "-manifest", filename, "objects", "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &bytes.Buffer{}, func(migrations.Config) (migrations.Engine, error) { return goose, nil }, func(string) (objects.Engine, error) { opened = true; return objectEngine, nil })
		if version == "30.1" {
			if err == nil || opened {
				t.Fatal("schema mismatch did not block apply")
			}
		} else {
			if err != nil || !objectEngine.closed || objectEngine.command != "apply" {
				t.Fatalf("matching release failed: %v", err)
			}
		}
		if !goose.closed {
			t.Fatal("Goose connection leaked")
		}
		if err := os.WriteFile(filename, []byte(`{"format":1,"version":"30","objects":[]}`), 0600); err != nil {
			t.Fatal(err)
		}
		err = run(context.Background(), []string{"-objects-dir", dir, "-manifest", filename, "objects", "apply"}, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &bytes.Buffer{}, nil, nil)
		if err == nil {
			t.Fatal("file mismatch accepted")
		}
	}
}

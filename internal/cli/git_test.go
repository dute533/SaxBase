package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
	"strings"
	"testing"
)

func committedFixture(t *testing.T, dir string) ([]objects.File, error) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "--", "*.sql"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "SQL fixture", "--allow-empty"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
	}
	t.Chdir(dir)
	return releases.CommittedFiles(context.Background(), dir)
}

func TestGitResolutionFailsBeforeDatabaseOpen(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := releases.New("30", files)
	if err != nil {
		t.Fatal(err)
	}
	m.Objects = append(m.Objects, releases.Object{Path: "missing.sql", Commit: strings.Repeat("0", 40)})
	path := filepath.Join(dir, "release.json")
	if err := m.Write(path); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"deploy"}, {"plan"}, {"objects", "apply"}, {"objects", "status"}, {"release", "rollback", "30"}} {
		prefix := []string{"-manifest", path}
		if command[0] == "release" {
			prefix = append(prefix, "-source-manifest", path)
		}
		args := append(prefix, command...)
		err := run(context.Background(), args, env(map[string]string{"GOOSE_DBSTRING": "dsn"}), &bytes.Buffer{},
			func(migrations.Config) (migrations.Engine, error) {
				t.Fatal("opened Goose before resolving all SQL")
				return nil, nil
			},
			func(string) (objects.Engine, error) { t.Fatal("opened DB before resolving all SQL"); return nil, nil })
		if err == nil {
			t.Fatalf("accepted %v", command)
		}
	}
}

func TestReleaseCreateDeltaFromParentManifest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.sql", "b.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("SELECT 1;"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "release-1.json")
	base, err := releases.New("1", files)
	if err != nil {
		t.Fatal(err)
	}
	if err := base.Write(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.sql"), []byte("SELECT 2;"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.sql"), []byte("SELECT 3;"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "release 2"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v %s", err, out)
		}
	}
	target := filepath.Join(dir, "release-2.json")
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-objects-dir", ".", "-parent-manifest", parent, "-manifest", target, "release", "create", "2"}, env(nil), &out, nil, nil); err != nil {
		t.Fatal(err)
	}
	manifest, err := releases.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Parent != "release-1.json" || len(manifest.Objects) != 2 {
		t.Fatalf("delta=%+v", manifest)
	}
}

func TestReleaseCreateWithoutGitUsesLatest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "view.sql"), []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "release.json")
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-objects-dir", dir, "-manifest", manifestPath, "release", "create", "1"}, env(nil), &out, nil, nil); err != nil {
		t.Fatal(err)
	}
	m, err := releases.Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Objects) != 1 || m.Objects[0].Commit != "latest" {
		t.Fatalf("manifest=%+v", m)
	}
}

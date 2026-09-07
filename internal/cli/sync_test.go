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

func TestReleaseSyncCLIIsLocal(t *testing.T) {
	dir := t.TempDir()
	objectDir := filepath.Join(dir, "objects")
	if err := os.Mkdir(objectDir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(objectDir, "view.sql")
	if err := os.WriteFile(file, []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, _ := committedFixture(t, objectDir)
	m, _ := releases.New("2", files)
	path := filepath.Join(dir, "release.json")
	if err := m.Write(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("SELECT 2;"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objectDir, "new.sql"), []byte("SELECT 3;"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := committedFixture(t, objectDir); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	invoke := func(args ...string) error {
		out.Reset()
		return run(context.Background(), append([]string{"-manifest", path, "-objects-dir", objectDir}, args...), env(nil), &out,
			func(migrations.Config) (migrations.Engine, error) { t.Fatal("opened Goose"); return nil, nil },
			func(string) (objects.Engine, error) { t.Fatal("opened database"); return nil, nil })
	}
	if err := invoke("release", "sync", "2.1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Version: 2 -> 2.1", "Updated: view.sql", "Added: new.sql"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(out.String())
		}
	}
	if err := invoke("release", "validate"); err != nil {
		t.Fatal(err)
	}
	if err := invoke("release", "sync"); err != nil || !strings.Contains(out.String(), "already synchronized") {
		t.Fatalf("%v %s", err, out.String())
	}
	if err := invoke("release", "sync", "2.2", "extra"); err == nil {
		t.Fatal("extra argument accepted")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := invoke("release", "sync"); err == nil || !strings.Contains(err.Error(), "missing locally") {
		t.Fatal(err)
	}
}

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

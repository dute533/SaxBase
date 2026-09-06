package releases

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"saxbase/internal/objects"
)

func TestSyncPreservesOrderAndAppendsSorted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	old := []objects.File{{Path: "z.sql", Checksum: a}, {Path: "m.sql", Checksum: a}}
	m, err := New("30.9", old)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Write(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	files := []objects.File{{Path: "b.sql", Checksum: a}, {Path: "m.sql", Checksum: a}, {Path: "a.sql", Checksum: a}, {Path: "z.sql", Checksum: b}}
	result, err := Sync(path, files, "30.10")
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousVersion != "30.9" || result.Version != "30.10" || !reflect.DeepEqual(result.Updated, []string{"z.sql"}) || !reflect.DeepEqual(result.Added, []string{"a.sql", "b.sql"}) {
		t.Fatalf("%+v", result)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(files); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, o := range got.Objects {
		paths = append(paths, o.Path)
	}
	if !reflect.DeepEqual(paths, []string{"z.sql", "m.sql", "a.sql", "b.sql"}) {
		t.Fatal(paths)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %v %v", info, err)
	}
	before, _ := os.ReadFile(path)
	result, err = Sync(path, files, "")
	if err != nil || len(result.Added)+len(result.Updated) != 0 || result.Version != "30.10" {
		t.Fatalf("%+v %v", result, err)
	}
	after, _ := os.ReadFile(path)
	afterInfo, _ := os.Stat(path)
	if string(before) != string(after) || !os.SameFile(info, afterInfo) {
		t.Fatal("no-op rewrote manifest")
	}
	if _, err := Sync(path, files, "31"); err != nil {
		t.Fatal(err)
	}
	got, _ = Load(path)
	if got.Version != "31" {
		t.Fatal(got.Version)
	}
}

func TestSyncFailuresLeaveManifestUntouched(t *testing.T) {
	file := objects.File{Path: "view.sql", Checksum: strings.Repeat("a", 64)}
	for _, kind := range []string{"missing", "invalid-version", "duplicate", "malformed", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "release.json")
			m, _ := New("2", []objects.File{file})
			if err := m.Write(path); err != nil {
				t.Fatal(err)
			}
			files, version := []objects.File{file}, ""
			switch kind {
			case "missing":
				files = nil
			case "invalid-version":
				version = "2.0"
			case "duplicate":
				files = append(files, file)
			case "malformed":
				if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(dir, "link.json")
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			}
			before, _ := os.ReadFile(path)
			if _, err := Sync(path, files, version); err == nil {
				t.Fatal("expected error")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("failed sync changed manifest")
			}
			leftovers, _ := filepath.Glob(filepath.Join(dir, ".saxbase-sync-*"))
			if len(leftovers) != 0 {
				t.Fatal(leftovers)
			}
		})
	}
}

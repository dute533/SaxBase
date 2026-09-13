package objects

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScan(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "views", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	definitions := map[string]string{"views/nested/b.sql": "CREATE OR ALTER VIEW dbo.b AS SELECT 2 AS n;\r\n", "a.SQL": "CREATE OR ALTER VIEW dbo.a AS SELECT 1 AS n;\n", "notes.txt": "ignored"}
	for path, sql := range definitions {
		if err := os.WriteFile(filepath.Join(root, path), []byte(sql), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "a.SQL" || files[1].Path != "views/nested/b.sql" {
		t.Fatalf("unexpected scan: %+v", files)
	}
	for _, file := range files {
		if file.SQL != definitions[file.Path] || file.Checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(definitions[file.Path]))) {
			t.Fatalf("incorrect content/checksum: %+v", file)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.SQL"), []byte(definitions["a.SQL"]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed[0].Checksum == files[0].Checksum {
		t.Fatal("whitespace change did not change checksum")
	}
}

func TestScanInvalidInputs(t *testing.T) {
	root := t.TempDir()
	if _, err := Scan(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing directory accepted")
	}
	empty := filepath.Join(root, "empty.sql")
	if err := os.WriteFile(empty, []byte(" \n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(root); err == nil {
		t.Fatal("empty SQL accepted")
	}
	if _, err := Scan(empty); err == nil {
		t.Fatal("file used as root accepted")
	}
}

func TestScanRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "linked")); err != nil {
		t.Skip(err)
	}
	if _, err := Scan(root); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestCompare(t *testing.T) {
	files := []File{{Path: "a.sql", Checksum: "new"}, {Path: "b.sql", Checksum: "changed"}, {Path: "c.sql", Checksum: "same"}}
	baseline := []File{{Path: "b.sql", Checksum: "old"}, {Path: "c.sql", Checksum: "same"}, {Path: "d.sql", Checksum: "missing"}}
	want := []Status{{Path: "a.sql", State: "new", Checksum: "new"}, {Path: "b.sql", State: "changed", Checksum: "changed"}, {Path: "c.sql", State: "unchanged", Checksum: "same"}, {Path: "d.sql", State: "missing", Checksum: "missing"}}
	if got := Compare(files, baseline); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	deleted := File{Path: "b.sql", Checksum: "old", Delete: true}
	if got := Compare([]File{deleted}, baseline); got[0].State != "deleted" {
		t.Fatalf("deletion status: %+v", got)
	}
	if got := Compare([]File{deleted}, nil); got[0].State != "missing" {
		t.Fatalf("missing deletion status: %+v", got)
	}
}

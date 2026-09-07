package releases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"saxbase/internal/objects"
)

func TestVersions(t *testing.T) {
	for _, test := range []struct {
		value string
		want  Version
	}{{"0", Version{}}, {"30", Version{Schema: 30}}, {"30.10", Version{30, 10}}, {"9223372036854775807.1", Version{9223372036854775807, 1}}} {
		got, err := ParseVersion(test.value)
		if err != nil || got != test.want {
			t.Fatalf("%s: %+v %v", test.value, got, err)
		}
	}
	for _, value := range []string{"", "01", "30.0", "30.01", "30.1.2", "-1", "1e2", " 30", "30.", "9223372036854775808", "1.9223372036854775808"} {
		if _, err := ParseVersion(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestManifestRoundTripAndMismatch(t *testing.T) {
	files := []objects.File{{Path: "views/a.sql", Commit: strings.Repeat("a", 64)}}
	m, err := New("30.1", files)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "release.json")
	if err := m.Write(filename); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(filename); err == nil {
		t.Fatal("overwrote existing manifest")
	}
	loaded, err := Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Validate(files); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]objects.File{nil, {{Path: "views/a.sql", Commit: strings.Repeat("b", 64)}}, append(append([]objects.File{}, files...), objects.File{Path: "extra.sql", Commit: strings.Repeat("c", 64)})} {
		if err := loaded.Validate(bad); err == nil {
			t.Fatalf("accepted mismatch: %+v", bad)
		}
	}
}

func TestInvalidManifests(t *testing.T) {
	sum := strings.Repeat("a", 64)
	for _, body := range []string{
		`{"version":30,"objects":[]}`,
		`{"version":"30","objects":null}`,
		`{"version":"30","objects":[],"typo":true}`,
		`{"version":"30","objects":[]} {}`,
		`{"version":"30","objects":[{"path":"../a.sql","commit":"` + sum + `"}]}`,
		`{"version":"30","objects":[{"path":"a.sql","commit":"bad"}]}`,
		`{"version":"30","objects":[{"path":"a.sql","commit":"` + sum + `"},{"path":"a.sql","commit":"` + sum + `"}]}`,
	} {
		file := filepath.Join(t.TempDir(), "release.json")
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(file); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestNewDeltaContainsOnlyChanges(t *testing.T) {
	old := []objects.File{
		{Path: "a.sql", Commit: strings.Repeat("a", 40), SQL: "SELECT 1;", Checksum: strings.Repeat("1", 64)},
		{Path: "b.sql", Commit: strings.Repeat("b", 40), SQL: "SELECT 2;", Checksum: strings.Repeat("2", 64)},
	}
	current := []objects.File{
		{Path: "a.sql", Commit: strings.Repeat("c", 40), SQL: "SELECT 3;", Checksum: strings.Repeat("3", 64)},
		{Path: "c.sql", Commit: strings.Repeat("d", 40), SQL: "SELECT 4;", Checksum: strings.Repeat("4", 64)},
	}
	m, err := NewDelta("2.1", "release-2.json", current, old)
	if err != nil {
		t.Fatal(err)
	}
	if m.Parent != "release-2.json" || len(m.Objects) != 3 {
		t.Fatalf("manifest=%+v", m)
	}
	if m.Objects[0].Path != "a.sql" || m.Objects[1].Path != "c.sql" || !m.Objects[2].Delete || m.Objects[2].Path != "b.sql" {
		t.Fatalf("objects=%+v", m.Objects)
	}
}

func TestNewDeltaDetectsLatestContentChanges(t *testing.T) {
	old := []objects.File{{Path: "a.sql", Commit: "latest", SQL: "SELECT 1;", Checksum: "old"}}
	current := []objects.File{{Path: "a.sql", Commit: "latest", SQL: "SELECT 2;", Checksum: "new"}}
	m, err := NewDelta("2", "release-1.json", current, old)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Objects) != 1 || m.Objects[0].Path != "a.sql" {
		t.Fatalf("manifest=%+v", m)
	}
}

package releases

import (
	"encoding/json"
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
		`{"version":"30","objects":[]}`,
		`{"releases":[]}`,
		`{"releases":[{"version":"30","objects":null}]}`,
		`{"releases":[{"version":"30","objects":[],"typo":true}]}`,
		`{"releases":[{"version":"30","parent":"29","objects":[]}]}`,
		`{"releases":[{"version":"1","objects":[]},{"version":"2","objects":[]}]}`,
		`{"releases":[{"version":"30","objects":[]}]} {}`,
		`{"releases":[{"version":"30","objects":[{"path":"../a.sql","commit":"` + sum + `"}]}]}`,
		`{"releases":[{"version":"30","objects":[{"path":"a.sql","commit":"bad"}]}]}`,
		`{"releases":[{"version":"30","objects":[{"path":"a.sql","commit":"` + sum + `"},{"path":"a.sql","commit":"` + sum + `"}]}]}`,
		`{"releases":[{"version":"30","objects":[{"path":"a.sql","commit":"` + sum + `","delete":true}]}]}`,
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

func TestAppendStoresNewestReleaseFirst(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "release.json")
	if err := (Manifest{Version: "1", Objects: []Object{}}).Write(filename); err != nil {
		t.Fatal(err)
	}
	if err := Append(filename, Manifest{Version: "2", Objects: []Object{}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var stored manifestDocument
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Releases) != 2 || stored.Releases[0].Version != "2" || stored.Releases[1].Version != "1" {
		t.Fatalf("stored history=%+v", stored.Releases)
	}
	history, err := LoadAll(filename)
	if err != nil || history[0].Version != "1" || history[1].Version != "2" {
		t.Fatalf("chronological history=%+v err=%v", history, err)
	}
	replacement := Manifest{Version: "2", Objects: []Object{{Path: "a.sql", Commit: strings.Repeat("a", 40)}}}
	if err := ReplaceLatest(filename, replacement); err != nil {
		t.Fatal(err)
	}
	history, err = LoadAll(filename)
	if err != nil || len(history[1].Objects) != 1 || history[1].Objects[0].Path != "a.sql" {
		t.Fatalf("replaced history=%+v err=%v", history, err)
	}
	if err := ReplaceLatest(filename, Manifest{Version: "1", Objects: []Object{}}); err == nil {
		t.Fatal("replaced a non-latest release")
	}
}

func TestFirstReleaseCannotBeADeletion(t *testing.T) {
	m := Manifest{Version: "1", Objects: []Object{{Path: "a.sql", Commit: strings.Repeat("a", 40), Delete: true}}}
	if err := m.Write(filepath.Join(t.TempDir(), "release.json")); err == nil {
		t.Fatal("wrote deletion as the initial release")
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
	m, err := NewDelta("2.1", current, old)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Objects) != 3 {
		t.Fatalf("manifest=%+v", m)
	}
	if m.Objects[0].Path != "a.sql" || m.Objects[1].Path != "c.sql" || !m.Objects[2].Delete || m.Objects[2].Path != "b.sql" {
		t.Fatalf("objects=%+v", m.Objects)
	}
}

func TestNewDeltaDetectsLatestContentChanges(t *testing.T) {
	old := []objects.File{{Path: "a.sql", Commit: "latest", SQL: "SELECT 1;", Checksum: "old"}}
	current := []objects.File{{Path: "a.sql", Commit: "latest", SQL: "SELECT 2;", Checksum: "new"}}
	m, err := NewDelta("2", current, old)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Objects) != 1 || m.Objects[0].Path != "a.sql" {
		t.Fatalf("manifest=%+v", m)
	}
}

func TestApplyDeltaDoesNotMutateParent(t *testing.T) {
	parent := []objects.File{{Path: "a.sql", Checksum: "old-a"}, {Path: "b.sql", Checksum: "old-b"}}
	delta := []objects.File{{Path: "a.sql", Checksum: "new-a"}, {Path: "b.sql", Checksum: "old-b", Delete: true}, {Path: "c.sql", Checksum: "new-c"}}
	state := ApplyDelta(parent, delta)
	if len(state) != 2 || state[0].Path != "a.sql" || state[0].Checksum != "new-a" || state[1].Path != "c.sql" {
		t.Fatalf("state=%+v", state)
	}
	if len(parent) != 2 || parent[0].Checksum != "old-a" || parent[1].Path != "b.sql" {
		t.Fatalf("parent mutated: %+v", parent)
	}
}

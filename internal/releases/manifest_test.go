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
	files := []objects.File{{Path: "views/a.sql", Checksum: strings.Repeat("a", 64)}}
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
	for _, bad := range [][]objects.File{nil, {{Path: "views/a.sql", Checksum: strings.Repeat("b", 64)}}, append(append([]objects.File{}, files...), objects.File{Path: "extra.sql", Checksum: strings.Repeat("c", 64)})} {
		if err := loaded.Validate(bad); err == nil {
			t.Fatalf("accepted mismatch: %+v", bad)
		}
	}
}

func TestInvalidManifests(t *testing.T) {
	sum := strings.Repeat("a", 64)
	for _, body := range []string{
		`{"format":2,"version":"30","objects":[]}`,
		`{"format":1,"version":30,"objects":[]}`,
		`{"format":1,"version":"30","objects":null}`,
		`{"format":1,"version":"30","objects":[],"typo":true}`,
		`{"format":1,"version":"30","objects":[]} {}`,
		`{"format":1,"version":"30","objects":[{"path":"../a.sql","sha256":"` + sum + `"}]}`,
		`{"format":1,"version":"30","objects":[{"path":"a.sql","sha256":"bad"}]}`,
		`{"format":1,"version":"30","objects":[{"path":"a.sql","sha256":"` + sum + `"},{"path":"a.sql","sha256":"` + sum + `"}]}`,
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

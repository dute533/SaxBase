package releases

import (
	"testing"

	"saxbase/internal/objects"
)

func TestManifestOrder(t *testing.T) {
	files := []objects.File{{Path: "a.sql", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {Path: "z.sql", Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	m, err := New("2", files)
	if err != nil {
		t.Fatal(err)
	}
	m.Objects[0], m.Objects[1] = m.Objects[1], m.Objects[0]
	ordered, err := m.OrderedFiles(files)
	if err != nil || ordered[0].Path != "z.sql" || files[0].Path != "a.sql" {
		t.Fatalf("order=%v err=%v", ordered, err)
	}
	m.Objects[1] = m.Objects[0]
	if _, err := m.OrderedFiles(files); err == nil {
		t.Fatal("duplicate accepted")
	}
}

// Keep the snapshot identity order-sensitive even when definitions match.
func TestSnapshotOrderIdentity(t *testing.T) {
	files := []objects.File{{Path: "z.sql", SQL: "z", Checksum: "z"}, {Path: "a.sql", SQL: "a", Checksum: "a"}}
	s := objects.Snapshot{Release: objects.Release{ObjectCount: 2}, Objects: []objects.SnapshotObject{{Path: "z.sql", SQL: "z", Checksum: "z"}, {Path: "a.sql", SQL: "a", Checksum: "a"}}}
	if !matchesSnapshot(s, files) {
		t.Fatal("identical ordered snapshot rejected")
	}
	files[0], files[1] = files[1], files[0]
	if matchesSnapshot(s, files) {
		t.Fatal("reordered snapshot accepted")
	}
}

package releases

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitFixture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestResolveHistoricalCommitsInManifestOrder(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	write := func(path, sql string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(sql), 0600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func() string {
		gitFixture(t, root, "add", ".")
		gitFixture(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture")
		return gitFixture(t, root, "rev-parse", "HEAD")
	}
	write("a.sql", "SELECT 1;")
	write("z.sql", "SELECT 2;")
	first := commit()
	write("a.sql", "SELECT 3;")
	second := commit()
	m := Manifest{Version: "1", Objects: []Object{{Path: "z.sql", Commit: first}, {Path: "a.sql", Commit: second}}}
	write("a.sql", "invalid working SQL")
	if err := os.Remove(filepath.Join(root, "z.sql")); err != nil {
		t.Fatal(err)
	}
	files, err := m.Resolve(ctx, root)
	if err != nil || len(files) != 2 || files[0].Path != "z.sql" || files[0].SQL != "SELECT 2;" || files[1].SQL != "SELECT 3;" {
		t.Fatalf("resolved: %+v %v", files, err)
	}
	// Missing commits, tag/tree/blob IDs, unsafe paths and symlinks are never SQL.
	tree := gitFixture(t, root, "rev-parse", "HEAD^{tree}")
	blob := gitFixture(t, root, "rev-parse", "HEAD:a.sql")
	for _, ref := range []Object{{Path: "a.sql", Commit: strings.Repeat("0", 40)}, {Path: "a.sql", Commit: tree}, {Path: "a.sql", Commit: blob}, {Path: "../a.sql", Commit: first}, {Path: "missing.sql", Commit: first}, {Path: "a.sql", Commit: "HEAD"}, {Path: "a.sql", Commit: first[:12]}} {
		bad := Manifest{Version: "1", Objects: []Object{ref}}
		if _, err := bad.Resolve(ctx, root); err == nil {
			t.Fatalf("accepted %+v", ref)
		}
	}
	if err := os.Symlink("a.sql", filepath.Join(root, "link.sql")); err != nil {
		t.Fatal(err)
	}
	linkCommit := commit()
	if _, err := (Manifest{Version: "1", Objects: []Object{{Path: "link.sql", Commit: linkCommit}}}).Resolve(ctx, root); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestCommittedFilesRequireCommittedSQL(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "database", "objects")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "view.sql")
	if err := os.WriteFile(path, []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "init", "-q")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture")
	files, err := CommittedFiles(ctx, dir)
	if err != nil || len(files) != 1 || files[0].Path != "database/objects/view.sql" || len(files[0].Commit) != 40 {
		t.Fatalf("%+v %v", files, err)
	}
	if err := os.WriteFile(path, []byte("SELECT 2;"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CommittedFiles(ctx, dir); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatal(err)
	}
	gitFixture(t, root, "add", ".")
	if _, err := CommittedFiles(ctx, dir); err == nil {
		t.Fatal("accepted staged SQL")
	}
}

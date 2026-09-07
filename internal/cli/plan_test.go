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

func TestPlanCLI(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "view.sql")
	if err := os.WriteFile(file, []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := committedFixture(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := releases.New("30.1", files)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release.json")
	if err := manifest.Write(path); err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []bool{false, true} {
		if blocked {
			if err := os.WriteFile(file, []byte("SELECT 2;"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		goose := &fakeEngine{}
		db := &fakeObjects{}
		var out bytes.Buffer
		err := run(context.Background(), []string{"-manifest", path, "-objects-dir", dir, "mssql", "dsn", "plan"}, env(nil), &out, func(migrations.Config) (migrations.Engine, error) { return goose, nil }, func(string) (objects.Engine, error) { return db, nil })
		if err != nil || !goose.closed || !db.closed {
			t.Fatalf("error=%v goose=%+v db=%+v", err, goose, db)
		}
		if goose.command != "" || db.command != "status" {
			t.Fatal("planner called a migration/deployment operation")
		}
		if !strings.Contains(out.String(), "30 -> 30.1") || !strings.Contains(out.String(), "No database changes made") {
			t.Fatal(out.String())
		}
		if strings.Contains(out.String(), "BLOCKERS") {
			t.Fatal("missing blockers")
		}
	}
}

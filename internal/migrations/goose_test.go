package migrations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDiscoversGooseSQLMigrations(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"00031_next.sql", "00030_schema.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, driver := range []string{"mssql", "sqlserver"} {
		e, err := Open(Config{Driver: driver, DSN: "sqlserver://localhost?database=saxbase_test", Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		sources := e.(*gooseEngine).provider.ListSources()
		if len(sources) != 2 || sources[0].Version != 30 || sources[1].Version != 31 {
			t.Fatalf("unexpected sources: %+v", sources)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsInvalidConfiguration(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []Config{
		{Driver: "postgres", Dir: dir},
		{Driver: "mssql", Dir: filepath.Join(dir, "missing")},
		{Driver: "mssql", Dir: file},
	} {
		if e, err := Open(cfg); err == nil {
			e.Close()
			t.Fatalf("accepted %+v", cfg)
		}
	}
}

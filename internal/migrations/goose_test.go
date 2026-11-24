package migrations

import (
	"os"
	"path/filepath"
	"testing"

	"saxbase/internal/dialect"
)

func TestOpenDiscoversGooseSQLMigrations(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"00031_next.sql", "00030_schema.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("-- migration Up\nSELECT 1;\n-- migration Down\nSELECT 1;\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	e, err := Open(Config{Dialect: dialect.SQLServer, DSN: "sqlserver://localhost?database=saxbase_test", Dir: dir})
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

func TestOpenRejectsInvalidConfiguration(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []Config{
		{Dir: dir},
		{Dialect: dialect.SQLServer, DSN: "sqlserver://localhost?database=saxbase_test", Dir: filepath.Join(dir, "missing")},
		{Dialect: dialect.SQLServer, DSN: "sqlserver://localhost?database=saxbase_test", Dir: file},
	} {
		if e, err := Open(cfg); err == nil {
			e.Close()
			t.Fatalf("accepted %+v", cfg)
		}
	}
}

func TestTranslateAnnotations(t *testing.T) {
	in := "-- migration Up\n-- migration StatementBegin\nCREATE TRIGGER x ON t AS BEGIN SET NOCOUNT ON; END;\n-- migration StatementEnd\n-- migration Down\nDROP TRIGGER x;\n"
	out, err := translateAnnotations([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := "-- +goose Up\n-- +goose StatementBegin\nCREATE TRIGGER x ON t AS BEGIN SET NOCOUNT ON; END;\n-- +goose StatementEnd\n-- +goose Down\nDROP TRIGGER x;\n"
	if string(out) != want {
		t.Fatalf("translateAnnotations = %q, want %q", out, want)
	}
	if _, err := translateAnnotations([]byte("-- +goose Up\nSELECT 1;\n")); err == nil {
		t.Fatal("expected a goose marker to be rejected")
	}
}

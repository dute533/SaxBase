package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateMigration(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"00001_first.sql", "00009_later.sql", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("existing"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path, err := Create(dir, "add_customer_email")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "00010_add_customer_email.sql" {
		t.Fatalf("path = %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, annotation := range []string{"-- +goose Up", "-- +goose Down"} {
		if !strings.Contains(string(data), annotation) {
			t.Fatalf("migration missing %q: %s", annotation, data)
		}
	}
}

func TestCreateFirstMigration(t *testing.T) {
	dir := t.TempDir()
	path, err := Create(dir, "initialize")
	if err != nil || filepath.Base(path) != "00001_initialize.sql" {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestCreateMigrationValidation(t *testing.T) {
	for _, name := range []string{"", "AddThing", "add-thing", "add thing", "add__thing", "1_add"} {
		t.Run(name, func(t *testing.T) {
			if _, err := Create(t.TempDir(), name); err == nil {
				t.Fatalf("accepted migration name %q", name)
			}
		})
	}

	dir := t.TempDir()
	for _, name := range []string{"00001_one.sql", "1_duplicate.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Create(dir, "next"); err == nil || !strings.Contains(err.Error(), "duplicate migration version") {
		t.Fatalf("duplicate versions: %v", err)
	}
}

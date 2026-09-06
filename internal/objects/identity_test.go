package objects

import (
	"strings"
	"testing"
)

func TestObjectIdentity(t *testing.T) {
	for _, tc := range []struct{ sql, kind, schema, name string }{
		{"CREATE OR ALTER VIEW dbo.v AS SELECT 1 AS n;", "VIEW", "dbo", "v"},
		{"-- comment\nCREATE PROC [dbo].[get]]item] @id INT AS SELECT @id;", "PROCEDURE", "dbo", "get]item"},
		{`/* outer /* nested */ */ CREATE OR ALTER FUNCTION "my.schema"."name""quote"() RETURNS INT AS BEGIN RETURN 1; END;`, "FUNCTION", "my.schema", "name\"quote"},
		{"\ufeffcreate view dbo.客户 as select 1 AS n;", "VIEW", "dbo", "客户"},
	} {
		id, err := objectIdentity(tc.sql)
		if err != nil || id.kind != tc.kind || id.schema != tc.schema || id.name != tc.name {
			t.Fatalf("%s: %+v %v", tc.sql, id, err)
		}
	}
	for _, definition := range []string{"SELECT 1;", "CREATE TABLE dbo.t(id int)", "CREATE VIEW unqualified AS SELECT 1;", "CREATE VIEW db.dbo.v AS SELECT 1;", "/* bad", "CREATE VIEW [dbo].[bad AS SELECT 1;"} {
		if _, err := objectIdentity(definition); err == nil {
			t.Fatalf("accepted unsupported definition: %s", definition)
		}
	}
	id, err := objectIdentity("CREATE VIEW [a;b].[v]];--] AS SELECT 1 AS n;")
	if err != nil {
		t.Fatal(err)
	}
	if id.drop() != "DROP VIEW IF EXISTS [a;b].[v]];--];" {
		t.Fatal(id.drop())
	}
}

func TestSnapshotValidationForRollback(t *testing.T) {
	file := objectFile("view.sql", "CREATE VIEW dbo.v AS SELECT 1 AS n;")
	snapshot := Snapshot{Release: Release{Version: "1", ObjectCount: 1}, Objects: []SnapshotObject{{Path: file.Path, SQL: file.SQL, Checksum: file.Checksum}}}
	if _, _, err := snapshotFiles(snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Objects[0].Checksum = strings.Repeat("0", 64)
	if _, _, err := snapshotFiles(snapshot); err == nil {
		t.Fatal("corrupt snapshot accepted")
	}
	snapshot.ObjectCount = 2
	if _, _, err := snapshotFiles(snapshot); err == nil {
		t.Fatal("incomplete snapshot accepted")
	}
}

func TestRestoreCreateDefinition(t *testing.T) {
	input := "-- header\nCREATE /* comment */ VIEW dbo.v AS SELECT N'CREATE' AS n;"
	want := "-- header\nCREATE OR ALTER /* comment */ VIEW dbo.v AS SELECT N'CREATE' AS n;"
	if got := restoreDefinition(input); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := restoreDefinition(want); got != want {
		t.Fatalf("changed CREATE OR ALTER: %q", got)
	}
}

package objects

import (
	"strings"
	"testing"
)

func TestNormalizeDefinition(t *testing.T) {
	cases := []struct{ a, b string }{
		{"CREATE OR ALTER VIEW dbo.v AS\nSELECT 1;", "CREATE VIEW dbo.v AS select 1;"},
		{"\ufeffCREATE OR ALTER PROCEDURE dbo.p AS SELECT 1;", "create procedure dbo.p as  select 1;"},
		{"CREATE   OR   ALTER FUNCTION dbo.f() RETURNS INT AS BEGIN RETURN 1; END", "create function dbo.f() returns int as begin return 1; end"},
	}
	for _, tc := range cases {
		if got, want := NormalizeDefinition(tc.a), NormalizeDefinition(tc.b); got != want {
			t.Fatalf("NormalizeDefinition(%q) = %q, want %q", tc.a, got, want)
		}
	}
}

func TestDiffFingerprints(t *testing.T) {
	stored := "table\tdbo.customers\n" +
		"column\tdbo.customers.id\tint\n" +
		"module\tdbo.v\tV\t" + hashDefinition("create view dbo.v as select 1") + "\n"
	current := "table\tdbo.customers\n" +
		"table\tdbo.extra\n" +
		"column\tdbo.customers.id\tint\n" +
		"column\tdbo.customers.v\tnvarchar\n" +
		"module\tdbo.v\tV\t" + hashDefinition("create view dbo.v as select 2") + "\n"
	got := diffFingerprints(stored, current)
	want := []string{
		"+ column\tdbo.customers.v\tnvarchar",
		"+ table\tdbo.extra",
		"~ dbo.v V definition changed",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("diffFingerprints = %q, want %q", got, want)
	}
}

func TestDiffDescribed(t *testing.T) {
	managed := []File{{Path: "v.sql", SQL: "CREATE OR ALTER VIEW dbo.v AS SELECT 1;"}}
	described := "module\tdbo.v\tv\t" + hashDefinition("create view dbo.v as select 1") + "\n" +
		"module\tdbo.tgr\ttr\t" + hashDefinition("create trigger dbo.tgr on dbo.t as return") + "\n"
	live := "module\tdbo.v\tv\t" + hashDefinition("create view dbo.v as select 1") + "\n" +
		"module\tdbo.tgr\ttr\t" + hashDefinition("create trigger dbo.tgr on dbo.t as select 1") + "\n" +
		"module\tdbo.extra_tr\ttr\t" + hashDefinition("create trigger dbo.extra_tr on dbo.t as return") + "\n"
	got := DiffDescribed(described, live, managed)
	want := []string{
		"+ module\tdbo.extra_tr\ttr\t" + hashDefinition("create trigger dbo.extra_tr on dbo.t as return"),
		"~ dbo.tgr tr definition changed",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("DiffDescribed = %q, want %q (managed dbo.v must be filtered out)", got, want)
	}
}

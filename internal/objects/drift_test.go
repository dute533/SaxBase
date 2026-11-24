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

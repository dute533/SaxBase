package objects

import "testing"

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

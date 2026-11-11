package dialect

import "testing"

func TestParse(t *testing.T) {
	for input, want := range map[string]Name{
		"sqlserver":   SQLServer,
		" SQLSERVER ": SQLServer,
		"postgres":    Postgres,
		"Postgres":    Postgres,
	} {
		got, err := Parse(input)
		if err != nil || got != want {
			t.Fatalf("Parse(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

func TestParseRejectsMissingAndUnsupportedDialects(t *testing.T) {
	for _, input := range []string{"", "mysql", "postgresql", "mssql"} {
		if _, err := Parse(input); err == nil {
			t.Fatalf("Parse(%q) succeeded", input)
		}
	}
}

func TestConnection(t *testing.T) {
	for name, driver := range map[Name]string{SQLServer: "sqlserver", Postgres: "pgx"} {
		connection, err := name.Connection()
		if err != nil || connection.Driver != driver || connection.ValidateDSN == nil {
			t.Fatalf("%s connection = %+v, %v", name, connection, err)
		}
	}
	if _, err := Name("mysql").Connection(); err == nil {
		t.Fatal("unsupported database returned a connection")
	}
}

package sqlserverdsn

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, dsn := range []string{
		"sqlserver://user:password@localhost:1433?database=ObjectStore",
		"server=localhost;user id=user;password=password;database=ObjectStore",
		"odbc:server={localhost};uid=user;pwd=password;database=ObjectStore",
	} {
		if err := Validate(dsn); err != nil {
			t.Errorf("valid DSN rejected: %v", err)
		}
	}
}

func TestRejectsUnsafeConnectionStrings(t *testing.T) {
	for _, test := range []struct {
		name, dsn, want string
	}{
		{"JDBC", "jdbc:sqlserver://localhost:1433;databaseName=ObjectStore;user=sa;password=secret", "JDBC"},
		{"missing database", "sqlserver://user:secret@localhost:1433", "explicitly set database"},
		{"wrong URL parameter", "sqlserver://user:secret@localhost:1433?databaseName=ObjectStore", "explicitly set database"},
		{"system database", "sqlserver://user:secret@localhost:1433?database=master", "system database"},
		{"invalid", "://secret", "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.dsn)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked credentials: %v", err)
			}
		})
	}
}

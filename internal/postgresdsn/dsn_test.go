package postgresdsn

import "testing"

func TestValidate(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:password@localhost:5432/app",
		"host=localhost user=user password=password dbname=app sslmode=disable",
	} {
		if err := Validate(dsn); err != nil {
			t.Fatalf("%s: %v", dsn, err)
		}
	}
}

func TestRejectsUnsafeDSNs(t *testing.T) {
	for _, dsn := range []string{"", "://bad", "postgres://localhost", "postgres://localhost/", "postgres://localhost/postgres", "host=localhost user=user"} {
		if err := Validate(dsn); err == nil {
			t.Fatalf("accepted %q", dsn)
		}
	}
}

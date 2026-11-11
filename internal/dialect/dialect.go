// Package dialect identifies the database implementations supported by SaxBase.
package dialect

import (
	"fmt"
	"strings"

	"saxbase/internal/postgresdsn"
	"saxbase/internal/sqlserverdsn"
)

type Name string

const (
	SQLServer Name = "sqlserver"
	Postgres  Name = "postgres"
)

// Connection contains the mandatory connection behavior for a supported
// database. Dialect-specific migration and object SQL remains in its owning
// package.
type Connection struct {
	Driver      string
	ValidateDSN func(string) error
}

func Parse(value string) (Name, error) {
	name := Name(strings.ToLower(strings.TrimSpace(value)))
	switch name {
	case SQLServer, Postgres:
		return name, nil
	case "":
		return "", fmt.Errorf("database dialect is required")
	default:
		return "", fmt.Errorf("unsupported database dialect %q", value)
	}
}

func (n Name) Validate() error {
	switch n {
	case SQLServer, Postgres:
		return nil
	case "":
		return fmt.Errorf("database dialect is required")
	default:
		return fmt.Errorf("unsupported database dialect %q", n)
	}
}

func (n Name) Connection() (Connection, error) {
	switch n {
	case SQLServer:
		return Connection{Driver: "sqlserver", ValidateDSN: sqlserverdsn.Validate}, nil
	case Postgres:
		return Connection{Driver: "pgx", ValidateDSN: postgresdsn.Validate}, nil
	default:
		return Connection{}, n.Validate()
	}
}

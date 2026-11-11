// Package postgresdsn validates connection strings before PostgreSQL access.
package postgresdsn

import (
	"errors"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Validate requires a PostgreSQL connection string with an explicit,
// non-maintenance database.
func Validate(dsn string) error {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return errors.New("PostgreSQL connection string is empty")
	}
	parseDSN := dsn
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
			return errors.New("invalid PostgreSQL connection string")
		}
		if strings.Trim(u.Path, "/") == "" {
			return errors.New("PostgreSQL connection string must explicitly set database")
		}
	} else {
		// pgx otherwise inherits a database from PGDATABASE or the user name.
		// Seed a sentinel so only an explicit database/dbname setting can replace it.
		parseDSN = "database=__saxbase_database_required__ " + dsn
	}
	config, err := pgx.ParseConfig(parseDSN)
	if err != nil {
		return errors.New("invalid PostgreSQL connection string")
	}
	database := strings.TrimSpace(config.Database)
	if database == "" || database == "__saxbase_database_required__" {
		return errors.New("PostgreSQL connection string must explicitly set database")
	}
	switch strings.ToLower(database) {
	case "postgres", "template0", "template1":
		return errors.New("refusing to deploy to PostgreSQL maintenance database " + database)
	}
	return nil
}

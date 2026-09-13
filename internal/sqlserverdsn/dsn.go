// Package sqlserverdsn validates connection strings before SQL Server access.
package sqlserverdsn

import (
	"errors"
	"fmt"
	"strings"

	"github.com/microsoft/go-mssqldb/msdsn"
)

// Validate requires a native go-mssqldb connection string with an explicit,
// non-system database. Without a database, SQL Server uses the login default,
// which is commonly master and is unsafe for schema deployment.
func Validate(dsn string) error {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return errors.New("SQL Server connection string is empty")
	}
	lower := strings.ToLower(dsn)
	if strings.HasPrefix(lower, "jdbc:") {
		return errors.New("JDBC SQL Server connection strings are not supported; use sqlserver://USER:PASSWORD@HOST:PORT?database=DATABASE")
	}
	if (strings.Contains(dsn, "://") && !strings.HasPrefix(lower, "sqlserver://")) ||
		(!strings.HasPrefix(lower, "sqlserver://") && !strings.HasPrefix(lower, "odbc:") && !strings.Contains(dsn, "=")) {
		return errors.New("invalid SQL Server connection string")
	}
	config, err := msdsn.Parse(dsn)
	if err != nil {
		return errors.New("invalid SQL Server connection string")
	}
	database := strings.TrimSpace(config.Database)
	if database == "" {
		return errors.New("SQL Server connection string must explicitly set database; refusing to use the login default")
	}
	switch strings.ToLower(database) {
	case "master", "model", "msdb", "tempdb":
		return fmt.Errorf("refusing to deploy to SQL Server system database %q", database)
	}
	return nil
}

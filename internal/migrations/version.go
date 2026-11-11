package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3/database"
	"saxbase/internal/dialect"
)

// VersionInTransaction uses Goose's own store semantics without creating or
// changing its version table. The caller controls transaction isolation.
func VersionInTransaction(ctx context.Context, tx *sql.Tx, name dialect.Name) (int64, error) {
	settings, err := gooseSettingsFor(name)
	if err != nil {
		return 0, err
	}
	store, err := database.NewStore(settings.storeDialect, settings.tableName)
	if err != nil {
		return 0, err
	}
	return store.GetLatestVersion(ctx, tx)
}

package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

// VersionInTransaction uses Goose's own store semantics without creating or
// changing its version table. The caller controls transaction isolation.
func VersionInTransaction(ctx context.Context, tx *sql.Tx) (int64, error) {
	store, err := database.NewStore(database.DialectMSSQL, goose.DefaultTablename)
	if err != nil {
		return 0, err
	}
	return store.GetLatestVersion(ctx, tx)
}

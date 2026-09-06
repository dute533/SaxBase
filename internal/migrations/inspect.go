package migrations

import (
	"context"
	"database/sql"
	"sort"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

// Inspect never initializes Goose's version table, unlike Provider.Status.
func (g *gooseEngine) Inspect(ctx context.Context) (Inspection, error) {
	result := Inspection{Migrations: make([]Status, 0)}
	var table sql.NullInt64
	if err := g.db.QueryRowContext(ctx, "SELECT OBJECT_ID(N'dbo.goose_db_version',N'U')").Scan(&table); err != nil {
		return result, err
	}
	applied := map[int64]bool{}
	if table.Valid {
		store, err := database.NewStore(database.DialectMSSQL, goose.DefaultTablename)
		if err != nil {
			return result, err
		}
		rows, err := store.ListMigrations(ctx, g.db)
		if err != nil {
			return result, err
		}
		// Goose returns newest records first. Respect the newest state per version.
		seen := map[int64]bool{}
		for _, row := range rows {
			if seen[row.Version] {
				continue
			}
			seen[row.Version] = true
			if row.IsApplied {
				applied[row.Version] = true
				if row.Version > result.Version {
					result.Version = row.Version
				}
			}
		}
	}
	for _, source := range g.provider.ListSources() {
		state := "pending"
		if applied[source.Version] {
			state = "applied"
		}
		result.Migrations = append(result.Migrations, Status{Version: source.Version, Path: source.Path, State: state})
		delete(applied, source.Version)
	}
	for version := range applied {
		if version != 0 {
			result.Migrations = append(result.Migrations, Status{Version: version, State: "missing"})
		}
	}
	sort.Slice(result.Migrations, func(i, j int) bool { return result.Migrations[i].Version < result.Migrations[j].Version })
	return result, nil
}

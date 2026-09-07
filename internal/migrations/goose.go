package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

type gooseEngine struct {
	db       *sql.DB
	provider *goose.Provider
}

func Open(cfg Config) (Engine, error) {
	if cfg.Driver != "mssql" && cfg.Driver != "sqlserver" {
		return nil, fmt.Errorf("unsupported driver %q: use mssql or sqlserver", cfg.Driver)
	}
	info, err := os.Stat(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("migration directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("migration path %q is not a directory", cfg.Dir)
	}
	db, err := sql.Open("sqlserver", cfg.DSN)
	if err != nil {
		// Driver parsing errors can include the DSN, which may contain a password.
		return nil, fmt.Errorf("open SQL Server connection: invalid connection string")
	}
	p, err := goose.NewProvider(goose.DialectMSSQL, db, os.DirFS(cfg.Dir), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize Goose: %w", err)
	}
	return &gooseEngine{db: db, provider: p}, nil
}

func (g *gooseEngine) UpTo(ctx context.Context, version int64) error {
	_, err := g.provider.UpTo(ctx, version)
	return err
}

func (g *gooseEngine) DownTo(ctx context.Context, version int64) error {
	if err := g.ValidateDownTo(ctx, version); err != nil {
		return err
	}
	_, err := g.provider.DownTo(ctx, version)
	return err
}

func (g *gooseEngine) ValidateDownTo(ctx context.Context, target int64) error {
	current, err := g.Version(ctx)
	if err != nil {
		return err
	}
	if target < 0 || target > current {
		return fmt.Errorf("cannot roll schema %d back to %d", current, target)
	}
	store, err := database.NewStore(database.DialectMSSQL, goose.DefaultTablename)
	if err != nil {
		return err
	}
	rows, err := store.ListMigrations(ctx, g.db)
	if err != nil {
		return err
	}
	sources := map[int64]bool{}
	for _, source := range g.provider.ListSources() {
		sources[source.Version] = true
	}
	targetApplied := target == 0
	for _, row := range rows {
		if !row.IsApplied {
			continue
		}
		if row.Version == target {
			targetApplied = true
		}
		if row.Version > target && !sources[row.Version] {
			return fmt.Errorf("migration file for applied version %d is missing", row.Version)
		}
	}
	if !targetApplied {
		return fmt.Errorf("target Goose version %d is not applied", target)
	}
	return nil
}

func (g *gooseEngine) Status(ctx context.Context) ([]Status, error) {
	rows, err := g.provider.Status(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Status, 0, len(rows))
	for _, row := range rows {
		result = append(result, Status{Version: row.Source.Version, Path: row.Source.Path, State: string(row.State)})
	}
	return result, nil
}

func (g *gooseEngine) Version(ctx context.Context) (int64, error) {
	return g.provider.GetDBVersion(ctx)
}

func (g *gooseEngine) Close() error { return g.db.Close() }

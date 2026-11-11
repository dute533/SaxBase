package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"saxbase/internal/dialect"
)

type gooseEngine struct {
	db           *sql.DB
	provider     *goose.Provider
	storeDialect database.Dialect
	tableName    string
	dialect      dialect.Name
}

type gooseSettings struct {
	gooseDialect goose.Dialect
	storeDialect database.Dialect
	tableName    string
}

func gooseSettingsFor(name dialect.Name) (gooseSettings, error) {
	switch name {
	case dialect.SQLServer:
		return gooseSettings{gooseDialect: goose.DialectMSSQL, storeDialect: database.DialectMSSQL, tableName: "dbo.saxbase_schema_version"}, nil
	case dialect.Postgres:
		return gooseSettings{gooseDialect: goose.DialectPostgres, storeDialect: database.DialectPostgres, tableName: "public.saxbase_schema_version"}, nil
	default:
		return gooseSettings{}, fmt.Errorf("unsupported database dialect %q", name)
	}
}

func Open(cfg Config) (Engine, error) {
	connection, err := cfg.Dialect.Connection()
	if err != nil {
		return nil, err
	}
	settings, err := gooseSettingsFor(cfg.Dialect)
	if err != nil {
		return nil, err
	}
	if err := connection.ValidateDSN(cfg.DSN); err != nil {
		return nil, err
	}
	info, err := os.Stat(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("migration directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("migration path %q is not a directory", cfg.Dir)
	}
	db, err := sql.Open(connection.Driver, cfg.DSN)
	if err != nil {
		// Driver parsing errors can include the DSN, which may contain a password.
		return nil, fmt.Errorf("open %s connection: invalid connection string", cfg.Dialect)
	}
	p, err := goose.NewProvider(
		settings.gooseDialect,
		db,
		os.DirFS(cfg.Dir),
		goose.WithDisableGlobalRegistry(true),
		goose.WithTableName(settings.tableName),
	)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize Goose: %w", err)
	}
	return &gooseEngine{db: db, provider: p, storeDialect: settings.storeDialect, tableName: settings.tableName, dialect: cfg.Dialect}, nil
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
	store, err := database.NewStore(g.storeDialect, g.tableName)
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
		return fmt.Errorf("target database schema %d is not applied", target)
	}
	return nil
}

func (g *gooseEngine) Version(ctx context.Context) (int64, error) {
	return g.provider.GetDBVersion(ctx)
}

func (g *gooseEngine) Close() error { return g.db.Close() }

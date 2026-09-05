package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/pressly/goose/v3"
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

func (g *gooseEngine) Up(ctx context.Context) error {
	_, err := g.provider.Up(ctx)
	return err
}

func (g *gooseEngine) Down(ctx context.Context) error {
	_, err := g.provider.Down(ctx)
	return err
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

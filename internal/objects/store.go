package objects

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	"saxbase/internal/migrations"
)

type store struct {
	db      *sql.DB
	dialect objectDialect
}

func Open(cfg migrations.Config) (Engine, error) {
	connection, err := cfg.Dialect.Connection()
	if err != nil {
		return nil, err
	}
	if err := connection.ValidateDSN(cfg.DSN); err != nil {
		return nil, err
	}
	dialectSpec, err := objectDialectFor(cfg.Dialect)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(connection.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open %s connection: invalid connection string", cfg.Dialect)
	}
	return &store{db: db, dialect: dialectSpec}, nil
}
func (s *store) Close() error { return s.db.Close() }

type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type transactionStarter interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// Apply uses its locked connection for the object transaction to avoid taking
// the same application lock on a second session.
func (s *store) applyOn(ctx context.Context, db transactionStarter, files, baseline []File, release Release, force bool) ([]Status, error) {
	if err := validateFiles(files); err != nil {
		return nil, err
	}
	if err := validateFiles(baseline); err != nil {
		return nil, fmt.Errorf("invalid release baseline: %w", err)
	}
	if err := validateObjectTransition(files, baseline); err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := s.checkReleaseSchema(ctx, tx, release.MigrationVersion); err != nil {
		return nil, err
	}
	result := Compare(files, baseline)
	if len(result) > len(files) {
		result = result[:len(files)]
	}
	if force {
		for i, file := range files {
			result[i].State = "changed"
			if file.Delete {
				result[i].State = "delete"
			}
		}
	}
	alreadyCurrent := false
	created, err := s.prepareRelease(ctx, tx, release, force)
	if err != nil {
		return nil, err
	}
	if !created && !force {
		current, err := s.currentVersion(ctx, tx)
		if err != nil {
			return nil, err
		}
		alreadyCurrent = current == release.Version
		if alreadyCurrent {
			for i, file := range files {
				result[i].State = "current"
				if file.Delete {
					result[i].State = "missing"
				}
			}
		}
	}
	for i, file := range files {
		if alreadyCurrent {
			continue
		}
		if file.Delete {
			if result[i].State == "missing" {
				continue
			}
			id, err := objectIdentity(file.SQL)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", file.Path, err)
			}
			if _, err := tx.ExecContext(ctx, s.dialect.drop(id)); err != nil {
				return nil, fmt.Errorf("drop %s: %w", file.Path, err)
			}
			continue
		}
		if result[i].State == "current" {
			continue
		}
		if _, err := tx.ExecContext(ctx, file.SQL); err != nil {
			return nil, fmt.Errorf("apply %s: %w", file.Path, err)
		}
	}

	if err := s.setCurrent(ctx, tx, release.Version); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for i := range result {
		if result[i].State == "new" || result[i].State == "changed" {
			result[i].State = "applied"
		}
	}
	return result, nil
}

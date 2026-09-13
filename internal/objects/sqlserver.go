package objects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"saxbase/internal/deploymentlock"
	"saxbase/internal/releaseversion"
	"saxbase/internal/sqlserverdsn"

	_ "github.com/microsoft/go-mssqldb"
)

type store struct{ db *sql.DB }

func Open(dsn string) (Engine, error) {
	if err := sqlserverdsn.Validate(dsn); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, errors.New("open SQL Server connection: invalid connection string")
	}
	return &store{db: db}, nil
}
func (s *store) Close() error { return s.db.Close() }

type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type transactionStarter interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func (s *store) apply(ctx context.Context, files, baseline []File, release *Release) ([]Status, error) {
	return s.applyOn(ctx, s.db, files, baseline, release, false, true)
}

// Apply uses its locked connection for the object transaction to avoid taking
// the same application lock on a second session.
func (s *store) applyOn(ctx context.Context, db transactionStarter, files, baseline []File, release *Release, force, acquireLock bool) ([]Status, error) {
	if err := validateFiles(files); err != nil {
		return nil, err
	}
	if err := validateFiles(baseline); err != nil {
		return nil, fmt.Errorf("invalid release baseline: %w", err)
	}
	var options *sql.TxOptions
	if release != nil {
		options = &sql.TxOptions{Isolation: sql.LevelSerializable}
	}
	tx, err := db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if acquireLock {
		var lock int
		err = tx.QueryRowContext(ctx, `DECLARE @result int;
 EXEC @result = sys.sp_getapplock @Resource=N'SaxBase.objects', @LockMode='Exclusive', @LockOwner='Transaction', @LockTimeout=30000;
 SELECT @result;`).Scan(&lock)
		if err != nil {
			return nil, fmt.Errorf("lock object deployment: %w", err)
		}
		if lock < 0 {
			return nil, fmt.Errorf("lock object deployment failed (code %d)", lock)
		}
	}
	if err := deploymentlock.CheckPending(ctx, tx); err != nil {
		return nil, err
	}
	if release != nil {
		version, err := releaseversion.Parse(release.Version)
		if err != nil {
			return nil, err
		}
		if err := checkReleaseSchema(ctx, tx, version.Schema); err != nil {
			return nil, err
		}
	}
	result := Compare(files, baseline)
	if release != nil && len(result) > len(files) {
		result = result[:len(files)]
	}
	if force {
		for i, file := range files {
			result[i].State = "changed"
			if file.Delete {
				result[i].State = "deleted"
			}
		}
	}
	alreadyCurrent := false
	if release != nil {
		created, err := prepareRelease(ctx, tx, *release, files, force)
		if err != nil {
			return nil, err
		}
		if !created && !force {
			current, err := currentVersion(ctx, tx)
			if err != nil {
				return nil, err
			}
			alreadyCurrent = current == release.Version
			if alreadyCurrent {
				for i, file := range files {
					result[i].State = "unchanged"
					if file.Delete {
						result[i].State = "missing"
					}
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
			if _, err := tx.ExecContext(ctx, id.drop()); err != nil {
				return nil, fmt.Errorf("drop %s: %w", file.Path, err)
			}
			continue
		}
		if result[i].State == "unchanged" {
			continue
		}
		if _, err := tx.ExecContext(ctx, file.SQL); err != nil {
			return nil, fmt.Errorf("apply %s: %w", file.Path, err)
		}
	}

	if release != nil {
		if err := setCurrent(ctx, tx, release.Version); err != nil {
			return nil, err
		}
	} else {
		for _, row := range result {
			if row.State == "new" || row.State == "changed" {
				if err := deploymentlock.Invalidate(ctx, tx); err != nil {
					return nil, err
				}
				break
			}
		}
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

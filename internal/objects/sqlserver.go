package objects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"saxbase/internal/deploymentlock"

	_ "github.com/microsoft/go-mssqldb"
)

type store struct{ db *sql.DB }

func Open(dsn string) (Engine, error) {
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

func read(ctx context.Context, db reader) (map[string]string, error) {
	var id sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT OBJECT_ID(N'dbo.saxbase_objects', N'U')").Scan(&id); err != nil {
		return nil, err
	}
	result := make(map[string]string)
	if !id.Valid {
		return result, nil
	}
	rows, err := db.QueryContext(ctx, "SELECT path, checksum FROM dbo.saxbase_objects")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var path, sum string
		if err := rows.Scan(&path, &sum); err != nil {
			return nil, err
		}
		result[path] = sum
	}
	return result, rows.Err()
}

// Status does not create metadata or execute object SQL.
func (s *store) Status(ctx context.Context, files []File) ([]Status, error) {
	deployed, err := read(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return compare(files, deployed), nil
}

type transactionStarter interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func (s *store) apply(ctx context.Context, files []File, release *Release) ([]Status, error) {
	return s.applyOn(ctx, s.db, files, release, true)
}

// Apply uses its locked connection for the object transaction to avoid taking
// the same application lock on a second session.
func (s *store) applyOn(ctx context.Context, db transactionStarter, files []File, release *Release, acquireLock bool) ([]Status, error) {
	if err := validateFiles(files); err != nil {
		return nil, err
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
		if err := checkReleaseSchema(ctx, tx, release.SchemaVersion); err != nil {
			return nil, err
		}
	}
	_, err = tx.ExecContext(ctx, `IF OBJECT_ID(N'dbo.saxbase_objects', N'U') IS NULL
 CREATE TABLE dbo.saxbase_objects (
 path nvarchar(450) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
 checksum char(64) NOT NULL
 );`)
	if err != nil {
		return nil, err
	}
	deployed, err := read(ctx, tx)
	if err != nil {
		return nil, err
	}
	result := compare(files, deployed)
	if release != nil && len(result) > len(files) {
		result = result[:len(files)]
	}
	for i, file := range files {
		if file.Delete {
			if _, ok := deployed[file.Path]; ok {
				result[i].State = "deleted"
			} else {
				result[i].State = "missing"
			}
		}
	}
	if release != nil {
		_, _, err = prepareRelease(ctx, tx, *release, files)
		if err != nil {
			return nil, err
		}
	}
	for _, file := range files {
		if file.Delete {
			if _, ok := deployed[file.Path]; !ok {
				return nil, fmt.Errorf("cannot remove untracked object %s", file.Path)
			}
			id, err := objectIdentity(file.SQL)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", file.Path, err)
			}
			if _, err := tx.ExecContext(ctx, id.drop()); err != nil {
				return nil, fmt.Errorf("drop %s: %w", file.Path, err)
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM dbo.saxbase_objects WHERE path=@path", sql.Named("path", file.Path)); err != nil {
				return nil, fmt.Errorf("untrack %s: %w", file.Path, err)
			}
			continue
		}
		if deployed[file.Path] == file.Checksum {
			continue
		}
		if _, err := tx.ExecContext(ctx, file.SQL); err != nil {
			return nil, fmt.Errorf("apply %s: %w", file.Path, err)
		}
		_, err = tx.ExecContext(ctx, `UPDATE dbo.saxbase_objects SET checksum=@checksum WHERE path=@path;
 IF @@ROWCOUNT=0 INSERT INTO dbo.saxbase_objects(path,checksum) VALUES(@path,@checksum);`, sql.Named("path", file.Path), sql.Named("checksum", file.Checksum))
		if err != nil {
			return nil, fmt.Errorf("track %s: %w", file.Path, err)
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

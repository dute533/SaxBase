package objects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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

// Apply serializes SaxBase object deployments and commits definitions and their
// checksums together. Each file is a separate SQL batch within the transaction.
func (s *store) Apply(ctx context.Context, files []File) ([]Status, error) {
	return s.apply(ctx, files, nil)
}

func (s *store) ApplyRelease(ctx context.Context, files []File, schema, revision int64) ([]Status, error) {
	if schema < 0 || revision < 0 {
		return nil, errors.New("release components must be nonnegative")
	}
	version := fmt.Sprint(schema)
	if revision > 0 {
		version += fmt.Sprintf(".%d", revision)
	}
	return s.apply(ctx, files, &Release{Version: version, SchemaVersion: schema, Revision: revision})
}

func (s *store) apply(ctx context.Context, files []File, release *Release) ([]Status, error) {
	if err := validateFiles(files); err != nil {
		return nil, err
	}
	var options *sql.TxOptions
	if release != nil {
		options = &sql.TxOptions{Isolation: sql.LevelSerializable}
	}
	tx, err := s.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
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
	if release != nil {
		if err := checkReleaseSchema(ctx, tx, release.SchemaVersion); err != nil {
			return nil, err
		}
	}
	_, err = tx.ExecContext(ctx, `IF OBJECT_ID(N'dbo.saxbase_objects', N'U') IS NULL
 CREATE TABLE dbo.saxbase_objects (
 path nvarchar(450) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
 checksum char(64) NOT NULL,
 deployed_at datetime2 NOT NULL DEFAULT SYSUTCDATETIME()
 );`)
	if err != nil {
		return nil, err
	}
	deployed, err := read(ctx, tx)
	if err != nil {
		return nil, err
	}
	result := compare(files, deployed)
	var releaseID int64
	var newRelease bool
	if release != nil {
		for _, row := range result {
			if row.State == "missing" {
				return nil, fmt.Errorf("release omits tracked object %s; object removal must be handled explicitly", row.Path)
			}
		}
		releaseID, newRelease, err = prepareRelease(ctx, tx, *release, files)
		if err != nil {
			return nil, err
		}
	}
	for _, file := range files {
		if deployed[file.Path] == file.Checksum {
			continue
		}
		if _, err := tx.ExecContext(ctx, file.SQL); err != nil {
			return nil, fmt.Errorf("apply %s: %w", file.Path, err)
		}
		_, err = tx.ExecContext(ctx, `UPDATE dbo.saxbase_objects SET checksum=@checksum, deployed_at=SYSUTCDATETIME() WHERE path=@path;
 IF @@ROWCOUNT=0 INSERT INTO dbo.saxbase_objects(path,checksum) VALUES(@path,@checksum);`, sql.Named("path", file.Path), sql.Named("checksum", file.Checksum))
		if err != nil {
			return nil, fmt.Errorf("track %s: %w", file.Path, err)
		}
	}
	if newRelease {
		if err := recordSnapshot(ctx, tx, releaseID, files); err != nil {
			return nil, err
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

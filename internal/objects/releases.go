package objects

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"saxbase/internal/migrations"
)

func validateFiles(files []File) error {
	seen := make(map[string]bool)
	for _, file := range files {
		if seen[file.Path] {
			return fmt.Errorf("duplicate object path %s", file.Path)
		}
		seen[file.Path] = true
		if !utf8.ValidString(file.SQL) {
			return fmt.Errorf("object %s is not UTF-8", file.Path)
		}
		if file.Checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(file.SQL))) {
			return fmt.Errorf("checksum mismatch: %s", file.Path)
		}
	}
	return nil
}

func checkReleaseSchema(ctx context.Context, tx *sql.Tx, want int64) error {
	current, err := migrations.VersionInTransaction(ctx, tx)
	if err != nil {
		return fmt.Errorf("read Goose version for release: %w", err)
	}
	if current != want {
		return fmt.Errorf("release requires Goose version %d; database is at %d", want, current)
	}
	return nil
}

const releaseTables = `IF OBJECT_ID(N'dbo.saxbase_releases', N'U') IS NULL
 CREATE TABLE dbo.saxbase_releases (
 id bigint IDENTITY(1,1) NOT NULL PRIMARY KEY,
 version varchar(39) NOT NULL UNIQUE,
 schema_version bigint NOT NULL,
 revision bigint NOT NULL,
 deployed_at datetime2 NOT NULL DEFAULT SYSUTCDATETIME(),
 object_count int NOT NULL,
 fingerprint char(64) NULL,
 is_current bit NOT NULL CONSTRAINT DF_saxbase_releases_is_current DEFAULT 0,
 UNIQUE(schema_version,revision)
 );
 IF COL_LENGTH(N'dbo.saxbase_releases', N'fingerprint') IS NULL
 ALTER TABLE dbo.saxbase_releases ADD fingerprint char(64) NULL;
 IF COL_LENGTH(N'dbo.saxbase_releases', N'is_current') IS NULL
 ALTER TABLE dbo.saxbase_releases ADD is_current bit NOT NULL CONSTRAINT DF_saxbase_releases_is_current DEFAULT 0;`

func prepareRelease(ctx context.Context, tx *sql.Tx, release Release, files []File) (int64, bool, error) {
	if _, err := tx.ExecContext(ctx, releaseTables); err != nil {
		return 0, false, fmt.Errorf("initialize release history: %w", err)
	}
	var id int64
	var fingerprint sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT id, fingerprint FROM dbo.saxbase_releases WHERE version=@version", sql.Named("version", release.Version)).Scan(&id, &fingerprint)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if exists && (!fingerprint.Valid || fingerprint.String != Fingerprint(files)) {
		return 0, false, fmt.Errorf("release %s is immutable or predates Git manifests; use a new release version", release.Version)
	}

	var schema, revision int64
	err = tx.QueryRowContext(ctx, "SELECT TOP (1) schema_version, revision FROM dbo.saxbase_releases ORDER BY schema_version DESC, revision DESC").Scan(&schema, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if err == nil && (release.SchemaVersion < schema || (release.SchemaVersion == schema && release.Revision < revision)) {
		return 0, false, fmt.Errorf("release %s is older than the latest recorded release; use rollback %s", release.Version, release.Version)
	}
	if exists {
		return id, false, nil
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO dbo.saxbase_releases(version,schema_version,revision,object_count,fingerprint)
 OUTPUT INSERTED.id VALUES(@version,@schema,@revision,@count,@fingerprint);`, sql.Named("version", release.Version), sql.Named("schema", release.SchemaVersion), sql.Named("revision", release.Revision), sql.Named("count", len(files)), sql.Named("fingerprint", Fingerprint(files))).Scan(&id)
	if err != nil {
		return 0, false, fmt.Errorf("record release: %w", err)
	}
	return id, true, nil
}

// Fingerprint binds the ordered paths and exact SQL contents without retaining SQL.
func Fingerprint(files []File) string {
	entries := make([][2]string, 0, len(files))
	for _, file := range files {
		entries = append(entries, [2]string{file.Path, fmt.Sprintf("%x", sha256.Sum256([]byte(file.SQL)))})
	}
	data, _ := json.Marshal(entries)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func historyExists(ctx context.Context, db reader) (bool, error) {
	var id sql.NullInt64
	err := db.QueryRowContext(ctx, "SELECT OBJECT_ID(N'dbo.saxbase_releases', N'U')").Scan(&id)
	return id.Valid, err
}

func (s *store) History(ctx context.Context) ([]Release, error) {
	result := make([]Release, 0)
	exists, err := historyExists(ctx, s.db)
	if err != nil || !exists {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT version, schema_version, revision, deployed_at, object_count FROM dbo.saxbase_releases ORDER BY schema_version DESC, revision DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var release Release
		if err := rows.Scan(&release.Version, &release.SchemaVersion, &release.Revision, &release.DeployedAt, &release.ObjectCount); err != nil {
			return nil, err
		}
		result = append(result, release)
	}
	return result, rows.Err()
}

func (s *store) Snapshot(ctx context.Context, version string) (Snapshot, error) {
	var snapshot Snapshot
	exists, err := historyExists(ctx, s.db)
	if err != nil {
		return snapshot, err
	}
	if !exists {
		return snapshot, fmt.Errorf("release %s not found", version)
	}
	var id int64
	var fingerprint sql.NullString
	err = s.db.QueryRowContext(ctx, `IF COL_LENGTH(N'dbo.saxbase_releases', N'fingerprint') IS NULL
 SELECT id, version, schema_version, revision, deployed_at, object_count, CAST(NULL AS char(64)) AS fingerprint FROM dbo.saxbase_releases WHERE version=@version;
 ELSE EXEC sys.sp_executesql N'SELECT id, version, schema_version, revision, deployed_at, object_count, fingerprint FROM dbo.saxbase_releases WHERE version=@version', N'@version varchar(39)', @version=@version;`, sql.Named("version", version)).Scan(&id, &snapshot.Version, &snapshot.SchemaVersion, &snapshot.Revision, &snapshot.DeployedAt, &snapshot.ObjectCount, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, fmt.Errorf("release %s not found", version)
	}
	if err != nil {
		return snapshot, err
	}
	snapshot.Fingerprint = fingerprint.String
	return snapshot, err
}

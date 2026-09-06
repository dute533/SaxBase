package objects

import (
	"context"
	"crypto/sha256"
	"database/sql"
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
 UNIQUE(schema_version,revision)
 );
 IF OBJECT_ID(N'dbo.saxbase_release_objects', N'U') IS NULL
 CREATE TABLE dbo.saxbase_release_objects (
 release_id bigint NOT NULL REFERENCES dbo.saxbase_releases(id),
 path nvarchar(450) COLLATE Latin1_General_100_BIN2 NOT NULL,
 checksum char(64) NOT NULL,
 definition varbinary(max) NOT NULL,
 PRIMARY KEY NONCLUSTERED(release_id,path)
 );`

func prepareRelease(ctx context.Context, tx *sql.Tx, release Release, files []File) (int64, bool, error) {
	if _, err := tx.ExecContext(ctx, releaseTables); err != nil {
		return 0, false, fmt.Errorf("initialize release history: %w", err)
	}
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM dbo.saxbase_releases WHERE version=@version", sql.Named("version", release.Version)).Scan(&id)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if exists {
		snapshot, err := readSnapshotObjects(ctx, tx, id)
		if err != nil {
			return 0, false, err
		}
		if err := sameSnapshot(snapshot, files); err != nil {
			return 0, false, fmt.Errorf("release %s is immutable: %w", release.Version, err)
		}
	}
	var schema, revision int64
	err = tx.QueryRowContext(ctx, "SELECT TOP (1) schema_version, revision FROM dbo.saxbase_releases ORDER BY schema_version DESC, revision DESC").Scan(&schema, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if err == nil && (release.SchemaVersion < schema || (release.SchemaVersion == schema && release.Revision < revision)) {
		return 0, false, fmt.Errorf("release %s is older than the latest recorded release; use release rollback %s", release.Version, release.Version)
	}
	if exists {
		return id, false, nil
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO dbo.saxbase_releases(version,schema_version,revision,object_count)
 OUTPUT INSERTED.id VALUES(@version,@schema,@revision,@count);`, sql.Named("version", release.Version), sql.Named("schema", release.SchemaVersion), sql.Named("revision", release.Revision), sql.Named("count", len(files))).Scan(&id)
	if err != nil {
		return 0, false, fmt.Errorf("record release: %w", err)
	}
	return id, true, nil
}

func sameSnapshot(snapshot []SnapshotObject, files []File) error {
	if len(snapshot) != len(files) {
		return errors.New("object set differs")
	}
	byPath := make(map[string]SnapshotObject, len(snapshot))
	for _, object := range snapshot {
		byPath[object.Path] = object
	}
	for _, file := range files {
		object, ok := byPath[file.Path]
		if !ok || object.Checksum != file.Checksum || object.SQL != file.SQL {
			return fmt.Errorf("definition differs for %s", file.Path)
		}
	}
	return nil
}

func recordSnapshot(ctx context.Context, tx *sql.Tx, id int64, files []File) error {
	for _, file := range files {
		_, err := tx.ExecContext(ctx, `INSERT INTO dbo.saxbase_release_objects(release_id,path,checksum,definition)
 VALUES(@id,@path,@checksum,@definition);`, sql.Named("id", id), sql.Named("path", file.Path), sql.Named("checksum", file.Checksum), sql.Named("definition", []byte(file.SQL)))
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", file.Path, err)
		}
	}
	return nil
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

func readSnapshotObjects(ctx context.Context, db reader, id int64) ([]SnapshotObject, error) {
	rows, err := db.QueryContext(ctx, "SELECT path, checksum, definition FROM dbo.saxbase_release_objects WHERE release_id=@id ORDER BY path", sql.Named("id", id))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SnapshotObject, 0)
	for rows.Next() {
		var object SnapshotObject
		var definition []byte
		if err := rows.Scan(&object.Path, &object.Checksum, &definition); err != nil {
			return nil, err
		}
		object.SQL = string(definition)
		result = append(result, object)
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
	err = s.db.QueryRowContext(ctx, "SELECT id, version, schema_version, revision, deployed_at, object_count FROM dbo.saxbase_releases WHERE version=@version", sql.Named("version", version)).Scan(&id, &snapshot.Version, &snapshot.SchemaVersion, &snapshot.Revision, &snapshot.DeployedAt, &snapshot.ObjectCount)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, fmt.Errorf("release %s not found", version)
	}
	if err != nil {
		return snapshot, err
	}
	snapshot.Objects, err = readSnapshotObjects(ctx, s.db, id)
	return snapshot, err
}

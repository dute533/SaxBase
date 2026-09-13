package objects

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"saxbase/internal/migrations"
	"saxbase/internal/releaseversion"
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
 BEGIN
 CREATE TABLE dbo.saxbase_releases (
 version varchar(39) NOT NULL CONSTRAINT PK_saxbase_releases PRIMARY KEY,
 fingerprint char(64) NOT NULL,
 is_current bit NOT NULL CONSTRAINT DF_saxbase_releases_is_current DEFAULT 0
 );
 CREATE UNIQUE INDEX UX_saxbase_releases_current
 ON dbo.saxbase_releases(is_current) WHERE is_current=1;
 END;`

func prepareRelease(ctx context.Context, tx *sql.Tx, release Release, files []File, force bool) (bool, error) {
	requestedVersion, err := releaseversion.Parse(release.Version)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, releaseTables); err != nil {
		return false, fmt.Errorf("initialize release history: %w", err)
	}
	var fingerprint string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint FROM dbo.saxbase_releases WHERE version=@version", sql.Named("version", release.Version)).Scan(&fingerprint)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	requestedFingerprint := Fingerprint(files)
	if exists && fingerprint != requestedFingerprint {
		if !force {
			return false, fmt.Errorf("release %s is immutable; use a new release version or force a development rewrite", release.Version)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE dbo.saxbase_releases SET fingerprint=@fingerprint WHERE version=@version", sql.Named("fingerprint", requestedFingerprint), sql.Named("version", release.Version)); err != nil {
			return false, fmt.Errorf("rewrite release fingerprint: %w", err)
		}
	}
	if exists {
		return false, nil
	}

	rows, err := tx.QueryContext(ctx, "SELECT version FROM dbo.saxbase_releases")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var latest string
	var latestVersion releaseversion.Version
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return false, err
		}
		parsed, err := releaseversion.Parse(value)
		if err != nil {
			return false, fmt.Errorf("invalid recorded release version: %w", err)
		}
		if latest == "" || releaseversion.Compare(parsed, latestVersion) > 0 {
			latest, latestVersion = value, parsed
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if latest != "" && releaseversion.Compare(requestedVersion, latestVersion) < 0 {
		return false, fmt.Errorf("release %s is older than the latest recorded release %s; use rollback %s", release.Version, latest, release.Version)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO dbo.saxbase_releases(version,fingerprint) VALUES(@version,@fingerprint)", sql.Named("version", release.Version), sql.Named("fingerprint", requestedFingerprint)); err != nil {
		return false, fmt.Errorf("record release: %w", err)
	}
	return true, nil
}

// Fingerprint binds the ordered operations, paths, and exact SQL contents without
// retaining SQL. Commit IDs are deliberately excluded: identical release inputs
// should have the same identity regardless of which Git commit supplied them.
func Fingerprint(files []File) string {
	type entry struct {
		Operation string `json:"operation"`
		Path      string `json:"path"`
		SHA256    string `json:"sha256"`
	}
	entries := make([]entry, 0, len(files))
	for _, file := range files {
		operation := "apply"
		if file.Delete {
			operation = "delete"
		}
		entries = append(entries, entry{
			Operation: operation,
			Path:      file.Path,
			SHA256:    fmt.Sprintf("%x", sha256.Sum256([]byte(file.SQL))),
		})
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
	type parsedRelease struct {
		release Release
		version releaseversion.Version
	}
	parsed := make([]parsedRelease, 0)
	exists, err := historyExists(ctx, s.db)
	if err != nil || !exists {
		return []Release{}, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT version FROM dbo.saxbase_releases")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var release Release
		if err := rows.Scan(&release.Version); err != nil {
			return nil, err
		}
		version, err := releaseversion.Parse(release.Version)
		if err != nil {
			return nil, fmt.Errorf("invalid recorded release version: %w", err)
		}
		parsed = append(parsed, parsedRelease{release: release, version: version})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(parsed, func(i, j int) bool {
		return releaseversion.Compare(parsed[i].version, parsed[j].version) > 0
	})
	result := make([]Release, len(parsed))
	for i := range parsed {
		result[i] = parsed[i].release
	}
	return result, nil
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
	var fingerprint string
	err = s.db.QueryRowContext(ctx, "SELECT version, fingerprint FROM dbo.saxbase_releases WHERE version=@version", sql.Named("version", version)).Scan(&snapshot.Version, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, fmt.Errorf("release %s not found", version)
	}
	if err != nil {
		return snapshot, err
	}
	if _, err := releaseversion.Parse(snapshot.Version); err != nil {
		return snapshot, fmt.Errorf("invalid recorded release version: %w", err)
	}
	snapshot.Fingerprint = fingerprint
	return snapshot, err
}

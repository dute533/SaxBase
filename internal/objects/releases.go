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

func (s *store) checkReleaseSchema(ctx context.Context, tx *sql.Tx, want int64) error {
	current, err := migrations.VersionInTransaction(ctx, tx, s.dialect.name)
	if err != nil {
		return fmt.Errorf("read Goose version for release: %w", err)
	}
	if current != want {
		return fmt.Errorf("release requires Goose version %d; database is at %d", want, current)
	}
	return nil
}

func (s *store) prepareRelease(ctx context.Context, tx *sql.Tx, release Release, force bool) (bool, error) {
	if err := releaseversion.Validate(release.Version); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, s.dialect.initializeReleases); err != nil {
		return false, fmt.Errorf("initialize release history: %w", err)
	}
	var fingerprint string
	err := tx.QueryRowContext(ctx, s.dialect.selectFingerprint, s.dialect.versionArg(release.Version)...).Scan(&fingerprint)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	requestedFingerprint := release.Fingerprint
	if requestedFingerprint == "" {
		return false, errors.New("release fingerprint is required")
	}
	if exists && fingerprint != requestedFingerprint {
		if !force {
			return false, fmt.Errorf("release %s is immutable; use a new release version or force a development rewrite", release.Version)
		}
		if _, err := tx.ExecContext(ctx, s.dialect.updateFingerprint, s.dialect.fingerprintArgs(requestedFingerprint, release.Version)...); err != nil {
			return false, fmt.Errorf("rewrite release fingerprint: %w", err)
		}
	}
	if exists {
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, s.dialect.insertRelease, s.dialect.insertArgs(release.Version, requestedFingerprint)...); err != nil {
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

// ReleaseFingerprint binds a target Goose migration to the ordered object
// operations so neither part of an applied release can be rewritten silently.
func ReleaseFingerprint(migrationVersion int64, files []File) string {
	type releaseIdentity struct {
		MigrationVersion int64  `json:"migration_version"`
		Objects          string `json:"objects_sha256"`
	}
	data, _ := json.Marshal(releaseIdentity{MigrationVersion: migrationVersion, Objects: Fingerprint(files)})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *store) History(ctx context.Context) ([]Release, error) {
	var result []Release
	exists, err := s.dialect.historyExists(ctx, s.db)
	if err != nil || !exists {
		return []Release{}, err
	}
	rows, err := s.db.QueryContext(ctx, s.dialect.selectHistory)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var release Release
		if err := rows.Scan(&release.Version); err != nil {
			return nil, err
		}
		if err := releaseversion.Validate(release.Version); err != nil {
			return nil, fmt.Errorf("invalid recorded release version: %w", err)
		}
		result = append(result, release)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *store) Snapshot(ctx context.Context, version string) (Snapshot, error) {
	var snapshot Snapshot
	exists, err := s.dialect.historyExists(ctx, s.db)
	if err != nil {
		return snapshot, err
	}
	if !exists {
		return snapshot, fmt.Errorf("release %s not found", version)
	}
	var fingerprint string
	err = s.db.QueryRowContext(ctx, s.dialect.selectSnapshot, s.dialect.versionArg(version)...).Scan(&snapshot.Version, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, fmt.Errorf("release %s not found", version)
	}
	if err != nil {
		return snapshot, err
	}
	if err := releaseversion.Validate(snapshot.Version); err != nil {
		return snapshot, fmt.Errorf("invalid recorded release version: %w", err)
	}
	snapshot.Fingerprint = fingerprint
	return snapshot, err
}

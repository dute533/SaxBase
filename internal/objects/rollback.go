package objects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"saxbase/internal/deploymentlock"
	"saxbase/internal/migrations"
)

// Rollback describes one completed transition between adjacent releases.
type Rollback struct {
	SourceVersion string
	TargetVersion string
}

// Reconciled verifies the durable release pointer and fingerprint. Object
// changes and the final fingerprint are committed in the same transaction.
func (s *store) Reconciled(ctx context.Context, target Snapshot) (bool, error) {
	current, err := s.currentVersion(ctx, s.db)
	if err != nil || current != target.Version {
		return false, err
	}
	recorded, err := s.Snapshot(ctx, target.Version)
	return err == nil && recorded.Fingerprint == target.Fingerprint, err
}

// Reconcile completes a rollback step whose target release was made current
// before Goose or object restoration finished. Repeating it is safe: Goose
// resumes from its recorded version and repeatable DDL restores target state.
func (s *store) Reconcile(ctx context.Context, target Snapshot, goose migrations.Engine) (err error) {
	targetFiles, _, err := snapshotFiles(target)
	if err != nil {
		return err
	}
	conn, unlock, err := deploymentlock.Acquire(ctx, s.db, s.dialect.name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := s.verifyRollbackTarget(ctx, target); err != nil {
		return err
	}
	currentRelease, err := s.currentVersion(ctx, conn)
	if err != nil {
		return err
	}
	if currentRelease != target.Version {
		return fmt.Errorf("current release is %s, cannot reconcile %s", currentRelease, target.Version)
	}
	currentSchema, err := goose.Version(ctx)
	if err != nil {
		return err
	}
	if currentSchema < target.MigrationVersion {
		return fmt.Errorf("Goose version %d is below current release %s", currentSchema, target.Version)
	}
	if currentSchema > target.MigrationVersion {
		if err := goose.ValidateDownTo(ctx, target.MigrationVersion); err != nil {
			return fmt.Errorf("rollback preflight: %w", err)
		}
		if err := goose.DownTo(ctx, target.MigrationVersion); err != nil {
			return fmt.Errorf("Goose rollback may be partially applied; repair the migration and retry rollback %s: %w", target.Version, err)
		}
	}
	if err := rollbackTransaction(ctx, conn, func(tx *sql.Tx) error {
		if err := s.checkReleaseSchema(ctx, tx, target.MigrationVersion); err != nil {
			return err
		}
		if err := s.restoreFiles(ctx, tx, targetFiles); err != nil {
			return err
		}
		if err := s.setCurrentFingerprint(ctx, tx, target.Version, target.Fingerprint); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("reconcile release %s: %w", target.Version, err)
	}
	return nil
}

// Rollback performs one transition between adjacent releases. The caller walks
// longer rollback chains one release at a time, making every completed release
// a durable checkpoint.
func (s *store) Rollback(ctx context.Context, target, source Snapshot, goose migrations.Engine) (result Rollback, err error) {
	result = Rollback{SourceVersion: source.Version, TargetVersion: target.Version}
	if target.MigrationVersion > source.MigrationVersion {
		return result, errors.New("rollback target migration must not be newer than the current release")
	}
	targetFiles, targetIDs, err := snapshotFiles(target)
	if err != nil {
		return result, err
	}
	sourceFiles, sourceIDs, err := snapshotFiles(source)
	if err != nil {
		return result, err
	}
	targetByName := make(map[string]identity, len(targetIDs))
	for _, id := range targetIDs {
		targetByName[id.key()] = id
	}
	for _, id := range sourceIDs {
		if targetID, ok := targetByName[id.key()]; ok && targetID.kind != id.kind {
			return result, fmt.Errorf("object type changed for %s.%s; automatic rollback is unsupported", id.schema, id.name)
		}
	}
	conn, unlock, err := deploymentlock.Acquire(ctx, s.db, s.dialect.name)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	for _, supplied := range []Snapshot{target, source} {
		if err := s.verifySnapshot(ctx, supplied); err != nil {
			return result, err
		}
	}
	currentRelease, err := s.currentVersion(ctx, conn)
	if err != nil {
		return result, err
	}
	if currentRelease == "" {
		return result, errors.New("current state is unversioned; apply a manifest first")
	}
	if currentRelease != source.Version {
		return result, fmt.Errorf("source manifest must describe current release %s", currentRelease)
	}
	currentSchema, err := goose.Version(ctx)
	if err != nil {
		return result, err
	}
	if currentSchema != source.MigrationVersion {
		return result, fmt.Errorf("Goose version %d does not match current release %s", currentSchema, source.Version)
	}
	if source.MigrationVersion != target.MigrationVersion {
		if err := goose.ValidateDownTo(ctx, target.MigrationVersion); err != nil {
			return result, fmt.Errorf("rollback preflight: %w", err)
		}
	}
	dropExtra := func(tx *sql.Tx) error {
		for i := len(sourceIDs) - 1; i >= 0; i-- {
			if _, keep := targetByName[sourceIDs[i].key()]; keep {
				continue
			}
			if _, err := tx.ExecContext(ctx, s.dialect.drop(sourceIDs[i])); err != nil {
				return fmt.Errorf("drop %s: %w", sourceFiles[i].Path, err)
			}
		}
		return nil
	}
	if source.MigrationVersion != target.MigrationVersion {
		// The target pointer and blocker removal commit together. A crash before
		// this transaction leaves the source intact; a crash after it is visible
		// as target release + source Goose version.
		if err := rollbackTransaction(ctx, conn, func(tx *sql.Tx) error {
			if err := dropExtra(tx); err != nil {
				return err
			}
			return s.setCurrentFingerprint(ctx, tx, target.Version, rollbackMarker(target.Fingerprint))
		}); err != nil {
			return result, fmt.Errorf("prepare rollback to %s: %w", target.Version, err)
		}
		if err := goose.DownTo(ctx, target.MigrationVersion); err != nil {
			return result, fmt.Errorf("Goose rollback may be partially applied; repair the migration and retry rollback %s: %w", target.Version, err)
		}
	}
	if err := rollbackTransaction(ctx, conn, func(tx *sql.Tx) error {
		if err := s.checkReleaseSchema(ctx, tx, target.MigrationVersion); err != nil {
			return err
		}
		if err := s.restoreFiles(ctx, tx, targetFiles); err != nil {
			return err
		}
		if err := dropExtra(tx); err != nil {
			return err
		}
		if err := s.setCurrentFingerprint(ctx, tx, target.Version, target.Fingerprint); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return result, fmt.Errorf("restore release %s: %w", target.Version, err)
	}
	return result, nil
}

func (s *store) verifySnapshot(ctx context.Context, supplied Snapshot) error {
	recorded, err := s.Snapshot(ctx, supplied.Version)
	if err != nil {
		return err
	}
	if recorded.Fingerprint == "" || recorded.Fingerprint != supplied.Fingerprint {
		return fmt.Errorf("manifest for release %s does not match its recorded fingerprint", supplied.Version)
	}
	return nil
}

func (s *store) verifyRollbackTarget(ctx context.Context, supplied Snapshot) error {
	recorded, err := s.Snapshot(ctx, supplied.Version)
	if err != nil {
		return err
	}
	if recorded.Fingerprint != supplied.Fingerprint && recorded.Fingerprint != rollbackMarker(supplied.Fingerprint) {
		return fmt.Errorf("manifest for release %s does not match its recorded fingerprint", supplied.Version)
	}
	return nil
}

func (s *store) restoreFiles(ctx context.Context, tx *sql.Tx, files []File) error {
	for _, file := range files {
		if _, err := tx.ExecContext(ctx, s.dialect.restore(file.SQL)); err != nil {
			return fmt.Errorf("restore %s: %w", file.Path, err)
		}
	}
	return nil
}

func rollbackTransaction(ctx context.Context, conn *sql.Conn, fn func(*sql.Tx) error) error {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

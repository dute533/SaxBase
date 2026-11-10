package objects

import (
	"context"
	"errors"
	"fmt"

	"saxbase/internal/deploymentlock"
	"saxbase/internal/migrations"
)

// Apply holds one session lock across preflight, Goose, and the object
// transaction. Preflight must inspect the intended release without writing.
func (s *store) Apply(ctx context.Context, files, baseline []File, release Release, force bool, goose migrations.Engine, preflight func(context.Context) error) (rows []Status, err error) {
	if release.Version == "" || release.MigrationVersion < 0 || release.Fingerprint == "" || preflight == nil {
		return nil, errors.New("apply requires a valid release and preflight")
	}
	if err := validateFiles(files); err != nil {
		return nil, err
	}
	if err := validateFiles(baseline); err != nil {
		return nil, fmt.Errorf("invalid release baseline: %w", err)
	}
	conn, unlock, err := deploymentlock.Acquire(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := preflight(ctx); err != nil {
		return nil, err
	}
	inspection, err := goose.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	if inspection.Version > release.MigrationVersion {
		return nil, errors.New("apply cannot downgrade the schema; use rollback")
	}
	if inspection.Version < release.MigrationVersion {
		// Invalidate before Goose: even a failed nontransactional migration can
		// change the database without advancing its recorded schema version.
		if err := deploymentlock.Invalidate(ctx, conn); err != nil {
			return nil, err
		}
		if err := goose.UpTo(ctx, release.MigrationVersion); err != nil {
			return nil, fmt.Errorf("apply migrations: %w; completed migrations remain, fix the failure and retry apply", err)
		}
	}
	actual, err := goose.Version(ctx)
	if err != nil {
		return nil, err
	}
	if actual != release.MigrationVersion {
		return nil, fmt.Errorf("apply expected Goose version %d, got %d", release.MigrationVersion, actual)
	}
	rows, err = s.applyOn(ctx, conn, files, baseline, &release, force, false)
	if err != nil {
		return nil, fmt.Errorf("apply objects at Goose version %d: %w; release success was not confirmed; completed Goose migrations remain, inspect the failure and retry apply", release.MigrationVersion, err)
	}
	return rows, nil
}

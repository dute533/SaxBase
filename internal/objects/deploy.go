package objects

import (
	"context"
	"errors"
	"fmt"

	"saxbase/internal/deploymentlock"
	"saxbase/internal/migrations"
)

// Deploy holds one session lock across preflight, Goose, and the object
// transaction. Preflight must inspect the intended release without writing.
func (s *store) Deploy(ctx context.Context, files []File, schema, revision int64, goose migrations.Engine, preflight func(context.Context) error) (rows []Status, err error) {
	if schema < 0 || revision < 0 || preflight == nil {
		return nil, errors.New("apply requires a valid release and preflight")
	}
	if err := validateFiles(files); err != nil {
		return nil, err
	}
	conn, unlock, err := deploymentlock.Acquire(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := deploymentlock.CheckPending(ctx, conn); err != nil {
		return nil, err
	}
	if err := preflight(ctx); err != nil {
		return nil, err
	}
	inspection, err := goose.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	if inspection.Version > schema {
		return nil, errors.New("apply cannot downgrade the schema; use rollback")
	}
	if inspection.Version < schema {
		// Invalidate before Goose: even a failed nontransactional migration can
		// change the database without advancing its recorded schema version.
		if err := deploymentlock.Invalidate(ctx, conn); err != nil {
			return nil, err
		}
		if err := goose.UpTo(ctx, schema); err != nil {
			return nil, fmt.Errorf("apply migrations: %w; completed migrations remain, fix the failure and retry apply", err)
		}
	}
	actual, err := goose.Version(ctx)
	if err != nil {
		return nil, err
	}
	if actual != schema {
		return nil, fmt.Errorf("apply expected Goose version %d, got %d", schema, actual)
	}
	version := fmt.Sprint(schema)
	if revision > 0 {
		version += fmt.Sprintf(".%d", revision)
	}
	rows, err = s.applyOn(ctx, conn, files, &Release{Version: version, SchemaVersion: schema, Revision: revision}, false)
	if err != nil {
		return nil, fmt.Errorf("apply objects at Goose version %d: %w; release success was not confirmed; completed Goose migrations remain, inspect the failure and retry apply", schema, err)
	}
	return rows, nil
}

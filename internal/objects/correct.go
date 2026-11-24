package objects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"saxbase/internal/deploymentlock"
)

// Correct re-applies the given object files (drift correction) and records a
// fresh schema fingerprint, all in one transaction.
func (s *store) Correct(ctx context.Context, files []File) (rows []Status, err error) {
	conn, unlock, err := deploymentlock.Acquire(ctx, s.db, s.dialect.name)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	err = rollbackTransaction(ctx, conn, func(tx *sql.Tx) error {
		for _, file := range files {
			if file.Delete {
				continue
			}
			if _, err := tx.ExecContext(ctx, s.dialect.restore(file.SQL)); err != nil {
				return fmt.Errorf("correct %s: %w", file.Path, err)
			}
			rows = append(rows, Status{Path: file.Path, State: "corrected", Checksum: file.Checksum})
		}
		return s.recordFingerprint(ctx, tx)
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

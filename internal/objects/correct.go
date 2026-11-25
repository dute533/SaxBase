package objects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"saxbase/internal/deploymentlock"
)

// CorrectSQL applies correction statements in one transaction.
func (s *store) CorrectSQL(ctx context.Context, statements []string) (err error) {
	conn, unlock, err := deploymentlock.Acquire(ctx, s.db, s.dialect.name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return rollbackTransaction(ctx, conn, func(tx *sql.Tx) error {
		for _, stmt := range statements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("correct: %w", err)
			}
		}
		return nil
	})
}

package objects

import (
	"context"
	"database/sql"
	"strings"

	"saxbase/internal/deploymentlock"
)

func (s *store) setCurrent(ctx context.Context, db deploymentlock.Reader, version string) error {
	if _, err := db.ExecContext(ctx, s.dialect.clearCurrent); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, s.dialect.setCurrent, s.dialect.versionArg(version)...)
	return err
}

func (s *store) setCurrentFingerprint(ctx context.Context, db deploymentlock.Reader, version, fingerprint string) error {
	if _, err := db.ExecContext(ctx, s.dialect.updateFingerprint, s.dialect.fingerprintArgs(fingerprint, version)...); err != nil {
		return err
	}
	return s.setCurrent(ctx, db, version)
}

func rollbackMarker(fingerprint string) string {
	marker := strings.ToUpper(fingerprint)
	if marker == fingerprint && len(marker) > 0 {
		marker = "G" + marker[1:]
	}
	return marker
}

// IsRollbackCheckpoint distinguishes SaxBase's temporary rollback marker from
// an intentionally changed manifest fingerprint.
func IsRollbackCheckpoint(recorded, fingerprint string) bool {
	return recorded == rollbackMarker(fingerprint)
}

func (s *store) currentVersion(ctx context.Context, db reader) (string, error) {
	exists, err := s.dialect.historyExists(ctx, db)
	if err != nil || !exists {
		return "", err
	}
	var version sql.NullString
	err = db.QueryRowContext(ctx, s.dialect.selectCurrent).Scan(&version)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return version.String, nil
}

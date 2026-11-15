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
	version, _, err := s.currentPosition(ctx, db)
	return version, err
}

// An empty-version metadata row records an explicit initial-state checkpoint.
// It is not a release and is excluded from release history.
func (s *store) currentPosition(ctx context.Context, db reader) (string, bool, error) {
	exists, err := s.dialect.historyExists(ctx, db)
	if err != nil || !exists {
		return "", false, err
	}
	var version sql.NullString
	err = db.QueryRowContext(ctx, s.dialect.selectCurrent).Scan(&version)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return version.String, version.Valid && version.String == "", nil
}

// InitialSnapshot describes schema zero with no managed objects.
func InitialSnapshot() Snapshot {
	return Snapshot{Release: Release{Fingerprint: ReleaseFingerprint(0, nil)}}
}

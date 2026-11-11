package objects

import (
	"context"
	"database/sql"
	"saxbase/internal/deploymentlock"
)

func setCurrent(ctx context.Context, db deploymentlock.Reader, version string) error {
	_, err := db.ExecContext(ctx, `UPDATE dbo.saxbase_releases SET is_current=0;
 UPDATE dbo.saxbase_releases SET is_current=1 WHERE version=@version;`, sql.Named("version", version))
	return err
}

func currentVersion(ctx context.Context, db reader) (string, error) {
	exists, err := historyExists(ctx, db)
	if err != nil || !exists {
		return "", err
	}
	var version sql.NullString
	err = db.QueryRowContext(ctx, "SELECT version FROM dbo.saxbase_releases WHERE is_current=1").Scan(&version)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return version.String, nil
}

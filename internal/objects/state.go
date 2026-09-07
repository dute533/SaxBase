package objects

import (
	"context"
	"database/sql"
	"fmt"
	"saxbase/internal/deploymentlock"
)

func setCurrent(ctx context.Context, db deploymentlock.Reader, version string) error {
	_, err := db.ExecContext(ctx, `IF COL_LENGTH(N'dbo.saxbase_releases',N'is_current') IS NULL
 ALTER TABLE dbo.saxbase_releases ADD is_current bit NOT NULL CONSTRAINT DF_saxbase_releases_is_current DEFAULT 0;
 UPDATE dbo.saxbase_releases SET is_current=0;
 UPDATE dbo.saxbase_releases SET is_current=1 WHERE version=@version;`, sql.Named("version", version))
	return err
}

func currentVersion(ctx context.Context, db reader) (string, error) {
	exists, err := historyExists(ctx, db)
	if err != nil || !exists {
		return "", err
	}
	var version sql.NullString
	err = db.QueryRowContext(ctx, `IF COL_LENGTH(N'dbo.saxbase_releases',N'is_current') IS NULL
   SELECT TOP (1) version FROM dbo.saxbase_releases ORDER BY schema_version DESC, revision DESC;
 ELSE
   SELECT TOP (1) version FROM dbo.saxbase_releases WHERE is_current=1 ORDER BY schema_version DESC, revision DESC;`).Scan(&version)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return version.String, nil
}

func (s *store) Current(ctx context.Context) (string, error) {
	if err := deploymentlock.CheckPending(ctx, s.db); err != nil {
		return "", err
	}
	version, err := currentVersion(ctx, s.db)
	if err != nil {
		return "", err
	}
	if version == "" {
		return "", fmt.Errorf("current state is not associated with a release; apply a manifest first")
	}
	return version, nil
}

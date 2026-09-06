package objects

import (
	"context"
	"database/sql"
	"fmt"
	"saxbase/internal/deploymentlock"
)

const stateTable = `IF OBJECT_ID(N'dbo.saxbase_release_state',N'U') IS NULL
 CREATE TABLE dbo.saxbase_release_state(id int NOT NULL PRIMARY KEY CHECK(id=1),version varchar(39) NULL);`

func setCurrent(ctx context.Context, db deploymentlock.Reader, version string) error {
	if _, err := db.ExecContext(ctx, stateTable); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `UPDATE dbo.saxbase_release_state SET version=@version WHERE id=1;
 IF @@ROWCOUNT=0 INSERT INTO dbo.saxbase_release_state(id,version) VALUES(1,@version);`, sql.Named("version", version))
	return err
}

func currentVersion(ctx context.Context, db reader) (string, error) {
	var id sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT OBJECT_ID(N'dbo.saxbase_release_state',N'U')").Scan(&id); err != nil {
		return "", err
	}
	if id.Valid {
		var version sql.NullString
		err := db.QueryRowContext(ctx, "SELECT version FROM dbo.saxbase_release_state WHERE id=1").Scan(&version)
		if err == sql.ErrNoRows {
			return "", nil
		}
		return version.String, err
	}
	exists, err := historyExists(ctx, db)
	if err != nil || !exists {
		return "", err
	}
	var version string
	err = db.QueryRowContext(ctx, "SELECT TOP (1) version FROM dbo.saxbase_releases ORDER BY schema_version DESC, revision DESC").Scan(&version)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return version, err
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
		return "", fmt.Errorf("current state is not associated with a release; deploy a manifest first")
	}
	return version, nil
}

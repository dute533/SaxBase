package objects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"saxbase/internal/deploymentlock"
	"saxbase/internal/migrations"
)

type Rollback struct {
	ID            int64      `json:"id"`
	SourceVersion string     `json:"source"`
	TargetVersion string     `json:"target"`
	Phase         string     `json:"phase"`
	Status        string     `json:"status"`
	LastError     string     `json:"error,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

const rollbackTable = `IF OBJECT_ID(N'dbo.saxbase_rollbacks',N'U') IS NULL
 CREATE TABLE dbo.saxbase_rollbacks(
 id bigint IDENTITY(1,1) NOT NULL PRIMARY KEY,
 source_version varchar(39) NOT NULL,target_version varchar(39) NOT NULL,
 phase varchar(32) NOT NULL,status varchar(16) NOT NULL,
 last_error nvarchar(max) NULL,started_at datetime2 NOT NULL DEFAULT SYSUTCDATETIME(),completed_at datetime2 NULL);`
const rollbackColumns = "id,source_version,target_version,phase,status,COALESCE(last_error,N''),started_at,completed_at"

func scanRollback(row interface{ Scan(...any) error }) (Rollback, error) {
	var r Rollback
	err := row.Scan(&r.ID, &r.SourceVersion, &r.TargetVersion, &r.Phase, &r.Status, &r.LastError, &r.StartedAt, &r.CompletedAt)
	return r, err
}

func (s *store) Rollbacks(ctx context.Context) ([]Rollback, error) {
	result := make([]Rollback, 0)
	var id sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT OBJECT_ID(N'dbo.saxbase_rollbacks',N'U')").Scan(&id); err != nil {
		return nil, err
	}
	if !id.Valid {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+rollbackColumns+" FROM dbo.saxbase_rollbacks ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRollback(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *store) Rollback(ctx context.Context, target, source Snapshot, goose migrations.Engine) (result Rollback, err error) {
	version := target.Version
	targetFiles, targetIDs, err := snapshotFiles(target)
	if err != nil {
		return result, err
	}
	sourceFiles, sourceIDs, err := snapshotFiles(source)
	if err != nil {
		return result, err
	}
	conn, unlock, err := deploymentlock.Acquire(ctx, s.db)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	for _, supplied := range []Snapshot{target, source} {
		recorded, err := s.Snapshot(ctx, supplied.Version)
		if err != nil {
			return result, err
		}
		if recorded.Fingerprint == "" || recorded.Fingerprint != supplied.Fingerprint || recorded.SchemaVersion != supplied.SchemaVersion || recorded.Revision != supplied.Revision {
			return result, fmt.Errorf("manifest for release %s does not match its recorded fingerprint", supplied.Version)
		}
	}
	if _, err := conn.ExecContext(ctx, rollbackTable); err != nil {
		return result, err
	}
	result, err = scanRollback(conn.QueryRowContext(ctx, "SELECT TOP (1) "+rollbackColumns+" FROM dbo.saxbase_rollbacks WHERE status <> 'completed' ORDER BY id DESC"))
	resuming := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if resuming && result.TargetVersion != version {
		return result, fmt.Errorf("rollback to %s is incomplete; retry that target first", result.TargetVersion)
	}
	sourceVersion := result.SourceVersion
	if !resuming {
		sourceVersion, err = currentVersion(ctx, conn)
		if err != nil {
			return result, err
		}
		if sourceVersion == "" {
			return result, errors.New("current state is unversioned; deploy a manifest before rolling back")
		}
	}
	if source.Version != sourceVersion {
		return result, fmt.Errorf("source manifest must describe release %s", sourceVersion)
	}
	if target.SchemaVersion > source.SchemaVersion || (target.SchemaVersion == source.SchemaVersion && target.Revision > source.Revision) {
		return result, errors.New("rollback target is newer than the current release")
	}
	targetByName := map[string]identity{}
	for _, id := range targetIDs {
		targetByName[id.key()] = id
	}
	for _, id := range sourceIDs {
		if targetID, ok := targetByName[id.key()]; ok && targetID.kind != id.kind {
			return result, fmt.Errorf("object type changed for %s.%s; automatic rollback is unsupported", id.schema, id.name)
		}
	}
	current, err := goose.Version(ctx)
	if err != nil {
		return result, err
	}
	if !resuming && current != source.SchemaVersion {
		return result, fmt.Errorf("Goose version %d does not match current release %s", current, sourceVersion)
	}
	if current < target.SchemaVersion || current > source.SchemaVersion {
		return result, fmt.Errorf("Goose version %d is outside rollback range %d..%d", current, target.SchemaVersion, source.SchemaVersion)
	}
	if !resuming || result.Phase == "started" {
		deployed, err := read(ctx, conn)
		if err != nil {
			return result, err
		}
		if len(deployed) != len(sourceFiles) {
			return result, errors.New("tracked object set differs from current release; deploy a manifest first")
		}
		for _, file := range sourceFiles {
			if deployed[file.Path] != file.Checksum {
				return result, fmt.Errorf("tracked object %s differs from current release", file.Path)
			}
		}
	}
	if current > target.SchemaVersion {
		if err := goose.ValidateDownTo(ctx, target.SchemaVersion); err != nil {
			return result, fmt.Errorf("rollback preflight: %w", err)
		}
	}
	if !resuming {
		result = Rollback{SourceVersion: sourceVersion, TargetVersion: version, Phase: "started", Status: "running"}
		err = conn.QueryRowContext(ctx, `INSERT INTO dbo.saxbase_rollbacks(source_version,target_version,phase,status)
 OUTPUT INSERTED.id,INSERTED.started_at VALUES(@source,@target,'started','running');`, sql.Named("source", sourceVersion), sql.Named("target", version)).Scan(&result.ID, &result.StartedAt)
		if err != nil {
			return result, err
		}
	} else {
		if _, err := conn.ExecContext(ctx, "UPDATE dbo.saxbase_rollbacks SET status='running',last_error=NULL WHERE id=@id", sql.Named("id", result.ID)); err != nil {
			return result, err
		}
		result.Status = "running"
		result.LastError = ""
	}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, recordErr := conn.ExecContext(cleanup, "UPDATE dbo.saxbase_rollbacks SET status='failed',last_error=@error WHERE id=@id", sql.Named("error", err.Error()), sql.Named("id", result.ID))
			result.Status = "failed"
			result.LastError = err.Error()
			err = errors.Join(err, recordErr)
		}
	}()
	// Remove only objects absent from the target; retained objects keep permissions.
	dropExtra := func(tx *sql.Tx) error {
		for i := len(sourceIDs) - 1; i >= 0; i-- {
			if _, keep := targetByName[sourceIDs[i].key()]; keep {
				continue
			}
			if _, err := tx.ExecContext(ctx, sourceIDs[i].drop()); err != nil {
				return fmt.Errorf("drop %s: %w", sourceFiles[i].Path, err)
			}
		}
		return nil
	}
	if source.SchemaVersion != target.SchemaVersion {
		if result.Phase == "started" {
			err = rollbackTransaction(ctx, conn, func(tx *sql.Tx) error {
				if err := dropExtra(tx); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, "UPDATE dbo.saxbase_rollbacks SET phase='objects_removed',status='running',last_error=NULL WHERE id=@id", sql.Named("id", result.ID))
				return err
			})
			if err != nil {
				return result, fmt.Errorf("rollback object-removal phase: %w", err)
			}
			result.Phase = "objects_removed"
		}
		if current > target.SchemaVersion {
			if err := goose.DownTo(ctx, target.SchemaVersion); err != nil {
				return result, fmt.Errorf("Goose rollback may be partially applied; repair the migration and retry release rollback %s: %w", version, err)
			}
		}
		if _, err := conn.ExecContext(ctx, "UPDATE dbo.saxbase_rollbacks SET phase='schema_rolled_back',status='running',last_error=NULL WHERE id=@id", sql.Named("id", result.ID)); err != nil {
			return result, err
		}
		result.Phase = "schema_rolled_back"
	}
	err = rollbackTransaction(ctx, conn, func(tx *sql.Tx) error {
		if err := checkReleaseSchema(ctx, tx, target.SchemaVersion); err != nil {
			return err
		}
		// Always restore the SQL resolved from the target manifest commits.
		for _, file := range targetFiles {
			if _, err := tx.ExecContext(ctx, restoreDefinition(file.SQL)); err != nil {
				return fmt.Errorf("restore %s: %w", file.Path, err)
			}
		}
		if err := dropExtra(tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM dbo.saxbase_objects"); err != nil {
			return err
		}
		for _, file := range targetFiles {
			if _, err := tx.ExecContext(ctx, "INSERT INTO dbo.saxbase_objects(path,checksum) VALUES(@path,@checksum)", sql.Named("path", file.Path), sql.Named("checksum", file.Checksum)); err != nil {
				return err
			}
		}
		if err := setCurrent(ctx, tx, version); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE dbo.saxbase_rollbacks SET phase='restored',status='completed',last_error=NULL,completed_at=SYSUTCDATETIME() WHERE id=@id", sql.Named("id", result.ID))
		return err
	})
	if err != nil {
		return result, fmt.Errorf("restore failed; schema may already be at %d; retry release rollback %s after resolving the error: %w", target.SchemaVersion, version, err)
	}
	result.Phase = "restored"
	result.Status = "completed"
	result.LastError = ""
	return result, nil
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

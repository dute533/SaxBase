package cli

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

// withDatabase returns dsn with the database name replaced, supporting both
// URL-style (sqlserver://...?database=x) and ADO-style (Database=x) DSNs.
func withDatabase(dsn, name string) string {
	if strings.HasPrefix(dsn, "sqlserver://") {
		if u, err := url.Parse(dsn); err == nil {
			q := u.Query()
			q.Set("database", name)
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	parts := strings.Split(dsn, ";")
	for i, part := range parts {
		key, _, found := strings.Cut(part, "=")
		if found && (strings.EqualFold(strings.TrimSpace(key), "Database") || strings.EqualFold(strings.TrimSpace(key), "Initial Catalog")) {
			parts[i] = key + "=" + name
			return strings.Join(parts, ";")
		}
	}
	return dsn + ";Database=" + name
}

// replayDescribed applies the release to a scratch database on the same server
// and returns the resulting schema fingerprint: what the release describes.
// It is a variable so tests can stub it.
var replayDescribed = func(ctx context.Context, cfg migrations.Config, release resolvedRelease, open OpenFunc, openObjects OpenObjectsFunc) (described string, err error) {
	withDB := func(name string) string { return withDatabase(cfg.DSN, name) }
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	name := "saxbase_drift_" + hex.EncodeToString(suffix[:])
	master, err := sql.Open("sqlserver", withDB("master"))
	if err != nil {
		return "", err
	}
	defer master.Close()
	if _, err := master.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		return "", fmt.Errorf("create scratch database: %w", err)
	}
	defer func() {
		dropCtx := context.Background()
		master.ExecContext(dropCtx, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		if _, dropErr := master.ExecContext(dropCtx, "DROP DATABASE ["+name+"]"); dropErr != nil {
			err = errors.Join(err, fmt.Errorf("drop scratch database %s: %w", name, dropErr))
		}
	}()
	scratchCfg := cfg
	scratchCfg.DSN = withDB(name)
	goose, err := open(scratchCfg)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, goose.Close()) }()
	migrationVersion, err := release.manifest.TargetMigration()
	if err != nil {
		return "", err
	}
	if err := goose.UpTo(ctx, migrationVersion); err != nil {
		return "", fmt.Errorf("replay migrations in scratch database: %w", err)
	}
	db, err := openObjects(scratchCfg)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	rel := objects.Release{Version: release.manifest.Version, MigrationVersion: migrationVersion, Fingerprint: release.manifest.Fingerprint(release.state)}
	if _, err := db.Apply(ctx, release.state, nil, rel, false, goose, func(context.Context) error { return nil }); err != nil {
		return "", fmt.Errorf("replay objects in scratch database: %w", err)
	}
	report, err := db.Drift(ctx, nil)
	if err != nil {
		return "", err
	}
	return report.SchemaCurrent, nil
}

// runDrift is read-only against the target database: it compares the live
// database against the recorded release (schema fingerprint plus managed
// object definitions) and against what the release describes (replayed in a
// scratch database).
func runDrift(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, open OpenFunc, openObjects OpenObjectsFunc) (err error) {
	if manifestPath == "" {
		manifestPath = "database/release.json"
	}
	history, err := resolveReleaseHistory(ctx, manifestPath, objectDir)
	if err != nil {
		return err
	}
	db, err := openObjects(cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	state, err := db.Inspect(ctx)
	if err != nil {
		return err
	}
	if state.Current == "" {
		return withNext(errors.New("no release is recorded in this database"), "saxbase apply")
	}
	index := releaseIndex(history, state.Current)
	if index < 0 {
		return withNext(fmt.Errorf("current release %s is not in manifest history", state.Current), "restore a manifest containing release "+state.Current)
	}
	report, err := db.Drift(ctx, history[index].state)
	if err != nil {
		return err
	}
	described, replayErr := replayDescribed(ctx, cfg, history[index], open, openObjects)
	if replayErr != nil {
		return replayErr
	}
	var describedDiff []string
	if described != report.SchemaCurrent {
		describedDiff = objects.DiffDescribed(described, report.SchemaCurrent)
	}
	if !report.Drifted() && len(describedDiff) == 0 {
		fmt.Fprintln(out, "No drift detected.")
		return nil
	}
	if report.SchemaStored == "" {
		fmt.Fprintln(out, "Schema fingerprint: unrecorded (release predates drift tracking; reapply with saxbase -f apply on a dev database)")
	} else if report.SchemaStored != report.SchemaCurrent {
		fmt.Fprintln(out, "Schema fingerprint: DRIFTED")
		for _, line := range report.SchemaDiff {
			fmt.Fprintln(out, line)
		}
	}
	if len(describedDiff) > 0 {
		fmt.Fprintf(out, "Release-described schema (%s): DRIFTED\n", state.Current)
		for _, line := range describedDiff {
			fmt.Fprintln(out, line)
		}
	}
	for _, row := range report.Objects {
		if row.State != "ok" {
			fmt.Fprintf(out, "%s: %s\n", row.Path, row.State)
		}
	}
	return errors.New("drift detected")
}

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

	"saxbase/internal/dialect"
	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

// withDatabase returns dsn with the database name replaced, supporting
// URL-style DSNs (sqlserver://...?database=x, postgres://.../x) and
// keyword-style DSNs (Database=x / dbname=x).
func withDatabase(dsn, name string) string {
	if strings.Contains(dsn, "://") {
		if u, err := url.Parse(dsn); err == nil {
			if u.Scheme == "sqlserver" {
				q := u.Query()
				q.Set("database", name)
				u.RawQuery = q.Encode()
			} else {
				u.Path = "/" + name
			}
			return u.String()
		}
	}
	sep := ";"
	if !strings.Contains(dsn, ";") && strings.Contains(dsn, " ") {
		sep = " "
	}
	parts := strings.Split(dsn, sep)
	for i, part := range parts {
		key, _, found := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		if found && (strings.EqualFold(key, "Database") || strings.EqualFold(key, "Initial Catalog") || strings.EqualFold(key, "dbname")) {
			parts[i] = key + "=" + name
			return strings.Join(parts, sep)
		}
	}
	if sep == ";" {
		return dsn + ";Database=" + name
	}
	return dsn + " dbname=" + name
}

// replayScratch applies the release to a scratch database on the same server
// and returns a live handle plus the resulting schema fingerprint: what the
// release describes. The caller must call drop.
// replayScratch applies the release to a scratch database on the same server
// and returns a live handle plus the resulting schema fingerprint: what the
// release describes. The caller must call drop.
var replayScratch = func(ctx context.Context, cfg migrations.Config, release resolvedRelease, open OpenFunc, openObjects OpenObjectsFunc) (scratch *sql.DB, described string, drop func(), err error) {
	driver, maintenance := "sqlserver", "master"
	if cfg.Dialect == dialect.Postgres {
		driver, maintenance = "pgx", "postgres"
	}
	withDB := func(name string) string { return withDatabase(cfg.DSN, name) }
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, "", nil, err
	}
	name := "saxbase_drift_" + hex.EncodeToString(suffix[:])
	master, err := sql.Open(driver, withDB(maintenance))
	if err != nil {
		return nil, "", nil, err
	}
	create := "CREATE DATABASE [" + name + "]"
	dropSQL := "ALTER DATABASE [" + name + "] SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE [" + name + "]"
	if cfg.Dialect == dialect.Postgres {
		create = `CREATE DATABASE "` + name + `"`
		dropSQL = `DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`
	}
	if _, err := master.ExecContext(ctx, create); err != nil {
		master.Close()
		return nil, "", nil, fmt.Errorf("create scratch database: %w", err)
	}
	drop = func() {
		defer master.Close()
		if _, dropErr := master.ExecContext(context.Background(), dropSQL); dropErr != nil && err == nil {
			err = fmt.Errorf("drop scratch database %s: %w", name, dropErr)
		}
	}
	fail := func(e error) (*sql.DB, string, func(), error) {
		drop()
		return nil, "", nil, e
	}
	scratchCfg := cfg
	scratchCfg.DSN = withDB(name)
	goose, err := open(scratchCfg)
	if err != nil {
		return fail(err)
	}
	defer func() { err = errors.Join(err, goose.Close()) }()
	migrationVersion, err := release.manifest.TargetMigration()
	if err != nil {
		return fail(err)
	}
	if err := goose.UpTo(ctx, migrationVersion); err != nil {
		return fail(fmt.Errorf("replay migrations in scratch database: %w", err))
	}
	db, err := openObjects(scratchCfg)
	if err != nil {
		return fail(err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	rel := objects.Release{Version: release.manifest.Version, MigrationVersion: migrationVersion, Fingerprint: release.manifest.Fingerprint(release.state)}
	if _, err := db.Apply(ctx, release.state, nil, rel, false, goose, func(context.Context) error { return nil }); err != nil {
		return fail(fmt.Errorf("replay objects in scratch database: %w", err))
	}
	report, err := db.Drift(ctx, nil)
	if err != nil {
		return fail(err)
	}
	scratch, err = sql.Open(driver, scratchCfg.DSN)
	if err != nil {
		return fail(err)
	}
	return scratch, report.SchemaCurrent, drop, nil
}

var replayDescribed = func(ctx context.Context, cfg migrations.Config, release resolvedRelease, open OpenFunc, openObjects OpenObjectsFunc) (string, error) {
	scratch, described, drop, err := replayScratch(ctx, cfg, release, open, openObjects)
	if err != nil {
		return "", err
	}
	defer func() {
		scratch.Close()
		drop()
	}()
	return described, nil
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
		describedDiff = objects.DiffDescribed(described, report.SchemaCurrent, history[index].state)
	}
	if !report.Drifted() && len(describedDiff) == 0 {
		fmt.Fprintln(out, "No drift detected.")
		return nil
	}
	if report.SchemaStored == "" {
		fmt.Fprintln(out, "Schema fingerprint: unrecorded (release predates drift tracking; reapply with saxbase -f apply on a dev database)")
	} else if len(report.SchemaDiff) > 0 {
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

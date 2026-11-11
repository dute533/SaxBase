package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

// runStatus presents the structural migration, release, and object state
// together so users do not need separate commands for one database.
func runStatus(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error)) (err error) {
	explicitManifest := manifestPath != ""
	if manifestPath == "" {
		manifestPath = "database/release.json"
	}
	var files []objects.File
	var history []resolvedRelease
	var current string
	if _, loadErr := releases.Load(manifestPath); loadErr == nil {
		history, err = resolveReleaseHistory(ctx, manifestPath, objectDir)
		if err == nil {
			files = history[len(history)-1].state
		}
	} else if explicitManifest || !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	} else {
		files, err = releases.WorkingFiles(ctx, objectDir)
	}
	if err != nil {
		return fmt.Errorf("scan objects: %w", err)
	}
	changed, err := releases.UncommittedFiles(ctx, objectDir)
	if err != nil {
		return fmt.Errorf("inspect object Git status: %w", err)
	}
	goose, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, goose.Close()) }()
	db, err := openObjects(cfg.DSN)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	migrationsState, err := goose.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect migrations: %w", err)
	}
	databaseState, err := db.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect releases: %w", err)
	}
	var baseline []objects.File
	if len(history) > 0 {
		var currentErr error
		current, currentErr = effectiveCurrent(history, databaseState)
		if currentErr != nil {
			return fmt.Errorf("resolve current release: %w", currentErr)
		}
		baseline, err = releaseBaseline(history, current)
		if err != nil {
			return fmt.Errorf("resolve current object state: %w", err)
		}
	}
	objectRows := objects.Compare(files, baseline)
	if cleanStatus(history, current, databaseState.Current, migrationsState.Migrations, objectRows, changed) {
		latest := history[len(history)-1].manifest.Version
		if _, err := fmt.Fprintf(out, "Release %s is current.\nDatabase schema %d is current.\nNo pending object changes.\n", latest, migrationsState.Version); err != nil {
			return err
		}
		return writeNext(out, "no action required")
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	recorded := databaseState.Current
	if recorded == "" {
		recorded = "(none)"
	}
	fmt.Fprintf(w, "Database schema:\t%d\nRelease:\t%s\n\n", migrationsState.Version, recorded)
	fmt.Fprintln(w, "MIGRATION\tSTATE\tFILE")
	for _, row := range migrationsState.Migrations {
		fmt.Fprintf(w, "%d\t%s\t%s\n", row.Version, row.State, row.Path)
	}
	fmt.Fprintln(w, "\nOBJECT\tSTATE\tSHA256")
	for _, row := range objectRows {
		fmt.Fprintf(w, "%s\t%s\t%s\n", row.Path, row.State, row.Checksum)
	}
	if len(changed) > 0 {
		fmt.Fprintln(w, "\nUNCOMMITTED OBJECTS")
		for _, path := range changed {
			fmt.Fprintf(w, "- %s\n", path)
		}
		fmt.Fprintln(w, "\nObject changes are not committed, so a reproducible release cannot be created.")
	}
	next := "no action required"
	if len(history) == 0 {
		next = "saxbase release create VERSION"
	} else {
		latest := history[len(history)-1].manifest.Version
		if current != latest {
			next = "saxbase plan"
		} else {
			latestMigration, migrationErr := history[len(history)-1].manifest.TargetMigration()
			if migrationErr != nil {
				return migrationErr
			}
			for _, row := range migrationsState.Migrations {
				if row.State == "missing" || (row.State == "pending" && row.Version <= latestMigration) {
					next = "saxbase plan"
					break
				}
				if row.State == "pending" && row.Version > latestMigration {
					next = "saxbase release create VERSION"
					break
				}
			}
		}
	}
	if len(changed) > 0 {
		next = gitCommitCommand(changed)
	}
	if err := writeNext(w, next); err != nil {
		return err
	}
	return w.Flush()
}

func cleanStatus(history []resolvedRelease, current, recorded string, migrationRows []migrations.Status, objectRows []objects.Status, changed []string) bool {
	if len(history) == 0 || len(changed) > 0 {
		return false
	}
	latest := history[len(history)-1].manifest.Version
	if current != latest || recorded != latest {
		return false
	}
	for _, row := range migrationRows {
		if row.State != "applied" {
			return false
		}
	}
	for _, row := range objectRows {
		if row.State != "current" {
			return false
		}
	}
	return true
}

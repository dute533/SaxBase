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
func runStatus(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, open OpenFunc, openObjects OpenObjectsFunc) (err error) {
	explicitManifest := manifestPath != ""
	if manifestPath == "" {
		manifestPath = "database/release.json"
	}
	var files []objects.File
	if _, loadErr := releases.Load(manifestPath); loadErr == nil {
		return runReleaseStatus(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
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
	db, err := openObjects(cfg)
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
	objectRows := objects.Compare(files, nil)
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
	next := "saxbase release create VERSION"
	if len(changed) > 0 {
		next = gitCommitCommand(changed)
	}
	if err := writeNext(w, next); err != nil {
		return err
	}
	return w.Flush()
}

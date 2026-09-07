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
	if manifest, loadErr := releases.Load(manifestPath); loadErr == nil {
		files, err = manifest.Resolve(ctx, objectDir)
	} else if explicitManifest || !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	} else {
		files, err = releases.WorkingFiles(ctx, objectDir)
	}
	if err != nil {
		return fmt.Errorf("scan objects: %w", err)
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
	objectRows, err := db.Status(ctx, files)
	if err != nil {
		return fmt.Errorf("inspect objects: %w", err)
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Goose version:\t%d\nRelease:\t%s\n\n", migrationsState.Version, statusValue(databaseState.Current))
	fmt.Fprintln(w, "MIGRATION\tSTATE\tFILE")
	for _, row := range migrationsState.Migrations {
		fmt.Fprintf(w, "%d\t%s\t%s\n", row.Version, row.State, row.Path)
	}
	fmt.Fprintln(w, "\nOBJECT\tSTATE\tSHA256")
	for _, row := range objectRows {
		fmt.Fprintf(w, "%s\t%s\t%s\n", row.Path, row.State, row.Checksum)
	}
	return w.Flush()
}

func statusValue(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}

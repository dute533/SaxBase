package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

func runDeploy(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error)) (err error) {
	if manifestPath == "" {
		manifestPath = "database/release.json"
	}
	manifest, err := releases.Load(manifestPath)
	if err != nil {
		return err
	}
	files, err := objects.Scan(objectDir)
	if err != nil {
		return fmt.Errorf("scan objects: %w", err)
	}
	files, err = manifest.OrderedFiles(files)
	if err != nil {
		return err
	}
	version, err := releases.ParseVersion(manifest.Version)
	if err != nil {
		return err
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
	rows, err := db.Deploy(ctx, files, version.Schema, version.Revision, goose, func(ctx context.Context) error {
		plan, err := releases.BuildPlan(ctx, manifest, files, goose, db)
		if err != nil {
			return err
		}
		if len(plan.Blockers) > 0 {
			return fmt.Errorf("deploy blocked:\n- %s", strings.Join(plan.Blockers, "\n- "))
		}
		return nil
	})
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Deployed release %s (Goose %d)\nSTATE\tFILE\tSHA256\n", manifest.Version, version.Schema)
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\n", row.State, row.Path, row.Checksum)
	}
	return w.Flush()
}

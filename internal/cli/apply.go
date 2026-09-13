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

func runApply(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, force bool, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error)) (err error) {
	if manifestPath == "" {
		manifestPath = "database/release.json"
	}
	history, err := resolveReleaseHistory(ctx, manifestPath, objectDir)
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
	state, err := db.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect releases: %w", err)
	}
	current, err := effectiveCurrent(history, state)
	if err != nil {
		return err
	}
	selected, err := selectNextRelease(history, current)
	if err != nil {
		return err
	}
	manifest, files := selected.manifest, selected.files
	baseline, err := releaseBaseline(history, current)
	if err != nil {
		return err
	}
	version, err := releases.ParseVersion(manifest.Version)
	if err != nil {
		return err
	}
	rows, err := db.Apply(ctx, files, baseline, manifest.Version, force, goose, func(ctx context.Context) error {
		plan, err := releases.BuildPlan(ctx, manifest, selected.delta, force, files, baseline, goose, db)
		if err != nil {
			return err
		}
		if len(plan.Blockers) > 0 {
			return fmt.Errorf("apply blocked:\n- %s", strings.Join(plan.Blockers, "\n- "))
		}
		planCurrent := plan.CurrentRelease
		if planCurrent == "" {
			planCurrent = current
		}
		if selected.previous != "" && planCurrent != selected.previous && planCurrent != manifest.Version {
			return fmt.Errorf("release %s must follow current release %s", manifest.Version, selected.previous)
		}
		return nil
	})
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	action := "Applied"
	if force {
		action = "Force-applied"
	}
	fmt.Fprintf(w, "%s release %s (Goose %d)\nSTATE\tFILE\tSHA256\n", action, manifest.Version, version.Schema)
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\n", row.State, row.Path, row.Checksum)
	}
	return w.Flush()
}

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

func runApply(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, force bool, out io.Writer, open OpenFunc, openObjects OpenObjectsFunc) (err error) {
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
	db, err := openObjects(cfg)
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
	if err := requireReconciledCurrent(ctx, history, state, goose, db); err != nil {
		return err
	}
	pending, err := pendingReleases(history, current)
	if err != nil {
		return err
	}
	baseline, err := releaseBaseline(history, current)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	defer func() { err = errors.Join(err, w.Flush()) }()
	changed := false
	for i, selected := range pending {
		manifest, files := selected.manifest, selected.files
		migrationVersion, migrationErr := manifest.TargetMigration()
		if migrationErr != nil {
			return migrationErr
		}
		release := objects.Release{Version: manifest.Version, MigrationVersion: migrationVersion, Fingerprint: manifest.Fingerprint(files)}
		rows, applyErr := db.Apply(ctx, files, baseline, release, force, goose, func(ctx context.Context) error {
			plan, planErr := releases.BuildPlan(ctx, manifest, selected.delta, force, files, baseline, goose, db)
			if planErr != nil {
				return planErr
			}
			if len(plan.Blockers) > 0 {
				return withNext(fmt.Errorf("apply blocked:\n- %s", strings.Join(plan.Blockers, "\n- ")), "saxbase plan")
			}
			planCurrent := plan.CurrentRelease
			if planCurrent == "" {
				planCurrent = current
			}
			if selected.previous != "" && planCurrent != selected.previous && planCurrent != manifest.Version {
				return withNext(fmt.Errorf("release %s must follow current release %s", manifest.Version, selected.previous), "saxbase plan")
			}
			return nil
		})
		if applyErr != nil {
			return applyErr
		}
		if i > 0 {
			fmt.Fprintln(w)
		}
		alreadyApplied := !force && current == manifest.Version
		if alreadyApplied {
			fmt.Fprintf(w, "Release %s is already applied.\nDatabase schema %d is current.\nNo object changes required.\n", manifest.Version, migrationVersion)
			current = manifest.Version
			baseline = selected.state
			continue
		}
		action := "Applied"
		if force {
			action = "Force-applied"
		}
		changed = true
		fmt.Fprintf(w, "%s release %s (database schema %d)\nSTATE\tFILE\tSHA256\n", action, manifest.Version, migrationVersion)
		for _, row := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\n", row.State, row.Path, row.Checksum)
		}
		current = manifest.Version
		baseline = selected.state
	}
	if !changed {
		return writeNext(w, "no action required")
	}
	return writeNext(w, "saxbase status")
}

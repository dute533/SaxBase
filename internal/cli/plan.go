package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

func runPlan(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error)) (err error) {
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
	plan, err := releases.BuildPlan(ctx, manifest, files, goose, db)
	if err != nil {
		return err
	}
	current := plan.CurrentRelease
	if current == "" {
		current = "(unversioned)"
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Release:\t%s -> %s\nGoose:\t%d -> %d\n\n", current, plan.TargetRelease, plan.CurrentSchema, plan.TargetSchema)
	fmt.Fprintln(w, "MIGRATION\tACTION\tFILE")
	for _, row := range plan.Migrations {
		fmt.Fprintf(w, "%d\t%s\t%s\n", row.Version, row.Action, row.Path)
	}
	fmt.Fprintln(w, "\nOBJECT\tSTATE\tSHA256")
	for _, row := range plan.Objects {
		fmt.Fprintf(w, "%s\t%s\t%s\n", row.Path, row.State, row.Checksum)
	}
	if len(plan.Blockers) > 0 {
		fmt.Fprintln(w, "\nBLOCKERS")
		for _, blocker := range plan.Blockers {
			fmt.Fprintf(w, "- %s\n", blocker)
		}
	} else {
		fmt.Fprintln(w, "\nReady: no deployment blockers found.")
	}
	fmt.Fprintln(w, "\nPreview only. No database changes made; SQL execution and dependencies are not validated.")
	if err := w.Flush(); err != nil {
		return err
	}
	if len(plan.Blockers) > 0 {
		return fmt.Errorf("plan blocked by %d issue(s)", len(plan.Blockers))
	}
	return nil
}

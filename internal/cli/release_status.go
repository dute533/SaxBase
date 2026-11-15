package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"saxbase/internal/migrations"
	"saxbase/internal/releases"
)

func runReleaseStatus(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, open OpenFunc, openObjects OpenObjectsFunc) (err error) {
	changed, err := releases.UncommittedFiles(ctx, objectDir)
	if err != nil {
		return fmt.Errorf("inspect object Git status: %w", err)
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
	reconcileErr := requireReconciledCurrent(ctx, history, state, goose, db)
	pending, err := pendingReleases(history, current)
	if err != nil {
		return err
	}
	selected := pending[0]
	pendingVersions := make([]string, 0, len(pending))
	for _, release := range pending {
		pendingVersions = append(pendingVersions, release.manifest.Version)
	}
	manifest, files := selected.manifest, selected.files
	baseline, err := releaseBaseline(history, current)
	if err != nil {
		return err
	}
	preview, err := releases.BuildPlan(ctx, manifest, selected.delta, false, files, baseline, goose, db)
	if err != nil {
		return err
	}
	if reconcileErr != nil {
		preview.Blockers = append(preview.Blockers, reconcileErr.Error())
	}
	if preview.CurrentRelease == "" {
		preview.CurrentRelease = current
	}
	if selected.previous != "" && preview.CurrentRelease != selected.previous && preview.CurrentRelease != manifest.Version {
		preview.Blockers = append(preview.Blockers, fmt.Sprintf("release %s must follow current release %s", manifest.Version, selected.previous))
	}
	current = preview.CurrentRelease
	if current == "" {
		current = "(unversioned)"
	}
	var migrationActions, objectActions int
	for _, row := range preview.Migrations {
		if row.Action != "applied" && row.Action != "waiting" {
			migrationActions++
		}
	}
	for _, row := range preview.Objects {
		if row.State != "current" {
			objectActions++
		}
	}
	hasWaiting := false
	for _, row := range preview.Migrations {
		hasWaiting = hasWaiting || row.Action == "waiting"
	}
	if preview.CurrentRelease == preview.TargetRelease && state.Current == preview.TargetRelease && preview.CurrentSchema == preview.TargetSchema && migrationActions == 0 && objectActions == 0 && !hasWaiting && len(changed) == 0 && len(preview.Blockers) == 0 {
		if _, err := fmt.Fprintf(out, "Release %s is current.\nDatabase schema %d is current.\nNo pending object changes.\n", preview.TargetRelease, preview.CurrentSchema); err != nil {
			return err
		}
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Release:\t%s -> %s\nDatabase schema:\t%d", current, preview.TargetRelease, preview.CurrentSchema)
	if preview.CurrentSchema == preview.TargetSchema {
		fmt.Fprintln(w, " (no migration change)")
	} else {
		fmt.Fprintf(w, " -> %d\n", preview.TargetSchema)
	}
	fmt.Fprintln(w)
	if len(pendingVersions) > 1 {
		fmt.Fprintf(w, "Pending releases:\t%s\n\n", strings.Join(pendingVersions, " -> "))
	}
	if migrationActions == 0 && objectActions == 0 {
		if preview.CurrentRelease == preview.TargetRelease {
			fmt.Fprintf(w, "Release %s is already current.\n", preview.TargetRelease)
		} else {
			fmt.Fprintf(w, "No schema or object changes in release %s.\n", preview.TargetRelease)
		}
	}
	if migrationActions > 0 {
		fmt.Fprintln(w, "MIGRATION\tACTION\tFILE")
		for _, row := range preview.Migrations {
			if row.Action == "applied" || row.Action == "waiting" {
				continue
			}
			fmt.Fprintf(w, "%d\t%s\t%s\n", row.Version, row.Action, row.Path)
		}
	}
	if objectActions > 0 {
		if migrationActions > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "OBJECT\tSTATE\tSHA256")
		for _, row := range preview.Objects {
			if row.State == "current" {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", row.Path, row.State, row.Checksum)
		}
	}
	var hasNotIncluded bool
	for _, row := range preview.Migrations {
		if row.Action != "waiting" {
			continue
		}
		if !hasNotIncluded {
			fmt.Fprintln(w, "\nNOT INCLUDED")
			hasNotIncluded = true
		}
		fmt.Fprintf(w, "- Migration %d (%s): release %s targets database schema %d\n", row.Version, row.Path, preview.TargetRelease, preview.TargetSchema)
	}
	if len(preview.Blockers) > 0 {
		fmt.Fprintln(w, "\nBLOCKERS")
		for _, blocker := range preview.Blockers {
			fmt.Fprintf(w, "- %s\n", blocker)
		}
	} else {
		fmt.Fprintln(w, "\nReady: no deployment blockers found.")
	}
	if len(changed) > 0 {
		fmt.Fprintln(w, "\nUNCOMMITTED OBJECTS")
		for _, path := range changed {
			fmt.Fprintf(w, "- %s\n", path)
		}
		fmt.Fprintln(w, "\nCommit object changes before creating a release. Apply uses committed release definitions.")
	}
	fmt.Fprintln(w, "\nPreview only. No database changes made; SQL execution and dependencies are not validated.")
	if len(preview.Blockers) > 0 {
		if err := writeNext(w, "resolve the blockers above, then run saxbase status"); err != nil {
			return err
		}
	} else {
		next := ""
		if preview.CurrentRelease != preview.TargetRelease || state.Current != preview.TargetRelease {
			next = "saxbase apply"
		} else {
			for _, row := range preview.Migrations {
				if row.Action == "waiting" {
					next = "saxbase release create VERSION"
					break
				}
			}
		}
		if next == "" && len(changed) > 0 {
			next = gitCommitCommand(changed)
		}
		if err := writeNext(w, next); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(preview.Blockers) > 0 {
		return fmt.Errorf("status blocked by %d issue(s)", len(preview.Blockers))
	}
	return nil
}

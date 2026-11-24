package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"saxbase/internal/migrations"
)

// runDrift is read-only: it compares the live database against the recorded
// release (schema fingerprint plus managed object definitions).
func runDrift(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, openObjects OpenObjectsFunc) (err error) {
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
	if !report.Drifted() {
		fmt.Fprintln(out, "No drift detected.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	defer func() { err = errors.Join(err, w.Flush()) }()
	if report.SchemaStored == "" {
		fmt.Fprintln(w, "Schema fingerprint:\tunrecorded (release predates drift tracking; reapply with saxbase -f apply on a dev database)")
	} else if report.SchemaStored != report.SchemaCurrent {
		fmt.Fprintln(w, "Schema fingerprint:\tDRIFTED")
	}
	for _, row := range report.Objects {
		if row.State != "ok" {
			fmt.Fprintf(w, "%s:\t%s\n", row.Path, row.State)
		}
	}
	return errors.New("drift detected")
}

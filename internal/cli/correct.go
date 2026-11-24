package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

// runCorrect aligns the live database to the current release description: it
// replays the release into a scratch database, diffs live against it, and
// applies the corrections (objects, triggers and other modules, tables,
// columns, constraints, indexes) in one transaction with a fresh fingerprint.
// Objects that exist only in the live database are left alone (unmanaged).
// Destructive corrections (column type changes, table creation) are printed as
// warnings; protected targets ask for confirmation like apply.
func runCorrect(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, open OpenFunc, openObjects OpenObjectsFunc) (err error) {
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
	scratch, described, drop, err := replayScratch(ctx, cfg, history[index], open, openObjects)
	if err != nil {
		return err
	}
	defer func() {
		if scratch != nil {
			scratch.Close()
		}
		drop()
	}()
	describedDiff := objects.DiffDescribed(described, report.SchemaCurrent, history[index].state)
	fileStates := map[string]string{}
	for _, row := range report.Objects {
		fileStates[row.Path] = row.State
	}
	corrections, err := objects.BuildCorrection(ctx, scratch, cfg.Dialect, describedDiff, history[index].state, fileStates)
	if err != nil {
		return err
	}
	if len(corrections) == 0 {
		fmt.Fprintln(out, "No drift detected.")
		return nil
	}
	var stmts []string
	for _, c := range corrections {
		if c.Destructive {
			fmt.Fprintf(out, "WARNING (may drop data): %s\n", c.Label)
		} else {
			fmt.Fprintf(out, "correcting %s\n", c.Label)
		}
		if os.Getenv("SAXBASE_DEBUG") != "" {
			for _, s := range c.Statements {
				fmt.Fprintf(out, "--- %s\n%s\n", c.Label, s)
			}
		}
		stmts = append(stmts, c.Statements...)
	}
	if err := db.CorrectSQL(ctx, stmts); err != nil {
		return err
	}
	fmt.Fprintf(out, "Corrected %d item(s). Run saxbase drift to verify.\n", len(corrections))
	return nil
}

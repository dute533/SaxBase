package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

// runCorrect heals drift that can be healed safely: pinned object files that
// drifted or went missing are re-applied from the repo and a fresh schema
// fingerprint is recorded. Schema (table) drift is reported but never
// auto-corrected; it needs a migration.
func runCorrect(ctx context.Context, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, openObjects OpenObjectsFunc) (err error) {
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
	byPath := map[string]objects.File{}
	for _, file := range history[index].state {
		byPath[file.Path] = file
	}
	var fix []objects.File
	for _, row := range report.Objects {
		if row.State != "ok" {
			fix = append(fix, byPath[row.Path])
		}
	}
	if len(fix) == 0 && len(report.SchemaDiff) == 0 {
		fmt.Fprintln(out, "No drift detected.")
		return nil
	}
	if len(fix) > 0 {
		rows, err := db.Correct(ctx, fix)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Corrected objects:")
		for _, row := range rows {
			fmt.Fprintf(out, "%s  %s\n", row.State, row.Path)
		}
	}
	var remains []string
	for _, line := range report.SchemaDiff {
		// Module lines belong to objects, which were just corrected above.
		if strings.HasPrefix(line, "~ ") || strings.HasPrefix(line, "+ module") || strings.HasPrefix(line, "- module") {
			continue
		}
		remains = append(remains, line)
	}
	if len(remains) > 0 {
		fmt.Fprintln(out, "Schema drift remains (fix with a migration, then release create + apply):")
		for _, line := range remains {
			fmt.Fprintln(out, line)
		}
	}
	fmt.Fprintln(out, "Run saxbase drift to verify.")
	return nil
}

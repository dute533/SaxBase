package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
	"saxbase/internal/releaseversion"
)

func runRelease(ctx context.Context, args []string, filename, dir, migrationDir string, out io.Writer) error {
	if !((len(args) == 2 && args[0] == "create") || (len(args) == 1 && args[0] == "validate")) {
		return withNext(errors.New("expected release create VERSION or release validate"), "saxbase release -h")
	}
	if filename == "" {
		filename = "database/release.json"
	}
	if args[0] == "validate" {
		m, err := releases.Load(filename)
		if err != nil {
			return err
		}
		files, err := releases.ResolveStateVersion(ctx, filename, dir, m.Version)
		if err != nil {
			return err
		}
		migrationVersion, err := m.TargetMigration()
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(out, "Release %s targets migration %d and resolves %d referenced object(s)\n", m.Version, migrationVersion, len(files)); err != nil {
			return err
		}
		return writeNext(out, "saxbase plan")
	}
	if err := releaseversion.Validate(args[1]); err != nil {
		return withNext(err, "saxbase release create -h")
	}
	migrationVersion, err := inferMigrationVersion(filename, migrationDir, args[1])
	if err != nil {
		return err
	}
	changed, err := releases.UncommittedFiles(ctx, dir)
	if err != nil {
		return fmt.Errorf("inspect object Git status: %w", err)
	}
	if len(changed) > 0 {
		var message strings.Builder
		fmt.Fprintf(&message, "cannot create release %s: database objects have uncommitted changes\n\nChanged files:\n", args[1])
		for _, path := range changed {
			fmt.Fprintf(&message, "- %s\n", path)
		}
		fmt.Fprintf(&message, "\nCommit them so the manifest can store reproducible Git references for rollback.\n\nNext: %s", gitCommitCommand(changed))
		return errors.New(message.String())
	}
	files, err := releases.CommittedFiles(ctx, dir)
	if err != nil {
		return err
	}
	var m releases.Manifest
	history, loadErr := releases.LoadAll(filename)
	switch {
	case loadErr == nil:
		latest := history[len(history)-1]
		for _, existing := range history[:len(history)-1] {
			if args[1] == existing.Version {
				return fmt.Errorf("release version %s already exists", args[1])
			}
		}
		latestMigration, migrationErr := latest.TargetMigration()
		if migrationErr != nil {
			return migrationErr
		}
		sameVersion := args[1] == latest.Version
		if migrationVersion == latestMigration {
			latestFiles, resolveErr := releases.ResolveStateVersion(ctx, filename, dir, latest.Version)
			if resolveErr != nil {
				return fmt.Errorf("resolve latest release: %w", resolveErr)
			}
			if objects.Fingerprint(files) == objects.Fingerprint(latestFiles) {
				if _, err := fmt.Fprintf(out, "Release %s is current.\nNo committed object changes found.\n", latest.Version); err != nil {
					return err
				}
				return writeNext(out, "no action required")
			}
		}
		var previous []objects.File
		if sameVersion && len(history) == 1 {
			m, err = releases.New(args[1], migrationVersion, files)
		} else {
			previousVersion := latest.Version
			if sameVersion {
				previousVersion = history[len(history)-2].Version
			}
			previous, err = releases.ResolveStateVersion(ctx, filename, dir, previousVersion)
			if err != nil {
				return fmt.Errorf("resolve existing manifest: %w", err)
			}
			m, err = releases.NewDelta(args[1], migrationVersion, files, previous)
		}
		if err == nil && sameVersion {
			err = releases.ReplaceLatest(filename, m)
			if err == nil {
				_, err = fmt.Fprintf(out, "Updated release %s: %s\n", m.Version, filename)
			}
			if err != nil {
				return err
			}
			return writeNext(out, "saxbase plan")
		}
	case errors.Is(loadErr, os.ErrNotExist):
		m, err = releases.New(args[1], migrationVersion, files)
	default:
		return loadErr
	}
	if err != nil {
		return err
	}
	if err := releases.Append(filename, m); err != nil {
		return err
	}
	if _, err = fmt.Fprintf(out, "Created release %s at migration %d: %s\n", m.Version, migrationVersion, filename); err != nil {
		return err
	}
	return writeNext(out, "saxbase plan")
}

func inferMigrationVersion(filename, migrationDir, requestedVersion string) (int64, error) {
	versions, err := availableMigrationVersions(migrationDir)
	if err != nil {
		return 0, err
	}
	history, err := releases.LoadAll(filename)
	if errors.Is(err, os.ErrNotExist) {
		if len(versions) > 0 {
			return versions[0], nil
		}
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	latest := history[len(history)-1]
	current, err := latest.TargetMigration()
	if err != nil {
		return 0, err
	}
	if requestedVersion == latest.Version {
		return current, nil
	}
	for _, version := range versions {
		if version > current {
			return version, nil
		}
	}
	return current, nil
}

func availableMigrationVersions(dir string) ([]int64, error) {
	versions, err := migrations.Versions(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return versions, err
}

type resolvedRelease struct {
	manifest releases.Manifest
	files    []objects.File
	state    []objects.File
	previous string
	delta    bool
}

// resolveReleaseHistory resolves every release before a database is opened, so
// a missing Git commit cannot leave a deployment partially started.
func resolveReleaseHistory(ctx context.Context, filename, objectDir string) ([]resolvedRelease, error) {
	history, err := releases.LoadAll(filename)
	if err != nil {
		return nil, err
	}
	result := make([]resolvedRelease, 0, len(history))
	var state []objects.File
	for i, manifest := range history {
		files, err := manifest.Resolve(ctx, objectDir)
		if err != nil {
			return nil, fmt.Errorf("scan objects for release %s: %w", manifest.Version, err)
		}
		files, err = manifest.OrderedFiles(files)
		if err != nil {
			return nil, err
		}
		previousState := state
		previous := ""
		if i > 0 {
			previous = history[i-1].Version
		}
		state = releases.ApplyDelta(previousState, files)
		result = append(result, resolvedRelease{manifest: manifest, files: files, state: state, previous: previous, delta: i > 0})
	}
	return result, nil
}

// releaseBaseline returns the complete manifest state represented by the
// active database release. All possible states were resolved before opening
// the database, preserving the no-partial-deployment Git safety check.
func releaseBaseline(history []resolvedRelease, current string) ([]objects.File, error) {
	if current == "" {
		return nil, nil
	}
	for _, release := range history {
		if release.manifest.Version == current {
			return release.state, nil
		}
	}
	return nil, fmt.Errorf("current release %s is not resolvable from manifest history", current)
}

// effectiveCurrent recovers the deployment position after a failed structural
// migration or object apply clears the current marker. The newest release that
// is both recorded in the database and present in this manifest remains the
// object baseline for a safe retry.
func effectiveCurrent(history []resolvedRelease, state objects.Inspection) (string, error) {
	if state.Current != "" {
		return state.Current, nil
	}
	if len(state.History) == 0 {
		return "", nil
	}
	recorded := make(map[string]bool, len(state.History))
	for _, release := range state.History {
		recorded[release.Version] = true
	}
	for i := len(history) - 1; i >= 0; i-- {
		if recorded[history[i].manifest.Version] {
			return history[i].manifest.Version, nil
		}
	}
	return "", errors.New("recorded database releases are not present in manifest history")
}

// pendingReleases returns the releases apply must visit, in order. If the
// newest entry is already current, it is returned so plan/apply remain
// idempotent and can recheck that release.
func pendingReleases(history []resolvedRelease, current string) ([]resolvedRelease, error) {
	if len(history) == 0 {
		return nil, errors.New("manifest contains no releases")
	}
	if current == "" {
		return history, nil
	}
	for i, release := range history {
		if release.manifest.Version != current {
			continue
		}
		if i+1 < len(history) {
			return history[i+1:], nil
		}
		return history[i : i+1], nil
	}
	return nil, fmt.Errorf("current release %s is not in manifest history", current)
}

func releaseIndex(history []resolvedRelease, version string) int {
	for i := range history {
		if history[i].manifest.Version == version {
			return i
		}
	}
	return -1
}

func requireReconciledCurrent(ctx context.Context, history []resolvedRelease, state objects.Inspection, goose migrations.Engine, db objects.Engine) error {
	if state.Current == "" {
		return nil
	}
	index := releaseIndex(history, state.Current)
	if index < 0 {
		return withNext(fmt.Errorf("current release %s is not in manifest history", state.Current), "restore a manifest containing release "+state.Current)
	}
	expectedMigration, err := history[index].manifest.TargetMigration()
	if err != nil {
		return err
	}
	schema, err := goose.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect migrations: %w", err)
	}
	retry := fmt.Sprintf("current release %s is not fully reconciled; retry rollback %s", state.Current, state.Current)
	if schema.Version != expectedMigration {
		return withNext(fmt.Errorf("current release %s is not fully reconciled (database schema is %d, expected %d)", state.Current, schema.Version, expectedMigration), "saxbase rollback "+state.Current)
	}
	current := releaseSnapshot(history[index])
	recorded, err := db.Snapshot(ctx, state.Current)
	if err != nil {
		return fmt.Errorf("inspect current release fingerprint: %w", err)
	}
	// A deliberately changed manifest is handled by the normal immutable
	// release checks. It is not evidence of an interrupted rollback.
	if recorded.Fingerprint != current.Fingerprint {
		if objects.IsRollbackCheckpoint(recorded.Fingerprint, current.Fingerprint) {
			return withNext(errors.New(retry), "saxbase rollback "+state.Current)
		}
		return nil
	}
	ok, err := db.Reconciled(ctx, current)
	if err != nil {
		return fmt.Errorf("inspect current release objects: %w", err)
	}
	if !ok {
		return withNext(errors.New(retry), "saxbase rollback "+state.Current)
	}
	return nil
}

func runRollback(ctx context.Context, args []string, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, open OpenFunc, openObjects OpenObjectsFunc) (err error) {
	if len(args) > 1 {
		return withNext(errors.New("expected rollback [VERSION]"), "saxbase rollback -h")
	}
	if len(args) == 1 {
		if err := releaseversion.Validate(args[0]); err != nil {
			return withNext(err, "saxbase rollback -h")
		}
	}
	if cfg.DSN == "" {
		return withNext(errors.New("no database connection is configured"), "set SAXBASE_DSN and SAXBASE_DIALECT or configure saxbase.yaml")
	}
	if manifestPath == "" {
		manifestPath = "database/release.json"
	}
	history, err := resolveReleaseHistory(ctx, manifestPath, objectDir)
	if err != nil {
		return err
	}
	engine, err := openObjects(cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	inspection, err := engine.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect current release: %w", err)
	}
	if inspection.Current == "" {
		return withNext(errors.New("current state is unversioned; apply a manifest first"), "saxbase apply")
	}
	currentIndex := releaseIndex(history, inspection.Current)
	if currentIndex < 0 {
		return fmt.Errorf("current release %s not found in manifest %s", inspection.Current, manifestPath)
	}
	var targetIndex int
	if len(args) == 0 {
		if currentIndex == 0 {
			return withNext(fmt.Errorf("release %s is the oldest recorded release; there is nothing to roll back", inspection.Current), "no action required")
		}
		targetIndex = currentIndex - 1
	} else {
		targetIndex = releaseIndex(history, args[0])
		if targetIndex < 0 {
			return withNext(fmt.Errorf("release %s not found in %s", args[0], manifestPath), "saxbase status")
		}
	}
	if targetIndex > currentIndex {
		return withNext(errors.New("rollback target is newer than the current release"), "saxbase status")
	}
	goose, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, goose.Close()) }()
	current := releaseSnapshot(history[currentIndex])
	schema, err := goose.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect migrations: %w", err)
	}
	reconciled, err := engine.Reconciled(ctx, current)
	if err != nil {
		return fmt.Errorf("inspect current release objects: %w", err)
	}
	if currentIndex == targetIndex || schema.Version != current.MigrationVersion || !reconciled {
		if err := engine.Reconcile(ctx, current, goose); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "Reconciled release %s (database schema %d)\n", current.Version, current.MigrationVersion); err != nil {
			return err
		}
	}
	for i := currentIndex; i > targetIndex; i-- {
		source := releaseSnapshot(history[i])
		target := releaseSnapshot(history[i-1])
		result, err := engine.Rollback(ctx, target, source, goose)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "Rolled back release %s to %s\n", result.SourceVersion, result.TargetVersion); err != nil {
			return err
		}
	}
	return writeNext(out, "saxbase status")
}

func releaseSnapshot(release resolvedRelease) objects.Snapshot {
	migrationVersion, _ := release.manifest.TargetMigration()
	result := objects.Snapshot{Release: objects.Release{Version: release.manifest.Version, MigrationVersion: migrationVersion, Fingerprint: release.manifest.Fingerprint(release.files)}}
	for _, file := range release.state {
		result.Objects = append(result.Objects, objects.SnapshotObject{Path: file.Path, SQL: file.SQL, Checksum: file.Checksum})
	}
	return result
}

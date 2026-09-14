package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

func runRelease(ctx context.Context, args []string, filename, dir string, out io.Writer) error {
	if !((len(args) == 2 && args[0] == "create") || (len(args) == 1 && args[0] == "validate")) {
		return errors.New("expected release create VERSION or release validate")
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
		_, err = fmt.Fprintf(out, "Release %s resolves %d referenced object(s)\n", m.Version, len(files))
		return err
	}
	files, err := releases.CommittedFiles(ctx, dir)
	if err != nil {
		return err
	}

	if args[0] == "create" {
		var m releases.Manifest
		history, loadErr := releases.LoadAll(filename)
		switch {
		case loadErr == nil:
			latest := history[len(history)-1]
			requestedVersion, parseErr := releases.ParseVersion(args[1])
			if parseErr != nil {
				return parseErr
			}
			latestVersion, _ := releases.ParseVersion(latest.Version)
			sameVersion := requestedVersion == latestVersion
			if requestedVersion.Schema < latestVersion.Schema ||
				(requestedVersion.Schema == latestVersion.Schema && requestedVersion.Revision < latestVersion.Revision) {
				return fmt.Errorf("release %s must be newer than existing release %s", args[1], latest.Version)
			}
			var previous []objects.File
			if sameVersion && len(history) == 1 {
				m, err = releases.New(args[1], files)
			} else {
				previousVersion := latest.Version
				if sameVersion {
					previousVersion = history[len(history)-2].Version
				}
				previous, err = releases.ResolveStateVersion(ctx, filename, dir, previousVersion)
				if err != nil {
					return fmt.Errorf("resolve existing manifest: %w", err)
				}
				if containsLatest(previous) {
					return errors.New("cannot add a release after one containing latest references; commit object files to Git first")
				}
				m, err = releases.NewDelta(args[1], files, previous)
			}
			if err == nil && sameVersion {
				err = releases.ReplaceLatest(filename, m)
				if err == nil {
					_, err = fmt.Fprintf(out, "Updated release %s: %s\n", m.Version, filename)
				}
				return err
			}
		case errors.Is(loadErr, os.ErrNotExist):
			m, err = releases.New(args[1], files)
		default:
			return loadErr
		}
		if err != nil {
			return err
		}
		if err := releases.Append(filename, m); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Created release %s: %s\n", m.Version, filename)
		return err
	}
	return errors.New("unsupported local release command")
}

func containsLatest(files []objects.File) bool {
	for _, file := range files {
		if file.Commit == "latest" {
			return true
		}
	}
	return false
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

// selectNextRelease returns only the immediate step for plan output.
func selectNextRelease(history []resolvedRelease, current string) (resolvedRelease, error) {
	pending, err := pendingReleases(history, current)
	if err != nil {
		return resolvedRelease{}, err
	}
	return pending[0], nil
}

// pendingReleaseVersions returns the releases that apply will visit, in order.
// The current release is omitted unless it is already the newest release, in
// which case it is returned so an idempotent plan can still describe its target.
func pendingReleaseVersions(history []resolvedRelease, current string) ([]string, error) {
	pending, err := pendingReleases(history, current)
	if err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(pending))
	for _, release := range pending {
		versions = append(versions, release.manifest.Version)
	}
	return versions, nil
}

func runRollback(ctx context.Context, args []string, cfg migrations.Config, manifestPath, objectDir string, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error)) (err error) {
	if len(args) != 1 {
		return errors.New("expected rollback VERSION")
	}
	if _, err := releases.ParseVersion(args[0]); err != nil {
		return err
	}
	if cfg.Driver != "mssql" && cfg.Driver != "sqlserver" {
		return fmt.Errorf("unsupported driver %q", cfg.Driver)
	}
	if cfg.DSN == "" {
		return errors.New("set GOOSE_DBSTRING before rollback")
	}
	if manifestPath == "" {
		manifestPath = "database/release.json"
	}
	history, err := resolveReleaseHistory(ctx, manifestPath, objectDir)
	if err != nil {
		return err
	}
	snapshots := make(map[string]objects.Snapshot, len(history))
	for _, release := range history {
		snapshots[release.manifest.Version] = releaseSnapshot(release)
	}
	target, ok := snapshots[args[0]]
	if !ok {
		return fmt.Errorf("release %s not found in %s", args[0], manifestPath)
	}
	engine, err := openObjects(cfg.DSN)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	inspection, err := engine.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect current release: %w", err)
	}
	sourceVersion := inspection.Current
	for _, rollback := range inspection.Rollbacks {
		if rollback.Status != "completed" {
			sourceVersion = rollback.SourceVersion
			break
		}
	}
	source, ok := snapshots[sourceVersion]
	if !ok {
		return fmt.Errorf("current release %s not found in manifest %s", sourceVersion, manifestPath)
	}
	goose, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, goose.Close()) }()
	result, err := engine.Rollback(ctx, target, source, goose)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Rolled back release %s to %s\n", result.SourceVersion, result.TargetVersion)
	return err
}

func releaseSnapshot(release resolvedRelease) objects.Snapshot {
	result := objects.Snapshot{Release: objects.Release{Version: release.manifest.Version, Fingerprint: objects.Fingerprint(release.files)}}
	for _, file := range release.state {
		result.Objects = append(result.Objects, objects.SnapshotObject{Path: file.Path, SQL: file.SQL, Checksum: file.Checksum})
	}
	return result
}

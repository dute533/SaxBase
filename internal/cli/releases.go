package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

func runRelease(ctx context.Context, args []string, filename, dir, parentFilename string, out io.Writer) error {
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
		if parentFilename != "" {
			previous, err := releases.ResolveState(ctx, parentFilename, dir)
			if err != nil {
				return fmt.Errorf("resolve parent manifest: %w", err)
			}
			if containsLatest(previous) {
				return errors.New("cannot create a delta from a release containing latest references; commit object files to Git first")
			}
			parentRef := ""
			manifestAbs, manifestErr := filepath.Abs(filename)
			parentAbs, parentErr := filepath.Abs(parentFilename)
			if manifestErr == nil && parentErr == nil && manifestAbs == parentAbs {
				parent, err := releases.Load(filename)
				if err != nil {
					return err
				}
				parentRef = parent.Version
			} else {
				parentRef, err = filepath.Rel(filepath.Dir(filename), parentFilename)
				if err != nil {
					return err
				}
			}
			m, err = releases.NewDelta(args[1], filepath.ToSlash(parentRef), files, previous)
			if err != nil {
				return err
			}
		} else {
			previous, loadErr := releases.Load(filename)
			switch {
			case loadErr == nil:
				oldFiles, resolveErr := releases.ResolveState(ctx, filename, dir)
				if resolveErr != nil {
					return fmt.Errorf("resolve existing manifest: %w", resolveErr)
				}
				if containsLatest(oldFiles) {
					return errors.New("cannot append a delta to a release containing latest references; commit object files to Git first")
				}
				m, err = releases.NewDelta(args[1], previous.Version, files, oldFiles)
			case errors.Is(loadErr, os.ErrNotExist):
				m, err = releases.New(args[1], files)
			default:
				return loadErr
			}
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
	manifest    releases.Manifest
	files       []objects.File
	state       []objects.File
	parentState []objects.File
}

// resolveReleaseHistory resolves every release before a database is opened, so
// a missing Git commit cannot leave a deployment partially started.
func resolveReleaseHistory(ctx context.Context, filename, objectDir string) ([]resolvedRelease, error) {
	history, err := releases.LoadAll(filename)
	if err != nil {
		return nil, err
	}
	result := make([]resolvedRelease, 0, len(history))
	states := make(map[string][]objects.File, len(history))
	for _, manifest := range history {
		files, err := manifest.Resolve(ctx, objectDir)
		if err != nil {
			return nil, fmt.Errorf("scan objects for release %s: %w", manifest.Version, err)
		}
		files, err = manifest.OrderedFiles(files)
		if err != nil {
			return nil, err
		}
		var parentState []objects.File
		if manifest.Parent != "" {
			if _, parseErr := releases.ParseVersion(manifest.Parent); parseErr == nil {
				var ok bool
				parentState, ok = states[manifest.Parent]
				if !ok {
					return nil, fmt.Errorf("release %s refers to parent %s before it is defined", manifest.Version, manifest.Parent)
				}
			} else {
				parentState, err = releases.ResolveParentState(ctx, filename, objectDir, manifest)
				if err != nil {
					return nil, fmt.Errorf("resolve parent state for release %s: %w", manifest.Version, err)
				}
			}
		}
		state := releases.ApplyDelta(parentState, files)
		states[manifest.Version] = state
		result = append(result, resolvedRelease{manifest: manifest, files: files, state: state, parentState: parentState})
	}
	return result, nil
}

// releaseBaseline returns the complete manifest state represented by the
// active database release. All possible states were resolved before opening
// the database, preserving the no-partial-deployment Git safety check.
func releaseBaseline(filename string, history []resolvedRelease, selected resolvedRelease, current string) ([]objects.File, error) {
	if current == "" {
		return nil, nil
	}
	for _, release := range history {
		if release.manifest.Version == current {
			return release.state, nil
		}
	}
	parent, err := releases.ParentVersion(filename, selected.manifest)
	if err != nil {
		return nil, err
	}
	if parent == current {
		return selected.parentState, nil
	}
	// A standalone full-state manifest has no historical baseline to resolve.
	// Applying all of its definitions remains safe, but omitted objects cannot
	// be inferred and therefore are not removed.
	if selected.manifest.Parent == "" {
		return nil, nil
	}
	return nil, fmt.Errorf("current release %s is not resolvable from manifest history", current)
}

// selectNextRelease advances one history entry at a time. If the newest entry
// is already current, it is selected again so plan/apply remain idempotent.
func selectNextRelease(filename string, history []resolvedRelease, current string) (resolvedRelease, error) {
	if len(history) == 0 {
		return resolvedRelease{}, errors.New("manifest contains no releases")
	}
	if len(history) == 1 {
		return history[0], nil
	}
	for i, release := range history {
		if release.manifest.Version != current {
			continue
		}
		if i+1 < len(history) {
			return history[i+1], nil
		}
		return release, nil
	}
	first := history[0]
	parent, err := releases.ParentVersion(filename, first.manifest)
	if err != nil {
		return resolvedRelease{}, err
	}
	if parent == "" || parent == current {
		return first, nil
	}
	return resolvedRelease{}, fmt.Errorf("current release %s is not in manifest history", current)
}

func runRollback(ctx context.Context, args []string, cfg migrations.Config, manifestPath, sourceManifest, objectDir string, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error)) (err error) {
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
	if sourceManifest == "" {
		sourceManifest = manifestPath
	}
	target, err := loadRollbackManifest(ctx, manifestPath, args[0], objectDir)
	if err != nil {
		return err
	}
	if target.Version != args[0] {
		return errors.New("target manifest version does not match rollback version")
	}
	sourceHistory, err := releases.LoadAll(sourceManifest)
	if err != nil {
		return err
	}
	sources := make(map[string]objects.Snapshot, len(sourceHistory))
	for _, manifest := range sourceHistory {
		source, err := loadRollbackManifest(ctx, sourceManifest, manifest.Version, objectDir)
		if err != nil {
			return err
		}
		sources[manifest.Version] = source
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
	source, ok := sources[sourceVersion]
	if !ok {
		return fmt.Errorf("current release %s not found in source manifest %s", sourceVersion, sourceManifest)
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

func loadRollbackManifest(ctx context.Context, filename, version, objectDir string) (objects.Snapshot, error) {
	m, err := releases.LoadVersion(filename, version)
	if err != nil {
		return objects.Snapshot{}, err
	}
	files, err := releases.ResolveStateVersion(ctx, filename, objectDir, m.Version)
	if err != nil {
		return objects.Snapshot{}, err
	}
	v, _ := releases.ParseVersion(m.Version)
	delta, err := m.Resolve(ctx, objectDir)
	if err != nil {
		return objects.Snapshot{}, err
	}
	result := objects.Snapshot{Release: objects.Release{Version: m.Version, SchemaVersion: v.Schema, Revision: v.Revision, Fingerprint: objects.Fingerprint(delta)}}
	for _, file := range files {
		result.Objects = append(result.Objects, objects.SnapshotObject{Path: file.Path, SQL: file.SQL, Checksum: file.Checksum})
	}
	return result, nil
}

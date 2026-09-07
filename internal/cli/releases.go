package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

func runRelease(ctx context.Context, args []string, filename, dir, parentFilename string, out io.Writer) error {
	if !((len(args) == 2 && args[0] == "create") || (len(args) == 1 && args[0] == "validate") || ((len(args) == 1 || len(args) == 2) && args[0] == "sync")) {
		return errors.New("expected release create VERSION, release validate, or release sync [VERSION]")
	}
	if filename == "" {
		filename = "database/release.json"
	}
	if args[0] == "validate" {
		m, err := releases.Load(filename)
		if err != nil {
			return err
		}
		files, err := m.Resolve(context.Background(), ".")
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

	if args[0] == "sync" {
		version := ""
		if len(args) == 2 {
			version = args[1]
		}
		var result releases.SyncResult
		if parentFilename != "" {
			current, err := releases.Load(filename)
			if err != nil {
				return err
			}
			previous, err := loadManifestState(ctx, parentFilename, dir)
			if err != nil {
				return fmt.Errorf("resolve parent manifest: %w", err)
			}
			if version == "" {
				version = current.Version
			}
			delta, err := releases.NewDelta(version, current.Parent, files, previous)
			if err != nil {
				return err
			}
			positions := make(map[string]int, len(current.Objects))
			for i, object := range current.Objects {
				positions[object.Path] = i
			}
			sort.SliceStable(delta.Objects, func(i, j int) bool {
				a, aok := positions[delta.Objects[i].Path]
				b, bok := positions[delta.Objects[j].Path]
				if aok != bok {
					return aok
				}
				if aok {
					return a < b
				}
				return delta.Objects[i].Path < delta.Objects[j].Path
			})
			result.PreviousVersion, result.Version = current.Version, delta.Version
			for _, object := range delta.Objects {
				if object.Delete {
					result.Removed = append(result.Removed, object.Path)
				} else if _, ok := positions[object.Path]; ok {
					result.Updated = append(result.Updated, object.Path)
				} else {
					result.Added = append(result.Added, object.Path)
				}
			}
			if err := delta.WriteReplace(filename); err != nil {
				return err
			}
		} else {
			result, err = releases.Sync(filename, files, version)
		}
		if err != nil {
			return err
		}
		var report strings.Builder
		if result.Version != result.PreviousVersion {
			fmt.Fprintf(&report, "Version: %s -> %s\n", result.PreviousVersion, result.Version)
		}
		for _, path := range result.Updated {
			fmt.Fprintf(&report, "Updated: %s\n", path)
		}
		for _, path := range result.Added {
			fmt.Fprintf(&report, "Added: %s\n", path)
		}
		for _, path := range result.Removed {
			fmt.Fprintf(&report, "Removed: %s\n", path)
		}
		if report.Len() == 0 {
			fmt.Fprintf(&report, "Release %s is already synchronized: %s\n", result.Version, filename)
		} else {
			fmt.Fprintf(&report, "Synced release %s: %s\n", result.Version, filename)
		}
		_, err = io.WriteString(out, report.String())
		return err
	}
	if args[0] == "create" {
		var m releases.Manifest
		if parentFilename != "" {
			previous, err := loadManifestState(ctx, parentFilename, dir)
			if err != nil {
				return fmt.Errorf("resolve parent manifest: %w", err)
			}
			parentRef, err := filepath.Rel(filepath.Dir(filename), parentFilename)
			if err != nil {
				return err
			}
			m, err = releases.NewDelta(args[1], filepath.ToSlash(parentRef), files, previous)
			if err != nil {
				return err
			}
		} else {
			m, err = releases.New(args[1], files)
		}
		if err != nil {
			return err
		}
		if err := m.Write(filename); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Created release %s: %s\n", m.Version, filename)
		return err
	}
	return errors.New("unsupported local release command")
}

func runReleaseDatabase(ctx context.Context, args []string, cfg migrations.Config, out io.Writer, open func(string) (objects.Engine, error)) (err error) {
	if !((len(args) == 1 && (args[0] == "history" || args[0] == "rollbacks" || args[0] == "current")) || (len(args) == 2 && args[0] == "show")) {
		return errors.New("expected release history, current, rollbacks, or show VERSION")
	}
	if args[0] == "show" {
		if _, err := releases.ParseVersion(args[1]); err != nil {
			return err
		}
	}
	if cfg.Driver != "mssql" && cfg.Driver != "sqlserver" {
		return fmt.Errorf("unsupported driver %q: use mssql or sqlserver", cfg.Driver)
	}
	if cfg.DSN == "" {
		return errors.New("set GOOSE_DBSTRING to inspect database releases")
	}
	engine, err := open(cfg.DSN)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	if args[0] == "current" {
		version, err := engine.Current(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, version)
		return err
	}
	if args[0] == "rollbacks" {
		rows, err := engine.Rollbacks(ctx)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rows)
	}
	if args[0] == "show" {
		snapshot, err := engine.Snapshot(ctx, args[1])
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(snapshot)
	}
	rows, err := engine.History(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RELEASE\tGOOSE\tOBJECTS\tFIRST DEPLOYED (UTC)")
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\n", row.Version, row.SchemaVersion, row.ObjectCount, row.DeployedAt.UTC().Format(time.RFC3339))
	}
	return w.Flush()
}

func runRollback(ctx context.Context, args []string, cfg migrations.Config, manifestPath, sourceManifest, objectDir string, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error)) (err error) {
	if len(args) != 1 {
		return errors.New("expected release rollback VERSION")
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
		return errors.New("rollback requires -source-manifest for the active release and -manifest for the target release")
	}
	target, err := loadRollbackManifest(ctx, manifestPath, objectDir)
	if err != nil {
		return err
	}
	if target.Version != args[0] {
		return errors.New("target manifest version does not match rollback version")
	}
	source, err := loadRollbackManifest(ctx, sourceManifest, objectDir)
	if err != nil {
		return err
	}
	engine, err := openObjects(cfg.DSN)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
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

func checkSchema(ctx context.Context, cfg migrations.Config, m releases.Manifest, open OpenFunc) (err error) {
	version, err := releases.ParseVersion(m.Version)
	if err != nil {
		return err
	}
	engine, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	current, err := engine.Version(ctx)
	if err != nil {
		return err
	}
	if current != version.Schema {
		return fmt.Errorf("release %s requires Goose version %d; database is at %d", m.Version, version.Schema, current)
	}
	return nil
}

func loadRollbackManifest(ctx context.Context, filename, objectDir string) (objects.Snapshot, error) {
	m, err := releases.Load(filename)
	if err != nil {
		return objects.Snapshot{}, err
	}
	files, err := loadManifestState(ctx, filename, objectDir)
	if err != nil {
		return objects.Snapshot{}, err
	}
	v, _ := releases.ParseVersion(m.Version)
	delta, err := m.Resolve(ctx, ".")
	if err != nil {
		return objects.Snapshot{}, err
	}
	result := objects.Snapshot{Release: objects.Release{Version: m.Version, SchemaVersion: v.Schema, Revision: v.Revision, ObjectCount: len(files), Fingerprint: objects.Fingerprint(delta)}}
	for _, file := range files {
		result.Objects = append(result.Objects, objects.SnapshotObject{Path: file.Path, SQL: file.SQL, Checksum: file.Checksum})
	}
	return result, nil
}

func loadManifestState(ctx context.Context, filename, objectDir string) ([]objects.File, error) {
	return loadManifestStateSeen(ctx, filename, objectDir, map[string]bool{})
}

func loadManifestStateSeen(ctx context.Context, filename, objectDir string, seen map[string]bool) ([]objects.File, error) {
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	if seen[absolute] {
		return nil, fmt.Errorf("manifest parent cycle includes %s", filename)
	}
	seen[absolute] = true
	defer delete(seen, absolute)
	m, err := releases.Load(filename)
	if err != nil {
		return nil, err
	}
	var state []objects.File
	if m.Parent != "" {
		parent := filepath.Join(filepath.Dir(filename), filepath.FromSlash(m.Parent))
		state, err = loadManifestStateSeen(ctx, parent, objectDir, seen)
		if err != nil {
			return nil, err
		}
	}
	delta, err := m.Resolve(ctx, ".")
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]int, len(state))
	for i, file := range state {
		byPath[file.Path] = i
	}
	for _, file := range delta {
		if i, ok := byPath[file.Path]; ok {
			state = append(state[:i], state[i+1:]...)
			for path, index := range byPath {
				if index > i {
					byPath[path] = index - 1
				}
			}
			delete(byPath, file.Path)
		}
		if !file.Delete {
			byPath[file.Path] = len(state)
			state = append(state, file)
		}
	}
	return state, nil
}

func manifestParentVersion(filename string, manifest releases.Manifest) (string, error) {
	if manifest.Parent == "" {
		return "", nil
	}
	parent, err := releases.Load(filepath.Join(filepath.Dir(filename), filepath.FromSlash(manifest.Parent)))
	if err != nil {
		return "", fmt.Errorf("load parent manifest: %w", err)
	}
	return parent.Version, nil
}

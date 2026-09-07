package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

func runRelease(args []string, filename, dir string, out io.Writer) error {
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
		_, err = fmt.Fprintf(out, "Release %s resolves %d committed object(s)\n", m.Version, len(files))
		return err
	}
	files, err := releases.CommittedFiles(context.Background(), dir)
	if err != nil {
		return err
	}

	if args[0] == "sync" {
		version := ""
		if len(args) == 2 {
			version = args[1]
		}
		result, err := releases.Sync(filename, files, version)
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
		if report.Len() == 0 {
			fmt.Fprintf(&report, "Release %s is already synchronized: %s\n", result.Version, filename)
		} else {
			fmt.Fprintf(&report, "Synced release %s: %s\n", result.Version, filename)
		}
		_, err = io.WriteString(out, report.String())
		return err
	}
	if args[0] == "create" {
		m, err := releases.New(args[1], files)
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

func runRollback(ctx context.Context, args []string, cfg migrations.Config, manifestPath, sourceManifest string, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error)) (err error) {
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
	target, err := loadRollbackManifest(ctx, manifestPath)
	if err != nil {
		return err
	}
	if target.Version != args[0] {
		return errors.New("target manifest version does not match rollback version")
	}
	source, err := loadRollbackManifest(ctx, sourceManifest)
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

func loadRollbackManifest(ctx context.Context, filename string) (objects.Snapshot, error) {
	m, err := releases.Load(filename)
	if err != nil {
		return objects.Snapshot{}, err
	}
	files, err := m.Resolve(ctx, ".")
	if err != nil {
		return objects.Snapshot{}, err
	}
	v, _ := releases.ParseVersion(m.Version)
	result := objects.Snapshot{Release: objects.Release{Version: m.Version, SchemaVersion: v.Schema, Revision: v.Revision, ObjectCount: len(files), Fingerprint: objects.Fingerprint(files)}}
	for _, file := range files {
		result.Objects = append(result.Objects, objects.SnapshotObject{Path: file.Path, SQL: file.SQL, Checksum: file.Checksum})
	}
	return result, nil
}

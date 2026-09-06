package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

func runRelease(args []string, filename, dir string, out io.Writer) error {
	if !((len(args) == 2 && args[0] == "create") || (len(args) == 1 && args[0] == "validate")) {
		return errors.New("expected release create VERSION or release validate")
	}
	if filename == "" {
		filename = "database/release.json"
	}
	files, err := objects.Scan(dir)
	if err != nil {
		return fmt.Errorf("scan objects: %w", err)
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
	m, err := releases.Load(filename)
	if err != nil {
		return err
	}
	if err := m.Validate(files); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Release %s matches %d object(s)\n", m.Version, len(files))
	return err
}

func runReleaseDatabase(ctx context.Context, args []string, cfg migrations.Config, out io.Writer, open func(string) (objects.Engine, error)) (err error) {
	if !((len(args) == 1 && args[0] == "history") || (len(args) == 2 && args[0] == "show")) {
		return errors.New("expected release history or release show VERSION")
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

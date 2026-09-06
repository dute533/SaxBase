package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

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

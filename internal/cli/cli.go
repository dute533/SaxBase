package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

// Version is set by the release build; local source builds report dev.
var Version = "dev"

const usage = `SaxBase

Usage:
  saxbase --version
  saxbase [-manifest database/release.json] plan|apply|status
  saxbase rollback VERSION
  saxbase [-manifest database/release.json] release create VERSION
  saxbase [-manifest database/release.json] release validate

Advanced usage:
  saxbase migration up|down

Commands:
  plan      Preview Goose migrations and objects without changing the database
  apply     Migrate to the manifest schema, apply objects, and record the release
  status    Show Goose, release, and object state together
  rollback  Restore a recorded release using its manifest
  release create VERSION  Write a new manifest from current object files
  release validate        Check the manifest against current object files

Advanced commands:
  migration up|down       Run Goose migration operations directly

Environment:
  GOOSE_DRIVER    mssql (default) or sqlserver
  GOOSE_DBSTRING  SQL Server connection string
  GOOSE_MIGRATION_DIR  Migration directory (default database/migrations)
  SAXBASE_OBJECTS_DIR  Object directory (default database/objects)

Configuration:
  -config PATH   Target config (default saxbase.yaml)
  -dir PATH      Migration directory (default database/migrations)
  -objects-dir PATH  Object directory (default database/objects)
  -manifest PATH Release manifest (default database/release.json)
  -parent-manifest PATH  Previous release manifest when creating a delta
  -source-manifest PATH  Active release manifest for rollback
  -target NAME   Database target (defaults to default_target in config)
  -yes           Confirm database writes to targets requiring confirmation
  .env           Loaded from the working directory; environment takes precedence

Options must precede the command or connection arguments.
`

type OpenFunc func(migrations.Config) (migrations.Engine, error)

func Run(ctx context.Context, args []string, getenv func(string) string, out io.Writer, open OpenFunc) (err error) {
	return run(ctx, args, getenv, out, open, objects.Open)
}

type runEnvironment struct {
	lookup      func(string) (string, bool)
	diagnostics io.Writer
	input       io.Reader
}

// RunWithLookup preserves explicitly empty environment variables when loading .env.
func RunWithLookup(ctx context.Context, args []string, lookup func(string) (string, bool), out, diagnostics io.Writer, open OpenFunc) error {
	getenv := func(key string) string { value, _ := lookup(key); return value }
	return run(ctx, args, getenv, out, open, objects.Open, runEnvironment{lookup: lookup, diagnostics: diagnostics, input: os.Stdin})
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error), environment ...runEnvironment) (err error) {
	if len(args) == 0 {
		_, err = io.WriteString(out, usage)
		return err
	}
	flags := flag.NewFlagSet("saxbase", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var dir, objectDir, manifestPath, sourceManifest, parentManifest, configPath, target string
	var yes, showVersion bool
	flags.BoolVar(&showVersion, "version", false, "print SaxBase CLI version")
	flags.BoolVar(&yes, "yes", false, "confirm writes to protected targets")
	flags.StringVar(&dir, "dir", "", "migration directory")
	flags.StringVar(&objectDir, "objects-dir", "", "full-state object directory")
	flags.StringVar(&sourceManifest, "source-manifest", "", "active release manifest for rollback")
	flags.StringVar(&parentManifest, "parent-manifest", "", "previous release manifest when creating a delta")
	flags.StringVar(&manifestPath, "manifest", "", "release manifest")
	flags.StringVar(&configPath, "config", "", "target configuration file")
	flags.StringVar(&target, "target", "", "database target")
	if parseErr := flags.Parse(args); parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			_, err = io.WriteString(out, usage)
			return err
		}
		return parseErr
	}
	if showVersion {
		if flags.NArg() != 0 {
			return errors.New("--version does not accept a command")
		}
		_, err = fmt.Fprintf(out, "saxbase %s\n", Version)
		return err
	}
	var lookup []func(string) (string, bool)
	targetOut := out
	var input io.Reader
	if len(environment) > 0 {
		if environment[0].lookup != nil {
			lookup = append(lookup, environment[0].lookup)
		}
		targetOut = environment[0].diagnostics
		input = environment[0].input
	}
	getenv, err = loadDotEnv(getenv, lookup...)
	if err != nil {
		return err
	}
	cfg := migrations.Config{Driver: getenv("GOOSE_DRIVER"), DSN: getenv("GOOSE_DBSTRING"), Dir: getenv("GOOSE_MIGRATION_DIR")}
	if cfg.Driver == "" {
		cfg.Driver = "mssql"
	}
	if cfg.Dir == "" {
		cfg.Dir = "database/migrations"
	}
	if dir != "" {
		cfg.Dir = dir
	}
	if objectDir == "" {
		objectDir = getenv("SAXBASE_OBJECTS_DIR")
	}
	if objectDir == "" {
		objectDir = "database/objects"
	}
	resolveTarget := func(positional bool, command string) error {
		return selectTarget(configPath, target, positional, getenv, &cfg, targetOut, func(name string) error {
			switch command {
			case "apply", "migration up", "migration down", "rollback":
				if !yes {
					return confirmWrite(ctx, name, command, input, targetOut)
				}
			}
			return nil
		})
	}
	pos := flags.Args()
	if parentManifest != "" && !(len(pos) > 1 && pos[0] == "release" && pos[1] == "create") {
		return errors.New("-parent-manifest only applies to release create")
	}
	if sourceManifest != "" && !(len(pos) > 0 && pos[0] == "rollback") {
		return errors.New("-source-manifest only applies to rollback")
	}
	if len(pos) > 0 && pos[0] == "rollback" {
		if err := resolveTarget(false, "rollback"); err != nil {
			return err
		}
		return runRollback(ctx, pos[1:], cfg, manifestPath, sourceManifest, objectDir, out, open, openObjects)
	}
	if len(pos) > 0 && pos[0] == "release" {
		return runRelease(ctx, pos[1:], manifestPath, objectDir, parentManifest, out)
	}
	var command string
	switch len(pos) {
	case 1:
		command = pos[0]
	case 2:
		if pos[0] != "migration" && pos[0] != "objects" {
			return errors.New("expected migration up or migration down")
		}
		command = "objects " + pos[1]
		if pos[0] == "migration" {
			command = "migration " + pos[1]
		}
	case 3:
		cfg.Driver, cfg.DSN, command = pos[0], pos[1], pos[2]
	case 4:
		if pos[2] != "objects" && pos[2] != "migration" {
			return errors.New("expected DRIVER CONNECTION_STRING migration up|down")
		}
		cfg.Driver, cfg.DSN, command = pos[0], pos[1], pos[2]+" "+pos[3]
	default:
		return errors.New("expected COMMAND or DRIVER CONNECTION_STRING COMMAND; use -h for help")
	}
	switch command {
	case "apply", "status", "plan", "migration up", "migration down":
	default:
		return fmt.Errorf("unknown command %q; use -h for help", command)
	}
	if err := resolveTarget(len(pos) >= 3, command); err != nil {
		return err
	}
	if cfg.Driver != "mssql" && cfg.Driver != "sqlserver" {
		return fmt.Errorf("unsupported driver %q: use mssql or sqlserver", cfg.Driver)
	}
	if manifestPath != "" && command != "plan" && command != "apply" && command != "status" {
		return errors.New("-manifest is supported only for plan, apply, status, and release commands")
	}
	if cfg.DSN == "" {
		return errors.New("set GOOSE_DBSTRING or provide a connection string")
	}
	if command == "apply" {
		return runApply(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	if command == "plan" {
		return runPlan(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	if command == "status" {
		return runStatus(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	engine, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	switch command {
	case "migration up":
		err = engine.Up(ctx)
	case "migration down":
		err = engine.Down(ctx)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	return nil
}

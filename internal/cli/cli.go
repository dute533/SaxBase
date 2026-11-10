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
  saxbase [-manifest database/release.json] plan|status
  saxbase [-f] [-manifest database/release.json] apply
  saxbase rollback [VERSION]
  saxbase [-dir database/migrations] migration create NAME
  saxbase [-manifest database/release.json] release create VERSION
  saxbase [-manifest database/release.json] release validate

Commands:
  plan      Preview schema migrations and objects without changing the database
  apply     Apply every pending manifest release in order and record each one
  status    Show database schema, release, and object state together
  rollback  Restore the previous or a specified recorded release
  migration create NAME  Create the next sequential Goose SQL migration
  release create VERSION    Add or refresh a release from current object files
  release validate        Check the manifest against current object files

Environment:
  GOOSE_DRIVER    mssql (default) or sqlserver
  GOOSE_DBSTRING  SQL Server connection string with an explicit database
  GOOSE_MIGRATION_DIR  Migration directory (default database/migrations)
  SAXBASE_OBJECTS_DIR  Object directory (default database/objects)

Configuration:
  -config PATH   Target config (default saxbase.yaml)
  -dir PATH      Migration directory (default database/migrations)
  -objects-dir PATH  Object directory (default database/objects)
  -manifest PATH Release manifest (default database/release.json)
  -f, -force     Rewrite and reapply the selected release (development only)
  -target NAME   Database target (defaults to default_target in config)
  -yes           Confirm database writes to targets requiring confirmation
  .env           Loaded from the working directory; environment takes precedence

Options may appear before or after the command.
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
	args, err = reorderOptions(args)
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("saxbase", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var dir, objectDir, manifestPath, configPath, target string
	var force, yes, showVersion bool
	flags.BoolVar(&showVersion, "version", false, "print SaxBase CLI version")
	flags.BoolVar(&force, "f", false, "rewrite and reapply the selected release")
	flags.BoolVar(&force, "force", false, "rewrite and reapply the selected release")
	flags.BoolVar(&yes, "yes", false, "confirm writes to protected targets")
	flags.StringVar(&dir, "dir", "", "migration directory")
	flags.StringVar(&objectDir, "objects-dir", "", "full-state object directory")
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
			return withNext(errors.New("--version does not accept a command"), "saxbase --version")
		}
		_, err = fmt.Fprintf(out, "saxbase %s\n", Version)
		return err
	}
	pos := flags.Args()
	if help, ok := commandHelp(pos); ok {
		_, err = io.WriteString(out, help)
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
	resolveTarget := func(command string) error {
		return selectTarget(configPath, target, getenv, &cfg, targetOut, func(name string) error {
			switch command {
			case "apply", "rollback":
				if !yes {
					return confirmWrite(ctx, name, command, input, targetOut)
				}
			}
			return nil
		})
	}
	if force && !(len(pos) == 1 && pos[0] == "apply") {
		return withNext(errors.New("-f and -force only apply to the apply command"), "saxbase apply -h")
	}
	if len(pos) > 0 && pos[0] == "rollback" {
		if err := resolveTarget("rollback"); err != nil {
			return err
		}
		return runRollback(ctx, pos[1:], cfg, manifestPath, objectDir, out, open, openObjects)
	}
	if len(pos) > 0 && pos[0] == "migration" {
		return runMigration(pos[1:], cfg.Dir, out)
	}
	if len(pos) > 0 && pos[0] == "release" {
		return runRelease(ctx, pos[1:], manifestPath, objectDir, cfg.Dir, out)
	}
	var command string
	if len(pos) != 1 {
		return withNext(errors.New("expected COMMAND"), "saxbase -h")
	}
	command = pos[0]
	switch command {
	case "apply", "status", "plan":
	default:
		return withNext(fmt.Errorf("unknown command %q", command), "saxbase -h")
	}
	if err := resolveTarget(command); err != nil {
		return err
	}
	if cfg.Driver != "mssql" && cfg.Driver != "sqlserver" {
		return withNext(fmt.Errorf("unsupported driver %q", cfg.Driver), "set GOOSE_DRIVER=mssql")
	}
	if manifestPath != "" && command != "plan" && command != "apply" && command != "status" {
		return withNext(errors.New("-manifest is supported only for plan, apply, status, and release commands"), "saxbase -h")
	}
	if cfg.DSN == "" {
		return withNext(errors.New("no database connection is configured"), "set GOOSE_DBSTRING or configure saxbase.yaml")
	}
	if command == "apply" {
		return runApply(ctx, cfg, manifestPath, objectDir, force, out, open, openObjects)
	}
	if command == "plan" {
		return runPlan(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	if command == "status" {
		return runStatus(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	return fmt.Errorf("unknown command %q; use -h for help", command)
}

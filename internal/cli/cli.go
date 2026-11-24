package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"saxbase/internal/dialect"
	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releaseversion"
)

// Version is set by the release build; local source builds report dev.
var Version = "dev"

const usage = `SaxBase

Usage:
  saxbase --version
  saxbase [-manifest database/release.json] status
  saxbase [-manifest database/release.json] drift
  saxbase [-manifest database/release.json] correct
  saxbase [-f] [-manifest database/release.json] apply [VERSION]
  saxbase rollback [VERSION]
  saxbase [-dir database/migrations] migration create NAME
  saxbase [-manifest database/release.json] release create VERSION
  saxbase [-manifest database/release.json] release validate

Commands:
  apply     Apply pending manifest releases in order and record each one; stop after VERSION when given
  status    Show current state, pending changes, and deployment blockers
  drift     Compare the database against the recorded release (schema fingerprint and object definitions)
  correct   Re-apply drifted or missing pinned object files; schema drift is reported, not auto-corrected
  rollback  Restore the previous or a specified recorded release
  migration create NAME  Create the next sequential Goose SQL migration
  release create VERSION    Add or refresh a release from current object files
  release validate        Check the manifest against current object files

Environment:
  SAXBASE_DIALECT  Database dialect: sqlserver or postgres
  SAXBASE_DSN  Database connection string
  SAXBASE_MIGRATIONS_DIR  Migration directory (default database/migrations)
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
type OpenObjectsFunc func(migrations.Config) (objects.Engine, error)

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

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer, open OpenFunc, openObjects OpenObjectsFunc, environment ...runEnvironment) (err error) {
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
	dialectName, dialectErr := dialect.Parse(getenv("SAXBASE_DIALECT"))
	cfg := migrations.Config{Dialect: dialectName, DSN: getenv("SAXBASE_DSN"), Dir: getenv("SAXBASE_MIGRATIONS_DIR")}
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
			case "apply", "rollback", "correct":
				if !yes {
					return confirmWrite(ctx, name, command, input, targetOut)
				}
			}
			return nil
		})
	}
	if force && !(len(pos) > 0 && pos[0] == "apply") {
		return withNext(errors.New("-f and -force only apply to the apply command"), "saxbase apply -h")
	}
	if len(pos) > 0 && pos[0] == "rollback" {
		if err := resolveTarget("rollback"); err != nil {
			return err
		}
		if err := cfg.Dialect.Validate(); err != nil {
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
	if len(pos) == 0 || (len(pos) != 1 && pos[0] != "apply") {
		return withNext(errors.New("expected COMMAND"), "saxbase -h")
	}
	command := pos[0]
	var applyVersion string
	switch command {
	case "apply", "status", "drift", "correct":
		if len(pos) > 2 || (len(pos) == 2 && command != "apply") {
			return withNext(errors.New("expected "+command), "saxbase "+command+" -h")
		}
		if len(pos) == 2 {
			if err := releaseversion.Validate(pos[1]); err != nil {
				return withNext(err, "saxbase apply -h")
			}
			applyVersion = pos[1]
		}
	default:
		return withNext(fmt.Errorf("unknown command %q", command), "saxbase -h")
	}
	if err := resolveTarget(command); err != nil {
		return err
	}
	if err := cfg.Dialect.Validate(); err != nil {
		if dialectErr != nil {
			return dialectErr
		}
		return err
	}
	if cfg.DSN == "" {
		return withNext(errors.New("no database connection is configured"), "set SAXBASE_DSN and SAXBASE_DIALECT or configure saxbase.yaml")
	}
	if command == "apply" {
		return runApply(ctx, cfg, manifestPath, objectDir, force, applyVersion, out, open, openObjects)
	}
	if command == "status" {
		return runStatus(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	if command == "drift" {
		return runDrift(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	if command == "correct" {
		return runCorrect(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	return fmt.Errorf("unknown command %q; use -h for help", command)
}

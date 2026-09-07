package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

// Version is set by the release build; local source builds report dev.
var Version = "dev"

const usage = `SaxBase

Usage:
  saxbase --version
  saxbase [-dir database/migrations] COMMAND
  saxbase [-dir database/migrations] mssql CONNECTION_STRING COMMAND
  saxbase [-objects-dir database/objects] objects apply|status
  saxbase [-manifest database/release.json] release create VERSION
  saxbase [-manifest database/release.json] release sync [VERSION]
  saxbase [-manifest database/release.json] release validate
  saxbase release history
  saxbase release show VERSION
  saxbase release rollback VERSION
  saxbase release rollbacks
  saxbase release current
  saxbase [-manifest database/release.json] plan|deploy

Commands:
  up       Apply all pending Goose migrations
  down     Roll back one Goose migration
  status   List applied and pending migrations
  version  Print the current Goose database version
  deploy   Migrate to the manifest schema, apply objects, and record the release
  plan     Preview a manifest release without changing the database
  objects apply   Deploy changed full-state SQL objects
  objects status  Compare local objects with deployed checksums
  release create VERSION  Write a new manifest from current object files
  release sync [VERSION]  Refresh a local manifest, preserving object order
  release validate        Check the manifest against current object files
  release history         List successfully recorded database releases
  release show VERSION    Print recorded release metadata as JSON
  release rollback VERSION Restore a release using Goose and release manifests
  release rollbacks       Show rollback progress and failures as JSON
  release current         Print the last recorded active release

Environment:
  GOOSE_DRIVER    mssql (default) or sqlserver
  GOOSE_DBSTRING  SQL Server connection string
  GOOSE_MIGRATION_DIR  Migration directory (default database/migrations)
  SAXBASE_OBJECTS_DIR  Object directory (default database/objects)

Configuration:
  -config PATH   Target config (default saxbase.yaml)
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
			case "up", "down", "deploy", "objects apply", "release rollback":
				if !yes {
					return confirmWrite(ctx, name, command, input, targetOut)
				}
			}
			return nil
		})
	}
	pos := flags.Args()
	if parentManifest != "" && !(len(pos) > 1 && pos[0] == "release" && (pos[1] == "create" || pos[1] == "sync")) {
		return errors.New("-parent-manifest only applies to release create or release sync")
	}
	if sourceManifest != "" && !(len(pos) > 1 && pos[0] == "release" && pos[1] == "rollback") {
		return errors.New("-source-manifest only applies to release rollback")
	}
	if len(pos) > 0 && pos[0] == "release" {
		if len(pos) > 1 && pos[1] == "rollback" {
			if err := resolveTarget(false, "release rollback"); err != nil {
				return err
			}
			return runRollback(ctx, pos[2:], cfg, manifestPath, sourceManifest, objectDir, out, open, openObjects)
		}
		if len(pos) > 1 && (pos[1] == "history" || pos[1] == "show" || pos[1] == "rollbacks" || pos[1] == "current") {
			if manifestPath != "" {
				return errors.New("-manifest does not apply to database release history")
			}
			if err := resolveTarget(false, "release "+pos[1]); err != nil {
				return err
			}
			return runReleaseDatabase(ctx, pos[1:], cfg, out, openObjects)
		}
		return runRelease(ctx, pos[1:], manifestPath, objectDir, parentManifest, out)
	}
	var command string
	switch len(pos) {
	case 1:
		command = pos[0]
	case 2:
		if pos[0] != "objects" {
			return errors.New("expected objects apply or objects status")
		}
		command = "objects " + pos[1]
	case 3:
		cfg.Driver, cfg.DSN, command = pos[0], pos[1], pos[2]
	case 4:
		if pos[2] != "objects" {
			return errors.New("expected DRIVER CONNECTION_STRING objects apply|status")
		}
		cfg.Driver, cfg.DSN, command = pos[0], pos[1], "objects "+pos[3]
	default:
		return errors.New("expected COMMAND or DRIVER CONNECTION_STRING COMMAND; use -h for help")
	}
	switch command {
	case "up", "down", "status", "version", "objects apply", "objects status", "plan", "deploy":
	default:
		return fmt.Errorf("unknown command %q; use -h for help", command)
	}
	if err := resolveTarget(len(pos) >= 3, command); err != nil {
		return err
	}
	if cfg.Driver != "mssql" && cfg.Driver != "sqlserver" {
		return fmt.Errorf("unsupported driver %q: use mssql or sqlserver", cfg.Driver)
	}
	if manifestPath != "" && command != "objects apply" && command != "objects status" && command != "plan" && command != "deploy" {
		return errors.New("-manifest is supported only for plan, deploy, release, and objects commands")
	}
	if cfg.DSN == "" {
		return errors.New("set GOOSE_DBSTRING or provide a connection string")
	}
	if command == "deploy" {
		return runDeploy(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	if command == "plan" {
		return runPlan(ctx, cfg, manifestPath, objectDir, out, open, openObjects)
	}
	if command == "objects apply" || command == "objects status" {
		var releaseVersion *releases.Version
		var parentVersion string
		var manifest releases.Manifest
		var files []objects.File
		if manifestPath == "" {
			var scanErr error
			files, scanErr = releases.WorkingFiles(ctx, objectDir)
			if scanErr != nil {
				return fmt.Errorf("scan objects: %w", scanErr)
			}
		}
		if manifestPath != "" {
			loaded, loadErr := releases.Load(manifestPath)
			if loadErr != nil {
				return loadErr
			}
			manifest = loaded
			var validateErr error
			files, validateErr = manifest.Resolve(ctx, ".")
			if validateErr != nil {
				return validateErr
			}
			if command == "objects apply" {
				var parentErr error
				parentVersion, parentErr = manifestParentVersion(manifestPath, manifest)
				if parentErr != nil {
					return parentErr
				}
				if checkErr := checkSchema(ctx, cfg, manifest, open); checkErr != nil {
					return checkErr
				}
				version, _ := releases.ParseVersion(manifest.Version)
				releaseVersion = &version
			}
		}
		engine, openErr := openObjects(cfg.DSN)
		if openErr != nil {
			return openErr
		}
		defer func() { err = errors.Join(err, engine.Close()) }()
		var rows []objects.Status
		if command == "objects apply" {
			if releaseVersion != nil {
				if parentVersion != "" {
					current, currentErr := engine.Current(ctx)
					if currentErr != nil {
						return currentErr
					}
					if current != parentVersion {
						return fmt.Errorf("release %s must follow current release %s", manifest.Version, parentVersion)
					}
				}
				rows, err = engine.ApplyRelease(ctx, files, releaseVersion.Schema, releaseVersion.Revision)
			} else {
				rows, err = engine.Apply(ctx, files)
			}
		} else {
			rows, err = engine.Status(ctx, files)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", command, err)
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "STATE\tFILE\tSHA256")
		for _, row := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\n", row.State, row.Path, row.Checksum)
		}
		return w.Flush()
	}
	engine, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	switch command {
	case "up":
		err = engine.Up(ctx)
	case "down":
		err = engine.Down(ctx)
	case "version":
		var version int64
		version, err = engine.Version(ctx)
		if err == nil {
			_, err = fmt.Fprintln(out, version)
		}
	case "status":
		var rows []migrations.Status
		rows, err = engine.Status(ctx)
		if err == nil {
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "VERSION\tSTATE\tFILE")
			for _, row := range rows {
				fmt.Fprintf(w, "%d\t%s\t%s\n", row.Version, row.State, row.Path)
			}
			err = w.Flush()
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	return nil
}

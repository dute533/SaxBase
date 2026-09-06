package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
	"saxbase/internal/releases"
)

const usage = `SaxBase

Usage:
  saxbase [-dir database/migrations] COMMAND
  saxbase [-dir database/migrations] mssql CONNECTION_STRING COMMAND
  saxbase [-objects-dir database/objects] objects apply|status
  saxbase [-manifest database/release.json] release create VERSION
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
  release validate        Check the manifest against current object files
  release history         List successfully recorded database releases
  release show VERSION    Print a stored release and its SQL definitions as JSON
  release rollback VERSION Restore a recorded release using Goose and stored SQL
  release rollbacks       Show rollback progress and failures as JSON
  release current         Print the last recorded active release

Environment:
  GOOSE_DRIVER    mssql (default) or sqlserver
  GOOSE_DBSTRING  SQL Server connection string
  GOOSE_MIGRATION_DIR  Migration directory (default database/migrations)
  SAXBASE_OBJECTS_DIR  Object directory (default database/objects)

Options must precede the command or connection arguments.
`

type OpenFunc func(migrations.Config) (migrations.Engine, error)

func Run(ctx context.Context, args []string, getenv func(string) string, out io.Writer, open OpenFunc) (err error) {
	return run(ctx, args, getenv, out, open, objects.Open)
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer, open OpenFunc, openObjects func(string) (objects.Engine, error)) (err error) {
	if len(args) == 0 {
		_, err = io.WriteString(out, usage)
		return err
	}
	cfg := migrations.Config{Driver: getenv("GOOSE_DRIVER"), DSN: getenv("GOOSE_DBSTRING"), Dir: getenv("GOOSE_MIGRATION_DIR")}
	if cfg.Driver == "" {
		cfg.Driver = "mssql"
	}
	if cfg.Dir == "" {
		cfg.Dir = "database/migrations"
	}
	flags := flag.NewFlagSet("saxbase", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.Dir, "dir", cfg.Dir, "migration directory")
	objectDir := getenv("SAXBASE_OBJECTS_DIR")
	if objectDir == "" {
		objectDir = "database/objects"
	}
	flags.StringVar(&objectDir, "objects-dir", objectDir, "full-state object directory")
	var manifestPath string
	flags.StringVar(&manifestPath, "manifest", "", "release manifest (release commands default to database/release.json)")
	if parseErr := flags.Parse(args); parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			_, err = io.WriteString(out, usage)
			return err
		}
		return parseErr
	}
	pos := flags.Args()
	if len(pos) > 0 && pos[0] == "release" {
		if len(pos) > 1 && pos[1] == "rollback" {
			if manifestPath != "" {
				return errors.New("rollback uses database snapshots, not -manifest")
			}
			return runRollback(ctx, pos[2:], cfg, out, open, openObjects)
		}
		if len(pos) > 1 && (pos[1] == "history" || pos[1] == "show" || pos[1] == "rollbacks" || pos[1] == "current") {
			if manifestPath != "" {
				return errors.New("-manifest does not apply to database release history")
			}
			return runReleaseDatabase(ctx, pos[1:], cfg, out, openObjects)
		}
		return runRelease(pos[1:], manifestPath, objectDir, out)
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
		files, scanErr := objects.Scan(objectDir)
		if scanErr != nil {
			return fmt.Errorf("scan objects: %w", scanErr)
		}
		if manifestPath != "" {
			manifest, loadErr := releases.Load(manifestPath)
			if loadErr != nil {
				return loadErr
			}
			if validateErr := manifest.Validate(files); validateErr != nil {
				return validateErr
			}
			if command == "objects apply" {
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

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
)

const usage = `SaxBase

Usage:
  saxbase [-dir database/migrations] COMMAND
  saxbase [-dir database/migrations] mssql CONNECTION_STRING COMMAND
  saxbase [-objects-dir database/objects] objects apply|status

Commands:
  up       Apply all pending Goose migrations
  down     Roll back one Goose migration
  status   List applied and pending migrations
  version  Print the current Goose database version
  objects apply   Deploy changed full-state SQL objects
  objects status  Compare local objects with deployed checksums

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
	if parseErr := flags.Parse(args); parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			_, err = io.WriteString(out, usage)
			return err
		}
		return parseErr
	}
	pos := flags.Args()
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
	case "up", "down", "status", "version", "objects apply", "objects status":
	default:
		return fmt.Errorf("unknown command %q; use -h for help", command)
	}
	if cfg.Driver != "mssql" && cfg.Driver != "sqlserver" {
		return fmt.Errorf("unsupported driver %q: use mssql or sqlserver", cfg.Driver)
	}
	if cfg.DSN == "" {
		return errors.New("set GOOSE_DBSTRING or provide a connection string")
	}
	if command == "objects apply" || command == "objects status" {
		files, scanErr := objects.Scan(objectDir)
		if scanErr != nil {
			return fmt.Errorf("scan objects: %w", scanErr)
		}
		engine, openErr := openObjects(cfg.DSN)
		if openErr != nil {
			return openErr
		}
		defer func() { err = errors.Join(err, engine.Close()) }()
		var rows []objects.Status
		if command == "objects apply" {
			rows, err = engine.Apply(ctx, files)
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

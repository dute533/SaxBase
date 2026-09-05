package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"saxbase/internal/migrations"
)

const usage = `SaxBase

Usage:
  saxbase [-dir database/migrations] COMMAND
  saxbase [-dir database/migrations] mssql CONNECTION_STRING COMMAND

Commands:
  up       Apply all pending Goose migrations
  down     Roll back one Goose migration
  status   List applied and pending migrations
  version  Print the current Goose database version

Environment:
  GOOSE_DRIVER    mssql (default) or sqlserver
  GOOSE_DBSTRING  SQL Server connection string
  GOOSE_MIGRATION_DIR  Migration directory (default database/migrations)

Options must precede the command or connection arguments.
`

type OpenFunc func(migrations.Config) (migrations.Engine, error)

func Run(ctx context.Context, args []string, getenv func(string) string, out io.Writer, open OpenFunc) (err error) {
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
	case 3:
		cfg.Driver, cfg.DSN, command = pos[0], pos[1], pos[2]
	default:
		return errors.New("expected COMMAND or DRIVER CONNECTION_STRING COMMAND; use -h for help")
	}
	switch command {
	case "up", "down", "status", "version":
	default:
		return fmt.Errorf("unknown command %q; use -h for help", command)
	}
	if cfg.Driver != "mssql" && cfg.Driver != "sqlserver" {
		return fmt.Errorf("unsupported driver %q: use mssql or sqlserver", cfg.Driver)
	}
	if cfg.DSN == "" {
		return errors.New("set GOOSE_DBSTRING or provide a connection string")
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

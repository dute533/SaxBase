package cli

import (
	"errors"
	"fmt"
	"io"

	"saxbase/internal/migrations"
)

const migrationCreateUsage = `Usage:
  saxbase [-dir database/migrations] migration create NAME

Create the next sequential Goose SQL migration.
`

func runMigration(args []string, dir string, out io.Writer) error {
	if len(args) == 2 && args[0] == "create" && (args[1] == "-h" || args[1] == "--help") {
		_, err := io.WriteString(out, migrationCreateUsage)
		return err
	}
	if len(args) != 2 || args[0] != "create" {
		return errors.New("expected migration create NAME")
	}
	path, err := migrations.Create(dir, args[1])
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(out, "Created migration: %s\n", path); err != nil {
		return err
	}
	return writeNext(out, fmt.Sprintf("edit %s", path))
}

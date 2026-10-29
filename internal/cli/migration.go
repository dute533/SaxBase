package cli

import (
	"errors"
	"fmt"
	"io"

	"saxbase/internal/migrations"
)

func runMigration(args []string, dir string, out io.Writer) error {
	if len(args) != 2 || args[0] != "create" {
		return errors.New("expected migration create NAME")
	}
	path, err := migrations.Create(dir, args[1])
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Created migration: %s\n", path)
	return err
}

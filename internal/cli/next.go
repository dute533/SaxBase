package cli

import (
	"fmt"
	"io"
)

func writeNext(out io.Writer, action string) error {
	_, err := fmt.Fprintf(out, "\nNext: %s\n", action)
	return err
}

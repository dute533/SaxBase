package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

func confirmWrite(ctx context.Context, target, command string, input io.Reader, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if input == nil {
		return withNext(errors.New("target requires confirmation"), fmt.Sprintf("saxbase %s -target %s -yes", command, shellQuote(target)))
	}
	if _, err := fmt.Fprintf(out, "Run %s on target %q? Type yes to continue: ", command, target); err != nil {
		return err
	}
	result := make(chan bool, 1)
	go func() {
		line, err := bufio.NewReader(input).ReadString('\n')
		result <- err == nil && strings.EqualFold(strings.TrimSpace(line), "yes")
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case accepted := <-result:
		if !accepted {
			return withNext(errors.New("database operation cancelled; confirmation requires yes"), fmt.Sprintf("rerun saxbase %s -target %s and type yes", command, shellQuote(target)))
		}
		return nil
	}
}

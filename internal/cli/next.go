package cli

import (
	"fmt"
	"io"
	"strings"
)

func writeNext(out io.Writer, action string) error {
	_, err := fmt.Fprintf(out, "\nNext: %s\n", action)
	return err
}

func withNext(err error, action string) error {
	if err == nil || strings.Contains(err.Error(), "\nNext:") {
		return err
	}
	return fmt.Errorf("%w\n\nNext: %s", err, action)
}

func gitCommitCommand(paths []string) string {
	quoted := make([]string, 0, len(paths))
	for _, path := range paths {
		quoted = append(quoted, shellQuote(path))
	}
	return "git add -- " + strings.Join(quoted, " ") + " && git commit"
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

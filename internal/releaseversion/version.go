// Package releaseversion validates database release identifiers.
package releaseversion

import (
	"fmt"
	"regexp"
)

var identifierPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]*$`)

const MaxLength = 39

func Validate(value string) error {
	if len(value) == 0 || len(value) > MaxLength || !identifierPattern.MatchString(value) {
		return fmt.Errorf("invalid release version %q: use 1-39 letters, numbers, dots, plus signs, or hyphens", value)
	}
	return nil
}

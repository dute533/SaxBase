// Package releaseversion validates release identifiers and parses the legacy
// SCHEMA[.REVISION] form used by manifests without migration_version.
package releaseversion

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	legacyPattern     = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.([1-9][0-9]*))?$`)
	identifierPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]*$`)
)

const MaxLength = 39

type Version struct {
	Schema   int64
	Revision int64
}

func Validate(value string) error {
	if len(value) == 0 || len(value) > MaxLength || !identifierPattern.MatchString(value) {
		return fmt.Errorf("invalid release version %q: use 1-39 letters, numbers, dots, plus signs, or hyphens", value)
	}
	return nil
}

func ParseLegacy(value string) (Version, error) {
	if !legacyPattern.MatchString(value) {
		return Version{}, fmt.Errorf("invalid release version %q: expected 30 or 30.1", value)
	}
	parts := strings.Split(value, ".")
	schema, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Version{}, fmt.Errorf("schema version out of range: %w", err)
	}
	var revision int64
	if len(parts) == 2 {
		revision, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("release revision out of range: %w", err)
		}
	}
	return Version{Schema: schema, Revision: revision}, nil
}

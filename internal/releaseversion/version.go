// Package releaseversion parses and compares canonical SaxBase release versions.
package releaseversion

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var pattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.([1-9][0-9]*))?$`)

type Version struct {
	Schema   int64
	Revision int64
}

func Parse(value string) (Version, error) {
	if !pattern.MatchString(value) {
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

func Compare(a, b Version) int {
	if a.Schema < b.Schema || (a.Schema == b.Schema && a.Revision < b.Revision) {
		return -1
	}
	if a == b {
		return 0
	}
	return 1
}

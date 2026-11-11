// Package releaseversion validates database release identifiers.
package releaseversion

import (
	"fmt"
	"strings"
)

const MaxLength = 39

func Validate(value string) error {
	invalid := func() error {
		return fmt.Errorf("invalid release version %q: expected SemVer MAJOR.MINOR.PATCH, optionally with -PRERELEASE and +BUILD", value)
	}
	if len(value) == 0 || len(value) > MaxLength || strings.Count(value, "+") > 1 {
		return invalid()
	}
	versionAndPre, build, hasBuild := strings.Cut(value, "+")
	if hasBuild && !validIdentifiers(build, false) {
		return invalid()
	}
	core, prerelease, hasPrerelease := strings.Cut(versionAndPre, "-")
	if hasPrerelease && !validIdentifiers(prerelease, true) {
		return invalid()
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return invalid()
	}
	for _, part := range parts {
		if !numericIdentifier(part) {
			return invalid()
		}
	}
	return nil
}

func validIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, char := range identifier {
			if char < '0' || char > '9' {
				numeric = false
			}
			if !((char >= '0' && char <= '9') || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || char == '-') {
				return false
			}
		}
		if rejectNumericLeadingZero && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func numericIdentifier(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

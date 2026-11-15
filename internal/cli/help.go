package cli

import "strings"

const applyUsage = `Usage:
  saxbase [options] apply

Apply every pending release in order. Use -f only to rewrite the current development release.
`

const statusUsage = `Usage:
  saxbase [options] status

Show current release and schema, pending changes, deployment blockers, and next action.
Status is read-only and is not required before apply.
`

const rollbackUsage = `Usage:
  saxbase [options] rollback [VERSION]

Restore the previous release, or the initial state (schema 0) from the first release.
Traverse back to VERSION when supplied.
`

const migrationUsage = `Usage:
  saxbase [options] migration create NAME

Create a sequential Goose SQL migration.
`

const releaseUsage = `Usage:
  saxbase [options] release create VERSION
  saxbase [options] release validate

Create or validate release manifest entries.
`

const releaseCreateUsage = `Usage:
  saxbase [options] release create VERSION

Create a release from committed object files. VERSION must be SemVer, such as
1.2.0 or 2.0.0-rc.1. SaxBase infers the target migration.
`

const releaseValidateUsage = `Usage:
  saxbase [options] release validate

Validate that the latest release resolves all referenced object files.
`

func commandHelp(args []string) (string, bool) {
	if len(args) < 2 || (args[len(args)-1] != "-h" && args[len(args)-1] != "--help") {
		return "", false
	}
	switch strings.Join(args[:len(args)-1], " ") {
	case "apply":
		return applyUsage, true
	case "status":
		return statusUsage, true
	case "rollback":
		return rollbackUsage, true
	case "migration":
		return migrationUsage, true
	case "migration create":
		return migrationCreateUsage, true
	case "release":
		return releaseUsage, true
	case "release create":
		return releaseCreateUsage, true
	case "release validate":
		return releaseValidateUsage, true
	default:
		return "", false
	}
}

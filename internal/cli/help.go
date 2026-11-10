package cli

import "strings"

const planUsage = `Usage:
  saxbase [options] plan

Preview the next release's schema and object actions without changing the database.
`

const applyUsage = `Usage:
  saxbase [options] apply

Apply every pending release in order. Use -f only to rewrite the current development release.
`

const statusUsage = `Usage:
  saxbase [options] status

Show the current database schema, release, migrations, objects, and next action.
`

const rollbackUsage = `Usage:
  saxbase [options] rollback [VERSION]

Restore the previous recorded release, or traverse back to VERSION when supplied.
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

Create a release from committed object files. VERSION may be semantic, such as
1.2.0. SaxBase infers the target migration.
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
	case "plan":
		return planUsage, true
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

# SaxBase

SaxBase deploys SQL Server and PostgreSQL migrations with versioned views,
procedures, and functions as one release. Goose manages structural migrations;
SaxBase applies complete object definitions in dependency order.

Use migrations for tables and data changes. Use object definitions for views,
procedures, and functions.

## Install

Download a binary from [GitHub Releases](https://github.com/dute533/SaxBase/releases),
or build from source:

```sh
go build -o saxbase .
```

Copy the configuration templates and provide a database connection string:

```sh
cp saxbase.yaml.example saxbase.yaml
cp .env.example .env
```

Every target selects its database explicitly:

```yaml
targets:
  local:
    dialect: sqlserver # or postgres
    connection_env: SAXBASE_LOCAL_DSN
```

SaxBase expects this layout by default:

```text
database/
  migrations/       # Goose SQL migrations
  objects/          # Complete view, procedure, and function definitions
  release.json      # Release history
```

## Create and deploy a release

Create a migration when the database structure or data must change:

```sh
./saxbase migration create "Add customer email"
```

Edit its Up and Down sections, then add or update complete SQL definitions under
`database/objects`. Commit object files before creating the release so SaxBase
can pin them to Git for reproducible rollback:

```sh
git add database/migrations database/objects
git commit -m "Add customer email"

./saxbase release create 1.0.0
git add database/release.json
git commit -m "Create database release"

./saxbase status
./saxbase apply
./saxbase status
```

Release versions must use SemVer. SaxBase records the target Goose migration
separately, so object-only releases can advance from `1.0.0` to `1.0.1` without
adding a migration. Every manifest entry declares its migration target.

`status` shows current state, the pending release chain, and the next release’s
migration and object changes and blockers. It is optional; `apply` validates and
deploys all pending releases in order. Use `-target NAME` to select a configured database.

## Rollback

Restore the preceding release (or the initial state from the first release):

```sh
./saxbase rollback
```

Pass a version to traverse back through multiple releases:

```sh
./saxbase rollback 1.0.0
```

SaxBase restores historical object definitions and runs the required Goose Down
migrations one release at a time.

## Drift detection

Compare the live database against the current release:

```sh
./saxbase drift
```

It prints `No drift detected.` when clean, and exits non-zero with exactly what
changed when not: the schema fingerprint recorded at the last `apply`/`rollback`
is diffed against the live catalog, pinned object files are compared with the live
definitions, and the release is replayed into a scratch database to catch
divergence from what the release describes. Objects added by hand that SaxBase
does not manage are ignored. See the [reference](docs/reference.md#drift).

## Commands

| Command | Purpose |
| --- | --- |
| `apply` | Apply every pending release |
| `status` | Show current state, pending changes, and deployment blockers |
| `rollback [VERSION]` | Restore the previous or specified release |
| `drift` | Detect drift between the database and the current release |
| `drift correct [-n]` | Align the database to the current release (objects, triggers, tables, columns, constraints); `-n` dry run |
| `migration create NAME` | Create a sequential Goose migration |
| `release create VERSION` | Create or refresh a release manifest entry |
| `release validate` | Validate manifest object references |

Run `./saxbase -h` for all options. See the
[working SQL Server example](examples/README.md) and
[full reference](docs/reference.md) for configuration, manifest behavior,
development force-apply, and failure recovery.

## Development

```sh
go test ./...
go vet ./...
bash scripts/test-integration.sh
```

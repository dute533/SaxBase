# SaxBase

SaxBase deploys SQL Server migrations and versioned views, procedures, and
functions as one release. Goose manages structural migrations; SaxBase applies
complete object definitions in dependency order.

Use migrations for tables and data changes. Use object definitions for views,
procedures, and functions.

## Install

Download a binary from [GitHub Releases](https://github.com/dute533/SaxBase/releases),
or build from source:

```sh
go build -o saxbase .
```

Copy the configuration templates and provide a SQL Server connection string:

```sh
cp saxbase.yaml.example saxbase.yaml
cp .env.example .env
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

./saxbase release create
git add database/release.json
git commit -m "Create database release"

./saxbase plan
./saxbase apply
./saxbase status
```

`release create` infers the next version. The whole-number portion selects the
migration version, while the decimal portion represents another object state at
the same schema version: release `2.1` uses database schema `2`.

`plan` previews every pending migration and object change. `apply` deploys all
pending releases in order. Use `-target NAME` to select a configured database.

## Rollback

Restore the preceding release:

```sh
./saxbase rollback
```

Pass a version to traverse back through multiple releases:

```sh
./saxbase rollback 2.1
```

SaxBase restores historical object definitions and runs the required Goose Down
migrations one release at a time.

## Commands

| Command | Purpose |
| --- | --- |
| `plan` | Preview pending migrations and objects |
| `apply` | Apply every pending release |
| `status` | Show schema, release, and object state |
| `rollback [VERSION]` | Restore the previous or specified release |
| `migration create NAME` | Create a sequential Goose migration |
| `release create` | Create or refresh a release manifest entry |
| `release validate` | Validate manifest object references |

Run `./saxbase -h` for all options. See the
[working SQL Server example](examples/sqlserver/README.md) and
[full reference](docs/reference.md) for configuration, manifest behavior,
development force-apply, and failure recovery.

## Development

```sh
go test ./...
go vet ./...
bash scripts/test-integration.sh
```

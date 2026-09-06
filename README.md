# SaxBase

A SQL Server deployment CLI that combines [Goose](https://github.com/pressly/goose)
schema migrations with versioned views, procedures, and functions.

**Work in progress — no stable release yet.** Requires Go 1.26+ and SQL Server.

## Quick start

```sh
go build -o saxbase .
export GOOSE_DRIVER=mssql
export GOOSE_DBSTRING='sqlserver://USER:PASSWORD@localhost:1433?database=example'
```

Organize your SQL like this (see the [runnable example](examples/sqlserver/README.md)):

```text
database/
  migrations/       # Goose SQL files with Up and Down sections
  objects/          # One CREATE OR ALTER view, procedure, or function per SQL file
  release.json      # Generated release manifest
```

Create and deploy a release, using your target Goose migration version:

```sh
./saxbase release create 30
# Review release.json: put object dependencies before their consumers.
./saxbase plan                 # Preview changes without writing to the database
./saxbase deploy               # Migrate to version 30, then apply changed objects
```

The manifest records object paths, checksums, and deployment order. Commit it
alongside your SQL. Object files must each contain a single SQL batch without `GO`.

## Update a release

After editing SQL, sync the manifest and deploy a new version:

```sh
./saxbase release sync 30.1
./saxbase plan
./saxbase deploy
```

`30.1` is an object revision at schema version `30`; use `31` when targeting
migration `31`. Deployed versions are immutable, so changes need a new version.
Sync preserves existing order, appends new files alphabetically, and rejects
missing files. Review dependencies when adding objects.

## Useful commands

| Command | Purpose |
| --- | --- |
| `release validate` | Check the manifest against local SQL files |
| `release current` / `release history` | Inspect deployed releases |
| `release show VERSION` | Print a stored release and its SQL |
| `release rollback VERSION` | Restore a recorded release |
| `release rollbacks` | Inspect rollback progress and failures |
| `status` / `version` | Inspect Goose migrations |
| `up` / `down` | Apply all pending migrations / undo one migration |
| `objects status` / `objects apply` | Compare / apply objects without recording a release |

Run `./saxbase -h` for help. Override default paths with `-dir`, `-objects-dir`,
or `-manifest`, placed before the command:

```sh
./saxbase -manifest database/release-30.1.json deploy
```

## Before deploying

- `deploy` stops at the manifest's schema version; `up` applies all pending migrations.
- A release is not one transaction. If deployment fails, completed migrations
  remain; fix the cause, sync the manifest if SQL changed, and retry.
- Rollback can drop newer objects and run Goose Down migrations that remove data.
  Keep the migration files available. After a failed rollback, fix the cause and
  retry the same command to resume.
- Normal deployment never drops missing objects. Dependencies are ordered by you;
  checksums do not detect manual database edits.

## Development

```sh
go test ./...
go vet ./...
bash scripts/test-integration.sh  # Requires Docker; runs disposable SQL Server
```

See the [full reference](docs/reference.md) for configuration, transaction and
rollback behavior, and testing details, or the
[dependency ordering example](examples/sqlserver/ordering/README.md).

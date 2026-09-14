# SaxBase

SaxBase deploys SQL Server schema migrations and versioned views, procedures, and
functions from one release manifest. Goose manages structural migrations; SaxBase
applies object definitions in the manifest’s order.

## Quick start

```sh
go build -o saxbase .
cp saxbase.yaml.example saxbase.yaml
cp .env.example .env
```

Put connection variables in `.env` (or export `GOOSE_DBSTRING`). Defaults are:

```text
database/migrations/
database/objects/
database/release.json
```

Use the native form
`sqlserver://USER:PASSWORD@HOST:PORT?database=DATABASE`. JDBC connection strings
are rejected because the SQL Server Go driver does not interpret
`databaseName`, which can otherwise cause a connection to the login's default
database.

Preview and apply the release:

```sh
./saxbase plan
./saxbase apply
./saxbase status
```

`plan` shows the next entry and, when needed, the full pending release chain.
`apply` advances through every pending manifest entry in order, migrating Goose,
applying changed objects, and recording each release as a separate deployment
step. Use `-target NAME` for a configured target and `-yes` for unattended
protected deployments.

## Create a release

Commit object SQL, create a manifest, review its order, then apply it:

```sh
git add database/objects
git commit -m "Update database objects"
./saxbase release create 30
./saxbase plan
./saxbase apply
```

The default `database/release.json` is a release history with the newest release
at the top. Creating a newer release prepends it as a delta from the previous
latest release, so
`./saxbase release create 31` can be used again without choosing a new
filename. Older releases remain available in the same file for rollback.

Each object entry contains a path and either a full Git commit hash or `latest`.
Full hashes make releases reproducible. `latest` reads the working tree and is
useful for an initial release in tests or projects without Git; adding another
release requires Git-backed references so previous contents remain available.

For a later object revision, run `release create` again. SaxBase compares the
working tree with the latest resolved state and adds only the changes at the top:

```sh
./saxbase release create 30.1
./saxbase plan
./saxbase apply
```

Move entries in the manifest to put dependencies before their consumers. A release
recorded in a database is immutable; use a new revision when SQL, paths, or order
changes after deployment. Before deployment, rerun `release create` with the
newest version to refresh that entry while leaving older releases unchanged.

For an intentionally mutable development database, force the refreshed release:

```sh
./saxbase release create 30.1
./saxbase -f apply
```

Force apply re-executes every object entry in the selected release and replaces
its recorded fingerprint. It does not infer operations removed from the old
release entry; ensuring that the refreshed delta produces the intended database
state is the user's responsibility. Do not use force for shared or production
databases.

## Rollback

Roll back directly to any earlier version in the manifest history:

```sh
./saxbase rollback 30
```

Rollback restores historical objects and runs the required Goose Down migrations.

## Commands

| Command | Purpose |
| --- | --- |
| `plan` | Preview migrations and objects |
| `apply` | Apply a manifest release |
| `status` | Show migration, release, and object state |
| `rollback VERSION` | Restore a recorded release |
| `release create/validate` | Manage manifests |

Run `./saxbase -h` for the complete option list.

See the [SQL Server example](examples/sqlserver/README.md) and the
[full reference](docs/reference.md).

## Development

```sh
go test ./...
go vet ./...
bash scripts/test-integration.sh
```

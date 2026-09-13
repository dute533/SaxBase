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

Preview and apply the release:

```sh
./saxbase plan
./saxbase apply
./saxbase status
```

`apply` advances one entry in the manifest history, migrates Goose to that
entry's schema version, applies its changed objects in manifest order, and
records the release. Run it again while more releases are pending. Use
`-target NAME` for a configured target and `-yes` for unattended protected
deployments.

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

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

`apply` migrates Goose to the manifest’s schema version, applies changed objects
in manifest order, and records the release. Use `-target NAME` for a configured
target and `-yes` for unattended protected deployments.

## Create a release

Commit object SQL, create a manifest, review its order, then apply it:

```sh
git add database/objects
git commit -m "Update database objects"
./saxbase release create 30
./saxbase plan
./saxbase apply
```

Each object entry contains a path and either a full Git commit hash or `latest`.
Full hashes make releases reproducible. `latest` reads the working tree and is useful
for tests or projects without Git.

For a later object revision, create a delta from its parent:

```sh
./saxbase -parent-manifest database/release-30.json \
  -manifest database/release-30.1.json release create 30.1
./saxbase -manifest database/release-30.1.json plan
./saxbase -manifest database/release-30.1.json apply
```

Move entries in the manifest to put dependencies before their consumers. A release
version is immutable; use a new revision when SQL, paths, or order changes.

## Rollback

Keep the target and active manifests available:

```sh
./saxbase -manifest database/release-30.json \
  -source-manifest database/release-30.1.json rollback 30
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
| `release history` | Inspect release history |

For direct subsystem maintenance, use the advanced `migration` and `objects`
namespaces. Run `./saxbase -h` for details.

See the [SQL Server example](examples/sqlserver/README.md) and the
[full reference](docs/reference.md).

## Development

```sh
go test ./...
go vet ./...
bash scripts/test-integration.sh
```

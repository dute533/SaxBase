# SaxBase

A SQL Server deployment CLI that combines [Goose](https://github.com/pressly/goose)
schema migrations with versioned views, procedures, and functions.

**Work in progress — no stable release yet.** Requires SQL Server.

Download Linux, macOS, and Windows binaries (amd64/arm64) from
[GitHub Releases](https://github.com/dute533/SaxBase/releases) once published.
Extract the archive and use `saxbase` (`saxbase.exe` on Windows); Go is only needed
to build from source. Preview builds are available as `saxbase-binaries` artifacts
in the [Binaries workflow](https://github.com/dute533/SaxBase/actions/workflows/binaries.yml).

## Quick start

```sh
go build -o saxbase .          # Source builds require Go 1.26+
cp saxbase.yaml.example saxbase.yaml
cp .env.example .env
```

Set your connection strings in `.env` (ignored by Git). The config maps target
names to environment variables and defaults to `local`. Exported variables take
precedence over `.env`; in CI, supply them through your secrets store.

```sh
./saxbase plan                 # Uses default_target from saxbase.yaml
./saxbase -target acc plan     # Select another database
./saxbase -target prod deploy
```

Set `require_confirmation: true` on targets that need confirmation (enabled for
`prod` in the example). Writes prompt for `yes`; CI can use
`./saxbase -target prod -yes deploy`. Read-only commands never prompt.

Commit `saxbase.yaml` alongside your SQL; keep passwords in `.env` or CI secrets.
Without a config, `GOOSE_DBSTRING` still works. See [configuration details](docs/reference.md#configuration).

Organize your SQL like this (see the [runnable example](examples/sqlserver/README.md)):

```text
database/
  migrations/       # Goose SQL files with Up and Down sections
  objects/          # One CREATE OR ALTER view, procedure, or function per SQL file
  release.json      # Generated release manifest
```

Create and deploy a release, using your target Goose migration version:

```sh
git add database/objects
git commit -m "Update database objects"
./saxbase release create 30
# Review release.json: put object dependencies before their consumers.
./saxbase plan                 # Preview changes without writing to the database
./saxbase deploy               # Migrate to version 30, then apply changed objects
```

The manifest records repository-relative object paths and full Git commit hashes.
Its array order controls deployment. Commit SQL first, then generate and commit the
manifest. Deployment reads committed SQL, so working-tree edits do not affect it. Object files must each contain a single SQL batch without `GO`.

Git must be installed and referenced commits available locally. No SQL snapshots
are stored in the database.

## Update a release

After committing edited SQL, sync the manifest and deploy a new version:

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
| `--version` | Show the SaxBase CLI version |
| `release validate` | Resolve the manifest’s committed SQL files |
| `release current` / `release history` | Inspect deployed releases |
| `release show VERSION` | Print release metadata and fingerprint |
| `release rollback VERSION` | Restore from target and source manifests |
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
  Supply `-manifest` for the target and `-source-manifest` for the active release.
  Keep both manifests, their Git commits, and migration files available. After a
  failed rollback, fix the cause and retry the same command to resume.
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

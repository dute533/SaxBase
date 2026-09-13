# SaxBase reference

SaxBase deploys SQL Server migrations and versioned database objects from a
release manifest. Goose manages structural migrations; SaxBase applies views,
procedures, and functions in the order listed by the manifest.

## Project layout

```text
database/
  migrations/        # Goose SQL migrations
  objects/            # Current object definitions
  release.json       # Release manifest
```

The paths above are the defaults. Use `-dir`, `-objects-dir`, and `-manifest` to
select different paths.

## Configuration

Copy the templates before connecting to a database:

```sh
cp saxbase.yaml.example saxbase.yaml
cp .env.example .env
```

Put connection strings in `.env` and keep that file out of Git. A target config
maps names to environment variables:

```yaml
default_target: local
targets:
  local:
    connection_env: SAXBASE_LOCAL_DSN
  prod:
    connection_env: SAXBASE_PROD_DSN
    require_confirmation: true
```

Select a target with `-target`; otherwise SaxBase uses `default_target`. A target
with `require_confirmation: true` asks for `yes` before a database write. Use
`-yes` for unattended writes. Read-only commands and manifest commands do not
need a target.

SaxBase loads `.env` from the current working directory. Existing environment
variables take precedence. Without a target config, use the `GOOSE_DBSTRING`
environment variable.

Only SQL Server is supported. The default driver is `mssql`.

## Everyday commands

```sh
./saxbase plan       # Preview migrations and objects
./saxbase apply      # Apply the manifest
./saxbase status     # Show migration, release, and object state
```

`plan` and `apply` select the next entry after the database's current release.
For an unversioned database they select the oldest entry, and when the newest
entry is already current they safely recheck it. `apply` brings Goose to that
release's schema version, then applies its object entries from top to bottom.
Run it again while more releases are pending.

## Structural migrations

Create Goose migrations in `database/migrations`, for example:

```sql
-- +goose Up
CREATE TABLE dbo.customers (id INT NOT NULL PRIMARY KEY);

-- +goose Down
DROP TABLE dbo.customers;
```

`apply` advances Goose to the schema version declared by the release manifest.
`rollback` runs the required Goose down migrations for the target release. Use
`status` to inspect migration state and the current Goose version.

Use Goose annotations and transaction rules. Do not include SQL Server `GO` batch
separators in migration files.

## Database objects

Store one complete definition per SQL file below `database/objects`, usually with
`CREATE OR ALTER`:

```sql
CREATE OR ALTER VIEW dbo.customer_names AS
SELECT id, name FROM dbo.customers;
```

Object files must contain one SQL batch. Do not include Goose annotations, `GO`,
`USE`, or transaction-control statements. SaxBase executes definitions as written
and does not infer dependencies, so list dependencies before consumers in the
manifest.

`status` compares the selected object state with the active release manifest and
reports each object as `new`, `changed`, `unchanged`, or `missing`. Missing files
are reported and are not automatically dropped. `apply` applies changed objects
together with the manifest's migrations.

## Release manifests

A manifest records a release version and its ordered object entries:

```json
{
  "version": "30.1",
  "parent": "release-30.json",
  "objects": [
    {
      "path": "database/objects/views/customer.sql",
      "commit": "0123456789abcdef0123456789abcdef01234567"
    }
  ]
}
```

The default history file can contain multiple releases. Later entries contain
only their changes and use the earlier version as `parent`:

```json
{
  "releases": [
    {
      "version": "1",
      "objects": [
        {"path": "database/objects/views/customer.sql", "commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
      ]
    },
    {
      "version": "2",
      "parent": "1",
      "objects": [
        {"path": "database/objects/views/customer.sql", "commit": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
        {"path": "database/objects/views/order.sql", "commit": "cccccccccccccccccccccccccccccccccccccccc"}
      ]
    }
  ]
}
```

Each entry has a repository-relative `path` and either a full Git commit hash or
`"latest"`. A commit hash makes the release reproducible by resolving the file
from that commit. `latest` reads the working tree and is useful for standalone
test manifests, but it cannot be used as the parent of an appended delta because
its previous contents are not recoverable.

Entries execute from top to bottom. Edit the array when dependencies require a
different order. A `delete` entry removes an object from the database and must
use a historical commit hash.

Versions use `SCHEMA` or `SCHEMA.REVISION`, such as `30`, `30.1`, and `31`. The
schema component identifies the Goose version; the revision identifies another
object state at that schema version. Use a new revision when object SQL, paths,
or order changes. Release versions are immutable once recorded.

Create and check manifests with:

```sh
git add database/objects
git commit -m "Update database objects"
./saxbase release create 30
./saxbase release validate
./saxbase plan
./saxbase apply
```

By default, `database/release.json` is a release history. Creating a newer
version appends a delta from the latest version while preserving all older
versions. `plan` and `apply` advance one release at a time; rollback selects an
older version from the same file. Rerunning an existing version or creating an
older version is rejected.

To create a delta containing only changes from a previous release:

```sh
./saxbase -parent-manifest database/release-30.json \
  -manifest database/release-30.1.json release create 30.1
```

Keep manifests and their referenced Git commits available for rollback.

## Rollback

When target and current releases are in the same history file, only the target
version is needed:

```sh
./saxbase rollback 30
```

When releases are stored in separate legacy files, provide both paths:

```sh
./saxbase -manifest database/release-30.json \
  -source-manifest database/release-30.1.json rollback 30
```

The target release must already be recorded and the source version must match the
active release. SaxBase resolves historical object files from the commits in the
manifests, so the manifests and commits must still be available. Fix any failed
deployment before requesting a rollback; rollback is for returning from a
recorded release to an earlier one.

SaxBase resolves object state from release manifests and stores only release
history and rollback progress in its `dbo` metadata tables. A successful apply
or rollback removes the obsolete `dbo.saxbase_objects` table if an older SaxBase
version created it. Goose stores its migration version in `goose_db_version`.

## Development

```sh
go test ./...
go vet ./...
bash scripts/test-integration.sh
```

The integration script runs against a disposable SQL Server container. See the
[SQL Server example](../examples/sqlserver/README.md) for a complete working
layout and workflow.

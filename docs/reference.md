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

Use a native SQL Server URL or ADO connection string and always name the
database explicitly:

```text
sqlserver://USER:PASSWORD@localhost:1433?database=ObjectStore
server=localhost;user id=USER;password=PASSWORD;database=ObjectStore
```

JDBC strings such as `jdbc:sqlserver://...;databaseName=...` are not supported.
SaxBase rejects missing database names and SQL Server system databases rather
than allowing the driver to fall back to the login's default database.

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

A manifest is an ordered release history with the newest release first. The
oldest entry at the bottom describes the full object state. Each entry above it
contains only changes from the entry immediately below it:

```json
{
  "releases": [
    {
      "version": "2",
      "objects": [
        {"path": "database/objects/views/customer.sql", "commit": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
        {"path": "database/objects/views/order.sql", "commit": "cccccccccccccccccccccccccccccccccccccccc"}
      ]
    },
    {
      "version": "1",
      "objects": [
        {"path": "database/objects/views/customer.sql", "commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
      ]
    }
  ]
}
```

Each entry has a repository-relative `path` and either a full Git commit hash or
`"latest"`. A commit hash makes the release reproducible by resolving the file
from that commit. `latest` reads the working tree, but a history containing it
cannot be extended because its previous contents are not recoverable.

Objects within a release execute from top to bottom. Edit that release's
`objects` array when dependencies require a different order. A `delete` entry
removes an object from the database and must use a historical commit hash.

Versions use `SCHEMA` or `SCHEMA.REVISION`, such as `30`, `30.1`, and `31`. The
schema component identifies the Goose version; the revision identifies another
object state at that schema version. Use a new revision when object SQL, paths,
or order changes after deployment. Release versions are immutable once recorded
in a database.

Create and check manifests with:

```sh
git add database/objects
git commit -m "Update database objects"
./saxbase release create 30
./saxbase release validate
./saxbase plan
./saxbase apply
```

By default, `database/release.json` is a newest-first release history. Creating
a newer version prepends a delta from the latest version while preserving all
older versions. `plan` and `apply` advance chronologically one release at a
time; rollback selects an older version from the same file. Creating a version
older than the newest manifest entry is rejected.

Before the newest release is deployed, rerun `release create` with that same
version to rebuild its entry from the current committed object files. Older
entries remain unchanged. If that version was already recorded in a database,
its stored fingerprint still prevents a changed definition from being applied.

To prepend a delta containing only changes from the latest release:

```sh
./saxbase release create 30.1
```

Keep manifests and their referenced Git commits available for rollback.

## Rollback

Only the target version is needed:

```sh
./saxbase rollback 30
```

The target and active releases must both be present in the history. SaxBase
resolves historical object files from the commits in the manifest, so the
manifest and commits must still be available. Fix any failed
deployment before requesting a rollback; rollback is for returning from a
recorded release to an earlier one.

SaxBase resolves object state from release manifests and stores only release
history and rollback progress in its `dbo` metadata tables. Goose stores its
migration version in `goose_db_version`.

## Development

```sh
go test ./...
go vet ./...
bash scripts/test-integration.sh
```

The integration script runs against a disposable SQL Server container. See the
[SQL Server example](../examples/sqlserver/README.md) for a complete working
layout and workflow.

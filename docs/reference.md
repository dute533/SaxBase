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

`plan` selects the next entry after the database's current release and prints
the complete pending release chain. `apply` visits that entire chain in order,
bringing Goose to each release's schema version, applying its object entries
from top to bottom, and recording the release before advancing. When the newest
entry is already current, both commands safely recheck it.

## Structural migrations

Create the next sequential SQL migration with SaxBase:

```sh
./saxbase migration create add_customer_email
```

This command works without a database connection and creates a file such as
`database/migrations/00003_add_customer_email.sql`. Use `-dir` before or after
the command to select another migration directory. Names use lowercase letters,
numbers, and single underscores.

Edit the generated Goose migration, for example:

```sql
-- +goose Up
CREATE TABLE dbo.customers (id INT NOT NULL PRIMARY KEY);

-- +goose Down
DROP TABLE dbo.customers;
```

`apply` advances the database schema to the version declared by the release
manifest. `rollback` runs the required Goose down migrations for the target
release. Use `status` to inspect migration state and the current database schema.
Repeating `apply` on the current release prints a concise no-change summary
instead of listing every current object.

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
reports each object as `new`, `changed`, `current`, or `missing`. A planned
deletion is shown as `delete`. In `plan`, migrations above the target schema are
listed under `NOT INCLUDED` with the release and target schema that excluded
them. Applied migrations and current objects are omitted so the plan contains
only actions, exclusions, and blockers. Missing files
are reported and are not automatically dropped. `apply` applies changed objects
together with the manifest's migrations.

Successful workflow commands end with one contextual `Next:` recommendation.
When the next migration is above the latest release schema, SaxBase recommends
the exact `release create` command needed to include it.
Global options may be placed before or after the command.
Every command and nested command accepts `-h` or `--help` without loading
configuration or connecting to the database.

Release object files must be committed so manifests can store reproducible Git
references for rollback. `status` and `release create` list any affected SQL
files and recommend the exact `git add` and `git commit` command; SaxBase never
commits repository changes automatically.

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
whole-number portion of a release selects its schema migration, so releases
`30.1` and `30.2` reuse database schema `30`; the revision identifies another
object state at that schema version. Use a new revision when object SQL, paths,
or order changes after deployment. Release versions are immutable once recorded
in a database.

The normal release workflow is:

```sh
git add database/objects
git commit -m "Update database objects"
./saxbase release create
./saxbase plan
./saxbase apply
```

`plan` and `apply` perform the required manifest validation themselves. Use
`release validate` only when you want to verify manifest references without a
database connection.

Without an explicit version, `release create` selects the first migration above
the latest release schema. When no migration is pending, it increments the
latest release revision. An explicit version overrides the inferred value.

By default, `database/release.json` is a newest-first release history. Creating
a newer version prepends a delta from the latest version while preserving all
older versions. `plan` previews the pending chain and `apply` deploys it
chronologically, one recorded release step at a time; rollback selects an older
version from the same file. Creating a version older than the newest manifest
entry is rejected.

Before the newest release is deployed, rerun `release create` with that same
version to rebuild its entry from the current committed object files. Older
entries remain unchanged. If that version was already recorded in a database,
its stored fingerprint still prevents a changed definition from being applied.

To deliberately rewrite that recorded release in a disposable development
database, use force after refreshing it:

```sh
./saxbase release create 30.1
./saxbase -f apply
```

`-f` (or `-force`) re-executes every object entry in the selected release and
atomically replaces its recorded fingerprint. Other deployment blockers remain
active. Force does not infer definitions or deletion operations that disappeared
when the release entry was refreshed, so the user is responsible for ensuring
the resulting database matches a fresh deployment. Never force a shared or
production database.

To prepend a delta containing only changes from the latest release:

```sh
./saxbase release create 30.1
```

Keep manifests and their referenced Git commits available for rollback.

## Rollback

Only the target version is needed:

```sh
./saxbase rollback
```

With no version, rollback restores the immediately preceding recorded release.
Pass a version to traverse back to that older release.

The target and active releases must both be present in the history. SaxBase
resolves historical object files from the commits in the manifest, so the
manifest and commits must still be available. Rollback visits every intervening
release in descending order and records each one as the current release before
continuing. Object-only steps are atomic. For a structural step, SaxBase removes
blocking objects and moves the current-release marker to the target atomically,
then runs Goose Down and restores the target definitions. Retry the same command
after repairing a failed migration or definition; SaxBase reconciles that
checkpoint before continuing.

SaxBase resolves object state from release manifests. Its
`dbo.saxbase_releases` table stores only the canonical release `version`, its
`fingerprint`, and the `is_current` marker; schema and revision numbers are
derived from the version when needed. Goose stores the migration history used by
SaxBase in `dbo.saxbase_schema_version`; rollback does not create another
metadata table. Successful rollback or reconciliation also removes the obsolete
`dbo.saxbase_rollbacks` table left by older SaxBase versions.

## Development

```sh
go test ./...
go vet ./...
bash scripts/test-integration.sh
```

The integration script runs against a disposable SQL Server container. See the
[SQL Server example](../examples/sqlserver/README.md) for a complete working
layout and workflow.

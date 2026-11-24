# SaxBase reference

SaxBase deploys SQL Server and PostgreSQL migrations with versioned views,
procedures, and functions as one release. Goose manages structural migrations;
SaxBase applies complete object definitions in manifest order.

## Project layout

```text
database/
  migrations/        # Goose SQL migrations
  objects/           # Current object definitions
  release.json       # Release history
```

These are the default paths. Override them with `-dir`, `-objects-dir`, and
`-manifest`.

## Configuration

Copy the templates and keep connection strings in the untracked `.env` file:

```sh
cp saxbase.yaml.example saxbase.yaml
cp .env.example .env
```

The target configuration maps deployment names to environment variables:

```yaml
default_target: local
targets:
  local:
    dialect: sqlserver
    connection_env: SAXBASE_LOCAL_DSN
  prod:
    dialect: postgres
    connection_env: SAXBASE_PROD_DSN
    require_confirmation: true
```

Use `-target NAME` to override `default_target`. Protected targets prompt before
`apply` and `rollback`; pass `-yes` for unattended deployment. Existing
environment variables override values loaded from `.env`.

Without a target configuration, set `SAXBASE_DIALECT` and `SAXBASE_DSN`. Every
connection must select an explicit, non-maintenance database:

```text
sqlserver://USER:PASSWORD@localhost:1433?database=ObjectStore
server=localhost;user id=USER;password=PASSWORD;database=ObjectStore
postgres://USER:PASSWORD@localhost:5432/object_store
```

Supported dialect values are `sqlserver` and `postgres`. JDBC SQL Server
connection strings are not supported. The dialect belongs to deployment
configuration, not the release manifest; targets sharing a manifest must use
the database syntax that manifest's migrations and objects were written for.

## Release workflow

Create and edit a migration when the structure or data changes:

```sh
./saxbase migration create "Add customer email"
```

Add or update complete definitions below `database/objects`, then commit the
database files before creating a release:

```sh
git add database/migrations database/objects
git commit -m "Add customer email"

./saxbase release create 1.4.0
git add database/release.json
git commit -m "Create database release 1.4.0"

./saxbase status
./saxbase apply
./saxbase status
```

`status` shows current release and schema, the pending release chain, and the
next release’s migration and object changes and blockers. It is read-only and
optional before `apply`. When everything is current, it shows a brief summary.

`apply` visits every pending release
in order, advances Goose to that release's migration target, applies its object
entries, and records it before continuing. Pass a version to stop after that
release instead of applying the whole pending chain:

```sh
./saxbase apply 1.4.0   # apply pending releases up to and including 1.4.0
./saxbase apply         # continue with the remaining pending releases
```

The version must exist in the manifest and must not be older than the current
release; use `rollback VERSION` to move to an older release.

## Migrations

`migration create` creates the next sequential file, such as
`database/migrations/00003_add_customer_email.sql`. Names are normalized to
lowercase snake case.

SQL Server migration:

```sql
-- migration Up
ALTER TABLE dbo.customers ADD email NVARCHAR(320) NULL;

-- migration Down
ALTER TABLE dbo.customers DROP COLUMN email;
```

Write migrations in the selected database's native SQL, using only the
`-- migration Up` / `-- migration Down` markers (and `-- migration StatementBegin` /
`-- migration StatementEnd` around multi-statement batches such as triggers).
SQL Server files must not contain `GO` batch separators.
Migrating Goose files is a mechanical rewrite:

```sh
sed -i 's/-- +goose Up/-- migration Up/; s/-- +goose Down/-- migration Down/; s/-- +goose StatementBegin/-- migration StatementBegin/; s/-- +goose StatementEnd/-- migration StatementEnd/' migrations/*.sql
```

A release's `migration_version` determines how far `apply` or `rollback` moves
the schema.

`release create VERSION` selects the highest available migration for a new release,
including all migrations up to that target.
If no migration is pending, it reuses the latest migration target for an
object-only release. Refreshing the same release version preserves its migration
target.

## Database objects

Store one complete view, procedure, or function definition per SQL file below
`database/objects`. Use `CREATE OR ALTER` for SQL Server:

```sql
CREATE OR ALTER VIEW dbo.customer_names AS
SELECT id, name FROM dbo.customers;
```

Use `CREATE OR REPLACE` for PostgreSQL:

```sql
CREATE OR REPLACE VIEW public.customer_names AS
SELECT id, name FROM public.customers;
```

An object file must contain one SQL batch. Do not include migration annotations,
`GO`, `USE`, or transaction-control statements.

SaxBase does not infer dependencies. Objects execute from top to bottom in the
manifest, so list dependencies before consumers. If `a_dependent.sql` uses the
view from `z_base.sql`, list `z_base.sql` first; filenames do not determine
deployment order.

Object files must be committed before `release create`. The manifest pins their
exact Git commits so deployments and rollbacks do not depend on the current
working tree.

Object identity is the schema-qualified name. Keep one view, procedure, or
function per identity; overloaded PostgreSQL routines are rejected.

## Release manifests

`database/release.json` is a newest-first release history. The oldest entry is a
complete object state; each newer entry contains only changes from the release
before it:

```json
{
  "releases": [
    {
      "version": "1.1.0",
      "migration_version": 2,
      "objects": [
        {
          "path": "database/objects/views/customer.sql",
          "commit": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
        }
      ]
    },
    {
      "version": "1.0.0",
      "migration_version": 1,
      "objects": [
        {
          "path": "database/objects/views/customer.sql",
          "commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        }
      ]
    }
  ]
}
```

Each release contains:

- `version`: a unique SemVer release identifier.
- `migration_version`: the Goose migration represented by the release.
- `objects`: ordered object changes pinned by repository-relative path and full
  Git commit hash.

An object-only patch can reuse a migration target. Migration targets may stay
the same or increase, but never decrease. A removed object is represented by a
`delete` entry that retains its last commit so SaxBase can identify and drop it.

Before the newest release has been deployed, rerun `release create` with the
same version to refresh it from committed files. Once a release is recorded in
a database, its migration target, object order, paths, and definitions are
immutable. Create a new version for later changes.

Use `release validate` to verify manifest references without connecting to a
database. Keep every manifest and referenced Git commit available for rollback.

### Development force apply

For a disposable development database, refresh the newest release and reapply
it deliberately:

```sh
./saxbase release create 1.4.1
./saxbase -f apply
```

Force apply rewrites the recorded fingerprint and reexecutes the release's
object entries. It does not infer missing definitions or deletions. Never use it
on a shared or production database.

## Rollback

Restore the preceding release, or name an older version:

```sh
./saxbase rollback
./saxbase rollback 1.2.0
```

The active and target releases must both exist in the manifest history, and
their referenced commits must remain available. SaxBase walks through
intervening releases one at a time, runs the required Goose Down migrations,
and restores the target object definitions.

Each completed release is a durable checkpoint. If a migration or definition
fails during rollback, repair it and repeat the same command; SaxBase resumes
from the recorded checkpoint.

See the [runnable example](../examples/README.md) and run `./saxbase -h` for the
complete command and option list.

### Rolling back the first release

Running `saxbase rollback` from the first release removes its managed objects
and runs migration Down steps to schema 0. Release history is retained; `apply`
can deploy the release chain again. If a Down step fails, repair the migration
and rerun `saxbase rollback` to finish returning to the initial state.

## Drift

`saxbase drift` is a read-only check that compares the live database with what
SaxBase last deployed:

```sh
./saxbase drift
```

Two things are compared. The schema fingerprint (tables, columns, indexes,
constraints, and module definitions, stored in `dbo.saxbase_meta` at every
`apply`/`rollback`) must match the live catalog, and every object file pinned
by the current release must match the live module definition. The command prints
`No drift detected.` when everything matches; when drift is found it says exactly
what changed — schema lines as `+ added` / `- removed` / `~ module definition
changed`, and each drifted or missing object file — and exits non-zero, so it can
gate a CI pipeline. Fix drift by reapplying the pinned release
(`saxbase -f apply` on a disposable database); note that a drifted view can
block reapplying a procedure that depends on it, so restore the drifted object
from its repo file first.

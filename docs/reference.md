# SaxBase reference

SaxBase deploys SQL Server migrations and versioned views, procedures, and
functions as one release. Goose manages structural migrations; SaxBase applies
complete object definitions in manifest order.

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
    connection_env: SAXBASE_LOCAL_DSN
  prod:
    connection_env: SAXBASE_PROD_DSN
    require_confirmation: true
```

Use `-target NAME` to override `default_target`. Protected targets prompt before
`apply` and `rollback`; pass `-yes` for unattended deployment. Existing
environment variables override values loaded from `.env`.

Without a target configuration, set `GOOSE_DBSTRING`. Use a native SQL Server
URL or ADO connection string with an explicit, non-system database:

```text
sqlserver://USER:PASSWORD@localhost:1433?database=ObjectStore
server=localhost;user id=USER;password=PASSWORD;database=ObjectStore
```

JDBC connection strings are not supported. SQL Server is currently the only
supported database.

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

./saxbase plan
./saxbase apply
./saxbase status
```

`plan` previews the pending release chain. `apply` visits every pending release
in order, advances Goose to that release's migration target, applies its object
entries, and records it before continuing.

## Migrations

`migration create` creates the next sequential file, such as
`database/migrations/00003_add_customer_email.sql`. Names are normalized to
lowercase snake case.

```sql
-- +goose Up
ALTER TABLE dbo.customers ADD email NVARCHAR(320) NULL;

-- +goose Down
ALTER TABLE dbo.customers DROP COLUMN email;
```

Use Goose annotations and transaction rules. Do not include SQL Server `GO`
batch separators. A release's `migration_version` determines how far `apply` or
`rollback` moves the schema.

`release create VERSION` selects the first migration above the latest release.
If no migration is pending, it reuses the latest migration target for an
object-only release. Create another release to include another pending
migration.

## Database objects

Store one complete view, procedure, or function definition per SQL file below
`database/objects`, normally using `CREATE OR ALTER`:

```sql
CREATE OR ALTER VIEW dbo.customer_names AS
SELECT id, name FROM dbo.customers;
```

An object file must contain one SQL batch. Do not include Goose annotations,
`GO`, `USE`, or transaction-control statements.

SaxBase does not infer dependencies. Objects execute from top to bottom in the
manifest, so list dependencies before consumers. If `a_dependent.sql` uses the
view from `z_base.sql`, list `z_base.sql` first; filenames do not determine
deployment order.

Object files must be committed before `release create`. The manifest pins their
exact Git commits so deployments and rollbacks do not depend on the current
working tree.

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

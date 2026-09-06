# SaxBase reference

SaxBase is a Go database deployment CLI built around [Goose](https://github.com/pressly/goose).
Goose remains an external Go module and owns structural migrations. SaxBase owns
deployment semantics through an internal migration engine interface.

## Structural migrations

Requires Go 1.26 or newer and SQL Server. Build with `go build -o saxbase .`,
or substitute `go run .` for `./saxbase` below. Running without arguments prints help.

```sh
export GOOSE_DRIVER=mssql
export GOOSE_DBSTRING='sqlserver://USER:PASSWORD@localhost:1433?database=example'
./saxbase up
./saxbase status
./saxbase version
./saxbase down
```

`up` applies all pending migrations; `down` rolls back exactly one migration.
`status` lists migration versions, applied/pending states, and filenames.
`version` prints the current Goose database version as an integer, not the CLI version.
Goose manages its usual `goose_db_version` table, including initialization when needed.

Goose-style positional connection arguments are also supported:

```sh
./saxbase -dir database/migrations mssql "$GOOSE_DBSTRING" status
```

Only `mssql` and `sqlserver` drivers are supported currently. Options precede the
command or connection arguments. `GOOSE_MIGRATION_DIR` overrides the default
`database/migrations`; `-dir` overrides that environment variable. Positional
driver and connection arguments override their environment variables.
Errors exit nonzero; Ctrl-C cancels the active database operation.
Command semantics follow Goose; this is not a replacement for every Goose CLI flag.

Create your migrations directory and add Goose SQL migrations, for example
`database/migrations/00001_create_example.sql`:

```sql
-- +goose Up
CREATE TABLE dbo.example (id INT NOT NULL PRIMARY KEY);

-- +goose Down
DROP TABLE dbo.example;
```

Use Goose's SQL annotations and transaction rules. SQL Server `GO` batch separators
are client directives and should not be included in migration SQL sent through this CLI.

## Full-state objects

Put complete current definitions in `database/objects/**/*.sql`, for example
`database/objects/views/customers.sql`:

```sql
CREATE OR ALTER VIEW dbo.customer_names AS
SELECT id, name FROM dbo.customers;
```

Or `database/objects/procedures/get_customer.sql`:

```sql
CREATE OR ALTER PROCEDURE dbo.get_customer @id INT AS
BEGIN
    SET NOCOUNT ON;
    SELECT id, name FROM dbo.customers WHERE id = @id;
END;
```

Functions work the same way. Run structural migrations first, then deploy objects:

```sh
./saxbase up
./saxbase objects status
./saxbase objects apply
```

These commands use the same connection settings as migrations. Set
`SAXBASE_OBJECTS_DIR` or pass `-objects-dir PATH` before the command to override
`database/objects`. Positional connections also work:
`./saxbase mssql "$GOOSE_DBSTRING" objects apply`. Object commands do not require
a migrations directory and do not change the Goose version.

SaxBase hashes exact file bytes with SHA-256, including whitespace and line endings.
The relative, case-sensitive file path identifies the tracked definition.
`objects status` reports `new`, `changed`, `unchanged`, or `missing` alongside
the path and checksum, without writing to the database. For missing files, it
shows the last deployed checksum. `objects apply` runs new and changed files and
reports `applied` only after the transaction commits. Unchanged files are skipped.

Definitions and checksums in `dbo.saxbase_objects` commit together in one SQL Server
transaction. A failure rolls back the whole object apply. A database application
lock serializes SaxBase object applies, Goose Up/Down commands, and release
rollback, with a 30-second lock wait. External database tools must still be
coordinated with SaxBase deployments.

Each file must contain one complete object definition in a single SQL batch,
typically `CREATE OR ALTER`. Do not include Goose annotations, `GO` separators,
`USE`, or transaction-control statements. SaxBase executes the SQL as supplied;
it does not parse object names or resolve dependencies. With a release manifest,
files run in the order of entries in its `objects` array; put dependencies first.
Without a manifest, `objects apply` uses lexical relative-path order.
An object should have one stable file path. Renaming a file is treated as a new
definition plus a missing old path.

Missing files are reported but never automatically dropped or removed from
tracking. Checksums compare files with the last deployment, not live database
definitions, so manual database edits are not detected. Automatic removal during
normal apply and dependency resolution are not implemented. Explicit release
rollback can restore saved definitions and remove objects introduced later.

## Release manifests

A JSON manifest names a release and records the exact set of object paths and
SHA-256 checksums. The version is a string: `30` requires Goose version 30;
`30.1` and `30.2` represent object revisions at that same structural version.
Components are parsed as nonnegative 64-bit integers, never floating-point
numbers. Use `30` for the initial revision, not `30.0`.

Create a manifest from current object files without connecting to a database:

```sh
./saxbase release create 30
./saxbase release validate
```

Both commands default to `database/release.json`. `release create` writes
`format: 1`, a string `version`, and an `objects` array containing `path` and
`sha256` for every scanned SQL file. It refuses to overwrite an existing file.
Creation initially lists objects in lexical path order. Reorder the array before
deploying to place dependencies before their consumers; there is no separate
priority field. `plan`, `deploy`, and manifest-backed `objects apply` and
`objects status` follow this order. Each path must still appear exactly once
with its matching checksum. Unchanged objects are skipped without changing the
relative execution order of changed objects.

Order is part of a release's immutable identity. To reorder an already recorded
release, create a new revision even if its SQL and checksums are unchanged.
Snapshots store the full array order, including unchanged objects; `release show`
returns that order and rollback restores it. Older snapshots retain the lexical
path order used when they were deployed. Read-only commands support the old
metadata layout; a versioned deployment adds the order column when needed.
See the [dependency ordering example](../examples/sqlserver/ordering/README.md).

See the [complete example manifest](../examples/sqlserver/database/release.json).
The schema component is supplied by you; creation does not inspect the database
or certify that the corresponding migration exists.

After editing objects, refresh an existing manifest while preserving its object order:

```sh
./saxbase release sync 30.1
./saxbase release validate
```

`release sync [VERSION]` defaults to `database/release.json`; use `-manifest`
to select another existing manifest. It updates changed checksums and appends
new files in lexical path order. Review the order of new objects before deployment
so dependencies run first. Missing local objects cause an error; sync never removes
manifest entries. Validation completes before the manifest is replaced atomically,
and an unchanged manifest is left untouched.

Omitting `VERSION` keeps the current version. Sync only edits the local manifest
and does not connect to the database. If the release has already been deployed,
supply a new version when changing its contents because recorded releases are
immutable. Sync does not check migration availability or database release history.

Alternatively, create a manifest at a new path for the next revision:

```sh
./saxbase -manifest database/release-30.1.json release create 30.1
./saxbase -manifest database/release-30.1.json release validate
./saxbase -manifest database/release-30.1.json objects apply
```

Validation rejects missing, extra, or changed local object files, duplicate paths,
invalid checksums, and unsupported manifest formats. Paths are relative to the
object directory, not the manifest file. `-objects-dir` still selects the object
directory. An empty objects array represents an empty local object set.

When `-manifest` is supplied to `objects apply`, SaxBase validates the files and
requires the current Goose database version to equal the version's integer
component before applying objects. It does not run structural migrations for
you. The initial check uses Goose and requires the migrations directory; the
version is checked again using Goose's store API inside the object transaction.
Supplying
`-manifest` to `objects status` validates the local files before displaying object
status; it does not check the database's Goose version.

The manifest is opt-in for object commands; without `-manifest`, existing object
deployment behavior is retained and no release is recorded. Keep manifests and
their matching SQL in Git. The manifest itself contains checksums rather than SQL;
successful manifest deployments also store full SQL snapshots in the database.

### Database release history

Deploying with `-manifest` automatically maintains these SaxBase-owned tables:

| Table | Contents |
| --- | --- |
| `dbo.saxbase_releases` | Release version, numeric Goose version and revision, first deployment time in UTC, and object count. |
| `dbo.saxbase_release_objects` | Every object path, SHA-256 checksum, and exact UTF-8 SQL bytes for each release, including unchanged objects. |
| `dbo.saxbase_release_state` | The last recorded active release, updated by versioned apply and successful rollback. |

The object definitions, deployed checksums, release record, and complete snapshot
commit in one transaction. SQL or metadata errors roll back the transaction,
leaving no successful release record. Existing databases with only
`dbo.saxbase_objects` need no manual metadata migration.

Inspect releases using the configured `GOOSE_DBSTRING`:

```sh
./saxbase release history
./saxbase release show 30.1
./saxbase release current
```

`history` lists versions in descending numeric schema/revision order (`30.10`
comes after `30.2` in version order). `show` prints JSON containing the release
metadata and each object's path, checksum, and SQL definition. These commands
are read-only, need no local SQL files, and never initialize database tables.
Before the first deployment, history is empty and showing a release returns an error.

Release versions are immutable: reusing a version with different SQL or a
different object set fails. Reapplying the latest version with the same snapshot
is allowed and creates no duplicate history or snapshot rows; its timestamp
remains the first successful deployment time. Concurrent applies use the same
database application lock. To restore an older version, use explicit
`release rollback VERSION` rather than `objects apply`.

A release manifest must include every previously tracked object as well as the
complete local file set. Omitted tracked objects block release deployment;
unversioned `objects apply` still reports missing files without dropping them.
Objects that SaxBase has never tracked are outside this check.

The Goose version is rechecked within a serializable release transaction, while
Goose still owns all structural migration tracking. Coordinate structural
migrations with object deployment, especially nontransactional SQL or external
tools. Release history is a record of successful versioned deployments, not a
live drift detector: unversioned applies, Goose `down`, or manual SQL can change
the current database without changing historical release snapshots. It is not
an audit log of every retry. SaxBase marks the active release as unversioned when
an unversioned object apply changes SQL or a standalone Goose command changes
the structural version. Manual SQL and external Goose commands cannot update
this marker automatically.

## Release rollback

```sh
./saxbase release current
./saxbase release rollback 30.1
./saxbase release rollbacks
```

Rollback reads the target SQL snapshot from the database. It does not read local
object files or require a manifest. The migrations directory is still required;
for a structural downgrade it must contain every applied migration above the
target version with its Goose Down SQL. `-dir` selects an alternate directory.
The target must be a previously recorded release no newer than the active release.

Before modifying objects, SaxBase validates snapshot checksums and completeness,
the active release's tracked object set and Goose version, and the required
migration files. A changed, unversioned object state must be deployed with a
manifest before rollback. Objects must use schema-qualified
`CREATE [OR ALTER] VIEW`, `PROCEDURE`/`PROC`, or `FUNCTION` definitions. Quoted
identifiers and leading SQL comments are supported; unsupported headers are
rejected before object changes.
For an older snapshot using plain `CREATE`, rollback executes it with
`CREATE OR ALTER`; the stored SQL bytes and checksum remain unchanged.

For an object-only rollback, SaxBase restores all target definitions, drops
tracked objects absent from the target, replaces the deployed checksum set,
updates the active release, and records completion in one transaction. Existing
target objects are altered rather than dropped, preserving their permissions.
Untracked database objects are never dropped. Historical releases and snapshots
remain unchanged.

For rollback across structural versions, Goose owns its migration transactions,
so the entire operation is **not atomic**. The phases are:

1. Drop tracked objects absent from the target and commit that phase.
2. Run Goose Down migrations to the target structural version.
3. Restore the target SQL definitions, checksums, and active release in one transaction.

Goose Down SQL can remove data. Retained modules are not automatically dropped
before structural rollback. Schema-bound modules or other dependencies may need
explicit preparation; SaxBase does not resolve dependency graphs. Target SQL is
restored in the target snapshot’s stored deployment order, and removed objects
are dropped in reverse source snapshot order. All DDL is executed as supplied in the saved snapshots and
migration files.

`dbo.saxbase_rollbacks` stores each operation's source, target, phase, status,
timestamps, and latest error. `release rollbacks` prints those records as JSON.
Phases are `started`, `objects_removed`, `schema_rolled_back`, and `restored`.
After failure, repair the migration or database issue and rerun the **same**
`release rollback VERSION` command. It resumes the existing operation using the
actual Goose version; it does not replay already completed Down migrations.

An incomplete operation blocks other SaxBase writes and `release current` until
the rollback completes. `version`, `status`, release history, and rollback history
remain available for inspection. A session-scoped database lock prevents another
SaxBase deployment from interleaving between phases. The test suite exercises
both partial structural failure and atomic object-restore failure with retries.

## Release plan

```sh
./saxbase -manifest database/release-30.1.json plan
```

`plan` defaults to `database/release.json` and uses the usual connection settings,
`-dir`, and `-objects-dir`. Goose-style positional connections also work:
`./saxbase -manifest database/release.json mssql "$GOOSE_DBSTRING" plan`.

The output shows the last recorded active release and actual Goose version,
the target release and schema version, every migration's action, and each
object's state and checksum in manifest order. Pending migrations through the target are marked
`apply`; later files are `deferred`. Already applied migrations remain visible.
Objects are `new`, `changed`, `unchanged`, or `missing` based on stored checksums.

Planning never executes SQL files or initializes metadata tables, even on an
empty database. It rejects malformed manifests and reports deployment blockers:
file/checksum mismatches, missing applied migration files, unknown targets,
out-of-order migrations, schema downgrades, omitted tracked objects, incomplete
rollbacks, older release versions, and conflicts with immutable release snapshots.
Blockers are printed with the preview and produce a nonzero exit status. An
unversioned starting database is allowed; a first deployment can be planned.

A ready plan is advisory, not a guarantee that SQL will execute successfully.
It does not validate SQL syntax, permissions, dependencies, or live object drift.
It reads current metadata without reserving the database against later changes.
Malformed files, directory errors, or query failures return errors immediately.

The plan is bounded by the manifest's structural version. `saxbase up` still
applies **all** pending migrations, including those marked `deferred` by a plan;
do not use it blindly when the directory contains versions above your target.
Use `deploy` to honor the manifest target.

## Release deployment

```sh
./saxbase plan
./saxbase deploy
# Or select another manifest:
./saxbase -manifest database/release-30.1.json deploy
```

`deploy` defaults to `database/release.json` and accepts the same connection,
`-dir`, and `-objects-dir` settings as `plan`. It validates the local manifest,
acquires the database deployment lock, and repeats the planning checks under
that lock before changing the database. Blocked deployments exit nonzero.

Goose applies only pending migrations up to the manifest's integer schema
version. Later migration files remain pending. SaxBase then verifies that schema
version and applies changed objects, stores their checksums and complete SQL
snapshots, and marks the release current in one object transaction. An object-only
revision skips structural migrations. Retrying an identical successful release
skips unchanged objects and does not duplicate release history.

The lock spans both phases and serializes cooperating SaxBase deployments,
rollbacks, and other writes. External SQL clients do not honor this lock.

The entire release is **not one transaction**: Goose retains its own transaction
rules. If a migration fails, completed migrations remain; fix the cause and retry.
Before attempting structural changes, SaxBase clears the active release marker,
so a partially migrated database does not claim the previous release is current.
If object execution fails, its transaction rolls back while successful structural
migrations remain. Correct the SQL, regenerate the manifest if its files changed,
and retry `deploy`. SQL errors, dependencies, and permissions can still cause
failure after a successful plan. A lost connection during commit can leave the
outcome uncertain; inspect the database and retry the same manifest.

Deployment never automatically runs Down migrations after a failure. Use
`release rollback VERSION` for an intentional rollback from a recorded current
release. After a failed structural deployment, finish or repair that deployment
before requesting release rollback.

## Development

The [SQL Server example](../examples/sqlserver/README.md) contains a complete
`database/migrations` and `database/objects/{views,procedures,functions}` layout,
with commands and SQL queries for applying and checking changes. The integration
pipeline uses these same files and the CLI's default directory paths.

```sh
go test ./...
go vet ./...
```

Unit tests cover command routing, configuration, output, error propagation,
cleanup, and Goose migration discovery without a database.
They also cover recursive object scanning, exact-byte checksums, state comparison,
object command routing, and error cleanup.

### SQL Server integration pipeline

GitHub Actions runs unit tests and a SQL Server 2022 integration test on pushes,
pull requests, and manual runs. The integration test builds the actual CLI,
waits for SQL Server, creates a uniquely named database, and runs two fixture
migrations: creating a customers table with a row, then adding a nullable
`NVARCHAR(100)` nickname column. It checks the schema, data, Goose history,
`status`, and `version`; verifies a second `up` does not rerun migrations; and
checks each `down` removes only its corresponding change. Cleanup drops the
test database even after assertion failures.
The same pipeline deploys and queries a view, procedure, and function, verifies
unchanged objects are skipped, updates a view, checks its stored SHA-256, verifies
transaction rollback after a later invalid object, and checks missing files are
not dropped.
Release tests verify complete SQL snapshots, retained historical definitions,
immutable version identities, concurrent retries without duplicate records,
and rollback of failed release records. SQL mock tests additionally inject
checksum and snapshot write failures to check transaction rollback without a server.
Rollback coverage also includes restoring despite changed local files, removing
newer objects, preserving permissions, rejecting missing migration files,
structural downgrade failure/retry, and DDL-trigger failure during object restore.
Planner tests cover read-only inspection, target boundaries, validation blockers,
and immutable release checks. The integration suite confirms that planning an
empty SQL Server database creates no tables and tests blocked and unchanged plans.
Deployment tests cover a fresh database, target boundaries, object revisions,
concurrent retries, blocked targets, and recovery after SQL failures in each phase.

Run the same step locally and in CI with Go and Docker available (SQL Server on x86-64):

```sh
bash scripts/test-integration.sh
```

The script starts a disposable SQL Server container on a random loopback port,
runs the integration suite, and removes the container on success, failure, or
interruption. Failed runs print SQL Server logs before cleanup. The tests wait
for database readiness and create and drop their own database. Set
`SAXBASE_SQLSERVER_IMAGE` to override the default SQL Server 2022 image.

To test an existing server instead, supply its connection directly:

```sh
export SAXBASE_TEST_SQLSERVER_DSN='sqlserver://sa:YOUR_PASSWORD@localhost:1433?database=master&encrypt=disable'
go test -tags=integration -count=1 -timeout=6m -v ./test/integration
```

The connection must point to a test server with permission to create and drop
databases. The test selects `master` for setup and changes to its own database
for migrations; it never migrates the database named in the supplied URL.
The script’s fixed password and unencrypted connection are for its disposable
local/CI server. Integration tests require the `integration` build tag and fail if the
connection environment variable is missing, so CI cannot silently skip them.

## Database layout and future releases

```text
database/
  migrations/        # Goose structural migrations
  objects/
    views/
    procedures/
    functions/
```

Releases pair a Goose structural version with an exact object state.
Versions such as `30`, `30.1`, `30.2`, `31`, and `31.1` consist of an integer
schema version and an optional object revision; they must never use floating-point
representation. Release manifests, database release history, and historical SQL
snapshots, explicit release rollback, and read-only release planning are implemented.
Unified deployment is also implemented; ArchiMate model generation remains future work.

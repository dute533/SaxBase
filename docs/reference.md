# SaxBase reference

SaxBase is a Go database deployment CLI built around [Goose](https://github.com/pressly/goose).
Goose remains an external Go module and owns structural migrations. SaxBase owns
deployment semantics through an internal migration engine interface.

## Configuration

Copy `saxbase.yaml.example` to `saxbase.yaml` and `.env.example` to `.env` in your
working directory. Set connection strings in `.env`; keep that file out of Git.
The YAML config contains only environment variable names:

```yaml
default_target: local
targets:
  local:
    connection_env: SAXBASE_LOCAL_DSN
  prod:
    connection_env: SAXBASE_PROD_DSN
    require_confirmation: true
```

```sh
./saxbase plan                       # Uses local
./saxbase -target prod deploy
./saxbase -config team.yaml -target prod plan
```

Set `require_confirmation: true` on any target to require confirmation for `up`,
`down`, `deploy`, `objects apply`, and `release rollback`. The default is `false`.
This applies equally to an explicitly selected target and the configured default.
Before opening a database connection, SaxBase prompts on stderr with the command
and target name. Enter `yes` followed by Enter to continue; other answers, EOF,
and input errors cancel without database access. Ctrl-C cancels the prompt.
Read-only commands and local manifest commands do not prompt.

For unattended writes, pass `-yes` before the command:

```sh
./saxbase -target prod -yes deploy
```

`-yes` skips confirmation only; it does not bypass deployment validation.

`-target` overrides `default_target`. Without either, a config requires an explicit
target. Unknown targets, missing config files requested explicitly, and empty or
missing target connection variables are errors; SaxBase never falls back to
another connection. Target selection overrides `GOOSE_DBSTRING`. Positional
connection strings cannot be combined with a target config. Without a config,
the existing Goose environment and positional connection settings still work.

SaxBase automatically reads `.env` from the current working directory, even when
`-config` points elsewhere. Existing process environment variables take precedence,
including explicitly empty values. Missing `.env` is allowed; malformed files
fail. Values are read without modifying the process environment. Dotenv quoting,
comments, and variable expansion follow the `godotenv` parser; quote connection
strings and URL-encode special characters in URL usernames and passwords.
`.acc.env` and `.prod.env` are not loaded automatically: put the named connection
variables in `.env` or supply them through your CI secrets store.

All database commands use the selected target. The CLI prints its name to stderr
before database access, preserving stdout for command results and JSON. It does
not print the connection string. Local `release create`, `release sync`, and
`release validate` commands need no target credentials; help needs no config.
Only SQL Server is supported; `GOOSE_DRIVER` defaults to `mssql`.

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
rollback can restore committed definitions and remove objects introduced later.

## Release manifests

A manifest contains a release `version`, an optional `parent` manifest path, and
an ordered `objects` array. Each object has a `path` and a `commit` reference,
which can be a full Git hash or `latest`:

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

The example hash is illustrative; use a real commit from your repository.
There is no `format`, `sha256`, or separate order field. A manifest without
`parent` is the initial complete object state. A manifest with `parent` contains
only changed or added objects; entries marked `"delete": true` remove an object.
Deletion entries must use a historical commit hash so SaxBase can recover the
object definition needed to identify and drop it.
Move entries to put changed dependencies before their consumers. SaxBase executes
the array top to bottom. Different objects may reference different commits in the
same repository. The parent must be the release currently deployed.

`30` requires Goose version 30; `30.1` and `30.2` are object revisions at that
structural version. Components are nonnegative 64-bit integers, never floating
point. Use `30` for the initial revision, not `30.0`.

Commit SQL before generating a manifest:

```sh
git add database/objects
git commit -m "Update database objects"
./saxbase release create 30
# Edit database/release.json to arrange dependencies.
./saxbase release validate
git add database/release.json
git commit -m "Record release 30"
./saxbase plan
./saxbase deploy
```

Creation scans `-objects-dir` (default `database/objects`) and pins each SQL file
to its latest touching commit on HEAD when Git is available. Outside Git, files are
recorded as `latest`; this is intended for tests and local, non-reproducible use.
To create a delta, pass the previous manifest with `-parent-manifest`; only changed,
added, and removed objects are written. Git-backed manifests require committed SQL.
Creation refuses to overwrite an existing manifest. It initially lists entries in
lexical path order. Keep every manifest in Git or release artifacts so the state
chain can be reconstructed for rollback.

`release sync [VERSION]` refreshes commit references, keeps existing array order,
and appends new paths in lexical order. For a delta manifest, sync recomputes the
changes against its parent and adds deletion entries when files were removed.
Omitting VERSION preserves the version. Sync replaces the manifest atomically;
invalid input leaves it untouched. Use a new revision when changing an already
deployed release's file contents, paths, or order.

```sh
git add database/objects
git commit -m "Revise database objects"
./saxbase release sync 30.1
./saxbase release validate
./saxbase deploy
```

Manifest entries accept full 40- or 64-character lowercase Git commit IDs or the
special value `latest`. Commit IDs must identify actual commits; branches, tags,
and abbreviated IDs are rejected. Hash references resolve repository-relative
regular SQL files from Git. `latest` reads the corresponding working-tree file,
which allows tests and projects without Git but is not reproducible. SaxBase does
not fetch, check out files, or apply Git filters. Symlinks, duplicate paths, invalid
versions, and unknown fields fail.

Every referenced file is loaded before opening a database connection. Hash-based
entries ignore working-tree edits; `latest` entries read them directly. `release
validate` checks that references can be resolved; it does not validate SQL syntax on
a server. Each object is one batch without `GO`.

`objects apply` and `objects status` use working SQL unless `-manifest` is supplied.
Inside Git, their tracked paths are repository-relative too. Outside Git, these
unversioned commands retain object-directory-relative paths. Manifest-backed apply
requires the current Goose version to match the manifest; `deploy` also applies
pending migrations through the requested schema version. Structural migrations
still come from `-dir`, not the manifest's object commits.

### Database release history

SaxBase stores no SQL snapshots or Git references in the database:

| Table | Contents |
| --- | --- |
| `dbo.saxbase_objects` | Current object paths, SQL checksums, and deployment times. |
| `dbo.saxbase_releases` | Release version, schema version, revision, first deployment time, object count, fingerprint of ordered paths and SQL contents, and the active-release marker. |
| `dbo.saxbase_rollbacks` | Durable rollback progress and errors, created when rollback is used. |

Goose also owns `goose_db_version`. New databases do not create
`dbo.saxbase_release_objects` or `dbo.saxbase_release_state`. The fingerprint detects reuse of a release version
with changed SQL, paths, or order without storing those contents. Definitions,
checksums, release metadata, and the current marker commit in one object transaction.
Concurrent writes use the same database application lock.

```sh
./saxbase release history
./saxbase release show 30.1
./saxbase release current
```

`history` lists releases in descending numeric schema/revision order. `show` prints
release metadata and its fingerprint, with no SQL. These commands need no Git or
local SQL and never create metadata tables. Reapplying the latest identical release
preserves its first deployment time and creates no duplicate history.

Manifest deployment applies only entries in the delta; omitted tracked paths are
left unchanged. Unversioned apply reports missing objects without dropping them.
Manual database edits are
not detected by the checksum cache. Changed unversioned SQL or standalone Goose
changes clear the active release marker.

### Existing databases and manifests

Old checksum manifests must be regenerated after committing their SQL, then reordered
as needed. The new repository-relative paths also change object identity for databases
previously tracking object-directory-relative paths. Map those tracked paths to their
repository-relative equivalents before deploying; SaxBase refuses omitted old paths.

The next versioned apply adds the fingerprint column if needed. Old release records
have no fingerprint and cannot be reused or restored through this workflow; create
a new release baseline. Read-only history/show remain available. The old snapshot
table is neither read nor written and is not automatically dropped. Once its old
rollback data is no longer needed, it can be removed as part of your database upgrade.
Existing `dbo.saxbase_release_state` tables are also not dropped automatically; after
verifying the active marker on `dbo.saxbase_releases`, remove that obsolete table as
part of the same database upgrade.

## Release rollback

Supply the target manifest and the manifest for the active source release:

```sh
./saxbase -manifest releases/30.1.json -source-manifest releases/30.2.json release rollback 30.1
./saxbase release current
./saxbase release rollbacks
```

The target manifest defaults to `database/release.json`; `-source-manifest` is
required. Its source version must match the active release (or the original source
of an interrupted rollback). Both manifests resolve SQL before database access,
from Git for commit entries and the working tree for `latest` entries. Their ordered
files must match the fingerprints recorded at deployment.
The target must already be recorded and no newer than the active release.

The source manifest identifies objects to remove and their reverse deployment order.
Target SQL executes top to bottom in the target manifest's order. No historical SQL
is obtained from the database. Keep both manifests and all referenced commits
available for retries, even on another machine or CI runner.

Before writes, SaxBase validates object identities, source tracking, Goose version,
and required Down migrations. Objects must have schema-qualified
`CREATE [OR ALTER] VIEW`, `PROCEDURE`/`PROC`, or `FUNCTION` headers. Plain CREATE is
executed as CREATE OR ALTER on restore; the original committed bytes determine the
checksum. Retained objects keep their permissions. Untracked objects are never dropped.

Object-only rollback restores target SQL, removes extra tracked objects, replaces
checksums, updates current, and records completion in one transaction. Structural
rollback has separate phases because Goose commits its own migrations:

1. Drop tracked objects absent from the target and commit progress.
2. Run Goose Down migrations using the supplied migrations directory.
3. Restore target objects, checksums, and current in one transaction.

Required Down SQL must be available through `-dir`. Structural rollback can remove
data; schema-bound dependencies may require explicit migration preparation. SaxBase
does not resolve dependency graphs.

`dbo.saxbase_rollbacks` retains source/target versions, phase, status, timestamps,
and latest error, so retries survive process or machine failures. Phases are
`started`, `objects_removed`, and `schema_rolled_back`, ending in `restored`.
After failure, repair the cause and rerun the same command with the same manifests.
Completed Down migrations are not replayed. An incomplete rollback blocks other
SaxBase writes and `release current`; status and history remain available. A
session-level database lock prevents concurrent SaxBase changes between phases.

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
unavailable Git references, missing applied migration files, unknown targets,
out-of-order migrations, schema downgrades, omitted tracked objects, incomplete
rollbacks, older release versions, and conflicts with immutable release fingerprints.
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
version and applies changed objects, stores their checksums and a release fingerprint, and marks the release current in one object transaction. An object-only
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
Release tests verify Git-resolved historical definitions, release fingerprints,
immutable version identities, concurrent retries without duplicate records,
and rollback of failed release records. SQL mock tests additionally inject
checksum write and commit failures to check transaction rollback without a server.
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
Git manifests, explicit release rollback, and read-only release planning are implemented.
Unified deployment is also implemented; ArchiMate model generation remains future work.

## CLI version

Run `saxbase --version` (or `-version`) to print the CLI version without loading
configuration or connecting to a database. Source builds report `saxbase dev`;
packaged builds report their release tag or snapshot label. The existing
`saxbase version` command reports the Goose database migration version.

## Binary distribution

The Binaries workflow runs unit tests, vet, and SQL Server integration tests before
packaging Linux, macOS, and Windows binaries for amd64 and arm64. Linux and macOS
use `.tar.gz` archives; Windows uses `.zip`.
Each includes the executable, license,
documentation, examples, and configuration templates. SHA-256 checksums accompany
the archives. Cross-compiled binaries do not require Go on the user's machine.

Pushes to `main` and manual workflow runs upload preview builds to Actions artifacts
for 14 days. Pushing a `v*` tag builds the tagged code and creates a **draft GitHub
Release** with the same archives and checksums. Review its notes and publish it
when ready; prerelease tags should be marked as prereleases before publishing.
No release is published automatically. Existing releases are never overwritten.

Build packages locally from the repository root with Go, Bash, tar, Python 3, and
sha256sum available:

```sh
bash scripts/build-binaries.sh snapshot
```

Files are written to `dist/`. On Linux, verify downloads with
`sha256sum -c saxbase_VERSION_checksums.txt` after downloading all six archives.
Binaries are currently unsigned and cross-compiled. Before creating a draft
release, CI verifies archive checksums and runs each packaged binary with `-h`
on a matching native runner. SQL Server integration tests run on Linux.

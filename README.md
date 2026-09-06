# SaxBase

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
lock serializes SaxBase object applies, with a 30-second lock wait. Structural
migrations are a separate operation; coordinate them with object deployments.

Each file must contain one complete object definition in a single SQL batch,
typically `CREATE OR ALTER`. Do not include Goose annotations, `GO` separators,
`USE`, or transaction-control statements. SaxBase executes the SQL as supplied;
it does not parse object names or resolve dependencies. Files run in lexical
order of their relative slash paths; arrange paths so dependencies come first.
An object should have one stable file path. Renaming a file is treated as a new
definition plus a missing old path.

Missing files are reported but never automatically dropped or removed from
tracking. Checksums compare files with the last deployment, not live database
definitions, so manual database edits are not detected. Automatic object removal,
dependency resolution, and restoring prior object versions are not implemented yet.

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
See the [complete example manifest](examples/sqlserver/database/release.json).
The schema component is supplied by you; creation does not inspect the database
or certify that the corresponding migration exists.

After editing objects, create a manifest at a new path for the next revision:

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

Deploying with `-manifest` automatically initializes two SaxBase-owned tables:

| Table | Contents |
| --- | --- |
| `dbo.saxbase_releases` | Release version, numeric Goose version and revision, first deployment time in UTC, and object count. |
| `dbo.saxbase_release_objects` | Every object path, SHA-256 checksum, and exact UTF-8 SQL bytes for each release, including unchanged objects. |

The object definitions, deployed checksums, release record, and complete snapshot
commit in one transaction. SQL or metadata errors roll back the transaction,
leaving no successful release record. Existing databases with only
`dbo.saxbase_objects` need no manual metadata migration.

Inspect releases using the configured `GOOSE_DBSTRING`:

```sh
./saxbase release history
./saxbase release show 30.1
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
database application lock. Older versions cannot be reapplied as an implicit
rollback; explicit release rollback is future work.

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
an audit log of every retry. Automatic restoration of snapshots is not yet implemented.

## Development

The [SQL Server example](examples/sqlserver/README.md) contains a complete
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

To run it locally, start a disposable SQL Server instance (Docker on x86-64):

```sh
docker run --rm -d --name saxbase-test-sqlserver \
  -e ACCEPT_EULA=Y -e MSSQL_PID=Developer \
  -e 'MSSQL_SA_PASSWORD=SaxBase_Test_Only_42!' \
  -p 127.0.0.1:1433:1433 mcr.microsoft.com/mssql/server:2022-latest

export SAXBASE_TEST_SQLSERVER_DSN='sqlserver://sa:SaxBase_Test_Only_42!@localhost:1433?database=master&encrypt=disable'
go test -tags=integration -count=1 -timeout=6m -v ./test/integration

docker stop saxbase-test-sqlserver
```

The connection must point to a test server with permission to create and drop
databases. The test selects `master` for setup and changes to its own database
for migrations; it never migrates the database named in the supplied URL.
The password and unencrypted connection above are for the disposable local/CI
server. Integration tests require the `integration` build tag and fail if the
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
snapshots are implemented. Rollback to previous releases, unified deployment
planning, and eventual ArchiMate model generation remain future work.

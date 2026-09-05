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
definitions, so manual database edits are not detected. Release history, automatic
object removal, dependency resolution, and restoring prior object versions are
not implemented yet.

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

Future releases will pair a Goose structural version with an exact object state.
Versions such as `30`, `30.1`, `30.2`, `31`, and `31.1` consist of an integer
schema version and an optional object revision; they must never use floating-point
representation. Release tracking, rollback to previous releases, planning, and
eventual ArchiMate model generation are future work. Release manifests are not
implemented in this milestone.

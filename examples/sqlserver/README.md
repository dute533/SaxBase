# SQL Server example

This is the example used by the SQL Server integration pipeline. Its layout uses
SaxBase's default directories:

```text
examples/sqlserver/
  database/
    migrations/
      00001_create_customers.sql
      00002_add_nickname.sql
    objects/
      functions/value.sql
      procedures/value.sql
      views/value.sql
```

The first migration creates `dbo.customers` and inserts Ada. The second adds a
nullable `nickname` column. The full-state objects read that table: the view
returns customer IDs, the procedure lists IDs, and the function counts customers.
Each object uses `CREATE OR ALTER` and can be edited in place.

## Run against a test database

From the repository root, build SaxBase and move into this example:

```sh
go build -o saxbase .
cd examples/sqlserver
```

On your SQL Server test instance, create a database using your SQL client:

```sql
CREATE DATABASE SaxBaseExample;
```

Configure the connection and apply the example:

```sh
export GOOSE_DRIVER=mssql
export GOOSE_DBSTRING='sqlserver://USER:PASSWORD@localhost:1433?database=SaxBaseExample'
unset GOOSE_MIGRATION_DIR SAXBASE_OBJECTS_DIR

../../saxbase status
../../saxbase up
../../saxbase version
../../saxbase objects status
../../saxbase objects apply
../../saxbase objects status
```

The structural version is now `2`; all three objects should report `unchanged`
after apply. Query the database to check the result:

```sql
SELECT id, name, nickname FROM dbo.customers; -- 1, Ada, NULL
SELECT value FROM dbo.saxbase_value;         -- 1
EXEC dbo.saxbase_get_value;                  -- 1
SELECT dbo.saxbase_function();               -- 1
SELECT path, checksum, deployed_at FROM dbo.saxbase_objects;
```

Run `../../saxbase up` and `../../saxbase objects apply` again: both should leave
the deployed state unchanged.

## Change an object

Edit `database/objects/views/value.sql` to contain:

```sql
CREATE OR ALTER VIEW dbo.saxbase_value AS
SELECT id + 1 AS value FROM dbo.customers;
```

Run `../../saxbase objects status`: the view is `changed`, while the procedure and
function are `unchanged`. Run `../../saxbase objects apply` and query the view
again: its value is now `2`. `../../saxbase version` remains `2` because this did
not add a structural migration.

`../../saxbase down` rolls back the column migration, leaving the customers table
and its row intact. A second `down` drops the table; full-state objects are not
rolled back by Goose and would then reference a missing table. Use a disposable
database for this walkthrough and drop it from `master` when finished.

## Automated verification

From the repository root, point `SAXBASE_TEST_SQLSERVER_DSN` at a test server login
that can create and drop databases, then run:

```sh
go test -tags=integration -count=1 -timeout=6m -v ./test/integration
```

The test creates its own database and copies this entire `database/` directory
to a temporary working directory. It invokes the built CLI with its default
paths, verifies migrations and object results, exercises object updates and
rollback on failure, checks that missing files do not drop objects, and cleans
up the database. The checked-in example files are never modified by the test.
See the root README for disposable Docker setup and connection configuration.

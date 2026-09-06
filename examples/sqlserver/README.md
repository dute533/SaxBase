# SQL Server example

This is the example used by the SQL Server integration pipeline. Its layout uses
SaxBase's default directories:

```text
examples/sqlserver/
  database/
    release.json
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

../../saxbase plan
../../saxbase status
../../saxbase up
../../saxbase version
../../saxbase objects status
../../saxbase release validate
../../saxbase -manifest database/release.json objects apply
../../saxbase objects status
../../saxbase release history
../../saxbase release show 2
```

The initial plan previews migrations 1 and 2 and three new objects without
creating any tables. A later plan of an already deployed release reports applied
migrations and unchanged objects. Blocked plans explain the issue and exit nonzero.

The structural version is now `2`; all three objects should report `unchanged`
after apply. Query the database to check the result:

```sql
SELECT id, name, nickname FROM dbo.customers; -- 1, Ada, NULL
SELECT value FROM dbo.saxbase_value;         -- 1
EXEC dbo.saxbase_get_value;                  -- 1
SELECT dbo.saxbase_function();               -- 1
SELECT path, checksum, deployed_at FROM dbo.saxbase_objects;
```

Run `../../saxbase up` and
`../../saxbase -manifest database/release.json objects apply` again: both leave
the deployed state unchanged, and release `2` has only one history record.

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

To version this change as release `2.1`, generate a new manifest and deploy with it:

```sh
../../saxbase -manifest database/release-2.1.json release create 2.1
../../saxbase -manifest database/release-2.1.json release validate
../../saxbase -manifest database/release-2.1.json plan
../../saxbase -manifest database/release-2.1.json objects apply
../../saxbase release history
../../saxbase release show 2.1
```

The original `database/release.json` contains release `2` and intentionally stops
matching after the view is edited. Applying with that old manifest now fails
before executing object SQL. Manifest creation is offline and refuses to
overwrite existing files. A successful manifest deployment stores a release
record and the complete SQL snapshot in the database, atomically with the object
changes. `release show 2` still returns the original view definition after `2.1`
is deployed. Reusing `2.1` with different definitions fails; create a new revision
instead. A failed deployment leaves no new release record.

## Roll back a release

After deploying `2.1`, restore the recorded release `2`:

```sh
../../saxbase release current
../../saxbase release rollback 2
../../saxbase release current
../../saxbase release rollbacks
```

The view returns `1` again even though its local SQL file still contains `id + 1`.
Goose remains at version `2`, and both release snapshots remain in history.
The object restore, checksum changes, and rollback completion commit together.

To try rollback across structural versions, first restore the local view file
to its initial checked-in contents, then add the supplied third migration:

```sh
cp rollback/00003_add_customer_note.sql database/migrations/
../../saxbase up
../../saxbase -manifest database/release-3.json release create 3
../../saxbase -manifest database/release-3.json objects apply
../../saxbase release rollback 2
../../saxbase version
```

Goose now returns to `2`, removes `rollback_note`, and SaxBase restores the release
`2` objects. Structural rollback runs in phases because Goose commits its own
migrations. If a Down migration or object restore fails, inspect
`release rollbacks`, fix the issue, and retry the same target. Other SaxBase
writes are blocked while that rollback is incomplete. The integration test uses
this third migration and deliberately injects failures to verify recovery.

`../../saxbase down` rolls back the column migration, leaving the customers table
and its row intact. A second `down` drops the table; full-state objects are not
rolled back by Goose and would then reference a missing table. Use a disposable
database for this walkthrough and drop it from `master` when finished.

## Automated verification

From the repository root, with Go and Docker available, run:

```sh
bash scripts/test-integration.sh
```

The script starts SQL Server and removes the container after the test, including
on failure. The test creates its own database and copies this entire `database/` directory
to a temporary working directory. It invokes the built CLI with its default
paths, verifies migrations and object results, exercises object updates and
rollback on failure, checks that missing files do not drop objects, and cleans
up the database. The checked-in example files are never modified by the test.
It also validates the checked-in release manifest, rejects a wrong Goose version
and stale checksums, and generates and applies an object revision manifest.
It verifies complete release snapshots, immutable versions, concurrent retries,
and that failed deployments leave no release or snapshot rows behind.
Rollback tests restore old views independently of local files, remove an object
introduced later, retain permissions, and recover from failures in both Goose
Down and object restoration.
See the root README for disposable Docker setup and connection configuration.

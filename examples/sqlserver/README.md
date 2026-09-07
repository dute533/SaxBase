# SQL Server example

This example uses SaxBase’s default layout:

```text
database/
  migrations/
    00001_create_customers.sql
    00002_add_nickname.sql
  objects/
    views/value.sql
    procedures/value.sql
    functions/value.sql
  release.json
```

The migrations create `dbo.customers`; the objects define a view, procedure, and
function that use that table. Each object is a complete `CREATE OR ALTER` definition.

## Run it

From the repository root:

```sh
go build -o saxbase .
cd examples/sqlserver
export GOOSE_DRIVER=mssql
export GOOSE_DBSTRING='sqlserver://USER:PASSWORD@localhost:1433?database=SaxBaseExample'
```

Create the database, then preview and apply the checked-in release:

```sql
CREATE DATABASE SaxBaseExample;
```

```sh
../../saxbase plan
../../saxbase apply
../../saxbase status
```

Check the result:

```sql
SELECT id, name, nickname FROM dbo.customers;
SELECT value FROM dbo.saxbase_value;
EXEC dbo.saxbase_get_value;
SELECT dbo.saxbase_function();
```

## Change an object

Edit `database/objects/views/value.sql`, then inspect the change:

```sh
../../saxbase status
../../saxbase apply
```

To record the change as release `2.1`, commit the SQL and create a delta manifest:

```sh
git add database/objects
git commit -m "Update example objects"
../../saxbase -parent-manifest database/release.json \
  -manifest database/release-2.1.json release create 2.1
../../saxbase -manifest database/release-2.1.json plan
../../saxbase -manifest database/release-2.1.json apply
```

## Roll back

Restore release `2` using both manifests:

```sh
../../saxbase -manifest database/release.json \
  -source-manifest database/release-2.1.json rollback 2
```

Rollback resolves the target objects from their historical Git commits. Keep the
manifests and referenced commits available.

## Verify locally

From the repository root, run the disposable SQL Server integration test:

```sh
bash scripts/test-integration.sh
```

The test uses a temporary database and does not modify these example files.

See the [manifest ordering example](ordering/README.md) and the
[full reference](../../docs/reference.md).

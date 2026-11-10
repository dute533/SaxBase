# SQL Server example

This example uses SaxBase's default layout:

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

The migrations create `dbo.customers`. The object files contain complete view,
procedure, and function definitions that use it.

## Run it

From the repository root:

```sh
go build -o saxbase .
cd examples/sqlserver
export GOOSE_DBSTRING='sqlserver://USER:PASSWORD@localhost:1433?database=SaxBaseExample'
```

Create the database, then deploy the checked-in release:

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

## Create another release

For an object-only release, edit an object definition and commit it before
creating the manifest entry:

```sh
git add database/objects
git commit -m "Update example objects"
../../saxbase release create
../../saxbase plan
../../saxbase apply
```

For a structural release, create and edit the next migration first:

```sh
../../saxbase migration create add_customer_email
```

Then commit the database files and repeat `release create`, `plan`, and `apply`.

## Roll back

Restore the preceding release, or specify an older version:

```sh
../../saxbase rollback
../../saxbase rollback 2
```

See the [manifest ordering example](ordering/README.md) and the
[full reference](../../docs/reference.md).

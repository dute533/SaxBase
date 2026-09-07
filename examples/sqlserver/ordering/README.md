# Manifest dependency order

These files are exercised by the automated SQL Server integration suite.
`a_dependent.sql` selects a column from the view defined in `z_base.sql`.
The base must execute first, despite its filename sorting last.

From `examples/sqlserver`, with the CLI built and a fresh test database connection
configured as described in the parent README:

```sh
cp ordering/*.sql database/objects/views/
git add database/objects
git commit -m "Add ordered example objects"
../../saxbase -manifest database/ordered-release.json release create 2
```

Edit `database/ordered-release.json`: move the `examples/sqlserver/database/objects/views/z_base.sql` entry first,
then `examples/sqlserver/database/objects/views/a_dependent.sql`, followed by the remaining entries. Keep every
generated commit reference and each object entry exactly once. Array position is the
deployment order; no additional order property is needed.

```sh
../../saxbase -manifest database/ordered-release.json plan
../../saxbase -manifest database/ordered-release.json apply
../../saxbase release show 2
```

Query `SELECT original_value FROM dbo.ordered_dependent;` to obtain `1`.
Rollback uses the order of the supplied historical manifest.

The integration test also renames the base view's output column, updates its
dependent view in a new revision, and rolls back. Restoring the dependency first
is necessary for the old dependent definition to compile. Tests work on temporary
copies and leave these example files unchanged.

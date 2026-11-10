# Manifest dependency order

`a_dependent.sql` uses the view defined in `z_base.sql`, so the manifest must
list `z_base.sql` first. Filenames do not determine deployment order.

From `examples/sqlserver`, copy the objects and create a release:

```sh
cp ordering/*.sql database/objects/views/
git add database/objects
git commit -m "Add ordered example objects"
../../saxbase -manifest database/ordered-release.json release create 2
```

Move the `z_base.sql` entry before `a_dependent.sql` in
`database/ordered-release.json`, then deploy it:

```sh
../../saxbase -manifest database/ordered-release.json plan
../../saxbase -manifest database/ordered-release.json apply
```

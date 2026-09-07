# Manifest dependency order

`a_dependent.sql` reads from the view in `z_base.sql`. The manifest must therefore
list `z_base.sql` first, even though its filename sorts later.

From `examples/sqlserver`, copy the objects and create a release:

```sh
cp ordering/*.sql database/objects/views/
git add database/objects
git commit -m "Add ordered example objects"
../../saxbase -manifest database/ordered-release.json release create 2
```

Edit `database/ordered-release.json` and move the `z_base.sql` entry before
`a_dependent.sql`. The array order is the deployment order; no extra order field is
needed.

```sh
../../saxbase -manifest database/ordered-release.json plan
../../saxbase -manifest database/ordered-release.json apply
```

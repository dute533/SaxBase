package objects

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"saxbase/internal/dialect"
)

func TestPostgresReleaseUsesNativeMetadataSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	spec, err := objectDialectFor(dialect.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	store := &store{db: db, dialect: spec}
	file := objectFile("views/value.sql", "CREATE OR REPLACE VIEW public.value AS SELECT 1 AS value;")
	release := releaseFor("1.0.0", 1, []File{file})

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT max\(version_id\) FROM public\.saxbase_schema_version`).WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(1))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS public.saxbase_releases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT fingerprint FROM public.saxbase_releases").WithArgs("1.0.0").WillReturnRows(sqlmock.NewRows([]string{"fingerprint"}))
	mock.ExpectExec("INSERT INTO public.saxbase_releases").WithArgs("1.0.0", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("CREATE OR REPLACE VIEW").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE public.saxbase_releases SET is_current=false").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE public.saxbase_releases SET is_current=true").WithArgs("1.0.0").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	rows, err := store.applyOn(context.Background(), db, []File{file}, nil, *release, false)
	if err != nil || len(rows) != 1 || rows[0].State != "applied" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresObjectSyntax(t *testing.T) {
	spec, err := objectDialectFor(dialect.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	id, err := objectIdentity(`CREATE OR REPLACE FUNCTION "odd.schema"."odd""name"() RETURNS integer LANGUAGE sql AS 'SELECT 1';`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := spec.drop(id), `DROP FUNCTION IF EXISTS "odd.schema"."odd""name";`; got != want {
		t.Fatalf("drop=%q want=%q", got, want)
	}
	plain := "CREATE VIEW public.value AS SELECT 1 AS value;"
	if got, want := spec.restore(plain), "CREATE OR REPLACE VIEW public.value AS SELECT 1 AS value;"; got != want {
		t.Fatalf("restore=%q want=%q", got, want)
	}
}

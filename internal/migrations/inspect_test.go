package migrations

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pressly/goose/v3"
)

func TestInspectDoesNotInitializeGoose(t *testing.T) {
	for _, existing := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		p, err := goose.NewProvider(goose.DialectMSSQL, db, fstest.MapFS{"00001_first.sql": {Data: []byte("-- +goose Up\nSELECT 1;\n")}, "00003_next.sql": {Data: []byte("-- +goose Up\nSELECT 3;\n")}})
		if err != nil {
			t.Fatal(err)
		}
		table := sqlmock.NewRows([]string{"id"})
		if existing {
			table.AddRow(1)
		} else {
			table.AddRow(nil)
		}
		mock.ExpectQuery("SELECT OBJECT_ID").WillReturnRows(table)
		if existing {
			mock.ExpectQuery("SELECT version_id, is_applied FROM goose_db_version").WillReturnRows(sqlmock.NewRows([]string{"version_id", "is_applied"}).AddRow(2, true).AddRow(1, true).AddRow(0, true))
		}
		state, err := (&gooseEngine{db: db, provider: p}).Inspect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if existing {
			if state.Version != 2 || len(state.Migrations) != 3 || state.Migrations[1].State != "missing" {
				t.Fatalf("%+v", state)
			}
		} else {
			if state.Version != 0 || len(state.Migrations) != 2 || state.Migrations[0].State != "pending" {
				t.Fatalf("%+v", state)
			}
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
}

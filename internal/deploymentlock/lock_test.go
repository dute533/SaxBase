package deploymentlock

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"saxbase/internal/dialect"
)

func TestLockReleaseAfterCancellation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("DECLARE.*sp_getapplock").WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow(0))
	mock.ExpectQuery("DECLARE.*sp_releaseapplock").WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow(0))
	ctx, cancel := context.WithCancel(context.Background())
	_, unlock, err := Acquire(ctx, db, dialect.SQLServer)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("SELECT pg_advisory_lock").WithArgs(postgresLockKey).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(postgresLockKey).WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
	_, unlock, err := Acquire(context.Background(), db, dialect.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

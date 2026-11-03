package deploymentlock

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
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
	_, unlock, err := Acquire(ctx, db)
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

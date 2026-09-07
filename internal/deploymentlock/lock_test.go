package deploymentlock

import (
	"context"
	"strings"
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

func TestPendingRollbackBlocksWrites(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT OBJECT_ID").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT TOP").WillReturnRows(sqlmock.NewRows([]string{"target"}).AddRow("30.1"))
	if err := CheckPending(context.Background(), db); err == nil || !strings.Contains(err.Error(), "retry rollback 30.1") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

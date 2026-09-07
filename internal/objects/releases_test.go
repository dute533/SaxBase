package objects

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func objectFile(path, definition string) File {
	return File{Path: path, SQL: definition, Checksum: fmt.Sprintf("%x", sha256.Sum256([]byte(definition)))}
}

func mockStore(t *testing.T) (*store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		db.Close()
	})
	return &store{db: db}, mock
}

func expectReleaseStart(mock sqlmock.Sqlmock, schema int64, deployed []File) {
	mock.ExpectBegin()
	mock.ExpectQuery("DECLARE @result int;").WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(0))
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_rollbacks").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(nil))
	mock.ExpectQuery(`SELECT MAX\(version_id\) FROM goose_db_version`).WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(schema))
	mock.ExpectExec("IF OBJECT_ID.*saxbase_objects").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_objects").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	rows := sqlmock.NewRows([]string{"path", "checksum"})
	for _, file := range deployed {
		rows.AddRow(file.Path, file.Checksum)
	}
	mock.ExpectQuery("SELECT path, checksum FROM dbo.saxbase_objects").WillReturnRows(rows)
}

func expectNewRelease(mock sqlmock.Sqlmock, version string, schema, revision int64, count int) {
	mock.ExpectExec("IF OBJECT_ID.*saxbase_releases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT id, fingerprint FROM dbo.saxbase_releases").WithArgs(version).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT TOP.*schema_version, revision").WillReturnRows(sqlmock.NewRows([]string{"schema", "revision"}))
	mock.ExpectQuery("INSERT INTO dbo.saxbase_releases").WithArgs(version, schema, revision, count, sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
}

func expectObjectApply(mock sqlmock.Sqlmock, file File) {
	mock.ExpectExec("CREATE OR ALTER VIEW").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE dbo.saxbase_objects").WithArgs(file.Path, file.Checksum).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestReleaseCommitsFingerprintWithoutSQLSnapshot(t *testing.T) {
	s, mock := mockStore(t)
	files := []File{objectFile("a.sql", "CREATE OR ALTER VIEW dbo.a AS SELECT N'Grüße' AS n;\r\n"), objectFile("b.sql", "CREATE OR ALTER VIEW dbo.b AS SELECT 2 AS n;")}
	// The second file is already deployed but remains part of release identity.
	expectReleaseStart(mock, 30, files[1:])
	expectNewRelease(mock, "30.1", 30, 1, 2)
	expectObjectApply(mock, files[0])
	expectCurrent(mock, "30.1")
	mock.ExpectCommit()
	rows, err := s.apply(context.Background(), files, &Release{Version: "30.1", SchemaVersion: 30, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].State != "applied" || rows[1].State != "unchanged" {
		t.Fatalf("statuses: %+v", rows)
	}
}

func TestReleaseRollsBackOnSQLOrChecksumFailure(t *testing.T) {
	failure := errors.New("injected database failure")
	for _, phase := range []string{"object", "checksum", "commit"} {
		t.Run(phase, func(t *testing.T) {
			s, mock := mockStore(t)
			file := objectFile("a.sql", "CREATE OR ALTER VIEW dbo.a AS SELECT 1 AS n;")
			expectReleaseStart(mock, 30, nil)
			expectNewRelease(mock, "30", 30, 0, 1)
			object := mock.ExpectExec("CREATE OR ALTER VIEW")
			if phase == "object" {
				object.WillReturnError(failure)
			} else {
				object.WillReturnResult(sqlmock.NewResult(0, 0))
				checksum := mock.ExpectExec("UPDATE dbo.saxbase_objects").WithArgs(file.Path, file.Checksum)
				if phase == "checksum" {
					checksum.WillReturnError(failure)
				} else {
					checksum.WillReturnResult(sqlmock.NewResult(0, 1))

				}
			}
			if phase == "commit" {
				expectCurrent(mock, "30")
				mock.ExpectCommit().WillReturnError(failure)
			} else {
				mock.ExpectRollback()
			}
			rows, err := s.apply(context.Background(), []File{file}, &Release{Version: "30", SchemaVersion: 30})
			if !errors.Is(err, failure) || rows != nil {
				t.Fatalf("result=%v error=%v", rows, err)
			}
		})
	}
}

func TestReleaseIdentityAndOrdering(t *testing.T) {
	file := objectFile("a.sql", "CREATE OR ALTER VIEW dbo.a AS SELECT 1 AS n;")
	for _, scenario := range []string{"retry", "changed", "older"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := mockStore(t)
			expectReleaseStart(mock, 30, []File{file})
			mock.ExpectExec("IF OBJECT_ID.*saxbase_releases").WillReturnResult(sqlmock.NewResult(0, 0))

			definition := file.SQL
			if scenario == "changed" {
				definition += "\n"
			}
			fingerprint := Fingerprint([]File{objectFile(file.Path, definition)})
			mock.ExpectQuery("SELECT id, fingerprint FROM dbo.saxbase_releases").WithArgs("30.2").WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint"}).AddRow(7, fingerprint))
			if scenario != "changed" {
				revision := int64(2)
				if scenario == "older" {
					revision = 10
				}
				mock.ExpectQuery("SELECT TOP.*schema_version, revision").WillReturnRows(sqlmock.NewRows([]string{"schema", "revision"}).AddRow(30, revision))
			}
			if scenario == "retry" {
				expectCurrent(mock, "30.2")
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			rows, err := s.apply(context.Background(), []File{file}, &Release{Version: "30.2", SchemaVersion: 30, Revision: 2})
			if scenario == "retry" {
				if err != nil || rows[0].State != "unchanged" {
					t.Fatalf("retry: %v %v", rows, err)
				}
			} else {
				if err == nil {
					t.Fatal("invalid release accepted")
				}
			}
		})
	}
}

func TestReleaseRechecksGooseVersionAndLock(t *testing.T) {
	for _, scenario := range []string{"schema", "lock"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := mockStore(t)
			mock.ExpectBegin()
			code := 0
			if scenario == "lock" {
				code = -1
			}
			mock.ExpectQuery("DECLARE @result int;").WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(code))
			if scenario == "schema" {
				mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_rollbacks").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(nil))
				mock.ExpectQuery(`SELECT MAX\(version_id\) FROM goose_db_version`).WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(31))
			}
			mock.ExpectRollback()
			if _, err := s.apply(context.Background(), nil, &Release{Version: "30.1", SchemaVersion: 30, Revision: 1}); err == nil {
				t.Fatal("guard failed")
			}
		})
	}
}

func expectCurrent(mock sqlmock.Sqlmock, version string) {
	mock.ExpectExec("UPDATE dbo.saxbase_releases").WithArgs(version).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestReleaseHistoryBeforeFirstDeployment(t *testing.T) {
	s, mock := mockStore(t)
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(nil))
	rows, err := s.History(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("history=%v err=%v", rows, err)
	}
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(nil))
	if _, err := s.Snapshot(context.Background(), "30"); err == nil {
		t.Fatal("missing release found")
	}
}

func TestStoredReleaseMetadata(t *testing.T) {
	s, mock := mockStore(t)
	when := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	file := objectFile("a.sql", "SELECT N'Grüße';\r\n")
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT id, version, schema_version").WithArgs("30.1").WillReturnRows(sqlmock.NewRows([]string{"id", "version", "schema", "revision", "time", "count", "fingerprint"}).AddRow(7, "30.1", 30, 1, when, 1, Fingerprint([]File{file})))

	snapshot, err := s.Snapshot(context.Background(), "30.1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != "30.1" || snapshot.ObjectCount != 1 || snapshot.Fingerprint != Fingerprint([]File{file}) || len(snapshot.Objects) != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestInvalidReleaseInput(t *testing.T) {
	s, _ := mockStore(t)
	file := objectFile("a.sql", "SELECT 1;")
	if _, err := s.apply(context.Background(), []File{file, file}, &Release{Version: "30.1", SchemaVersion: 30, Revision: 1}); err == nil {
		t.Fatal("duplicate accepted")
	}
	file.Checksum = "bad"
	if _, err := s.apply(context.Background(), []File{file}, &Release{Version: "30.1", SchemaVersion: 30, Revision: 1}); err == nil {
		t.Fatal("incorrect checksum accepted")
	}
}

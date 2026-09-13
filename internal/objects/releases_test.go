package objects

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

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

func expectReleaseStart(mock sqlmock.Sqlmock, schema int64) {
	mock.ExpectBegin()
	mock.ExpectQuery("DECLARE @result int;").WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(0))
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_rollbacks").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(nil))
	mock.ExpectQuery(`SELECT MAX\(version_id\) FROM goose_db_version`).WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(schema))
}

func expectNewRelease(mock sqlmock.Sqlmock, version string) {
	mock.ExpectExec("IF OBJECT_ID.*saxbase_releases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT fingerprint FROM dbo.saxbase_releases").WithArgs(version).WillReturnRows(sqlmock.NewRows([]string{"fingerprint"}))
	mock.ExpectQuery("SELECT version FROM dbo.saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"version"}))
	mock.ExpectExec("INSERT INTO dbo.saxbase_releases").WithArgs(version, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectObjectApply(mock sqlmock.Sqlmock, file File) {
	mock.ExpectExec("CREATE OR ALTER VIEW").WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestReleaseCommitsFingerprintWithoutSQLSnapshot(t *testing.T) {
	s, mock := mockStore(t)
	files := []File{objectFile("a.sql", "CREATE OR ALTER VIEW dbo.a AS SELECT N'Grüße' AS n;\r\n"), objectFile("b.sql", "CREATE OR ALTER VIEW dbo.b AS SELECT 2 AS n;")}
	// The second file is already deployed but remains part of release identity.
	expectReleaseStart(mock, 30)
	expectNewRelease(mock, "30.1")
	expectObjectApply(mock, files[0])
	expectCurrent(mock, "30.1")
	mock.ExpectCommit()
	rows, err := s.apply(context.Background(), files, files[1:], &Release{Version: "30.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].State != "applied" || rows[1].State != "unchanged" {
		t.Fatalf("statuses: %+v", rows)
	}
}

func TestReleaseRollsBackOnObjectOrCommitFailure(t *testing.T) {
	failure := errors.New("injected database failure")
	for _, phase := range []string{"object", "commit"} {
		t.Run(phase, func(t *testing.T) {
			s, mock := mockStore(t)
			file := objectFile("a.sql", "CREATE OR ALTER VIEW dbo.a AS SELECT 1 AS n;")
			expectReleaseStart(mock, 30)
			expectNewRelease(mock, "30")
			object := mock.ExpectExec("CREATE OR ALTER VIEW")
			if phase == "object" {
				object.WillReturnError(failure)
			} else {
				object.WillReturnResult(sqlmock.NewResult(0, 0))
				expectCurrent(mock, "30")
			}
			if phase == "commit" {
				mock.ExpectCommit().WillReturnError(failure)
			} else {
				mock.ExpectRollback()
			}
			rows, err := s.apply(context.Background(), []File{file}, nil, &Release{Version: "30"})
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
			expectReleaseStart(mock, 30)
			mock.ExpectExec("IF OBJECT_ID.*saxbase_releases").WillReturnResult(sqlmock.NewResult(0, 0))

			definition := file.SQL
			if scenario == "changed" {
				definition += "\n"
			}
			fingerprint := Fingerprint([]File{objectFile(file.Path, definition)})
			existing := sqlmock.NewRows([]string{"fingerprint"})
			if scenario != "older" {
				existing.AddRow(fingerprint)
			}
			mock.ExpectQuery("SELECT fingerprint FROM dbo.saxbase_releases").WithArgs("30.2").WillReturnRows(existing)
			if scenario == "older" {
				mock.ExpectQuery("SELECT version FROM dbo.saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow("30.10"))
			}
			if scenario == "retry" {
				mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
				mock.ExpectQuery("SELECT version FROM dbo.saxbase_releases WHERE is_current=1").WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow("30.2"))
				expectCurrent(mock, "30.2")
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			var baseline []File
			if scenario != "retry" {
				baseline = []File{file}
			}
			rows, err := s.apply(context.Background(), []File{file}, baseline, &Release{Version: "30.2"})
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

func TestForceApplyRewritesFingerprintAndEveryDeltaEntry(t *testing.T) {
	s, mock := mockStore(t)
	view := objectFile("view.sql", "CREATE OR ALTER VIEW dbo.current_view AS SELECT 2 AS n;")
	removed := objectFile("removed.sql", "CREATE VIEW dbo.removed_view AS SELECT 1 AS n;")
	removed.Delete = true
	files := []File{view, removed}

	expectReleaseStart(mock, 30)
	mock.ExpectExec("IF OBJECT_ID.*saxbase_releases").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT fingerprint FROM dbo.saxbase_releases").WithArgs("30.1").WillReturnRows(
		sqlmock.NewRows([]string{"fingerprint"}).AddRow(Fingerprint([]File{objectFile("old.sql", "SELECT 1;")})),
	)
	mock.ExpectExec("UPDATE dbo.saxbase_releases SET fingerprint").WithArgs(Fingerprint(files), "30.1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("CREATE OR ALTER VIEW dbo.current_view").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DROP VIEW IF EXISTS.*removed_view").WillReturnResult(sqlmock.NewResult(0, 0))
	expectCurrent(mock, "30.1")
	mock.ExpectCommit()

	rows, err := s.applyOn(context.Background(), s.db, files, nil, &Release{Version: "30.1"}, true, true)
	if err != nil || len(rows) != 2 || rows[0].State != "applied" || rows[1].State != "deleted" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestFingerprintIncludesOperationButNotCommit(t *testing.T) {
	applied := objectFile("view.sql", "CREATE VIEW dbo.current_view AS SELECT 1 AS n;")
	fromAnotherCommit := applied
	fromAnotherCommit.Commit = "different-source-commit"
	deleted := applied
	deleted.Delete = true

	if Fingerprint([]File{applied}) != Fingerprint([]File{fromAnotherCommit}) {
		t.Fatal("commit metadata changed the release fingerprint")
	}
	if Fingerprint([]File{applied}) == Fingerprint([]File{deleted}) {
		t.Fatal("apply and delete operations have the same release fingerprint")
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
			if _, err := s.apply(context.Background(), nil, nil, &Release{Version: "30.1"}); err == nil {
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

func TestReleaseHistoryUsesNumericVersionOrder(t *testing.T) {
	s, mock := mockStore(t)
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT version FROM dbo.saxbase_releases").WillReturnRows(
		sqlmock.NewRows([]string{"version"}).AddRow("2.2").AddRow("10").AddRow("2.10").AddRow("2"),
	)

	history, err := s.History(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10", "2.10", "2.2", "2"}
	if len(history) != len(want) {
		t.Fatalf("history=%+v", history)
	}
	for i := range want {
		if history[i].Version != want[i] {
			t.Fatalf("history[%d]=%s, want %s", i, history[i].Version, want[i])
		}
	}
}

func TestStoredReleaseMetadata(t *testing.T) {
	s, mock := mockStore(t)
	file := objectFile("a.sql", "SELECT N'Grüße';\r\n")
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT version, fingerprint").WithArgs("30.1").WillReturnRows(sqlmock.NewRows([]string{"version", "fingerprint"}).AddRow("30.1", Fingerprint([]File{file})))

	snapshot, err := s.Snapshot(context.Background(), "30.1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != "30.1" || snapshot.Fingerprint != Fingerprint([]File{file}) || len(snapshot.Objects) != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestInvalidReleaseInput(t *testing.T) {
	s, _ := mockStore(t)
	file := objectFile("a.sql", "SELECT 1;")
	if _, err := s.apply(context.Background(), []File{file, file}, nil, &Release{Version: "30.1"}); err == nil {
		t.Fatal("duplicate accepted")
	}
	file.Checksum = "bad"
	if _, err := s.apply(context.Background(), []File{file}, nil, &Release{Version: "30.1"}); err == nil {
		t.Fatal("incorrect checksum accepted")
	}
}

package objects

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"saxbase/internal/migrations"
)

type rollbackGoose struct {
	migrations.Engine
	version int64
	target  int64
	err     error
}

func (g *rollbackGoose) Version(context.Context) (int64, error) { return g.version, nil }
func (g *rollbackGoose) ValidateDownTo(context.Context, int64) error {
	return nil
}
func (g *rollbackGoose) DownTo(_ context.Context, target int64) error {
	g.target = target
	return g.err
}

func snapshot(version string, migrationVersion int64, delta []File, state ...File) Snapshot {
	result := Snapshot{Release: Release{Version: version, MigrationVersion: migrationVersion, Fingerprint: ReleaseFingerprint(migrationVersion, delta)}}
	for _, file := range state {
		result.Objects = append(result.Objects, SnapshotObject{Path: file.Path, SQL: file.SQL, Checksum: file.Checksum})
	}
	return result
}

func expectRecordedSnapshot(mock sqlmock.Sqlmock, release Snapshot) {
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT version, fingerprint").WithArgs(release.Version).WillReturnRows(
		sqlmock.NewRows([]string{"version", "fingerprint"}).AddRow(release.Version, release.Fingerprint),
	)
}

func expectCurrentRelease(mock sqlmock.Sqlmock, version string) {
	mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT version FROM dbo.saxbase_releases WHERE is_current=1").WillReturnRows(
		sqlmock.NewRows([]string{"version"}).AddRow(version),
	)
}

func TestReconciledChecksStoredFingerprint(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fingerprint string
		want        bool
	}{
		{name: "matching", want: true},
		{name: "changed", fingerprint: "different", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := mockStore(t)
			file := objectFile("a.sql", "CREATE OR ALTER VIEW dbo.a AS SELECT 1 AS n;\n")
			release := snapshot("2.1.0", 2, []File{file}, file)
			expectCurrentRelease(mock, release.Version)
			fingerprint := tc.fingerprint
			if fingerprint == "" {
				fingerprint = release.Fingerprint
			}
			mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_releases").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			mock.ExpectQuery("SELECT version, fingerprint").WithArgs(release.Version).WillReturnRows(
				sqlmock.NewRows([]string{"version", "fingerprint"}).AddRow(release.Version, fingerprint),
			)

			got, err := s.Reconciled(context.Background(), release)
			if err != nil || got != tc.want {
				t.Fatalf("reconciled=%v, want %v: %v", got, tc.want, err)
			}
		})
	}
}

func TestRollbackCheckpointIsDistinctFromManifestRewrite(t *testing.T) {
	fingerprint := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if !IsRollbackCheckpoint(rollbackMarker(fingerprint), fingerprint) {
		t.Fatal("rollback checkpoint was not recognized")
	}
	if IsRollbackCheckpoint("different", fingerprint) || IsRollbackCheckpoint(fingerprint, fingerprint) {
		t.Fatal("ordinary fingerprints were recognized as rollback checkpoints")
	}
}

func TestStructuralRollbackCheckpointsTargetBeforeGooseDown(t *testing.T) {
	s, mock := mockStore(t)
	base := objectFile("a.sql", "CREATE OR ALTER VIEW dbo.a AS SELECT 1 AS n;")
	extra := objectFile("b.sql", "CREATE OR ALTER VIEW dbo.b AS SELECT 2 AS n;")
	target := snapshot("2.1.0", 2, []File{base}, base)
	source := snapshot("3.0.0", 3, []File{extra}, base, extra)
	failure := errors.New("down failed")
	goose := &rollbackGoose{version: 3, err: failure}

	mock.ExpectQuery("DECLARE.*sp_getapplock").WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow(0))
	expectRecordedSnapshot(mock, target)
	expectRecordedSnapshot(mock, source)
	expectCurrentRelease(mock, source.Version)
	mock.ExpectBegin()
	mock.ExpectExec("DROP VIEW IF EXISTS.*dbo.*b").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE dbo.saxbase_releases SET fingerprint").WithArgs(rollbackMarker(target.Fingerprint), target.Version).WillReturnResult(sqlmock.NewResult(0, 1))
	expectCurrent(mock, target.Version)
	mock.ExpectCommit()
	mock.ExpectQuery("DECLARE.*sp_releaseapplock").WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow(0))

	_, err := s.Rollback(context.Background(), target, source, goose)
	if !errors.Is(err, failure) || goose.target != 2 {
		t.Fatalf("target=%d err=%v", goose.target, err)
	}
}

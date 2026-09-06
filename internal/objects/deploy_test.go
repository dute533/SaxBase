package objects

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"saxbase/internal/migrations"
)

type deployGoose struct {
	migrations.Engine
	target  int64
	failure error
}

func (g *deployGoose) Inspect(context.Context) (migrations.Inspection, error) {
	return migrations.Inspection{Version: 2}, nil
}
func (g *deployGoose) UpTo(_ context.Context, target int64) error {
	g.target = target
	return g.failure
}

func TestDeployReleasesLockOnPreflightOrMigrationFailure(t *testing.T) {
	for _, phase := range []string{"preflight", "migration"} {
		t.Run(phase, func(t *testing.T) {
			s, mock := mockStore(t)
			failure := errors.New("injected failure")
			g := &deployGoose{failure: failure}
			mock.ExpectQuery("DECLARE.*sp_getapplock").WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow(0))
			mock.ExpectQuery("SELECT OBJECT_ID.*saxbase_rollbacks").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(nil))
			if phase == "migration" {
				mock.ExpectExec("IF OBJECT_ID.*saxbase_release_state").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectQuery("DECLARE.*sp_releaseapplock").WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow(0))
			_, err := s.Deploy(context.Background(), nil, 3, 0, g, func(context.Context) error {
				if phase == "preflight" {
					return failure
				}
				return nil
			})
			if !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if phase == "preflight" && g.target != 0 {
				t.Fatal("migration ran despite blocked preflight")
			}
			if phase == "migration" && g.target != 3 {
				t.Fatal("incorrect migration target")
			}
		})
	}
}

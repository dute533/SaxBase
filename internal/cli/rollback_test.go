package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

func TestRollbackCommand(t *testing.T) {
	for _, failure := range []error{nil, errors.New("rollback failed")} {
		goose := &fakeEngine{}
		store := &fakeObjects{err: failure}
		var out bytes.Buffer
		err := run(context.Background(), []string{"release", "rollback", "30.1"}, env(map[string]string{"GOOSE_DBSTRING": "dsn", "SAXBASE_OBJECTS_DIR": "absent"}), &out, func(migrations.Config) (migrations.Engine, error) { return goose, nil }, func(string) (objects.Engine, error) { return store, nil })
		if !errors.Is(err, failure) || !goose.closed || !store.closed || store.command != "rollback" {
			t.Fatalf("result: %v %+v %+v", err, goose, store)
		}
		if failure == nil && !strings.Contains(out.String(), "30.2 to 30.1") {
			t.Fatal(out.String())
		}
		if failure != nil && out.Len() != 0 {
			t.Fatal("printed success after failure")
		}
	}
}

func TestRollbackValidationBeforeOpening(t *testing.T) {
	for _, args := range [][]string{{"release", "rollback"}, {"release", "rollback", "30.0"}, {"release", "rollback", "30"}, {"-manifest", "some.json", "release", "rollback", "30"}} {
		if err := run(context.Background(), args, env(nil), &bytes.Buffer{}, nil, nil); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

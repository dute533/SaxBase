package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"saxbase/internal/migrations"
)

func TestCLIVersionIsOffline(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfigFixture(t, ".env", "INVALID='")
	writeConfigFixture(t, "saxbase.yaml", "invalid: [")
	for _, flag := range []string{"--version", "-version"} {
		var out bytes.Buffer
		err := Run(context.Background(), []string{flag}, func(string) string { t.Fatal("read environment"); return "" }, &out,
			func(migrations.Config) (migrations.Engine, error) { t.Fatal("opened database"); return nil, nil })
		if err != nil || out.String() != "saxbase "+Version+"\n" {
			t.Fatalf("%q %v", out.String(), err)
		}
	}
	if err := Run(context.Background(), []string{"--version", "apply"}, nil, &bytes.Buffer{}, nil); err == nil {
		t.Fatal("accepted mixed command")
	}
	failure := errors.New("write failed")
	if err := Run(context.Background(), []string{"--version"}, nil, versionErrorWriter{failure}, nil); !errors.Is(err, failure) {
		t.Fatalf("%v", err)
	}
}

type versionErrorWriter struct{ err error }

func (w versionErrorWriter) Write([]byte) (int, error) { return 0, w.err }

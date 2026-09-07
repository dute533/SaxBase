package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

func TestProtectedWritesRejectBeforeDatabaseAccess(t *testing.T) {
	for _, command := range [][]string{{"apply"}, {"rollback", "30"}} {
		for _, answer := range []string{"no\n", "\n", "", "yes"} {
			t.Run(strings.Join(command, " ")+"/"+answer, func(t *testing.T) {
				t.Chdir(t.TempDir())
				writeConfigFixture(t, "saxbase.yaml", "default_target: prod\ntargets: {prod: {connection_env: DSN, require_confirmation: true}}")
				var out bytes.Buffer
				err := run(context.Background(), command, env(map[string]string{"DSN": "secret"}), &out,
					func(migrations.Config) (migrations.Engine, error) {
						t.Fatal("opened migrations before confirmation")
						return nil, nil
					},
					func(string) (objects.Engine, error) { t.Fatal("opened objects before confirmation"); return nil, nil },
					runEnvironment{diagnostics: &out, input: strings.NewReader(answer)})
				if err == nil || !strings.Contains(err.Error(), "cancelled") {
					t.Fatalf("%v", err)
				}
				if !strings.Contains(out.String(), "target \"prod\"") || strings.Contains(out.String(), "secret") {
					t.Fatal(out.String())
				}
			})
		}
	}
}

func TestConfirmationSelectionAndBypass(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		answer    string
		protected bool
		prompt    bool
	}{
		{"accept", []string{"apply"}, "yes\n", true, true},
		{"explicit target", []string{"-target", "prod", "apply"}, "YES\n", true, true},
		{"ci", []string{"-yes", "apply"}, "", true, false},
		{"unprotected", []string{"apply"}, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.MkdirAll("database/objects", 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("database/release.json", []byte(`{"version":"0","objects":[]}`), 0600); err != nil {
				t.Fatal(err)
			}
			flag := "false"
			if tc.protected {
				flag = "true"
			}
			writeConfigFixture(t, "saxbase.yaml", "default_target: prod\ntargets: {prod: {connection_env: DSN, require_confirmation: "+flag+"}}")
			var out, diagnostics bytes.Buffer
			opened := false
			err := run(context.Background(), tc.args, env(map[string]string{"DSN": "secret"}), &out,
				func(migrations.Config) (migrations.Engine, error) { opened = true; return &fakeEngine{}, nil }, func(string) (objects.Engine, error) { return &fakeObjects{}, nil },
				runEnvironment{diagnostics: &diagnostics, input: strings.NewReader(tc.answer)})
			if !opened || (err == nil && tc.prompt) {
				t.Fatalf("%v", err)
			}
			if strings.Contains(diagnostics.String(), "Type yes") != tc.prompt {
				t.Fatal(diagnostics.String())
			}
			if strings.Contains(out.String(), "Type yes") {
				t.Fatal("prompt leaked to stdout")
			}
		})
	}
}

func TestConfirmationCancellationAndUnavailableInput(t *testing.T) {
	if err := confirmWrite(context.Background(), "prod", "up", nil, io.Discard); err == nil {
		t.Fatal("missing input accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() { done <- confirmWrite(ctx, "prod", "up", reader, io.Discard) }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

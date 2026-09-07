package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

func TestProtectedWritesRejectBeforeDatabaseAccess(t *testing.T) {
	for _, command := range [][]string{{"migration", "up"}, {"migration", "down"}, {"apply"}, {"objects", "apply"}, {"rollback", "30"}} {
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
		{"accept", []string{"migration", "up"}, "yes\n", true, true},
		{"explicit target", []string{"-target", "prod", "migration", "up"}, "YES\n", true, true},
		{"ci", []string{"-yes", "migration", "up"}, "", true, false},
		{"read only", []string{"migration", "version"}, "", true, false},
		{"unprotected", []string{"migration", "up"}, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			flag := "false"
			if tc.protected {
				flag = "true"
			}
			writeConfigFixture(t, "saxbase.yaml", "default_target: prod\ntargets: {prod: {connection_env: DSN, require_confirmation: "+flag+"}}")
			var out, diagnostics bytes.Buffer
			opened := false
			err := run(context.Background(), tc.args, env(map[string]string{"DSN": "secret"}), &out,
				func(migrations.Config) (migrations.Engine, error) { opened = true; return &fakeEngine{}, nil }, nil,
				runEnvironment{diagnostics: &diagnostics, input: strings.NewReader(tc.answer)})
			if err != nil || !opened {
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

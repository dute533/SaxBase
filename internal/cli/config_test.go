package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

func TestTargetsAndDotEnv(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		values        map[string]string
		want, failure string
	}{
		{name: "default", args: []string{"version"}, want: "local-secret"},
		{name: "explicit", args: []string{"-target", "prod", "version"}, want: "prod-secret"},
		{name: "environment wins", args: []string{"version"}, values: map[string]string{"LOCAL_DSN": "override"}, want: "override"},
		{name: "empty environment blocks fallback", args: []string{"version"}, values: map[string]string{"LOCAL_DSN": ""}, failure: "empty or missing"},
		{name: "unknown", args: []string{"-target", "typo", "up"}, failure: "unknown database target"},
		{name: "missing secret", args: []string{"-target", "acc", "up"}, values: map[string]string{"GOOSE_DBSTRING": "wrong-db"}, failure: "empty or missing"},
		{name: "positional conflict", args: []string{"mssql", "other-secret", "up"}, failure: "cannot combine"},
		{name: "missing config", args: []string{"-config", "missing.yaml", "up"}, failure: "open target config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeConfigFixture(t, "saxbase.yaml", "default_target: local\ntargets:\n  local:\n    connection_env: LOCAL_DSN\n  prod:\n    connection_env: PROD_DSN\n  acc:\n    connection_env: ACC_DSN\n")
			writeConfigFixture(t, ".env", "LOCAL_DSN='local-secret'\nPROD_DSN=prod-secret\n")
			var out bytes.Buffer
			opened := false
			err := RunWithLookup(context.Background(), tc.args, func(k string) (string, bool) { v, ok := tc.values[k]; return v, ok }, &out, &out, func(cfg migrations.Config) (migrations.Engine, error) {
				opened = true
				if tc.failure != "" {
					t.Fatal("opened database on invalid configuration")
				}
				if cfg.DSN != tc.want {
					t.Fatal("wrong target connection")
				}
				return &fakeEngine{}, nil
			})
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("error: %v", err)
				}
			} else if err != nil || !opened || !strings.Contains(out.String(), "Target: ") {
				t.Fatalf("%v %s", err, out.String())
			}
			if strings.Contains(out.String(), "secret") {
				t.Fatal("output leaked connection")
			}
		})
	}
}

func writeConfigFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigValidation(t *testing.T) {
	for _, content := range []string{
		"default_target: local\ntargets: {local: {connection_env: SECRET, typo: value}}",
		"default_target: local\ndefault_target: prod\ntargets: {}",
		"targets: {}",
		"default_target: local\ntargets: {local: {}}",
		"default_target: local\ntargets: {}\n---\ntargets: {}",
	} {
		t.Run(content, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeConfigFixture(t, "saxbase.yaml", content)
			err := Run(context.Background(), []string{"up"}, env(nil), &bytes.Buffer{}, func(migrations.Config) (migrations.Engine, error) { t.Fatal("opened database"); return nil, nil })
			if err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}

func TestDotEnvLegacyAndLocalCommands(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfigFixture(t, ".env", "GOOSE_DBSTRING='legacy-secret'\n")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"version"}, env(nil), &out, func(cfg migrations.Config) (migrations.Engine, error) {
		if cfg.DSN != "legacy-secret" {
			t.Fatal("dotenv not loaded")
		}
		return &fakeEngine{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	writeConfigFixture(t, "saxbase.yaml", "default_target: prod\ntargets: {prod: {connection_env: MISSING}}")
	if err := os.Mkdir("objects", 0700); err != nil {
		t.Fatal(err)
	}
	writeConfigFixture(t, "objects/view.sql", "SELECT 1;")
	if _, err := committedFixture(t, "."); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"-objects-dir", "objects", "-manifest", "release.json", "release", "create", "0"}, env(nil), &out, nil); err != nil {
		t.Fatal(err)
	}
	writeConfigFixture(t, ".env", "PASSWORD='do-not-print\n")
	err := Run(context.Background(), []string{"up"}, env(nil), &out, nil)
	if err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatalf("unsafe dotenv error: %v", err)
	}
	if err := Run(context.Background(), []string{"-h"}, env(nil), &out, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTargetReleaseCommandsKeepOutputUsable(t *testing.T) {
	for _, command := range [][]string{{"release", "history"}, {"release", "show", "30"}, {"release", "current"}, {"release", "rollbacks"}} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeConfigFixture(t, "team.yaml", "default_target: local\ntargets: {local: {connection_env: LOCAL_DSN}}")
			var out, diagnostics bytes.Buffer
			opened := false
			lookup := func(k string) (string, bool) { return "test-secret", k == "LOCAL_DSN" }
			err := run(context.Background(), append([]string{"-config", "team.yaml"}, command...), env(map[string]string{"LOCAL_DSN": "test-secret"}), &out,
				func(cfg migrations.Config) (migrations.Engine, error) {
					if cfg.DSN != "test-secret" {
						t.Fatal("wrong migration connection")
					}
					return &fakeEngine{}, nil
				},
				func(dsn string) (objects.Engine, error) {
					opened = true
					if dsn != "test-secret" {
						t.Fatal("wrong object connection")
					}
					return &fakeObjects{}, nil
				},
				runEnvironment{lookup: lookup, diagnostics: &diagnostics})
			if err != nil || !opened {
				t.Fatalf("%v", err)
			}
			if diagnostics.String() != "Target: local\n" || strings.Contains(out.String(), "Target:") {
				t.Fatal("target output mixed with command output")
			}
			if command[1] == "show" || command[1] == "rollbacks" {
				if !json.Valid(out.Bytes()) {
					t.Fatal("invalid JSON output")
				}
			}
		})
	}
}

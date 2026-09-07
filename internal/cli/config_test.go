package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"saxbase/internal/migrations"
)

func TestTargetsAndDotEnv(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		values        map[string]string
		want, failure string
	}{
		{name: "default", args: []string{"migration", "up"}, want: "local-secret"},
		{name: "explicit", args: []string{"-target", "prod", "migration", "up"}, want: "prod-secret"},
		{name: "environment wins", args: []string{"migration", "up"}, values: map[string]string{"LOCAL_DSN": "override"}, want: "override"},
		{name: "empty environment blocks fallback", args: []string{"migration", "up"}, values: map[string]string{"LOCAL_DSN": ""}, failure: "empty or missing"},
		{name: "unknown", args: []string{"-target", "typo", "migration", "up"}, failure: "unknown database target"},
		{name: "missing secret", args: []string{"-target", "acc", "migration", "up"}, values: map[string]string{"GOOSE_DBSTRING": "wrong-db"}, failure: "empty or missing"},
		{name: "positional conflict", args: []string{"mssql", "other-secret", "migration", "up"}, failure: "cannot combine"},
		{name: "missing config", args: []string{"-config", "missing.yaml", "migration", "up"}, failure: "open target config"},
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
			err := Run(context.Background(), []string{"migration", "up"}, env(nil), &bytes.Buffer{}, func(migrations.Config) (migrations.Engine, error) { t.Fatal("opened database"); return nil, nil })
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
	if err := Run(context.Background(), []string{"migration", "up"}, env(nil), &out, func(cfg migrations.Config) (migrations.Engine, error) {
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
	err := Run(context.Background(), []string{"migration", "up"}, env(nil), &out, nil)
	if err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatalf("unsafe dotenv error: %v", err)
	}
	if err := Run(context.Background(), []string{"-h"}, env(nil), &out, nil); err != nil {
		t.Fatal(err)
	}
}

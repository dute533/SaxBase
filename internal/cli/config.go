package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"go.yaml.in/yaml/v3"
	"saxbase/internal/migrations"
)

type targetConfig struct {
	DefaultTarget string `yaml:"default_target"`
	Targets       map[string]struct {
		ConnectionEnv       string `yaml:"connection_env"`
		RequireConfirmation bool   `yaml:"require_confirmation"`
	} `yaml:"targets"`
}

func loadDotEnv(getenv func(string) string, lookup ...func(string) (string, bool)) (func(string) string, error) {
	values, err := godotenv.Read(".env")
	if errors.Is(err, os.ErrNotExist) {
		return getenv, nil
	}
	if err != nil {
		return nil, errors.New("cannot read .env: check file permissions and dotenv syntax")
	}
	return func(key string) string {
		if len(lookup) > 0 {
			if value, exists := lookup[0](key); exists {
				return value
			}
		} else if value := getenv(key); value != "" {
			return value
		}
		return values[key]
	}, nil
}

func selectTarget(filename, target string, positional bool, getenv func(string) string, cfg *migrations.Config, out io.Writer, confirm func(string) error) error {
	explicit := filename != ""
	if filename == "" {
		filename = "saxbase.yaml"
	}
	file, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) && !explicit && target == "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open target config: %w", err)
	}
	defer file.Close()
	var config targetConfig
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return errors.New("invalid target config: check YAML syntax and supported fields")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("target config must contain exactly one document")
	}
	if positional {
		return errors.New("cannot combine target configuration with a positional connection string")
	}
	if target == "" {
		target = config.DefaultTarget
	}
	if target == "" {
		return errors.New("select -target or set default_target in the config")
	}
	entry, ok := config.Targets[target]
	if !ok {
		return fmt.Errorf("unknown database target %q", target)
	}
	if strings.TrimSpace(entry.ConnectionEnv) == "" {
		return fmt.Errorf("target %q requires connection_env", target)
	}
	cfg.DSN = getenv(entry.ConnectionEnv)
	if strings.TrimSpace(cfg.DSN) == "" {
		return fmt.Errorf("target %q: environment variable %q is empty or missing", target, entry.ConnectionEnv)
	}
	_, err = fmt.Fprintf(out, "Target: %s\n", target)
	if err != nil {
		return err
	}
	if entry.RequireConfirmation {
		return confirm(target)
	}
	return nil
}

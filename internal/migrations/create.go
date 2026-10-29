package migrations

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var migrationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

const sqlMigrationTemplate = `-- +goose Up
-- Write the forward migration here.

-- +goose Down
-- Write the rollback migration here.
`

// Create creates the next sequential Goose SQL migration in dir.
func Create(dir, name string) (string, error) {
	if !migrationNamePattern.MatchString(name) {
		return "", errors.New("migration name must use lowercase letters, numbers, and single underscores, starting with a letter")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read migration directory: %w", err)
	}
	seen := make(map[int64]string)
	var latest int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		ext := filepath.Ext(filename)
		if ext != ".sql" && ext != ".go" {
			continue
		}
		versionText, _, ok := strings.Cut(filename, "_")
		if !ok {
			continue
		}
		version, parseErr := strconv.ParseInt(versionText, 10, 64)
		if parseErr != nil || version < 1 {
			continue
		}
		if previous, ok := seen[version]; ok {
			return "", fmt.Errorf("duplicate migration version %d in %s and %s", version, previous, filename)
		}
		seen[version] = filename
		if version > latest {
			latest = version
		}
	}
	if latest == math.MaxInt64 {
		return "", errors.New("cannot create a migration after the maximum version")
	}
	filename := fmt.Sprintf("%05d_%s.sql", latest+1, name)
	path := filepath.Join(dir, filename)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("create migration: %w", err)
	}
	_, writeErr := file.WriteString(sqlMigrationTemplate)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return "", fmt.Errorf("write migration: %w", writeErr)
	}
	return path, nil
}

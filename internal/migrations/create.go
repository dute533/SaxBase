package migrations

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const sqlMigrationTemplate = `-- +goose Up
-- Write the forward migration here.

-- +goose Down
-- Write the rollback migration here.
`

// Create creates the next sequential Goose SQL migration in dir.
func Create(dir, name string) (string, error) {
	name = normalizeName(name)
	if name == "" {
		return "", errors.New("migration name must contain at least one letter or number")
	}
	versions, err := Versions(dir)
	if err != nil {
		return "", err
	}
	var latest int64
	if len(versions) > 0 {
		latest = versions[len(versions)-1]
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

func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var result strings.Builder
	separator := false
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if separator && result.Len() > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(r)
			separator = false
		} else {
			separator = true
		}
	}
	normalized := strings.Trim(result.String(), "_")
	if normalized != "" && normalized[0] >= '0' && normalized[0] <= '9' {
		normalized = "migration_" + normalized
	}
	return normalized
}

// Versions returns the ordered migration versions present in dir.
func Versions(dir string) ([]int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}
	seen := make(map[int64]string)
	var versions []int64
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
			return nil, fmt.Errorf("duplicate migration version %d in %s and %s", version, previous, filename)
		}
		seen[version] = filename
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions, nil
}

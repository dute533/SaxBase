// Package objects deploys complete SQL object definitions independently of Goose.
package objects

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"saxbase/internal/migrations"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

type File struct{ Path, SQL, Checksum, Commit string }
type Status struct{ Path, State, Checksum string }
type Engine interface {
	Deploy(context.Context, []File, int64, int64, migrations.Engine, func(context.Context) error) ([]Status, error)
	Apply(context.Context, []File) ([]Status, error)
	ApplyRelease(context.Context, []File, int64, int64) ([]Status, error)
	History(context.Context) ([]Release, error)
	Snapshot(context.Context, string) (Snapshot, error)
	Rollback(context.Context, Snapshot, Snapshot, migrations.Engine) (Rollback, error)
	Rollbacks(context.Context) ([]Rollback, error)
	Current(context.Context) (string, error)
	Inspect(context.Context) (Inspection, error)
	Status(context.Context, []File) ([]Status, error)
	Close() error
}

type Inspection struct {
	Current   string
	History   []Release
	Rollbacks []Rollback
}

type Release struct {
	Version       string    `json:"version"`
	SchemaVersion int64     `json:"schema_version"`
	Revision      int64     `json:"revision"`
	DeployedAt    time.Time `json:"deployed_at"`
	ObjectCount   int       `json:"object_count"`
	Fingerprint   string    `json:"fingerprint"`
}

type SnapshotObject struct {
	Path     string `json:"path"`
	Checksum string `json:"sha256"`
	SQL      string `json:"sql"`
}

// Snapshot is an in-memory release file set used by rollback. Database lookups
// populate only Release metadata; SQL is resolved from Git by the caller.
type Snapshot struct {
	Release
	Objects []SnapshotObject `json:"-"`
}

// Scan hashes exact file bytes and orders definitions by relative slash path.
func Scan(root string) ([]File, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("object path %q is not a directory", root)
	}
	var files []File
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("object symlink is not supported: %s", path)
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".sql") {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("object is not a regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(data)) == "" {
			return fmt.Errorf("empty object file: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if len(utf16.Encode([]rune(rel))) > 450 {
			return fmt.Errorf("object path exceeds 450 UTF-16 units: %s", rel)
		}
		files = append(files, File{Path: rel, SQL: string(data), Checksum: fmt.Sprintf("%x", sha256.Sum256(data))})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func compare(files []File, deployed map[string]string) []Status {
	result := make([]Status, 0, len(files))
	seen := make(map[string]bool)
	for _, file := range files {
		state := "new"
		if sum, exists := deployed[file.Path]; exists {
			state = "changed"
			if sum == file.Checksum {
				state = "unchanged"
			}
		}
		result = append(result, Status{Path: file.Path, State: state, Checksum: file.Checksum})
		seen[file.Path] = true
	}
	var missing []Status
	for path, sum := range deployed {
		if !seen[path] {
			missing = append(missing, Status{Path: path, State: "missing", Checksum: sum})
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Path < missing[j].Path })
	return append(result, missing...)
}

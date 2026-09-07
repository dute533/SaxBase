package releases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"saxbase/internal/objects"
)

type SyncResult struct {
	PreviousVersion, Version string
	Added, Updated           []string
}

// Sync refreshes an existing local manifest without changing existing order.
// All validation completes before an atomic replacement in the same directory.
func Sync(filename string, files []objects.File, version string) (SyncResult, error) {
	var result SyncResult
	info, err := os.Lstat(filename)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, fmt.Errorf("manifest must be a regular file: %s", filename)
	}
	m, err := Load(filename)
	if err != nil {
		return result, err
	}
	result.PreviousVersion = m.Version
	if version != "" {
		m.Version = version
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return result, err
	}
	result.Version = m.Version
	// Validate paths and commit references independently of the stale manifest.
	if _, err := New(m.Version, files); err != nil {
		return result, err
	}
	byPath := make(map[string]objects.File, len(files))
	for _, file := range files {
		byPath[file.Path] = file
	}
	for i, object := range m.Objects {
		file, ok := byPath[object.Path]
		if !ok {
			return result, fmt.Errorf("manifest object missing locally: %s; sync does not remove objects", object.Path)
		}
		if object.Commit != file.Commit {
			m.Objects[i].Commit = file.Commit
			result.Updated = append(result.Updated, file.Path)
		}
		delete(byPath, object.Path)
	}
	for path := range byPath {
		result.Added = append(result.Added, path)
	}
	sort.Strings(result.Added)
	for _, path := range result.Added {
		m.Objects = append(m.Objects, Object{Path: path, Commit: byPath[path].Commit})
	}
	if err := m.Validate(files); err != nil {
		return result, err
	}
	if len(result.Added) == 0 && len(result.Updated) == 0 && result.Version == result.PreviousVersion {
		return result, nil
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return result, err
	}
	temp, err := os.CreateTemp(filepath.Dir(filename), ".saxbase-sync-*")
	if err != nil {
		return result, err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		return result, err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		return result, err
	}
	if err := temp.Sync(); err != nil {
		return result, err
	}
	if err := temp.Close(); err != nil {
		return result, err
	}
	if err := os.Rename(temp.Name(), filename); err != nil {
		return result, err
	}
	return result, nil
}

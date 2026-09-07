// Package releases describes reproducible object states without floating-point versions.
package releases

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"saxbase/internal/objects"
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.([1-9][0-9]*))?$`)
var commitPattern = regexp.MustCompile(`^(latest|[0-9a-f]{40}|[0-9a-f]{64})$`)

type Version struct{ Schema, Revision int64 }

func ParseVersion(value string) (Version, error) {
	if !versionPattern.MatchString(value) {
		return Version{}, fmt.Errorf("invalid release version %q: expected 30 or 30.1", value)
	}
	parts := strings.Split(value, ".")
	schema, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Version{}, fmt.Errorf("schema version out of range: %w", err)
	}
	var revision int64
	if len(parts) == 2 {
		revision, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("release revision out of range: %w", err)
		}
	}
	return Version{Schema: schema, Revision: revision}, nil
}

type Object struct {
	Path   string `json:"path"`
	Commit string `json:"commit"`
	Delete bool   `json:"delete,omitempty"`
}
type Manifest struct {
	Version string   `json:"version"`
	Parent  string   `json:"parent,omitempty"`
	Objects []Object `json:"objects"`
}

func New(version string, files []objects.File) (Manifest, error) {
	m := Manifest{Version: version, Objects: make([]Object, 0, len(files))}
	for _, file := range files {
		m.Objects = append(m.Objects, Object{Path: file.Path, Commit: file.Commit})
	}
	return m, m.Validate(files)
}

func Load(filename string) (Manifest, error) {
	file, err := os.Open(filename)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var m Manifest
	if err := decoder.Decode(&m); err != nil {
		return m, fmt.Errorf("decode manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return m, errors.New("manifest must contain exactly one JSON object")
	}
	return m, m.check()
}

func (m Manifest) check() error {
	if _, err := ParseVersion(m.Version); err != nil {
		return err
	}
	if m.Objects == nil {
		return errors.New("manifest objects must be an array")
	}
	seen := make(map[string]bool)
	for _, object := range m.Objects {
		p := object.Path
		if p == "" || p == "." || strings.Contains(p, "\\") || path.IsAbs(p) || path.Clean(p) != p || strings.HasPrefix(p, "../") || !strings.EqualFold(path.Ext(p), ".sql") {
			return fmt.Errorf("invalid object path %q", p)
		}
		if seen[p] {
			return fmt.Errorf("duplicate object path %q", p)
		}
		seen[p] = true
		if !commitPattern.MatchString(object.Commit) {
			return fmt.Errorf("invalid commit reference for %s: use a full Git commit hash or latest", p)
		}
		if object.Delete && object.Commit == "latest" {
			return fmt.Errorf("deleted object %s requires a historical Git commit", p)
		}
	}
	return nil
}

// Validate requires the resolved file set to match the manifest references exactly.
func (m Manifest) Validate(files []objects.File) error {
	if err := m.check(); err != nil {
		return err
	}
	expected := make(map[string]string, len(m.Objects))
	for _, object := range m.Objects {
		expected[object.Path] = object.Commit
	}
	for _, file := range files {
		sum, ok := expected[file.Path]
		if !ok {
			if m.Parent != "" {
				continue
			}
			return fmt.Errorf("object not in manifest: %s", file.Path)
		}
		if sum != file.Commit {
			return fmt.Errorf("commit mismatch: %s", file.Path)
		}
		delete(expected, file.Path)
	}
	for _, object := range m.Objects {
		if _, ok := expected[object.Path]; ok {
			return fmt.Errorf("manifest object missing from resolved files: %s", object.Path)
		}
	}
	return nil
}

// Write creates a new file exclusively; existing manifests are never overwritten.
func (m Manifest) Write(filename string) error {
	if err := m.check(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	return errors.Join(writeErr, file.Close())
}

// WriteReplace atomically replaces an existing manifest after validating it.
func (m Manifest) WriteReplace(filename string) error {
	if err := m.check(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	info, err := os.Stat(filename)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(filename), ".saxbase-manifest-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, filename)
}

// OrderedFiles validates the complete file set and returns manifest array order.
func (m Manifest) OrderedFiles(files []objects.File) ([]objects.File, error) {
	if err := m.Validate(files); err != nil {
		return nil, err
	}
	byPath := make(map[string]objects.File, len(files))
	for _, file := range files {
		byPath[file.Path] = file
	}
	ordered := make([]objects.File, 0, len(files))
	for _, object := range m.Objects {
		ordered = append(ordered, byPath[object.Path])
	}
	return ordered, nil
}

// NewDelta creates a manifest containing only objects whose committed
// definitions differ from the parent state. Removed objects retain their last
// commit so SaxBase can identify and drop the database object during apply.
func NewDelta(version, parent string, current, previous []objects.File) (Manifest, error) {
	old := make(map[string]objects.File, len(previous))
	for _, file := range previous {
		old[file.Path] = file
	}
	m := Manifest{Version: version, Parent: parent, Objects: make([]Object, 0)}
	for _, file := range current {
		before, ok := old[file.Path]
		if !ok || before.Commit != file.Commit || before.Checksum != file.Checksum {
			m.Objects = append(m.Objects, Object{Path: file.Path, Commit: file.Commit})
		}
		delete(old, file.Path)
	}
	removed := make([]string, 0, len(old))
	for path := range old {
		removed = append(removed, path)
	}
	sort.Strings(removed)
	for _, path := range removed {
		file := old[path]
		m.Objects = append(m.Objects, Object{Path: path, Commit: file.Commit, Delete: true})
	}
	if err := m.check(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

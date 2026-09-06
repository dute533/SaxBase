// Package releases describes reproducible object states without floating-point versions.
package releases

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"saxbase/internal/objects"
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.([1-9][0-9]*))?$`)
var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

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
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Format  int      `json:"format"`
	Version string   `json:"version"`
	Objects []Object `json:"objects"`
}

func New(version string, files []objects.File) (Manifest, error) {
	m := Manifest{Format: 1, Version: version, Objects: make([]Object, 0, len(files))}
	for _, file := range files {
		m.Objects = append(m.Objects, Object{Path: file.Path, SHA256: file.Checksum})
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
	if m.Format != 1 {
		return fmt.Errorf("unsupported manifest format %d", m.Format)
	}
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
		if !checksumPattern.MatchString(object.SHA256) {
			return fmt.Errorf("invalid SHA-256 for %s", p)
		}
	}
	return nil
}

// Validate requires the complete local file set to match the manifest exactly.
func (m Manifest) Validate(files []objects.File) error {
	if err := m.check(); err != nil {
		return err
	}
	expected := make(map[string]string, len(m.Objects))
	for _, object := range m.Objects {
		expected[object.Path] = object.SHA256
	}
	for _, file := range files {
		sum, ok := expected[file.Path]
		if !ok {
			return fmt.Errorf("object not in manifest: %s", file.Path)
		}
		if sum != file.Checksum {
			return fmt.Errorf("checksum mismatch: %s", file.Path)
		}
		delete(expected, file.Path)
	}
	for _, object := range m.Objects {
		if _, ok := expected[object.Path]; ok {
			return fmt.Errorf("manifest object missing locally: %s", object.Path)
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

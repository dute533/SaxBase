package releases

import (
	"context"
	"fmt"
	"path/filepath"

	"saxbase/internal/objects"
)

// ResolveState reconstructs the complete object set represented by a manifest
// and its parent chain, preserving each manifest's explicit order.
func ResolveState(ctx context.Context, filename, objectDir string) ([]objects.File, error) {
	return resolveState(ctx, filename, objectDir, map[string]bool{})
}

func resolveState(ctx context.Context, filename, objectDir string, seen map[string]bool) ([]objects.File, error) {
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	if seen[absolute] {
		return nil, fmt.Errorf("manifest parent cycle includes %s", filename)
	}
	seen[absolute] = true
	defer delete(seen, absolute)
	m, err := Load(filename)
	if err != nil {
		return nil, err
	}
	var state []objects.File
	if m.Parent != "" {
		parent := filepath.Join(filepath.Dir(filename), filepath.FromSlash(m.Parent))
		state, err = resolveState(ctx, parent, objectDir, seen)
		if err != nil {
			return nil, err
		}
	}
	delta, err := m.Resolve(ctx, objectDir)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]int, len(state))
	for i, file := range state {
		byPath[file.Path] = i
	}
	for _, file := range delta {
		if i, ok := byPath[file.Path]; ok {
			state = append(state[:i], state[i+1:]...)
			for path, index := range byPath {
				if index > i {
					byPath[path] = index - 1
				}
			}
			delete(byPath, file.Path)
		}
		if !file.Delete {
			byPath[file.Path] = len(state)
			state = append(state, file)
		}
	}
	return state, nil
}

// ParentVersion returns the version named by a manifest's parent reference.
func ParentVersion(filename string, manifest Manifest) (string, error) {
	if manifest.Parent == "" {
		return "", nil
	}
	parent, err := Load(filepath.Join(filepath.Dir(filename), filepath.FromSlash(manifest.Parent)))
	if err != nil {
		return "", fmt.Errorf("load parent manifest: %w", err)
	}
	return parent.Version, nil
}

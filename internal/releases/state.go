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
	return ResolveStateVersion(ctx, filename, objectDir, "")
}

// ResolveStateVersion reconstructs a specific release from a manifest file
// containing release history. An empty version selects the newest release.
func ResolveStateVersion(ctx context.Context, filename, objectDir, version string) ([]objects.File, error) {
	m, err := LoadVersion(filename, version)
	if err != nil {
		return nil, err
	}
	return resolveManifestState(ctx, filename, objectDir, m, map[string]bool{})
}

func resolveManifestState(ctx context.Context, filename, objectDir string, m Manifest, seen map[string]bool) ([]objects.File, error) {
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	key := absolute + "#" + m.Version
	if seen[key] {
		return nil, fmt.Errorf("manifest parent cycle includes %s release %s", filename, m.Version)
	}
	seen[key] = true
	defer delete(seen, key)
	var state []objects.File
	if m.Parent != "" {
		if _, parseErr := ParseVersion(m.Parent); parseErr == nil {
			parent, loadErr := LoadVersion(filename, m.Parent)
			if loadErr != nil {
				return nil, fmt.Errorf("load parent release: %w", loadErr)
			}
			state, err = resolveManifestState(ctx, filename, objectDir, parent, seen)
		} else {
			parent := filepath.Join(filepath.Dir(filename), filepath.FromSlash(m.Parent))
			parentManifest, loadErr := Load(parent)
			if loadErr != nil {
				return nil, loadErr
			}
			state, err = resolveManifestState(ctx, parent, objectDir, parentManifest, seen)
		}
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
	if _, parseErr := ParseVersion(manifest.Parent); parseErr == nil {
		if _, err := LoadVersion(filename, manifest.Parent); err != nil {
			return "", fmt.Errorf("load parent release: %w", err)
		}
		return manifest.Parent, nil
	}
	parent, err := Load(filepath.Join(filepath.Dir(filename), filepath.FromSlash(manifest.Parent)))
	if err != nil {
		return "", fmt.Errorf("load parent manifest: %w", err)
	}
	return parent.Version, nil
}

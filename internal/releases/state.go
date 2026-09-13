package releases

import (
	"context"
	"fmt"

	"saxbase/internal/objects"
)

// ResolveState reconstructs the complete object set represented by the history.
func ResolveState(ctx context.Context, filename, objectDir string) ([]objects.File, error) {
	return ResolveStateVersion(ctx, filename, objectDir, "")
}

// ResolveStateVersion reconstructs a specific release from a manifest file
// containing release history. An empty version selects the newest release.
func ResolveStateVersion(ctx context.Context, filename, objectDir, version string) ([]objects.File, error) {
	history, err := LoadAll(filename)
	if err != nil {
		return nil, err
	}
	var state []objects.File
	for _, release := range history {
		delta, err := release.Resolve(ctx, objectDir)
		if err != nil {
			return nil, fmt.Errorf("resolve release %s: %w", release.Version, err)
		}
		state = ApplyDelta(state, delta)
		if version != "" && release.Version == version {
			return state, nil
		}
	}
	if version != "" {
		return nil, fmt.Errorf("release %s not found in %s", version, filename)
	}
	return state, nil
}

// ApplyDelta returns the complete object state produced by applying an ordered
// release delta to its preceding complete state.
func ApplyDelta(state, delta []objects.File) []objects.File {
	state = append([]objects.File(nil), state...)
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
	return state
}

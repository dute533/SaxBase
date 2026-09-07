package releases

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"saxbase/internal/objects"
)

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "--no-replace-objects"}, args...)...)
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: ensure the repository and referenced commits are available: %w", args[0], err)
	}
	return data, nil
}

func repository(ctx context.Context, dir string) (string, error) {
	data, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(string(data)), err
}

// Resolve reads regular SQL blobs in manifest order. Full commit references are
// loaded from Git; latest reads the working-tree file and is intentionally not
// reproducible. It never checks out files, invokes filters, or fetches remotely.
func (m Manifest) Resolve(ctx context.Context, dir string) ([]objects.File, error) {
	if err := m.check(); err != nil {
		return nil, err
	}
	root, repoErr := repository(ctx, dir)
	if repoErr != nil {
		// Object scans may use a subdirectory that is absent in a repository
		// containing historical manifest files. Resolve Git paths from cwd in
		// that case so missing object directories do not hide the repository.
		root, repoErr = repository(ctx, ".")
		if repoErr != nil {
			root = ""
		}
	}
	files := make([]objects.File, 0, len(m.Objects))
	for _, object := range m.Objects {
		if object.Commit == "latest" {
			var data []byte
			var err error
			candidates := []string{filepath.Join(dir, filepath.FromSlash(object.Path))}
			if root != "" {
				candidates = append([]string{filepath.Join(root, filepath.FromSlash(object.Path))}, candidates...)
			}
			for _, filename := range candidates {
				info, statErr := os.Lstat(filename)
				if statErr != nil {
					err = statErr
					continue
				}
				if !info.Mode().IsRegular() {
					err = fmt.Errorf("%s is not a regular file", object.Path)
					break
				}
				data, err = os.ReadFile(filename)
				if err == nil {
					break
				}
			}
			if err != nil {
				return nil, fmt.Errorf("read latest %s: %w", object.Path, err)
			}
			if !utf8.Valid(data) || strings.TrimSpace(string(data)) == "" || len(utf16.Encode([]rune(object.Path))) > 450 {
				return nil, fmt.Errorf("%s must be nonempty UTF-8 SQL", object.Path)
			}
			files = append(files, objects.File{Path: object.Path, Commit: object.Commit, Delete: object.Delete, SQL: string(data), Checksum: fmt.Sprintf("%x", sha256.Sum256(data))})
			continue
		}
		if root == "" {
			return nil, fmt.Errorf("%s requires a Git repository; use commit latest for working-tree files", object.Path)
		}
		kind, err := git(ctx, root, "cat-file", "-t", object.Commit)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", object.Path, err)
		}
		if strings.TrimSpace(string(kind)) != "commit" {
			return nil, fmt.Errorf("%s: reference must identify a commit", object.Path)
		}
		entry, err := git(ctx, root, "ls-tree", "-z", object.Commit, "--", ":(literal)"+object.Path)
		if err != nil {
			return nil, err
		}
		parts := strings.SplitN(strings.TrimSuffix(string(entry), "\x00"), "\t", 2)
		if len(parts) != 2 || parts[1] != object.Path || !(strings.HasPrefix(parts[0], "100644 blob ") || strings.HasPrefix(parts[0], "100755 blob ")) {
			return nil, fmt.Errorf("%s is not a regular file in commit %s", object.Path, object.Commit)
		}
		data, err := git(ctx, root, "cat-file", "blob", object.Commit+":"+object.Path)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(data) || strings.TrimSpace(string(data)) == "" || len(utf16.Encode([]rune(object.Path))) > 450 {
			return nil, fmt.Errorf("%s must be nonempty UTF-8 SQL with a path of at most 450 UTF-16 units", object.Path)
		}
		files = append(files, objects.File{Path: object.Path, Commit: object.Commit, Delete: object.Delete, SQL: string(data), Checksum: fmt.Sprintf("%x", sha256.Sum256(data))})
	}
	return files, nil
}

// CommittedFiles captures the current SQL tree. In Git it pins each file to its
// latest touching commit and rejects dirty SQL; outside Git it uses latest.
func CommittedFiles(ctx context.Context, dir string) ([]objects.File, error) {
	root, err := repository(ctx, dir)
	if err != nil {
		files, scanErr := objects.Scan(dir)
		if scanErr != nil {
			return nil, scanErr
		}
		for i := range files {
			files[i].Commit = "latest"
		}
		m, manifestErr := New("0", files)
		if manifestErr != nil {
			return nil, manifestErr
		}
		if _, manifestErr = m.Resolve(ctx, dir); manifestErr != nil {
			return nil, manifestErr
		}
		return files, nil
	}
	files, err := objects.Scan(dir)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for i := range files {
		rel, err := filepath.Rel(root, filepath.Join(abs, filepath.FromSlash(files[i].Path)))
		if err != nil {
			return nil, err
		}
		files[i].Path = filepath.ToSlash(rel)
		commit, err := git(ctx, root, "log", "-1", "--format=%H", "HEAD", "--", ":(literal)"+files[i].Path)
		if err != nil {
			return nil, err
		}
		files[i].Commit = strings.TrimSpace(string(commit))
	}
	m, err := New("0", files)
	if err != nil {
		return nil, fmt.Errorf("commit SQL files before creating a release: %w", err)
	}
	committed, err := m.Resolve(ctx, root)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if files[i].SQL != committed[i].SQL {
			return nil, fmt.Errorf("%s has uncommitted changes; commit SQL before creating a release", files[i].Path)
		}
	}
	return files, nil
}

// WorkingFiles uses the same repository-relative identity as manifests while
// preserving standalone object deployment outside a Git working tree.
func WorkingFiles(ctx context.Context, dir string) ([]objects.File, error) {
	files, err := objects.Scan(dir)
	if err != nil {
		return nil, err
	}
	root, err := repository(ctx, dir)
	if err != nil {
		return files, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for i := range files {
		rel, err := filepath.Rel(root, filepath.Join(abs, filepath.FromSlash(files[i].Path)))
		if err != nil {
			return nil, err
		}
		files[i].Path = filepath.ToSlash(rel)
	}
	return files, nil
}

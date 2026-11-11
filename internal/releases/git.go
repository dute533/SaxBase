package releases

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
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

// UncommittedFiles returns repository-relative SQL object paths changed in the
// index or working tree. Outside Git there is no status to report.
func UncommittedFiles(ctx context.Context, dir string) ([]string, error) {
	root, err := repository(ctx, dir)
	if err != nil {
		return nil, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return nil, err
	}
	data, err := git(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames", "--", ":(literal)"+filepath.ToSlash(rel))
	if err != nil {
		return nil, err
	}
	var files []string
	for _, record := range strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00") {
		if len(record) < 4 {
			continue
		}
		path := filepath.ToSlash(record[3:])
		if strings.EqualFold(filepath.Ext(path), ".sql") {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files, nil
}

// Resolve reads regular SQL blobs from Git in manifest order. It never checks
// out files, invokes filters, or fetches remotely.
func (m Manifest) Resolve(ctx context.Context, dir string) ([]objects.File, error) {
	if err := m.check(); err != nil {
		return nil, err
	}
	if len(m.Objects) == 0 {
		return []objects.File{}, nil
	}
	root, repoErr := repository(ctx, dir)
	if repoErr != nil {
		// Object scans may use a subdirectory that is absent in a repository
		// containing historical manifest files. Resolve Git paths from cwd in
		// that case so missing object directories do not hide the repository.
		root, repoErr = repository(ctx, ".")
		if repoErr != nil {
			return nil, repoErr
		}
	}
	files := make([]objects.File, 0, len(m.Objects))
	for _, object := range m.Objects {
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

// CommittedFiles captures the current SQL tree, pins each file to its latest
// touching commit, and rejects dirty SQL.
func CommittedFiles(ctx context.Context, dir string) ([]objects.File, error) {
	root, err := repository(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("database releases require a Git repository: %w", err)
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
	m, err := New("0.0.0", 0, files)
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

// WorkingFiles uses repository-relative identity when Git is available.
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

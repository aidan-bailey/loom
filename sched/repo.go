package sched

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FindRepo finds the repository holding dir from git's own files, with no
// git run (smart scheduler spec §5.1): it walks up from dir, symlinks
// resolved, to the first directory with a .git. A .git directory is the
// repository's git dir; a .git file names a worktree's (or a submodule's)
// git dir on its gitdir: line, whose commondir file, when it has one, names
// the common dir. repo is the common dir, symlinks resolved, so every
// worktree of a repository has the same one; folder is dir relative to the
// directory holding the .git, and checkout is that directory, the working
// tree's top (advisor run context spec §5.1). With no .git up to the root,
// dir is its own repo and checkout, and folder is ".". A .git it can't
// read, or one naming nothing that resolves, is an error: the caller sizes
// the job without a signature.
func FindRepo(dir string) (repo, folder, checkout string, err error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	for top := real; ; {
		common, found, err := gitCommonDir(top)
		if err != nil {
			return "", "", "", err
		}
		if found {
			rel, err := filepath.Rel(top, real)
			if err != nil {
				return "", "", "", fmt.Errorf("place %s under %s: %w", real, top, err)
			}
			return common, rel, top, nil
		}
		parent := filepath.Dir(top)
		if parent == top {
			return real, ".", real, nil
		}
		top = parent
	}
}

// gitCommonDir reads top's .git and returns the common dir it leads to;
// found is false when top has no .git.
func gitCommonDir(top string) (common string, found bool, err error) {
	dotgit := filepath.Join(top, ".git")
	info, err := os.Stat(dotgit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("read %s: %w", dotgit, err)
	case info.IsDir():
		common = dotgit
	default:
		data, err := os.ReadFile(dotgit)
		if err != nil {
			return "", false, fmt.Errorf("read %s: %w", dotgit, err)
		}
		gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		if !ok {
			return "", false, fmt.Errorf("%s names no gitdir", dotgit)
		}
		gitdir = strings.TrimSpace(gitdir)
		if !filepath.IsAbs(gitdir) {
			gitdir = filepath.Join(top, gitdir)
		}
		common = gitdir
		data, err = os.ReadFile(filepath.Join(gitdir, "commondir"))
		switch {
		case err == nil:
			c := strings.TrimSpace(string(data))
			if !filepath.IsAbs(c) {
				c = filepath.Join(gitdir, c)
			}
			common = c
		case !errors.Is(err, fs.ErrNotExist):
			return "", false, fmt.Errorf("read %s: %w", filepath.Join(gitdir, "commondir"), err)
		}
	}
	resolved, err := filepath.EvalSymlinks(common)
	if err != nil {
		return "", false, fmt.Errorf("resolve the git dir %s: %w", common, err)
	}
	return resolved, true, nil
}

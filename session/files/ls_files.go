// Package files provides filesystem enumeration helpers for the file
// explorer overlay. It sits alongside session/git rather than inside
// it because the callers may operate on non-git roots (workspace
// terminals pointed at bare directories), and because these helpers
// are stateless path-only operations — session/git is organized
// around stateful GitWorktree lifecycles.
package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/git"
)

// listTimeout caps both the git ls-files subprocess and the walk
// fallback so a stalled filesystem (stuck NFS mount, cloud drive
// hiccup) can't freeze the TUI.
const listTimeout = 5 * time.Second

// ListResult describes the enumeration output.
//
// Paths are relative to Root, alphabetically sorted. FromGit
// distinguishes the two code paths so callers (and tests) can assert
// which route fired without inspecting logs.
type ListResult struct {
	Root    string
	Paths   []string
	FromGit bool
}

// List enumerates files under root. When root is a git repository it
// prefers `git ls-files --cached --others --exclude-standard -z`, which
// respects .gitignore while still surfacing newly-created untracked
// files — the workflow the file explorer targets. On a non-git root or
// when git exits with an error, it falls back to filepath.WalkDir,
// pruning .git directories and skipping symlinks.
//
// An empty root is rejected because the walk fallback would happily
// enumerate the current working directory, which is almost never what
// the caller wants.
//
// runner executes both git subprocesses (the repo probe and ls-files);
// pass nil for the default subprocess runner.
func List(root string, runner internalexec.Executor) (ListResult, error) {
	if root == "" {
		return ListResult{}, errors.New("files.List: root is empty")
	}
	if _, err := os.Stat(root); err != nil {
		return ListResult{}, fmt.Errorf("files.List: stat %q: %w", root, err)
	}

	if runner == nil {
		runner = internalexec.Default{}
	}
	if git.IsGitRepo(root, runner) {
		paths, err := listViaGit(root, runner)
		if err == nil {
			return ListResult{Root: root, Paths: paths, FromGit: true}, nil
		}
		// git ls-files failed inside a git repo — fall through to the
		// walker rather than returning an error. A freshly-init'd repo
		// with no commits returns non-zero exit on some git versions;
		// the walker still gives the user a usable listing.
	}

	paths, err := listViaWalk(root)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Root: root, Paths: paths, FromGit: false}, nil
}

// listViaGit runs git ls-files with NUL separation so paths with
// embedded whitespace (even newlines) round-trip cleanly.
func listViaGit(root string, runner internalexec.Executor) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()

	cmd := internalexec.GitCommand(ctx, root,
		"ls-files", "--cached", "--others", "--exclude-standard", "-z")
	out, err := runner.Output(cmd)
	if err != nil {
		return nil, err
	}

	raw := bytes.Split(out, []byte{0})
	paths := make([]string, 0, len(raw))
	for _, p := range raw {
		if len(p) == 0 {
			continue
		}
		paths = append(paths, string(p))
	}
	sort.Strings(paths)
	return paths, nil
}

// listViaWalk traverses root, pruning .git and skipping symlinks.
// Errors on individual entries are skipped (log + continue semantics
// via WalkDir's SkipDir return) so a permission-denied subtree does
// not abort the whole listing.
func listViaWalk(root string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()

	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// Inaccessible subtree — skip it rather than failing the whole walk.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		// Skip symlinks (both file- and directory-targeted) to avoid
		// following them out of the root tree.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		// filepath separator normalization: git ls-files always
		// returns forward-slash paths; match that here so callers
		// don't have to special-case Windows.
		rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

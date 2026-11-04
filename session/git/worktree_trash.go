package git

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// A kill's `git worktree remove -f` deletes the whole tree before it
// returns, a Rust target/ of several GB included, and the session's row
// spins in Deleting all that while. Cleanup instead renames a tree it can
// vouch for (linkedWorktreeOf) into the trash, prunes its registration,
// and leaves the delete to a background job (RemoveTrash, from core's
// deliverKill); a trash a stop left behind is purged at the next load
// (PurgeWorktreeTrash).

// TrashDir is where Cleanup moves a worktree it deletes: beside configDir's
// worktrees dir, where orphan discovery never looks, and on its filesystem,
// so the move is a rename. A rename that fails (another filesystem, a
// handle open on Windows) leaves the tree to `git worktree remove`.
func TrashDir(configDir string) (string, error) {
	wt, err := getWorktreeDirectory(configDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(wt), "worktree-trash"), nil
}

// Trashed returns where Cleanup moved the tree, "" when it moved none (it
// removed the tree with git, or never ran).
func (g *GitWorktree) Trashed() string {
	g.refMu.Lock()
	defer g.refMu.Unlock()
	return g.trashed
}

// readGitFile returns the admin dir a linked worktree's .git file names
// ("gitdir: <path>", relative to the tree under worktree.useRelativePaths),
// and false when worktreePath's .git is no such file.
func readGitFile(worktreePath string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		return "", false
	}
	rest, ok := strings.CutPrefix(string(data), "gitdir:")
	if !ok {
		return "", false
	}
	dir := strings.TrimSpace(rest)
	if dir == "" || strings.ContainsAny(dir, "\r\n") {
		return "", false
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(worktreePath, dir)
	}
	return filepath.Clean(dir), true
}

// linkedWorktreeOf reports whether worktreePath is registered as a linked
// worktree of the repository at repoPath, unlocked, from the files git
// keeps for it: its .git file names an admin dir under the repository's
// .git/worktrees, and that admin dir's gitdir file points back at the
// tree's .git. Reading them is what lets the move skip a subprocess. Any
// doubt (a repository whose .git is not a directory, a missing back
// pointer, a lock) reads false, and git removes the tree instead.
func linkedWorktreeOf(repoPath, worktreePath string) bool {
	admin, ok := readGitFile(worktreePath)
	if !ok || filepath.Base(filepath.Dir(admin)) != "worktrees" {
		return false
	}
	if _, err := os.Lstat(filepath.Join(admin, "locked")); err == nil {
		return false
	}
	back, err := os.ReadFile(filepath.Join(admin, "gitdir"))
	if err != nil {
		return false
	}
	backPath := strings.TrimSpace(string(back))
	if backPath == "" {
		return false
	}
	if !filepath.IsAbs(backPath) {
		backPath = filepath.Join(admin, backPath)
	}
	if same, err := sameFile(backPath, filepath.Join(worktreePath, ".git")); err != nil || !same {
		return false
	}
	repoGit := filepath.Join(repoPath, ".git")
	if fi, err := os.Lstat(repoGit); err != nil || !fi.IsDir() {
		return false
	}
	same, err := sameFile(filepath.Dir(filepath.Dir(admin)), repoGit)
	return err == nil && same
}

// moveToTrash renames the tree, when linkedWorktreeOf vouches for it, into
// TrashDir and records where it went (Trashed). It reports whether it
// moved the tree; when not, the caller removes it with git.
func (g *GitWorktree) moveToTrash() bool {
	if !linkedWorktreeOf(g.repoPath, g.worktreePath) {
		return false
	}
	root, err := TrashDir(g.configDir)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		log.For("git").Debug("worktree.trash_unavailable", "root", root, "err", err.Error())
		return false
	}
	dest := filepath.Join(root, fmt.Sprintf("%s.%x", filepath.Base(g.worktreePath), time.Now().UnixNano()))
	if err := os.Rename(g.worktreePath, dest); err != nil {
		log.For("git").Debug("worktree.trash_move_failed", "path", g.worktreePath, "err", err.Error())
		return false
	}
	g.refMu.Lock()
	g.trashed = dest
	g.refMu.Unlock()
	return true
}

// RemoveTrash deletes path, a tree Cleanup moved into the trash, making
// read-only directories (a Go module cache's) writable when a plain
// delete fails. Run off the model's goroutine: it takes as long as the
// tree is large.
func RemoveTrash(path string) error {
	if err := os.RemoveAll(path); err == nil {
		return nil
	}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			if info, err := d.Info(); err == nil {
				_ = os.Chmod(p, info.Mode().Perm()|0o700)
			}
		}
		return nil
	})
	return os.RemoveAll(path)
}

// HasWorktreeTrash reports whether configDir's worktree trash holds
// anything, so a load queues a purge (PurgeWorktreeTrash) only when there is
// something to purge.
func HasWorktreeTrash(configDir string) bool {
	root, err := TrashDir(configDir)
	if err != nil {
		return false
	}
	d, err := os.Open(root)
	if err != nil {
		return false
	}
	defer d.Close()
	names, _ := d.Readdirnames(1)
	return len(names) > 0
}

// PurgeWorktreeTrash removes every entry of configDir's worktree trash and
// returns how many it removed: trees whose background delete a stop or a
// crash cut short, which nothing else would retry. Everything there is a
// tree a kill already removed from git. Failures are logged at debug.
func PurgeWorktreeTrash(configDir string) int {
	root, err := TrashDir(configDir)
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		p := filepath.Join(root, e.Name())
		if err := RemoveTrash(p); err != nil {
			log.For("git").Debug("worktree.trash_purge_failed", "path", p, "err", err.Error())
			continue
		}
		removed++
	}
	return removed
}

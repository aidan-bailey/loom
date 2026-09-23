package git

import (
	"errors"
	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns the exit code rather than exiting, so its deferred
// cleanup runs.
func runTests(m *testing.M) int {
	_ = log.Initialize("", false)
	defer log.Close()
	// An empty ConfigDir resolves the worktrees dir (and a new worktree's
	// config) under LOOM_HOME: keep it off the developer's ~/.loom.
	defer testenv.MustIsolateLoomDirs()()
	return m.Run()
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s failed: %s", strings.Join(args, " "), string(out))
}

// setupTestRepoWithWorktree creates a real git repo and a linked worktree,
// returning the config dir (where worktrees/ lives), the repo dir, the
// worktree path, and the branch name.
func setupTestRepoWithWorktree(t *testing.T) (configDir, repoDir, worktreePath, branchName string) {
	t.Helper()
	tmpDir := t.TempDir()
	configDir = filepath.Join(tmpDir, "config")
	repoDir = filepath.Join(tmpDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0755))

	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0644))
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "init")

	branchName = "cleanup-test-branch"
	worktreePath = filepath.Join(configDir, "worktrees", branchName+"_fixture")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreePath), 0755))
	runGit(t, repoDir, "worktree", "add", "-b", branchName, worktreePath)

	return configDir, repoDir, worktreePath, branchName
}

func branchExists(t *testing.T, repoDir, branchName string) bool {
	t.Helper()
	out, _ := exec.Command("git", "-C", repoDir, "branch", "--list", branchName).Output()
	return strings.TrimSpace(string(out)) != ""
}

// TestCleanupWorktrees_DeletesBranch is the integration test for the fix:
// CleanupWorktrees must remove the worktree directory AND delete the branch.
// The pre-fix code tried `git branch -D` before removing the worktree
// registration, so deletion failed (worktree was still checked out) and the
// error was only logged — the branch leaked.
// TestWorktreeTitleSidecarPath verifies the sidecar lives next to the
// worktree directory (a sibling file), NOT inside it — putting it inside
// the work tree would pollute git status/diffs.
func TestWorktreeTitleSidecarPath(t *testing.T) {
	wt := "/cfg/worktrees/u/my-feature_18acb35cb8ad6e5a"
	got := WorktreeTitleSidecarPath(wt)
	assert.Equal(t, wt+".loom-title", got)
	assert.Equal(t, filepath.Dir(wt), filepath.Dir(got),
		"sidecar must sit beside the worktree dir, not inside it")
}

// TestSetup_WritesTitleSidecar is the M2a fix: Setup records the original
// instance title in a sidecar so orphan discovery can reconstruct the
// exact display title (and thus the exact tmux session name), which the
// lossy branch-name sanitization otherwise destroys.
func TestSetup_WritesTitleSidecar(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0755))
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0644))
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "init")

	const title = "My Mixed-Case Title"
	tree, _, err := NewGitWorktree(repoDir, title, configDir)
	require.NoError(t, err)
	require.NoError(t, tree.Setup())

	sidecar := WorktreeTitleSidecarPath(tree.GetWorktreePath())
	got, readErr := os.ReadFile(sidecar)
	require.NoError(t, readErr, "Setup must write the title sidecar")
	assert.Equal(t, title, string(got))

	// Cleanup must remove the sidecar so it doesn't leak.
	require.NoError(t, tree.Cleanup())
	_, statErr := os.Stat(sidecar)
	assert.True(t, os.IsNotExist(statErr), "Cleanup must remove the title sidecar")
}

func TestCleanupWorktrees_DeletesBranch(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)

	err := CleanupWorktrees(configDir, nil)
	assert.NoError(t, err)

	_, statErr := os.Stat(worktreePath)
	assert.True(t, os.IsNotExist(statErr), "worktree dir should be removed")

	assert.False(t, branchExists(t, repoDir, branchName),
		"branch %q should be deleted after cleanup", branchName)
}

// TestSetup_BranchDeletedExternallyReturnsErrBranchGone is the F10
// regression guard. When a paused instance's branch is deleted via
// `git branch -D` from outside the app AND no origin tracking branch
// exists, Setup must return a typed sentinel so Resume can surface a
// recovery hint instead of a generic "failed to setup git worktree".
// Before this fix the error was a plain `fmt.Errorf` string, not an
// errors.Is-compatible signal, so callers could only string-match.
func TestSetup_BranchDeletedExternallyReturnsErrBranchGone(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)

	// Simulate the post-pause pathological state: worktree removed
	// AND the branch deleted out-of-band (no origin remote).
	runGit(t, repoDir, "worktree", "remove", "-f", worktreePath)
	runGit(t, repoDir, "branch", "-D", branchName)
	require.False(t, branchExists(t, repoDir, branchName), "precondition: branch must be gone")

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "lost-session", branchName, "", true, configDir)
	err := gw.Setup()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBranchGone, "Setup must return ErrBranchGone sentinel when branch vanished")
}

// TestCleanupWorktrees_EmptyDirectoryNoError confirms cleanup is a safe no-op
// when the worktrees directory is empty.
func TestCleanupWorktrees_EmptyDirectoryNoError(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "worktrees"), 0755))

	err := CleanupWorktrees(configDir, nil)
	assert.NoError(t, err)
}

// TestIsWorktreeAbsentErr validates that absent-worktree errors are
// classified correctly. These are the messages git produces when we
// try to remove a worktree that isn't registered — the expected no-op
// case during pre-setup cleanup. Any unrecognized message must fall
// through so operators see the real failure (locked index, etc).
func TestIsWorktreeAbsentErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not a working tree", errors.New("git command failed: fatal: 'foo' is not a working tree (exit status 128)"), true},
		{"no such file", errors.New("git command failed: fatal: No such file or directory"), true},
		{"permission denied", errors.New("git command failed: fatal: Permission denied"), false},
		{"lockfile", errors.New("git command failed: Unable to create 'index.lock': File exists"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isWorktreeAbsentErr(tc.err))
		})
	}
}

// TestIsBranchAbsentErr validates that absent-branch errors are
// classified correctly. Non-absent failures (branch checked out
// elsewhere, etc.) must return false so they get logged.
func TestIsBranchAbsentErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"branch not found", errors.New("git command failed: error: branch 'foo' not found."), true},
		{"checked out in worktree", errors.New("git command failed: error: Cannot delete branch 'foo' checked out at 'bar'"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isBranchAbsentErr(tc.err))
		})
	}
}

// TestSetup_RecoversFromHalfRemovedWorktree pins the fix for the
// permanently-unresumable session bug. `git worktree remove -f` drops the
// worktree's .git file and then rmdir's the tree, so an agent process that
// outlived its tmux session and is still writing into it (node_modules,
// target/) makes the rmdir fail with "Directory not empty". That leaves a
// half-removed worktree — directory still on disk, registry entry marked
// `prunable` — and because the failure was only logged, every later resume
// fell through to `worktree add` and died with a cryptic
// `fatal: '<path>' already exists`. Forever: the state is not self-healing,
// so the session could never be resumed again.
func TestSetup_RecoversFromHalfRemovedWorktree(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)

	// Reproduce the half-removed state observed in the incident: git got far
	// enough to unlink .git (making the registry entry `prunable`) but the
	// directory survived, holding files a concurrent writer re-created.
	require.NoError(t, os.Remove(filepath.Join(worktreePath, ".git")))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreePath, "leftover-from-live-writer"), []byte("x"), 0644))

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)

	require.NoError(t, gw.Setup(), "resume must recover from a half-removed worktree")

	// The worktree is a working tree again, checked out at the preserved branch.
	assert.FileExists(t, filepath.Join(worktreePath, ".git"))
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--abbrev-ref", "HEAD").Output()
	require.NoError(t, err)
	assert.Equal(t, branchName, strings.TrimSpace(string(out)),
		"recovered worktree must be checked out at the session's branch")
}

// TestSetup_RefusesToDeleteLiveWorktree guards the recovery path above.
// Clearing a leftover directory is only safe once git has unlinked .git,
// because nothing tracked can survive there. A directory that is still a
// working tree may hold tracked work, so Setup must fail loudly rather
// than delete it. A locked worktree is the deterministic stand-in for any
// `worktree remove -f` failure that leaves .git intact.
func TestSetup_RefusesToDeleteLiveWorktree(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	runGit(t, repoDir, "worktree", "lock", worktreePath)
	t.Cleanup(func() { _ = exec.Command("git", "-C", repoDir, "worktree", "unlock", worktreePath).Run() })

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)

	err := gw.Setup()

	require.Error(t, err, "Setup must not silently proceed past a live worktree")
	assert.Contains(t, err.Error(), "live working tree")
	assert.FileExists(t, filepath.Join(worktreePath, "README.md"),
		"tracked work in a live worktree must never be deleted by recovery")
}

// TestSetup_PreservesGuttedWorktreeContents guards the recovery path
// against silent data loss. A gutted worktree (git unlinked .git, then
// its delete was interrupted — by a timeout kill or a losing race with a
// live writer) can still hold work that was never committed; the
// 2026-08-21 incident stranded a modified README.md exactly this way.
// Since .git is gone, git can no longer tell us what is dirty, so
// recovery must not assume the leftovers are disposable: set them aside
// rather than delete them.
func TestSetup_PreservesGuttedWorktreeContents(t *testing.T) {
	configDir, repoDir, worktreePath, branchName := setupTestRepoWithWorktree(t)
	require.NoError(t, os.Remove(filepath.Join(worktreePath, ".git")))
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "uncommitted.txt"),
		[]byte("work that was never committed\n"), 0644))

	gw := NewGitWorktreeFromStorage(repoDir, worktreePath, "sess", branchName, "", true, configDir)
	require.NoError(t, gw.Setup())

	// The session is usable again...
	assert.FileExists(t, filepath.Join(worktreePath, ".git"))

	// ...and the stranded work was set aside, not destroyed.
	matches, err := filepath.Glob(worktreePath + ".orphaned*")
	require.NoError(t, err)
	require.NotEmpty(t, matches, "gutted worktree contents must be preserved, not deleted")
	body, err := os.ReadFile(filepath.Join(matches[0], "uncommitted.txt"))
	require.NoError(t, err)
	assert.Equal(t, "work that was never committed\n", string(body),
		"uncommitted work must survive recovery verbatim")
}

// TestInspectTree classifies what Resume and CrashRestart may do with a
// worktree path. Rebuilding runs `git worktree remove -f`, so only a path
// with nothing git can vouch for (absent or gutted) may be rebuilt, and
// anything git cannot confirm must be left alone.
func TestInspectTree(t *testing.T) {
	inspect := func(t *testing.T, repoDir, path string) (TreeState, error) {
		t.Helper()
		return NewGitWorktreeFromStorage(repoDir, path, "sess", "b", "", true, "").InspectTree()
	}

	t.Run("absent", func(t *testing.T) {
		_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
		state, err := inspect(t, repoDir, worktreePath+"-missing")
		require.NoError(t, err)
		assert.Equal(t, TreeAbsent, state)
	})

	t.Run("intact, dirty", func(t *testing.T) {
		_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
		require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "notes.txt"), []byte("x"), 0644))
		state, err := inspect(t, repoDir, worktreePath)
		require.NoError(t, err)
		assert.Equal(t, TreeIntact, state)
	})

	t.Run("gutted", func(t *testing.T) {
		_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
		require.NoError(t, os.Remove(filepath.Join(worktreePath, ".git")))
		state, err := inspect(t, repoDir, worktreePath)
		require.NoError(t, err)
		assert.Equal(t, TreeGutted, state)
	})

	t.Run("gutted inside the repo it belongs to", func(t *testing.T) {
		// A workspace keeps its worktrees under <repo>/.loom/worktrees, so
		// git run in a gutted one answers for the enclosing repo. It must
		// still read as gutted, never as intact.
		_, repoDir, _, _ := setupTestRepoWithWorktree(t)
		nested := filepath.Join(repoDir, ".loom", "worktrees", "nested")
		require.NoError(t, os.MkdirAll(filepath.Dir(nested), 0755))
		runGit(t, repoDir, "worktree", "add", "-b", "nested-branch", nested)
		require.NoError(t, os.Remove(filepath.Join(nested, ".git")))
		require.NoError(t, exec.Command("git", "-C", nested, "rev-parse", "--is-inside-work-tree").Run(),
			"precondition: git in the gutted tree answers for the enclosing repo")

		state, err := inspect(t, repoDir, nested)
		require.NoError(t, err)
		assert.Equal(t, TreeGutted, state)
	})

	t.Run("a .git git rejects is unverified", func(t *testing.T) {
		_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
		require.NoError(t, os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: /nonexistent/loom-test\n"), 0644))
		state, err := inspect(t, repoDir, worktreePath)
		assert.Error(t, err)
		assert.Equal(t, TreeUnverified, state)
	})

	t.Run("a .git that resolves to another tree is unverified", func(t *testing.T) {
		_, repoDir, _, _ := setupTestRepoWithWorktree(t)
		nested := filepath.Join(repoDir, "sub")
		require.NoError(t, os.MkdirAll(filepath.Join(nested, ".git"), 0755)) // not a git dir: discovery walks up
		state, err := inspect(t, repoDir, nested)
		assert.Error(t, err)
		assert.Equal(t, TreeUnverified, state)
	})

	t.Run("a file is gutted", func(t *testing.T) {
		_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
		file := worktreePath + "-file"
		require.NoError(t, os.WriteFile(file, []byte("x"), 0644))
		state, err := inspect(t, repoDir, file)
		require.NoError(t, err)
		assert.Equal(t, TreeGutted, state)
	})
}

package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookRunner runs every command for real, after giving the test a chance
// to act first: a non-nil error from before fails the command unrun.
type hookRunner struct {
	before func(c *exec.Cmd) error
}

func (h hookRunner) intercept(c *exec.Cmd) error {
	if h.before == nil {
		return nil
	}
	return h.before(c)
}

func (h hookRunner) Run(c *exec.Cmd) error {
	if err := h.intercept(c); err != nil {
		return err
	}
	return c.Run()
}

func (h hookRunner) Output(c *exec.Cmd) ([]byte, error) {
	if err := h.intercept(c); err != nil {
		return nil, err
	}
	return c.Output()
}

func (h hookRunner) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	if err := h.intercept(c); err != nil {
		return nil, err
	}
	return c.CombinedOutput()
}

// gitOut runs git in dir and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	require.NoError(t, err, "git %v", args)
	return strings.TrimSpace(string(out))
}

func inspectAt(t *testing.T, repoDir, path string) (TreeState, error) {
	t.Helper()
	return NewGitWorktreeFromStorage(repoDir, path, "sess", "b", "", true, "").InspectTree()
}

// adminDir returns a linked worktree's git dir (<repo>/.git/worktrees/<id>).
func adminDir(t *testing.T, worktreePath string) string {
	t.Helper()
	gitfile, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	require.NoError(t, err)
	return strings.TrimSpace(strings.TrimPrefix(string(gitfile), "gitdir:"))
}

// TestInspectTree_StderrDoesNotBreakTheParse: InspectTree parses git's
// answer, so anything git writes to stderr (a warning, GIT_TRACE) must not
// be mistaken for it — that would turn an intact tree into an unverified
// one and block its resume.
func TestInspectTree_StderrDoesNotBreakTheParse(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	t.Setenv("GIT_TRACE", "1")

	state, err := inspectAt(t, repoDir, worktreePath)

	require.NoError(t, err)
	assert.Equal(t, TreeIntact, state)
}

// TestInspectTree_SymlinkedStoredPath: the stored worktree path need not
// be the path git reports — a symlinked parent (a home directory linked
// to another disk) or leaf still names the same tree.
func TestInspectTree_SymlinkedStoredPath(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)

	parentLink := filepath.Join(t.TempDir(), "parent-link")
	require.NoError(t, os.Symlink(filepath.Dir(worktreePath), parentLink))
	state, err := inspectAt(t, repoDir, filepath.Join(parentLink, filepath.Base(worktreePath)))
	require.NoError(t, err)
	assert.Equal(t, TreeIntact, state, "symlinked parent directory")

	leafLink := filepath.Join(t.TempDir(), "leaf-link")
	require.NoError(t, os.Symlink(worktreePath, leafLink))
	state, err = inspectAt(t, repoDir, leafLink)
	require.NoError(t, err)
	assert.Equal(t, TreeIntact, state, "symlinked worktree directory")

	repoLink := filepath.Join(t.TempDir(), "repo-link")
	require.NoError(t, os.Symlink(repoDir, repoLink))
	state, err = inspectAt(t, repoLink, worktreePath)
	require.NoError(t, err)
	assert.Equal(t, TreeIntact, state, "symlinked repository path")
}

// TestInspectTree_OnlyThisRepositorysLinkedWorktreeIsIntact: an intact
// verdict lets Resume launch an agent into the tree, so it must be a
// linked worktree of this session's repository — not an unrelated repo,
// the repository's own main checkout, or a submodule.
func TestInspectTree_OnlyThisRepositorysLinkedWorktreeIsIntact(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)

	t.Run("unrelated repository", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "other")
		require.NoError(t, os.MkdirAll(other, 0755))
		runGit(t, other, "init", "-b", "main")
		state, err := inspectAt(t, repoDir, other)
		assert.Error(t, err)
		assert.Equal(t, TreeUnverified, state)
	})

	t.Run("the main checkout", func(t *testing.T) {
		state, err := inspectAt(t, repoDir, repoDir)
		assert.Error(t, err)
		assert.Equal(t, TreeUnverified, state)
	})

	t.Run("a submodule inside the worktree", func(t *testing.T) {
		sub := filepath.Join(t.TempDir(), "subrepo")
		require.NoError(t, os.MkdirAll(sub, 0755))
		runGit(t, sub, "init", "-b", "main")
		runGit(t, sub, "-c", "user.email=a@b", "-c", "user.name=n", "commit", "--allow-empty", "-m", "i")
		runGit(t, worktreePath, "-c", "protocol.file.allow=always", "submodule", "add", sub, "sm")

		state, err := inspectAt(t, repoDir, worktreePath)
		require.NoError(t, err)
		assert.Equal(t, TreeIntact, state, "a worktree holding a submodule is still intact")

		state, err = inspectAt(t, repoDir, filepath.Join(worktreePath, "sm"))
		assert.Error(t, err)
		assert.Equal(t, TreeUnverified, state, "the submodule is not this repository's worktree")
	})
}

// TestInspectTree_InterruptedWorktreeAdd: `git worktree add` locks the
// new worktree "initializing" until it finishes. A lock left behind means
// the checkout never completed, so the tree cannot be vouched for.
func TestInspectTree_InterruptedWorktreeAdd(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	require.NoError(t, os.WriteFile(filepath.Join(adminDir(t, worktreePath), "locked"), []byte("initializing"), 0644))

	state, err := inspectAt(t, repoDir, worktreePath)

	assert.Error(t, err)
	assert.Equal(t, TreeUnverified, state)
}

// TestInspectTree_UserLockIsStillIntact: a worktree locked for any other
// reason (`git worktree lock`) is an ordinary, complete tree.
func TestInspectTree_UserLockIsStillIntact(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	runGit(t, repoDir, "worktree", "lock", "--reason", "on a removable disk", worktreePath)
	t.Cleanup(func() { _ = exec.Command("git", "-C", repoDir, "worktree", "unlock", worktreePath).Run() })

	state, err := inspectAt(t, repoDir, worktreePath)

	require.NoError(t, err)
	assert.Equal(t, TreeIntact, state)
}

// TestInspectTree_DanglingGitfile is what an interrupted removal leaves
// when it deleted the admin dir but not the tree: .git points nowhere.
// That is a permanent condition, not a timeout.
func TestInspectTree_DanglingGitfile(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "work.txt"), []byte("x"), 0644))
	require.NoError(t, os.RemoveAll(adminDir(t, worktreePath)))

	state, err := inspectAt(t, repoDir, worktreePath)

	require.Error(t, err)
	assert.Equal(t, TreeUnverified, state)
	assert.False(t, IsTimeout(err), "a .git pointing nowhere is permanent, not a timeout")
}

// TestInspectTree_HeldIndexLockIsIntact: another git process holding the
// index lock says nothing about whether the tree is intact.
func TestInspectTree_HeldIndexLockIsIntact(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	require.NoError(t, os.WriteFile(filepath.Join(adminDir(t, worktreePath), "index.lock"), nil, 0644))

	state, err := inspectAt(t, repoDir, worktreePath)

	require.NoError(t, err)
	assert.Equal(t, TreeIntact, state)
}

// TestInspectTree_TimeoutIsReportedAsSuch: a git that never answered
// leaves the tree unverified, and callers can tell that apart from a
// rejection (retry later, versus the tree being unusable).
func TestInspectTree_TimeoutIsReportedAsSuch(t *testing.T) {
	_, repoDir, worktreePath, _ := setupTestRepoWithWorktree(t)
	orig := gitTimeout
	gitTimeout = time.Nanosecond
	t.Cleanup(func() { gitTimeout = orig })

	state, err := inspectAt(t, repoDir, worktreePath)

	require.Error(t, err)
	assert.Equal(t, TreeUnverified, state)
	assert.True(t, IsTimeout(err), "a git that never answered must be reported as a timeout: %v", err)
}

// TestStashListed_LookupFailureIsNeverAbsence: StashListed decides
// whether a stash is still wanted, and "not listed" makes callers forget
// it (Resume) or skip dropping it (DropStash). Its one lookup is the
// `git stash list` call: when that fails — here, killed at its deadline —
// the answer is an error, and DropStash acts on nothing.
func TestStashListed_LookupFailureIsNeverAbsence(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0644))
	sha, err := gw.StashChanges("ours")
	require.NoError(t, err)

	failList := hookRunner{before: func(c *exec.Cmd) error {
		if slices.Contains(c.Args, "stash") && slices.Contains(c.Args, "list") {
			return fmt.Errorf("git command timed out after 8s: git stash list: %w", ErrTimeout)
		}
		return nil
	}}
	gwR := NewGitWorktreeFromStorageWithRunner(dir, dir, "stash-test", "main", "", true, dir, failList)

	ref, err := gwR.StashListed(sha)
	require.Error(t, err, "an unreadable list is not an absent entry")
	assert.Empty(t, ref)
	assert.True(t, IsTimeout(err))

	require.Error(t, gwR.DropStash(sha), "DropStash must not read the failure as \"already dropped\"")
	assert.Contains(t, strings.Fields(gitOut(t, dir, "stash", "list", "--format=%H")), sha,
		"the entry is untouched")
}

// TestStashListed_ListFailureIsAnError: when the list itself cannot be
// read, the answer is unknown — an error, never "not listed".
func TestStashListed_ListFailureIsAnError(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0644))
	sha, err := gw.StashChanges("ours")
	require.NoError(t, err)

	failList := hookRunner{before: func(c *exec.Cmd) error {
		if slices.Contains(c.Args, "stash") && slices.Contains(c.Args, "list") {
			return errors.New("git command timed out")
		}
		return nil
	}}
	gwR := NewGitWorktreeFromStorageWithRunner(dir, dir, "stash-test", "main", "", true, dir, failList)

	ref, err := gwR.StashListed(sha)

	assert.Error(t, err)
	assert.Empty(t, ref)
}

// TestDropStash_ConcurrentPushKeepsOtherEntry: refs/stash is shared by
// every worktree of the repository, and other sessions push to it. A push
// landing between resolving our entry's stash@{N} and `git stash drop`
// shifts the stack, so the drop removes the other session's entry. It
// must be detected and put back, and ours dropped instead.
func TestDropStash_ConcurrentPushKeepsOtherEntry(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("ours\n"), 0644))
	ours, err := gw.StashChanges("ours")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("theirs\n"), 0644))
	theirs := gitOut(t, dir, "stash", "create", "other session")
	require.NotEmpty(t, theirs)

	var once sync.Once
	pushBeforeDrop := hookRunner{before: func(c *exec.Cmd) error {
		if slices.Contains(c.Args, "stash") && slices.Contains(c.Args, "drop") {
			once.Do(func() { runGit(t, dir, "stash", "store", "-m", "other session", theirs) })
		}
		return nil
	}}
	gwR := NewGitWorktreeFromStorageWithRunner(dir, dir, "stash-test", "main", "", true, dir, pushBeforeDrop)

	require.NoError(t, gwR.DropStash(ours))

	listed := strings.Fields(gitOut(t, dir, "stash", "list", "--format=%H"))
	assert.Contains(t, listed, theirs, "the other session's stash must survive")
	assert.NotContains(t, listed, ours, "our stash must be dropped")
	assert.Contains(t, gitOut(t, dir, "stash", "list"), "other session",
		"the restored entry keeps a recognizable message")
}

// TestStashOnDisk_LeavesRealIndexAlone: StashOnDisk stages into a scratch
// index; the worktree's own index, staged changes included, is untouched
// and no scratch file is left behind.
func TestStashOnDisk_LeavesRealIndexAlone(t *testing.T) {
	gw := newStashTestRepo(t)
	dir := gw.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0644))
	runGit(t, dir, "add", "tracked.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v3\n"), 0644))
	sha, err := gw.StashChanges("staged and unstaged")
	require.NoError(t, err)
	statusBefore := gitOut(t, dir, "status", "--porcelain")
	scratchBefore, _ := filepath.Glob(filepath.Join(os.TempDir(), "loom-stash-index-*"))

	onDisk, err := gw.StashOnDisk(sha)

	require.NoError(t, err)
	assert.True(t, onDisk)
	assert.Equal(t, statusBefore, gitOut(t, dir, "status", "--porcelain"))
	scratchAfter, _ := filepath.Glob(filepath.Join(os.TempDir(), "loom-stash-index-*"))
	for _, f := range scratchAfter {
		assert.Contains(t, scratchBefore, f, "StashOnDisk left a scratch index behind")
	}
}

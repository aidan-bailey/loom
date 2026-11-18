package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pushFromAnotherClone adds a commit (file, with its name as content) to
// origin's main from a second clone of it, and returns that commit's SHA.
// clone's own refs/remotes/origin/main stays where it was: stale.
func pushFromAnotherClone(t *testing.T, clone, file string) string {
	t.Helper()
	tmp := filepath.Dir(clone)
	pusher := filepath.Join(tmp, "pusher-"+file)
	runGit(t, tmp, "clone", "origin.git", pusher)
	runGit(t, pusher, "config", "user.email", "test@example.com")
	runGit(t, pusher, "config", "user.name", "Test")
	commitFile(t, pusher, file, file)
	runGit(t, pusher, "push", "origin", "main")
	return revParse(t, pusher, "HEAD")
}

func TestFetchBranch_ReturnsTheTipOriginHasNow(t *testing.T) {
	clone := newClone(t, "main")
	old := revParse(t, clone, "refs/remotes/origin/main")
	tip := pushFromAnotherClone(t, clone, "newer.txt")
	require.NotEqual(t, old, tip)

	got, err := FetchBranch(clone, "main", nil)

	require.NoError(t, err)
	assert.Equal(t, tip, got)
	assert.Equal(t, tip, revParse(t, clone, "refs/remotes/origin/main"), "the remote-tracking ref moved")
	assert.Equal(t, old, revParse(t, clone, "refs/heads/main"), "the local branch did not")
}

func TestFetchBranch_AMissingBranchIsAnError(t *testing.T) {
	clone := newClone(t, "main")

	got, err := FetchBranch(clone, "no-such-branch", nil)

	require.Error(t, err)
	assert.Empty(t, got)
	assert.ErrorContains(t, err, "fetch")
}

func TestFetchBranch_RefusesWhatIsNoBranchName(t *testing.T) {
	clone := newClone(t, "main")
	for _, name := range []string{"", "a..b", "has space", "x:refs/heads/main"} {
		t.Run(name, func(t *testing.T) {
			got, err := FetchBranch(clone, name, nil)

			require.Error(t, err)
			assert.Empty(t, got)
			assert.ErrorContains(t, err, "not a valid branch name")
		})
	}
}

func TestShowFile_ReadsTheFileAtThatRevision(t *testing.T) {
	repo := newRepo(t, "main")
	commitFile(t, repo, "loom-watch.json", `{"v":1}`)
	v1 := revParse(t, repo, "HEAD")
	commitFile(t, repo, "loom-watch.json", `{"v":2}`)

	old, err := ShowFile(repo, v1, "loom-watch.json", nil)
	require.NoError(t, err)
	cur, err := ShowFile(repo, "HEAD", "loom-watch.json", nil)
	require.NoError(t, err)

	assert.Equal(t, `{"v":1}`, string(old), "the file as of the revision asked for")
	assert.Equal(t, `{"v":2}`, string(cur))
}

func TestShowFile_PathsAreRelativeToTheRepositoryRoot(t *testing.T) {
	repo := newRepo(t, "main")
	commitFile(t, repo, "loom-watch.json", `{"v":1}`)
	sub := filepath.Join(repo, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	got, err := ShowFile(sub, "HEAD", "loom-watch.json", nil)

	require.NoError(t, err)
	assert.Equal(t, `{"v":1}`, string(got), "asked from a subdirectory, the path still names the root's file")
}

func TestShowFile_ReadsANestedPath(t *testing.T) {
	repo := newRepo(t, "main")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "prompts"), 0o755))
	commitFile(t, repo, filepath.Join("prompts", "start.md"), "go")

	got, err := ShowFile(repo, "HEAD", "prompts/start.md", nil)

	require.NoError(t, err)
	assert.Equal(t, "go", string(got))
}

func TestShowFile_AMissingPathIsErrNoSuchPath(t *testing.T) {
	repo := newRepo(t, "main")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "prompts"), 0o755))
	commitFile(t, repo, filepath.Join("prompts", "start.md"), "go")

	_, err := ShowFile(repo, "HEAD", "loom-watch.json", nil)
	assert.True(t, errors.Is(err, ErrNoSuchPath), "no such file at the revision: %v", err)

	_, err = ShowFile(repo, "HEAD", "prompts", nil)
	assert.True(t, errors.Is(err, ErrNoSuchPath), "a directory is no file: %v", err)
}

func TestShowFile_AnUnknownRevisionIsNotAMissingFile(t *testing.T) {
	repo := newRepo(t, "main")

	_, err := ShowFile(repo, "origin/gone", "README.md", nil)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNoSuchPath), "a revision git cannot read is a failure, not an empty answer")

	_, err = ShowFile(repo, "--all", "README.md", nil)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNoSuchPath))
}

// syncFixture is a clone and the path of a watcher checkout inside it
// (`<repo>/.loom/watch-checkout`, where the daemon puts one), with the
// clone's first commit.
func syncFixture(t *testing.T) (clone, path, first string) {
	t.Helper()
	clone = newClone(t, "main")
	// The checkout's directory is untracked noise to the clone, and a later
	// `git add .` there would commit it as an embedded repository.
	exclude := filepath.Join(clone, ".git", "info", "exclude")
	require.NoError(t, os.MkdirAll(filepath.Dir(exclude), 0o755))
	require.NoError(t, os.WriteFile(exclude, []byte(".loom/\n"), 0o644))
	return clone, filepath.Join(clone, ".loom", "watch-checkout"), revParse(t, clone, "HEAD")
}

// headIsDetached reports whether dir's HEAD names no branch.
func headIsDetached(dir string) bool {
	return exec.Command("git", "-C", dir, "symbolic-ref", "-q", "HEAD").Run() != nil
}

// branchOfTree returns the branch dir has checked out ("" when detached).
func branchOfTree(dir string) string {
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "-q", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func TestSyncDetachedWorktree_AddsItWhenAbsent(t *testing.T) {
	clone, path, first := syncFixture(t)

	require.NoError(t, SyncDetachedWorktree(clone, path, first, nil))

	assert.Equal(t, first, revParse(t, path, "HEAD"))
	assert.True(t, headIsDetached(path))
	branches, err := exec.Command("git", "-C", clone, "branch", "--format=%(refname:short)").Output()
	require.NoError(t, err)
	assert.Equal(t, "main\n", string(branches), "no branch was created for it")
}

func TestSyncDetachedWorktree_MovesAnExistingCheckoutAndDiscardsItsEdits(t *testing.T) {
	clone, path, first := syncFixture(t)
	require.NoError(t, SyncDetachedWorktree(clone, path, first, nil))
	commitFile(t, clone, "README.md", "hi2")
	second := revParse(t, clone, "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(path, "README.md"), []byte("left by a hook"), 0o644))

	require.NoError(t, SyncDetachedWorktree(clone, path, second, nil))

	assert.Equal(t, second, revParse(t, path, "HEAD"))
	body, err := os.ReadFile(filepath.Join(path, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "hi2", string(body), "the checkout is loom's scratch tree: its edits do not block the move")
	assert.True(t, headIsDetached(path))
}

func TestSyncDetachedWorktree_AddsItAgainWhenItsDirectoryIsGone(t *testing.T) {
	clone, path, first := syncFixture(t)
	require.NoError(t, SyncDetachedWorktree(clone, path, first, nil))
	require.NoError(t, os.Rename(path, path+".gone"), "the directory is gone, its registration stays")

	require.NoError(t, SyncDetachedWorktree(clone, path, first, nil))

	assert.Equal(t, first, revParse(t, path, "HEAD"))
}

func TestSyncDetachedWorktree_LeavesAnUnregisteredDirectoryAlone(t *testing.T) {
	clone, path, first := syncFixture(t)
	require.NoError(t, os.MkdirAll(path, 0o755))
	keep := filepath.Join(path, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("not ours"), 0o644))

	err := SyncDetachedWorktree(clone, path, first, nil)

	require.Error(t, err)
	assert.ErrorContains(t, err, "not a detached worktree")
	assert.FileExists(t, keep)
}

func TestSyncDetachedWorktree_RefusesTheRepositorysOwnCheckout(t *testing.T) {
	clone, _, first := syncFixture(t)
	commitFile(t, clone, "x.txt", "x")
	second := revParse(t, clone, "HEAD")
	runGit(t, clone, "checkout", "--detach", second)

	err := SyncDetachedWorktree(clone, clone, first, nil)

	require.Error(t, err)
	assert.Equal(t, second, revParse(t, clone, "HEAD"), "the user's checkout, detached or not, is never moved")
	assert.FileExists(t, filepath.Join(clone, "x.txt"))
}

func TestSyncDetachedWorktree_RefusesASessionsBranchWorktree(t *testing.T) {
	clone, _, first := syncFixture(t)
	session := filepath.Join(t.TempDir(), "session-tree")
	runGit(t, clone, "worktree", "add", "-b", "feat", session)
	commitFile(t, session, "work.txt", "work")
	work := revParse(t, session, "HEAD")

	err := SyncDetachedWorktree(clone, session, first, nil)

	require.Error(t, err)
	assert.Equal(t, "feat", branchOfTree(session), "a worktree on a branch is not the watcher's")
	assert.Equal(t, work, revParse(t, session, "HEAD"))
}

func TestSyncDetachedWorktree_RefusesAGuttedCheckoutInsideTheRepository(t *testing.T) {
	clone, path, first := syncFixture(t)
	require.NoError(t, SyncDetachedWorktree(clone, path, first, nil))
	commitFile(t, clone, "y.txt", "y")
	second := revParse(t, clone, "HEAD")
	require.NoError(t, os.Remove(filepath.Join(path, ".git")), "gutted: still registered, no longer a tree of its own")

	err := SyncDetachedWorktree(clone, path, second, nil)

	require.Error(t, err)
	assert.ErrorContains(t, err, "left as it is")
	assert.Equal(t, "main", branchOfTree(clone), "git run in the gutted directory would walk up to the user's checkout")
	assert.Equal(t, second, revParse(t, clone, "HEAD"))
}

func TestSyncDetachedWorktree_NeedsAFullCommitSHA(t *testing.T) {
	clone, path, _ := syncFixture(t)
	for name, c := range map[string]struct{ sha, msg string }{
		"a branch name": {"main", "is not a full commit SHA"},
		"no commit":     {strings.Repeat("0", 40), "is not a commit in"},
	} {
		t.Run(name, func(t *testing.T) {
			err := SyncDetachedWorktree(clone, path, c.sha, nil)

			require.Error(t, err)
			assert.ErrorContains(t, err, c.msg)
			assert.NoDirExists(t, path)
		})
	}
}

func TestBranchExists_LocallyOnOriginOrNeither(t *testing.T) {
	clone := newClone(t, "main", "heal-on-origin")
	runGit(t, clone, "branch", "heal-local")
	// A branch only origin has, which this clone never fetched: ls-remote
	// must still see it.
	tmp := filepath.Dir(clone)
	runGit(t, tmp, "--git-dir", "origin.git", "branch", "heal-never-fetched", "main")

	for branch, want := range map[string]string{
		"heal-local":         "locally",
		"heal-on-origin":     "on origin",
		"heal-never-fetched": "on origin",
		"heal-nowhere":       "",
		"heal":               "", // a prefix of real branches is not one
	} {
		t.Run(branch, func(t *testing.T) {
			got, err := BranchExists(clone, branch, nil)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestBranchExists_AFailedCheckIsAnErrorNotAbsence(t *testing.T) {
	clone := newClone(t, "main")
	runGit(t, clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	got, err := BranchExists(clone, "heal-nowhere", nil)
	require.Error(t, err)
	assert.Empty(t, got)

	_, err = BranchExists(clone, "bad..name", nil)
	assert.Error(t, err, "not a branch name")
}

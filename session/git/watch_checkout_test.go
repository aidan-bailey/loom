package git

import (
	"errors"
	"os"
	"path/filepath"
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

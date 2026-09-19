package git

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAheadBehind_CountsBothSides(t *testing.T) {
	repo := newRepo(t, "main")
	runGit(t, repo, "checkout", "-b", "feature")
	commitFile(t, repo, "a.txt", "a")
	commitFile(t, repo, "b.txt", "b")
	runGit(t, repo, "checkout", "main")
	commitFile(t, repo, "m.txt", "m")

	ahead, behind, err := AheadBehind(repo, "feature", "main", nil)
	require.NoError(t, err)
	assert.Equal(t, 2, ahead)
	assert.Equal(t, 1, behind)
}

func TestAheadBehind_Even(t *testing.T) {
	repo := newRepo(t, "main")
	runGit(t, repo, "checkout", "-b", "feature")
	ahead, behind, err := AheadBehind(repo, "feature", "main", nil)
	require.NoError(t, err)
	assert.Equal(t, 0, ahead)
	assert.Equal(t, 0, behind)
}

func TestAheadBehind_MissingBaseErrors(t *testing.T) {
	repo := newRepo(t, "main")
	_, _, err := AheadBehind(repo, "main", "nope", nil)
	assert.Error(t, err)
}

func TestFetchRef_UpdatesRemoteTracking(t *testing.T) {
	clone := newClone(t, "main")
	// Advance origin out of band.
	origin := filepath.Join(filepath.Dir(clone), "origin.git")
	src := filepath.Join(filepath.Dir(clone), "src")
	commitFile(t, src, "new.txt", "n")
	runGit(t, src, "push", origin, "main")

	require.NoError(t, FetchRef(clone, "origin/main", nil))
	out, err := exec.Command("git", "-C", clone, "rev-list", "--count", "main..origin/main").Output()
	require.NoError(t, err)
	assert.Equal(t, "1\n", string(out))
}

func TestFetchRef_LocalRefIsNoop(t *testing.T) {
	repo := newRepo(t, "main")
	assert.NoError(t, FetchRef(repo, "main", nil), "no remote prefix: nothing to fetch, no error")
}

func TestFetchRef_LocalBranchWithSlashIsNoop(t *testing.T) {
	repo := newRepo(t, "main")
	runGit(t, repo, "checkout", "-b", "release/2.0")
	runGit(t, repo, "checkout", "main")
	// "release/2.0" splits into remote="release", branch="2.0", but no
	// such remote-tracking ref exists. A fetch would fail with exit 128,
	// so a nil error here proves nothing was run.
	assert.NoError(t, FetchRef(repo, "release/2.0", nil), "a local branch whose name contains a slash is not a remote ref")
}

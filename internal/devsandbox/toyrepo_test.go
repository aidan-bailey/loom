package devsandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitToyRepo_HistoryAndOrigin(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	repo, origin := filepath.Join(dir, "repo"), filepath.Join(dir, "origin.git")
	require.NoError(t, initToyRepo(repo, origin))

	assert.Equal(t, "docs: add notes\nfeat: add hello program\nchore: initial commit",
		gitOut(t, repo, "log", "--format=%s"))
	assert.Equal(t, gitOut(t, repo, "rev-parse", "HEAD"), gitOut(t, origin, "rev-parse", "main"))
	assert.Equal(t, "origin/main", gitOut(t, repo, "rev-parse", "--abbrev-ref", "origin/HEAD"))
	assert.Empty(t, gitOut(t, repo, "status", "--porcelain"), ".loom/ must be ignored")
	assert.FileExists(t, filepath.Join(repo, "docs", "notes.md"))

	require.NoError(t, initToyRepo(repo, origin), "re-running is a no-op")
}

func TestInitToyRepo_IncompleteRepoIsAnError(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	repo, origin := filepath.Join(dir, "repo"), filepath.Join(dir, "origin.git")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	require.NoError(t, git(repo, "init", "-q", "-b", "main"))

	err := initToyRepo(repo, origin)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incomplete")
	assert.Contains(t, err.Error(), "loomdev down")
}

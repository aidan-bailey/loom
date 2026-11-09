package flowview

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfilePaths(t *testing.T) {
	for _, c := range []struct{ toml, state, drafts, err string }{
		{"", "", "", ""},
		{"schema = 1\n[gates]\nstory = []\n", "", "", ""},
		{"[paths]\nstate = \"/var/flow\"\ndrafts = 'notes/drafts'\n", "/var/flow", "notes/drafts", ""},
		{"paths.state = \"st\"   # trailing comment\n", "st", "", ""},
		{"[paths]\nstate = \"\"\"multi\"\"\"\n", "", "", "paths.state"},
		{"[paths]\ndrafts = \"a\\\\b\"\n", "", "", "paths.drafts"},
		{"[paths]\nstate = \"x\" y\n", "", "", "paths.state"},
		{"[paths]\nrecords = \"r\"\n[review]\nidioms = \"state = x\"\nstate = \"not paths\"\n", "", "", ""},
		{"state = \"a root key, not paths\"\n", "", "", ""},
	} {
		dir := t.TempDir()
		if c.toml != "" {
			writeFile(t, dir, profileFile, c.toml)
		}
		state, drafts, err := profilePaths(dir)
		if c.err != "" {
			assert.ErrorContains(t, err, c.err, c.toml)
			continue
		}
		require.NoError(t, err, c.toml)
		assert.Equal(t, c.state, state, c.toml)
		assert.Equal(t, c.drafts, drafts, c.toml)
	}
}

func TestStateAndDraftsDirs(t *testing.T) {
	repo := gitRepo(t)
	st, err := stateDir(repo, "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(repo, ".git", "flow"), st)
	st, err = stateDir(repo, "var/state")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(repo, "var", "state"), st)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	st, err = stateDir(repo, "~/flowstate")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "flowstate"), st)
	assert.Equal(t, filepath.Join(repo, ".flow", "drafts", "p"), draftsDir(repo, "", "p"))
	assert.Equal(t, filepath.Join(repo, "d", "p"), draftsDir(repo, "d", "p"))
}

func TestRepoKey_IsFlowconfsKey(t *testing.T) {
	repo := gitRepo(t)
	key, err := RepoKey(repo)
	require.NoError(t, err)
	assert.Equal(t, regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(repo, "-"), key)
	linked := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "side", linked)
	k2, err := RepoKey(linked)
	require.NoError(t, err)
	assert.Equal(t, key, k2, "every worktree of a repo shares its key, as flowconf_test pins")
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(repo, alias))
	k3, err := RepoKey(alias)
	require.NoError(t, err)
	assert.Equal(t, key, k3, "a symlinked spelling resolves to the same key")
}

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadPatch_LeavesTheCachedDiffAlone: a patch is read for one client,
// so the diff stats the published views carry to every client stay as the
// probe left them.
func TestReadPatch_LeavesTheCachedDiffAlone(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644))
	runGit(t, repo, "add", "a.txt")
	runGit(t, repo, "commit", "-qm", "init")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	base := strings.TrimSpace(string(out))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0o644))

	inst, err := NewInstance(InstanceOptions{Title: "t", Path: repo, Program: "claude"})
	require.NoError(t, err)
	inst.setStarted(true)
	inst.setGitWorktree(git.NewGitWorktreeFromStorage(repo, repo, "t", "main", base, false, ""))
	cached := &git.DiffStats{Added: 9, Removed: 9}
	inst.setDiffStats(cached)

	p, err := inst.ReadPatch()
	require.NoError(t, err)
	assert.Equal(t, base, p.Base)
	require.Len(t, p.Files, 1)
	assert.Equal(t, "a.txt", p.Files[0].Path)
	assert.Equal(t, 1, p.Files[0].Added)
	assert.Same(t, cached, inst.GetDiffStats(), "the cached stats are left as they were")
}

func TestReadPatch_AWorkspaceTerminalHasNoWorktree(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "term", Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	_, err = inst.ReadPatch()
	assert.EqualError(t, err, "read the diff of term: the session has no worktree")
}

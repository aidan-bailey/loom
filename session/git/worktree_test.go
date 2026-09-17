package git

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitWorktree_StashRefGetSet(t *testing.T) {
	gw := NewGitWorktreeFromStorage("/repo", "/wt", "s", "b", "", true, "")
	assert.Empty(t, gw.GetStashRef())

	gw.SetStashRef("abc123")
	assert.Equal(t, "abc123", gw.GetStashRef())

	gw.SetStashRef("")
	assert.Empty(t, gw.GetStashRef())
}

func strptr(s string) *string { return &s }

func TestNewGitWorktreeFromSpec_PrefixOverrideBeatsConfig(t *testing.T) {
	repo := newRepo(t, "main")
	configDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, configDir, `{"branch_prefix":"team/"}`)

	_, branch, err := NewGitWorktreeFromSpec(WorktreeSpec{
		RepoPath:     repo,
		SessionName:  "My Session",
		ConfigDir:    configDir,
		BranchPrefix: strptr("spike/"),
	})
	require.NoError(t, err)
	assert.Equal(t, "spike/my-session", branch)
}

func TestNewGitWorktreeFromSpec_NilPrefixFallsBackToConfig(t *testing.T) {
	repo := newRepo(t, "main")
	configDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, configDir, `{"branch_prefix":"team/"}`)

	_, branch, err := NewGitWorktreeFromSpec(WorktreeSpec{
		RepoPath:    repo,
		SessionName: "My Session",
		ConfigDir:   configDir,
	})
	require.NoError(t, err)
	assert.Equal(t, "team/my-session", branch)
}

// TestNewGitWorktreeFromSpec_EmptyPrefixIsHonoured pins the reason the
// override is a *string: a non-nil empty prefix means "no prefix at all",
// which is distinct from nil meaning "fall back to config".
func TestNewGitWorktreeFromSpec_EmptyPrefixIsHonoured(t *testing.T) {
	repo := newRepo(t, "main")
	configDir := filepath.Join(t.TempDir(), "config")
	writeConfig(t, configDir, `{"branch_prefix":"team/"}`)

	_, branch, err := NewGitWorktreeFromSpec(WorktreeSpec{
		RepoPath:     repo,
		SessionName:  "My Session",
		ConfigDir:    configDir,
		BranchPrefix: strptr(""),
	})
	require.NoError(t, err)
	assert.Equal(t, "my-session", branch)
}

// TestNewGitWorktreeFromSpec_RejectsEmptyBranchName covers surface that only
// exists now the prefix is user-controlled: with the prefix cleared, a title
// made entirely of punctuation sanitizes away to nothing, and `worktree add
// -b ""` would otherwise fail cryptically.
func TestNewGitWorktreeFromSpec_RejectsEmptyBranchName(t *testing.T) {
	repo := newRepo(t, "main")
	configDir := filepath.Join(t.TempDir(), "config")

	_, _, err := NewGitWorktreeFromSpec(WorktreeSpec{
		RepoPath:     repo,
		SessionName:  "...",
		ConfigDir:    configDir,
		BranchPrefix: strptr(""),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch name")
}

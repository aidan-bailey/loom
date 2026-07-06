package git

import "testing"

import "github.com/stretchr/testify/assert"

func TestGitWorktree_StashRefGetSet(t *testing.T) {
	gw := NewGitWorktreeFromStorage("/repo", "/wt", "s", "b", "", true, "")
	assert.Empty(t, gw.GetStashRef())

	gw.SetStashRef("abc123")
	assert.Equal(t, "abc123", gw.GetStashRef())

	gw.SetStashRef("")
	assert.Empty(t, gw.GetStashRef())
}

package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMergeWorkspaceInstances_CorruptExistingDataReturnsError verifies that
// when a workspace's existing state.json contains non-array JSON (or otherwise
// unparseable instance data), the merge aborts with an error rather than
// silently discarding the existing content. The buggy pre-fix code used
// `_ = json.Unmarshal(...)`, which caused migration to overwrite corrupt data
// with only the new instances, making the corruption permanent with no signal.
func TestMergeWorkspaceInstances_CorruptExistingDataReturnsError(t *testing.T) {
	// An object, not an array — will fail to unmarshal into []json.RawMessage.
	corrupt := json.RawMessage(`{"oops":"not an array"}`)
	newInst := json.RawMessage(`{"title":"New","worktree":{"repo_path":"/r","worktree_path":"/wt"}}`)

	_, _, err := mergeWorkspaceInstances(corrupt, []json.RawMessage{newInst})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

// TestMergeWorkspaceInstances_AppendsToExisting verifies the happy path:
// existing entries are preserved and new ones appended.
func TestMergeWorkspaceInstances_AppendsToExisting(t *testing.T) {
	existing := json.RawMessage(`[{"title":"Alpha","worktree":{"repo_path":"/r","worktree_path":"/wt-a"}}]`)
	newInst := json.RawMessage(`{"title":"Beta","worktree":{"repo_path":"/r","worktree_path":"/wt-b"}}`)

	merged, added, err := mergeWorkspaceInstances(existing, []json.RawMessage{newInst})
	assert.NoError(t, err)
	assert.Equal(t, 1, added)

	var out []json.RawMessage
	assert.NoError(t, json.Unmarshal(merged, &out))
	assert.Len(t, out, 2)
}

// TestMergeWorkspaceInstances_SkipsDuplicatesByTitle ensures that a new
// instance whose title matches an existing one is not appended.
func TestMergeWorkspaceInstances_SkipsDuplicatesByTitle(t *testing.T) {
	existing := json.RawMessage(`[{"title":"Alpha","worktree":{"repo_path":"/r","worktree_path":"/wt-a"}}]`)
	dup := json.RawMessage(`{"title":"Alpha","worktree":{"repo_path":"/r","worktree_path":"/wt-a2"}}`)

	merged, added, err := mergeWorkspaceInstances(existing, []json.RawMessage{dup})
	assert.NoError(t, err)
	assert.Equal(t, 0, added)

	var out []json.RawMessage
	assert.NoError(t, json.Unmarshal(merged, &out))
	assert.Len(t, out, 1)
}

// TestMergeWorkspaceInstances_EmptyExistingAcceptsNew verifies nil or "[]"
// existing data is treated as "no existing instances", not as an error.
func TestMergeWorkspaceInstances_EmptyExistingAcceptsNew(t *testing.T) {
	newInst := json.RawMessage(`{"title":"Solo","worktree":{"repo_path":"/r","worktree_path":"/wt"}}`)

	cases := []json.RawMessage{nil, json.RawMessage("[]")}
	for _, existing := range cases {
		merged, added, err := mergeWorkspaceInstances(existing, []json.RawMessage{newInst})
		assert.NoError(t, err)
		assert.Equal(t, 1, added)

		var out []json.RawMessage
		assert.NoError(t, json.Unmarshal(merged, &out))
		assert.Len(t, out, 1)
	}
}

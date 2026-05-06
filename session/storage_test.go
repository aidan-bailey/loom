package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// trackingMockStorage records every SaveInstances payload, so tests can
// assert what was written and in what order.
type trackingMockStorage struct {
	data  json.RawMessage
	saved []json.RawMessage
}

func (m *trackingMockStorage) SaveInstances(jsonData json.RawMessage) error {
	m.saved = append(m.saved, append(json.RawMessage(nil), jsonData...))
	m.data = append(json.RawMessage(nil), jsonData...)
	return nil
}

func (m *trackingMockStorage) GetInstances() json.RawMessage { return m.data }

func (m *trackingMockStorage) DeleteAllInstances() error {
	m.data = nil
	return nil
}

// TestDeleteInstance_DoesNotConstructLiveInstances verifies that DeleteInstance
// never routes through LoadInstances, which would call FromInstanceData on
// every persisted row and spawn a tmux attach PTY for each running instance.
// The fixture includes an entry with an empty title. FromInstanceData for a
// non-paused instance calls Instance.Start(false), which returns
// "instance title cannot be empty" when Title is "". If DeleteInstance uses
// LoadInstances (the buggy path), that bad entry aborts the whole delete —
// the target is never removed. The fix routes through raw InstanceData.
func TestDeleteInstance_DoesNotConstructLiveInstances(t *testing.T) {
	mock := &trackingMockStorage{
		data: json.RawMessage(`[
			{"title":"Target","status":3,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/t","worktree_path":"/tmp/wt-t","branch_name":"target"}},
			{"title":"","status":0,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/b","worktree_path":"/tmp/wt-b","branch_name":"bad"}}
		]`),
	}

	s, err := NewStorage(mock, "")
	assert.NoError(t, err)

	err = s.DeleteInstance("Target")
	assert.NoError(t, err)

	assert.Len(t, mock.saved, 1)
	var remaining []InstanceData
	assert.NoError(t, json.Unmarshal(mock.saved[0], &remaining))
	assert.Len(t, remaining, 1)
	assert.Equal(t, "", remaining[0].Title)
}

// TestDeleteInstance_NotFound preserves the existing error path: removing a
// title that isn't present returns an error and does not save.
func TestDeleteInstance_NotFound(t *testing.T) {
	mock := &trackingMockStorage{
		data: json.RawMessage(`[{"title":"Foo","status":3,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/a","worktree_path":"/tmp/wt-a","branch_name":"foo"}}]`),
	}
	s, err := NewStorage(mock, "")
	assert.NoError(t, err)
	err = s.DeleteInstance("NoSuchInstance")
	assert.Error(t, err)
	assert.Empty(t, mock.saved)
}

// TestSaveInstances_PersistsWhileKilling is the regression for the
// op_failed op=delete race. Kill() flips Instance.started = false early,
// before tmux/worktree teardown completes. A concurrent SaveInstances during
// that window would previously drop the killing instance from disk (because
// the old filter excluded !Started()), so DeleteInstance would then fail
// with ErrInstanceNotFound. The fix is that SaveInstances must trust the
// caller's list — the lifecycle filter belongs at the call site
// (persistableInstances in app/app.go), which already excludes Deleting.
func TestSaveInstances_PersistsWhileKilling(t *testing.T) {
	mock := &trackingMockStorage{}
	s, err := NewStorage(mock, "")
	assert.NoError(t, err)

	// Simulate the kill window: status=Deleting was set by preAction, then
	// Kill() flipped started=false while tmux teardown is still running.
	killing := &Instance{Title: "Killing", Status: Deleting, Program: "claude"}
	killing.setStarted(false)

	// Another instance is in normal Running state alongside it.
	running := &Instance{Title: "Running", Status: Running, Program: "claude"}
	running.setStarted(true)

	err = s.SaveInstances([]*Instance{killing, running})
	assert.NoError(t, err)

	assert.Len(t, mock.saved, 1)
	var persisted []InstanceData
	assert.NoError(t, json.Unmarshal(mock.saved[0], &persisted))
	assert.Len(t, persisted, 2, "both instances must reach disk; SaveInstances must not filter by started flag")

	titles := map[string]bool{}
	for _, d := range persisted {
		titles[d.Title] = true
	}
	assert.True(t, titles["Killing"], "killing instance must still be on disk so DeleteInstance can find it")
	assert.True(t, titles["Running"], "running instance must still be on disk")
}

// TestUpdateInstance_DoesNotConstructLiveInstances mirrors the DeleteInstance
// test for the Update path, which has the same problem.
func TestUpdateInstance_DoesNotConstructLiveInstances(t *testing.T) {
	mock := &trackingMockStorage{
		data: json.RawMessage(`[
			{"title":"Target","status":3,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/t","worktree_path":"/tmp/wt-t","branch_name":"target"}},
			{"title":"","status":0,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/b","worktree_path":"/tmp/wt-b","branch_name":"bad"}}
		]`),
	}

	s, err := NewStorage(mock, "")
	assert.NoError(t, err)

	// Build a fresh Instance that has Title "Target" but nothing else real —
	// we only need Snapshot()/ToInstanceData() to report the title so the
	// update-by-title path can locate it.
	target := &Instance{Title: "Target", Status: Paused, Program: "claude"}
	target.setStarted(true)

	err = s.UpdateInstance(target)
	assert.NoError(t, err)
	assert.Len(t, mock.saved, 1)
}

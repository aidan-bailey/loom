package session

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestStorage_LoadAndReconcile_PreservesFailedRecords is the regression
// guard for the "sessions disappear after exit/reopen" bug. When
// ReconcileAndRestore fails for an instance (transient tmux flake, bad
// data, etc.), LoadAndReconcile used to silently drop it; the next
// SaveInstances then overwrote state.json with only the survivors,
// permanently deleting the failed record from disk. The fix retains
// the raw InstanceData of failed records and merges them back into
// every SaveInstances payload so a future launch can retry reconcile.
func TestStorage_LoadAndReconcile_PreservesFailedRecords(t *testing.T) {
	wt := t.TempDir()
	// Two instances on disk: one with an empty Title (forces Start to
	// reject with "title cannot be empty", which propagates as a
	// reconcile failure) and one Paused (reconcile no-ops successfully).
	initial := `[
		{"title":"","status":0,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/r","worktree_path":"` + wt + `","branch_name":"orphan"}},
		{"title":"alive","status":3,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/r","worktree_path":"` + wt + `","branch_name":"alive-branch"}}
	]`
	mock := &trackingMockStorage{data: json.RawMessage(initial)}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return nil }, // has-session reports tmux alive
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}

	s, err := NewStorage(mock, "")
	require.NoError(t, err)

	instances, err := s.LoadAndReconcile(cmdExec)
	require.NoError(t, err)
	require.Len(t, instances, 1, "empty-title record fails reconcile and is dropped from the live list")
	assert.Equal(t, "alive", instances[0].Title)

	// Save the surviving live instances. Before the fix this wrote
	// only [alive]; the failed record was permanently deleted from disk.
	require.NoError(t, s.SaveInstances(instances))
	require.NotEmpty(t, mock.saved)

	var persisted []InstanceData
	require.NoError(t, json.Unmarshal(mock.saved[len(mock.saved)-1], &persisted))
	titles := make([]string, 0, len(persisted))
	for _, d := range persisted {
		titles = append(titles, d.Title)
	}
	assert.ElementsMatch(t, []string{"", "alive"}, titles,
		"the failed record must remain on disk so a future launch can retry reconcile")
}

// TestStorage_SaveInstances_LiveWinsOverUnrecoveredOnTitleCollision keeps
// SaveInstances from emitting duplicate records when a live instance
// shares its title with an unrecovered one (rare in practice — only
// possible if a user creates a new instance whose title matches an
// orphan — but a defensive guard since duplicate titles confuse the
// next load pass).
func TestStorage_SaveInstances_LiveWinsOverUnrecoveredOnTitleCollision(t *testing.T) {
	wt := t.TempDir()
	initial := `[
		{"title":"shared","status":0,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/r","worktree_path":"` + wt + `","branch_name":"orphan"}}
	]`
	mock := &trackingMockStorage{data: json.RawMessage(initial)}
	s, err := NewStorage(mock, "")
	require.NoError(t, err)

	// Seed unrecovered directly. A live LoadAndReconcile setup with a
	// non-empty title would actually reconcile OK against the mock, so
	// this is a white-box test of the dedup rule.
	s.unrecovered = []InstanceData{
		{Title: "shared", Path: wt, Program: "claude", Status: Running},
	}

	live := &Instance{Title: "shared", Status: Paused, Program: "claude"}
	live.setStarted(true)

	require.NoError(t, s.SaveInstances([]*Instance{live}))
	var persisted []InstanceData
	require.NoError(t, json.Unmarshal(mock.saved[len(mock.saved)-1], &persisted))
	require.Len(t, persisted, 1, "live record must win; unrecovered duplicate must be dropped")
	assert.Equal(t, Paused, persisted[0].Status, "live data (Paused) wins, not unrecovered (Running)")
}

// TestStorage_UnrecoveredWorktreePaths_ReturnsCachedPaths verifies the
// orphan-recovery cooperation hook: callers that build a "claimed
// worktree paths" set (to avoid double-surfacing a worktree as both a
// preserved-but-failed record AND an orphan candidate) can ask Storage
// which paths are tracked in unrecovered. Without this, a reconcile
// failure would mean the user sees both a preserved record (via my
// non-destructive reconcile fix) and an orphan-recovery prompt for the
// same worktree, and accepting the prompt would duplicate state.
func TestStorage_UnrecoveredWorktreePaths_ReturnsCachedPaths(t *testing.T) {
	wt := t.TempDir()
	initial := `[
		{"title":"","status":0,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/r","worktree_path":"` + wt + `","branch_name":"orphan"}},
		{"title":"alive","status":3,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/r","worktree_path":"` + wt + `/alive","branch_name":"alive-branch"}}
	]`
	mock := &trackingMockStorage{data: json.RawMessage(initial)}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}
	s, err := NewStorage(mock, "")
	require.NoError(t, err)

	// Before LoadAndReconcile, nothing is unrecovered yet.
	require.Empty(t, s.UnrecoveredWorktreePaths())

	_, err = s.LoadAndReconcile(cmdExec)
	require.NoError(t, err)

	got := s.UnrecoveredWorktreePaths()
	assert.True(t, got[wt], "the failed record's worktree path must be exposed so orphan discovery can treat it as claimed")
	assert.False(t, got[wt+"/alive"], "the successfully reconciled record must not appear in unrecovered")
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

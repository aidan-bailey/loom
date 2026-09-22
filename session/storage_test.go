package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"

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

// TestStorage_ConcurrentAccessDoesNotDeadlock exercises the mutex added
// to guard the unrecovered cache. The real target is the data race
// between a pause/resume SaveInstances (which runs in a tea.Cmd
// goroutine) and a workspace-activation LoadAndReconcile (which resets
// the cache); that race can only be *detected* under `go test -race`,
// which needs CGO. What this test CAN verify in a CGO-less build is that
// the locking is reentrancy-free — i.e. hammering the locked methods
// concurrently completes rather than deadlocking.
func TestStorage_ConcurrentAccessDoesNotDeadlock(t *testing.T) {
	mock := &trackingMockStorage{data: json.RawMessage(`[
		{"title":"a","status":3,"program":"claude","worktree":{"worktree_path":"/tmp/wt-a","branch_name":"a"}}
	]`)}
	s, err := NewStorage(mock, "")
	require.NoError(t, err)
	cmdExec := cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(3)
			go func() { defer wg.Done(); _, _ = s.LoadAndReconcile(cmdExec) }()
			go func() { defer wg.Done(); _ = s.SaveInstances(nil) }()
			go func() { defer wg.Done(); _ = s.UnrecoveredWorktreePaths() }()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("storage locking deadlocked under concurrent access")
	}
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

// futureRecord is a record written by a newer loom (schema_version above
// CurrentSchemaVersion) carrying a field this binary has never heard of.
// It is the shape a downgrade leaves behind in state.json.
const futureRecord = `{"schema_version":99,"title":"from-the-future","status":3,"program":"claude","worktree":{"repo_path":"/tmp/r","worktree_path":"/tmp/wt-future","branch_name":"future"},"shiny_new_field":{"nested":[1,2,3]}}`

// futureStorage returns a Storage whose backing store holds one record
// this binary can decode ("alive", Paused so reconcile no-ops) followed
// by futureRecord.
func futureStorage(t *testing.T) (*Storage, *trackingMockStorage) {
	t.Helper()
	mock := &trackingMockStorage{data: json.RawMessage(`[
		{"title":"alive","status":3,"program":"claude","is_workspace_terminal":false,"worktree":{"repo_path":"/tmp/r","worktree_path":"/tmp/wt-alive","branch_name":"alive"}},
		` + futureRecord + `
	]`)}
	s, err := NewStorage(mock, "")
	require.NoError(t, err)
	return s, mock
}

// assertFutureRecordPreserved checks the last saved payload still carries
// futureRecord with JSON identical to the original (modulo whitespace).
func assertFutureRecordPreserved(t *testing.T, mock *trackingMockStorage) {
	t.Helper()
	require.NotEmpty(t, mock.saved, "expected a save")
	var records []json.RawMessage
	require.NoError(t, json.Unmarshal(mock.saved[len(mock.saved)-1], &records))
	var want bytes.Buffer
	require.NoError(t, json.Compact(&want, []byte(futureRecord)))
	for _, r := range records {
		var got bytes.Buffer
		require.NoError(t, json.Compact(&got, r))
		if bytes.Equal(got.Bytes(), want.Bytes()) {
			return
		}
	}
	t.Fatalf("undecodable record missing or altered in saved payload: %s", mock.saved[len(mock.saved)-1])
}

func noopExec() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc:    func(c *exec.Cmd) error { return nil },
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}
}

// TestStorage_UndecodableRecord_SurvivesSave is the regression guard for
// the downgrade wipe: a record written by a newer loom used to abort the
// whole load, the caller carried on with an empty list, and the next save
// rewrote state.json without any records. The record must now be skipped
// on load and written back verbatim by every save.
func TestStorage_UndecodableRecord_SurvivesSave(t *testing.T) {
	s, mock := futureStorage(t)

	instances, err := s.LoadAndReconcile(noopExec())
	require.NoError(t, err, "one undecodable record must not fail the load")
	require.Len(t, instances, 1)
	assert.Equal(t, "alive", instances[0].Title)
	assert.Equal(t, 1, s.UndecodableCount())
	assert.Empty(t, s.UnrecoveredTitles(), "undecodable records are not reconcile failures")

	require.NoError(t, s.SaveInstances(instances))
	assertFutureRecordPreserved(t, mock)

	var persisted []json.RawMessage
	require.NoError(t, json.Unmarshal(mock.saved[len(mock.saved)-1], &persisted))
	assert.Len(t, persisted, 2, "live record + preserved record, no duplicates")
}

// TestStorage_UndecodableRecord_SurvivesDelete covers the raw-data write
// path behind DeleteInstance.
func TestStorage_UndecodableRecord_SurvivesDelete(t *testing.T) {
	s, mock := futureStorage(t)
	_, err := s.LoadAndReconcile(noopExec())
	require.NoError(t, err)

	require.NoError(t, s.DeleteInstance("alive"))
	assertFutureRecordPreserved(t, mock)

	var persisted []json.RawMessage
	require.NoError(t, json.Unmarshal(mock.saved[len(mock.saved)-1], &persisted))
	assert.Len(t, persisted, 1, "only the preserved record remains")
}

// TestStorage_UndecodableRecord_SurvivesUpdate covers the raw-data write
// path behind UpdateInstance.
func TestStorage_UndecodableRecord_SurvivesUpdate(t *testing.T) {
	s, mock := futureStorage(t)

	alive := &Instance{Title: "alive", Status: Paused, Program: "aider"}
	alive.setStarted(true)
	require.NoError(t, s.UpdateInstance(alive))
	assertFutureRecordPreserved(t, mock)

	data, err := s.LoadInstanceData()
	require.NoError(t, err)
	require.Len(t, data, 1)
	assert.Equal(t, "aider", data[0].Program, "the update itself landed")
}

// TestStorage_UndecodableRecord_WorktreeIsClaimed keeps orphan discovery
// from offering a preserved record's worktree as a Recoverable, which
// would let the user adopt it under a second, duplicate record.
func TestStorage_UndecodableRecord_WorktreeIsClaimed(t *testing.T) {
	s, _ := futureStorage(t)
	_, err := s.LoadAndReconcile(noopExec())
	require.NoError(t, err)

	got := s.UnrecoveredWorktreePaths()
	assert.True(t, got["/tmp/wt-future"], "the undecodable record's worktree must be claimed")
	assert.False(t, got["/tmp/wt-alive"], "a loaded record is claimed by the live list, not by storage")
}

// TestStorage_SaveBeforeLoad_KeepsUndecodable guards the never-loaded
// path: a Storage whose first operation is a save must still learn which
// records it cannot decode before it writes, or it would drop them.
func TestStorage_SaveBeforeLoad_KeepsUndecodable(t *testing.T) {
	mock := &trackingMockStorage{data: json.RawMessage(`[` + futureRecord + `]`)}
	s, err := NewStorage(mock, "")
	require.NoError(t, err)

	live := &Instance{Title: "fresh", Status: Paused, Program: "claude"}
	live.setStarted(true)
	require.NoError(t, s.SaveInstances([]*Instance{live}))
	assertFutureRecordPreserved(t, mock)

	var persisted []json.RawMessage
	require.NoError(t, json.Unmarshal(mock.saved[len(mock.saved)-1], &persisted))
	assert.Len(t, persisted, 2)
}

// TestStorage_TopLevelCorrupt_RefusesWrites pins the fail-closed save
// latch: when the payload itself cannot be decoded there is nothing to
// preserve record by record, so every write must refuse rather than
// replace it with whatever the caller holds (usually nothing).
func TestStorage_TopLevelCorrupt_RefusesWrites(t *testing.T) {
	const corrupt = `{"not":"an array"}`

	t.Run("after a failed load", func(t *testing.T) {
		mock := &trackingMockStorage{data: json.RawMessage(corrupt)}
		s, err := NewStorage(mock, "")
		require.NoError(t, err)

		_, err = s.LoadAndReconcile(noopExec())
		require.Error(t, err)

		live := &Instance{Title: "fresh", Status: Paused, Program: "claude"}
		live.setStarted(true)
		err = s.SaveInstances([]*Instance{live})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrStorageLoadFailed), "got %v", err)
		assert.Empty(t, mock.saved, "nothing may be written")
		assert.Equal(t, corrupt, string(mock.data), "backing state must be byte-identical")
	})

	t.Run("never loaded", func(t *testing.T) {
		mock := &trackingMockStorage{data: json.RawMessage(corrupt)}
		s, err := NewStorage(mock, "")
		require.NoError(t, err)

		err = s.SaveInstances(nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrStorageLoadFailed), "got %v", err)
		assert.Empty(t, mock.saved)
		assert.Equal(t, corrupt, string(mock.data))
	})

	// DeleteInstance and UpdateInstance load inside the write; their load
	// failure is the refusal, so it must carry the sentinel too.
	t.Run("delete refuses", func(t *testing.T) {
		mock := &trackingMockStorage{data: json.RawMessage(corrupt)}
		s, err := NewStorage(mock, "")
		require.NoError(t, err)

		err = s.DeleteInstance("fresh")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrStorageLoadFailed), "got %v", err)
		assert.Empty(t, mock.saved)
		assert.Equal(t, corrupt, string(mock.data))
	})

	t.Run("update refuses", func(t *testing.T) {
		mock := &trackingMockStorage{data: json.RawMessage(corrupt)}
		s, err := NewStorage(mock, "")
		require.NoError(t, err)

		live := &Instance{Title: "fresh", Status: Paused, Program: "claude"}
		live.setStarted(true)
		err = s.UpdateInstance(live)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrStorageLoadFailed), "got %v", err)
		assert.Empty(t, mock.saved)
		assert.Equal(t, corrupt, string(mock.data))
	})

	t.Run("delete all is the explicit wipe", func(t *testing.T) {
		mock := &trackingMockStorage{data: json.RawMessage(corrupt)}
		s, err := NewStorage(mock, "")
		require.NoError(t, err)
		_, err = s.LoadAndReconcile(noopExec())
		require.Error(t, err)

		require.NoError(t, s.DeleteAllInstances(), "reset must work even when the payload is unreadable")
		assert.Zero(t, s.UndecodableCount())

		live := &Instance{Title: "fresh", Status: Paused, Program: "claude"}
		live.setStarted(true)
		require.NoError(t, s.SaveInstances([]*Instance{live}), "a wipe clears the latch")
		var persisted []InstanceData
		require.NoError(t, json.Unmarshal(mock.saved[len(mock.saved)-1], &persisted))
		require.Len(t, persisted, 1)
		assert.Equal(t, "fresh", persisted[0].Title)
	})
}

// TestStorage_PreservedTitles covers the title set that title-keyed sweeps
// (the server-wide orphan tmux sweep, the subagent hooks sweep) must spare:
// reconcile failures plus undecodable records whose title can be read.
// Without the undecodable half, a downgraded loom killed a newer loom's
// still-running agents even though their records were preserved.
func TestStorage_PreservedTitles(t *testing.T) {
	mock := &trackingMockStorage{data: json.RawMessage(`[
		{"title":"alive","status":3,"program":"claude","worktree":{"worktree_path":"/tmp/wt-alive"}},
		{"schema_version":99,"title":"future"},
		{"schema_version":99},
		{"schema_version":99,"title":{"garbled":true}},
		{"title":42},
		"not an object"
	]`)}
	s, err := NewStorage(mock, "")
	require.NoError(t, err)
	_, err = s.LoadInstanceData()
	require.NoError(t, err)
	require.Equal(t, 5, s.UndecodableCount())

	// Seed a reconcile failure directly (white-box, as in the collision
	// test above): forcing one through LoadAndReconcile needs an empty
	// title, which would make the assertion ambiguous.
	s.unrecovered = []InstanceData{{Title: "flaky"}}

	assert.ElementsMatch(t, []string{"flaky", "future"}, s.PreservedTitles(),
		"untitled and unreadable titles are skipped; the loaded record is the live list's to claim")
}

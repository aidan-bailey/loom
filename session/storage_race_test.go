package session

import (
	"encoding/json"
	"sync"
	"testing"
)

// syncMockStorage is a trivially thread-safe InstanceStorage: it never
// mutates shared state (SaveInstances discards, GetInstances returns an
// immutable snapshot), so the ONLY unsynchronized shared state exercised by
// the race test below is *Storage's* internal state — the unrecovered cache
// and its read-modify-write of the backing store. That isolates the
// production race the audit flagged rather than a race inside the test double.
type syncMockStorage struct {
	fixed json.RawMessage // immutable; GetInstances always returns this snapshot
}

func (m *syncMockStorage) SaveInstances(json.RawMessage) error { return nil }
func (m *syncMockStorage) GetInstances() json.RawMessage       { return m.fixed }
func (m *syncMockStorage) DeleteAllInstances() error           { return nil }

// TestStorage_ConcurrentSaveAndDelete reproduces the data race the audit
// identified between the pause/resume save path (Storage.SaveInstances, which
// ranges over s.unrecovered) and the kill path (Storage.DeleteInstance, which
// rewrites s.unrecovered via dropUnrecovered). Both run from separate tea.Cmd
// goroutines in production, so Storage has no business touching shared state
// without synchronization. Must pass under `go test -race`.
func TestStorage_ConcurrentSaveAndDelete(t *testing.T) {
	mock := &syncMockStorage{
		fixed: json.RawMessage(`[{"title":"Target","status":3,"program":"claude","worktree":{"repo_path":"/tmp","worktree_path":"/tmp/wt","branch_name":"t"}}]`),
	}
	s, err := NewStorage(mock, "")
	if err != nil {
		t.Fatal(err)
	}
	// Seed the unrecovered cache so dropUnrecovered does real work each call.
	s.unrecovered = []InstanceData{{Title: "u1"}, {Title: "u2"}}

	var wg sync.WaitGroup
	const n = 2000
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = s.SaveInstances(nil) // reads s.unrecovered
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = s.DeleteInstance("Target") // writes s.unrecovered via dropUnrecovered
		}
	}()
	wg.Wait()
}

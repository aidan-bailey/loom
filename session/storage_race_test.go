package session

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
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
			_ = s.DeleteInstance("Target", time.Time{}) // writes s.unrecovered via dropUnrecovered
		}
	}()
	wg.Wait()
}

// blockingStateStorage is an InstanceStorage whose SaveInstances signals
// writing and then waits for release, as a slow fsync does.
type blockingStateStorage struct {
	data             json.RawMessage
	writing, release chan struct{}
}

func (b *blockingStateStorage) SaveInstances(data json.RawMessage) error {
	close(b.writing)
	<-b.release
	b.data = data
	return nil
}
func (b *blockingStateStorage) GetInstances() json.RawMessage { return b.data }
func (b *blockingStateStorage) DeleteAllInstances() error     { return nil }

// TestStorage_ReadsDoNotWaitForAWrite: the daemon's loop asks every
// workspace's storage whether it refuses writes and which titles it
// preserves on every wake (core's wsViewOf), while a pause, resume or kill
// writes the same storage from its job. The write held the storage's lock
// across the state file's fsyncs, so every client froze for as long as the
// disk took. Those reads wait for no write now.
func TestStorage_ReadsDoNotWaitForAWrite(t *testing.T) {
	state := &blockingStateStorage{
		data:    json.RawMessage(`[{"title":"kept","status":3,"program":"claude","worktree":{"repo_path":"/r","worktree_path":"/wt","branch_name":"b"}},{"schema_version":99,"title":"future"}]`),
		writing: make(chan struct{}),
		release: make(chan struct{}),
	}
	s, err := NewStorage(state, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadAndReconcile(noopExec()); err != nil {
		t.Fatal(err)
	}
	wrote := make(chan error)
	go func() { wrote <- s.DeleteInstance("kept", time.Time{}) }()
	<-state.writing

	read := make(chan []string)
	go func() {
		_ = s.WritesRefused()
		read <- s.PreservedTitles()
	}()
	select {
	case titles := <-read:
		if len(titles) != 1 || titles[0] != "future" {
			t.Errorf("PreservedTitles() = %v, want [future]", titles)
		}
	case <-time.After(2 * time.Second):
		t.Error("a read waited for the write")
	}
	close(state.release)
	if err := <-wrote; err != nil {
		t.Fatal(err)
	}
}

package config

import (
	"encoding/json"
	"sync"
	"testing"
)

// TestState_ConcurrentInstanceAndHelpSaves reproduces the race the audit
// identified on *State. The same *State object backs both the Storage
// instance-save path (Storage.SaveInstances -> State.SaveInstances ->
// SaveState, run from a tea.Cmd goroutine) and the help-screen path
// (m.appState.SetHelpScreensSeen, run from the Update goroutine). They
// share HelpScreensSeen, InstancesData and the lastWritten cache with no
// synchronization. Must pass under `go test -race`.
func TestState_ConcurrentInstanceAndHelpSaves(t *testing.T) {
	dir := t.TempDir()
	st := LoadStateFrom(dir) // configDir set to dir; writes go to the temp dir

	var wg sync.WaitGroup
	const n = 500
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = st.SaveInstances(json.RawMessage(`[]`))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = st.SetHelpScreensSeen(uint32(i))
		}
	}()
	wg.Wait()
}

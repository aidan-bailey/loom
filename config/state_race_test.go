package config

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

// TestState_ReadsDoNotWaitForASave: the daemon's loop reads the UI prefs and
// help flags (core's wsViewOf) on every wake, while a pause, resume or kill
// saves the same State from its job. The save held the State's lock across
// the write and its two fsyncs, so every client froze for as long as the
// disk took. A read waits for no write now.
func TestState_ReadsDoNotWaitForASave(t *testing.T) {
	st := LoadStateFrom(t.TempDir())
	writing, release := make(chan struct{}), make(chan struct{})
	prev := writeStateFile
	writeStateFile = func(path string, data []byte, perm os.FileMode) error {
		close(writing)
		<-release
		return prev(path, data, perm)
	}
	t.Cleanup(func() { writeStateFile = prev })
	saved := make(chan error)
	go func() { saved <- st.SaveInstances(json.RawMessage(`[{"title":"a"}]`)) }()
	<-writing

	read := make(chan struct{})
	go func() {
		_ = st.GetUIPrefs()
		_ = st.GetHelpScreensSeen()
		_ = st.GetInstances()
		close(read)
	}()
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Error("a read waited for the save's write")
	}
	close(release)
	require.NoError(t, <-saved)
	<-read
}

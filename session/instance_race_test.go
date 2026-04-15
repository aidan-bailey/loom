package session

import (
	"claude-squad/session/git"
	"sync"
	"testing"
)

// TestInstance_ConcurrentStatusReadWrite exercises the race the audit
// identified between tick-worker goroutines and main-loop status writers.
// Must pass under `go test -race`.
func TestInstance_ConcurrentStatusReadWrite(t *testing.T) {
	inst := &Instance{Title: "race", Status: Ready}

	var wg sync.WaitGroup
	const n = 1000

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			inst.SetStatus(Running)
			inst.SetStatus(Paused)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = inst.GetStatus()
		}
	}()
	wg.Wait()
}

// TestInstance_ConcurrentDiffStats reproduces INST-22: worker goroutines
// writing i.diffStats while the render path reads it.
func TestInstance_ConcurrentDiffStats(t *testing.T) {
	inst := &Instance{Title: "race"}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			inst.setDiffStats(&git.DiffStats{Added: i, Removed: i})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = inst.GetDiffStats()
		}
	}()
	wg.Wait()
}

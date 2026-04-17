package session

import (
	"claude-squad/session/git"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
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

// TestInstance_ReserveStartOnlyOneWinner verifies that reserveStart() is
// atomic: under N concurrent callers, exactly one wins the reservation.
// The pre-fix Start() used a check-then-set sequence — lock, test started,
// unlock, then run setup. Two goroutines could both observe started=false
// and both proceed, orphaning duplicate tmux sessions and worktrees. The
// guard was documented as INST-04 idempotency but had a TOCTOU hole.
func TestInstance_ReserveStartOnlyOneWinner(t *testing.T) {
	inst := &Instance{Title: "race", Program: "claude"}

	var wg sync.WaitGroup
	var wins atomic.Int32
	const n = 100

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if inst.reserveStart() {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), wins.Load(),
		"exactly one goroutine should win the start reservation under concurrency")
}

// TestInstance_ReserveStartRejectsAfterSuccess verifies that once a Start
// has succeeded (started=true), reserveStart refuses further reservations.
func TestInstance_ReserveStartRejectsAfterSuccess(t *testing.T) {
	inst := &Instance{Title: "race", Program: "claude"}
	inst.setStarted(true)

	assert.False(t, inst.reserveStart(),
		"reserveStart must return false when instance is already started")
}

// TestInstance_ReserveStartReleasableOnFailure verifies that when a Start
// attempt fails, the reservation is released so another attempt can succeed.
// Without this, a transient tmux or git failure would permanently wedge the
// instance in a "starting" state.
func TestInstance_ReserveStartReleasableOnFailure(t *testing.T) {
	inst := &Instance{Title: "race", Program: "claude"}

	assert.True(t, inst.reserveStart(), "first reservation should succeed")
	inst.releaseStart()
	assert.True(t, inst.reserveStart(),
		"second reservation should succeed after release (failed start path)")
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

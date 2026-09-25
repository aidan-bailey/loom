package session

import (
	"github.com/aidan-bailey/loom/session/git"
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
			_ = inst.TransitionTo(Running)
			_ = inst.TransitionTo(Paused)
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
	inst := &Instance{Title: "race", program: "claude"}

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
	inst := &Instance{Title: "race", program: "claude"}
	inst.setStarted(true)

	assert.False(t, inst.reserveStart(),
		"reserveStart must return false when instance is already started")
}

// TestInstance_ReserveStartReleasableOnFailure verifies that when a Start
// attempt fails, the reservation is released so another attempt can succeed.
// Without this, a transient tmux or git failure would permanently wedge the
// instance in a "starting" state.
func TestInstance_ReserveStartReleasableOnFailure(t *testing.T) {
	inst := &Instance{Title: "race", program: "claude"}

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

// TestInstance_LaunchFieldAccessors round-trips every launch-field
// accessor, and pins that SetLaunchOptions sets all three fields at once
// and NewInstance maps InstanceOptions.Prompt onto the instance.
func TestInstance_LaunchFieldAccessors(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{
		Title: "acc", Path: t.TempDir(), Program: "claude",
		HeadroomProxy: true, Prompt: "hello",
	})
	assert.NoError(t, err)
	assert.Equal(t, "claude", inst.Program())
	assert.True(t, inst.HeadroomProxy())
	assert.False(t, inst.CacheTTL1h())
	assert.Equal(t, "hello", inst.Prompt())
	assert.False(t, inst.CrashRecovered())

	inst.SetProgram("aider")
	assert.Equal(t, "aider", inst.Program())
	assert.True(t, inst.HeadroomProxy(), "SetProgram leaves the toggles alone")

	inst.SetLaunchOptions("claude --model 'opus'", false, true)
	assert.Equal(t, "claude --model 'opus'", inst.Program())
	assert.False(t, inst.HeadroomProxy())
	assert.True(t, inst.CacheTTL1h())
	le, _ := inst.launchEnv(false)
	program, hp, ttl := le.Program, le.HeadroomProxy, le.CacheTTL1h
	assert.Equal(t, "claude --model 'opus'", program)
	assert.False(t, hp)
	assert.True(t, ttl)

	inst.SetPrompt("")
	assert.Empty(t, inst.Prompt())

	inst.SetCrashRecovered(true)
	assert.True(t, inst.CrashRecovered())
	inst.SetCrashRecovered(false)
	assert.False(t, inst.CrashRecovered())

	data := inst.Snapshot()
	assert.Equal(t, "claude --model 'opus'", data.Program)
	assert.False(t, data.HeadroomProxy)
	assert.True(t, data.CacheTTL1h)
}

// TestInstance_ConcurrentLaunchOptions pins that the launch fields are
// written under i.mu: before they were encapsulated, the launch-options
// callbacks assigned them bare on the Update goroutine while Snapshot (a
// save from a Cmd goroutine) and the launch paths (Start, Resume's
// recovery, CrashRestart — also Cmd goroutines) read them. Also checks
// SetLaunchOptions is atomic: launchSpec never pairs one call's program
// with another call's toggles. Must pass under `go test -race`.
func TestInstance_ConcurrentLaunchOptions(t *testing.T) {
	inst := &Instance{Title: "race", Status: Paused}

	var wg sync.WaitGroup
	const n = 1000
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			inst.SetLaunchOptions("claude", true, true)
			inst.SetLaunchOptions("aider", false, false)
			inst.SetPrompt("p")
			inst.SetCrashRecovered(i%2 == 0)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			le, _ := inst.launchEnv(false)
			program, hp, ttl := le.Program, le.HeadroomProxy, le.CacheTTL1h
			if program != "" {
				on := program == "claude"
				assert.Equal(t, on, hp, "torn launch options: %q with headroomProxy=%v", program, hp)
				assert.Equal(t, on, ttl, "torn launch options: %q with cacheTTL1h=%v", program, ttl)
			}
			_ = inst.Snapshot()
			_ = inst.Program()
			_ = inst.Prompt()
			_ = inst.CrashRecovered()
		}
	}()
	wg.Wait()
}

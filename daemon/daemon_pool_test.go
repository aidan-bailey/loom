package daemon

import (
	"github.com/aidan-bailey/loom/log"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDaemonPool_Saturates drives Phase 2.2's core contract: once every
// pool slot is held by wedged work, subsequent calls return quickly
// (skip-this-tick) instead of each spawning a fresh abandoned goroutine.
// Pre-fix, every tick spent against a persistent wedge leaked another
// goroutine — with a 1-second poll and a persistent wedge that was
// ~3600 leaked goroutines per hour.
func TestDaemonPool_Saturates(t *testing.T) {
	original := tickInstanceTimeout
	tickInstanceTimeout = 20 * time.Millisecond
	defer func() { tickInstanceTimeout = original }()

	everyN := log.NewEvery(time.Millisecond) // allow warn log to fire immediately for test
	pool := newDaemonPool(2)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	// Saturate the pool with two goroutines that never return until release.
	go pool.runWithTimeout("wedge-1", func() { <-release }, everyN)
	go pool.runWithTimeout("wedge-2", func() { <-release }, everyN)

	// Give the two wedged goroutines a moment to acquire their slots.
	require.Eventually(t, func() bool {
		return len(pool.sem) == 2
	}, time.Second, 5*time.Millisecond, "both wedged goroutines should occupy pool slots")

	// A third call with the pool saturated must return immediately
	// (saturation skip), NOT wait for the tick timeout.
	start := time.Now()
	pool.runWithTimeout("would-be-third", func() {
		t.Fatal("saturation skip path must not execute the work function")
	}, everyN)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 10*time.Millisecond,
		"saturated pool must skip immediately, not wait for tickInstanceTimeout; elapsed=%s", elapsed)
}

// TestDaemonPool_GoroutineCountStable guards the underlying invariant
// behind Saturates: many saturation-attempt iterations must not grow
// the process goroutine count. Before Phase 2.2, each tick against a
// wedged instance leaked a goroutine; with the semaphore cap, the
// count tops out at the pool capacity and stays there.
func TestDaemonPool_GoroutineCountStable(t *testing.T) {
	original := tickInstanceTimeout
	tickInstanceTimeout = 10 * time.Millisecond
	defer func() { tickInstanceTimeout = original }()

	everyN := log.NewEvery(time.Second)
	const cap = 3
	pool := newDaemonPool(cap)
	release := make(chan struct{})

	// Saturate the pool once.
	var wg sync.WaitGroup
	for i := 0; i < cap; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool.runWithTimeout("wedge", func() { <-release }, everyN)
		}()
	}
	require.Eventually(t, func() bool {
		return len(pool.sem) == cap
	}, time.Second, 5*time.Millisecond, "pool should fill to capacity")

	runtime.GC()
	baseline := runtime.NumGoroutine()

	// Fire many additional saturation-attempts. Each must hit the
	// skip-this-tick path and exit synchronously.
	const attempts = 200
	var skipped atomic.Int32
	for i := 0; i < attempts; i++ {
		pool.runWithTimeout("skip-me", func() {
			t.Fatal("work must not execute while pool is saturated")
		}, everyN)
		skipped.Add(1)
	}

	runtime.GC()
	after := runtime.NumGoroutine()

	require.Equal(t, int32(attempts), skipped.Load())
	// Allow a small fudge factor for scheduler-internal goroutines; the
	// key assertion is that we didn't leak ~attempts new ones.
	require.LessOrEqual(t, after, baseline+5,
		"goroutine count must not grow per skipped tick; baseline=%d after=%d", baseline, after)

	// Cleanup happens via the t.Cleanup close(release) above and
	// pool.drain; wait for wedged goroutines to finish so the test
	// does not leave them for subsequent tests.
	close(release)
	wg.Wait()
}

// TestDaemonPool_Drain_TimesOut exercises the shutdown path: when work
// does not return, drain must still bound its wait by the caller's
// budget so daemon shutdown stays prompt.
func TestDaemonPool_Drain_TimesOut(t *testing.T) {
	original := tickInstanceTimeout
	tickInstanceTimeout = time.Second
	defer func() { tickInstanceTimeout = original }()

	everyN := log.NewEvery(time.Second)
	pool := newDaemonPool(1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	go pool.runWithTimeout("wedge", func() { <-release }, everyN)
	require.Eventually(t, func() bool { return len(pool.sem) == 1 },
		time.Second, 5*time.Millisecond, "wedged goroutine should occupy the slot")

	start := time.Now()
	pool.drain(30 * time.Millisecond)
	elapsed := time.Since(start)

	require.GreaterOrEqual(t, elapsed, 30*time.Millisecond, "drain must honor its budget")
	require.Less(t, elapsed, 500*time.Millisecond,
		"drain must not wait much past its budget; elapsed=%s", elapsed)
}

package daemon

import (
	"claude-squad/log"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRunWithTimeoutFastWorkReturns is the happy path: when the work
// function returns promptly, runWithTimeout should too — no goroutine
// leak, no wasted tick budget. This is the common case on a healthy
// daemon and must not accrue latency.
func TestRunWithTimeoutFastWorkReturns(t *testing.T) {
	everyN := log.NewEvery(time.Second)
	start := time.Now()
	runWithTimeout("fast", func() {}, everyN)
	elapsed := time.Since(start)
	require.Less(t, elapsed, 100*time.Millisecond,
		"fast work must not wait for the timeout, got %s", elapsed)
}

// TestRunWithTimeoutHangingWorkIsBounded drives the F4 fix. Before it,
// one instance's hung HasUpdated or UpdateDiffStats would wedge the
// whole daemon tick loop — every other instance would stop auto-yesing
// until the hang resolved. After the fix, a hung work function causes
// the tick for THAT instance to be abandoned while the rest continue
// to be serviced.
func TestRunWithTimeoutHangingWorkIsBounded(t *testing.T) {
	original := tickInstanceTimeout
	tickInstanceTimeout = 50 * time.Millisecond
	defer func() { tickInstanceTimeout = original }()

	everyN := log.NewEvery(time.Second)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	runWithTimeout("stuck", func() { <-release }, everyN)
	elapsed := time.Since(start)

	require.GreaterOrEqual(t, elapsed, 50*time.Millisecond,
		"runWithTimeout must honor the configured timeout")
	require.Less(t, elapsed, 500*time.Millisecond,
		"runWithTimeout must not wait significantly beyond the timeout, got %s", elapsed)
}

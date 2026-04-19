package daemon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestRunBoundedParallel_FanOutBeatsSerial asserts the core Phase 5
// property: if we have N workers each sleeping D, wall time is ~(N/max)*D
// not N*D. A blocking instance must not starve the rest.
func TestRunBoundedParallel_FanOutBeatsSerial(t *testing.T) {
	const n = 8
	const max = 4
	const sleep = 50 * time.Millisecond

	var active atomic.Int32
	var peak atomic.Int32
	start := time.Now()
	runBoundedParallel(n, max, func(i int) {
		cur := active.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(sleep)
		active.Add(-1)
	})
	elapsed := time.Since(start)

	// With max=4 and n=8, two batches run sequentially → ~2*sleep.
	// Serial would be 8*sleep. Allow generous upper bound for CI jitter.
	assert.Less(t, elapsed, 6*sleep,
		"bounded-parallel must be significantly faster than serial")
	assert.LessOrEqual(t, peak.Load(), int32(max),
		"concurrency must not exceed max")
	assert.Greater(t, peak.Load(), int32(1),
		"real parallelism must occur (else test proves nothing)")
}

// TestRunBoundedParallel_SlowWorkerDoesNotBlockOthers simulates the
// daemon's core concern: one instance with a stalled git diff must not
// delay prompt detection on other instances.
func TestRunBoundedParallel_SlowWorkerDoesNotBlockOthers(t *testing.T) {
	// 4 workers, 4 slots. Worker 0 blocks. Workers 1..3 should finish
	// while 0 is still blocked — we observe this by counting completions
	// before releasing worker 0.
	release := make(chan struct{})
	var finished atomic.Int32
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		runBoundedParallel(4, 4, func(i int) {
			if i == 0 {
				<-release
			}
			finished.Add(1)
		})
	}()

	// Poll until workers 1-3 report done. 500ms is plenty — they do no I/O.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && finished.Load() < 3 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.GreaterOrEqual(t, finished.Load(), int32(3),
		"fast workers must complete while slow worker blocks")

	close(release)
	wg.Wait()
	assert.EqualValues(t, 4, finished.Load())
}

// TestRunBoundedParallel_ZeroIsNoop guards the edge case: a tick with
// zero eligible instances must not spin up a goroutine or block.
func TestRunBoundedParallel_ZeroIsNoop(t *testing.T) {
	var called atomic.Int32
	runBoundedParallel(0, 4, func(i int) { called.Add(1) })
	assert.EqualValues(t, 0, called.Load())
}

package tmux

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// notifierGuard swaps in a test notifier and restores the previous one,
// so parallel-package state never leaks across tests.
func notifierGuard(t *testing.T, n Notifier) {
	t.Helper()
	SetNotifier(n)
	t.Cleanup(func() { SetNotifier(Notifier{}) })
}

func TestCoalescer_FirstTouchFiresImmediately(t *testing.T) {
	var dirty atomic.Int32
	var got atomic.Value
	notifierGuard(t, Notifier{Output: func(s string) {
		got.Store(s)
		dirty.Add(1)
	}})

	c := newCoalescer("sess1")
	defer c.stop()
	c.touch()
	// Well inside the 16ms coalesce window would be the measure on an idle
	// machine; a second leaves room for a saturated one.
	require.Eventually(t, func() bool { return dirty.Load() == 1 }, time.Second, time.Millisecond,
		"the first touch after idle fires at once, without waiting for the coalesce window")
	require.Equal(t, "sess1", got.Load())
}

// The writer is the output pump, and the notifier is tea.Program.Send, which
// blocks until the TUI's Update returns. Update can be waiting for that very
// pump to stop (PausePreview, Restore), so the pump must never wait on the
// notifier: a touch hands the event to another goroutine.
func TestCoalescer_TouchNeverWaitsOnTheNotifier(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	notifierGuard(t, Notifier{Output: func(string) {
		entered <- struct{}{}
		<-release
	}})
	c := newCoalescer("sess1")
	t.Cleanup(c.stop)
	t.Cleanup(func() { close(release) })

	done := make(chan struct{})
	go func() {
		c.touch()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("touch waited on a notifier that blocks")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("the event never reached the notifier")
	}
}

func TestCoalescer_BurstCoalescesToTrailingEvent(t *testing.T) {
	var dirty atomic.Int32
	notifierGuard(t, Notifier{Output: func(string) { dirty.Add(1) }})

	c := newCoalescer("sess1")
	defer c.stop()
	// A tight burst: one immediate leading event, then at most one trailing
	// event per coalesce window. 100 touches inside ~1ms must NOT mean 100
	// events.
	for i := 0; i < 100; i++ {
		c.touch()
	}
	require.LessOrEqual(t, dirty.Load(), int32(2), "burst must coalesce (leading + at most one armed trailing)")
	// The trailing timer eventually fires so the final state is rendered.
	require.Eventually(t, func() bool { return dirty.Load() >= 2 }, time.Second, time.Millisecond,
		"trailing event must fire after the burst")
}

func TestCoalescer_QuietFiresOnceAfterDelay(t *testing.T) {
	var quiet atomic.Int32
	notifierGuard(t, Notifier{Quiet: func(s string) {
		require.Equal(t, "sess1", s)
		quiet.Add(1)
	}})

	c := newCoalescer("sess1")
	defer c.stop()
	c.touch()
	require.Equal(t, int32(0), quiet.Load(), "quiet must not fire immediately")
	require.Eventually(t, func() bool { return quiet.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
	// It fires once per burst, not repeatedly.
	time.Sleep(2 * quietDelay)
	require.Equal(t, int32(1), quiet.Load(), "quiet fires once per burst")
}

func TestCoalescer_TouchReArmsQuiet(t *testing.T) {
	var quiet atomic.Int32
	notifierGuard(t, Notifier{Quiet: func(string) { quiet.Add(1) }})

	c := newCoalescer("sess1")
	defer c.stop()
	// Keep touching more often than quietDelay: quiet must not fire.
	for i := 0; i < 5; i++ {
		c.touch()
		time.Sleep(quietDelay / 4)
	}
	require.Equal(t, int32(0), quiet.Load(), "quiet must not fire while output keeps arriving")
	require.Eventually(t, func() bool { return quiet.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
}

func TestCoalescer_StopPreventsFiring(t *testing.T) {
	var fired atomic.Int32
	notifierGuard(t, Notifier{
		Output: func(string) { fired.Add(1) },
		Quiet:  func(string) { fired.Add(1) },
	})

	c := newCoalescer("sess1")
	c.touch() // leading dirty fires: 1
	require.Eventually(t, func() bool { return fired.Load() == 1 }, time.Second, time.Millisecond)
	c.touch() // arms trailing timer
	c.stop()
	before := fired.Load()
	time.Sleep(2 * quietDelay)
	require.Equal(t, before, fired.Load(), "no timer may fire after stop")
	require.NotPanics(t, func() { c.touch() }, "touch after stop is a no-op")
}

func TestCoalescer_NoNotifierIsSafe(t *testing.T) {
	SetNotifier(Notifier{})
	c := newCoalescer("sess1")
	defer c.stop()
	require.NotPanics(t, func() {
		for i := 0; i < 10; i++ {
			c.touch()
		}
	})
}

func TestSetNotifier_ConcurrentAccessIsSafe(t *testing.T) {
	var wg sync.WaitGroup
	c := newCoalescer("sess1")
	defer c.stop()
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); SetNotifier(Notifier{Output: func(string) {}}) }()
		go func() { defer wg.Done(); c.touch() }()
	}
	wg.Wait()
	SetNotifier(Notifier{})
}

// stalledNotifier is a notifier whose Output and Quiet block until release
// closes, as tea.Program.Send does while the TUI's Update is stalled (a
// terminal that stopped reading holds the renderer, and every Update waits
// on it). It records how many senders are parked in it at once.
type stalledNotifier struct {
	release             chan struct{}
	outputs, quiets     atomic.Int32
	parked, maxParked   atomic.Int32
	parkedQ, maxParkedQ atomic.Int32
}

func newStalledNotifier(t *testing.T) *stalledNotifier {
	t.Helper()
	s := &stalledNotifier{release: make(chan struct{})}
	park := func(n, max *atomic.Int32) {
		v := n.Add(1)
		for {
			m := max.Load()
			if v <= m || max.CompareAndSwap(m, v) {
				break
			}
		}
		<-s.release
		n.Add(-1)
	}
	notifierGuard(t, Notifier{
		Output: func(string) { s.outputs.Add(1); park(&s.parked, &s.maxParked) },
		Quiet:  func(string) { s.quiets.Add(1); park(&s.parkedQ, &s.maxParkedQ) },
	})
	return s
}

// On 2026-11-24 a TUI whose terminal stopped reading parked a goroutine per
// pane per dirty event (about 62 a second from each streaming pane), with
// nothing to bound them. While a send is in flight, later events only mark
// one owed.
func TestCoalescer_AStalledNotifierHoldsOneOutputSender(t *testing.T) {
	s := newStalledNotifier(t)
	c := newCoalescer("sess1")
	t.Cleanup(c.stop)
	released := false
	t.Cleanup(func() {
		if !released {
			close(s.release)
		}
	})

	// Output streaming for ten coalesce windows and more.
	deadline := time.Now().Add(20 * dirtyCoalesceInterval)
	for time.Now().Before(deadline) {
		c.touch()
		time.Sleep(time.Millisecond)
	}
	time.Sleep(3 * dirtyCoalesceInterval) // the last trailing timer fires
	require.Equal(t, int32(1), s.maxParked.Load(), "one sender parked, however long Update is stalled")
	require.Equal(t, int32(1), s.outputs.Load())

	// Update takes the parked event, then the one owed since: the pane's
	// last output still renders.
	close(s.release)
	released = true
	require.Eventually(t, func() bool { return s.outputs.Load() == 2 && s.parked.Load() == 0 }, time.Second, time.Millisecond,
		"the event owed while the send was in flight is sent once it lands")
	time.Sleep(3 * dirtyCoalesceInterval)
	require.Equal(t, int32(2), s.outputs.Load(), "the owed events of a stall are one event, not one each")
}

func TestCoalescer_AStalledNotifierHoldsOneQuietSender(t *testing.T) {
	s := newStalledNotifier(t)
	c := newCoalescer("sess1")
	t.Cleanup(c.stop)
	released := false
	t.Cleanup(func() {
		if !released {
			close(s.release)
		}
	})

	// Two bursts, each followed by silence long enough for Quiet.
	for range 2 {
		c.touch()
		time.Sleep(quietDelay + 150*time.Millisecond)
	}
	require.Equal(t, int32(1), s.maxParkedQ.Load(), "one quiet sender parked across bursts")
	require.Equal(t, int32(1), s.quiets.Load())

	close(s.release)
	released = true
	require.Eventually(t, func() bool { return s.quiets.Load() == 2 && s.parkedQ.Load() == 0 }, time.Second, time.Millisecond,
		"the quiet owed while the first was in flight is sent once it lands")
}

func TestCoalescer_StopDropsWhatAStalledSenderOwes(t *testing.T) {
	s := newStalledNotifier(t)
	c := newCoalescer("sess1")
	c.touch()
	require.Eventually(t, func() bool { return s.parked.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(2 * dirtyCoalesceInterval)
	c.touch() // owed while the first is in flight
	time.Sleep(3 * dirtyCoalesceInterval)
	c.stop()
	close(s.release)
	require.Eventually(t, func() bool { return s.parked.Load() == 0 }, time.Second, time.Millisecond)
	time.Sleep(3 * dirtyCoalesceInterval)
	require.Equal(t, int32(1), s.outputs.Load(), "a stopped pump's session sends nothing more")
}

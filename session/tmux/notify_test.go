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

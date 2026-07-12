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
	notifierGuard(t, Notifier{Output: func(s string) {
		require.Equal(t, "sess1", s)
		dirty.Add(1)
	}})

	c := newCoalescer("sess1")
	defer c.stop()
	c.touch()
	require.Equal(t, int32(1), dirty.Load(), "first touch after idle must fire synchronously")
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

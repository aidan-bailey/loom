package tmux

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/vt"
	"github.com/stretchr/testify/require"
)

func pumpFixture(t *testing.T, name string, withEmu bool) (*TmuxSession, *os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })
	ts := NewTmuxSession(name, "prog")
	if withEmu {
		ts.SetEmulatorForTest(vt.NewXVT(80, 24))
	}
	return ts, r, w
}

func TestPump_OutputFiresDirtyAndQuiet(t *testing.T) {
	ts, r, w := pumpFixture(t, "pump-dirty", true)

	var dirty, quiet atomic.Int32
	notifierGuard(t, Notifier{
		Output: func(s string) {
			require.Equal(t, ts.SessionName(), s)
			dirty.Add(1)
		},
		Quiet: func(string) { quiet.Add(1) },
	})

	ts.startOutputPump(r)
	t.Cleanup(func() { ts.signalPumpStop(r); ts.waitPumpExit() })

	_, _ = w.WriteString("hello")
	require.Eventually(t, func() bool { return dirty.Load() >= 1 }, time.Second, time.Millisecond,
		"pump output must fire a dirty event")
	require.Eventually(t, func() bool { return quiet.Load() == 1 }, 2*time.Second, 5*time.Millisecond,
		"quiet must fire after the pane settles")
}

func TestPump_NoEmulatorNoEvents(t *testing.T) {
	// Snapshot mode (nil emulator) keeps the legacy tick path — the pump
	// must not emit events for it.
	var fired atomic.Int32
	notifierGuard(t, Notifier{
		Output: func(string) { fired.Add(1) },
		Dead:   func(string) { fired.Add(1) },
	})

	ts, r, w := pumpFixture(t, "pump-snap", false)
	ts.startOutputPump(r)
	_, _ = w.WriteString("hello")
	time.Sleep(50 * time.Millisecond)
	_ = w.Close() // EOF → pump exits
	ts.waitPumpExit()
	require.Equal(t, int32(0), fired.Load(), "no emulator → no events")
}

func TestPump_EOFFiresDead(t *testing.T) {
	ts, r, w := pumpFixture(t, "pump-dead", true)

	var dead atomic.Int32
	notifierGuard(t, Notifier{Dead: func(s string) {
		require.Equal(t, ts.SessionName(), s)
		dead.Add(1)
	}})

	ts.startOutputPump(r)

	_ = w.Close() // genuine EOF, pump ctx still live
	require.Eventually(t, func() bool { return dead.Load() == 1 }, time.Second, time.Millisecond,
		"PTY EOF must fire exactly one Dead event")
}

func TestPump_DeliberateStopDoesNotFireDead(t *testing.T) {
	var dead atomic.Int32
	notifierGuard(t, Notifier{Dead: func(string) { dead.Add(1) }})

	ts, r, _ := pumpFixture(t, "pump-stop", true)
	ts.startOutputPump(r)
	time.Sleep(10 * time.Millisecond)

	ts.signalPumpStop(r) // Close/Restore/PausePreview path: ctx cancelled first
	ts.waitPumpExit()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), dead.Load(), "deliberate pump stop must not report the session dead")
}

func TestSessionAccessors(t *testing.T) {
	ts, _, _ := pumpFixture(t, "acc", true)
	require.Equal(t, TmuxPrefix+"acc", ts.SessionName())
	require.True(t, ts.HasEmulator())
	ts2 := NewTmuxSession("acc2", "prog")
	require.False(t, ts2.HasEmulator())
}

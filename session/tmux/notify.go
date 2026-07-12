package tmux

import (
	"sync"
	"time"
)

// Notifier carries the app's sinks for pump-driven pane events. All funcs are
// optional (nil = dropped). They are invoked from pump/timer goroutines, so
// implementations must be goroutine-safe — in practice each one wraps
// tea.Program.Send, which is designed for exactly this.
type Notifier struct {
	// Output fires when a pane emitted output, coalesced to at most one
	// event per dirtyCoalesceInterval per session (leading + trailing edge).
	Output func(session string)
	// Quiet fires once when a pane has emitted nothing for quietDelay after
	// a burst — the app runs status/prompt detection on settled content.
	Quiet func(session string)
	// Bell fires when the pane's emulator receives BEL. Never coalesced.
	Bell func(session string)
	// Dead fires when the output pump exits on a genuine read error (PTY
	// EOF/close) — NOT on deliberate stops (Close/Restore/PausePreview).
	Dead func(session string)
}

var (
	notifierMu sync.RWMutex
	notifier   Notifier
)

// SetNotifier installs the process-wide pane-event notifier. Called once at
// app startup (and with the zero value to tear down). A package-level hook —
// rather than per-session constructor plumbing — because ui/terminal.go
// creates its own TmuxSessions outside app's reach.
func SetNotifier(n Notifier) {
	notifierMu.Lock()
	notifier = n
	notifierMu.Unlock()
}

func currentNotifier() Notifier {
	notifierMu.RLock()
	defer notifierMu.RUnlock()
	return notifier
}

// dirtyCoalesceInterval bounds dirty events to ~60/s per session: fast enough
// that a single keystroke's echo renders on arrival, cheap enough that a
// `yes`-spammy pane cannot melt the Update loop.
const dirtyCoalesceInterval = 16 * time.Millisecond

// quietDelay is how long a pane must stay silent after a burst before the
// Quiet event fires. Matches the old 500ms metadata tick cadence, so
// Prompting/Ready detection latency is no worse than before.
const quietDelay = 500 * time.Millisecond

// coalescer rate-limits one session's dirty notifications and schedules its
// quiet notification. One coalescer per output pump; stop() on pump exit.
type coalescer struct {
	session string

	mu         sync.Mutex
	lastDirty  time.Time
	dirtyTimer *time.Timer // armed trailing-edge dirty; nil when none pending
	quietTimer *time.Timer // re-armed on every touch
	stopped    bool
}

func newCoalescer(session string) *coalescer {
	return &coalescer{session: session}
}

// touch records that output arrived. Fires Output immediately when the last
// event is older than dirtyCoalesceInterval, otherwise arms one trailing
// timer; always re-arms the quiet timer.
func (c *coalescer) touch() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	if c.quietTimer != nil {
		c.quietTimer.Stop()
	}
	c.quietTimer = time.AfterFunc(quietDelay, c.fireQuiet)

	now := time.Now()
	if now.Sub(c.lastDirty) >= dirtyCoalesceInterval {
		c.lastDirty = now
		c.mu.Unlock()
		if f := currentNotifier().Output; f != nil {
			f(c.session)
		}
		return
	}
	if c.dirtyTimer == nil {
		c.dirtyTimer = time.AfterFunc(dirtyCoalesceInterval-now.Sub(c.lastDirty), c.fireDirty)
	}
	c.mu.Unlock()
}

func (c *coalescer) fireDirty() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.dirtyTimer = nil
	c.lastDirty = time.Now()
	c.mu.Unlock()
	if f := currentNotifier().Output; f != nil {
		f(c.session)
	}
}

func (c *coalescer) fireQuiet() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.quietTimer = nil
	c.mu.Unlock()
	if f := currentNotifier().Quiet; f != nil {
		f(c.session)
	}
}

// stop cancels pending timers and makes all future touches no-ops. Must be
// called when the pump exits so a torn-down session cannot Send into a dead
// program.
func (c *coalescer) stop() {
	c.mu.Lock()
	c.stopped = true
	if c.dirtyTimer != nil {
		c.dirtyTimer.Stop()
		c.dirtyTimer = nil
	}
	if c.quietTimer != nil {
		c.quietTimer.Stop()
		c.quietTimer = nil
	}
	c.mu.Unlock()
}

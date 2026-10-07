package app

import (
	"context"
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/internal/takeover"
	"github.com/aidan-bailey/loom/log"
)

// takeoverMsg is another loom asking this one to save and quit, so it can
// run on the same workspaces without the two overwriting each other's
// sessions (see internal/takeover).
type takeoverMsg struct{ by takeover.Holder }

// handleTakeover saves like q and quits, recording who took over for the
// line Run prints once the TUI is gone. A failed save keeps this loom
// running, as it keeps a failed q: the requester times out waiting for
// the lock and says so.
func (m *home) handleTakeover(msg takeoverMsg) (tea.Model, tea.Cmd) {
	log.For("app").Info("takeover.requested", "by_pid", msg.by.PID, "by_tty", msg.by.TTY)
	if err := m.saveForQuit(); err != nil {
		return m, m.handleError(fmt.Errorf("another loom (%s) asked to take over, but saving failed, so this one keeps running: %w", msg.by, err))
	}
	by := msg.by
	m.takenOverBy = &by
	return m, tea.Quit
}

// takeoverListener is what the lock's listener runs, on its own goroutine,
// for each request: it ends a full-screen attach first, because the
// event loop is blocked in it and would not see the request until the
// user detached.
func takeoverListener(fs *foregroundAttach, send func(tea.Msg)) func(takeover.Holder) {
	return func(by takeover.Holder) {
		fs.end()
		send(takeoverMsg{by: by})
	}
}

// foregroundAttach holds the cancel of the full-screen attach running in
// the foreground, if any. Update sets it and the takeover listener's
// goroutine ends it, hence the lock. Nil-safe: bare test homes have none.
type foregroundAttach struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// set records the attach now in the foreground (nil when it returned),
// releasing the previous one's context.
func (f *foregroundAttach) set(cancel context.CancelFunc) {
	if f == nil {
		return
	}
	f.mu.Lock()
	prev := f.cancel
	f.cancel = cancel
	f.mu.Unlock()
	if prev != nil {
		prev()
	}
}

// end ends the attach in the foreground, if any: its tmux client exits and
// the session keeps running.
func (f *foregroundAttach) end() {
	if f == nil {
		return
	}
	f.mu.Lock()
	cancel := f.cancel
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

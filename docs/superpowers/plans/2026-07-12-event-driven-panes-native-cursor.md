# Event-Driven Panes + Native Cursor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Loom's timer-polled pane rendering and subprocess-based status detection with output-driven events, then render the host terminal's real hardware cursor (plus title/bell/focus pass-through) in the focused pane.

**Architecture:** The PTY output pump (already event-driven) gains a per-session coalescer that pushes `paneDirtyMsg`/`paneQuietMsg`/`ptyDeadMsg` into Bubble Tea via `program.Send`. Renders and status detection react to those messages; polling demotes to a 3s health tick. x/vt emulator callbacks (cursor visibility/style, title, bell, mode 1004) feed the native-terminal features; `tea.View.Cursor` positions the hardware cursor. Windows / `LOOM_PANE_RENDERER=snapshot` keeps the legacy tick path untouched.

**Tech Stack:** Go 1.23, bubbletea v2 (`tea.View.Cursor`, `program.Send`), `charmbracelet/x/vt` (`Callbacks`), lipgloss v2, testify.

**Spec:** `docs/superpowers/specs/2026-07-12-event-driven-panes-native-cursor-design.md`

**Commands used throughout:**
- Run package tests: `CGO_ENABLED=0 go test ./session/tmux/ -run <Name> -v` (repo defaults CGO off; plain `go test` needs `CGO_ENABLED=0`)
- Race suite (before each phase's final commit): `CC=clang CGO_ENABLED=1 go test -race ./...`
- Format: `gofmt -w .` — CI enforces it; run before every commit.

**Codebase invariants to respect (from CLAUDE.md):**
- No model mutation from `tea.Cmd` goroutines — all reactions to Send'd messages happen in `Update`.
- `TmuxSession.stateMu` snapshot-then-use: never hold it across PTY/subprocess I/O.
- x/vt callbacks fire inside `emu.Write`/`Resize` while the xvt wrapper's write lock is held — callbacks must only set wrapper fields, never lock `e.mu` again (RWMutex is not reentrant), never call app code.

---

## File Structure

**New files:**
- `session/tmux/notify.go` — `Notifier` registry + per-session `coalescer` (dirty/quiet timers)
- `session/tmux/notify_test.go`
- `app/events.go` — event message types + routing/handling helpers
- `app/events_test.go`
- `ui/cursor.go` — `SplitPane.CursorScreenPosition` + pure geometry helper
- `ui/cursor_test.go`
- `session/vt/xvt_state_test.go` — cursor/title/mode/bell callback state tests

**Modified files:**
- `session/tmux/tmux.go` — pump wiring, `SessionName`/`HasEmulator`/`CursorState`/`PaneTitle`/`ForwardFocus`/`SetEmulatorForTest`, emulator-backed `statusContent`
- `session/tmux/emulator_unix.go` / `emulator_windows.go` — `EmulatorEnabled()`
- `session/vt/vt.go` — extended `Cursor`, interface additions
- `session/vt/xvt.go` — callback registration + state fields
- `session/instance.go` — `TmuxSessionName`/`HasEmulator`/`CursorState`/`PaneTitle`/`ForwardFocus`/bell flag
- `app/app.go` — Init/Update/View changes, health tick, gather changes
- `ui/split_pane.go` — `CurrentTerminalSessionName`, `ForwardTerminalFocus`
- `ui/preview.go`, `ui/terminal.go` — `ShowingFallback`, terminal `CursorState`/`ForwardFocus`/`CurrentSessionName`
- `ui/list.go` — bell badge
- `CLAUDE.md`, `USAGE.md` — docs

---

## Phase 1 — Event backbone

### Task 1: Notifier registry + coalescer

**Files:**
- Create: `session/tmux/notify.go`
- Test: `session/tmux/notify_test.go`

- [ ] **Step 1: Write the failing tests**

Create `session/tmux/notify_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestCoalescer|TestSetNotifier' -v`
Expected: FAIL — `undefined: Notifier`, `undefined: newCoalescer`, etc.

- [ ] **Step 3: Implement `session/tmux/notify.go`**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestCoalescer|TestSetNotifier' -v`
Expected: PASS (all 7)

- [ ] **Step 5: Race-check the new code and commit**

```bash
CC=clang CGO_ENABLED=1 go test -race ./session/tmux/ -run 'TestCoalescer|TestSetNotifier'
gofmt -w session/tmux/
git add session/tmux/notify.go session/tmux/notify_test.go
git commit -m "feat(tmux): pane-event notifier registry and per-session coalescer"
```

---

### Task 2: Pump wiring — dirty/quiet on output, dead on EOF

**Files:**
- Modify: `session/tmux/tmux.go` (`startOutputPump` at ~line 490; add accessors near `RenderEmulator` at ~line 860)
- Modify: `session/tmux/emulator_unix.go`, `session/tmux/emulator_windows.go`
- Test: `session/tmux/notify_pump_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `session/tmux/notify_pump_test.go` (mirrors the `os.Pipe` stand-in pattern from `tmux_pump_test.go` / `emulator_test.go`):

```go
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
	var dirty, quiet atomic.Int32
	notifierGuard(t, Notifier{
		Output: func(s string) {
			require.Equal(t, TmuxPrefix+"pump-dirty", s)
			dirty.Add(1)
		},
		Quiet: func(string) { quiet.Add(1) },
	})

	ts, r, w := pumpFixture(t, "pump-dirty", true)
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
	var dead atomic.Int32
	notifierGuard(t, Notifier{Dead: func(s string) {
		require.Equal(t, TmuxPrefix+"pump-dead", s)
		dead.Add(1)
	}})

	ts, r, w := pumpFixture(t, "pump-dead", true)
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
```

Note: `NewTmuxSession` prefixes names with `TmuxPrefix` via its sanitizer — if the existing tests show unprefixed `sanitizedName` for these fixtures (check `TestSessionAccessors` failure output), drop the `TmuxPrefix+` from the expectations; the invariant under test is "the event carries exactly `ts.SessionName()`", not the prefix itself. Adjust the two `require.Equal` name assertions accordingly.

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run 'TestPump_|TestSessionAccessors' -v`
Expected: FAIL — `undefined: SetEmulatorForTest`, `undefined: SessionName`, etc.

- [ ] **Step 3: Implement pump wiring and accessors in `session/tmux/tmux.go`**

Add accessors (near `RenderEmulator`, ~line 857):

```go
// SessionName returns the sanitized tmux session name — the identity carried
// by pane events (Notifier callbacks) and used by the app to route them.
func (t *TmuxSession) SessionName() string {
	return t.sanitizedName
}

// HasEmulator reports whether this session renders through the in-process
// emulator (event-driven path) or the legacy capture-pane snapshot path.
func (t *TmuxSession) HasEmulator() bool {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	return t.emu != nil
}

// SetEmulatorForTest wires an emulator directly, bypassing Restore's attach
// lifecycle. Test-only: the name and doc comment are guardrails, nothing
// about the method enforces test-only use.
func (t *TmuxSession) SetEmulatorForTest(emu vt.Emulator) {
	t.stateMu.Lock()
	t.emu = emu
	t.stateMu.Unlock()
}
```

Modify `startOutputPump` (~line 490). The current body reads `emu` under `stateMu` to pick `dest`; extend it to create a coalescer when an emulator is present, touch it on every write, and fire Dead on non-deliberate exit:

```go
func (t *TmuxSession) startOutputPump(ptmx *os.File) {
	ctx, cancel := context.WithCancel(context.Background())
	// Default the pump into the emulator so the visible screen stays current.
	// nil emu (Windows / snapshot kill-switch) keeps the legacy io.Discard drain.
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	var dest io.Writer = io.Discard
	var co *coalescer
	if emu != nil {
		dest = emu
		// Event-driven pane updates ride the pump: dirty/quiet notifications
		// only exist on the emulator path — snapshot mode stays tick-polled.
		co = newCoalescer(t.sanitizedName)
	}
	t.pumpMu.Lock()
	t.pumpDest = dest
	t.pumpCancel = cancel
	t.pumpMu.Unlock()
	t.pumpDone = make(chan struct{})

	go func() {
		defer close(t.pumpDone)
		buf := make([]byte, 4096)
		for {
			if ctx.Err() != nil {
				if co != nil {
					co.stop()
				}
				return
			}
			n, err := ptmx.Read(buf)
			if n > 0 {
				t.pumpMu.Lock()
				dest := t.pumpDest
				t.pumpMu.Unlock()
				_, _ = dest.Write(buf[:n])
				if co != nil {
					co.touch()
				}
			}
			if err != nil {
				if co != nil {
					co.stop()
					// Dead only on a genuine EOF/read failure. Deliberate
					// stops (Close/Restore/PausePreview) cancel ctx first via
					// signalPumpStop, and must not look like a died session.
					if ctx.Err() == nil {
						if f := currentNotifier().Dead; f != nil {
							f(t.sanitizedName)
						}
					}
				}
				return
			}
		}
	}()
}
```

Add `EmulatorEnabled` to `session/tmux/emulator_unix.go`:

```go
// EmulatorEnabled reports whether panes render from the in-process emulator —
// i.e. whether the event-driven update path (pane dirty/quiet/dead events)
// is available. False selects the legacy tick-polled snapshot path.
func EmulatorEnabled() bool {
	return os.Getenv("LOOM_PANE_RENDERER") != "snapshot"
}
```

And to `session/tmux/emulator_windows.go` (which has no emulator at all):

```go
// EmulatorEnabled reports whether the event-driven pane-update path is
// available. Always false on Windows — panes poll via capture-pane.
func EmulatorEnabled() bool {
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -v`
Expected: PASS — including all pre-existing pump/emulator tests (the wiring must not change `io.Discard`-path behavior).

- [ ] **Step 5: Race-check and commit**

```bash
CC=clang CGO_ENABLED=1 go test -race ./session/tmux/
gofmt -w session/tmux/
git add session/tmux/
git commit -m "feat(tmux): emit dirty/quiet/dead pane events from the output pump"
```

---

### Task 3: App event routing — render on dirty, gate the legacy tick

**Files:**
- Create: `app/events.go`
- Modify: `app/app.go` (`Run` ~line 60, `Init` ~line 635, `Update` — `previewTickMsg` case ~line 655, `instanceChanged` ~line 1402)
- Test: `app/events_test.go` (create)

- [ ] **Step 1: Create `app/events.go` with messages and routing**

```go
package app

import (
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
)

// Pane events, Sent from pump/timer goroutines via tea.Program.Send and
// handled exclusively on the Update goroutine (no model mutation off it).

// paneDirtyMsg: a session's pane emitted output (coalesced ≤ ~60/s).
type paneDirtyMsg struct{ session string }

// paneQuietMsg: a session's pane settled (500ms with no output).
type paneQuietMsg struct{ session string }

// ptyDeadMsg: a session's output pump exited on a genuine PTY EOF.
type ptyDeadMsg struct{ session string }

// bellMsg: a session's pane rang BEL.
type bellMsg struct{ session string }

// instanceForSession resolves a tmux session name (as carried by pane
// events) to the owning instance across every workspace slot, or nil for
// terminal-pane sessions and unknown names.
func (m *home) instanceForSession(name string) *session.Instance {
	if name == "" {
		return nil
	}
	check := func(l *ui.List) *session.Instance {
		for _, inst := range l.GetInstances() {
			if inst.TmuxSessionName() == name {
				return inst
			}
		}
		return nil
	}
	if len(m.slots) > 0 {
		for i, slot := range m.slots {
			l := slot.list
			if i == m.focusedSlot {
				l = m.list
			}
			if inst := check(l); inst != nil {
				return inst
			}
		}
		return nil
	}
	return check(m.list)
}

// markDirty records that a session produced output since the last health
// tick — the tick uses this to gate diff-stat refreshes.
func (m *home) markDirty(sessionName string) {
	if m.dirtySessions == nil {
		m.dirtySessions = make(map[string]bool)
	}
	m.dirtySessions[sessionName] = true
}

// takeDirty returns the dirty-set and resets it for the next tick window.
func (m *home) takeDirty() map[string]bool {
	d := m.dirtySessions
	m.dirtySessions = make(map[string]bool)
	return d
}
```

Note: `slot.list` field name — confirm against the slot struct used at `app/app.go:729-735` (`m.slots[i].list`); if the field is exported or named differently, match it.

- [ ] **Step 2: Add the `dirtySessions` field to `home`**

In `app/app.go`, find the `home` struct field block containing `lastPreviewHash` / `lastPreviewTitle` (search `lastPreviewHash []byte`) and add below them:

```go
	// dirtySessions records tmux session names that emitted output since the
	// last health tick (event mode only). Consumed by takeDirty to gate
	// diff-stat refreshes. Update-goroutine only.
	dirtySessions map[string]bool
```

- [ ] **Step 3: Wire the notifier in `Run`**

In `app/app.go` `Run` (~line 74), after `p := tea.NewProgram(h)` and before `p.Run()`:

```go
	p := tea.NewProgram(h) // alt-screen + mouse mode are set on the tea.View (see View())
	// Pane events: the output pumps push dirty/quiet/bell/dead into the
	// program from their own goroutines; Send is goroutine-safe by design.
	// Torn down before Run returns so a late timer can't Send into a dead
	// program (Send after Kill is a no-op, but keep the lifecycle explicit).
	tmux.SetNotifier(tmux.Notifier{
		Output: func(s string) { p.Send(paneDirtyMsg{session: s}) },
		Quiet:  func(s string) { p.Send(paneQuietMsg{session: s}) },
		Bell:   func(s string) { p.Send(bellMsg{session: s}) },
		Dead:   func(s string) { p.Send(ptyDeadMsg{session: s}) },
	})
	defer tmux.SetNotifier(tmux.Notifier{})
	_, err = p.Run()
	return err
```

- [ ] **Step 4: Gate the legacy preview tick to snapshot mode**

In `Init` (~line 635), arm the preview tick only when the emulator path is off:

```go
func (m *home) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.spinner.Tick,
		tickUpdateMetadataCmd,
	}
	// Event mode renders on paneDirtyMsg; the timer poll only survives for
	// the snapshot/Windows path, which has no emulator to emit events.
	if !tmux.EmulatorEnabled() {
		cmds = append(cmds, func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return previewTickMsg{}
		})
	}
	return tea.Batch(cmds...)
}
```

At the top of the `previewTickMsg` case in `Update` (~line 655), add a guard so a stray tick in event mode neither renders nor re-arms:

```go
	case previewTickMsg:
		if tmux.EmulatorEnabled() {
			return m, nil
		}
```

(Leave the rest of the case untouched — it is the snapshot path now. `app/preview_tick_test.go` sets `LOOM_PANE_RENDERER=snapshot`, so it keeps passing unchanged.)

- [ ] **Step 5: Handle `paneDirtyMsg` in `Update`**

Add a new case adjacent to `previewTickMsg`:

```go
	case paneDirtyMsg:
		m.markDirty(msg.session)
		selected := m.list.GetSelectedInstance()

		// Inline-attach liveness (previously checked every preview tick):
		// if the attached pane's session died, ptyDeadMsg handles it; dirty
		// events only ever mean the pane is alive.

		if inst := m.instanceForSession(msg.session); inst != nil {
			// Output arrived → the agent is doing something. Mirrors the old
			// tick's updated→Running transition; Prompting/Ready re-derive on
			// the quiet event once the burst settles.
			st := inst.GetStatus()
			if st == session.Ready || st == session.Prompting {
				if err := inst.TransitionTo(session.Running); err != nil {
					log.For("app").Warn("event.transition_failed", "instance", inst.Title, "to", "Running", "err", err.Error())
				}
				m.updateTabBarStatuses()
			}
			if selected != nil && inst == selected {
				if err := m.splitPane.UpdateAgent(selected); err != nil {
					return m, m.handleError(err)
				}
			}
			return m, nil
		}
		// Not an agent session — the terminal pane's current session renders;
		// dirty events from cached-but-hidden terminal sessions are dropped.
		if selected != nil && msg.session == m.splitPane.CurrentTerminalSessionName() {
			if err := m.splitPane.UpdateTerminal(selected); err != nil {
				return m, m.handleError(err)
			}
		}
		return m, nil
```

Stub the not-yet-implemented cases so the package compiles while Tasks 5–6 land (each returns `(m, nil)`):

```go
	case paneQuietMsg:
		return m, nil // Task 5: status detection on settled content
	case ptyDeadMsg:
		return m, nil // Task 6: verified death handling
	case bellMsg:
		return m, nil // Task 12: attention badge
```

- [ ] **Step 6: Add `CurrentTerminalSessionName` + `TmuxSessionName` accessors**

`ui/split_pane.go` (near `TerminalTmuxSession`, ~line 429):

```go
// CurrentTerminalSessionName returns the tmux session name backing the
// currently displayed terminal pane, or "" if none — the key pane events
// are routed by.
func (s *SplitPane) CurrentTerminalSessionName() string {
	if ts := s.terminal.CurrentTmuxSession(); ts != nil {
		return ts.SessionName()
	}
	return ""
}
```

`session/instance.go` (near `GetContentHash`, ~line 1368):

```go
// TmuxSessionName returns the sanitized tmux session name backing this
// instance, or "" when no session is attached. Pane events carry this name.
func (i *Instance) TmuxSessionName() string {
	ts := i.getTmuxSession()
	if ts == nil {
		return ""
	}
	return ts.SessionName()
}

// HasEmulator reports whether this instance's pane renders through the
// in-process emulator (event-driven path).
func (i *Instance) HasEmulator() bool {
	ts := i.getTmuxSession()
	if ts == nil {
		return false
	}
	return ts.HasEmulator()
}
```

- [ ] **Step 7: Write the event-mode scrolled-agent regression test**

Append to `app/events_test.go` — the event-mode twin of `TestPreviewTickRerendersScrolledAgent` (same fixture, but dirty-event-driven and with an emulator wired so routing matches):

```go
package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPaneDirtyRerendersScrolledAgent: scrolling changes the WINDOW, not the
// live content — a dirty event for the scrolled agent's session must still
// re-render it (capture-pane -S) so new output keeps the anchored view fresh.
func TestPaneDirtyRerendersScrolledAgent(t *testing.T) {
	var historyCaptures int
	inst := startedInstanceWithHistory(t, &historyCaptures)

	// startedInstanceWithHistory pins LOOM_PANE_RENDERER=snapshot for its
	// capture-pane mock; the event path needs the emulator flag ON so the
	// previewTick guard doesn't matter and dirty routing engages.
	t.Setenv("LOOM_PANE_RENDERER", "")
	require.NotEmpty(t, inst.TmuxSessionName())

	m := homeWithAppState(t)
	_ = m.list.AddInstance(inst)
	require.Same(t, inst, m.list.GetSelectedInstance())
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)

	require.NoError(t, m.splitPane.UpdateAgent(inst))
	for i := 0; i < 30; i++ {
		m.splitPane.ScrollAgentUp()
	}
	require.NoError(t, m.splitPane.UpdateAgent(inst))
	require.True(t, m.splitPane.IsAgentInScrollMode())

	before := historyCaptures
	_, _ = m.Update(paneDirtyMsg{session: inst.TmuxSessionName()})
	require.Greater(t, historyCaptures, before,
		"a dirty event must re-render a scrolled agent pane")
	require.True(t, m.splitPane.IsAgentInScrollMode())
}
```

Note: `startedInstanceWithHistory` never wires an emulator — `inst.HasEmulator()` is false but routing does not require one; the dirty handler only needs `TmuxSessionName()` to match. If `homeWithAppState` lives in a `_test.go` with a different name, reuse whatever helper `preview_tick_test.go` uses.

- [ ] **Step 8: Run the app tests**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestPaneDirty|TestPreviewTick' -v`
Expected: PASS for both (snapshot tick test unchanged, new event test green).

Then the full suite: `CGO_ENABLED=0 go test ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
gofmt -w app/ ui/ session/
git add app/ ui/ session/
git commit -m "feat(app): render panes on pump dirty events; gate preview tick to snapshot mode"
```

---

## Phase 2 — In-process status detection + health tick

### Task 4: Emulator-backed status content

**Files:**
- Modify: `session/tmux/tmux.go` (`HasUpdated` ~line 664, `CaptureAndProcess` ~line 684, `CheckAndHandleTrustPrompt` ~line 360)
- Test: `session/tmux/status_content_test.go` (create)

- [ ] **Step 1: Write the failing parity test**

Create `session/tmux/status_content_test.go`:

```go
package tmux

import (
	"testing"

	"github.com/aidan-bailey/loom/session/vt"
	"github.com/stretchr/testify/require"
)

// TestStatusContent_PrefersEmulator: with an emulator wired, status content
// comes from the in-process screen — no capture-pane subprocess.
func TestStatusContent_PrefersEmulator(t *testing.T) {
	ts := NewTmuxSession("status-emu", "claude")
	ts.SetEmulatorForTest(vt.NewXVT(80, 24))
	_, _ = ts.emu.Write([]byte("Do you want to proceed?"))

	content, err := ts.statusContent()
	require.NoError(t, err)
	require.Contains(t, content, "Do you want to proceed?")
}

// TestStatusContent_HasUpdatedParity: HasUpdated's change detection behaves
// identically on emulator content — first call hashes (updated), unchanged
// content is not an update, new content is.
func TestStatusContent_HasUpdatedParity(t *testing.T) {
	ts := NewTmuxSession("status-hash", "claude")
	ts.SetEmulatorForTest(vt.NewXVT(80, 24))

	_, _ = ts.emu.Write([]byte("line one"))
	updated, _ := ts.HasUpdated()
	require.True(t, updated, "first capture is always an update")

	updated, _ = ts.HasUpdated()
	require.False(t, updated, "unchanged screen is not an update")

	_, _ = ts.emu.Write([]byte("\r\nline two"))
	updated, _ = ts.HasUpdated()
	require.True(t, updated, "new output is an update")
}

// TestStatusContent_PromptDetectionParity: pendingPrompt patterns must match
// against emulator-rendered content (which carries SGR escapes, same as
// capture-pane -e output did).
func TestStatusContent_PromptDetectionParity(t *testing.T) {
	ts := NewTmuxSession("status-prompt", "claude")
	ts.SetEmulatorForTest(vt.NewXVT(80, 24))

	// Feed the claude adapter's pending-prompt marker with SGR styling
	// wrapped around it, as a real agent screen would.
	_, _ = ts.emu.Write([]byte("\x1b[1mNo, and tell Claude what to do differently\x1b[0m"))
	_, hasPrompt := ts.HasUpdated()
	require.True(t, hasPrompt, "prompt marker must be detected in emulator content")
}
```

Note: the exact pending-prompt marker lives in the claude adapter (`session/agent/`); check `pendingPrompt` (`tmux.go:396`) → adapter pattern and substitute the real marker string if it differs from the one above. The assertion is the contract; the fixture string must be the adapter's actual pattern.

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestStatusContent -v`
Expected: FAIL — `undefined: statusContent` (and possibly prompt-detection failures).

- [ ] **Step 3: Implement `statusContent` and switch the status paths to it**

In `session/tmux/tmux.go`, add above `HasUpdated` (~line 664):

```go
// statusContent returns the pane content that status detection (update hash,
// pending-prompt, trust-prompt) scans. With an emulator wired it reads the
// in-process visible screen — no subprocess; the capture-pane fallback keeps
// the snapshot/Windows path working. Both sources carry SGR escapes
// (capture-pane is invoked with -e), so downstream pattern scans see the
// same shape of content either way.
func (t *TmuxSession) statusContent() (string, error) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu != nil {
		return emu.Render(), nil
	}
	return t.CapturePaneContent()
}
```

Then replace the `CapturePaneContent()` call in `HasUpdated` (~line 665):

```go
func (t *TmuxSession) HasUpdated() (updated bool, hasPrompt bool) {
	content, err := t.statusContent()
	if err != nil {
		log.For("tmux").Error("capture_pane_failed", "context", "status_monitor", "session", t.sanitizedName, "err", err)
		return false, false
	}
	// ... rest unchanged
```

And in `CaptureAndProcess` (~line 684):

```go
func (t *TmuxSession) CaptureAndProcess() (content string, updated bool, hasPrompt bool, trustHandled bool, err error) {
	content, err = t.statusContent()
	if err != nil {
		return "", false, false, false, fmt.Errorf("capture pane content: %w", err)
	}
	// ... rest unchanged
```

Also check `CheckAndHandleTrustPrompt` (~line 360): if it calls `CapturePaneContent` directly, switch it to `statusContent()` the same way.

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -v`
Expected: PASS — including every pre-existing status-monitor test (they use mocked capture-pane with no emulator, i.e. the fallback branch).

- [ ] **Step 5: Commit**

```bash
gofmt -w session/tmux/
git add session/tmux/
git commit -m "feat(tmux): source status detection from the emulator, capture-pane fallback"
```

---

### Task 5: Quiet-driven status transitions

**Files:**
- Modify: `app/events.go`, `app/app.go` (replace the `paneQuietMsg` stub)
- Test: `app/events_test.go`

- [ ] **Step 1: Write the failing test**

Append to `app/events_test.go`:

```go
// TestPaneQuietRunsStatusDetection: a quiet event on an agent session runs
// CaptureAndProcessStatus and applies Prompting/Ready, mirroring the old
// 500ms metadata tick's transition logic.
func TestPaneQuietRunsStatusDetection(t *testing.T) {
	var historyCaptures int
	inst := startedInstanceWithHistory(t, &historyCaptures)
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	_ = m.list.AddInstance(inst)

	// Quiet handler returns a statusDetectCmd; run it and feed the result
	// message back through Update, as the Bubble Tea runtime would.
	_, cmd := m.Update(paneQuietMsg{session: inst.TmuxSessionName()})
	require.NotNil(t, cmd, "quiet on a live agent session must schedule detection")
	msg := cmd()
	detected, ok := msg.(statusDetectedMsg)
	require.True(t, ok, "detection cmd must return statusDetectedMsg, got %T", msg)
	_, _ = m.Update(detected)
	// The mock capture returns non-prompt content and the first hash counts
	// as an update → instance lands in Running.
	require.Equal(t, session.Running, inst.GetStatus())
}

// TestPaneQuietIgnoresUnknownAndInertSessions guards the drop paths.
func TestPaneQuietIgnoresUnknownAndInertSessions(t *testing.T) {
	m := homeWithAppState(t)
	_, cmd := m.Update(paneQuietMsg{session: "loom_nonexistent"})
	require.Nil(t, cmd, "unknown session → dropped")
}
```

(Imports: add `"github.com/aidan-bailey/loom/session"` to the test file.)

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./app/ -run TestPaneQuiet -v`
Expected: FAIL — `undefined: statusDetectedMsg`.

- [ ] **Step 3: Implement quiet handling**

In `app/events.go`, add:

```go
// statusDetectedMsg carries one instance's settled-content detection result
// back to the Update goroutine (the detection itself runs in a tea.Cmd:
// in-process on the emulator path, but trust-prompt handling can write keys
// to the PTY, and the snapshot fallback shells out — neither belongs on the
// Update goroutine).
type statusDetectedMsg struct {
	instance  *session.Instance
	updated   bool
	hasPrompt bool
	err       error
}

func statusDetectCmd(inst *session.Instance) func() tea.Msg {
	return func() tea.Msg {
		updated, hasPrompt, err := inst.CaptureAndProcessStatus()
		return statusDetectedMsg{instance: inst, updated: updated, hasPrompt: hasPrompt, err: err}
	}
}

// statusEligible reports whether the tick/event pipelines may drive this
// instance's status — the same guard set the metadata tick uses (Recoverable
// placeholders and Loading rows are owned by explicit flows; see the comment
// on the tickUpdateMetadataMessage case).
func statusEligible(inst *session.Instance) bool {
	if inst == nil || !inst.Started() || inst.Paused() {
		return false
	}
	st := inst.GetStatus()
	return st != session.Deleting && st != session.Recoverable && st != session.Loading
}
```

(Imports for `events.go` now include `tea "charm.land/bubbletea/v2"`.)

Replace the `paneQuietMsg` stub in `app/app.go` `Update`:

```go
	case paneQuietMsg:
		inst := m.instanceForSession(msg.session)
		if !statusEligible(inst) {
			return m, nil
		}
		return m, statusDetectCmd(inst)
	case statusDetectedMsg:
		if !statusEligible(msg.instance) {
			return m, nil
		}
		if msg.err != nil {
			log.WarnKV("app.event.capture_failed", "instance", msg.instance.Title, "err", msg.err.Error())
			return m, nil
		}
		// Same transition ladder as the old metadata tick: still-changing →
		// Running; settled with a prompt → Prompting; settled → Ready.
		target := session.Ready
		if msg.updated {
			target = session.Running
		} else if msg.hasPrompt {
			target = session.Prompting
		}
		if err := msg.instance.TransitionTo(target); err != nil {
			log.For("app").Warn("event.transition_failed", "instance", msg.instance.Title, "to", target.String(), "err", err.Error())
		}
		m.updateTabBarStatuses()
		return m, nil
```

Note: if `session.Status` has no `String()` method, log the target with `fmt.Sprintf("%v", target)` — check how the existing tick's transition-failure logs format it and match.

- [ ] **Step 4: Run to verify pass, then full suite**

Run: `CGO_ENABLED=0 go test ./app/ -run TestPaneQuiet -v` → PASS
Run: `CGO_ENABLED=0 go test ./...` → PASS

- [ ] **Step 5: Commit**

```bash
gofmt -w app/
git add app/
git commit -m "feat(app): run status detection on pane quiet events"
```

---

### Task 6: Health-tick demotion + verified PTY-death handling

**Files:**
- Modify: `app/app.go` (`tickUpdateMetadataCmd` ~line 1620, `gatherMetadataCmd` ~line 1634, `metadataReadyMsg` case ~line 762, `ptyDeadMsg` stub)
- Test: `app/events_test.go`

- [ ] **Step 1: Slow the tick in event mode and shed status detection**

Replace `tickUpdateMetadataCmd` (~line 1618):

```go
// tickUpdateMetadataCmd drives the health tick. In event mode (emulator
// path) it is a slow belt-and-braces sweep — liveness, ptmx self-heal, and
// diff stats — because status detection rides pane events instead. On the
// snapshot path it keeps the legacy 500ms cadence and does everything.
var tickUpdateMetadataCmd = func() tea.Msg {
	if tmux.EmulatorEnabled() {
		time.Sleep(3 * time.Second)
	} else {
		time.Sleep(500 * time.Millisecond)
	}
	return tickUpdateMetadataMessage{}
}
```

In the `tickUpdateMetadataMessage` case (~line 725), thread the dirty-set through to the gather (replace the final `return m, gatherMetadataCmd(active, selected)`):

```go
		// Inline-attach liveness backstop (the preview tick used to check
		// this every 100ms; ptyDeadMsg is the fast path now, the tick is
		// the safety net for deaths that never EOF'd the PTY).
		var cmds []tea.Cmd
		if m.state == stateInlineAttach {
			if selected == nil || selected.Paused() || !focusedPaneAlive(m, selected) {
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				cmds = append(cmds, tea.RequestWindowSize)
			}
		}
		cmds = append(cmds, gatherMetadataCmd(active, selected, m.takeDirty()))
		return m, tea.Batch(cmds...)
```

Change `gatherMetadataCmd`'s signature and body (~line 1634):

```go
func gatherMetadataCmd(active []*session.Instance, selected *session.Instance, dirty map[string]bool) tea.Cmd {
	return func() tea.Msg {
		results := make([]metadataResult, len(active))
		var wg sync.WaitGroup
		for i, inst := range active {
			wg.Add(1)
			go func(idx int, instance *session.Instance) {
				defer wg.Done()
				r := &results[idx]
				r.instance = instance

				r.tmuxAlive = instance.TmuxAlive()
				if !r.tmuxAlive {
					return
				}
				r.ptmxAlive = instance.PtmxAlive()

				// Event-mode instances get status from quiet events; the
				// subprocess scan only remains for the snapshot path.
				emulatorDriven := instance.HasEmulator()
				if !emulatorDriven {
					r.updated, r.hasPrompt, r.captureErr = instance.CaptureAndProcessStatus()
				}

				wantFull := instance == selected
				tmuxUpdated := r.updated || dirty[instance.TmuxSessionName()]
				if !instance.ShouldRefreshDiff(tmuxUpdated, wantFull) {
					return
				}
				if wantFull {
					r.diffErr = instance.UpdateDiffStats()
				} else {
					r.diffErr = instance.UpdateDiffStatsShort()
				}
			}(i, inst)
		}
		wg.Wait()
		return metadataReadyMsg{results: results}
	}
}
```

Add an `emulatorDriven` field to `metadataResult` (struct at ~line 79) and set it in the goroutine (`r.emulatorDriven = emulatorDriven`); then in the `metadataReadyMsg` case, skip the status-transition ladder for emulator-driven instances (the `r.updated` / `r.hasPrompt` branch at ~line 808):

```go
				if !r.emulatorDriven {
					if r.updated {
						if err := r.instance.TransitionTo(session.Running); err != nil {
							log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Running", "err", err.Error())
						}
					} else {
						if r.hasPrompt {
							if err := r.instance.TransitionTo(session.Prompting); err != nil {
								log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Prompting", "err", err.Error())
							}
						} else {
							if err := r.instance.TransitionTo(session.Ready); err != nil {
								log.For("app").Warn("tick.transition_failed", "instance", r.instance.Title, "to", "Ready", "err", err.Error())
							}
						}
					}
				}
```

(The `!r.tmuxAlive`, restart-circuit, and `!r.ptmxAlive` repair branches stay exactly as they are — they are the health tick's whole job now.)

- [ ] **Step 2: Extract the liveness application into a shared helper**

The `metadataReadyMsg` handler's per-result liveness block (`!r.tmuxAlive` workspace-restart/pause + `!r.ptmxAlive` repair, ~lines 765–807) is needed verbatim by `ptyDeadMsg`. Extract it into a method on `home` in `app/app.go`, and call it from the loop:

```go
// applyLiveness reacts to one instance's health-probe result: dead tmux →
// pause (or restart a workspace terminal, with the existing circuit
// breaker); live tmux but dead attach PTY → RepairPtmx self-heal. Returns
// false when the instance was found dead (so callers can stop treating it
// as running). Must run on the Update goroutine.
func (m *home) applyLiveness(inst *session.Instance, tmuxAlive, ptmxAlive bool) (alive bool) {
	if !tmuxAlive {
		if inst.IsWorkspaceTerminal {
			if failures := inst.RecordRestartFailure(); failures >= maxWorkspaceTerminalRestartFailures {
				log.For("app").Error("workspace_terminal.restart_circuit_tripped", "title", inst.Title, "consecutive_failures", failures)
				if err := inst.TransitionTo(session.Paused); err != nil {
					log.For("app").Warn("tick.transition_failed", "instance", inst.Title, "to", "Paused", "err", err.Error())
				}
				return false
			}
			log.For("app").Warn("workspace_terminal.tmux_died_restarting", "title", inst.Title)
			if err := inst.Restart(); err != nil {
				log.For("app").Error("workspace_terminal.restart_failed", "title", inst.Title, "err", err)
			}
			return false
		}
		log.For("app").Warn("tick.tmux_gone_marking_paused", "title", inst.Title)
		if err := inst.TransitionTo(session.Paused); err != nil {
			log.For("app").Warn("tick.transition_failed", "instance", inst.Title, "to", "Paused", "err", err.Error())
		}
		return false
	}
	inst.ResetRestartFailures()
	if !ptmxAlive && inst != m.attachingInstance {
		log.For("app").Warn("tick.ptmx_dead_repairing", "title", inst.Title)
		if err := inst.RepairPtmx(); err != nil {
			log.For("app").Error("tick.ptmx_repair_failed", "title", inst.Title, "err", err)
		}
	}
	return true
}
```

The `metadataReadyMsg` loop body becomes:

```go
		for _, r := range msg.results {
			if !m.applyLiveness(r.instance, r.tmuxAlive, r.ptmxAlive) {
				continue
			}
			if !r.emulatorDriven {
				// ... status ladder from Step 1 ...
			}
			if r.captureErr != nil {
				log.WarnKV("app.tick.capture_failed", "instance", r.instance.Title, "err", r.captureErr.Error())
			}
			if r.diffErr != nil {
				log.For("app").Warn("diff_stats_update_failed", "err", r.diffErr)
			}
		}
```

- [ ] **Step 3: Implement `ptyDeadMsg` (replace the stub)**

In `app/events.go`, add the verify cmd + result message:

```go
// deadVerifiedMsg carries the background has-session probe triggered by a
// ptyDeadMsg. A dead attach PTY does not always mean a dead session (a
// failed reattach leaves the session alive), so the probe distinguishes
// pause-the-instance from repair-the-ptmx.
type deadVerifiedMsg struct {
	instance  *session.Instance
	tmuxAlive bool
	ptmxAlive bool
}

func verifyDeadCmd(inst *session.Instance) func() tea.Msg {
	return func() tea.Msg {
		return deadVerifiedMsg{instance: inst, tmuxAlive: inst.TmuxAlive(), ptmxAlive: inst.PtmxAlive()}
	}
}
```

In `app/app.go` `Update`, replace the `ptyDeadMsg` stub:

```go
	case ptyDeadMsg:
		var cmds []tea.Cmd
		// If the dead session backs the inline-attached pane, exit attach
		// immediately (the fast path for what the preview tick used to poll).
		if m.state == stateInlineAttach {
			selected := m.list.GetSelectedInstance()
			if selected == nil || selected.Paused() || !focusedPaneAlive(m, selected) {
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				cmds = append(cmds, tea.RequestWindowSize)
			}
		}
		inst := m.instanceForSession(msg.session)
		if inst == nil || inst == m.attachingInstance || !statusEligible(inst) {
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
			return m, nil
		}
		cmds = append(cmds, verifyDeadCmd(inst))
		return m, tea.Batch(cmds...)
	case deadVerifiedMsg:
		if !statusEligible(msg.instance) {
			return m, nil
		}
		_ = m.applyLiveness(msg.instance, msg.tmuxAlive, msg.ptmxAlive)
		m.updateTabBarStatuses()
		return m, m.instanceChanged()
```

- [ ] **Step 4: Write the regression test**

Append to `app/events_test.go`:

```go
// TestPtyDeadVerifiesBeforePausing: a ptyDeadMsg must probe has-session in a
// Cmd; a still-live session (failed reattach) must NOT be marked Paused.
func TestPtyDeadVerifiesBeforePausing(t *testing.T) {
	var historyCaptures int
	inst := startedInstanceWithHistory(t, &historyCaptures)
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	_ = m.list.AddInstance(inst)

	_, cmd := m.Update(ptyDeadMsg{session: inst.TmuxSessionName()})
	require.NotNil(t, cmd, "dead event on a live instance must schedule verification")
	msg := cmd()
	verified, ok := msg.(deadVerifiedMsg)
	require.True(t, ok, "expected deadVerifiedMsg, got %T", msg)
	// The mock cmdExec answers has-session with success → tmuxAlive true.
	require.True(t, verified.tmuxAlive)
	_, _ = m.Update(verified)
	require.NotEqual(t, session.Paused, inst.GetStatus(),
		"a live session must not be paused by a PTY-death false positive")
}
```

- [ ] **Step 5: Run tests, full suite, race**

```bash
CGO_ENABLED=0 go test ./app/ -v
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
```
Expected: all PASS. (Phase 1+2 are complete and independently shippable here.)

- [ ] **Step 6: Commit**

```bash
gofmt -w app/
git add app/
git commit -m "feat(app): demote metadata poll to 3s health tick; verified PTY-death handling"
```

---

## Phase 3 — Native cursor

### Task 7: Extend vt.Cursor; track visibility/shape/blink in the xvt wrapper

**Files:**
- Modify: `session/vt/vt.go`, `session/vt/xvt.go`
- Test: `session/vt/xvt_state_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `session/vt/xvt_state_test.go`:

```go
package vt

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCursor_DefaultVisibleBlinkingBlock(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	c := e.Cursor()
	require.True(t, c.Visible)
	require.Equal(t, CursorShapeBlock, c.Shape)
	require.True(t, c.Blink)
}

func TestCursor_DECTCEMHideShow(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	_, _ = e.Write([]byte("\x1b[?25l"))
	require.False(t, e.Cursor().Visible, "DECTCEM reset must hide the cursor")
	_, _ = e.Write([]byte("\x1b[?25h"))
	require.True(t, e.Cursor().Visible, "DECTCEM set must show the cursor")
}

func TestCursor_DECSCUSRShapes(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	cases := []struct {
		seq   string
		shape CursorShape
		blink bool
	}{
		{"\x1b[1 q", CursorShapeBlock, true},
		{"\x1b[2 q", CursorShapeBlock, false},
		{"\x1b[3 q", CursorShapeUnderline, true},
		{"\x1b[4 q", CursorShapeUnderline, false},
		{"\x1b[5 q", CursorShapeBar, true},
		{"\x1b[6 q", CursorShapeBar, false},
	}
	for _, tc := range cases {
		_, _ = e.Write([]byte(tc.seq))
		c := e.Cursor()
		require.Equal(t, tc.shape, c.Shape, "seq %q", tc.seq)
		require.Equal(t, tc.blink, c.Blink, "seq %q blink", tc.seq)
	}
}

func TestCursor_TracksPosition(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	_, _ = e.Write([]byte("\x1b[5;10H")) // row 5, col 10 (1-based)
	c := e.Cursor()
	require.Equal(t, 9, c.X)
	require.Equal(t, 4, c.Y)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./session/vt/ -run TestCursor -v`
Expected: FAIL — `undefined: CursorShapeBlock`, `c.Shape`, etc.

- [ ] **Step 3: Extend `session/vt/vt.go`**

```go
// CursorShape is the visual form of the cursor, as set by DECSCUSR.
type CursorShape int

const (
	CursorShapeBlock CursorShape = iota
	CursorShapeUnderline
	CursorShapeBar
)

// Cursor is the visible cursor state in cells, 0-based, origin top-left.
// Visible reflects DECTCEM (apps hide the cursor while painting); Shape and
// Blink reflect DECSCUSR. The defaults — visible blinking block — match a
// fresh terminal.
type Cursor struct {
	X, Y    int
	Visible bool
	Shape   CursorShape
	Blink   bool
}
```

- [ ] **Step 4: Track state in `session/vt/xvt.go`**

Add fields to `xvtEmulator`:

```go
type xvtEmulator struct {
	mu        sync.RWMutex
	term      *xvt.Emulator
	drainDone chan struct{}

	// Callback-fed state. x/vt invokes Callbacks inside term.Write/Resize,
	// while THIS goroutine already holds e.mu's write lock — so callbacks
	// must only assign these fields (never re-lock e.mu: RWMutex is not
	// reentrant; never call out of the package). Readers take RLock.
	cursorVisible bool
	cursorShape   CursorShape
	cursorBlink   bool
}
```

In `NewXVT`, initialize defaults and register callbacks (before the drain goroutine, after constructing `e`):

```go
	e := &xvtEmulator{
		term:          xvt.NewEmulator(cols, rows),
		drainDone:     make(chan struct{}),
		cursorVisible: true,
		cursorShape:   CursorShapeBlock,
		cursorBlink:   true,
	}
	e.term.SetCallbacks(xvt.Callbacks{
		CursorVisibility: func(visible bool) {
			e.cursorVisible = visible
		},
		CursorStyle: func(style xvt.CursorStyle, blink bool) {
			switch style {
			case xvt.CursorUnderline:
				e.cursorShape = CursorShapeUnderline
			case xvt.CursorBar:
				e.cursorShape = CursorShapeBar
			default:
				e.cursorShape = CursorShapeBlock
			}
			e.cursorBlink = blink
		},
	})
```

Replace `Cursor()`:

```go
func (e *xvtEmulator) Cursor() Cursor {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p := e.term.CursorPosition()
	return Cursor{
		X:       p.X,
		Y:       p.Y,
		Visible: e.cursorVisible,
		Shape:   e.cursorShape,
		Blink:   e.cursorBlink,
	}
}
```

**Known gotcha:** x/vt's `screen.go:251` invokes the callback as `s.cb.CursorStyle(style, !blink)` — the second parameter's polarity relative to DECSCUSR odd/even codes is exactly what `TestCursor_DECSCUSRShapes` pins. If the test reports inverted Blink values, negate at the callback (`e.cursorBlink = !blink`) with a comment citing `x/vt screen.go setCursorStyle's !blink`. The test's table (odd codes blink, even codes steady — standard DECSCUSR) is the contract; make the wrapper satisfy it.

- [ ] **Step 5: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session/vt/ -v`
Expected: PASS (including pre-existing xvt tests).

- [ ] **Step 6: Race-check and commit**

```bash
CC=clang CGO_ENABLED=1 go test -race ./session/vt/
gofmt -w session/vt/
git add session/vt/
git commit -m "feat(vt): track cursor visibility, shape, and blink via x/vt callbacks"
```

---

### Task 8: Cursor accessors — TmuxSession, Instance, TerminalPane

**Files:**
- Modify: `session/tmux/tmux.go`, `session/instance.go`, `ui/terminal.go`
- Test: `session/tmux/status_content_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `session/tmux/status_content_test.go`:

```go
func TestCursorState(t *testing.T) {
	ts := NewTmuxSession("cursor", "claude")
	_, ok := ts.CursorState()
	require.False(t, ok, "no emulator → no cursor state")

	ts.SetEmulatorForTest(vt.NewXVT(80, 24))
	_, _ = ts.emu.Write([]byte("\x1b[3;7H"))
	c, ok := ts.CursorState()
	require.True(t, ok)
	require.Equal(t, 6, c.X)
	require.Equal(t, 2, c.Y)
	require.True(t, c.Visible)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./session/tmux/ -run TestCursorState -v`
Expected: FAIL — `undefined: CursorState`.

- [ ] **Step 3: Implement the accessors**

`session/tmux/tmux.go` (near `RenderEmulator`):

```go
// CursorState returns the pane's live cursor (position, visibility, shape,
// blink) from the in-process emulator, or ok=false when no emulator is
// wired (snapshot/Windows path — those panes show no cursor).
func (t *TmuxSession) CursorState() (vt.Cursor, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return vt.Cursor{}, false
	}
	return emu.Cursor(), true
}
```

`session/instance.go` (near `Preview`, ~line 737; add `"github.com/aidan-bailey/loom/session/vt"` to imports):

```go
// CursorState returns the agent pane's live cursor state, or ok=false when
// the instance has no running emulator-backed session.
func (i *Instance) CursorState() (vt.Cursor, bool) {
	if !i.isStarted() || i.GetStatus() == Paused {
		return vt.Cursor{}, false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return vt.Cursor{}, false
	}
	return ts.CursorState()
}
```

`ui/terminal.go` (near `CurrentTmuxSession`, ~line 320; add `"github.com/aidan-bailey/loom/session/vt"` to imports):

```go
// CursorState returns the current terminal session's live cursor state, or
// ok=false when no live emulator-backed session is displayed.
func (t *TerminalPane) CursorState() (vt.Cursor, bool) {
	t.mu.Lock()
	s, ok := t.sessions[t.currentTitle]
	t.mu.Unlock()
	if !ok || s.tmuxSession == nil {
		return vt.Cursor{}, false
	}
	return s.tmuxSession.CursorState()
}
```

- [ ] **Step 4: Run tests and commit**

```bash
CGO_ENABLED=0 go test ./session/tmux/ ./session/ ./ui/
gofmt -w session/ ui/
git add session/ ui/
git commit -m "feat(session,ui): expose emulator cursor state through tmux, instance, terminal pane"
```

---

### Task 9: SplitPane cursor mapping

**Files:**
- Create: `ui/cursor.go`
- Modify: `ui/preview.go`, `ui/terminal.go` (fallback accessors)
- Test: `ui/cursor_test.go` (create)

- [ ] **Step 1: Write the failing geometry tests**

Create `ui/cursor_test.go` (pure-geometry table test, same spirit as `selection_hittest_test.go`):

```go
package ui

import (
	"testing"

	"github.com/aidan-bailey/loom/session/vt"
	"github.com/stretchr/testify/require"
)

// cursorLocal is the inverse of HitTest for a single cell: pane content
// (x, y) → split-local coordinates. Keep this table in sync with HitTest's
// geometry comments (title row at y=0, left border at x=0, terminal content
// starting at agentHeight+3).
func TestCursorLocal_Geometry(t *testing.T) {
	const agentHeight = 20
	cases := []struct {
		name       string
		pane       int
		cx, cy     int
		wantX      int
		wantY      int
	}{
		{"agent origin", FocusAgent, 0, 0, 1, 1},
		{"agent interior", FocusAgent, 12, 5, 13, 6},
		{"terminal origin", FocusTerminal, 0, 0, 1, agentHeight + 3},
		{"terminal interior", FocusTerminal, 7, 2, 8, agentHeight + 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x, y := cursorLocal(tc.pane, agentHeight, vt.Cursor{X: tc.cx, Y: tc.cy})
			require.Equal(t, tc.wantX, x)
			require.Equal(t, tc.wantY, y)
		})
	}
}

// TestCursorLocal_RoundTripsHitTest: mapping a cursor cell to split-local
// coordinates and hit-testing those coordinates must land on the same pane
// and cell — the two geometries may never drift apart.
func TestCursorLocal_RoundTripsHitTest(t *testing.T) {
	agent := &PreviewPane{}
	terminal := NewTerminalPane()
	s := NewSplitPane(agent, &DiffPane{}, terminal)
	s.SetSize(100, 40)

	for _, pane := range []int{FocusAgent, FocusTerminal} {
		x, y := cursorLocal(pane, s.agent.height, vt.Cursor{X: 3, Y: 2})
		gotPane, row, col, ok := s.HitTest(x, y)
		require.True(t, ok, "cursor cell must be hit-testable (pane %d)", pane)
		require.Equal(t, pane, gotPane)
		require.Equal(t, 2, row)
		require.Equal(t, 3, col)
	}
}
```

Note: `PreviewPane`/`DiffPane` zero-value construction — check how `split_pane_scroll_indicator_test.go` or `pane_border_test.go` build a `SplitPane` and reuse their fixture if zero values panic in `SetSize`.

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./ui/ -run TestCursorLocal -v`
Expected: FAIL — `undefined: cursorLocal`.

- [ ] **Step 3: Implement `ui/cursor.go`**

```go
package ui

import (
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/vt"
)

// cursorLocal maps a pane-content cell to split-local coordinates — the
// exact inverse of HitTest's geometry: each pane box is a title row (y=0 /
// y=agentHeight+2) plus a body bordered left/right/bottom, agent stacked
// over terminal.
func cursorLocal(pane int, agentHeight int, c vt.Cursor) (x, y int) {
	x = 1 + c.X // left body border occupies column 0
	if pane == FocusTerminal {
		return x, agentHeight + 3 + c.Y
	}
	return x, 1 + c.Y // title border row occupies row 0
}

// CursorScreenPosition returns the focused pane's live cursor as split-local
// coordinates plus its full state, or ok=false whenever no hardware cursor
// should be shown: diff overlay up, focused pane scrolled off the live tail
// or showing fallback text, no emulator-backed session, cursor hidden
// (DECTCEM), or the cell out of the pane's bounds.
func (s *SplitPane) CursorScreenPosition(instance *session.Instance) (x, y int, cur vt.Cursor, ok bool) {
	if s.diffVisible {
		return 0, 0, vt.Cursor{}, false
	}
	var c vt.Cursor
	var have bool
	var w, h int
	switch s.focusedPane {
	case FocusAgent:
		if instance == nil || s.agent.IsScrolling() || s.agent.ShowingFallback() {
			return 0, 0, vt.Cursor{}, false
		}
		c, have = instance.CursorState()
		w, h = s.agent.width, s.agent.height
	case FocusTerminal:
		if s.terminal.IsScrolling() || s.terminal.ShowingFallback() {
			return 0, 0, vt.Cursor{}, false
		}
		c, have = s.terminal.CursorState()
		w, h = s.terminal.width, s.terminal.height
	default:
		return 0, 0, vt.Cursor{}, false
	}
	if !have || !c.Visible || c.X < 0 || c.X >= w || c.Y < 0 || c.Y >= h {
		return 0, 0, vt.Cursor{}, false
	}
	x, y = cursorLocal(s.focusedPane, s.agent.height, c)
	return x, y, c, true
}
```

Add the fallback accessors. `ui/preview.go`:

```go
// ShowingFallback reports whether the pane is displaying splash/fallback
// text instead of live terminal content (no cursor applies there).
func (p *PreviewPane) ShowingFallback() bool {
	return p.previewState.fallback
}
```

`ui/terminal.go`:

```go
// ShowingFallback reports whether the pane is displaying fallback text
// instead of live terminal content (no cursor applies there).
func (t *TerminalPane) ShowingFallback() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fallback
}
```

- [ ] **Step 4: Run tests and commit**

```bash
CGO_ENABLED=0 go test ./ui/ -v
gofmt -w ui/
git add ui/
git commit -m "feat(ui): map focused-pane emulator cursor to split-local coordinates"
```

---

### Task 10: Hardware cursor in View()

**Files:**
- Modify: `app/app.go` (`View` at ~line 2197)

- [ ] **Step 1: Wire `tea.View.Cursor` on the main-view path**

In `home.View()`, the final `return asView(mainView)` becomes:

```go
	view := asView(mainView)
	m.attachCursor(&view)
	return view
```

Add the helper (below `View`):

```go
// attachCursor positions the REAL hardware cursor over the focused pane's
// cursor cell — the host terminal then renders its own native cursor
// (user-configured color, blink) there. Only on the plain main-view path:
// overlays, pickers, and non-default states keep the cursor hidden
// (Bubble Tea's default when View.Cursor is nil).
func (m *home) attachCursor(v *tea.View) {
	if m.state != stateDefault && m.state != stateInlineAttach {
		return
	}
	if m.activeOverlay != nil {
		return
	}
	lx, ly, cur, ok := m.splitPane.CursorScreenPosition(m.list.GetSelectedInstance())
	if !ok {
		return
	}
	// Same screen↔split mapping the mouse path uses:
	// HitTest(mouse.X - m.listWidth, mouse.Y - m.tabBar.Height()).
	c := tea.NewCursor(m.listWidth+lx, m.tabBar.Height()+ly)
	c.Blink = cur.Blink
	switch cur.Shape {
	case vt.CursorShapeUnderline:
		c.Shape = tea.CursorUnderline
	case vt.CursorShapeBar:
		c.Shape = tea.CursorBar
	default:
		c.Shape = tea.CursorBlock
	}
	v.Cursor = c
}
```

Add `"github.com/aidan-bailey/loom/session/vt"` to `app/app.go` imports.

- [ ] **Step 2: Build, run the full suite, verify manually**

```bash
CGO_ENABLED=0 go build -o loom && CGO_ENABLED=0 go test ./...
```

Manual verification (per the repo's smoke-test isolation hazard: use a THROWAWAY git repo as the workspace, never a real one; an isolated `LOOM_HOME` under the scratchpad; and a dedicated tmux socket if scripting tmux):
1. `LOOM_HOME=$SCRATCH/loomhome ./loom` in a throwaway repo → create an instance running `bash`.
2. Focused agent pane at live tail → your terminal's own cursor sits at the shell prompt and blinks natively; typing `read x` then text shows it tracking.
3. `ctrl+t` focus terminal pane → cursor jumps to the terminal pane's prompt.
4. Scroll the focused pane up → cursor disappears; jump to bottom → returns.
5. Open any overlay (`?`, `S`) → cursor disappears; close → returns.
6. In the pane run `printf '\x1b[?25l'; sleep 3; printf '\x1b[?25h'` → cursor hides for 3s (DECTCEM).
7. Run `printf '\x1b[5 q'` → bar cursor; `printf '\x1b[2 q'` → steady block (DECSCUSR pass-through; skip if the host terminal ignores shape changes).

- [ ] **Step 3: Commit**

```bash
gofmt -w app/
git add app/
git commit -m "feat(app): render the native hardware cursor over the focused pane"
```

---

## Phase 4 — Title, bell, focus

### Task 11: Window-title pass-through

**Files:**
- Modify: `session/vt/vt.go` (interface), `session/vt/xvt.go`, `session/tmux/tmux.go`, `session/instance.go`, `app/app.go`
- Test: `session/vt/xvt_state_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `session/vt/xvt_state_test.go`:

```go
func TestTitle_OSCPassThrough(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	require.Equal(t, "", e.Title())
	_, _ = e.Write([]byte("\x1b]2;✳ claude — working\x07"))
	require.Equal(t, "✳ claude — working", e.Title())
	_, _ = e.Write([]byte("\x1b]0;both-title\x07")) // OSC 0 sets icon+title
	require.Equal(t, "both-title", e.Title())
}
```

- [ ] **Step 2: Run to verify failure** — `CGO_ENABLED=0 go test ./session/vt/ -run TestTitle -v` → FAIL (`e.Title` undefined).

- [ ] **Step 3: Implement**

`session/vt/vt.go` — add to the `Emulator` interface:

```go
	// Title returns the window title most recently set by the inner app via
	// OSC 0/2, or "" if never set.
	Title() string
```

`session/vt/xvt.go` — add a `title string` field to `xvtEmulator` (same callback-fed block as the cursor fields), register in the same `SetCallbacks` literal:

```go
		Title: func(s string) {
			e.title = s
		},
```

and add the reader:

```go
func (e *xvtEmulator) Title() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.title
}
```

`session/tmux/tmux.go` (near `CursorState`):

```go
// PaneTitle returns the inner app's OSC-set window title, or ok=false when
// no emulator is wired or no title was ever set.
func (t *TmuxSession) PaneTitle() (string, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return "", false
	}
	title := emu.Title()
	return title, title != ""
}
```

`session/instance.go` (near `CursorState`):

```go
// PaneTitle returns the agent's OSC-set window title, or ok=false.
func (i *Instance) PaneTitle() (string, bool) {
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return "", false
	}
	return ts.PaneTitle()
}
```

`app/app.go` — in `attachCursor`'s sibling spot, extend `View`: after `view := asView(mainView)` add:

```go
	view.WindowTitle = m.windowTitle()
```

and the helper:

```go
// windowTitle passes the selected agent's OSC title through to the host
// terminal, suffixed so window lists stay identifiable; falls back to the
// instance title when the inner app never set one.
func (m *home) windowTitle() string {
	sel := m.list.GetSelectedInstance()
	if sel == nil {
		return "loom"
	}
	if t, ok := sel.PaneTitle(); ok {
		return t + " — loom"
	}
	return "loom — " + sel.Title
}
```

- [ ] **Step 4: Run, verify, commit**

```bash
CGO_ENABLED=0 go test ./... && gofmt -w .
git add session/ app/
git commit -m "feat(app): pass inner-app OSC titles through to the host terminal"
```

Manual check: run a Claude session in an isolated Loom — the host terminal tab shows Claude's `✳ …` title live.

---

### Task 12: Bell → attention badge

**Files:**
- Modify: `session/vt/vt.go`, `session/vt/xvt.go`, `session/tmux/tmux.go` (`Restore` ~line 438), `session/instance.go`, `app/app.go`, `ui/list.go`
- Test: `session/vt/xvt_state_test.go`, `app/events_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `session/vt/xvt_state_test.go`:

```go
func TestBell_InvokesBellFunc(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	rang := 0
	e.SetBellFunc(func() { rang++ })
	_, _ = e.Write([]byte("ding\x07dong"))
	require.Equal(t, 1, rang)
	require.NotPanics(t, func() {
		e2 := NewXVT(10, 5)
		defer e2.Close()
		_, _ = e2.Write([]byte("\x07")) // no func set → no-op
	})
}
```

Append to `app/events_test.go`:

```go
// TestBellBadgesUnselectedInstance: BEL from a backgrounded pane badges its
// list row; the selected instance never badges (the user is looking at it),
// and selecting a badged instance clears it.
func TestBellBadgesUnselectedInstance(t *testing.T) {
	var hc1, hc2 int
	inst1 := startedInstanceWithHistory(t, &hc1)
	inst2 := startedInstanceWithHistory2(t, &hc2, "scroll2")
	t.Setenv("LOOM_PANE_RENDERER", "")

	m := homeWithAppState(t)
	_ = m.list.AddInstance(inst1) // auto-selected
	fin := m.list.AddInstance(inst2)
	fin()

	_, _ = m.Update(bellMsg{session: inst2.TmuxSessionName()})
	require.True(t, inst2.BellPending(), "bell on unselected instance must badge it")

	_, _ = m.Update(bellMsg{session: inst1.TmuxSessionName()})
	require.False(t, inst1.BellPending(), "bell on the selected instance is not badged")

	m.list.SetSelectedInstance(1) // select inst2
	_ = m.instanceChanged()
	require.False(t, inst2.BellPending(), "selecting a badged instance clears the badge")
}
```

Note: `startedInstanceWithHistory` hardcodes the title "scroll"; add a thin variant `startedInstanceWithHistory2(t, counter, title)` by parameterizing the title (refactor the existing helper to take the title, keep a wrapper with the old signature so `preview_tick_test.go` is untouched). `AddInstance` returns a finalizer (see `list.go:657`) — call it as shown.

- [ ] **Step 2: Run to verify failure** — both new tests FAIL (`SetBellFunc`, `BellPending` undefined).

- [ ] **Step 3: Implement**

`session/vt/vt.go` — interface addition:

```go
	// SetBellFunc installs a handler invoked when the inner app rings BEL.
	// The handler runs inside Write on the pump goroutine — it must be
	// cheap, must not call back into the Emulator, and must be safe to
	// invoke concurrently with readers (tea.Program.Send qualifies).
	SetBellFunc(f func())
```

`session/vt/xvt.go` — add `bellFunc func()` to the callback-fed field block, register in `SetCallbacks`:

```go
		Bell: func() {
			if e.bellFunc != nil {
				e.bellFunc()
			}
		},
```

and:

```go
func (e *xvtEmulator) SetBellFunc(f func()) {
	e.mu.Lock()
	e.bellFunc = f
	e.mu.Unlock()
}
```

`session/tmux/tmux.go` — in `Restore` (~line 438), right after `emu := newEmulator(cols, rows)`:

```go
	emu := newEmulator(cols, rows)
	if emu != nil {
		// Bell rides the notifier, NOT the coalescer: bells are rare,
		// discrete signals that must never be swallowed by rate limiting.
		emu.SetBellFunc(func() {
			if f := currentNotifier().Bell; f != nil {
				f(t.sanitizedName)
			}
		})
	}
```

`session/instance.go` — add to the `Instance` struct (near other unserialized runtime fields; it must NOT be persisted — confirm it's absent from `InstanceData`/`ToInstanceData`):

```go
	// bellPending marks that the pane rang BEL while this instance was not
	// selected — surfaced as an attention badge in the session list and
	// cleared on selection. Ephemeral: never serialized. atomic because the
	// list renderer and Update goroutine both touch it.
	bellPending atomic.Bool
```

(add `"sync/atomic"` to imports) and the accessors:

```go
// BellPending reports whether an unseen bell is pending for this instance.
func (i *Instance) BellPending() bool { return i.bellPending.Load() }

// SetBellPending sets or clears the pending-bell attention flag.
func (i *Instance) SetBellPending(v bool) { i.bellPending.Store(v) }
```

`app/app.go` — replace the `bellMsg` stub:

```go
	case bellMsg:
		if inst := m.instanceForSession(msg.session); inst != nil && inst != m.list.GetSelectedInstance() {
			inst.SetBellPending(true)
		}
		return m, nil
```

and clear on selection — in `instanceChanged` (~line 1402), after `selected := m.list.GetSelectedInstance()`:

```go
	if selected != nil {
		selected.SetBellPending(false)
	}
```

`ui/list.go` — in `InstanceRenderer.Render` (~line 240), right before the `joinWidth` computation, prefix the badge onto `join`:

```go
	if i.BellPending() {
		join = promptingStyle.Render("● ") + join
	}
```

(`promptingStyle` already exists for the Prompting icon; the badge reuses its attention color. `joinWidth` is computed after this line, so width math stays correct.)

- [ ] **Step 4: Run, race, commit**

```bash
CGO_ENABLED=0 go test ./... 
CC=clang CGO_ENABLED=1 go test -race ./session/vt/ ./app/
gofmt -w .
git add session/ app/ ui/
git commit -m "feat(app): surface pane bells as attention badges in the session list"
```

---

### Task 13: Focus reporting + forwarding

**Files:**
- Modify: `session/vt/vt.go`, `session/vt/xvt.go`, `session/tmux/tmux.go`, `session/instance.go`, `ui/terminal.go`, `ui/split_pane.go`, `app/app.go`
- Test: `session/vt/xvt_state_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `session/vt/xvt_state_test.go`:

```go
func TestFocusReporting_Mode1004Tracking(t *testing.T) {
	e := NewXVT(80, 24)
	defer e.Close()
	require.False(t, e.FocusReportingEnabled(), "off until the app enables mode 1004")
	_, _ = e.Write([]byte("\x1b[?1004h"))
	require.True(t, e.FocusReportingEnabled())
	_, _ = e.Write([]byte("\x1b[?1004l"))
	require.False(t, e.FocusReportingEnabled())
}
```

- [ ] **Step 2: Run to verify failure** — FAIL (`FocusReportingEnabled` undefined).

- [ ] **Step 3: Implement emulator-side tracking**

`session/vt/vt.go` — interface addition:

```go
	// FocusReportingEnabled reports whether the inner app enabled focus
	// reporting (DEC private mode 1004). Focus in/out sequences must only
	// be forwarded while true — unsolicited CSI I/O is garbage input to
	// apps that never asked for it.
	FocusReportingEnabled() bool
```

`session/vt/xvt.go` — add `focusReporting bool` to the callback-fed block; imports gain `"github.com/charmbracelet/x/ansi"`; register in `SetCallbacks`:

```go
		EnableMode: func(mode ansi.Mode) {
			if mode == ansi.ModeFocusEvent {
				e.focusReporting = true
			}
		},
		DisableMode: func(mode ansi.Mode) {
			if mode == ansi.ModeFocusEvent {
				e.focusReporting = false
			}
		},
```

and the reader:

```go
func (e *xvtEmulator) FocusReportingEnabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.focusReporting
}
```

- [ ] **Step 4: Forwarding plumbing**

`session/tmux/tmux.go`:

```go
// ForwardFocus writes a focus-in (CSI I) or focus-out (CSI O) event into the
// pane's PTY — but only when the inner app enabled focus reporting (mode
// 1004). No-op (nil) otherwise: apps that never asked must not receive it.
func (t *TmuxSession) ForwardFocus(in bool) error {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil || !emu.FocusReportingEnabled() {
		return nil
	}
	seq := []byte("\x1b[O")
	if in {
		seq = []byte("\x1b[I")
	}
	return t.SendKeysRaw(seq)
}
```

`session/instance.go`:

```go
// ForwardFocus forwards a host focus in/out event to the agent pane, gated
// on the app having enabled focus reporting. Errors are logged, not
// returned — focus is best-effort.
func (i *Instance) ForwardFocus(in bool) {
	if !i.isStarted() || i.GetStatus() == Paused {
		return
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return
	}
	if err := ts.ForwardFocus(in); err != nil {
		log.For("session").Warn("forward_focus_failed", "instance", i.Title, "err", err)
	}
}
```

`ui/terminal.go`:

```go
// ForwardFocus forwards a host focus in/out event to the current terminal
// session, gated on mode 1004. Best-effort.
func (t *TerminalPane) ForwardFocus(in bool) {
	t.mu.Lock()
	s, ok := t.sessions[t.currentTitle]
	t.mu.Unlock()
	if !ok || s.tmuxSession == nil {
		return
	}
	if err := s.tmuxSession.ForwardFocus(in); err != nil {
		log.For("ui").Info("terminal.forward_focus_failed", "err", err)
	}
}
```

`ui/split_pane.go`:

```go
// ForwardTerminalFocus forwards a focus event to the terminal pane's session.
func (s *SplitPane) ForwardTerminalFocus(in bool) {
	s.terminal.ForwardFocus(in)
}
```

- [ ] **Step 5: App wiring — ReportFocus, host events, switch synthesis**

`app/app.go`:

1. In `View`'s `asView` closure, add `v.ReportFocus = true` next to `v.AltScreen = true`.

2. Add a `hostFocused bool` field to `home` (near `dirtySessions`), and initialize it to `true` in `newHome` (find where other `home` fields are initialized — search `func newHome`):

```go
	// hostFocused mirrors the host terminal's focus state (via tea.FocusMsg/
	// BlurMsg with ReportFocus on). Assumed focused at startup; used to
	// synthesize correct focus events when panes/sessions switch.
	hostFocused bool
```

3. Add `Update` cases:

```go
	case tea.FocusMsg:
		m.hostFocused = true
		m.forwardFocus(true)
		return m, nil
	case tea.BlurMsg:
		m.hostFocused = false
		m.forwardFocus(false)
		return m, nil
```

4. Add the helpers:

```go
// forwardFocus sends the host's focus state to whichever pane currently has
// focus. PTY writes from the Update goroutine are established practice here
// (inline attach does the same via SendKeysRaw).
func (m *home) forwardFocus(in bool) {
	selected := m.list.GetSelectedInstance()
	if selected == nil {
		return
	}
	switch m.splitPane.GetFocusedPane() {
	case ui.FocusAgent:
		selected.ForwardFocus(in)
	case ui.FocusTerminal:
		m.splitPane.ForwardTerminalFocus(in)
	}
}

// setPaneFocus switches the focused pane, synthesizing focus-out to the old
// pane's app and focus-in to the new one (only while the host itself is
// focused) — so an agent that watches focus (e.g. Claude Code idle
// notifications) sees Loom's pane focus like a real terminal's.
func (m *home) setPaneFocus(pane int) {
	if pane == m.splitPane.GetFocusedPane() {
		return
	}
	if m.hostFocused {
		m.forwardFocus(false)
	}
	m.splitPane.SetFocusedPane(pane)
	if m.hostFocused {
		m.forwardFocus(true)
	}
}
```

5. Route existing pane-focus switches through the helper. Find every call site:

```bash
grep -rn "SetFocusedPane" app/
```

Replace each `m.splitPane.SetFocusedPane(x)` in `app/` with `m.setPaneFocus(x)` (leave `ui/`-internal uses and test files alone). Typical sites: the `ctrl+a`/`ctrl+t` interact handlers (`state_default.go` / `intents.go` / `app_scripts.go` — wherever the grep hits) and the mouse click-to-focus path in `app.go`.

6. Session-switch synthesis — in `instanceChanged` (~line 1402), track the previous selection and synthesize on change. Add a `lastFocusTitle string` field to `home`, and at the top of `instanceChanged` (after `selected := ...` and the bell-clear from Task 12):

```go
	newTitle := ""
	if selected != nil {
		newTitle = selected.Title
	}
	if newTitle != m.lastFocusTitle {
		// The user's attention moved to a different instance: the old pane
		// loses focus, the new one gains it (host focus permitting).
		if m.hostFocused && m.splitPane.GetFocusedPane() == ui.FocusAgent {
			if prev := m.list.GetInstanceByTitle(m.lastFocusTitle); prev != nil {
				prev.ForwardFocus(false)
			}
		}
		m.lastFocusTitle = newTitle
		if m.hostFocused && selected != nil && m.splitPane.GetFocusedPane() == ui.FocusAgent {
			selected.ForwardFocus(true)
		}
	}
```

Note: if `ui.List` has no `GetInstanceByTitle`, add it next to `findByTitle` (`ui/list.go:533`):

```go
// GetInstanceByTitle returns the instance with the given title, or nil.
func (l *List) GetInstanceByTitle(title string) *session.Instance {
	if idx := l.findByTitle(title); idx >= 0 {
		return l.instances[idx]
	}
	return nil
}
```

(Match the actual backing-slice field name from `findByTitle`'s body.)

- [ ] **Step 6: Run everything, race, commit**

```bash
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
gofmt -w .
git add session/ ui/ app/
git commit -m "feat(app): forward host focus events to panes, gated on mode 1004"
```

Manual check (isolated env): run Claude Code in an instance, focus another window → after going idle Claude's unfocused-notification behavior fires; switch back → no spurious notifications.

---

### Task 14: Docs + final verification sweep

**Files:**
- Modify: `CLAUDE.md`, `USAGE.md`

- [ ] **Step 1: Update `CLAUDE.md`**

In the **`session/tmux/`** package bullet, after the sentence about the output pump/emulator, add:

```
The output pump also drives the event-based UI: a per-session coalescer (`notify.go`) emits dirty (≤ ~60/s), quiet (500ms settled), bell, and dead notifications through the package-level `tmux.SetNotifier` hook, which `app.Run` wires to `tea.Program.Send`. Status detection (`statusContent`) reads the emulator in-process; capture-pane remains only as the snapshot-path fallback.
```

In the **Gotchas** section, add:

```
- **Pane updates are event-driven, not polled.** With the emulator enabled there is NO preview tick: panes re-render on `paneDirtyMsg` from the output pump, status transitions ride `paneQuietMsg`, and the 3s health tick only does liveness/ptmx-repair/diff stats. The legacy 100ms preview tick and 500ms full metadata scan only survive under `LOOM_PANE_RENDERER=snapshot` (and Windows). When adding per-instance periodic work, put it on the health tick; when reacting to output, handle the event messages in `app/events.go` — and never `Send` from the Update goroutine's own handlers (the pump/timer goroutines own that).
- **x/vt callbacks run under the xvt wrapper's write lock.** `session/vt/xvt.go` registers `Callbacks` that fire inside `emu.Write`; they may only assign wrapper fields — re-locking `e.mu` deadlocks (RWMutex is not reentrant) and calling app code from them violates the no-model-mutation rule. New emulator-sourced state follows the same pattern: callback writes the field, a read-locked accessor exposes it.
```

In the **Environment Variables** section, extend the `LOOM_PANE_RENDERER` entry's last sentence:

```
Unset (default) renders panes from the emulator, enabling live-scroll, mouse forwarding, event-driven updates (no render/status polling), the native hardware cursor, and title/bell/focus pass-through.
```

- [ ] **Step 2: Update `USAGE.md`**

Find the section describing the panes (search for "agent pane" or "Terminal pane") and add a short subsection:

```markdown
### Native terminal behavior

The focused pane shows your terminal's **real cursor** — native blink, color,
and shape (bar/underline/block follow the app's DECSCUSR setting, and apps
that hide their cursor hide yours). The cursor only appears at the live tail;
scrolling back or opening an overlay hides it.

The host window title mirrors the selected agent's own title (e.g. Claude's
status line). A backgrounded session that rings the terminal bell gets a ●
attention badge in the session list until you select it. Apps that enable
focus reporting (like Claude Code) receive real focus in/out events when you
focus/unfocus Loom's window, switch panes, or switch sessions — so idle
notifications fire correctly.
```

- [ ] **Step 3: Full verification sweep**

```bash
CGO_ENABLED=0 go build -o loom
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
golangci-lint run --timeout=3m --fast
gofmt -l .   # must print nothing
```

Expected: build clean, all tests PASS, race clean, lint clean.

Manual idle-CPU spot-check (isolated env): run Loom with 2–3 idle instances; `pidstat -p $(pgrep -f './loom') 5 3` (or `top`) should show near-zero CPU between keystrokes, vs. constant wakeups before this change.

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md USAGE.md
git commit -m "docs: document event-driven pane updates and native terminal behavior"
```

---

## Plan Self-Review Notes

- **Spec coverage:** backbone (Tasks 1–3), in-process status + health tick + PTY-death (Tasks 4–6), cursor (Tasks 7–10), title/bell/focus (Tasks 11–13), docs/testing (14). Spec §6 edge cases land in: notifier-unset (Task 1 test), burst storm (coalescer), unknown session dropped (Task 3 handler), dead-vs-repair (Task 6), cursor bounds/scroll/fallback (Task 9), selected-instance-only title (Task 11).
- **Type consistency:** event messages carry `session string`; `TmuxSession.SessionName()`/`Instance.TmuxSessionName()` are the routing keys everywhere; `vt.Cursor{X,Y,Visible,Shape,Blink}` flows tmux→instance→ui→app unchanged.
- **Known verify-at-implementation points (flagged inline, not placeholders):** tmux name-prefix expectations in Task 2's tests; claude adapter's prompt marker string in Task 4; `slot.list` field name in Task 3; `List` backing-slice field in Task 13; zero-value pane construction in Task 9's round-trip test. Each has a stated fallback.

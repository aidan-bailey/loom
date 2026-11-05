# TUI frame gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** All pane output shares one 16 ms frame, and output from a pane the screen doesn't draw costs no frame. Everything else renders as it does today.

**Architecture:** A zero-value-ready `frameGate` in `app/` lets `home.View` return its last frame, but only for the one `View` after an Update in which pane output asked for that. Drawn output renders at once when a frame has passed, and otherwise arms one trailing `frameTickMsg`. `home.drawsPane` decides from the layout `View` draws. The spec is `docs/superpowers/specs/2026-11-05-tui-frame-gate-design.md`.

**Tech Stack:** Go, Bubble Tea v2 (`charm.land/bubbletea/v2`, vendored), testify.

**Execution:** inline, by the owner's choice (2026-11-05). Three packages, one commit each. Run tests from the worktree root with `CGO_ENABLED=0`. Run the full and race suites through `loom job run`.

---

## Refinements to the spec, made while planning

Package 1 writes these into the spec, so the spec and the code agree.

1. **Reuse is opt-in for each Update, not opt-out.** The spec's §4 had `Update` invalidate for every message but the pane ones. The plan instead lets only `paneDirtyMsg`'s handler ask to reuse the frame (`frameGate.output`), for the one `View` after that Update; any other path renders. Same behaviour, but a `View()` with no Update before it renders too. Tests that change `home` directly and call `View()` again, as `app/work_test.go` does, then keep working, and a message the gate doesn't know about can never reuse a frame.
2. **`frameTickMsg` is handled in the `Update` wrapper,** before the message switch. A state whose routing swallows the tick can then never leave a tick pending forever, which would freeze drawn panes inside a burst.
3. **The link condition is "offline after the Update" (`m.offline()`).** Every change of link state that can happen in a pane message's Update leaves it offline: `checkLink` and `lose` move connected to stopping, waiting or reconnecting. A rejoin arrives as a message of its own.
4. **The workbench draws the current terminal too:** its terminal tab (`ui.WbTabTerminal`) shows the split's terminal pane. §5's table gains it.
5. **No change to `app/app_init.go`:** the zero-value gate needs no construction.

## Files

| File | Responsibility |
|---|---|
| `app/frame_gate.go` (new) | `frameGate`, `frameInterval`, `frameTickMsg` |
| `app/frame_gate_test.go` (new) | the gate's unit tests |
| `app/app.go` | `Update` (wrapper), `View` → `render`, `layout`, the `paneDirtyMsg` case |
| `app/frame_draws.go` (new) | `drawsPane`, `listShows` |
| `app/core_glue.go` | `drainCoreApplied`, with `drainCore` built on it |
| `app/frame_draws_test.go` (new) | the filter's and the wiring's tests |
| `app/CLAUDE.md`, `app/README.md`, `docs/claude/debugging-freezes.md` | the rule, the structure, the finding |

---

### Package 1: the frame gate

**Files:**
- Create: `app/frame_gate.go`, `app/frame_gate_test.go`
- Modify: `docs/superpowers/specs/2026-11-05-tui-frame-gate-design.md` (refinements 1–5)

- [ ] **Step 1: Write the failing tests** in `app/frame_gate_test.go`:

```go
package app

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateAt is a frameGate whose clock reads *now.
func gateAt(now *time.Time) *frameGate {
	return &frameGate{now: func() time.Time { return *now }}
}

// counting is a render func that counts its calls in *n and names the
// count in the frame it returns.
func counting(n *int) func() tea.View {
	return func() tea.View {
		*n++
		return tea.NewView(fmt.Sprintf("frame %d", *n))
	}
}

func TestFrameGate_RendersUnlessOutputAsksToReuse(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	r := counting(&n)

	assert.Equal(t, "frame 1", g.view(r).Content, "the first View renders")
	g.fresh()
	assert.Equal(t, "frame 2", g.view(r).Content, "an Update that asked for nothing renders")
	assert.Equal(t, "frame 3", g.view(r).Content, "a View with no Update before it renders")

	g.fresh()
	assert.Nil(t, g.output(false))
	assert.Equal(t, "frame 3", g.view(r).Content, "undrawn output reuses the frame")
	assert.Equal(t, "frame 4", g.view(r).Content, "a reuse covers only the View after its Update")

	g.fresh()
	assert.Nil(t, g.output(false))
	g.fresh()
	assert.Equal(t, "frame 5", g.view(r).Content, "fresh after output renders")
}

func TestFrameGate_NoFrameYetRenders(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	assert.Nil(t, g.output(false))
	assert.Equal(t, "frame 1", g.view(counting(&n)).Content, "there is no frame to reuse")
	g.fresh()
	assert.Nil(t, g.output(true), "drawn output with no frame yet renders at once")
}

func TestFrameGate_DrawnOutputRendersAtOnceAfterAFrame(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	r := counting(&n)
	g.view(r)

	now = now.Add(frameInterval)
	g.fresh()
	assert.Nil(t, g.output(true), "a frame has passed: no tick")
	assert.Equal(t, "frame 2", g.view(r).Content, "it renders at once (a keystroke's echo)")
	assert.False(t, g.pending)
}

func TestFrameGate_ABurstGetsOneTrailingFrame(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	r := counting(&n)
	g.view(r)

	now = now.Add(5 * time.Millisecond)
	g.fresh()
	tick := g.output(true)
	require.NotNil(t, tick, "within a frame of the last render: a trailing tick")
	assert.Equal(t, "frame 1", g.view(r).Content, "the frame is reused until the tick")

	now = now.Add(5 * time.Millisecond)
	g.fresh()
	assert.Nil(t, g.output(true), "one tick pending at most")
	assert.Equal(t, "frame 1", g.view(r).Content)

	g.ticked()
	g.fresh()
	assert.Equal(t, "frame 2", g.view(r).Content, "the tick's Update renders")
	assert.False(t, g.pending)

	g.fresh()
	assert.NotNil(t, g.output(true), "after the tick a new burst arms again")
}

func TestFrameGate_TheTickIsAFrameTickMsgForTheRestOfTheFrame(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	g.view(counting(&n))
	now = now.Add(frameInterval - time.Millisecond)
	g.fresh()
	tick := g.output(true)
	require.NotNil(t, tick)
	start := time.Now()
	assert.Equal(t, frameTickMsg{}, tick())
	assert.Less(t, time.Since(start), frameInterval, "it waits the rest of the frame, not a whole one")
}

func TestFrameGate_ZeroValueUsesTheWallClock(t *testing.T) {
	var g frameGate
	n := 0
	g.view(counting(&n))
	g.fresh()
	assert.NotNil(t, g.output(true), "rendered just now: within a frame")
}
```

- [ ] **Step 2: Run them to confirm they fail**

Run: `CGO_ENABLED=0 go test ./app/ -run TestFrameGate -count=1`
Expected: build failure, `undefined: frameGate` (also `frameInterval`, `frameTickMsg`).

- [ ] **Step 3: Write `app/frame_gate.go`**

```go
package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// frameInterval is one frame at Bubble Tea's default 60 fps: the rate its
// renderer flushes at, and the output pump's coalescer interval
// (session/tmux/notify.go).
const frameInterval = 16 * time.Millisecond

// frameTickMsg ends a burst: the trailing frame frameGate.output armed
// for pane output that came within a frame of the last render.
type frameTickMsg struct{}

// frameGate lets View reuse its last frame for pane output, so all panes
// share one frame instead of each event rendering one
// (docs/superpowers/specs/2026-11-05-tui-frame-gate-design.md). Reuse is
// opt-in: only output asks for it, and only for the View that follows its
// Update; every other View renders. The zero value is ready. It is
// touched only on the Update goroutine, where Bubble Tea also calls View.
type frameGate struct {
	now      func() time.Time // nil: time.Now
	last     tea.View
	has      bool
	rendered time.Time
	reuse    bool // the next View may return last
	pending  bool // a frameTickMsg is armed
}

func (g *frameGate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// fresh makes the next View render, whatever output asked: Update calls
// it before the message switch, and again when the Update changed more
// than a pane (a drain that applied events, the link offline).
func (g *frameGate) fresh() { g.reuse = false }

// output handles a pane's output; drawn says the screen can show it
// (home.drawsPane). Undrawn output reuses the frame. Drawn output renders
// at once when a frame has passed since the last render, as a keystroke's
// echo does, and otherwise reuses the frame and arms one trailing
// frameTickMsg for the rest of it.
func (g *frameGate) output(drawn bool) tea.Cmd {
	if !drawn {
		g.reuse = true
		return nil
	}
	wait := frameInterval - g.clock().Sub(g.rendered)
	if !g.has || wait <= 0 {
		return nil
	}
	g.reuse = true
	if g.pending {
		return nil
	}
	g.pending = true
	return tea.Tick(wait, func(time.Time) tea.Msg { return frameTickMsg{} })
}

// ticked records that the armed frameTickMsg arrived; its Update renders.
func (g *frameGate) ticked() { g.pending = false }

// view returns the last frame when the Update before asked to reuse it,
// and otherwise renders and keeps the result.
func (g *frameGate) view(render func() tea.View) tea.View {
	if g.reuse && g.has {
		g.reuse = false
		return g.last
	}
	g.reuse = false
	g.last = render()
	g.has = true
	g.rendered = g.clock()
	return g.last
}
```

- [ ] **Step 4: Run the tests to confirm they pass**

Run: `CGO_ENABLED=0 go test ./app/ -run TestFrameGate -count=1 -v 2>&1 | tail -15`
Expected: all six `TestFrameGate_…` PASS.

- [ ] **Step 5: Amend the spec with refinements 1–5.**
  - §4: reuse is opt-in for each Update, `fresh()` and `output(drawn)`, and the tick is handled in the wrapper.
  - §4: the link condition is `m.offline()`.
  - §5: the workbench row gains the current terminal (the workbench's terminal tab).
  - §10: drop `app/app_init.go` and `app/link.go`; add `app/frame_draws.go` and `app/frame_draws_test.go`.

- [ ] **Step 6: Commit**

```bash
gofmt -l app/frame_gate.go app/frame_gate_test.go   # expect no output
git add -- app/frame_gate.go app/frame_gate_test.go docs/superpowers/specs/2026-11-05-tui-frame-gate-design.md
git commit -q -m "feat(app): a frame gate that lets pane output reuse the last frame" -- app/frame_gate.go app/frame_gate_test.go docs/superpowers/specs/2026-11-05-tui-frame-gate-design.md
```

(End the message with the attribution line.)

---

### Package 2: wiring and the on-screen filter

**Files:**
- Create: `app/frame_draws.go`, `app/frame_draws_test.go`
- Modify: `app/app.go` (`Update` around line 715, the `paneDirtyMsg` case around line 825, `View` around line 1984), `app/core_glue.go` (`drainCore`)

- [ ] **Step 1: Build the baseline binary for Package 3's measurement** (behaviour is still unchanged at this commit):

Run: `CGO_ENABLED=0 go build -o "$S/loom-before" .`, where `$S` is the session scratchpad.
Expected: the binary exists. Never run it in a pane; Package 3 runs it through loomdev.

- [ ] **Step 2: Write the failing tests** in `app/frame_draws_test.go`:

```go
package app

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	"charm.land/bubbles/v2/spinner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drawsHome is fleetHome in focus mode with two live rows: "f-live" in the
// focused slot (not selected) and "b-live" in the peer slot, both Running
// so output promotes nothing, and a current terminal session, injected.
func drawsHome(t *testing.T) (m *home, focusRow, peerRow, terminal string) {
	t.Helper()
	isolateTmux(t)
	m = fleetHome(t)
	m.viewMode = viewFocus
	f := liveInstance(t, "f-live")
	b := liveInstance(t, "b-live")
	m.slots[0].ws().AddForTest(f)
	m.slots[1].ws().AddForTest(b)
	m.syncViews()
	m.drainCore()
	ts := tmux.NewSessionWithDeps("t-cur", "bash", fakePtyFactory{t: t}, aliveCmdExecForTest())
	m.splitPane.InjectTerminalSessionForTest("t-cur", ts, t.TempDir())
	require.NotEqual(t, f.Title, m.list.GetSelectedInstance().Title, "fixture: f-live is not the selection")
	return m, f.Pane().TmuxSessionName(), b.Pane().TmuxSessionName(), ts.SessionName()
}

func (m *home) drawsSession(name string) bool {
	v, _ := m.viewBySession(name)
	return m.drawsPane(name, v)
}

func TestDrawsPane_ByLayout(t *testing.T) {
	m, focusRow, peerRow, term := drawsHome(t)
	// The selection is a fleet row with no tmux session of its own, so it
	// is asked by its row, as the case asks it.
	selectedDrawn := func() bool {
		sel := m.list.GetSelectedInstance()
		return m.drawsPane(sel.TmuxSession, sel)
	}

	// Focus: the selection, the current terminal, the focused slot's rows
	// while the rail shows; never a peer slot's row or another terminal.
	assert.True(t, selectedDrawn(), "focus: the selected agent")
	assert.True(t, m.drawsSession(term), "focus: the current terminal")
	assert.True(t, m.drawsSession(focusRow), "focus: a rail row")
	assert.False(t, m.drawsSession(peerRow), "focus: another tab's row")
	assert.False(t, m.drawsSession("loom_t-cached"), "focus: a terminal that is not current")
	m.railHidden = true
	assert.False(t, m.drawsSession(focusRow), "focus, rail hidden: a rail row")
	assert.True(t, selectedDrawn(), "focus, rail hidden: the selected agent")
	m.railHidden = false

	m.viewMode = viewOverview
	assert.True(t, m.drawsSession(peerRow), "overview: every open workspace's row")
	assert.False(t, m.drawsSession(term), "overview: no terminal pane")

	m.viewMode = viewWorkbench
	assert.True(t, selectedDrawn(), "workbench: the selected agent")
	assert.True(t, m.drawsSession(term), "workbench: its terminal tab")
	assert.False(t, m.drawsSession(focusRow), "workbench: no rail")

	m.viewMode = viewWork
	assert.False(t, selectedDrawn(), "work: no pane")
	assert.False(t, m.drawsSession(term), "work: no pane")

	// The file explorer keeps the focus layout over overview and workbench.
	m.viewMode = viewOverview
	m.state = stateFileExplorer
	assert.True(t, m.drawsSession(focusRow), "file explorer over overview: the rail shows")
	assert.False(t, m.drawsSession(peerRow), "file explorer over overview: focus rules")
}

// rendered leaves m just past a render made a frame ago, so drawn output
// renders at once rather than arming a tick.
func rendered(t *testing.T, m *home) *time.Time {
	t.Helper()
	now := time.Unix(1000, 0)
	m.gate.now = func() time.Time { return now }
	m.View()
	now = now.Add(frameInterval)
	return &now
}

func TestPaneOutput_UndrawnOutputReusesTheFrame(t *testing.T) {
	m, _, peerRow, _ := drawsHome(t)
	rendered(t, m)
	m.Update(paneDirtyMsg{session: peerRow})
	assert.True(t, m.gate.reuse, "another tab's output asks for no frame")
	m.Update(paneDirtyMsg{session: "loom_t-cached"})
	assert.True(t, m.gate.reuse, "a cached terminal's output asks for no frame")
}

func TestPaneOutput_DrawnOutputRenders(t *testing.T) {
	m, focusRow, _, term := drawsHome(t)
	rendered(t, m)
	m.Update(paneDirtyMsg{session: focusRow})
	assert.False(t, m.gate.reuse, "a rail row's output renders")
	m.Update(paneDirtyMsg{session: term})
	assert.False(t, m.gate.reuse, "the current terminal's output renders")
}

func TestPaneOutput_PromotionRenders(t *testing.T) {
	m, inst := peerLiveHome(t, session.Ready) // a peer slot's row, not drawn in focus mode
	m.drainCore()
	rendered(t, m)
	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	require.Equal(t, session.Running, m.ladder[idOf(m, inst)].status, "fixture: the output promoted the row")
	assert.False(t, m.gate.reuse, "a promotion changes the tab bar and the peer footer")
}

func TestUpdate_OtherMessagesRender(t *testing.T) {
	m, _, peerRow, _ := drawsHome(t)
	rendered(t, m)
	m.Update(paneDirtyMsg{session: peerRow})
	require.True(t, m.gate.reuse, "fixture")
	m.Update(spinner.TickMsg{})
	assert.False(t, m.gate.reuse, "a message that is not pane output renders")
}

func TestUpdate_ADrainThatAppliedEventsRenders(t *testing.T) {
	m, _, peerRow, _ := drawsHome(t)
	rendered(t, m)
	m.slots[1].ws().AddForTest(liveInstance(t, "b-new")) // the model publishes; the drain applies it
	m.Update(paneDirtyMsg{session: peerRow})
	assert.False(t, m.gate.reuse, "a model change that rode a pane message renders")
}

func TestUpdate_OfflineRenders(t *testing.T) {
	m, _, peerRow, _ := drawsHome(t)
	rendered(t, m)
	m.link.state = linkReconnecting
	m.Update(paneDirtyMsg{session: peerRow})
	assert.False(t, m.gate.reuse, "offline, every Update renders")
}

func TestUpdate_TheFrameTickRendersAndClearsTheTick(t *testing.T) {
	m, focusRow, _, _ := drawsHome(t)
	now := rendered(t, m)
	m.View()
	*now = now.Add(time.Millisecond)
	m.Update(paneDirtyMsg{session: focusRow})
	require.True(t, m.gate.pending, "fixture: within a frame, a tick is armed")
	require.True(t, m.gate.reuse)
	m.Update(frameTickMsg{})
	assert.False(t, m.gate.pending)
	assert.False(t, m.gate.reuse)
}

var _ = ui.FocusAgent // ui is imported for the fixture's types
```

Notes for the implementer:
- Check the fixtures' names against the code before running: `fleetHome` (`app/overview_cursor_test.go`), `liveInstance` (`app/slot_release_test.go`), `peerLiveHome` (`app/views_freshness_test.go`), `isolateTmux`, `fakePtyFactory`, `aliveCmdExecForTest`, `idOf`, `InjectTerminalSessionForTest` (`ui/split_pane.go`).
- Drop the `ui` import and the `var _` line if nothing else needs `ui`.
- If `drawsHome`'s selection turns out to be `f-live`, select `f1` with the list's selection setter rather than changing the asserts.

- [ ] **Step 3: Run them to confirm they fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestDrawsPane|TestPaneOutput|TestUpdate_(OtherMessages|ADrain|Offline|TheFrameTick)' -count=1`
Expected: build failure, `m.drawsPane undefined` and `m.gate undefined`.

- [ ] **Step 4: Add the gate to `home`.** Put it in `app/app.go`'s `home` struct, next to `lastPreviewHash`:

```go
	// gate lets View reuse its last frame for pane output
	// (app/frame_gate.go); its zero value is ready.
	gate frameGate
```

- [ ] **Step 5: Make the drain report whether it applied anything.** In `app/core_glue.go`, replace `drainCore`'s body:

```go
func (m *home) drainCore() tea.Cmd {
	cmd, _ := m.drainCoreApplied()
	return cmd
}

// drainCoreApplied is drainCore, also saying whether it applied anything:
// Update renders a frame after a drain that did (frameGate).
func (m *home) drainCoreApplied() (tea.Cmd, bool) {
	if m.core == nil {
		return nil, false
	}
	var cmds []tea.Cmd
	applied := false
	for events := m.core.Sync(); len(events) > 0; events = m.core.Sync() {
		applied = true
		for _, ev := range events {
			cmds = append(cmds, m.applyCoreEvent(ev))
		}
	}
	return tea.Batch(cmds...), applied
}
```

- [ ] **Step 6: Wire `Update`** in `app/app.go`, replacing the wrapper:

```go
func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.exitErr != nil {
		return m, nil
	}
	// The next View renders unless pane output asks to reuse the frame
	// (paneDirtyMsg's case); a tick ends a burst whatever state routes it.
	m.gate.fresh()
	if _, ok := msg.(frameTickMsg); ok {
		m.gate.ticked()
	}
	model, cmd := m.update(msg)
	drained, applied := m.drainCoreApplied()
	cmd = tea.Batch(cmd, drained, m.checkLink(), m.workDrained())
	if applied || m.offline() {
		m.gate.fresh()
	}
	m.publishSelection()
	return model, cmd
}
```

- [ ] **Step 7: Put `View` behind the gate and share its layout choice.** Rename today's `func (m *home) View() tea.View` to `func (m *home) render() tea.View`, with its doc comment kept, and add:

```go
// View is the frame: render's, or the last one when the Update before
// was pane output that asked to reuse it (frameGate).
func (m *home) View() tea.View { return m.gate.view(m.render) }

// layout is the presentation render draws: the view mode, except that
// the file explorer keeps the focus layout over overview and the
// workbench (its overlay replaces the right pane).
func (m *home) layout() viewMode {
	if m.state == stateFileExplorer && (m.viewMode == viewOverview || m.viewMode == viewWorkbench) {
		return viewFocus
	}
	return m.viewMode
}
```

In `render`, replace the dispatch `if m.viewMode == viewOverview && m.state != stateFileExplorer { … } else if m.viewMode == viewWork { … } else if m.viewMode == viewWorkbench && m.state != stateFileExplorer { … } else { … }` with a `switch m.layout() { case viewOverview: …; case viewWork: …; case viewWorkbench: …; default: … }` that keeps each branch's body and comments. Leave the status-line hints on `m.viewMode`.

- [ ] **Step 8: Write `app/frame_draws.go`**

```go
package app

import "github.com/aidan-bailey/loom/core"

// drawsPane reports whether the layout render draws can show session's
// pane output; v is the session's row, nil for a terminal session
// (docs/superpowers/specs/2026-11-05-tui-frame-gate-design.md §5). It
// leans toward drawing: a wrong true costs a shared frame, a wrong false
// a pane frozen on screen.
func (m *home) drawsPane(session string, v *core.InstanceView) bool {
	layout := m.layout()
	if layout == viewWork {
		return false
	}
	if v == nil {
		// Only the current terminal is drawn: by the split in focus, and
		// by the workbench's terminal tab.
		return layout != viewOverview && session != "" && session == m.splitPane.CurrentTerminalSessionName()
	}
	if layout == viewOverview {
		return true // every open workspace's row has a card
	}
	if selected := m.list.GetSelectedInstance(); selected != nil && v.ID == selected.ID {
		return true
	}
	return layout == viewFocus && !m.railHidden && m.listShows(v.ID)
}

// listShows reports whether the focused slot's list has the row id: the
// rail draws its rows, scrolled out of its window or not.
func (m *home) listShows(id core.InstanceID) bool {
	for _, row := range m.list.GetInstances() {
		if row.ID == id {
			return true
		}
	}
	return false
}
```

- [ ] **Step 9: Make the `paneDirtyMsg` case ask the gate.** In `app/app.go`, the agent branch keeps its comment and becomes:

```go
		if v, _ := m.viewBySession(msg.session); v != nil {
			m.core.PaneOutput(v.ID)
			// (the existing promotion comment, unchanged)
			promoted := v.Status == session.Ready && !v.StatusReported
			if promoted {
				m.setLadder(v.ID, session.Running)
				m.updateTabBarStatuses()
			}
			if selected != nil && v.ID == selected.ID {
				if err := m.splitPane.UpdateAgent(selected); err != nil {
					return m, m.handleError(err)
				}
			}
			return m, m.gate.output(promoted || m.drawsPane(msg.session, v))
		}
```

The terminal branch's final `return m, nil` becomes `return m, m.gate.output(m.drawsPane(msg.session, nil))`. The error returns stay as they are: they return before `output`, so they render.

- [ ] **Step 10: Run the new tests**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestDrawsPane|TestPaneOutput|TestUpdate_(OtherMessages|ADrain|Offline|TheFrameTick)|TestFrameGate' -count=1 -v 2>&1 | grep -E '^(=== RUN|--- |ok|FAIL)' | tail -25`
Expected: all PASS. If `TestPaneOutput_UndrawnOutputReusesTheFrame` fails because the drain applied events, find which event the `PaneOutput`/`MarkOutput` casts produce. A cast that publishes on every pane event would make the gate render each time; report it rather than weaken the test.

- [ ] **Step 11: Mutation-check every term**, each change made alone and reverted, confirming a test fails each time:
  - in `drawsPane`, `layout == viewWork` → `false`;
  - `layout != viewOverview &&` removed;
  - `session == m.splitPane.CurrentTerminalSessionName()` → `true`;
  - the overview `return true` → `return false`;
  - the selected check removed;
  - `!m.railHidden` → `true`;
  - `layout == viewFocus &&` removed;
  - `m.listShows(v.ID)` → `true`;
  - in `Update`, `applied ||` removed, then `|| m.offline()` removed, then the tick `ticked()` removed;
  - in the case, `promoted ||` removed.

  Run the Step 10 command after each.

- [ ] **Step 12: Audit `render`'s side effects.**

Run: `rtk proxy grep -n 'm\.[a-zA-Z]* = \|m\.[a-zA-Z]*\.[a-zA-Z]* = \|normalize' app/app.go app/overview.go app/work*.go | head -40`. Read each assignment reachable from `render` (`overviewData` → `normalizeOverviewCursor`; `workContent`; `attachCursor`; `windowTitle`).

Expected: each changes only after a key, a resize or a model event, never after pane output alone. Record the list in the plan's execution notes. If one changes after output alone, move that change into Update.

- [ ] **Step 13: Run the app package, vet and gofmt**

Run: `CGO_ENABLED=0 go test ./app/... -count=1 2>&1 | tail -3 && CGO_ENABLED=0 go vet ./app/ && gofmt -l app/`
Expected: `ok`, and no vet or gofmt output.

- [ ] **Step 14: Commit**

```bash
git add -- app/app.go app/core_glue.go app/frame_draws.go app/frame_draws_test.go
git commit -q -m "feat(app): pane output shares one frame, and hidden panes ask for none" -- app/app.go app/core_glue.go app/frame_draws.go app/frame_draws_test.go
```

---

### Package 3: measure, document, gate

**Files:**
- Modify: `app/CLAUDE.md`, `app/README.md`, `docs/claude/debugging-freezes.md`, this plan (execution notes)

- [ ] **Step 1: Measure before and after in a sandbox.** Invoke the `loom-dev` skill and follow it; never run loom in a pane.
  - Write `$S/spin.sh`:
    ```sh
    #!/bin/sh
    i=0
    while :; do i=$((i+1)); printf '\r⠋ working %d ' "$i"; sleep 0.05; done
    ```
    and `chmod +x` it.
  - In the sandbox, start 10 sessions whose program is `$S/spin.sh`.
  - For each build (`$S/loom-before`, then the worktree's build), read the TUI process's `/proc/PID/stat` fields 14 and 15 10 s apart, three times. Use the method in `docs/claude/debugging-freezes.md` §6.

  Expected: the after build uses at most a third of the before build's CPU. Record the numbers in the plan's execution notes.

- [ ] **Step 2: Add the rule to `app/CLAUDE.md`.** Extend the "Put per-instance periodic work on the health tick" rule's sentence about pane event messages with:

```
Pane output asks for a frame through `m.gate.output(drawn)` (`app/frame_gate.go`), and only `paneDirtyMsg`'s case may let `View` reuse one, for the View after its Update; a reuse anywhere else shows a frame older than the state. **Enforced** by `TestPaneOutput_UndrawnOutputReusesTheFrame` and `TestUpdate_OtherMessagesRender`.
```

Run `CGO_ENABLED=0 go run ./tools/claudemd -v 2>&1 | grep app/CLAUDE`. It must stay within 20,000 bytes. If it doesn't fit, shorten the rule's own wording, not another rule.

- [ ] **Step 3: Update `app/README.md` line 7** (the `home.Update` wrapper sentence):

```
`home.View` returns the frame gate's frame (`frame_gate.go`): `render` draws a new one unless the Update before was pane output from a pane the layout doesn't draw (`drawsPane`, `frame_draws.go`) or within a frame of the last render, which arms one trailing `frameTickMsg`.
```

Also add `fresh` and the drain's `applied` to the wrapper's description.

- [ ] **Step 4: Update `docs/claude/debugging-freezes.md` §6's finding.** Append the fix's commit and the measured before/after to the 2026-11-05 bullet.

- [ ] **Step 5: Run the gates through the queue**

```
loom job run -d "frame gate: full suite" -- sh -c 'CGO_ENABLED=0 go test ./... 2>&1 | tail -30'
loom job run -d "frame gate: race" -- sh -c 'CC=clang CGO_ENABLED=1 go test -race ./app/... ./ui/... 2>&1 | tail -20'
loom job run -d "frame gate: e2e" -- sh -c 'go test -tags e2e ./e2e/... 2>&1 | tail -20'
```

Run each with Bash's `run_in_background`. Then run `CGO_ENABLED=0 go vet ./...`, the CLAUDE.md lint (`CGO_ENABLED=0 go run ./tools/claudemd`), and `GOOS=windows CGO_ENABLED=0 go build ./...`. golangci-lint is skipped (CI pins v1).

Expected: all green. The core log-counting tests can fail while another session's suite rotates `/tmp/loom.log` (`docs/claude/testing-pitfalls.md`); rerun `core` alone before calling that a failure.

- [ ] **Step 6: Commit the docs and the execution notes**

```bash
git add -- app/CLAUDE.md app/README.md docs/claude/debugging-freezes.md docs/superpowers/plans/2026-11-05-tui-frame-gate.md
git commit -q -m "docs(app): the frame gate's rule, structure and measured effect" -- app/CLAUDE.md app/README.md docs/claude/debugging-freezes.md docs/superpowers/plans/2026-11-05-tui-frame-gate.md
```

## Execution notes

(Filled in while executing: the side-effect audit, the measurements, deviations.)

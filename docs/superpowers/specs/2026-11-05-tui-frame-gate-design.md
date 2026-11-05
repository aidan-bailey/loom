# One frame for all pane output: the TUI's frame gate

Status: design approved 2026-11-05.

## 1. Problem

On fresco the TUI process (`loom`) used 0.25–0.55 of a core while agents worked, on a host whose cores the work queue shares out. A perf profile of the live process (method: [`docs/claude/debugging-freezes.md`](../../claude/debugging-freezes.md) §6) put 79% of its CPU in `home.View`. Most of that went to lipgloss and `x/ansi` measuring string widths: `SplitPane.String` 48%, the rail's `List.String` building a card per row. Parsing the PTYs took about 2%.

The chain:

- Every attached pane client (23 that day, agent and terminal panes) runs an output pump whose coalescer (`session/tmux/notify.go`, `dirtyCoalesceInterval` 16 ms) sends `paneDirtyMsg` up to 60 times a second. It does so whether the pane is on screen or not. The TUI did 311 PTY reads a second.
- Bubble Tea v2 calls `View()` after every message (`vendor/charm.land/bubbletea/v2/tea.go:880`). A dirty message from a hidden session changes nothing on screen (`app/app.go`, the `paneDirtyMsg` case), yet it costs a full frame.
- Each frame rebuilds every rail card from that session's whole emulator screen to show its tail (`ui/list.go`, `BuildCardData` → `Pane.EmulatorScreen`), then measures every line's width again.

So the TUI rendered a few hundred frames a second while the terminal shows 60. The cost grows with the number of busy panes, times the cost of a frame. The [event-driven panes design](2026-07-12-event-driven-panes-native-cursor-design.md) capped each session at 60 events a second, but assumed each event costs "one emulator Render + one status detect", not a full frame.

## 2. Decisions

- **Stay responsive (owner).** Nothing visible may update later than today. Keys, navigation and every on-screen pane keep the terminal's 60 fps.
- **Approach A (owner), chosen over B and C.** Pane output asks for a frame, all panes share one 16 ms frame, and output from a pane the screen doesn't draw asks for none.
  - Rejected: B (cheaper frames through per-card and per-pane caches): many invalidations to get right, a stale card as the failure mode, and still a few hundred frames a second.
  - Rejected: C (per-pane coalescer rates set by the TUI): a new TUI-to-pump signal, and on-screen panes still render many times per frame.
- Measure after A. Add B's card-tail cache only if frames still cost much at 60 a second.

## 3. Scope

- **In:** the frame gate (`app/frame_gate.go`); `home.View` and `home.Update` wired through it; the on-screen filter in the `paneDirtyMsg` handler; tests; the docs listed in §9.
- **Out:** cheaper frames (B), coalescer rates (C), the `MarkOutput`/`PaneOutput` casts each dirty message sends the daemon (cheap: the daemon used 0.02 of a core), and the spinner's 12 frames a second.

## 4. The frame gate

A unit in `app/frame_gate.go`, owned by `home` and touched only on the Update goroutine (Bubble Tea calls `View` on the same goroutine):

- **State:**
  - the last rendered `tea.View`, and whether there is one;
  - `stale`;
  - the time of the last render;
  - whether a `frameTickMsg` is pending.
- **`invalidate()`:** the next `view` call renders.
- **`frame() tea.Cmd`:** pane output asks for a frame.
  - If 16 ms or more have passed since the last render, it invalidates and returns nil. This is the leading edge: a single keystroke's echo renders at once.
  - Otherwise it arms one trailing `frameTickMsg` for the rest of the 16 ms, unless one is pending, and returns that tick's Cmd.
- **`view(render func() tea.View) tea.View`:** returns the cached view unless it is stale or there is none. Otherwise it calls `render`, caches the result, records the time and clears `stale`.
- **The clock** is a field (`now func() time.Time`), so tests drive it. The 16 ms interval is `frameInterval`, matching Bubble Tea's default 60 fps and the coalescer's interval.

**Wiring:**

- `home.View()` becomes `return m.gate.view(m.render)`, where `render` is today's `View` body, unchanged.
- `home.Update()` (the wrapper around `update`) invalidates in each of these cases:
  - every message that is neither `paneDirtyMsg` nor `frameTickMsg`;
  - when `drainCore` applied any event: a model change that lands during a pane message's Update must not wait for its wake message;
  - when `checkLink` changed the link's state.
- `frameTickMsg` clears the pending flag and invalidates.
- `paneDirtyMsg` invalidates nothing itself; its handler's return value decides (§5).

The renderer already skips a flush when the view equals the last one (`viewEquals`, `vendor/charm.land/bubbletea/v2/cursed_renderer.go`), so a cached view also saves most of the flush work.

## 5. The on-screen filter

The `paneDirtyMsg` handler keeps all of today's bookkeeping for every session:

- `MarkOutput` and `PaneOutput`;
- the Ready→Running promotion (`setLadder`, `updateTabBarStatuses`);
- `UpdateAgent` for the selected agent, and `UpdateTerminal` for the current terminal session.

It returns `m.gate.frame()` when either holds, and nil otherwise:

- **It promoted the row:** the status shown on rail cards, overview cards and the tab bar changed.
- **`m.drawsPane(session)`:** the session's output could be on screen in the current view mode. This covers the selected agent's and the current terminal's pane content, which the handler updates, wherever those panes are drawn.

| View mode | Panes it draws |
|---|---|
| `viewFocus` | the selected agent; the current terminal (`SplitPane.CurrentTerminalSessionName`); with the rail shown, every row of the focused slot's list, including rows scrolled out of the rail's window |
| `viewOverview` | every agent session in an open workspace |
| `viewWorkbench` | the selected agent |
| `viewWork` | none |

The filter leans toward drawing: a wrong `true` costs one shared frame, while a wrong `false` would freeze a visible pane. The spinner's ticks still invalidate about 12 times a second, so a pane the filter misses degrades to 12 fps rather than freezing. That is a backstop, not something the filter relies on.

## 6. Edge cases

- **The first frame, a resize, overlays, keys, mouse:** there is no cached view yet, or the message invalidates, so all of these render as today.
- **Side effects during render:**
  - `overviewData` heals the overview cursor (`normalizeOverviewCursor`);
  - the rail windows its rows from `scrollOffset`.

  They now run only on rendered frames. Pane output changes neither, since selection, collapse and slot changes come from keys or model events, which invalidate. The plan audits every side effect reachable from `render` for this.
- **Cursor and window title:** both are part of the cached `tea.View`, so they stay consistent with its content.
- **Inline attach:** the first echo renders at once (the leading edge), and a burst renders at most once per 16 ms, the renderer's own flush rate.
- **A stray `frameTickMsg`:** one arriving after a burst, or after quit, only invalidates.

## 7. Testing

- **`frameGate` with a fake clock:**
  - a first `frame()` renders at once;
  - a burst within 16 ms gives exactly one trailing tick;
  - at most one tick is pending;
  - `view` returns the cached view until `invalidate`;
  - a tick invalidates.
- **`app`, pinning both sides of each guard:**
  - for each view mode, output from a drawn pane asks for a frame, and output from an undrawn one (a cached terminal that isn't current, another tab's session in focus mode, anything in the work view) returns nil and leaves the cached view;
  - the Ready→Running promotion asks for a frame;
  - keys, a wake whose drain applied an event, and a link change invalidate;
  - a mutation check of each term of `drawsPane` on its own.
- **The full and `-race` suites.** The gate is touched only on the Update goroutine.

## 8. Success measure

- **In a `loomdev` sandbox,** with about 10 panes streaming spinner-like output, the TUI's CPU falls at least 3× against the build before the change, and output-driven renders stay at or below 60 a second.
- **On the owner's TUI,** once deployed, the perf profile from §1 is re-run, and `home.View`'s share and the TUI's CPU are compared with 79% and 0.25–0.55 of a core.

## 9. Docs

- `app/CLAUDE.md` holds the rule, in the room it has (about 380 bytes): pane output asks for a frame through `m.gate.frame()`, and only from a pane the screen draws (`drawsPane`); everything else invalidates in `Update`. The best fit is extending the "Periodic work and status" rule that sends output to the pane event messages.
- `docs/claude/debugging-freezes.md` §6: the finding gets the fix's commit and the measured result.
- `app/README.md`: the frame gate in the package's structure.

## 10. Components

| File | Change |
|---|---|
| `app/frame_gate.go` | new: the gate and `frameTickMsg` |
| `app/frame_gate_test.go` | new: the gate's tests |
| `app/app.go` | `View` → `render` behind `m.gate.view`; `Update` invalidates; the `paneDirtyMsg` handler returns `m.gate.frame()` or nil; `drawsPane` |
| `app/core_glue.go` | `drainCore` reports whether it applied any event |
| `app/link.go` | `checkLink` reports a change of link state |
| `app/app_init.go` | the gate's construction |
| `app/events_test.go` | the filter's and the wiring's tests |

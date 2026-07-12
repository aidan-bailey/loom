# Event-Driven Panes + Native Cursor — Finishing the Native Terminal Experience

**Status:** Approved design (2026-07-12)
**Predecessor:** [2026-06-19-native-terminal-experience-design.md](2026-06-19-native-terminal-experience-design.md) — its Phase 1 promised "event-driven `vtUpdatedMsg` replaces the poll"; the emulator shipped but the poll survived. This spec completes that promise and layers the remaining native-terminal affordances (hardware cursor, title, bell, focus) on top.
**Scope:** Pane update architecture (`app`, `session/tmux`, `session/vt`, `session`), plus cursor/title/bell/focus pass-through in both right-hand panes.

---

## 1. Problem

The data path is already event-driven; the UI is not. Every started instance
has a PTY attach whose pump goroutine writes bytes into its in-process
emulator the instant tmux emits them (`session/tmux/tmux.go:startOutputPump`).
But nothing tells the UI, so:

- **`previewTickMsg`** re-renders on a fixed 100ms timer (16ms during inline
  attach), polling an in-process emulator that was updated by events. Typed
  echo latency is bounded by tick cadence, not output arrival. An idle app
  still wakes 10–60×/second.
- **`tickUpdateMetadataCmd`** (500ms) ignores the emulator entirely: per
  active instance it spawns `tmux has-session` + `tmux capture-pane`
  subprocesses for liveness and status/prompt/trust detection, plus a
  conditional `git diff`. With 10 active sessions that is ~40 subprocess
  spawns/second at idle, and status transitions (Running/Ready/Prompting)
  lag by up to 500ms.
- **No cursor.** Panes are painted strings; the host terminal's real cursor
  is never positioned (Bubble Tea hides it unless `View.Cursor` is set), so
  the agent's input point is invisible. `vt.Emulator.Cursor()` exists but is
  called by nobody and hardcodes `Visible: true` — DECTCEM (apps hiding the
  cursor) and DECSCUSR (shape) are ignored.
- **Inner-app signals are discarded.** The emulator parses OSC titles, BEL,
  cursor visibility/style, and mode changes — x/vt exposes all of them via
  `Callbacks` — and Loom registers none.

## 2. Goals / Non-goals

**Goals**
- Replace timer-driven pane rendering with output-driven rendering
  (coalesced to a ~16ms frame budget). Idle app = zero wakeups.
- Move status/prompt/trust detection in-process (read the emulator, not
  `capture-pane`), driven by the same events.
- Demote polling to a slow health tick (3s) for the checks with no event
  source: `has-session` belt-and-braces, ptmx self-heal, diff stats.
- Show the **real hardware cursor** (host terminal's own blink/color/shape)
  at the focused pane's cursor cell, honoring DECTCEM and DECSCUSR.
- Pass through window title (OSC 0/2), bell (attention badge), and focus
  reporting (mode 1004) between host terminal and inner apps.

**Non-goals**
- tmux control mode (`-C`). The per-session PTY-attach architecture stays.
- Windows / `LOOM_PANE_RENDERER=snapshot` parity: those paths have no
  emulator and keep today's tick-based polling unchanged.
- fsnotify-based diff stats (diff refresh stays on the health tick, gated on
  dirty-since-last-tick).
- Cursor in the unfocused pane (tmux-style: active pane only). An iTerm2-ish
  hollow cursor for inactive panes was considered and rejected as noise.
- The auto-yes daemon (removed long ago) and full-screen attach — unchanged.

## 3. Decisions

| Decision | Choice | Why |
|---|---|---|
| Event transport | Pump → coalescer → `program.Send(paneDirtyMsg)` | `p.Send` is goroutine-safe by design; the pump is the earliest point that knows output arrived |
| Coalescing | Per-session: notify immediately if >16ms since last, else arm one trailing timer | Bounds a `yes`-spammy pane to ≤60 events/sec while keeping single-keystroke echo instant |
| Notifier plumbing | Package hook `tmux.SetOutputNotifier(func(sessionName string))`, set once at app startup | `ui/terminal.go` creates its own `TmuxSession`s; constructor threading can't reach them without touching every call site |
| Status detection | In-process from the emulator screen, triggered by dirty events; capture-pane variant kept for snapshot/Windows | Kills the 2N subprocesses per 500ms; status latency → near-instant |
| Liveness | PTY EOF in the pump fires an instant `ptyDeadMsg`; 3s health tick keeps `has-session` as belt-and-braces | EOF covers the common death; the tick covers pathological cases (wedged pump, external kills that leave the PTY open) |
| Cursor rendering | Hardware cursor via `tea.View.Cursor` (position + `Shape` + `Blink`), never a painted cell | Truly native: user's terminal renders its own cursor. `tea.Cursor{Position, Shape, Blink}` maps 1:1 to x/vt `Cursor{Position, Style, Steady, Hidden}` |
| Cursor state source | x/vt `Callbacks.CursorVisibility` / `CursorStyle` registered in the xvt wrapper; fields stored under the existing wrapper mutex | The vendored `x/vt` Emulator only exposes `CursorPosition()` publicly; visibility/shape/blink are reachable only via callbacks |
| Cursor policy | Focused pane (`FocusAgent`/`FocusTerminal`), always — hidden when: overlay up, pane scrolled off live tail, instance not running, or app hid it (DECTCEM) | User decision (tmux-style active-pane cursor); interact mode changes where keys go, not cursor visibility |
| Title format | `"{inner title} — loom"`; fallback `"loom — {instance title}"` | Inner title (e.g. Claude's ✳ status) is the signal; suffix keeps window lists identifiable |
| Bell | Attention badge on the instance's session-list row, cleared on selection | Backgrounded agent can ping; no host re-ring in v1 (config candidate later) |
| Focus forwarding | `View().ReportFocus = true`; forward `CSI I`/`CSI O` to the focused pane's PTY only when the inner app enabled mode 1004 (tracked via `EnableMode`/`DisableMode` callbacks); synthesize blur/focus on pane or session switch | Claude Code uses focus reporting for idle notifications; unconditional forwarding would inject garbage into apps that never enabled it |

## 4. Architecture

### 4.1 Event backbone

```
PTY bytes → pump (startOutputPump)
              ├→ emu.Write(bytes)                     (existing)
              └→ coalescer.notify()                   (new)
                   └→ program.Send(paneDirtyMsg{session})
                        ├→ selected agent/terminal session? → re-render pane
                        └→ owning instance → in-process status detect
PTY EOF   → program.Send(ptyDeadMsg{session})
x/vt Callbacks → title/bell/cursor/mode fields (read on demand from Update)
Health tick (3s): has-session, ptmx repair, diff stats (dirty-gated)
```

**Coalescer** (`session/tmux`): per-session struct — mutex, `lastSent
time.Time`, `timer *time.Timer`. `notify()`: if now−lastSent ≥ 16ms, send
immediately and stamp; else if no timer armed, arm one for the remainder.
Trailing-edge send guarantees the final state of a burst is always rendered.
Stopped when the pump exits.

**Notifier hook** (`session/tmux`): `SetOutputNotifier(func(sessionName
string))`, package-level, set once from `app.Run` after `tea.NewProgram`
(the `*tea.Program` handle already exists at `app/app.go:74`). Sessions with
no notifier (tests, snapshot mode) behave exactly as today. The notifier
value is read under `pumpMu` to keep `-race` clean.

**Messages** (`app`): `paneDirtyMsg{session string}`, `ptyDeadMsg{session
string}`. Update-goroutine handling only; no model mutation from the pump.

**Routing:** the app resolves `session →` (instance, pane) via the existing
naming scheme (`TmuxPrefix + title` for agent sessions,
`TerminalSessionName(title)` for terminal-pane sessions). A dirty event for
the selected instance's agent session re-renders the agent pane; for the
terminal pane's current session, the terminal pane; any other dirty event
runs status detection only (no render).

**Deletions:** `previewTickMsg` and its 100ms/16ms re-arm loop are removed.
The inline-attach fast tick is unnecessary — echo now renders on arrival.
The scrolled-pane re-render path (which today rides the tick) re-renders on
dirty events for the scrolled session instead (`newLinesBelow` accrual keeps
working; scroll gestures already trigger their own render via key handling).

### 4.2 In-process status detection

`Instance.CaptureAndProcessStatus` gains an emulator-backed path: when the
session has an emulator, source `content` from it instead of
`CapturePaneContent()` (subprocess). Trust-prompt handling
(`handleTrustPrompt`), prompt detection (`pendingPrompt`), and the content
hash (`processContentHash` — also feeds `ShouldRefreshDiff` and the
Running/Ready transition) all operate on the same string as before, so agent
adapters are untouched. Plain-text extraction from the emulator must strip
SGR the same way capture-pane's output is currently processed — a parity
test locks this (§7).

Trigger: dirty events (per instance, at coalesced cadence) instead of the
500ms tick. The `metadataReadyMsg` application logic in `app.Update`
(transitions, workspace-terminal restart circuit, tab-bar statuses) is
reused with per-instance granularity.

### 4.3 Health tick (demoted metadata tick)

`tickUpdateMetadataCmd` drops from 500ms to 3s and sheds status detection.
Remaining duties: `TmuxAlive()` (has-session), `PtmxAlive()` + `RepairPtmx`
self-heal, workspace-terminal restart circuit, and diff-stat refresh (full
for selected, short otherwise) gated on dirty-since-last-tick. On the
snapshot/Windows path it keeps today's full 500ms behavior including
capture-pane status detection.

`ptyDeadMsg` handling mirrors the tick's `!tmuxAlive` branch (mark Paused /
restart workspace terminal) but fires immediately — after confirming via
`DoesSessionExist()` that the session is really gone, since a dead PTY can
also mean a failed reattach with a live session (that case routes to
`RepairPtmx`, same as the tick's `!ptmxAlive` branch).

### 4.4 Native cursor

**`session/vt`:** `vt.Cursor` grows `Shape CursorShape` (block/underline/
bar) and `Blink bool`. The xvt wrapper registers `Callbacks.CursorVisibility`
and `Callbacks.CursorStyle` at construction, storing into fields guarded by
the wrapper's existing mutex (callbacks fire inside `term.Write`, which
already holds the write lock — they must only set fields, never call out).
`Cursor()` returns position + visibility + shape + blink;
`Visible: true` hardcoding is removed (DECTCEM respected).

**`session/tmux`:** `TmuxSession.CursorState() (vt.Cursor, bool)` — false
when no emulator (snapshot path → no cursor, as today).

**`ui`:** `SplitPane.CursorScreenPosition()` maps the focused pane's
emulator cell `(x, y)` to screen coordinates, reusing the pane-geometry
math that `HitTest` already implements in reverse (tab-bar offset + title
row + border + agent/terminal split). Returns ok=false when the focused
pane is scrolled off the live tail or showing a fallback.

**`app`:** `home.View()` sets `v.Cursor = tea.NewCursor(x, y)` (+ `Shape`,
`Blink`) when **all** hold: state is `stateDefault` or `stateInlineAttach`;
no overlay active; diff overlay not visible; focused pane at live tail;
selected instance started, not Paused/Recoverable; emulator cursor visible.
Otherwise `v.Cursor` stays nil (cursor hidden — Bubble Tea's default).
Cursor moves arrive as pump bytes → dirty event → re-render, so the cursor
tracks output with no extra plumbing.

### 4.5 Title, bell, focus

**Title:** xvt wrapper registers `Callbacks.Title`, exposes `Title()
string`. `home.View()` sets `v.WindowTitle` from the selected instance's
agent session: `"{inner} — loom"`, falling back to `"loom — {instance
title}"` when the inner app never set one.

**Bell:** `Callbacks.Bell` → notifier-style `program.Send(bellMsg{session})`
(reuses the coalescer's Send path, not the coalescer itself — bells are
rare and must not be swallowed by coalescing). App marks the owning
instance's list row with an attention badge (`ui/list.go`); cleared when
the instance becomes selected.

**Focus:** `home.View()` sets `v.ReportFocus = true`. On `tea.FocusMsg` /
`tea.BlurMsg`, write `\x1b[I` / `\x1b[O` to the focused pane's PTY iff that
pane's emulator has mode 1004 enabled (xvt wrapper tracks it via
`EnableMode`/`DisableMode` callbacks; exposed as `FocusReportingEnabled()
bool`). Pane-focus switches and session switches synthesize blur-to-old +
focus-to-new under the same gate. Host focus state is cached so a
just-focused pane receives the current state.

## 5. Concurrency

- Pump goroutine: `emu.Write` + `coalescer.notify` + `program.Send`. Send is
  goroutine-safe; the coalescer's timer callback also only calls Send.
- x/vt callbacks run inside `emu.Write` under the xvt wrapper's write lock;
  they set plain fields on the wrapper (same mutex domain) and never invoke
  app code. App reads (`Cursor()`, `Title()`, `FocusReportingEnabled()`)
  take the read lock from the Update goroutine — same discipline as
  `Render()` today.
- No model mutation off the Update goroutine: all reactions to
  `paneDirtyMsg`/`ptyDeadMsg`/`bellMsg` happen in `Update`. This preserves
  the invariant documented in CLAUDE.md (tea.Cmd goroutines must not mutate
  shared state).
- Everything verified under `go test -race` (CGO_ENABLED=1, CC=clang).

## 6. Error handling / edge cases

- **Notifier unset** (tests, snapshot mode, early startup before `SetOutputNotifier`): pump behaves exactly as today; health tick still transitions statuses on the snapshot path, so nothing is stranded.
- **Burst storms:** coalescer caps per-session event rate at ~60/s; render work is per-event bounded (one emulator Render + one status detect).
- **Dirty event for an unknown session** (session killed between Send and Update): dropped silently.
- **`ptyDeadMsg` races a live session** (reattach failure): gated on `DoesSessionExist()`; live → `RepairPtmx`, dead → Paused/restart path.
- **Cursor during selection drag:** unaffected — selection paints cells; the hardware cursor sits wherever the app's cursor is. If this proves visually noisy it can be suppressed while `sel.active` without design change.
- **Wide glyphs / cursor past EOL:** `tea.Cursor` takes cell coordinates from the emulator, which owns wrapping; clamp to pane bounds defensively in `CursorScreenPosition`.
- **Title/bell from non-selected instances:** title only read from the selected instance; bells badge any instance.

## 7. Testing strategy

- **Coalescer:** unit tests — single write → immediate event; burst → immediate + one trailing; quiescence resets. Fake clock.
- **Status parity:** table test feeding identical pane content through the capture-pane path and the emulator path; transitions and hashes must match (guards SGR-stripping drift).
- **Cursor mapping:** geometry tests alongside the existing hit-test tests (`ui/selection_hittest_test.go` pattern): known emulator cursor + pane layout → expected screen cell; scrolled/fallback → ok=false.
- **xvt callback state:** feed DECTCEM hide/show, DECSCUSR shapes, OSC 2 title, BEL, mode 1004 sequences through `Write`; assert exposed state. (Callbacks run under the write lock — assert no deadlock via the existing `-race` suite.)
- **Lifecycle:** `ptyDeadMsg` with live vs dead session; notifier-unset pump; health-tick dirty-gating for diffs.
- **Manual:** typed-echo latency in inline attach; cursor blink/shape in kitty + tmux-nested; `loom` idle CPU before/after (expect ~0 wakeups idle).

## 8. Phased roadmap

Each phase ships independently.

1. **Event backbone** — coalescer, notifier hook, `paneDirtyMsg`/`ptyDeadMsg`, delete `previewTickMsg`, render-on-dirty (scrolled panes included). *Outcome: instant echo, idle silence; ticks otherwise unchanged.*
2. **In-process status + health tick** — emulator-backed `CaptureAndProcessStatus`, dirty-driven detection, metadata tick → 3s health tick. *Outcome: subprocess storm gone, instant status transitions.*
3. **Native cursor** — vt.Cursor extension, callbacks, `CursorScreenPosition`, `View().Cursor`. *Outcome: the original ask — a real blinking cursor in the focused pane.*
4. **Title / bell / focus** — remaining callbacks, `WindowTitle`, attention badge, mode-1004-gated focus forwarding.

## 9. Risks & open questions

- **x/vt callback coverage:** vendored build (v0.0.0-20260615) confirmed to expose `CursorVisibility`, `CursorStyle`, `Title`, `Bell`, `EnableMode`/`DisableMode`; a future x/vt bump must preserve these (interface-guarded in `session/vt`).
- **Coalescer timer churn:** one `time.Timer` per active burst per session — negligible, but the timer must be stopped on pump exit to avoid Send-after-close of the tea program (Program.Send after Kill is a no-op; still, stop it).
- **Status-detection cadence change:** prompt detection now runs at burst cadence (≤60/s) instead of 2/s; `pendingPrompt` and `handleTrustPrompt` are string scans — cheap, but if profiling disagrees, detection can be sub-sampled (e.g. every Nth event or trailing-edge only) without design change.
- **Nested-tmux cursor:** when the host terminal is itself tmux, hardware-cursor shape (DECSCUSR) pass-through depends on host tmux config; position/visibility still work.

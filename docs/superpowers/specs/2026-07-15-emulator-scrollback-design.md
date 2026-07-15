# Emulator-Owned Scrollback — Design

**Date:** 2026-07-15
**Status:** Approved (design review with maintainer)
**Replaces:** capture-pane-windowed scroll-back in `ui/preview.go` / `ui/terminal.go`

## Problem

Pane scroll-back today windows the output of `tmux capture-pane -p -e -S - -E -`
on every scrolled re-render. A code audit (2026-07-15) found this design is the
root cause of a bug cluster:

1. **(High)** The capture subprocess runs synchronously on the Bubble Tea
   Update goroutine — up to ~60/s via `paneDirtyMsg` while scrolled, plus one
   per scroll keystroke. A slow tmux (5s timeout) freezes the UI.
2. **(High)** `TerminalPane.UpdateContent` holds `t.mu` across that subprocess,
   violating the repo lock rule; concurrent script calls block up to 5s.
3. **(Medium)** Anchor math assumes the captured buffer only grows; on shrink
   (clear-history, alt-screen exit, width-grow re-wrap) the offset drifts, then
   jumps at the clamp.
4. **(Medium)** Resize while scrolled corrupts the anchor: `lastTotal` counted
   physical rows at the old width, so the wrap-delta bumps the offset by a
   meaningless amount.
5. **(Medium)** The terminal pane is a diverged copy of the agent pane's scroll
   state machine and lost alt-screen handling — scrolling vim/htop in it
   windows a scrollback-less capture instead of forwarding wheel events.
6. **(Low–Med)** The 750ms alt-screen TTL cache (`isAgentTUI`) can misroute
   scroll across a mode transition, worst case injecting SGR mouse bytes into a
   plain shell.

Structural root cause: the anchor math needs a stable append-only line buffer,
but capture-pane re-materializes physical rows at the current width every call.
Findings 1–4 are symptoms; 5 is duplication drift.

## Goals

- Native feel: scrolled rendering is an in-process memory read, no subprocess,
  no lock held across I/O.
- Correct anchoring: append-only logical buffer; no width/clear/alt-screen
  drift.
- One scroll implementation shared by both panes.
- History from before Loom attached remains reachable (seeded from tmux once).
- Snapshot path (`LOOM_PANE_RENDERER=snapshot`, Windows) keeps capture-pane
  windowing as its fallback.
- Alt-screen TUIs keep wheel-forwarding.

## Non-goals

- Scrollback search (possible follow-up; the in-process buffer makes it easy).
- Fixing selection-slide under live output at the tail (pre-existing, display-
  coordinate selection is unchanged).
- Perfect scroll-position preservation across resize (explicitly reset to live
  tail instead — see Decisions).

## Design

### 1. `vt.Emulator` interface additions (`session/vt`)

The vendored `charmbracelet/x/vt` already maintains a scrollback buffer
(`Scrollback`, default 10k lines, pushed on scroll-off; alt-screen does not
push). Loom's wrapper exposes it:

```go
// ScrollbackLen returns the number of lines in the emulator's scrollback.
ScrollbackLen() int
// RenderWindow returns rows lines ending offset lines above the live
// bottom, drawn from scrollback + visible screen, ANSI-styled.
// offset=0 is equivalent to Render().
RenderWindow(offset, rows int) string
// AltScreen reports whether the inner app is on the alternate screen
// (modes 1047/1049).
AltScreen() bool
// SetScrollbackSize caps the scrollback line count.
SetScrollbackSize(n int)
```

`xvtEmulator` implements all four under its existing lock discipline:
readers take `RLock`; alt-screen state is callback-written (assign-only under
the write lock, per the xvt callback rules in CLAUDE.md). Cell-to-ANSI
rendering for scrollback lines reuses the same `x/ansi` machinery `Render`
uses. The no-op/snapshot emulator returns zero values (`ok`-style callers
treat no emulator as "fallback path").

### 2. Cold-history seed

On pump attach (`TmuxSession.Start`/`Restore`, already off the Update
goroutine), run one `tmux capture-pane -p -e -S - -E -1` — `-E -1` captures
**history rows only**, excluding the visible screen, so the seed cannot
duplicate the attach repaint the emulator mirrors. Store the result as an
immutable `[]string` on the `TmuxSession` (written under `stateMu`; snapshot
and release before any I/O, per existing rules). Failure → empty seed, log,
proceed with post-attach history only.

The logical scroll buffer is `seed ++ emulator scrollback ++ visible screen`,
append-only by construction. The seed is captured once per attach; a
re-attach (`Restore`) re-seeds, replacing the old seed (the new capture
includes everything the previous seed had plus what accrued while detached).

### 3. Shared `ScrollModel` (`ui/scroll.go`)

One struct owning all scroll state, embedded by `PreviewPane` and
`TerminalPane`:

- `offset` — lines-from-bottom; 0 = live tail.
- New-lines-below baseline for the jump-to-bottom footer: baselined at scroll
  start from total logical lines; appended count = `ScrollbackLen()` delta
  (stable cell lines, not re-wrapped physical rows).
- Wheel damping (`wheelAccum`, moved from PreviewPane).
- Alt-screen routing: when `em.AltScreen()`, scroll inputs forward wheel
  events (`ForwardWheel`) instead of moving the offset — now for both panes.

Scrolled rendering calls `em.RenderWindow(offset, rows)` prefixed by seed
lines when the window reaches above the emulator's scrollback. No subprocess,
no lock across I/O, no per-pane duplication.

### 4. Anchoring rules

- Output appended while scrolled: bump `offset` by the `ScrollbackLen()`
  delta so content under the cursor stays put. Scrollback trimming at the cap
  drops lines from the *front*, which does not disturb a from-bottom offset.
- Resize while scrolled: reset to live tail. The emulator reflows and line
  identities change; pretending the offset survived is how finding 4
  happened. (Revisit only if it annoys in practice.)
- Instance/session switch: reset to live tail (unchanged behavior).
- `GotoTop` sentinel and clamping semantics carry over, clamped against
  `seedLen + ScrollbackLen()`.

### 5. Snapshot/Windows fallback

No emulator → keep capture-pane windowing, with two hardenings:

- `TerminalPane` snapshots the session handle under `t.mu`, releases, runs
  the capture, re-locks to store (fixes finding 2 on the fallback too).
- Any shrink (`total < lastTotal`) or width change resets to live tail
  instead of drifting (graceful degradation for findings 3/4).

### 6. Deletions

- `CaptureHistory` calls from the scrolled render path (survives for seeding
  and the snapshot fallback only).
- `isAgentTUI`'s tmux probe + 750ms TTL cache (`altScreen`,
  `altScreenChecked`, `agentScrollTTL`); `IsAlternateScreen` survives only on
  the snapshot fallback.
- Both per-pane anchor state machines (`scrollStarting`,
  `totalAtScrollStart`, `lastTotal` duplication).

## Validation spike (first implementation task)

The one unknown: what tmux client redraws (attach repaint, `clear`, ED-based
full repaints) push into x/vt's scrollback. `ClearWithScrollback` preserves
cleared content as history — right for `clear`, potentially double-pushing on
repaints. Headless spike: drive a real tmux session through the emulator;
run `clear`, resize, detach/reattach; assert scrollback contents. If
pollution appears, suppress scrollback pushes during the attach-repaint
window (we control when Restore runs) before building on top.

## Testing

- Unit: `ScrollModel` anchoring (append while scrolled, trim at cap, reset on
  resize/switch), `RenderWindow` across seed/scrollback/screen boundaries,
  alt-screen routing for both panes, seed exclusion of the visible screen.
- Race: concurrent pump `Write` + `RenderWindow`/`ScrollbackLen` (pin the xvt
  lock discipline), run under `CC=clang CGO_ENABLED=1 go test -race`.
- Adapt existing regressions (`TestPaneDirtyRerendersScrolledAgent`,
  `TestPreviewTickRerendersScrolledAgent`) to the new source; the snapshot
  fallback keeps its current tests.
- The spike doubles as the integration test for redraw pollution.

## Error handling

- Seed capture failure: empty seed, warn once, post-attach history only.
- Emulator absent/closed mid-scroll: fall back to snapshot windowing (ok-form
  accessors already model this).
- `RenderWindow` with out-of-range offset: clamp internally; never panic.

## Rollout

Phased behind the existing renderer switch: the emulator path adopts
ScrollModel wholesale (no config flag — `LOOM_PANE_RENDERER=snapshot` is the
escape hatch, same as for event-driven rendering). CLAUDE.md's pane-renderer
section is updated to describe scrollback sourcing, replacing the stale
`RenderWindow`/`ScrollbackLen` mention with the real semantics.

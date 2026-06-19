# Native Terminal Experience — Embedded VT for Loom's Panes

**Status:** Approved design (2026-06-19)
**Scope:** Both the agent pane and the terminal pane in Loom's right-hand split.
**Goal:** Make Loom's panes feel like a real terminal — native scrolling, mouse selection / copy-paste, in-pane search, and low-latency rendering — by running an in-process terminal emulator for *display* while tmux remains the session owner.

---

## 1. Problem

Loom never emulates a terminal. Each agent runs in a tmux session, and Loom paints **snapshots** of it: it polls `tmux capture-pane -p -e` every ~100ms and renders the result into a Bubble Tea viewport (`session/tmux/tmux.go:718-747`, `ui/preview.go`, `ui/terminal.go`). Almost everything that feels "unnatural" traces back to this one choice — Loom renders photographs of a terminal, not the terminal itself:

- **Scrolling is a hard mode-switch.** Entering scroll mode does a synchronous, O(buffer) full-history capture on the render tick (`ui/preview.go`), detaches from the live tail, and auto-exits at `viewport.AtBottom()`. A scrolled pane goes stale; inline-attach tail isn't scrollable at all.
- **No copy-paste inside Loom.** The only path to select/copy is to inline-attach and drop into tmux's native copy-mode — i.e. leave Loom's model entirely.
- **Mouse is wheel-scroll only.** No drag-select, no click-to-focus.
- **Tick-based latency.** A fixed 100ms/33ms budget regardless of activity, plus a SHA-256 dedup dance to skip redundant renders.
- **Recurring fidelity bugs.** tmux-physical-rows vs. lipgloss-rewrap mismatch (the deliberate `-J` omission is a workaround); wrap/overflow has needed repeated fixing.
- **Four overlapping interaction modes** — quick-input bar (`a`/`t`), inline attach (`ctrl+a`/`ctrl+t`), full-screen attach (`alt+a`/`alt+t`), default keymap — that exist largely because the preview is dead and attaching is how you reach the live session.

## 2. Goals / Non-goals

**Goals**
- Native scroll that keeps tailing (no mode-switch), in both panes.
- Mouse drag-selection and copy to the system clipboard (incl. over SSH).
- In-pane search.
- Low-latency, event-driven rendering.
- A single, clear interaction model for typing into a pane.
- Higher render fidelity (correct widths, no rewrap mismatch).

**Non-goals**
- Replacing tmux. tmux keeps owning session persistence, crash recovery, and the full-screen "power" attach.
- Changing the auto-yes daemon. It runs as a separate process and keeps using `capture-pane` for prompt detection — explicitly untouched.
- Multi-pane-per-session tmux layouts (each session remains one pane).
- Full Windows parity for the VT path (Windows degrades to the snapshot path).

## 3. Decisions

| Decision | Choice | Why |
|---|---|---|
| Architecture | **Hybrid (Path C):** in-process VT for *display*, tmux underneath for persistence + power-attach | Native scroll/select/search/latency become inherent properties of one live buffer, without re-owning session persistence/recovery |
| Framework | **Bubble Tea v2 migration as Phase 0** | Cell-buffer renderer (less flicker, cheaper repaints), Kitty keyboard protocol (faithful key forwarding, lets us shrink the hand-rolled encoder), correct grapheme widths, distinct `PasteMsg` and mouse-message types |
| Interaction model | **Focus-to-interact** | Panes always live for scroll/select; to *type*, focus a pane → raw PTY, `esc` back to nav. Native where it counts; no leader-key tax; keeps Loom's single-letter Lua keymap intact |
| VT feed | **Redirect the existing output pump into the emulator** | The live stream already exists and is already mutex-guarded; beats `pipe-pane` (forward-only, second channel) and control mode (lossy/escaped) |
| VT library | **`charmbracelet/x/vt` behind an `Emulator` interface** | Best Charm-ecosystem fit (cell grid + scrollback + thread-safe `SafeEmulator`); pre-v1 risk is neutralized by the interface + a snapshot fallback impl |
| Clipboard | **OSC 52 primary + `atotto/clipboard` fallback** | OSC 52 tunnels through SSH; `atotto` (already a dependency) covers terminals without OSC 52 |

## 4. Key architectural finding

The live feed Path C needs **already exists and is currently discarded.** Loom runs `tmux attach-session` on a PTY it owns (`ptmx`), and a pump goroutine continuously reads that PTY straight into `io.Discard` (`session/tmux/tmux.go:367-397`, default dest `io.Discard` at `:373`). It reads it *only* to stop tmux's output buffer from filling and deadlocking keystroke input. Keystrokes already write to the same `ptmx` via `SendKeysRaw` (`session/tmux/tmux.go:521-528`). The recent `fix(tmux): guard ptmx and monitor with a mutex` (e87b10c) already made this concurrency-safe.

**The embedded-VT change is, at its core, redirecting that pump from `io.Discard` into a terminal emulator.** No `pipe-pane`, no fifo, no control-mode parsing.

Supporting setup:
- Add `set status off` for the session so the client stream is bare pane content (no tmux status line). Session options are already set near `session/tmux/tmux.go:245-264`.
- Seed initial scrollback once from `capture-pane -p -e -S -` (`CapturePaneContentWithOptions("-","-")`, `session/tmux/tmux.go:741`) when the emulator is created.
- Keep PTY/pane sizing in sync via the existing `SetDetachedSize` → `pty.Setsize` path (`session/tmux/tmux.go:689-705`), also calling `emu.Resize`.

## 5. Architecture

### New package: `session/vt`
A single interface, two implementations, pure and golden-file testable:

```go
type Emulator interface {
    Write(p []byte) (int, error)  // feed raw PTY bytes from the pump
    Resize(cols, rows int)
    Cell(x, y int) Cell           // grid access for rendering
    Scrollback() Lines            // accumulated history
    Cursor() (x, y int, shown bool)
}
```

- **`xvt`** — wraps `charmbracelet/x/vt.SafeEmulator`.
- **`snapshot`** — renders `capture-pane` output through the same interface. This is the **graceful-degradation path**: if `xvt` chokes on an agent's exotic escape sequence, that pane falls back to today's proven snapshot rendering with no crash and no special-casing. It is also the Windows path.

### Wiring
- **`session/tmux`** owns a `vt.Emulator` per session. The pump writes pump bytes to `emu.Write` instead of `io.Discard`; resize calls `emu.Resize`; `Restore()` builds a fresh emulator and reseeds.
- **`ui/`** pane renderers read the emulator's cells / scrollback / cursor and produce the lipgloss output, replacing the `capture-pane` snapshot render path in `preview.go` / `terminal.go`. Scroll / selection / search state lives in the pane model as a viewport over the emulator's grid+scrollback.
- **`app/`** turns pump activity into an event-driven render and hosts the focus-to-interact state machine.

### Data flow — push, not poll
Today a 100ms tick re-captures and re-renders regardless of activity. New model: the pump goroutine, on receiving bytes, coalesces them and emits a `vtUpdatedMsg` `tea.Cmd`; Bubble Tea renders **only when the emulator's screen actually changed**. A low-frequency safety tick remains as a backstop. This removes the tick-latency tension and the SHA-256 dedup dance (the emulator already knows whether its grid changed).

### Interaction model — Focus-to-interact
- **Nav (default state):** Loom's single-letter keymap intact; mouse wheel scrolls the hovered pane; mouse drag selects; a clear focused-pane indicator.
- **Interact:** click a pane (or one key, e.g. `i`/Enter) → keystrokes encode to PTY bytes and flow to `ptmx`; `esc` returns to nav. This **unifies inline-attach + quick-input** into one model. The `a`/`t` quick-input bar may remain as an optional fast path.
- **Full-screen attach (`alt+a`/`alt+t`)** is unchanged — the tmux power path.
- **Scroll / select / search never require "attaching"** — they operate on the live emulator buffer.

## 6. Error handling / edge cases

- **Malformed sequences:** `SafeEmulator` is hardened; on any emulator error, the pane falls back to the `snapshot` impl (same render code).
- **Resume / reconnect:** `Restore()` builds a fresh emulator and reseeds from `capture-pane -S -`; tmux still holds the session, so crash recovery is unchanged.
- **Deep scrollback divergence:** tmux optimizes redraws, so the VT's own scrollback is authoritative for the visible screen but may diverge for deep history. The live-accumulated buffer is authoritative for the visible screen; scrolling past it reconciles against tmux's authoritative `capture-pane -S -`.
- **Daemon (auto-yes):** separate process, keeps using `capture-pane` — zero coupling.
- **Windows:** the tmux/ptmx feed is unix-only; the VT path follows the existing `_unix.go` / `_windows.go` split and Windows degrades to the snapshot path.

## 7. Phased roadmap

Each phase ships and is verifiable on its own.

- **Phase 0 — Bubble Tea v2 migration.** `bubbletea`, `lipgloss`, `bubbles` → v2 (from v1.3.4 / v1.0.0 / v0.20.0). Migrate `Init`/`Update`/`View` signatures, mouse handling (`app/app.go:733`), and key dispatch to the new message types. No UX change; the enabler. Ship and stabilize first.
- **Phase 1 — Embedded VT display.** `Emulator` interface + `xvt` + `snapshot` fallback; redirect pump → emulator; render the emulator's visible screen into both panes; event-driven `vtUpdatedMsg` replaces the poll; `set status off` + capture-pane seed. *Outcome: same UX, lower latency, better fidelity, no capture-pane churn.* The riskiest correctness phase — verify render parity against `tmux attach`.
- **Phase 2 — Live scroll-while-tailing.** Decouple scroll position from the live tail; "jump to bottom / N new lines" affordance; deep history reconciles to `capture-pane -S -`. Kills the scroll mode-switch. Panes + diff overlay.
- **Phase 3 — Mouse selection + clipboard.** Drag-select cells/lines; OSC 52 (+ `atotto` fallback) copy; click-to-focus-pane.
- **Phase 4 — In-pane search.** `/` over the emulator buffer, `n`/`N` navigate, highlight matches. Panes + diff overlay.
- **Phase 5 — Focus-to-interact input.** Unify inline-attach + quick-input into "focus a pane → type, `esc` out"; v2 Kitty key fidelity lets us shrink/delete the hand-rolled `app/keybytes.go`; bracketed paste via `PasteMsg`. Full-screen attach unchanged.
- **Quick win (slot anywhere):** adjustable / swappable agent-terminal split (parameterize the hardcoded `SplitAgentPercent = 0.7`, `ui/consts.go:12`, and the chrome math in `ui/split_pane.go`).

## 8. Testing strategy

- **Golden-file emulator tests** — feed known ANSI byte streams, assert the cell grid (x/vt's strong suit); run both impls for parity where applicable.
- Extend `session/tmux/tmux_pump_test.go` for the pump → emulator wiring.
- Pane-render snapshot tests in `ui/`.
- Full suite under the race detector (CI already runs `-race`; locally `CC=clang CGO_ENABLED=1` since the repo defaults `CGO_ENABLED=0`).
- A manual verification harness: run a real agent session (claude/aider) and diff the VT render against `tmux attach` for fidelity.

## 9. Risks & open questions

- **`charmbracelet/x/vt` is pre-v1 / experimental.** Mitigated by the `Emulator` interface (cheap swap) and the `snapshot` fallback (runtime degradation). Pin the version.
- **Deep-scrollback reconciliation** between the VT's accumulated buffer and tmux's authoritative history is the main implementation subtlety in Phase 2 — settle the exact handoff point during implementation.
- **v2 migration surface area** touches the whole UI layer; it's mostly mechanical but should be its own stabilized phase before any VT work begins.
- **Full-screen-attach blocking** the main goroutine (audit item APP-13) is pre-existing and out of scope here, but worth keeping in mind as the attach paths get touched in Phase 5.

## 10. Key references (current code)

- Pump / ptmx (the feed): `session/tmux/tmux.go:367-425` (`startOutputPump`, `setPumpDest`, default `io.Discard` at `:373`)
- Session creation & options: `session/tmux/tmux.go:186-279`; `Restore()` `:308-337`
- Snapshot capture: `session/tmux/tmux.go:718-747`
- Keystroke injection: `session/tmux/tmux.go:482-528` (`SendKeys`, `SendKeysRaw`, `TapEnter`)
- PTY sizing: `session/tmux/tmux.go:689-705`
- Preview / scroll modes: `ui/preview.go`, `ui/terminal.go`; full-history `session/instance.go:1127-1137`
- Mouse / input dispatch: `app/app.go:733-789`, `app/keybytes.go`, `app/state_inline_attach.go`
- Split layout: `ui/split_pane.go`, `ui/consts.go:12`

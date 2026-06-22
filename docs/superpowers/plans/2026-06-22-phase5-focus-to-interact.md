# Phase 5 — Focus-to-interact input Implementation Plan

> REQUIRED SUB-SKILL: executing-plans. Checkbox (`- [ ]`) tracking.

**Goal:** Unify Loom's input model around "focus a pane → type & mouse into the agent, esc out." Keystrokes already forward to the PTY (today's inline-attach); Phase 5 adds **mouse-into-agent**, **bracketed paste**, a keyboard entry (`i`), and a non-esc-stealing exit. Quick-input (`a`/`t`) and full-screen attach (`alt+a/t`) stay.

**Decisions (from the user):**
- **Key to interact, click stays select.** Enter interact via `ctrl+a`/`ctrl+t` (existing) or new `i`; nav-mode click/drag keeps selecting (Phase 3). While interacting, mouse routes INTO the agent.
- **Keep the `a`/`t` quick-input bar** as a fast path.
- **Exit = double-`esc` (or `ctrl+q`).** Single `esc` forwards to the agent (Claude uses esc heavily); a quick double-tap exits. Honors "esc → nav" without stealing esc.
- **Keep `keybytes.go`** (reuse the working, tested encoder). v2-Kitty deletion is deferred — not required for the UX.
- **Internal state stays `stateInlineAttach`** (it *is* the interact state); only user-facing labels change to "interact/focus".

**Architecture:** The interact state already forwards keys via `keyMsgToBytes → SendKeysRaw(ptmx)`. We generalize `ForwardWheel` (SGR → ptmx) into `ForwardMouse` for click/drag/release, route mouse to the focused pane while in interact, send bracketed-paste-wrapped `PasteMsg` to the PTY, and add `i`/double-esc. Nav-mode selection is unchanged; the only behavior flip is that mouse in `stateInlineAttach` now forwards to the agent instead of selecting.

---

## Task 1: ForwardMouse SGR encoding (tmux + instance + panes)

**Files:** `session/tmux/tmux.go`, `session/instance.go`, `ui/split_pane.go`, `ui/terminal.go`; Test `session/tmux/forward_test.go`

- [ ] `TmuxSession.ForwardMouse(cb, col, row int, press bool) error`: writes one SGR mouse event `\x1b[<{cb};{col};{row}{M|m}` to the PTY (M=press, m=release). col/row floored at 1. Share a `writeSGRMouse` helper with `ForwardWheel`.
- [ ] `Instance.ForwardMouse(cb, col, row int, press bool) error` (guards isStarted/Paused/session, like `ForwardWheel`).
- [ ] `SplitPane.ForwardAgentMouse`/`ForwardTerminalMouse(cb, col, row int, press bool)` delegating to the focused pane's session (agent via the selected instance is passed in; terminal via the cached session — add `TerminalPane.ForwardMouse`).
- [ ] Tests: `ForwardMouse` writes the expected SGR bytes for left-press (cb=0,M), left-release (cb=0,m), drag (cb=32,M) — temp-file PTY read-back, mirroring `TestForwardWheel_WritesSGRToPTY`.

## Task 2: Bracketed-paste helper

**Files:** `session/instance.go`, `ui/split_pane.go`/`ui/terminal.go`; Test `session/...`

- [ ] `Instance.Paste(text string) error`: writes `\x1b[200~` + text + `\x1b[201~` to the PTY via the existing `SendKeysRaw` path. `TerminalPane.Paste` equivalent. No-op for empty text / dead session.
- [ ] Test: the wrapped bytes are produced (unit on the wrapping; the PTY write reuses tested `SendKeysRaw`).

## Task 3: app.go interact-mode mouse + paste routing

**Files:** `app/app.go`

- [ ] Remove `stateInlineAttach` from the selection gate (selection only in `stateDefault`): change the `MouseClickMsg` guard to `if m.state != stateDefault { return m, nil }` and clear any selection on entering interact.
- [ ] In the mouse cases, when `m.state == stateInlineAttach`: `HitTest(x-listWidth, y-tabBar.Height())` → if the hit pane == focused pane, forward to the agent: `MouseClickMsg`→ForwardMouse(cb=0,col+1,row+1,press=true); `MouseReleaseMsg`→(cb=0,…,press=false); `MouseMotionMsg`(dragging)→(cb=32,…,press=true); `MouseWheelMsg`→`ForwardWheel`. Else ignore.
- [ ] Add `case tea.PasteMsg:` — when `m.state == stateInlineAttach`, send `Paste(msg.Content)` to the focused pane (bracketed). Otherwise ignore (textinput overlays consume their own paste).

## Task 4: Interact entry (`i`) + double-esc exit + labels

**Files:** `script/defaults.lua`, `app/app_scripts.go`, `app/intents.go`, `app/state_inline_attach.go`, `app/app.go` (home field), `ui/menu.go`, `app/help.go`

- [ ] Add `i` binding in `defaults.lua` → a new `cs.actions.interact()` deferred intent → `runInteract(m)` enters interact on the currently focused pane (default agent), mirroring `runInlineAttachAgent` (reset scroll, SetInlineAttach(true), state=stateInlineAttach).
- [ ] `handleStateInlineAttachKey`: exit on `ctrl+q` (keep) OR double-`esc` (two `esc` within ~500ms). Add `lastEscAt time.Time` to `home`; first esc → record + forward esc bytes to the agent; second within window → exit. Any non-esc key resets the pending esc.
- [ ] Update user-facing strings: menu/help "attach" → "interact/focus"; note `i`/`ctrl+a` enter, double-esc/`ctrl+q` exit, mouse-into-agent.

## Task 5: Verify + tune

- [ ] gofmt, build, full `go test ./...`, `-race` on app/ui/tmux.
- [ ] Manual (`nix run .`): `i`/`ctrl+a` → interact; type into Claude; **click Claude's UI** (e.g. select a menu item); paste multi-line; single esc reaches Claude, double-esc exits; verify nav-mode drag-select still works. Iterate on SGR coordinate offsets (likely tuning point, as in Phase 3).

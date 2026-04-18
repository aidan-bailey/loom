# Scriptable hotkeys — design

**Date:** 2026-04-18
**Author:** Aidan Bailey
**Status:** Approved (brainstorming phase)

## Context

Today every hotkey in the TUI is hardcoded. `keys/keys.go` owns the `KeyName`
enum and its string map; `app/actions.go` wires each key to a Go handler via
`ActionRegistry`. The Lua scripting layer added in `script/` only runs *after*
the built-in dispatcher misses, so users can add new keys but cannot rebind,
unbind, or compose existing ones.

The goal of this change is to invert that relationship: the default keymap
becomes a Lua file shipped inside the binary, and user scripts can freely
override, remove, or sequence every binding. Built-in Go handlers are retired
in favor of a small set of host-exposed *primitives*, keeping the engine the
single source of truth for dispatch.

This unlocks:

- Rebinding any key without recompiling.
- Macro-style workflows (e.g. "press `P`: push every paused session").
- Conditional bindings driven by session state.
- A single, inspectable keymap in one Lua file.

## Goals

- Ship a built-in `defaults.lua` (embedded via `go:embed`) that reproduces the
  current keymap 1:1.
- Expose a named-action registry (`cs.actions.*`) so user scripts can call the
  same primitives the defaults use.
- Add `cs.bind(key, fn, opts)` / `cs.unbind(key)` as the canonical binding API,
  with `cs.register_action{...}` preserved as a back-compat alias.
- Support awaitable intents via `cs.await(intent)` so a single Lua handler can
  chain overlay-driven flows (confirmations, pickers, attach).
- Retire `keys.GlobalKeyStringsMap`, `ActionRegistry`, and every `runXYZ` in
  `app/actions_*.go`.
- Keep the app usable no matter what user scripts do: `ctrl+c` always quits,
  `--no-scripts` disables user scripts, and a parse error falls back to
  embedded defaults.

## Non-goals

- Per-workspace scripts. Scripts remain global (same as today).
- Scripting overlays (name entry, confirmation, pickers) from Lua. Overlays
  keep their own keymaps in Go; scripts trigger them via intents.
- Hot-reloading scripts at runtime.
- A richer scripting language. We stay on Lua 5.1 via `gopher-lua`.

## Scope — big-bang migration

All built-in hotkeys move to Lua in one change. No feature flag, no dual
dispatch path.

Retired: `KeyUp`, `KeyDown`, `KeyNew`, `KeyPrompt`, `KeyKill`, `KeySubmit`,
`KeyCheckout`, `KeyResume`, `KeyHelp`, `KeyQuit`, `KeyWorkspace`,
`KeyWorkspaceLeft`, `KeyWorkspaceRight`, `KeyFullScreenAttachAgent`,
`KeyFullScreenAttachTerminal`, `KeyDiff`, `KeyQuickInputAgent`,
`KeyQuickInputTerminal`, `KeyDirectAttachAgent`, `KeyDirectAttachTerminal`.

Retained in Go: `KeySubmitName` (overlay-owned, not part of the default
dispatcher).

## Primitives exposed on `cs.actions`

Two shapes based on whether the action needs the UI loop:

### Sync primitives (return immediately)

| Name | Effect |
|---|---|
| `cs.actions.cursor_up()` | Move list cursor up |
| `cs.actions.cursor_down()` | Move list cursor down |
| `cs.actions.toggle_diff()` | Toggle diff overlay |
| `cs.actions.workspace_prev()` | Previous workspace tab |
| `cs.actions.workspace_next()` | Next workspace tab |

These complete inline; no coroutine yield needed.

### Deferred primitives (return an intent; use `cs.await` to wait)

| Name | Default behavior | Opt-flags | Resolve value |
|---|---|---|---|
| `cs.actions.quit()` | Quit app | — | never resolves |
| `cs.actions.push_selected(opts)` | Confirm + push branch | `{confirm=false}` to skip dialog | `true`/`false` (pushed?) |
| `cs.actions.kill_selected(opts)` | Confirm + kill selected | `{confirm=false}` | `true`/`false` |
| `cs.actions.checkout_selected(opts)` | Help + confirm + checkout | `{confirm=false, help=false}` | `true`/`false` |
| `cs.actions.resume_selected()` | Resume paused session | — | `true`/`false` |
| `cs.actions.new_instance(opts)` | Name overlay → spawn | `{prompt=true}` for prompt flow, `{title="..."}` to skip overlay | instance handle or nil |
| `cs.actions.show_help()` | Open help overlay | — | closes when dismissed |
| `cs.actions.open_workspace_picker()` | Open picker | — | selected workspace or nil |
| `cs.actions.inline_attach_agent()` | Inline attach to agent pane | — | resolves on detach |
| `cs.actions.inline_attach_terminal()` | Inline attach to terminal pane | — | resolves on detach |
| `cs.actions.fullscreen_attach_agent()` | Full-screen attach (agent) | — | resolves on detach |
| `cs.actions.fullscreen_attach_terminal()` | Full-screen attach (terminal) | — | resolves on detach |
| `cs.actions.quick_input_agent()` | Open quick-input bar (agent) | — | submitted text or nil |
| `cs.actions.quick_input_terminal()` | Open quick-input bar (terminal) | — | submitted text or nil |

Deferred primitives return an `intent` userdata. The handler calls
`cs.await(intent)` to receive the resolve value.

## API surface

### `cs.bind(key, fn, opts?)`

Register a binding. `fn` runs inside an implicit coroutine so `cs.await`
works anywhere. `opts.help` sets the help-overlay label. Collisions with an
existing binding log a warning and keep the first registration (unchanged
from today).

### `cs.unbind(key)`

Remove a binding. No-op if unbound. Warn if `key == "ctrl+c"` (hard-reserved).

### `cs.register_action{key, help, run, precondition?}`

Preserved as a thin alias over `cs.bind`. The optional `precondition` gates
the run function silently.

### `cs.await(intent)`

Yields the current coroutine until the host resolves the intent, returning
the resolve value. Errors if called outside a bound handler.

### New host methods

`Host` gains:

- `CursorUp()`, `CursorDown()`, `ToggleDiff()`, `WorkspacePrev()`,
  `WorkspaceNext()` — sync primitives.
- `Enqueue(intent Intent) IntentID` — queue a deferred primitive and return
  an ID the engine uses to match the later `Resume`.

### Intent types

Each deferred primitive is backed by a struct in `script/intent.go`:
`QuitIntent`, `PushSelectedIntent`, `KillSelectedIntent`, `CheckoutIntent`,
`ResumeIntent`, `NewInstanceIntent`, `ShowHelpIntent`,
`WorkspacePickerIntent`, `InlineAttachIntent`, `FullscreenAttachIntent`,
`QuickInputIntent`. All embed `IntentID`.

### Intent lifecycle

1. Lua handler calls `cs.actions.push_selected()`; stub constructs a
   `PushSelectedIntent` and calls `cs.await(intent)`.
2. `cs.await` records the current coroutine in a table keyed by `IntentID`,
   then yields.
3. Engine returns control to `scriptHost.Dispatch`, which pops the queued
   intent from the host buffer and emits a `tea.Cmd` wrapping it.
4. `handleScriptDone` (on the main goroutine) receives the cmd, performs the
   UI work (overlay, push, attach, etc.), and when the flow completes posts
   a `scriptResumeMsg{id, value}`.
5. `handleScriptResume` takes the engine mutex and calls `Engine.Resume(id,
   value)` which reinvokes the stored coroutine.
6. The handler continues past `cs.await` with the resolve value. If the
   coroutine yields again (next intent), repeat from step 3; if it returns,
   discard the coroutine.

## `defaults.lua` sketch

```lua
-- Navigation
cs.bind("up",    cs.actions.cursor_up,    { help = "up" })
cs.bind("k",     cs.actions.cursor_up)
cs.bind("down",  cs.actions.cursor_down,  { help = "down" })
cs.bind("j",     cs.actions.cursor_down)
cs.bind("d",     cs.actions.toggle_diff,  { help = "diff" })

-- Lifecycle
cs.bind("n", function() cs.actions.new_instance{} end,
  { help = "new" })
cs.bind("N", function() cs.actions.new_instance{ prompt = true } end,
  { help = "new with prompt" })
cs.bind("D", function() cs.actions.kill_selected{} end,
  { help = "kill" })
cs.bind("p", function() cs.actions.push_selected{} end,
  { help = "push branch" })
cs.bind("c", function() cs.actions.checkout_selected{} end,
  { help = "checkout" })
cs.bind("r", function() cs.actions.resume_selected() end,
  { help = "resume" })
cs.bind("?", cs.actions.show_help,         { help = "help" })
cs.bind("q", cs.actions.quit,              { help = "quit" })

-- Workspace
cs.bind("W", cs.actions.open_workspace_picker, { help = "workspace" })
cs.bind("[", cs.actions.workspace_prev, { help = "prev ws" })
cs.bind("l", cs.actions.workspace_prev)
cs.bind("]", cs.actions.workspace_next, { help = "next ws" })
cs.bind(";", cs.actions.workspace_next)

-- Attach
cs.bind("alt+a",  cs.actions.fullscreen_attach_agent,
  { help = "fullscreen agent" })
cs.bind("alt+t",  cs.actions.fullscreen_attach_terminal,
  { help = "fullscreen terminal" })
cs.bind("ctrl+a", cs.actions.inline_attach_agent,
  { help = "attach agent" })
cs.bind("ctrl+t", cs.actions.inline_attach_terminal,
  { help = "attach terminal" })

-- Quick input
cs.bind("a", cs.actions.quick_input_agent,    { help = "input to agent" })
cs.bind("t", cs.actions.quick_input_terminal, { help = "input to terminal" })
```

User scripts in `~/.claude-squad/scripts/*.lua` load after defaults and may
rebind freely. Example macro:

```lua
-- Push every paused session, then announce.
cs.bind("P", function()
  local n = 0
  for _, inst in ipairs(cs.instances()) do
    if inst:paused() then
      cs.actions.resume_selected()  -- awaits until resumed
      if cs.actions.push_selected{ confirm = false } then
        n = n + 1
      end
    end
  end
  cs.notify(cs.sprintf("pushed %d paused session(s)", n))
end, { help = "push all paused" })
```

## Safety & error handling

**Hard-reserved keys** (cannot be unbound):

- `ctrl+c` → quit, dispatched in `app/state_default.go` *before* the engine
  sees the key. Even an empty keymap keeps the user in control.
- `enter` inside the name-entry overlay stays in Go. Overlays own their own
  keymaps in this phase.

**Fallback chain on load:**

1. Embedded `defaults.lua` loads first. Parse error here → panic (build bug).
2. Each user script loads independently. Parse/runtime failure during load →
   log a warning via `log.WarnKV`, skip that file, keep everything loaded so
   far. Startup surfaces a count of failed scripts.
3. `claude-squad --no-scripts` skips step 2 entirely and uses only embedded
   defaults.

**Runtime errors** inside a bound handler are caught by the engine, a
traceback is logged, the key is consumed, and the user sees a short toast.
The app keeps running.

**`cs.unbind` semantics:** removing an unbound key is silent; unbinding
`ctrl+c` logs a warning and is a no-op.

**Primitive validation:** unknown opt-flag keys or wrong types raise a Lua
error at call time so mistakes surface during testing. `cs.await` outside a
handler errors with a clear message.

**Backward compat:** `cs.register_action{key, help, run, precondition}` maps
to `cs.bind` with the `precondition` wrapped around the run function. All
existing sample scripts in `script/testdata/` keep working.

## Testing

- `script/engine_test.go`
  - Embedded `defaults.lua` loads and registers the expected count of keys.
  - User override replaces default handler.
  - User parse error → warning logged, defaults intact, no panic.
  - Empty user dir behaves like `--no-scripts`.
  - `cs.unbind("j")` removes the binding; `cs.unbind("ctrl+c")` warns + no-op.
  - `cs.await` yields, resumes with the host-supplied value, multiple
    sequential awaits complete in order, runtime error between awaits is
    isolated.
- `app/app_scripts_test.go`
  - One test per intent type: construct via host fake → assert `tea.Cmd`
    message type → feed result through `handleScriptDone` → verify resume.
- `app/state_default_test.go`
  - `ctrl+c` quits even with every key unbound.
  - Unknown key falls through cleanly.
  - Handler runtime error does not break the next dispatch.
- `app/actions_parity_test.go` (new)
  - For each retired `runXYZ`, assert the legacy key triggers the equivalent
    intent with the right arguments. Uses the same host fake.

## Critical files

- `keys/keys.go` — shrunk to `KeySubmitName` (overlay-owned) only.
- `app/actions.go`, `app/actions_*.go` — deleted. Replaced by
  `cs.actions.*` primitives in `script/api_actions.go`.
- `app/app.go`, `app/state_default.go` — hard-reserve `ctrl+c` before engine
  dispatch; remove `ActionRegistry.Dispatch`.
- `app/app_scripts.go` — expand `scriptHost` with sync primitives, intent
  queue, and resume plumbing. Add `handleScriptResume`.
- `script/engine.go` — add `Enqueue`, `Resume(id, value)`, coroutine
  tracking.
- `script/intent.go` (new) — intent types + ID.
- `script/api_actions.go` (new) — `cs.actions.*` Lua stubs.
- `script/api.go` — add `cs.bind`, `cs.unbind`, `cs.await`; keep
  `cs.register_action` as alias.
- `script/defaults.lua` (new, embedded) — 1:1 port of the current keymap.
- `main.go` — `--no-scripts` flag.
- `docs/specs/scripting.md` — update to describe the new binding model,
  primitive catalog, intent lifecycle, and safety rails.

## Verification

1. `go test -v ./script/... ./app/...` — unit + parity tests pass.
2. `go build -o claude-squad` and run the TUI; every existing keybinding
   behaves the same as before migration (navigate, kill with confirm, push
   with confirm, workspace picker, attach flows, quick input).
3. Drop a user script that rebinds `n` to `cs.notify("hi")`; confirm
   override wins.
4. Drop a malformed user script; confirm startup logs the warning, app
   loads, defaults intact.
5. Start with `--no-scripts`; confirm only defaults are active.
6. Press `ctrl+c` after `cs.unbind("ctrl+c")`; app still quits.

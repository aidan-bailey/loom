# Scripting

Loom ships an embedded Lua runtime that owns the entire default-state keymap. The stock hotkeys (`n`, `D`, `p`, `c`, `r`, `?`, `q`, arrow keys, workspace nav, attach/quick-input, etc.) live in `script/defaults.lua`, baked into the binary via `go:embed` and loaded before any user script. Users author additional bindings or replacement defaults in `~/.loom/scripts/*.lua`; those load after defaults and can override any binding except `ctrl+c` (panic-exit backstop). The runtime is a hard allow-list sandbox and is single-threaded under a mutex.

The implementation is Lua 5.1 via [`github.com/yuin/gopher-lua`](https://github.com/yuin/gopher-lua), vendored in `vendor/github.com/yuin/gopher-lua`.

## Canonical keymap

`script/defaults.lua` is the source of truth for the stock keymap. Every built-in hotkey is declared there as a `cs.bind` call. Reading that file is the fastest way to see exactly what keys do what — the [TUI Keybindings table in CLAUDE.md](../../CLAUDE.md) is a human-friendly summary, not the specification.

## Concepts

### Engine

A single `gopher-lua` state plus the bound-action table. One `Engine` lives for the lifetime of the app, owned by the `*home` model.

```go
// script/engine.go
type Engine struct {
    mu           sync.Mutex
    L            *lua.LState
    actions      map[string]*scriptAction
    order        []string        // insertion order for Registrations()
    loading      bool            // true only inside Load()/LoadDefaults()
    curFile      string          // script file currently being compiled
    reserved     map[string]bool // raw key strings owned by built-ins
    curHost      Host            // Host active for the current dispatch
    lastEnqueued IntentID        // most recent intent id for bare cs.await()
    pending      map[IntentID]*lua.LState // parked coroutines awaiting a resume
    logs         []LogEntry      // buffered ctx:log / cs.log output
}
```

`*lua.LState` is not goroutine-safe, so every entry point takes `e.mu`. See [Concurrency](#concurrency).

### Host

An interface implemented by `app/app_scripts.go#scriptHost` that lets the engine touch live TUI state without importing `app/` (which would be a cycle). It carries the sync primitives (cursor movement, diff toggle, workspace switching) plus the `Enqueue(Intent) IntentID` method that powers deferred actions.

```go
// script/host.go
type Host interface {
    // Queries
    SelectedInstance() *session.Instance
    Instances() []*session.Instance
    Workspaces() *config.WorkspaceRegistry
    ConfigDir() string
    RepoPath() string
    DefaultProgram() string
    BranchPrefix() string

    // Side-effects
    QueueInstance(inst *session.Instance)
    Notify(msg string)

    // Sync primitives (immediate)
    CursorUp()
    CursorDown()
    ToggleDiff()
    WorkspacePrev()
    WorkspaceNext()

    // Deferred primitives — returns an id the script can cs.await()
    Enqueue(intent Intent) IntentID
}
```

A fresh `scriptHost` is allocated per dispatch so pending instances, notices, and enqueued intents from one script can't leak into another.

### Script Action

The unit of registration: a key binding plus a handler function (and optional `help` text / `precondition` for `cs.register_action`).

```go
// script/engine.go
type scriptAction struct {
    key          string
    help         string
    file         string // source file, cited in error logs
    precondition *lua.LFunction // register_action path only
    run          *lua.LFunction
}
```

### Context (`ctx`)

A userdata value handed to handlers. Lives for the duration of a single call, then is discarded. `ctx` exposes methods that forward to the `Host` interface.

```lua
cs.bind("ctrl+shift+p", function(ctx)
  local inst = ctx:selected()
  if inst then
    ctx:notify("hello from " .. inst:title())
  end
end, { help = "say hello" })
```

## Directory Layout

Scripts are **always global** — stored at `~/.loom/scripts/`, not inside a workspace's `.loom/scripts/`. Defaults live inside the binary.

```
~/.loom/
├── config.json
├── state.json
├── workspaces.json
└── scripts/
    ├── my_override.lua
    ├── spawn_instance.lua
    └── ...
```

`app/app_scripts.go#scriptsDir` resolves the user directory via `config.GetConfigDir()`. Files load alphabetically (`loader.go#loadScripts`). Defaults load first, users next — so any `cs.bind` in a user file overwrites the default for that key.

## Safety Rails

Three layers guard against a broken script locking the user out of the TUI.

### Embedded defaults always load

`defaults.lua` is packaged into the binary via `go:embed`. `initScriptsIn` in `app/app_scripts.go` calls `engine.LoadDefaults()` before the user-script pass, so the stock keymap is live even when user scripts are absent, syntactically broken, or intentionally skipped.

### `--no-scripts` CLI flag

Passing `--no-scripts` to `loom` skips the user-scripts directory entirely; only the embedded defaults load. Use it to recover from a script that crashes the TUI on startup:

```bash
loom --no-scripts
```

Source: `main.go:269` wires the flag, `app/app.go:186` stores it as `skipScripts`, `app/app_scripts.go:211` skips the `engine.Load(dir)` call when set.

### Hard-reserved `ctrl+c`

The reservation operates at two layers:

1. **Binding-API layer (global, load time).** `buildReservedKeys()` puts `ctrl+c` and `ctrl+q` in the reserved set passed to `script.NewEngine`. `cs.bind` / `cs.unbind` silently refuse calls for those keys — a user script cannot register a handler for them, period.
2. **Dispatch layer (default state only).** `state_default.go#handleStateDefaultKey` checks `msg.String() == "ctrl+c"` **before** ever calling `dispatchScript` and returns `tea.Quit` unconditionally. Even if the binding layer were bypassed, the pre-dispatch check still wins in the default state.

Scope caveat: the script engine is only consulted from `stateDefault`. Other states (e.g. `stateQuickInteract`) never call `dispatchScript`, so `ctrl+c` / `ctrl+q` in those states are handled by the state's own widget (the textinput's line editor, the attach overlay's detach handler). That is by design — the hard-reserve prevents a user script from *stealing* those keys from the default state, not from overriding widget behavior in other states.

### Parse-error fallback

A user script that fails to compile is logged (to `~/.loom/logs/loom.log`) and skipped. Other scripts in the directory continue to load. Defaults remain live. The user sees the error in the log file on next inspection; they are not blocked from launching the TUI.

## Security

This is an **allow-list sandbox**. New `gopher-lua` versions cannot widen the attack surface without an explicit code change in `script/sandbox.go`.

### Allowed Standard Libraries

Only these five libraries are opened:

| Library | Purpose |
|---------|---------|
| `base` | Arithmetic, type introspection, `print`, `tostring`, `error`, `pcall`, etc. |
| `string` | String manipulation, pattern matching (minus `string.dump`). |
| `table` | Table manipulation. |
| `math` | Arithmetic and trig. |
| `coroutine` | Cooperative multitasking primitives. |

**Not opened**: `io`, `os`, `debug`, `package`. Script code has no way to read files, execute shell commands, access environment variables, or introspect the Go runtime.

### Stripped Globals

Even inside the allowed set, these escape hatches are nil'd out after library load:

| Name | Why it's removed |
|------|-----------------|
| `load`, `loadstring` | Execute arbitrary source at runtime. |
| `loadfile`, `dofile` | Pull source from disk outside the loader. |
| `require` | Module loading via `package` (which is never opened, but defense-in-depth). |
| `collectgarbage` | Could be used to probe the Go runtime; no legitimate script use. |
| `string.dump` | Serializes a function to bytecode, which `gopher-lua` can execute — bypasses our source-only load path. |

Source: `script/sandbox.go`.

### Userdata Boundary

All host objects (`session.Instance`, `git.GitWorktree`, `ctx`) are exposed as opaque userdata with metatables that restrict access to an explicit method list. Scripts cannot read Go struct fields directly or reach into unexposed methods.

### Untrusted Scripts

Scripts are user-provided, not downloaded. The sandbox protects against an author's *mistake* (e.g. accidentally calling a destructive API in a wide-matching handler) rather than a malicious script — a malicious script can still kill every instance, spam the log, or consume CPU. Users should treat `~/.loom/scripts/` the same way they treat `~/.bashrc`.

## API Reference

### Global `cs` Table

Installed as a global at engine construction (`script/api.go`).

| Symbol | Signature | Description |
|--------|-----------|-------------|
| `cs.bind` | `(key, fn, {help}?)` → void | Register a key binding. **Load-time only.** Overwrites existing bindings. |
| `cs.unbind` | `(key)` → void | Remove a binding. **Load-time only.** Silent no-op for reserved keys. |
| `cs.register_action` | `{key, help, precondition?, run}` → void | Table-form alias for `cs.bind`. Retained for back-compat and for handlers that want an explicit `precondition` — the precondition is evaluated before `run`, and a falsy return skips the action silently. |
| `cs.actions.*` | various | Catalog of host primitives — see [cs.actions catalog](#csactions-catalog). |
| `cs.await` | `(id?)` → any | Suspend the current coroutine until `Engine.Resume` delivers a value for `id`. Without an argument, waits on the most recently enqueued intent. See [Intent Lifecycle](#intent-lifecycle). |
| `cs.log` | `(level: string, msg: string)` → void | Buffer a log entry. Drained by the app into the main log file. `level` is free-form (`"info"`, `"warn"`, `"error"` are conventional). |
| `cs.notify` | `(msg: string)` → void | Send a transient message to the error/info bar. When called at load time, downgrades to a log entry. |
| `cs.now` | `()` → number | Unix time in seconds. |
| `cs.sprintf` | `(fmt, ...)` → string | Alias for `string.format`. Forgiving — non-string args are `tostring`'d before substitution. |

### `cs.actions` Catalog

`cs.actions.*` are the primitives `cs.bind` handlers call to make things happen in the TUI. They split into two categories.

**Sync primitives** run on the dispatch goroutine by calling a `Host` method directly. No overlay, no `tea.Cmd`, no coroutine yield.

| Primitive | Effect |
|-----------|--------|
| `cs.actions.cursor_up()` | Move the list selection up. |
| `cs.actions.cursor_down()` | Move the list selection down. |
| `cs.actions.toggle_diff()` | Toggle the diff overlay. |
| `cs.actions.workspace_prev()` | Focus the previous workspace tab. |
| `cs.actions.workspace_next()` | Focus the next workspace tab. |

**Deferred primitives** enqueue an `Intent` on the host and `Yield` the running coroutine with the resulting `IntentID`. Any UI work that opens an overlay or produces a `tea.Cmd` goes through this path. All deferred primitives take a single opt-table argument; unknown keys are ignored.

| Primitive | Opt-flags | Default | Intent |
|-----------|-----------|---------|--------|
| `cs.actions.quit()` | — | | `QuitIntent` |
| `cs.actions.push_selected{confirm=?}` | `confirm` | `true` | `PushSelectedIntent{Confirm}` |
| `cs.actions.kill_selected{confirm=?}` | `confirm` | `true` | `KillSelectedIntent{Confirm}` |
| `cs.actions.checkout_selected{confirm=?, help=?}` | `confirm`, `help` | `true`, `true` | `CheckoutIntent{Confirm, Help}` |
| `cs.actions.resume_selected()` | — | | `ResumeIntent` |
| `cs.actions.new_instance{prompt=?, title=?}` | `prompt`, `title` | `false`, `""` | `NewInstanceIntent{Prompt, Title}` |
| `cs.actions.show_help()` | — | | `ShowHelpIntent` |
| `cs.actions.open_workspace_picker()` | — | | `WorkspacePickerIntent` |
| `cs.actions.inline_attach_agent()` | — | | `InlineAttachIntent{Pane: Agent}` |
| `cs.actions.inline_attach_terminal()` | — | | `InlineAttachIntent{Pane: Terminal}` |
| `cs.actions.fullscreen_attach_agent()` | — | | `FullscreenAttachIntent{Pane: Agent}` |
| `cs.actions.fullscreen_attach_terminal()` | — | | `FullscreenAttachIntent{Pane: Terminal}` |
| `cs.actions.quick_input_agent()` | — | | `QuickInputIntent{Pane: Agent}` |
| `cs.actions.quick_input_terminal()` | — | | `QuickInputIntent{Pane: Terminal}` |

Source of truth: `script/api_actions.go` (primitives + Lua wiring), `script/intent.go` (Intent types).

### `ctx` Methods

A userdata handed to every bound handler. Lives for one dispatch.

| Method | Signature | Description |
|--------|-----------|-------------|
| `ctx:selected()` | → instance\|nil | The focused instance in the list panel. |
| `ctx:instances()` | → instance[] | 1-indexed array of every tracked instance. Mutating the array does nothing; use per-instance methods. |
| `ctx:find(title)` | → instance\|nil | First instance with a matching title. |
| `ctx:config_dir()` | → string | Resolved config directory for the active workspace. |
| `ctx:repo_path()` | → string | Repo root new instances should be created against. |
| `ctx:default_program()` | → string | The configured default agent command (e.g. `"claude"`). |
| `ctx:branch_prefix()` | → string | The branch prefix for the active workspace (e.g. `"alice/"`). |
| `ctx:new_instance{title=, ...}` | → instance | Create a new session. Required: `title`. Optional: `program`, `path`, `prompt`, `branch`, `auto_yes`. Instance is queued; actual `list.AddInstance` happens on the main goroutine after the script returns. |
| `ctx:log(level, msg)` | → void | Equivalent to `cs.log`. |
| `ctx:notify(msg)` | → void | Equivalent to `cs.notify` when dispatch is active. |

### `instance` Methods

Wraps `*session.Instance`. Obtained from `ctx:selected()`, `ctx:instances()`, `ctx:find()`, or `ctx:new_instance{}`.

| Method | Returns | Description |
|--------|---------|-------------|
| `inst:title()` | string | Session title. |
| `inst:status()` | string | Lowercase status (`"ready"`, `"loading"`, `"running"`, `"paused"`). |
| `inst:branch()` | string | Git branch name. |
| `inst:path()` | string | Repo path for this session. |
| `inst:program()` | string | Agent command. |
| `inst:auto_yes()` | bool | Auto-yes flag. |
| `inst:started()` | bool | True once the tmux session has been created. |
| `inst:paused()` | bool | True while the worktree is torn down. |
| `inst:diff_stats()` | {added, removed, content} \| nil | Diff stats. Nil if not yet computed. |
| `inst:preview()` | string, err? | Tmux pane contents. Returns `(nil, errmsg)` on failure. |
| `inst:send_keys(keys)` | void | Raw tmux `send-keys` to the **agent** pane. Raises on error. |
| `inst:send_terminal_keys(text)` | void | Send text followed by Enter to the instance's **terminal** pane (the bottom pane). Useful for launching out-of-TUI tools like `inst:send_terminal_keys("emacs " .. wt:path() .. " &")`. Raises if the terminal session is not cached (e.g. the instance was never visible) or has died. |
| `inst:send_prompt(text)` | void | Send text followed by Enter to the agent pane. Raises on error. |
| `inst:tap_enter()` | void | Send a single Enter keystroke. |
| `inst:pause()` | void | Pause the session. Raises on error. |
| `inst:resume()` | void | Resume a paused session. Raises on error. |
| `inst:kill()` | void | Kill and clean up. Raises on error. |
| `inst:worktree()` | worktree\|nil | The git worktree, or nil if none is attached (paused / unstarted / workspace terminal). |
| `tostring(inst)` | string | `instance(title, status)` for debugging. |

### `worktree` Methods

Wraps `*git.GitWorktree`. Obtained from `inst:worktree()`.

| Method | Returns | Description |
|--------|---------|-------------|
| `wt:branch_name()` | string | Branch name (e.g. `"alice/my_feature"`). |
| `wt:path()` | string | Absolute worktree path on disk. |
| `wt:repo_path()` | string | Absolute repo root (parent of the worktree). |
| `wt:is_dirty()` | bool, err? | True if the worktree has uncommitted changes. Returns `(nil, errmsg)` on git failure. |
| `wt:is_checked_out()` | bool, err? | True if the branch is checked out elsewhere. |
| `wt:commit(msg)` | void | Commit all changes with `msg`. Raises on error. |
| `wt:push(msg, open?)` | void | Commit and push. `open=true` opens a browser to the push URL; defaults to `false`. Raises on error. |

## Registration Rules

`cs.bind`, `cs.unbind`, and `cs.register_action` are **load-time only** — the engine sets `loading=true` inside `Load()` / `LoadDefaults()` and rejects registration outside that window with a Lua error. To change bindings at runtime, edit the source and restart.

### Key Collisions

| Collision | Behavior |
|-----------|----------|
| Against a reserved key (`ctrl+c`) | Registration skipped, warning logged. `ctrl+c` always means quit. |
| Against a previously loaded binding (default or earlier script) | **Overwrites** silently. This is how user scripts customize defaults. |

This is a behavioral change from the pre-migration spec: `cs.bind` is overwrite-semantics because overriding a default is the common case. If you need to know whether a key is already taken, call `cs.unbind(key)` first (it's a no-op for reserved keys) and then `cs.bind`.

### Required vs Optional Fields

```lua
-- Preferred form: positional handler with optional opts table
cs.bind("ctrl+shift+r", function(ctx)
  -- do work...
end, { help = "Resume all" })

-- Table form (alias): same effect, adds precondition
cs.register_action{
  key = "ctrl+shift+r",
  help = "Resume all",
  precondition = function(ctx)
    return #ctx:instances() > 0
  end,
  run = function(ctx)
    -- do work...
  end,
}
```

A `precondition` that returns falsy silently skips the handler. A precondition that raises surfaces as a dispatch error.

## Intent Lifecycle

Deferred primitives route through a 6-step enqueue → yield → Cmd → runXYZ → resume → continue lifecycle so Lua handlers can await overlay results or multi-step flows on the main goroutine without blocking.

1. **Enqueue.** A handler calls e.g. `cs.actions.push_selected{}`. The primitive calls `host.Enqueue(intent)`, which stores the intent on the `scriptHost` and returns a monotonically increasing `IntentID`.
2. **Yield.** The primitive calls `L.Yield(id)`. The coroutine suspends; `runAction` catches the yield and parks the coroutine in `engine.pending[id]`.
3. **Cmd.** When `Engine.Dispatch` returns from `runAction`, `dispatchScript` drains the `scriptHost` via `host.drain()` and returns the collected intents inside `scriptDoneMsg.pendingIntents`.
4. **runXYZ.** `app.Update` receives the `scriptDoneMsg` and walks each `pendingIntent`. `handleScriptIntent` checks preconditions (moved here from the retired `ActionRegistry`) and calls the matching `runXYZ` helper in `app/intents.go`. Each helper returns the same `tea.Cmd` it did pre-migration (e.g. `runSubmitSelected` opens the push-confirm overlay).
5. **Resume.** `handleScriptIntent` batches a `scriptResumeMsg{id}` with that `tea.Cmd`. When the message fires, `Engine.Resume(id, nil)` unparks the coroutine; any `cs.await(id)` call resumes with the delivered value.
6. **Continue.** The coroutine runs to completion or yields again on another deferred primitive, repeating the loop.

```
Lua: cs.bind("p", function()
       cs.await(cs.actions.push_selected{})  -- yields here
       cs.notify("pushed")                   -- runs after resume
     end)

Step:       [1 Enqueue][2 Yield]──┐
                                  ▼
                 host.intents += PushSelectedIntent
                                  │
                     [3 Cmd: scriptDoneMsg]
                                  │
                                  ▼
                     handleScriptIntent (app)
                                  │
                     [4 runSubmitSelected] → overlay opens
                                  │
                     [5 scriptResumeMsg]
                                  │
                                  ▼
                     Engine.Resume(id) — unparks coroutine
                                  │
                     [6 cs.notify("pushed")] runs
```

Source: `script/api_actions.go` (steps 1-2), `app/app_scripts.go#dispatchScript` and `#handleScriptDone` (step 3), `app/app_scripts.go#handleScriptIntent` + `app/intents.go` (step 4), `script/engine.go#Resume` (step 5).

**Forgotten `cs.await`**: Even if a handler enqueues a deferred primitive without calling `cs.await`, the primitive still yields — the coroutine is simply abandoned in `engine.pending` rather than racing ahead. Lua state remains consistent; the coroutine is collected when the LState is.

## Dispatch Flow

```
User keystroke
     │
     ▼
app/app.go: handleKeyPress
     │
     ▼
app/state_default.go: handleStateDefaultKey
     │
     ├── ctrl+c → tea.Quit (hard-reserved, pre-engine)
     │
     ├── Esc → dismiss diff / exit scroll mode (state-specific)
     │
     └── else ──► app_scripts.go: dispatchScript(key)
                      │
                      ├── Engine.HasAction(key) false → return (nil, false)
                      │                                     │
                      │                                     ▼
                      │                            caller no-ops key
                      │
                      └── true → return tea.Cmd, true
                                      │
                                      ▼
                               goroutine: Engine.Dispatch(key, host)
                                      │ (holds engine.mu)
                                      │
                                      ├── runAction(coroutine)
                                      │    ├── precondition → bail if falsy
                                      │    └── run(ctx)
                                      │
                                      ▼
                               scriptDoneMsg{err, pendingInstances, notices, pendingIntents}
                                      │
                                      ▼
                               Update: handleScriptDone
                                      │
                                      ├── list.AddInstance for each pending inst
                                      ├── errBox for each notice
                                      ├── handleScriptIntent for each intent (→ step 4 of Intent Lifecycle)
                                      └── if err: errBox
```

## Concurrency

`gopher-lua` is not goroutine-safe. The engine guarantees serialized Lua execution through `Engine.mu`:

1. `HasAction` — takes the mutex, cheap map lookup, releases. Called on the main goroutine to decide whether to schedule a dispatch.
2. `Dispatch` — takes the mutex, runs the handler inside a coroutine under it, releases when the coroutine yields or returns. Called from a `tea.Cmd` goroutine so the Bubble Tea main loop stays responsive while Lua executes.
3. `Resume` — takes the mutex, unparks a coroutine, runs until it yields or returns. Called from the main goroutine via a `scriptResumeMsg`.

**What this means for scripts**:
- A slow script blocks other scripts but not the TUI.
- Two keys bound to the same long-running script serialize.
- Scripts see a consistent view of the host state *between* host method calls, but not *across* the whole dispatch — a script that reads `ctx:instances()` twice may see different results if the main goroutine mutated the list in between.
- `cs.await` is cheap — the coroutine is parked, the mutex released, and no CPU is consumed until `Resume` delivers the value.

**What this means for the app**:
- `h.list.AddInstance` must run on the main goroutine. Scripts queue instances via `Host.QueueInstance`; finalization happens in `handleScriptDone`. Never call `AddInstance` from inside the Lua VM.
- Intent dispatch (`handleScriptIntent`) also runs on the main goroutine, from inside `Update`.
- Notices and the instance queue are buffered and surfaced through `scriptDoneMsg` so error-bar updates happen on the main loop.

## Error Handling

| Failure mode | Surfaced as |
|--------------|-------------|
| Script file fails to parse | Warning in the main log; load continues with remaining files. Defaults remain live. |
| `run` function raises a Lua error | Wrapped as `<file>: <error>`, returned from `Dispatch`, shown in the error bar. |
| `precondition` (register_action) raises | Wrapped as `<file>: precondition: <error>`, shown in the error bar. |
| Go panic inside userdata (shouldn't happen) | Recovered, Lua stack drained, wrapped as `script <file> panic: ...`. |
| Host method returns an error (e.g. `inst:send_keys` on a dead tmux session) | The userdata method raises a Lua error, which becomes a dispatch error via the above. |
| Intent precondition fails (e.g. `kill_selected` with nothing selected) | Intent is silently dropped in `handleScriptIntent`. The coroutine is resumed anyway so `cs.await` returns cleanly; handlers can observe the no-op by checking state via `ctx` after the await. |

Script log output via `cs.log` / `ctx:log` is buffered and drained asynchronously by the app — no dispatch-time coupling to the log subsystem.

## Example Scripts

Reference scripts ship in `script/testdata/`. Copy to `~/.loom/scripts/` to activate.

- `push_message.lua` — push the selected branch with a timestamped commit.
- `resume_all.lua` — resume every paused session.
- `spawn_instance.lua` — create a new session with a prefilled prompt.
- `open_emacs.lua` — bind `e` to launch emacs on the selected session's worktree as a detached process.

## Key Source Files

| File | Role |
|------|------|
| `script/defaults.lua` | Canonical stock keymap, embedded via `go:embed`. |
| `script/engine.go` | `Engine` lifecycle, `Dispatch`, `Resume`, `Load`, `LoadDefaults`, coroutine bookkeeping. |
| `script/sandbox.go` | Allow-list lib loader, escape-hatch stripping. See [Security](#security). |
| `script/api.go` | Installs the `cs` global (`bind`, `unbind`, `register_action`, `log`, `notify`, `now`, `sprintf`, `await`). |
| `script/api_actions.go` | Installs `cs.actions.*` (sync + deferred primitives). |
| `script/intent.go` | Deferred Intent types consumed by the app. |
| `script/loader.go` | Walks `~/.loom/scripts/`, runs each `.lua` file under `loading=true`. |
| `script/host.go` | The `Host` interface. |
| `script/userdata_ctx.go` | `ctx` userdata metatable and methods. |
| `script/userdata_instance.go` | `instance` userdata metatable and methods. |
| `script/userdata_worktree.go` | `worktree` userdata metatable and methods. |
| `app/app_scripts.go` | `scriptHost` adapter, `initScripts`, `dispatchScript`, `handleScriptIntent`, `handleScriptDone`. |
| `app/intents.go` | Preconditions + `runXYZ` helpers each intent routes to. |
| `app/state_default.go` | `ctrl+c` hard-reserve and single-point dispatch into the script engine. |
| `script/testdata/` | Sample scripts. |

## Design Decisions

**Lua, not JS/Python/a custom DSL.** `gopher-lua` is pure Go (no cgo, matches our `CGO_ENABLED=0` build), small, and embeddable with a single import. Lua 5.1's surface is small enough that a new user can skim the API reference and be productive; a bigger language would make the sandbox audit hard to keep honest.

**Defaults live in Lua, not Go.** Before the migration, built-in hotkeys were a Go `ActionRegistry` that users could not touch. Since every default now goes through `cs.bind`, users customize by editing a file in `~/.loom/scripts/` rather than forking and recompiling. The engine codepath is identical for defaults and user scripts — there is no special "built-in" tier.

**Scripts are global, not per-workspace.** Users think of custom keybindings as personal ergonomics, not project-specific config. A script that pushes branches or spawns review sessions should work across every repo the user opens.

**Allow-list sandbox, not deny-list.** Deny-lists silently widen when the underlying library gains new features. The allow-list in `sandbox.go` means a future `gopher-lua` release that adds a new standard library has no effect on us until someone changes that file.

**Load-time-only registration.** Letting scripts mutate the key map at runtime opens a pit of complexity: hot-reloading, conflict resolution mid-dispatch, state leakage between actions. Scripts register once at startup and are immutable thereafter.

**Overwrite on collision (defaults → user).** The common case is a user replacing a default binding; the error path would force `cs.unbind` + `cs.bind` everywhere. Overwrite-semantics keep the override surface terse and match how people actually use custom keymaps.

**Coroutine-based deferred intents.** The alternative — exposing tea.Cmd construction to Lua — would leak Bubble Tea internals into the sandbox. A coroutine + Intent enum keeps the API host-agnostic: Lua sees "enqueue this, await the result," and Go decides how to realize that intent on the main loop.

**Hard-reserved `ctrl+c`.** No matter what a user script does, `ctrl+c` in the default state always quits. This is the one footgun we refuse to let scripts take away.

**No `io`, `os`, or shell execution in the sandbox.** If a script needs to shell out, it should do it via an instance's tmux session (where the user already has agent output visible) rather than forking a subprocess the user cannot observe. This keeps the surface of "what scripts can do" bounded to "what the TUI already shows."

**Instance creation is queued, not immediate.** `ctx:new_instance{}` returns a userdata handle, but the actual `list.AddInstance` call happens on the main goroutine in `handleScriptDone`. This preserves the invariant that `h.list` is only mutated from the Bubble Tea loop, even though scripts execute in a `tea.Cmd` goroutine.

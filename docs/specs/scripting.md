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
    mu            sync.Mutex
    L             *lua.LState
    actions       map[string]*scriptAction
    order         []string        // insertion order for Registrations()
    loading       bool            // true only inside Load()/LoadDefaults()
    curFile       string          // script file currently being compiled
    reserved      map[string]bool // raw key strings owned by built-ins
    curActionFile string          // source file of the handler running now (runtime log lines)
    curCo         *lua.LState     // handler coroutine running now (yieldable checks it)
    inFlight      atomic.Pointer[inFlightAction]  // key/file of the handler holding mu, for Shutdown's warning
    bindings      atomic.Pointer[bindingSnapshot] // what HasAction/Registrations read without mu
    cancel        context.CancelFunc              // cancels the LState's context (Shutdown)
    curHost       Host                            // Host active for the current dispatch
    coroutines    map[IntentID]coroutineSlot      // parked coroutines awaiting a resume
    waitingIn     map[IntentID]string             // the yielding method each parked lifecycle call waits in
    lastEnqueued  IntentID                        // most recent intent id for bare cs.await()
    logs          []LogEntry                      // bounded test capture; real sink is log.For("script")
}
```

`*lua.LState` is not goroutine-safe, so every entry point takes `e.mu`. See [Concurrency](#concurrency).

### Host

An interface implemented by `app/app_scripts.go#scriptHost` that lets the engine touch live TUI state without importing `app/` (which would be a cycle). Its queries answer from a snapshot; its sync primitives record UI changes for the main goroutine to apply; and `Enqueue(Intent) IntentID` powers every deferred action, the lifecycle calls included, which reach the model as requests (see [Lifecycle calls](#lifecycle-calls)).

```go
// script/host.go
type Host interface {
    // Queries, answered from the snapshot taken when the dispatch or resume began
    SelectedInstance() (core.InstanceView, bool)
    Instances() []core.InstanceView
    ConfigDir() string
    RepoPath() string
    DefaultProgram() string
    BranchPrefix() string

    // Side-effects
    Notify(msg string)
    Enqueue(intent Intent) IntentID // deferred primitives, lifecycle calls, ctx:new_instance
    SendTerminalKeys(v core.InstanceView, text string) error

    // Sync primitives: recorded, applied on the main goroutine (handleScriptDone)
    CursorUp()
    CursorDown()
    ToggleDiff()
    WorkspacePrev()
    WorkspaceNext()
    ScrollLineUp()
    ScrollLineDown()
    ScrollPageUp()
    ScrollPageDown()
    ScrollTop()
    ScrollBottom()
    ScrollTerminalLineUp()
    ScrollTerminalLineDown()
    ScrollTerminalPageUp()
    ScrollTerminalPageDown()
    ResetAgentScroll()
    ResetTerminalScroll()
    ListPageUp()
    ListPageDown()
    ListTop()
    ListBottom()
    NextWaiting()
    PrevWaiting()
    ToggleRail()
    ToggleTerminalPane()
    ToggleOverview()
    ResizeSplitUp()
    ResizeSplitDown()
}
```

Instances are views: `core.InstanceView` values copied into the snapshot, never the live instance, and never the TUI's draft row (a creation flow's session-to-be, which has no instance yet). A fresh `scriptHost` is allocated per dispatch and per resume so notices, enqueued intents and recorded actions from one script can't leak into another. It holds no `*home`: the queries answer from a snapshot taken on the main goroutine (see [Concurrency](#concurrency)).

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

A userdata value handed to handlers. It lives for one handler run: the dispatch, plus any resumes after the handler yields on an intent (each resume rebinds it to that resume's host). `ctx` exposes methods that forward to the `Host` interface.

Don't keep a `ctx` across dispatches. A `ctx` saved in a Lua global and reused from a later dispatch still points at the host of the run that created it, which the app has already drained, so what it posts there (`ctx:notify`) is dropped and its reads come from that old snapshot (`ctx:new_instance` reaches the running handler's host, but takes its default program and path from that old snapshot, so it can create the session in the current workspace against the old snapshot's repository path). Use the `ctx` each handler receives.

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

Source: `main.go` wires the flag (`noScriptsFlag`) through `app.Run`, `app/app_init.go#startHome` stores it as `skipScripts`, and `app/app_scripts.go#initScriptsIn` skips the `engine.Load(dir)` call when set.

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
| `base` | Arithmetic, type introspection, `print` (replaced — see below), `tostring`, `error`, `pcall`, etc. |
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
| `setfenv`, `getfenv` | Read or replace another function's environment table, reaching past whatever scope handed it a closure. |
| `newproxy` | Creates a bare userdata a script can attach its own metatable to, which could otherwise forge a type our Go-side registrations treat as trusted. |
| `string.dump` | Serializes a function to bytecode, which `gopher-lua` can execute — bypasses our source-only load path. |
| `_printregs` | `print`'s lower-level twin; same stdout-corruption risk as `print` (see Replaced Globals), with no legitimate script use, so it is nil'd rather than replaced. |

Source: `script/sandbox.go`.

### Replaced Globals

Unlike the stripped globals above, `print` isn't nil'd — a missing `print` is a worse authoring experience than a working one, and scripts calling it is expected, not an attack. Instead `openSandbox` replaces the base library's `print` with a Go function that:

- never touches the real `os.Stdout`: base's `print` writes straight to it via `fmt.Print`, which would corrupt the TUI's alt-screen the moment a script called `print("debug")`;
- routes its output through the engine's script log instead — the same sink `cs.log`/`ctx:log` use, which writes straight to `log.For("script")` — at `info` level;
- joins its arguments with tabs and runs `tostring` on each (respecting `__tostring` metamethods), matching Lua's own `print` exactly.

Source: `script/sandbox.go`; test: `TestOpenSandbox_PrintRoutesToScriptLog` in `script/sandbox_test.go`.

### Userdata Boundary

All host objects (an instance as a `core.InstanceView` value, `git.GitWorktree`, `ctx`) are exposed as opaque userdata with metatables that restrict access to an explicit method list. Scripts cannot read Go struct fields directly or reach into unexposed methods. An instance userdata is a copy of the view, not the instance: it changes the instance only through requests to the model, and reaches its pane only through tmux, by the session name in the view.

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
| `cs.await` | `(id?)` → any | Suspend the current coroutine until `Engine.Resume` delivers a value for `id`. Without an argument, waits on the most recently enqueued intent. A `nil` argument returns `nil` at once: deferred `cs.actions.*` primitives already wait on their own, so `cs.await(cs.actions.X())` receives the action's `nil` return after the intent has run. See [Intent Lifecycle](#intent-lifecycle). |
| `cs.log` | `(level: string, msg: string)` → void | Write a log entry straight to `log.For("script")` (main log file), tagged with the source file: the one being loaded, or else the one whose handler is running. `level` is matched case-insensitively against `info`/`warn`/`warning`/`error`/`err`/`debug`; anything else logs at info. |
| `cs.notify` | `(msg: string)` → void | Send a transient message to the error/info bar. When called at load time, downgrades to a log entry. |
| `cs.now` | `()` → number | Unix time in seconds. |
| `cs.sprintf` | `(fmt, ...)` → string | Alias for `string.format`: it calls the real `string.format`, so every verb behaves as it does there. |

### `cs.actions` Catalog

`cs.actions.*` are the primitives `cs.bind` handlers call to make things happen in the TUI. They split into two categories.

**Sync primitives** call a `Host` method directly on the dispatch goroutine: no overlay, no `tea.Cmd`, no coroutine yield. The host only records the change; the app applies it on the main goroutine when the dispatch (or resume) returns (`handleScriptDone`), so the handler's own later reads don't see it.

| Primitive | Effect |
|-----------|--------|
| `cs.actions.cursor_up()` | Move the list selection up. |
| `cs.actions.cursor_down()` | Move the list selection down. |
| `cs.actions.toggle_diff()` | Toggle the diff overlay. |
| `cs.actions.workspace_prev()` | Focus the previous workspace tab. |
| `cs.actions.workspace_next()` | Focus the next workspace tab. |
| `cs.actions.scroll_line_up()` / `scroll_line_down()` | Scroll the active pane one line (the diff overlay when shown, else the focused pane). |
| `cs.actions.scroll_page_up()` / `scroll_page_down()` | Scroll the active pane one page. |
| `cs.actions.scroll_top()` / `scroll_bottom()` | Scroll the active pane to its top / back to the live tail. |
| `cs.actions.scroll_terminal_line_up()` / `scroll_terminal_line_down()` | Scroll the terminal pane one line, whatever has focus. |
| `cs.actions.scroll_terminal_page_up()` / `scroll_terminal_page_down()` | Scroll the terminal pane one page. |
| `cs.actions.reset_agent_scroll()` / `reset_terminal_scroll()` | Drop that pane back to the live tail (no-op when not scrolled). |
| `cs.actions.list_page_up()` / `list_page_down()` | Move the list selection one page. |
| `cs.actions.list_top()` / `list_bottom()` | Select the first / last row. |
| `cs.actions.next_waiting()` / `prev_waiting()` | Jump to the next / previous agent waiting for input (`]` / `[`). |
| `cs.actions.toggle_rail()` | Show or hide the session rail. |
| `cs.actions.toggle_terminal_pane()` | Show or hide the terminal pane. |
| `cs.actions.toggle_overview()` | Switch between focus mode and the overview grid (persisted per workspace). |
| `cs.actions.resize_split_up()` / `resize_split_down()` | Resize the agent/terminal split. |

**Deferred primitives** enqueue an `Intent` on the host and `Yield` the running coroutine with the resulting `IntentID`. Any UI work that opens an overlay or produces a `tea.Cmd` goes through this path. All deferred primitives take a single opt-table argument; unknown keys are ignored.

| Primitive | Opt-flags | Default | Intent |
|-----------|-----------|---------|--------|
| `cs.actions.quit()` | — | | `QuitIntent` |
| `cs.actions.push_selected{confirm=?}` | `confirm` | `true` | `PushSelectedIntent{Confirm}` |
| `cs.actions.kill_selected{confirm=?}` | `confirm` | `true` | `KillSelectedIntent{Confirm}` |
| `cs.actions.stash_selected{confirm=?, help=?}` | `confirm`, `help` | `true`, `true` | `StashIntent{Confirm, Help}` |
| `cs.actions.resume_selected()` | — | | `ResumeIntent` |
| `cs.actions.restart_with_options_selected()` | — | | `RestartWithOptionsIntent` |
| `cs.actions.open_review()` | — | | `OpenReviewIntent` |
| `cs.actions.new_instance{prompt=?, title=?}` | `prompt`, `title` | `false`, `""` | `NewInstanceIntent{Prompt, Title}` |
| `cs.actions.show_help()` | — | | `ShowHelpIntent` |
| `cs.actions.open_workspace_picker()` | — | | `WorkspacePickerIntent` |
| `cs.actions.open_settings()` | — | | `SettingsIntent` |
| `cs.actions.inline_attach_agent()` | — | | `InlineAttachIntent{Pane: Agent}` |
| `cs.actions.inline_attach_terminal()` | — | | `InlineAttachIntent{Pane: Terminal}` |
| `cs.actions.fullscreen_attach_agent()` | — | | `FullscreenAttachIntent{Pane: Agent}` |
| `cs.actions.fullscreen_attach_terminal()` | — | | `FullscreenAttachIntent{Pane: Terminal}` |
| `cs.actions.quick_input_agent()` | — | | `QuickInputIntent{Pane: Agent}` |
| `cs.actions.quick_input_terminal()` | — | | `QuickInputIntent{Pane: Terminal}` |
| `cs.actions.toggle_file_explorer()` | — | | `ToggleFileExplorerIntent` |
| `cs.actions.merge_selected()` | — | | `MergeSessionsIntent` |
| `cs.actions.new_from_issue()` | — | | `NewFromIssueIntent` |

These open the same flows the keys do; the lifecycle calls on an instance itself (`inst:kill()`, …) are [instance methods](#instance-methods), not `cs.actions`.

Source of truth: `script/api_actions.go` (primitives + Lua wiring), `script/intent.go` (Intent types).

### `ctx` Methods

A userdata handed to every bound handler. Lives for one handler run (the dispatch and its resumes; see [Context](#context-ctx)).

| Method | Signature | Description |
|--------|-----------|-------------|
| `ctx:selected()` | → instance\|nil | The focused instance in the list panel; nil when the list is empty or the selection is a creation flow's draft row. |
| `ctx:instances()` | → instance[] | 1-indexed array of every tracked instance (the draft row excluded). Mutating the array does nothing; use per-instance methods. |
| `ctx:find(title)` | → instance\|nil | First instance with a matching title. |
| `ctx:config_dir()` | → string | Resolved config directory for the active workspace. |
| `ctx:repo_path()` | → string | Repo root new instances should be created against. |
| `ctx:default_program()` | → string | The configured default agent command (e.g. `"claude"`). |
| `ctx:branch_prefix()` | → string | The branch prefix for the active workspace (e.g. `"alice/"`). |
| `ctx:new_instance{title=, ...}` | → instance | Create a new, unstarted session in the workspace focused when the dispatch, or the resume the call runs in, began. Required: `title`. Optional: `program` (default `ctx:default_program()`), `path` (default `ctx:repo_path()`), `prompt`, `branch`. Yields until the model's `Create` replies and returns the created instance (`Ready`, not started). Raises `new_instance: workspace changed while a script ran; not creating <title> here` if the user switched workspace while the script ran, and `new_instance: <err>` if the model refuses it. A [lifecycle call](#lifecycle-calls): not inside `pcall` or a callback. |
| `ctx:log(level, msg)` | → void | Equivalent to `cs.log`. |
| `ctx:notify(msg)` | → void | Equivalent to `cs.notify` when dispatch is active. |

### `instance` Methods

Wraps a `core.InstanceView`: the instance as the rail showed it when the dispatch or resume began (for `ctx:new_instance{}`, when it was created). Obtained from `ctx:selected()`, `ctx:instances()`, `ctx:find()`, or `ctx:new_instance{}`. Its reads change only when a lifecycle call on it succeeds: it then takes the session's view as the call left it, so `inst:status()` after `inst:pause()` reads `"Paused"`, and after `inst:resume()` `"Loading"` or `"Running"`. After `inst:kill()` the session is gone: the instance keeps the view it had when the kill was asked for, marked `"Deleting"`, and further calls on it are refused (`<op>: no such session`). Only the handle the call was made on refreshes: a second handle to the same session stays as it was (`local a, b = ctx:selected(), ctx:selected()`, then `a:pause()`: `a:status()` reads `"Paused"`, `b:status()` still what it read before). A killed handle's pane methods act on nothing: `preview()` reads `""`, `send_keys()` raises, `tap_enter()` does nothing. Otherwise, after a yield, read again through `ctx` for a fresh copy. The lifecycle methods (`kill`, `pause`, `resume`, `send_prompt`) are requests to the model ([Lifecycle calls](#lifecycle-calls)); the pane methods (`preview`, `send_keys`, `tap_enter`) act on the agent's tmux session by the name in the view.

| Method | Returns | Description |
|--------|---------|-------------|
| `inst:title()` | string | Session title. |
| `inst:status()` | string | Status as the rail shows it: `"Ready"`, `"Loading"`, `"Running"`, `"Prompting"`, `"Paused"`, `"Deleting"`, `"Recoverable"`. |
| `inst:branch()` | string | Git branch name. |
| `inst:path()` | string | Repo path for this session. |
| `inst:program()` | string | Agent command. |
| `inst:started()` | bool | True once the session has started (its tmux session was created). |
| `inst:paused()` | bool | True while the worktree is torn down. |
| `inst:diff_stats()` | {added, removed, content} \| nil | Diff stats. Nil if not yet computed. |
| `inst:preview()` | string, err? | The agent pane's visible screen, read with tmux `capture-pane` on the session named in the view (no attach client needed). Empty when the session is not started, is paused, or is gone. Returns `(nil, errmsg)` on failure. |
| `inst:send_keys(keys)` | void | Types `keys` into the **agent** pane as raw text, through tmux `load-buffer` + `paste-buffer`, not `send-keys`: the bytes arrive verbatim, so an escape sequence reaches the agent exactly as written, where the write to the attach client's PTY it replaced was parsed into keys by tmux and re-encoded for the pane's modes (DECCKM cursor keys, say). Raises on error, and on an unstarted or paused session. |
| `inst:send_terminal_keys(text)` | void | Send text followed by Enter to the instance's **terminal** pane (the bottom pane). Useful for launching out-of-TUI tools like `inst:send_terminal_keys("emacs " .. wt:path() .. " &")`. Raises if the terminal session is not cached (e.g. the instance was never visible) or has died. |
| `inst:send_prompt(text)` | void | Asks the model to type text into the agent pane (`load-buffer` + `paste-buffer`, as `send_keys`) and press Enter (`send-keys`), and waits until it has. Meanwhile the TUI holds input to the session: inline attach and other sends to it are refused. Raises on an unstarted or paused session (the model's refusal, e.g. `send a prompt to x: the session is paused`), a failed send (`send_prompt: <err>`), or while another send to it is in flight (`send_prompt: still sending the last prompt to x`). |
| `inst:tap_enter()` | void | Presses Enter in the agent pane (`send-keys`). Does nothing on an unstarted or paused session. |
| `inst:pause()` | void | Asks the model to pause the session (stash, end its tmux session, remove the worktree) and waits until it has; the rail shows the spinner meanwhile, and the record is saved. Raises on a session already paused (`pause: instance is already paused`), a workspace terminal or a busy session (Loading or Deleting; the model's refusal, e.g. `pause x: not allowed on a workspace terminal`), or a failed pause (`pause: <err>`). |
| `inst:resume()` | void | Asks the model to resume a paused session, or to recover a Recoverable one (as `r` does), and waits until it has; the rail shows the spinner meanwhile, and the record is saved. A recovered session is a new instance: the handle takes its view and ID. Raises the model's refusal (e.g. `resume x: the session is not paused (Running)`, or for a Recoverable one `recover x: …`) or, when the resume or recovery fails, `resume: <err>`. |
| `inst:kill()` | void | Asks the model to kill the session and clean up (tmux, worktree, branch) and waits until it has: the session leaves the list and storage. A Recoverable one is discarded. Raises on a workspace terminal or a busy session (the model's refusal, e.g. `kill x: the session is busy (Deleting)`), or a failed kill (`kill: <err>`). |
| `inst:worktree()` | worktree\|nil | A handle on the worktree, built from the view (repository, worktree path, branch); nil before the session has started and for a workspace terminal. |
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
2. **Yield.** The primitive calls `L.Yield(id)`. The coroutine suspends; `runAction` catches the yield and parks the coroutine in `engine.coroutines[id]`.
3. **Cmd.** When `Engine.Dispatch` returns from `runAction`, `dispatchScript` drains the `scriptHost` via `host.drain()` and returns the collected intents inside `scriptDoneMsg.pendingIntents`.
4. **runXYZ.** `app.Update` receives the `scriptDoneMsg` and walks each `pendingIntent`. `handleScriptIntent` checks preconditions (moved here from the retired `ActionRegistry`) and calls the matching `runXYZ` helper in `app/intents.go`. Each helper returns the same `tea.Cmd` it did pre-migration (e.g. `runSubmitSelected` opens the push-confirm overlay).
5. **Resume.** `handleScriptIntent` batches a `scriptResumeMsg{id}` with that `tea.Cmd`. When the message fires, `Engine.ResumeWithHost(ctx, id, host, value)` unparks the coroutine with the zero `ResumeValue`, which is `nil` in Lua: the primitive's call returns `nil`, and a `cs.await(id)` parked by hand returns `nil` as well. (A [lifecycle call](#lifecycle-calls) is resumed by its request's `Reply` instead.)
6. **Continue.** The coroutine runs to completion or yields again on another deferred primitive, repeating the loop.

Intent actions yield on their own, so a bare `cs.actions.push_selected{}` also waits, and the code after it runs only after the resume. Wrapping the call in `cs.await` is optional: the resumed action returns `nil`, and `cs.await(nil)` returns at once.

```
Lua: cs.bind("p", function()
       cs.await(cs.actions.push_selected{})  -- the action yields here
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
                     Engine.ResumeWithHost(…, id, …) — unparks coroutine
                                  │
                     [6 cs.notify("pushed")] runs
```

Source: `script/api_actions.go` (steps 1-2), `app/app_scripts.go#dispatchScript` and `#handleScriptDone` (step 3), `app/app_scripts.go#handleScriptIntent` + `app/intents.go` (step 4), `script/engine.go#ResumeWithHost` (step 5).

**No `cs.await` needed**: a handler that calls a deferred primitive without `cs.await` behaves the same as one that wraps it — the primitive yields, the coroutine is parked in `engine.coroutines`, and it resumes after the intent runs. A coroutine that yields anything other than an intent id (e.g. a raw `coroutine.yield()`) is dropped with an error from the dispatch or resume, and its context is cancelled.

### Lifecycle calls

`inst:kill()`, `inst:pause()`, `inst:resume()`, `inst:send_prompt(text)` and `ctx:new_instance{}` are deferred too, but their intent is a request to the model rather than a UI flow, and they keep the shape of a plain method: void (or the new instance), raise on error.

1. **Enqueue and yield.** The method enqueues an `InstanceOpIntent{ID, Title, Op, Text}` (for `ctx:new_instance`, a `CreateInstanceIntent{Title, Program, Path, Prompt, Branch}`), records the method it waits in (`Engine.waitingIn`), and yields.
2. **Request.** `handleScriptIntent` sends the request with a `ReqID` and does not resume the coroutine itself: `scriptInstanceOp` calls `core.Model.Kill`, `Pause`, `Resume` (`Recover` for a Recoverable session) or `SendPrompt`, and `scriptCreate` calls `core.Model.Create`, unstarted, in the workspace focused when the dispatch, or the resume the call runs in, began. A few calls the TUI refuses before any request, resuming at once with the error: `pause()` on a Paused session, `send_prompt()` while another send to that session is in flight, and `new_instance` when the user switched workspace while the script ran (decided before the script's recorded actions run, so the script's own `workspace_next()` doesn't count).
3. **Reply.** The model answers with a `core.Reply`: at once when it refuses the request (its precondition mirrors the keys' gates), when the job finishes otherwise. `scriptReplied` (`app/requests.go`) turns it into a `script.ResumeValue`. On failure, `Err` is the error the method raises (`scriptError`): a refusal's own message, which names the request and the session (`"kill x: not allowed on a workspace terminal"`), or `"<op>: <err>"` for a failed job, a gone session (`"kill: no such session"`) and every `ctx:new_instance` error. On success, `Instance` is the session's row as the model left it: `new_instance` returns it, and a lifecycle call's instance takes it as its view (the method still returns nothing; `Engine.luaValue`). A kill leaves no row, so its instance keeps the view it had when the kill was asked for, marked `Deleting`. A Reply's `Notice` (a stash the kill or resume could not drop, say) changes none of this: the model has shown it already.
4. **Resume and raise.** `Engine.ResumeWithHost(ctx, id, host, value)` resumes the coroutine with the Lua form of the `ResumeValue` (`nil`, the error string, or an instance userdata). A Lua wrapper around each of these methods (`raiseReturnedErrors`) raises an error string at the line that called the method. After a tail call (`return inst:kill()`) no frame names that line, and the error carries no position.

The raise ends the handler like any other Lua error, shown in the error bar prefixed with the script's file. When the request's job failed, the model's own notice shows first and the Lua error then replaces it in the error bar.

**Not inside `pcall` or a callback.** gopher-lua can't yield across a Go call, so these five methods refuse to run where they couldn't yield (`Engine.yieldable`): inside `pcall` or `xpcall`, inside a callback such as a `table.sort` comparator or a `string.gsub` function, in a `register_action` precondition, or in a coroutine the script created. There they raise `"<op>: cannot be called inside pcall or a callback (it waits for the TUI)"` and request nothing. A script therefore can't catch a lifecycle error with `pcall`; check state first (`inst:paused()`, `inst:status()`).

**Shutdown.** A call still parked when loom quits is resumed with `"<op>: loom is shutting down"`, which it raises, rather than with `nil`, which would read as a success that never happened.

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
                               scriptDoneMsg{err, pendingActions, notices, pendingIntents, slot}
                                      │
                                      ▼
                               Update: handleScriptDone
                                      │
                                      ├── is slot still focused? (decides where a new_instance may create)
                                      ├── apply each recorded action (sync primitives)
                                      ├── errBox for each notice
                                      ├── if err: errBox
                                      └── handleScriptIntent for each intent
                                            ├── UI intents → step 4 of Intent Lifecycle
                                            └── lifecycle calls, new_instance → a model request (Lifecycle calls)
```

## Concurrency

`gopher-lua` is not goroutine-safe. The engine guarantees serialized Lua execution through `Engine.mu`:

1. `HasAction` / `Registrations` — do **not** take the mutex. They read an immutable snapshot of the bindings (an `atomic.Pointer`) that `bind`/`unbind` republish on every change, so the main goroutine never waits on a running handler when deciding whether to schedule a dispatch or rendering the help screen.
2. `Dispatch` — takes the mutex, runs the handler inside a coroutine under it, releases when the coroutine yields or returns. Called from a `tea.Cmd` goroutine so the Bubble Tea main loop stays responsive while Lua executes.
3. `ResumeWithHost` — takes the mutex, unparks a coroutine, runs until it yields or returns. Called from the `tea.Cmd` goroutine `handleScriptResume` returns for a `scriptResumeMsg`, like `Dispatch`.

**What this means for scripts**:
- A slow script blocks other scripts but not the TUI: key lookups and the help screen read the bindings snapshot, not the engine mutex.
- Two keys bound to the same long-running script serialize.
- Host reads (`ctx:selected()`, `ctx:instances()`, `ctx:config_dir()`, …) come from a snapshot `newScriptHost` takes on the main goroutine when the dispatch begins, not from the live model, so they never see later changes — including the handler's own recorded sync primitives. When a handler resumes after an intent yield, its `ctx` is rebound to the resume's host, so reads after the yield see a fresh snapshot and `ctx:notify`/`ctx:new_instance` reach the resume's `scriptDoneMsg`.
- `cs.await` is cheap — the coroutine is parked, the mutex released, and no CPU is consumed until `Resume` delivers the value.

**What this means for the app**:
- Scripts never touch an instance or a workspace: the userdata holds views, and lifecycle calls and `ctx:new_instance` are intents the app turns into model requests on the main goroutine (`scriptInstanceOp`, `scriptCreate`); the model edits its workspaces itself. A `ctx:new_instance` creates in the workspace of the slot the dispatch or resume snapshotted, and only while that slot is still focused when the script's result lands.
- Intent dispatch (`handleScriptIntent`) also runs on the main goroutine, from inside `Update`.
- Notices, intents and recorded actions are buffered and surfaced through `scriptDoneMsg` so error-bar updates happen on the main loop.
- On quit, `Engine.Shutdown` drains parked coroutines and closes the LState within a bound (`scriptShutdownTimeout`); a coroutine parked in a lifecycle call raises `"<op>: loom is shutting down"`. If a handler is still running it cancels the LState's context, which stops a Lua loop at its next instruction. A handler blocked inside a Go call is left for process exit to reclaim, and the `engine_busy_at_shutdown` warning names its key and file.

## Error Handling

| Failure mode | Surfaced as |
|--------------|-------------|
| Script file fails to parse | Warning in the main log; load continues with remaining files. Defaults remain live. |
| `run` function raises a Lua error | Wrapped as `<file>: <error>`, returned from `Dispatch`, shown in the error bar. |
| `precondition` (register_action) raises | Wrapped as `<file>: precondition: <error>`, shown in the error bar. |
| Go panic inside userdata (shouldn't happen) | Recovered, Lua stack drained, wrapped as `script <file> panic: ...`. |
| A userdata method fails (e.g. `inst:send_keys` on a dead tmux session, `inst:send_terminal_keys` with no cached terminal session) | The method raises a Lua error, which becomes a dispatch error via the above. |
| A lifecycle call or `ctx:new_instance` is refused or fails | The method raises at the calling line ([Lifecycle calls](#lifecycle-calls)), surfacing as a dispatch or resume error: a refusal's own message (`kill x: …`), or `<op>: <err>` for a failed job, a gone session and `ctx:new_instance`. A failed job's notice from the model shows first; the raise replaces it. |
| A lifecycle call inside `pcall`, a callback, a precondition or a script's own coroutine | Raises `<op>: cannot be called inside pcall or a callback (it waits for the TUI)`; nothing is requested. |
| loom quits while a lifecycle call waits | The call raises `<op>: loom is shutting down` during the shutdown drain, logged as `cleanup_resume_failed`. |
| Intent precondition fails (e.g. `kill_selected` with nothing selected) | Intent is silently dropped in `handleScriptIntent`. The coroutine is resumed anyway so `cs.await` returns cleanly; handlers can observe the no-op by checking state via `ctx` after the await. |

Script log output via `cs.log` / `ctx:log` writes straight to `log.For("script")` inside the same call, under `e.mu` — no separate drain step, and no coupling to the app's Update loop (the app never calls into the engine's log path). Each line carries a `file` attribute: the file being loaded, or at runtime the file that bound the running handler.

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
| `script/intent.go` | Deferred Intent types consumed by the app, the lifecycle calls' (`InstanceOpIntent`, `CreateInstanceIntent`) included, and `ResumeValue`. |
| `script/loader.go` | Walks `~/.loom/scripts/`, runs each `.lua` file under `loading=true`. |
| `script/host.go` | The `Host` interface. |
| `script/userdata_ctx.go` | `ctx` userdata metatable and methods. |
| `script/userdata_instance.go` | `instance` userdata (a `core.InstanceView`) metatable and methods; the yielding lifecycle methods (`lifecycleOp`, `yieldable`) and their raise wrapper (`raiseReturnedErrors`). |
| `script/userdata_worktree.go` | `worktree` userdata metatable and methods. |
| `app/app_scripts.go` | `scriptHost` adapter, `initScripts`, `dispatchScript`, `handleScriptIntent`, `handleScriptDone`, and the lifecycle calls' requests (`scriptInstanceOp`, `scriptCreate`). |
| `app/requests.go` | The TUI's request book: `scriptReplied` turns a lifecycle call's `Reply` into its `ResumeValue`. |
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

**Instance changes are model requests.** A script never holds an instance: its userdata is a copy of a `core.InstanceView`, and `kill`, `pause`, `resume`, `send_prompt` and `ctx:new_instance` yield until the model's `Reply`. A script's changes therefore pass the same preconditions, show the same spinner, save the same records and show the same notices as the keys' (before daemon stage 1C a Lua kill left the row and its record behind, and a Lua pause or resume neither saved nor showed the spinner), and only the model edits a workspace, even though scripts execute in a `tea.Cmd` goroutine. The cost is that these five methods can't run inside `pcall` or a callback.

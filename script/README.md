# script

The Lua scripting engine, on `github.com/yuin/gopher-lua`. Every key in the TUI's default state dispatches through it. The full API, errors included, is [`docs/specs/scripting.md`](../docs/specs/scripting.md); this page covers the engine's own shape.

## Loading

- The full built-in keymap lives in `defaults.lua`, embedded via `go:embed` and loaded at engine init before any user script.
- Users extend or override bindings from `~/.loom/scripts/*.lua` (the config dir's `scripts/`: user-supplied `*.lua` files; global, shared across workspaces, not per workspace), loaded at startup in filename order after the defaults (`loader.go`). `cs.bind` and `cs.register_action` overwrite each other, so the last write wins and a user binding for a built-in key replaces it. `ctrl+c` is reserved: binding it is dropped with a warning.

## Dispatch

- The TUI drives the engine (`script.Engine`) from `app/state_default.go` through `app/app_scripts.go`'s `scriptHost` adapter (`host.go` is the interface it implements). Every dispatch runs under `engine.mu` on a `tea.Cmd` goroutine, and the TUI awaits `scriptDoneMsg`.
- Lua changes instances only through the model: `inst:kill()`, `inst:pause()`, `inst:resume()`, `inst:send_prompt()` and `ctx:new_instance{}` enqueue an intent (`InstanceOpIntent`, `CreateInstanceIntent`, `intent.go`) and yield until the request's `Reply` resumes them (`script.ResumeValue`, `Engine.ResumeWithHost`).
- On failure they raise (`scriptError`) a refusal's own message, which names the request (`"kill x: not allowed on a workspace terminal"`), or `"<op>: <err>"` for a failed job, a gone session and `ctx:new_instance`. On success the instance a lifecycle call acted on takes the session's view as the call left it (`Engine.luaValue`), so `inst:status()` after `inst:pause()` reads `Paused`; a killed one keeps its last view, marked `Deleting`.
- Because they yield, they refuse to run inside `pcall`/`xpcall`, a callback, a precondition or a coroutine the script made (`yieldable`), so a script can't catch their errors.
- Deferred `cs.actions.*` intents yield on their own, so a bare call already waits for the intent; `cs.await(cs.actions.X())` also works, because the resumed action returns nil and `cs.await(nil)` returns at once.
- `preview`, `send_keys` and `tap_enter` act on tmux by the view's session name (`tmux.NewSessionNamed`).

## API surface

`cs.bind`/`cs.unbind`/`cs.register_action`, `cs.actions.*` (sync primitives and deferred intent factories, `api_actions.go`), `cs.await`, `cs.log`, `cs.notify`, `cs.now`, `cs.sprintf` (`api.go`), plus userdata wrappers for `core.InstanceView` (the dispatch-time view: the host serves views, never the draft row; `userdata_instance.go`), `git.GitWorktree` (built from the view, `userdata_worktree.go`) and a per-dispatch `ctx` (`userdata_ctx.go`).

## Sandbox and logging

- Hard-sandboxed (`script/sandbox.go`): only `base`, `string`, `table`, `math` and `coroutine`, with `dofile`, `loadfile`, `load`, `loadstring`, `require`, `string.dump`, `collectgarbage`, `setfenv`, `getfenv`, `newproxy` and `_printregs` stripped.
- `print` isn't stripped but replaced: base's version writes straight to process stdout via `fmt.Print`, which would corrupt the TUI's alt screen, so the replacement tab-joins `tostring`'d args (matching Lua's own `print`) and routes them into the engine's script log at info level.
- That log is the same sink `cs.log` and `ctx:log` use: `Engine.logScript` (`script/engine.go`) writes straight to `log.For("script")`, synchronously, tagged with the file being loaded (`curFile`) or else the file whose handler is running (`curActionFile`). `DrainLogs` is only a bounded test capture.

## Tests

`host_fake_test.go` is the fake host the engine tests drive; `testdata/` holds sample scripts. The app-side round trip (intents becoming requests, Replies resuming coroutines) is tested in `app/script_requests_test.go`.

# internal/devsandbox

Dev sandboxes for developing loom inside loom. A sandbox is a named, persistent dir under `BaseDir()` (`${XDG_STATE_HOME:-~/.local/state}/loom-dev/<name>`) holding a dev build, a toy workspace, seeded config, its own global dir and a private tmux server (`loomdev-<name>`), so a dev loom runs from inside a loom pane without touching the host's sessions or state. Design: [`docs/superpowers/specs/2026-09-16-dev-sandbox-design.md`](../../docs/superpowers/specs/2026-09-16-dev-sandbox-design.md). CLI: `tools/loomdev`; deterministic agent stand-in: `tools/fakeagent`. `tools/` is excluded from the Nix package.

## Layout and setup (`sandbox.go`, `up.go`, `toyrepo.go`)

- `Open` validates the name and computes the paths: `bin/` (`LoomBin`, `FakeAgentBin`, `PersonaDir`), `global/` (`GlobalDir`, the sandbox's `LOOM_GLOBAL_DIR`), `home/` (`HomeDir`, its `LOOM_HOME`), `repo/` (the toy workspace, `WorkspaceName`) and `origin.git`; `sandbox.json` is its `Meta` (source worktree, build SHA, the sticky real-Claude flag).
- `Up` creates the sandbox or tops up a partial one: the toy repo with a bare origin, the sandbox config and state seeded into both `global/` and `repo/.loom/`, and the registry entry. Each step checks for its own artifact, so re-running is safe; it warns when the sandbox was last used from another source worktree and refuses a path holding whitespace (loom splits program strings on spaces).
- `Build` compiles loom and fakeagent from the source worktree; `BuildLoom` compiles another build beside it, with ldflags (a newer release for the replace-the-daemon tests, say).
- `Env` is the overlay every sandboxed process runs with (`LOOM_TMUX_SOCKET`, `LOOM_GLOBAL_DIR`, `LOOM_HOME`); `Environ` appends it to the process environment, and `Cmd` runs a command in it from the toy repo.

## The daemon

A sandboxed loom spawns the sandbox's own daemon (its own global dir), which outlives the driver. `Daemon` reads its lock record, `StopDaemon` stops it, `Stop` quits the driver and stops the daemon while `StopDriver` leaves it running, `Start` with `Restart` does both first, and `Down` stops it before removing the sandbox and the socket files a killed daemon or tmux left outside it. A lock holder that won't stop (a daemon past its stop timeout, a loom from before the daemon) makes `Down` fail with nothing removed, its error naming `loomdev down --force`; `ForceDown` (`--force`) kills it with SIGKILL instead, but only once its executable is proved a build in the sandbox's bin dir (`killSandboxLoom`, `runsSandboxLoom`), since a stale record's pid may have been reused. `List` reports every sandbox with its daemon, and `TailLogs` the end of each log (`LogFiles`: the TUI's `loom.log` files, the daemon's `serve.log` and `serve-crash.log`).

## The driver (`driver.go`)

A headless loom in a tmux session (`DriverSession`, no loom prefix) on the sandbox's private socket: `Start` (`StartOptions`: pane size, command, restart), `SendKeys`/`SendText`, `Screen`, `WaitFor` (whose timeout error carries the screen) and `Stop`.

`Start` keeps a running driver and replaces a dead one, which `remain-on-exit` keeps for diagnosis. The private server exits with its last session, so the `new-session` after killing the old driver (or after `Stop`, for `Restart`) can reach that server while it exits; tmux then answers "server exited unexpectedly" having run nothing, and `Start` tries once more, which starts a fresh server.

For the e2e suite: `WithDriver` (a second driver session on the same daemon), `BuildLoom`, `Cmd` and `Text` (the pane with wrapped lines joined).

## What it does not isolate

`CLAUDE_CONFIG_DIR`, `PATH` and the clipboard stay the host's. An account flow needs a throwaway `CLAUDE_CONFIG_DIR` and fakeagent first on `PATH` as `claude`; a drag-select in a sandboxed loom writes the host's clipboard.

# Long-running CLI processes carry on under an upgraded daemon

Feedback thread 16 (`loom feedback show 16`). Design agreed with the owner on 2026-11-11.

## Problem

During a flow run, the daemon was replaced by a newer devel build while the `loom` that started the run stayed older. The engine job (it runs `os.Executable()` of whoever started or resumed the run: `internal/cli/flow_run.go`) and `loom flow run wait` were refused at every rejoin with `newerDaemonError` ("the loom daemon is …, newer than this loom": `daemon_client.go`). After `rejoinFor` (10 minutes) the engine exited 125 "lost", taking the story's developer agent with it. A resume from the same old `loom` hits the same refusal.

The feedback says `loom session list` kept working from the old CLI. Every CLI command joins through the same refusal (`cli.Join` → `joinCLI` → `daemonLink.join`), so that observation doesn't match the code; don't design around it.

The session "feedback simple fixes" (branch `aidanb/feedback-simple-fixes`, its item 3) makes `rejoin` (`internal/cli/rejoin.go`) return at once on a newer-daemon refusal, with a message to resume under the new loom. This design builds on that: re-exec when it can, and that fail-fast error when it can't.

## Decision

When a long-running CLI process, waiting in `rejoin`, is refused because the daemon is newer, it replaces itself with the daemon's own executable, with the same arguments (`syscall.Exec`). The process keeps its PID, so a job's shim keeps waiting on it, its log carries on, and its cgroup is unchanged. The engine's job doesn't end, so its agents aren't taken down with it. The new engine replays the run's state from disk (`recall`/`remember`) and follows the agent it had already started (`AgentStarted` → `follow`). A newer build reading an older build's run state is the normal compatibility direction.

### Finding the daemon's executable

`rpc.Hello.Exe` is the executable's SHA-256, not its path. Add an absolute-path field (e.g. `ExePath`, from `os.Executable()`, `omitempty`) to `rpc.Hello`, which the daemon sends. Regenerate `docs/specs/protocol.md` (`go test ./core/rpc -run TestProtocolReference -update`); never edit it by hand. A daemon that sends no path (an older build) gets the fail-fast error.

### Before the exec

Re-exec only when all of these hold; otherwise return the fail-fast error from simple-fixes item 3:

- The path is absolute and names a regular file this user can execute.
- The file's SHA-256 equals the hello's `Exe`. The file at the path is then still the daemon's build, not something installed over it since, which could even be older.
- No loop: an environment marker (e.g. `LOOM_REEXEC=<hello Exe>`) set on the new process. A process that already re-execed into that build and is still refused fails fast. A later re-exec into a different, newer build is allowed.
- The platform supports it: put the exec in a `_unix.go` file; the `_windows.go` side answers unsupported, and the caller fails fast. Run `GOOS=windows CGO_ENABLED=0 go build ./...`.

Say it before the exec, on the process's log or stderr through `rejoin`'s `say`: "the loom daemon was upgraded to <build>; carrying on under it (<path>)".

### Which commands

Re-exec runs the whole command again from its start, not just `rejoin`'s `fn`. Check each `rejoin` caller and allow only those whose whole command can safely start again:

- `loom flow run engine` (`flow_run_fx.go`): yes, it replays.
- `loom flow run wait` (`flow_run.go`), `loom flow agent` wait (`flow_agent.go`), `loom flow wait` (`flow_wait.go`), `loom flow gates` start and wait (`flow_gates.go`): check that each one reads its record before acting, so that starting again doesn't submit a second job or start a second run. Exclude any that would, and say which in the commit.

The TUI is unchanged: it still exits on a newer daemon.

## Tests

Put the exec behind a seam and test the decision:

- A newer-daemon refusal with a valid path and matching hash execs that path with the same arguments and the marker set.
- A hash mismatch, a relative or missing path, a daemon that sends no path, the marker already naming this build, and an unsupported platform each fail fast with simple-fixes' error, and never exec.
- An ordinary lost daemon still rejoins as before.
- A caller that isn't allowed to re-exec fails fast.

If it fits the e2e harness (`go test -tags e2e ./e2e/...`, which needs tmux and a private server), add an end-to-end check that a waiting process survives a daemon replaced by a newer build. Otherwise, say why not.

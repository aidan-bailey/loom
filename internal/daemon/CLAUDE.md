# internal/daemon

The daemon behind `loom serve`: one per global config dir, holding `loom.lock` for its whole life and serving the model on a unix socket, plus `Connect`, the TUI's way in. The root's `serve.go` and `daemon_client.go` are its command and the TUI's join. Full reference in [`README.md`](README.md).

## Rules when modifying this package

### The lock and the record

- **Never remove `loom.lock`, and rewrite its record in place, never by rename.** The flock is on the inode: a contender waiting on the old inode and a new holder on a recreated file would both "hold" it, and two daemons writing one `state.json` drop each other's records, which come back as `Recoverable` orphans without their launch flags, conversation or account. **Convention** — two daemons, silently losing sessions; `ReadRecord` rereads a half-written record (`TestReadRecord_WaitsOutATornRecord`).
- **Stand down at once for a live loom holder; wait only while no live process holds the lock.** A daemon started while another runs or stops must never take over once that one has gone. **Enforced** by `TestServe_StandsDownAtOnceForALiveDaemon` and `TestServe_RefusesASecondDaemon`.

### Socket and stop

- **Compute the socket path only in the daemon (`SocketPath`), and have clients dial what the record says.** A client's environment (`XDG_RUNTIME_DIR`, `TMPDIR`) may differ from the daemon's, so a path it computed itself names a socket nobody listens on. **Convention** — a TUI that spawns a second daemon or times out; `TestSocketPath` covers the choice of place.
- **`Stop` signals only a local pid whose arguments say `serve` (`servesLoom`), and `StopPID` only the daemon its caller dialed.** A stale record's pid may have been reused by another process, and two new TUIs starting together both dial the old daemon, so the second must not stop the daemon the first just started. **Enforced** by `TestStop`, `TestServesLoom` and `TestStopPID_StopsOnlyTheDaemonItNames`.
- **Find the socket's place before the boot, and listen only once booted.** A host with no place for a socket must fail having swept and relaunched nothing, and a socket that answers must mean a daemon that is ready. **Enforced** by `TestServe_FindsAPlaceToListenBeforeItBoots`.
- **Exit without saving when the model fails (`Server.Fatal()`); on a stop, quiesce first, then save (`stopModel`).** After a panic the model's state is unknown, and a save would write it over good records; a stop that saves mid-pause writes a half-done lifecycle. **Enforced** by `TestStopModel_AFailedModelIsNotSaved` and `TestServe_AStopWaitsForJobsInFlight`.

### Guards

- **A guard judges the resource it is about to use.** The nesting guard in the root's `serve.go` and in `reset` checks the server `TmuxServer` resolves (the last daemon's while it runs), not the caller's `$TMUX` or `LOOM_TMUX_SOCKET`: a dev loom in a loom pane with a private socket set once started the user's daemon on the user's server. **Enforced** by `TestServeCmd_GuardsTheServerItPins`, `TestResetCmd_SweepsTheServerADaemonWouldPin` and `TestNesting_DecidedOnTheServerTheDaemonPins`.
- **Let neither `LOOM_TMUX_SOCKET` nor `LOOM_ALLOW_NESTED` bypass `replaceGuard`.** A loom built in a worktree and run in an agent's pane would otherwise stop the user's daemon, and every TUI on it quits; only a sandbox's own global dir (`LOOM_GLOBAL_DIR`) lets it replace its own daemon. **Enforced** by `TestReplaceGuard`.
- **Every command that writes `state.json` itself refuses while any process holds the lock (`refuseWhileServed`).** The daemon would overwrite what it wrote, or it would overwrite the daemon's records. `reset` calls it and `workspace migrate` reaches it through `cmd.StateWriteGuard`; a new writer needs it too. **Enforced** for those two by `TestStateWriters_RefuseWhileTheDaemonRuns`.

### Spawning and signals

- **Spawn the daemon detached, with the global dir as its working directory and stdio on `/dev/null`, and log every refusal to `serve.log`.** It keeps its working directory for its whole life, and with no stderr `Connect` can only quote the log to the TUI that spawned it. **Enforced** by `TestConnect_ADaemonThatExitsAtOnceSaysWhy` and `TestConnect_QuotesTheCrashLog`; the working directory by the e2e suite.
- **Treat the daemon's environment as frozen at spawn.** It decides the tmux server, the credential override, `$CLAUDE_CONFIG_DIR`, Claude's temp root, `LOOM_PANE_RENDERER` and every agent's `update-environment` variables; read nothing a TUI sends as if it were the daemon's environment. **Convention** — warnings and agents that follow a terminal the user closed long ago.
- **The first SIGTERM, SIGINT or SIGHUP quiesces and saves; a later SIGTERM or SIGHUP only logs; a Ctrl-C meanwhile forces an exit (`forcesExit`).** Two looms replacing the daemon together each send a SIGTERM, and the second must not cost it its save. **Convention** — a daemon killed mid-save loses the last changes.

### Platform, builds and testing

- **Keep Windows differences behind `proc_unix.go`/`proc_windows.go`, and cross-build with `GOOS=windows CGO_ENABLED=0 go build ./...` after changing them.** CI builds no Windows binary, but the release does. **Convention** — a release that fails to build.
- **The newer build wins at join: a newer daemon refuses an older TUI, and a newer TUI replaces an older daemon once.** `rpc.CompareBuilds` decides, and a full tie is the same build, so two installs of one release never replace each other's daemon on every launch. **Enforced** by `TestJoin_TheNewerSideWins` and `TestCompareBuilds`.
- **Run `go test -tags e2e ./e2e/...` after changing the daemon's lifecycle.** Spawn on demand, stale sockets, replacement under an open TUI and `loom serve stop` with live sessions run only there, with real builds. **Convention** — the in-process tests share one process, so they can't see a lost working directory or a daemon outliving its sandbox.

## Pointers

- [`../../core/rpc/CLAUDE.md`](../../core/rpc/CLAUDE.md) — the wire the daemon serves.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.

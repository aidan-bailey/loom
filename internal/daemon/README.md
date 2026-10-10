# internal/daemon

The daemon (`internal/daemon/`): `loom serve`, the one process per global config dir that owns its sessions whether or not a TUI is open, and how a TUI finds it. The root package holds its command and the TUI's join: [`../../serve.go`](../../serve.go) and [`../../daemon_client.go`](../../daemon_client.go). Everything logs under `subsystem=serve`.

## Why one daemon

Two processes writing one workspace's sessions overwrite each other: `Storage.SaveInstances` rewrites a workspace's whole list from the writer's own memory, so each one's saves drop the records only the other knows, and the dropped ones come back as `Recoverable` orphans that have lost their launch flags, conversation ID and (when dead) account. So one process owns the model and writes `state.json`, holding the lock for its whole life, and any number of TUIs connect to it at once over its socket. Each TUI attaches its own pane clients to the sessions it shows, and tmux sizes a window to its most recently active client. The open list, the last-used workspace and the UI prefs are shared registry and state values: the last TUI to write one wins.

## Files

| File | Holds |
|---|---|
| `lock.go` | the lock, `Record`, `ReadRecord`, `acquire`, `liveHolder` |
| `paths.go` | `SocketPath`, `privateDir`, `maxSocketPath` |
| `serve.go` | `Serve`, `Options`, `stopModel`, `Stop`, `StopPID`, `servesLoom`, `TmuxServer` |
| `connect.go` | `Connect`, `spawn`, `LogPath`, `CrashLogPath` |
| `proc_unix.go`, `proc_windows.go` | `recordPath`, `terminate`, process probes per platform |

## The lock

One daemon per global dir holds a gofrs flock on `<globalDir>/loom.lock` for its whole life; the OS releases it on exit, crash included. It lives in the global dir, not beside the socket, so two processes whose environments would put the socket in different places still exclude each other; a loom from before the daemon took its takeover lock on the same file, so the two exclude each other too.

The holder's `Record` (`pid`, `tty`, `started`, `socket` once it listens, `build`, `host`, `tmux`) is written into the lock file itself on Unix, where flock is advisory, and into `loom.lock.json` beside it on Windows, whose lock (`LockFileEx`) is mandatory (`recordPath`). It is rewritten in place, never renamed, since the lock is on the inode, so `ReadRecord` rereads a record it meets half-written (`TestReadRecord_WaitsOutATornRecord`). A record reads three ways:
- `IsDaemon`: a socket is set; dial it;
- booting: a build but no socket yet;
- `IsPreDaemon`: a pid but neither: a TUI from before the daemon, refused only while its pid lives.

`acquire`, the daemon's take, stands down at once (`ErrRunning`, which `loom serve` prints before exiting 0) when a live loom process holds the lock (`liveHolder`: alive, and pre-daemon or `servesLoom`, whose `/proc/<pid>/cmdline` says `serve`), and waits (`Options.LockWait`) only while no live process does (a client's `ReadRecord` probing it, a daemon that took it and has not yet written its record). So a daemon started while another runs or stops never takes over once that one has gone (`TestServe_StandsDownAtOnceForALiveDaemon`).

## The socket

`SocketPath` (`paths.go`; `daemon.SocketPath` to callers): `$XDG_RUNTIME_DIR/loom/<hash>.sock`, else `<globalDir>/run/serve.sock` when that fits a socket address (`maxSocketPath`), else `loom-<uid>/<hash>.sock` in `os.TempDir()`, each in a 0700 directory this user owns that is not a symlink (`privateDir`). The socket itself is 0600, and `<hash>` is 16 hex digits of the SHA-256 of the resolved global dir. Only the daemon computes it: it records it, and clients dial what the record says, since their environment may differ from the daemon's.

## Serve

`Serve` (`serve.go`):
1. takes the lock;
2. finds the socket's place before the boot, so a host with none fails having swept and relaunched nothing;
3. builds the model (`Options.NewModel`: the root's loads the registry, serving the global workspace alone with a kept notice when it won't load, and the global config's program) and boots it;
4. keeps the boot's notices for the first client (`Server.Keep`);
5. starts the loop and `Begin`s it;
6. listens, so a socket that answers is a daemon that is ready, and writes the socket into the record (a record it can't write stops it, since no client could find or stop it).

Every `Options.WatchInterval` it checks that the socket file is still its own (`os.SameFile`) and, when it is gone or replaced (a runtime dir removed at logout, a tmp cleaner), listens again wherever `SocketPath` finds room now and rewrites the record; a failed `Accept` (EMFILE) is waited out.

`Serve`'s own failures (a pre-daemon holder, no place for the socket, a failed listen or record write) return to its caller and reach stderr, which a spawned daemon has none of: only the root's tmux-server and nesting refusals are logged, so `Connect` can quote nothing for the others.

When `Options.Stop` closes it says bye to every client (`Server.Bye`: from then on a request is refused as unavailable and a cast dropped, and it returns once the calls already let in have their replies queued), closes the listener, waits for foreground jobs while their replies still reach the connections left open, and saves (`stopModel`: `Loop.Quiesce`, bounded by `Options.QuiesceTimeout`, then `SaveForQuit`). Then it closes the server (`Server.Close`: a last publish, each connection flushed, then closed), stops the loop and removes the socket. When the model fails (`Server.Fatal()`) it exits without a bye and without saving (`serve.model_gone`), since the model's state is unknown; the TUIs that lost it (no bye: a crash) and the next `loom` each spawn a fresh daemon, and the second finds the first's lock.

## The root's serve.go

The root's `serve.go` finds the tmux server (`TmuxServer`), runs the nesting guard on it (`nestingCheck(server)`) and logs its refusal to `serve.log` (a spawned daemon has no stderr; `Connect` quotes the log), pins the server (`tmux.UseServer`), sends runtime crashes to `serve-crash.log` (`debug.SetCrashOutput`), and turns signals into `Options.Stop`: the first SIGTERM, SIGINT or SIGHUP stops gracefully, a Ctrl-C meanwhile exits at once (`forcesExit`), and another SIGTERM or SIGHUP is only logged, since two looms replacing the daemon together each send one and the second must not cost it its save. `loom serve stop` (`serveStopCmd`) waits up to `serveStopTimeout`.

**The nesting guard.** The daemon is the process that sweeps orphaned tmux sessions, so the guard (`nestingCheck`, over `tmux.CheckNestingFromEnv`) runs in `loom serve` and in `reset`, each decided on the server it is about to pin (`daemon.TmuxServer`: the last daemon's while that server runs, else this environment's). It refuses inside a `loom_*`/`claudesquad_*` session on that server (`$TMUX`'s first field, compared with `tmux.SameServer`) unless `LOOM_ALLOW_NESTED=1`; a session it can't name there counts as loom's. `LOOM_TMUX_SOCKET` gets no one past it, since the pin ignores it while the last daemon's server runs. A sandbox from `tools/loomdev` passes because its global dir, and with it its server, is its own; run dev builds through `loomdev`, never directly in a pane. The guard recognizes only loom-managed enclosing sessions: a dev loom started from a plain shell pane on a server that also hosts loom sessions is not refused. The TUI runs no nesting guard: in a loom pane it is a client of its global dir's daemon, and replacing that daemon is `replaceGuard`'s to refuse. A `loom serve stop` in a loom pane stops the host's daemon, and every TUI on it quits.

## Stop

`Stop` (`loom serve stop`) signals the pid the record names rather than asking over the socket, so it stops a daemon of any protocol. It refuses another host's record (`Host`), signals only a pid whose arguments say `serve` (`servesLoom`), sends SIGTERM (`terminate`) and waits until that process has gone, whoever holds the lock by then (`TestStop_ReturnsOnceItsProcessHasGone`).

`StopPID`, which a newer TUI replacing a daemon calls (`daemon_client.go`), goes through the same signalling but stops only the daemon it dialed and compared (the record's pid when it dialed): when the lock is free by then, another process holds it, or the pid has died (its record outlives it while a probe or the next daemon holds the lock), it signals nothing and answers `ErrNotRunning`. Two new TUIs starting together both dial the old daemon, and the second must not stop the daemon the first just started (`TestStopPID_StopsOnlyTheDaemonItNames`). Windows has no graceful stop, and `Stop` says so.

## Connect

`Connect` (`connect.go`), the TUI's side:
- makes the global dir (a first launch with a new `LOOM_GLOBAL_DIR`, or `LOOM_HOME` and no `~/.loom`, has none, and the spawn starts the daemon in it);
- reads the record and dials, or spawns `os.Executable() serve` detached (`spawn`: `Setsid` on Unix, `DETACHED_PROCESS` on Windows; stdio to `/dev/null`; the global dir as its working directory, never the client's, which it would keep for its whole life; the client's environment), backing off until a daemon answers;
- waits past its timeout (`connectTimeout`, in the root) up to `bootTimeout` for a live daemon still booting (a boot relaunches every dead agent before it listens);
- starts another (up to `maxSpawns`) when the one it started stood down for a daemon that has gone since;
- quotes what a daemon that exited early added to `serve.log` and `serve-crash.log` (`LogPath`, `CrashLogPath`).

The spawner takes no lock: two clients starting a daemon each is harmless, since the second daemon stands down and both clients dial the first.

`ConnectNoSpawn` is the same walk that never starts a daemon, for a TUI that heard a bye and waits for one: with no process holding the lock it returns `ErrNoDaemon` at once, and it waits for a daemon still booting only as long as its timeout (the TUI polls again, once a second; `waitDialTimeout` bounds each dial at 2s). A daemon that `Connect` started and that exited before serving is `ErrDidNotStart`, wrapped with what it logged, which the TUI counts toward giving up (`TestConnectNoSpawn_FindsNoDaemon`, `TestConnectNoSpawn_DialsARunningDaemon`, `TestConnect_ADaemonThatDidNotStartIsErrDidNotStart`).

## The tmux server

`TmuxServer` is the last daemon's server (`Record.Tmux`) while it still runs (`tmux.ServerRunning`), else `tmux.ResolveServer()` (`LOOM_TMUX_SOCKET`'s socket in `$TMUX_TMPDIR/tmux-<uid>`, `/tmp` by default, else `$TMUX`'s server, else that dir's `default`). So a daemon started from another environment (an ssh login, inside another tmux server, a newer loom replacing an older daemon) finds the agents where they run, rather than find every session dead and relaunch each on its own server, two to a worktree (`TestTmuxServer_KeepsTheLastDaemonsServerWhileItRuns`). The daemon pins it (`tmux.UseServer`: `-S <path>` on every `tmux.Command`) before the boot and names it in its hello and record.

## Joining (root `daemon_client.go`)

`joinDaemon` (a TUI starting) and `rejoinDaemon` (a running TUI that lost its daemon: the `app.Rejoin` `main.go` hands `app.Run`) build a `daemonLink` (`newDaemonLink`, with seams for tests), whose `join(spawn)`:
1. dials (`dialDaemon`: `daemon.Connect` when `spawn`, else `daemon.ConnectNoSpawn`, then `handshake`, `rpc.Dial` under a fresh `connectTimeout` of its own, so a daemon that accepts and never answers can't hang loom; at startup, after a short silence, it prints "loom: waiting for the loom daemon (it loads every workspace as it starts)…");
2. compares builds (`rpc.CompareBuilds`).

Startup always spawns. A rejoin spawns only after a crash or on `ctrl+r`, and is quiet: it prints nothing, because the TUI holds the screen, and `say` hears a replacement for the banner instead of stderr. The dial after a replacement's stop always spawns, since this build must start the replacement.

The same build is used, with its tmux server pinned (`tmux.UseServer(peer.Tmux)`). A newer daemon is refused (`newerDaemonError`: "the loom daemon is X, newer than this loom (Y): upgrade loom, or run `loom serve stop`"). It matches `app.ErrDaemonNewer`, so a rejoining TUI exits instead of retrying, and `main.go` says "loom: the loom daemon was replaced by a newer loom (X); run loom again". An older one is replaced once, announced on stderr at startup and on the banner on a rejoin ("replacing the loom daemon (…) with this build…"): `daemon.StopPID` of the daemon dialed (`dialDaemon` returns the pid from `Connect`'s record), then a connect that spawns this build. Unless `replaceGuard` refuses: from a `loom_*` or `claudesquad_*` tmux session on the daemon's own tmux server (compared cleaned, then through symlinks, `tmux.SameServer`), or on any server when the daemon named none, unless `LOOM_GLOBAL_DIR` is set; a session it can't name there counts as loom's. A loomdev sandbox still replaces its own daemon, whose server is the sandbox's.

`refuseWhileServed` refuses `reset` and, through `cmd.StateWriteGuard` (which `main.go` sets, since `cmd` cannot import the daemon's packages), `workspace migrate` while any process holds the lock: both write `state.json` themselves.

## Lifetime and environment

The first `loom` that finds no daemon spawns one, detached, and it outlives every TUI until SIGTERM, SIGINT, SIGHUP or `loom serve stop`. Sessions are tmux's and keep running through any stop; the next daemon reattaches them at its boot. Saves are the daemon's: on requests, after the changes it makes on its own, and at its stop; a TUI's quit saves no session. When the daemon goes, no TUI exits (only one that finds a newer daemon on rejoining does). After a graceful stop (`loom serve stop`, or a newer loom replacing the daemon: it said bye) every open TUI waits for a daemon, polling and starting none, and `ctrl+r` starts one; after a crash (no bye) it redials on a backoff and starts one itself, giving up after three that did not start or did not stay up 30s after it rejoined them. A rejoined TUI keeps its tabs and selection. The link states are in [`../../app/README.md`](../../app/README.md).

The daemon keeps the environment of the `loom` that spawned it, so a TUI started later from another terminal or SSH session changes nothing the model reads from its environment: the tmux server (pinned, and kept from the last daemon while that server runs), the credential override and `$CLAUDE_CONFIG_DIR` its warnings read, Claude's temp root, `LOOM_PANE_RENDERER` for the model's tick, and the `update-environment` variables every agent gets at `new-session` (`SSH_AUTH_SOCK`, `DISPLAY`, …). The rest of an agent's environment (`ANTHROPIC_*`, the default account's `CLAUDE_CONFIG_DIR`, `PATH`) is the tmux server's global environment, the daemon's only when the daemon started that server. What a TUI starts itself keeps the TUI's environment: the terminal pane's shells, a relative or `~` `ctx:new_instance` path, and a login from the Accounts screen. To pick up a new environment, `loom serve stop`, then start loom from it (and, on a tmux server the daemon did not start, change that server's environment too).

The other CLI subcommands take no lock: `loom account` and `loom workspace add` write registries the daemon rereads (`accounts.json` when its stat changes, the workspace registry on `ReloadRegistry`). loomdev sandboxes have their own global dir, so their own daemon.

## Runtime files

- `loom.lock` (global dir): the lock, and on Unix its record (JSON); on Windows the record is `loom.lock.json` beside it.
- the socket: `$XDG_RUNTIME_DIR/loom/<hash>.sock`, else `run/serve.sock` in the global dir when that path fits, else `loom-<uid>/<hash>.sock` in the temp dir; named in the record.
- `logs/serve.log`, `logs/serve-crash.log` (global dir): the daemon's log (`log.Initialize(dir, true)`) and its runtime-crash output. The TUI keeps `logs/loom.log` in `LOOM_HOME`'s dir.

## Tests

The daemon is tested at three levels:
- `internal/daemon`'s tests run `Serve` in their own process, on a goroutine, and swap `Connect`'s `spawn` for one that does the same (`spawnInProcess`, `connect_test.go`), each daemon stopped when its test ends (`serve_test.go`, `connect_test.go`, `lifecycle_test.go`);
- the root's `daemon_client_test.go` drives `daemonLink.join` through its seams (`dial`, `stop`, `mayReplace`, `say`): `TestJoin_TheNewerSideWins`, `TestJoin_PinsTheDaemonsTmuxServer`, `TestJoin_SpawnFlag`, `TestJoin_ANewerDaemonIsNewerDaemonError`, `TestRejoin_IsQuiet`, `TestReplaceGuard`, `TestHandshake_IsBounded`; `main_test.go` covers `loom debug`'s daemon lines, the guard and pin of `serve` and `reset` (`TestServeCmd_GuardsTheServerItPins`, `TestResetCmd_SweepsTheServerADaemonWouldPin`), and `reset`/`workspace migrate` refusing while the lock is held;
- `e2e/daemon_test.go` runs real `loom` builds in a loomdev sandbox (rules in [`../../e2e/CLAUDE.md`](../../e2e/CLAUDE.md)).

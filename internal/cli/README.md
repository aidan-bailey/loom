# internal/cli

The `loom workspace`, `loom account`, `loom session`, `loom web`, `loom sweep` and `loom job` subcommands, and the client half of `loom reset`: clients of the loom daemon. Each joins it as the TUI's startup join does, makes its request, prints the answer and exits. `core` imports `cmd`, so `cmd` cannot import the wire; these commands live here and `main.go` attaches them (`rootCmd.AddCommand(cli.WorkspaceCmd)`, `cli.AccountCmd`, `cli.SessionCmd`, `cli.WebCmd`, `cli.SweepCmd`, `cli.JobCmd`), with `workspace migrate` (`cmd.MigrateCmd`, direct under the lock) added under the workspace command.

## Files

| File | Holds |
|---|---|
| `join.go` | `Join` (the join seam `main.go` sets to `joinCLI`), `withDaemon`, `client` (`events`, `await`, `awaitReply`, `awaitStart`, `awaitSend`, `request`; `ctx`, which ends a wait), `accountWait` |
| `workspace.go` | `WorkspaceCmd` and `add`, `list`, `remove`, `use`, `rename`, `status`; `client.resolveWorkspace`, `client.served` |
| `session.go` | `SessionCmd` (built by `newSessionCmd`) and `list`, `new`, `send`, `kill`, `pause`, `resume`; `findSession`, `client.launchOptions`, `sessionJSON` (the `--json` row) |
| `account.go` | `AccountCmd` and `add`, `login`, `list`, `use`, `sync`, `remove`; the environment guards and credential warnings |
| `web.go` | `WebCmd` (built by `newWebCmd`) and `enable`, `disable`, `pair`, `devices`, `revoke`, `status`; `client.awaitServing`, `servesAlready`, `webSettled`, `sameAddr`, `webDeviceJSON` (the `devices --json` row), `webWait` |
| `job.go` | `JobCmd` (built by `newJobCmd`) and `submit`, `cancel`; `claimFlags` (`submission`: the argv, directory, environment and `$LOOM_LAUNCH` a job is sent with), `client.submitJob`, `client.awaitJob`, `ExitError` (a command's exit code, which `main.go` honours), the helpers the other files share |
| `job_follow.go` | `run`, `wait`, `logs`; `jobFollower` (the follower of `run`, `wait` and `logs -f`), `jobOutcome` (a job's last line and exit code), `interruptible` and `notifySignals` (the interrupt), `tailOffset` |
| `job_list.go` | `list` and `writeJobList` |
| `job_unix.go`, `job_windows.go` | `signalExit` (the name of the signal that killed a job to the exit code 128 + its number, through `unix.SignalNum`, or the n of a `SIG<n>` name; the Windows stub maps none) |
| `sweep.go` | `SweepCmd` (built by `newSweepCmd`, with `--dry-run`, `--after` and `--verbose`) and `writeSweepReport` |
| `reset.go` | `Reset`, the client half of `loom reset` while a daemon runs |

## How a command runs

`withDaemon` calls `Join`, which starts the daemon when none runs (the newer build wins, `replaceGuard` applies), runs the command's function over the client and closes it. The client keeps a replica of the daemon's published state, so a read (`Registry`, `Workspaces`, `Views`, `AccountUsage`, `Web`) is answered locally and only a request crosses the wire. The notices the daemon gives the connection are written to stderr, except one naming the command's own request, which the command gets instead (`events`) and reports with the request's outcome. `main.go`'s `Join` dials as `rpc.RoleCLI`, so the daemon gives a command none of another client's request's notices (a phone's slash command) nor the backlog it keeps for the next TUI (the boot's notices).

The flow commands that wait (the run engine, `flow run wait`, `flow agent wait`, `flow wait wait`, `flow gates wait`) and `flow gates start` reach the daemon through `rejoin` (`rejoin.go`), which joins again with backoff after a loss. A join refused because the daemon is newer ends it at once: with `reexecOnUpgrade` (every caller but `flow gates start`, whose fresh nonce would queue a partly queued chain again) it first replaces the process with the daemon's executable (`reexecInto`, `reexec.go`), the path its hello names (`rpc.Hello.ExePath`), once that file is a regular file this user can execute and still hashes to the hello's `Exe`. The exec (`execProcess`: `syscall.Exec`, `reexec_unix.go`; unsupported on Windows) keeps the arguments and the PID, sets `LOOM_REEXEC` to the build's hash so a process refused again by that build fails fast, and, for a bounded wait, `LOOM_REEXEC_DEADLINE` so the new process stops at the old deadline (`waitDeadline`). The process reads both at startup and removes them from its environment (`takeReexecEnv`), so the jobs and agents it starts inherit neither. Where it can't re-execute, it returns a `daemonUpgradedError`, which each command turns into exit 125 naming what carries on with the new loom.

| Command | Request or read |
|---|---|
| `workspace add` | `Register` |
| `workspace list` | `ReloadRegistry`, then `Registry` |
| `workspace remove` | `Unregister` |
| `workspace use` | `SetLastUsed` |
| `workspace rename` | `RenameWorkspace` |
| `workspace status` | `ReloadRegistry`, then `Workspaces` and `Views` |
| `account add` | `AddAccount`, then `claude auth login` in the command's terminal unless `--no-login` |
| `account login` | `claude auth login`, then a refresh of the daemon's auth |
| `account list` | `RequestUsageProbe` and a wait, then the published accounts and usage |
| `account use` | `SetDefaultAccount` |
| `account sync` | `RequestAccountsRefresh`, then a wait for each account's `AccountSync` report |
| `account remove` | `RemoveAccount` (with `force` for `--force`) |
| `reset` (`Reset`) | `Reset`, then a wait for its `Reply` |
| `session list` | `ReloadRegistry`, then `Views` |
| `session new` | `ReloadRegistry`, `ReloadAccounts`, then `Create` and a wait for its `Started` or its notices (`awaitStart`) |
| `session send` | `ReloadRegistry`, then `Send` and a wait for its `Reply` (queued) and, with `--wait`, its `Answered` or a notice (`awaitSend`) |
| `session kill`, `pause`, `resume` | `ReloadRegistry`, then `Kill`, `Pause` or `Resume` and a wait for its `Reply` (`request`) |
| `web enable` | `ConfigureWeb` once (a repeat of the stored settings retries a failed bind, since every call bumps `Web().Rev`; nothing is sent when they are already served: `servesAlready`), then a wait up to `webWait` for a `Web()` past the `Rev` read before the call to report the address served, or an error (`awaitServing`) |
| `web disable` | `ConfigureWeb` with an empty `listen` and the stored origin |
| `web pair` | `PairDevice`; the code is printed as `XXXX-XXXX` with its expiry and the origin |
| `web devices` | `Web().Devices` (read only; `--json` for a script) |
| `web revoke` | `RevokeDevice`, once per name for `--all` |
| `web status` | `Web()` (read only) |
| `job submit` | `SubmitJob` and a wait for its `Reply` (`submitJob`), then for the replica to hold the job (`awaitJob`); prints where it stands |
| `job run` | `SubmitJob` without a message, then follows the job (`follow`) and exits with its code |
| `job wait` | follows an existing job (`follow`); an interrupt asks for a message (`NotifyJob`) when the job has an owner and has not ended |
| `job logs` | `Queue()` for the log's path, then the file on disk; `-f` follows until the job ends |
| `job list` | `Queue()` (read only) |
| `job cancel` | `CancelJob` with `CancelFromCLI`, then a wait for its `Reply` (`request`) |
| `sweep` | `SweepBuilds` (dry run with `--dry-run`, the threshold `--after` or else `build_sweep_after_hours`), then a wait for its `Reply`, whose `Sweep` report it prints |

A command waits for a round the daemon runs by its data, not by the next event, for up to `accountWait`; a request with a `ReqID` waits for its `Reply`. The daemon probes usage only once an extra account exists, so `account list` with only the default account shows no usage.

## Tests

The commands run against a daemon served in process (`serveDaemon`, `daemon_test.go`): `Join` returns an in-process client over a booted model, and a test asserts the model's state after the request, not the files. `testmain_test.go` isolates the loom dirs and the Claude config dir. `workspace_test.go`, `account_test.go`, `reset_test.go`, `session_test.go`, `web_test.go` and `sweep_test.go` hold the commands' tests, and `loom_context_test.go` checks the `loom session` and `loom job` commands and flags the loom-context prompt teaches (the web tests play the web layer themselves through `*core.Loop`'s `core.WebHost` methods: `webLayer`, `webDevice`); the session tests that start sessions run the daemon on a private real tmux server (`serveDaemonLive`) with a fake `claude` (`fakeAgent`) in a committed repository (`liveWorkspace`); `e2e/clients_test.go` runs them against real builds.

The job tests (`job_test.go`) use the same test daemon, with its jobs started through this test binary as their shim (`serveDaemon` installs `sched.TestRunner()` before the model boots, with `sched.MaybeRunTestShim()` first in `TestMain`), so a job is a real process group that the daemon settles on its 3 s queue gate; they wait for an end through the daemon's view (`waitFinished`) and deliver the follower's interrupt through `notifySignals`. `e2e/job_test.go` restarts a real daemon under a running job.

# internal/cli

The `loom workspace`, `loom account`, `loom session` and `loom web` subcommands, and the client half of `loom reset`: clients of the loom daemon. Each joins it as the TUI's startup join does, makes its request, prints the answer and exits. `core` imports `cmd`, so `cmd` cannot import the wire; these commands live here and `main.go` attaches them (`rootCmd.AddCommand(cli.WorkspaceCmd)`, `cli.AccountCmd`, `cli.SessionCmd`, `cli.WebCmd`), with `workspace migrate` (`cmd.MigrateCmd`, direct under the lock) added under the workspace command.

## Files

| File | Holds |
|---|---|
| `join.go` | `Join` (the join seam `main.go` sets to `joinCLI`), `withDaemon`, `client` (`events`, `await`, `awaitReply`, `awaitStart`, `request`; `ctx`, which ends a wait), `accountWait` |
| `workspace.go` | `WorkspaceCmd` and `add`, `list`, `remove`, `use`, `rename`, `status`; `client.resolveWorkspace`, `client.served` |
| `session.go` | `SessionCmd` (built by `newSessionCmd`) and `list`, `new`, `kill`, `pause`, `resume`; `findSession`, `client.launchOptions`, `sessionJSON` (the `--json` row) |
| `account.go` | `AccountCmd` and `add`, `login`, `list`, `use`, `sync`, `remove`; the environment guards and credential warnings |
| `web.go` | `WebCmd` (built by `newWebCmd`) and `enable`, `disable`, `pair`, `devices`, `revoke`, `status`; `client.awaitServing`, `servesAlready`, `webSettled`, `sameAddr`, `webDeviceJSON` (the `devices --json` row), `webWait` |
| `reset.go` | `Reset`, the client half of `loom reset` while a daemon runs |

## How a command runs

`withDaemon` calls `Join`, which starts the daemon when none runs (the newer build wins, `replaceGuard` applies), runs the command's function over the client and closes it. The client keeps a replica of the daemon's published state, so a read (`Registry`, `Workspaces`, `Views`, `AccountUsage`, `Web`) is answered locally and only a request crosses the wire. The notices the daemon gives the connection are written to stderr, except one naming the command's own request, which the command gets instead (`events`) and reports with the request's outcome.

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
| `session kill`, `pause`, `resume` | `ReloadRegistry`, then `Kill`, `Pause` or `Resume` and a wait for its `Reply` (`request`) |
| `web enable` | `ConfigureWeb` once (a repeat of the stored settings retries a failed bind, since every call bumps `Web().Rev`; nothing is sent when they are already served: `servesAlready`), then a wait up to `webWait` for a `Web()` past the `Rev` read before the call to report the address served, or an error (`awaitServing`) |
| `web disable` | `ConfigureWeb` with an empty `listen` and the stored origin |
| `web pair` | `PairDevice`; the code is printed as `XXXX-XXXX` with its expiry and the origin |
| `web devices` | `Web().Devices` (read only; `--json` for a script) |
| `web revoke` | `RevokeDevice`, once per name for `--all` |
| `web status` | `Web()` (read only) |

A command waits for a round the daemon runs by its data, not by the next event, for up to `accountWait`; a request with a `ReqID` waits for its `Reply`. The daemon probes usage only once an extra account exists, so `account list` with only the default account shows no usage.

## Tests

The commands run against a daemon served in process (`serveDaemon`, `daemon_test.go`): `Join` returns an in-process client over a booted model, and a test asserts the model's state after the request, not the files. `testmain_test.go` isolates the loom dirs and the Claude config dir. `workspace_test.go`, `account_test.go`, `reset_test.go`, `session_test.go` and `web_test.go` hold the commands' tests (the web tests play the web layer themselves through `*core.Loop`'s `core.WebHost` methods: `webLayer`, `webDevice`); the session tests that start sessions run the daemon on a private real tmux server (`serveDaemonLive`) with a fake `claude` (`fakeAgent`) in a committed repository (`liveWorkspace`); `e2e/clients_test.go` runs them against real builds.

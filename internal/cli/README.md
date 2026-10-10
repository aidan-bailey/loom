# internal/cli

The `loom workspace` and `loom account` subcommands, and the client half of `loom reset`: clients of the loom daemon. Each joins it as the TUI's startup join does, makes its request, prints the answer and exits. `core` imports `cmd`, so `cmd` cannot import the wire; these commands live here and `main.go` attaches them (`rootCmd.AddCommand(cli.WorkspaceCmd)`, `cli.AccountCmd`), with `workspace migrate` (`cmd.MigrateCmd`, direct under the lock) added under the workspace command.

## Files

| File | Holds |
|---|---|
| `join.go` | `Join` (the join seam `main.go` sets to `joinCLI`), `withDaemon`, `client` (`events`, `await`, `awaitReply`), `accountWait` |
| `workspace.go` | `WorkspaceCmd` and `add`, `list`, `remove`, `use`, `rename`, `status`; `client.served` |
| `account.go` | `AccountCmd` and `add`, `login`, `list`, `use`, `sync`, `remove`; the environment guards and credential warnings |
| `reset.go` | `Reset`, the client half of `loom reset` while a daemon runs |

## How a command runs

`withDaemon` calls `Join`, which starts the daemon when none runs (the newer build wins, `replaceGuard` applies), runs the command's function over the client and closes it. The client keeps a replica of the daemon's published state, so a read (`Registry`, `Workspaces`, `Views`, `AccountUsage`) is answered locally and only a request crosses the wire. The notices the daemon gives the connection are written to stderr, except one naming the command's own request, which its `Reply` already reports.

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

A command waits for a round the daemon runs by its data, not by the next event, for up to `accountWait`; a request with a `ReqID` waits for its `Reply`. The daemon probes usage only once an extra account exists, so `account list` with only the default account shows no usage.

## Tests

The commands run against a daemon served in process (`serveDaemon`, `daemon_test.go`): `Join` returns an in-process client over a booted model, and a test asserts the model's state after the request, not the files. `testmain_test.go` isolates the loom dirs and the Claude config dir. `workspace_test.go`, `account_test.go` and `reset_test.go` hold the commands' tests; `e2e/clients_test.go` runs them against real builds.

# cmd

`loom workspace migrate` (`MigrateCmd`, `workspace_migrate.go`), which `main.go` attaches under `internal/cli`'s workspace command, and the subprocess seam: `cmd.Executor` re-exports `internal/exec.Executor` for testability, and the production implementation, `internal/exec.Default`, is aliased as `cmd.Exec` and returned by `MakeExecutor`; tests use `MockCmdExec` (`cmd/cmd_test/testutils.go`). The other workspace and account subcommands are clients of the daemon and live in `internal/cli`, since `core` imports this package.

## Rules when modifying this package

- **Never import `session`, `core` or `internal/cli` from non-test code.** `session/tmux/tmux_test.go` imports `cmd`, so `cmd` importing `session` (which imports `session/tmux`) cycles in that test build, and `core` imports `cmd`. **Enforced** by the compiler: `go test ./session/tmux` fails with "import cycle not allowed in test", and `go build` on a `core` import.
- **Changing `session.InstanceData`?** `workspace migrate` decodes records into its own typed mirror (`migrationInstance`) and writes them back through `config.SaveStateTo`, so a field the mirror lacks is silently dropped from every record it moves; update the mirror and the fixture in `cmd/workspace_migrate_shape_test.go`. **Enforced** by `TestMigrationInstance_TypeDriftGuard` and `TestMigrationInstance_MirrorsInstanceData_JSON`.
- **Hold `StateWriteGuard` for the whole of any command that writes `state.json` itself.** `main.go` sets it to `holdForStateWrite`, which takes the global dir's lock (`daemon.TakeLock`): `workspace migrate` refuses while the daemon runs, which would overwrite the migration with its own save, and no daemon starts while it writes. **Enforced** for the refusal by `TestStateWriters_RefuseWhileTheDaemonRuns` (`main_test.go`).

## Pointers

- [`../internal/cli/CLAUDE.md`](../internal/cli/CLAUDE.md) — the workspace and account subcommands, the daemon's clients.
- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where this package sits.

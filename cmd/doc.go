// Package cmd holds `loom workspace migrate` and the subprocess seam the
// rest of Loom shells out through.
//
// [MigrateCmd] (workspace_migrate.go), which main.go attaches under the
// workspace command (internal/cli), moves instances from the global
// state.json into the config dirs of their matching workspaces. It writes
// state.json directly, holding the global dir's lock ([StateWriteGuard]),
// rather than asking the daemon as the other workspace and account
// subcommands do: those live in internal/cli, since core imports this
// package and cannot be imported back. migrate decodes records into its own
// typed mirror of session.InstanceData; workspace_migrate_shape_test.go
// guards that mirror against schema drift.
//
// The subprocess seam is [Executor], re-exported from internal/exec so
// callers that shell out (git operations, tmux calls, the claude command
// lookup) can be unit-tested against a fake executor without spawning real
// subprocesses. The production implementation is internal/exec.Default,
// aliased here as [Exec] and returned by [MakeExecutor]; tests supply a
// recorder or cmd/cmd_test.MockCmdExec that returns scripted output. Build
// git and gh commands with internal/exec.GitCommand/GhCommand and tmux
// commands with session/tmux.Command (tests enforce both), then run them
// through an Executor rather than calling exec.Command directly.
package cmd

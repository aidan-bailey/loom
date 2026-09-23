// Package cmd holds the `loom workspace` Cobra subcommands and the
// subprocess seam the rest of Loom shells out through.
//
// Most of the package is the workspace CLI. [WorkspaceCmd], which main.go
// adds to the root command, groups add, list, remove, use, rename and
// status (workspace.go), which read and write the global workspace
// registry, and migrate (workspace_migrate.go), which moves instances from
// the global state.json into the config dirs of their matching workspaces.
// migrate decodes records into its own typed mirror of
// session.InstanceData; workspace_migrate_shape_test.go guards that mirror
// against schema drift.
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

// Package git encapsulates the per-session git worktree: creation,
// diff stats, push, and cleanup.
//
// Each agent session runs inside its own worktree under
// {configDir}/worktrees/, on a branch named
// "{BranchPrefix}{SessionTitle}". The worktree is torn down on pause
// (branch preserved) and destroyed entirely on kill. Callers interact
// via the [GitWorktree] type; a [CommandRunner] abstraction makes
// the exec layer injectable for tests.
//
// Error handling convention: errors are wrapped with
// fmt.Errorf("...: %w", err) so callers can match the cause.
// [ErrBranchGone] is a sentinel returned when the underlying branch
// has disappeared between operations — callers typically recover by
// rediscovering the branch or dropping the session.
package git

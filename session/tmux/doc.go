// Package tmux manages the tmux sessions that drive each agent.
//
// A [TmuxSession] wraps one tmux session: start, attach, detach,
// capture pane content for prompt detection, and cleanup. Loom's
// session names are prefixed with [TmuxPrefix] ("loom_");
// [LegacyTmuxPrefix] ("claudesquad_") is still recognized so that
// sessions created before the rebrand are renamed on startup by
// [RenameLegacySessions] and swept by the orphan-cleanup path.
//
// Platform-specific code is split across tmux_unix.go and
// tmux_windows.go. The PTY factory is pluggable via [PtyFactory] so
// tests can inject a fake PTY without touching real file descriptors;
// see [NewTmuxSessionWithDeps] for the injectable constructor.
package tmux

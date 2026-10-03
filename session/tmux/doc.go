// Package tmux manages the tmux sessions that drive each agent.
//
// A tmux session is handled in two halves. A [Session] is the session as
// lifecycle sees it: it launches the session without attaching, probes
// its liveness, captures pane content, types into it through tmux
// commands (load-buffer + paste-buffer for text, send-keys for keys) and
// kills it. It owns no PTY. A [TmuxSession] embeds a Session and is an
// attach client: a PTY attached to the session, an output pump feeding an
// embedded terminal emulator, and the status scan that reads its screen.
// session.Instance holds only a Session; the TUI attaches its own clients
// by session name ([NewAttachClient]). The terminal pane's shells are the
// one place a TmuxSession both launches and attaches its session.
//
// Loom's session names are prefixed with [TmuxPrefix] ("loom_");
// [LegacyTmuxPrefix] ("claudesquad_") is still recognized so that
// sessions created before the rebrand are renamed on startup by
// [RenameLegacySessions] and swept by the orphan-cleanup path.
//
// Platform-specific code is split across tmux_unix.go and
// tmux_windows.go. The PTY factory is pluggable via [PtyFactory] so
// tests can inject a fake PTY without touching real file descriptors;
// see [NewSessionWithDeps], [NewAttachClientWithDeps] and
// [NewTmuxSessionWithDeps] for the injectable constructors.
package tmux

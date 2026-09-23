package tmux

// tmux resolves a bare `-t <name>` by exact match first and then, when no
// session has exactly that name, by prefix (and fnmatch pattern). Loom's
// sessions die on their own all the time — an agent exits, a crash, a
// kill-server — and a command aimed at a dead "loom_api" then lands on a
// live "loom_api-v2": the preview PTY attaches to it, keystrokes and
// prompts go to its agent, a kill kills it. Every -t in loom's production
// code is therefore built by one of the two helpers below, which forbid
// everything but an exact match (TestTmuxTargetsAreExact enforces it).
//
// Neither can name a session whose name holds ':' or '.': tmux splits a
// target on them (session:window.pane) and has no escape. ToLoomTmuxName
// maps both out of every name loom creates; ExactlyTargetable tells a
// caller holding a name from elsewhere (a session listing) whether it can
// be targeted at all.

// SessionTarget returns the -t value naming the session called name, and
// nothing else, for session-typed commands: attach-session, has-session,
// kill-session, rename-session, switch-client. A missing session is an
// error ("can't find session"), never a sibling.
func SessionTarget(name string) string { return "=" + name }

// PaneTarget returns the -t value naming the current window and active
// pane of the session called name, and nothing else, for pane- and
// window-typed commands: capture-pane, display-message, send-keys,
// set-option, resize-window, list-panes. SessionTarget does not work
// there: tmux reads a colon-free target as a window or pane and fails
// with "can't find pane: =loom_x". The trailing ':' makes "=name" the
// session part. A missing session fails cleanly.
func PaneTarget(name string) string { return "=" + name + ":" }

// ExactlyTargetable reports whether SessionTarget and PaneTarget can name
// the session called name. A name holding ':' or '.' cannot be targeted
// exactly: "=loom_feat:0" names window 0 of "loom_feat". Every name
// ToLoomTmuxName produces is targetable; a session some older loom (or
// another tool) created may not be, and must then be left alone.
func ExactlyTargetable(name string) bool {
	for _, r := range name {
		if r == ':' || r == '.' {
			return false
		}
	}
	return name != ""
}

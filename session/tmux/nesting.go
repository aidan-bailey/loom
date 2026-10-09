package tmux

import (
	"fmt"
	"os"
	"strings"
)

// EnvAllowNested bypasses CheckNesting when set to "1".
const EnvAllowNested = "LOOM_ALLOW_NESTED"

// NestedError reports that a loom process that sweeps tmux sessions (the
// daemon, reset) was started inside one of loom's own tmux sessions, on
// the very server it would manage.
type NestedError struct {
	// Session is the enclosing loom-managed tmux session.
	Session string
	// Lookup is why the enclosing session, on that server, could not be
	// named (Session is then ""): it may be one of loom's.
	Lookup error
}

// Error implements error.
func (e *NestedError) Error() string {
	if e.Lookup != nil {
		return fmt.Sprintf("loom: refusing to start inside tmux, on the tmux server this loom would manage, in a session it could not name (%v): it may be one of loom's own, whose sweep would kill the host's sessions. Use `go run ./tools/loomdev run`, start loom outside loom, or set %s=1 if this session is not loom's.",
			e.Lookup, EnvAllowNested)
	}
	return fmt.Sprintf("loom: refusing to start inside a loom-managed tmux session (%s), on the tmux server this loom would manage — its orphan sweep would kill the host's sessions. Use `go run ./tools/loomdev run`, or start loom outside loom.",
		e.Session)
}

// CheckNesting returns a *NestedError when the process runs inside a
// loom-managed tmux session (loom_* or claudesquad_*) on server, the tmux
// server (its socket's path) the process is about to pin and manage. The
// enclosing server is the one $TMUX names (its first comma-separated
// field), compared with SameServer. What LOOM_TMUX_SOCKET names decides
// nothing: a daemon keeps the last daemon's server while that server runs,
// whatever its own environment selects (daemon.TmuxServer), so a dev loom
// run in a loom pane with a private socket set would otherwise start the
// user's daemon on the user's server. A loomdev sandbox passes on its own:
// its global dir is its own, and so is its server. A lookup that fails on
// that server (a timeout under load) fails closed, as replaceGuard does:
// the session may be one of loom's.
func CheckNesting(getenv func(string) string, enclosing func() (string, error), server string) error {
	if getenv(EnvAllowNested) == "1" {
		return nil
	}
	tmuxEnv := getenv("TMUX")
	if tmuxEnv == "" {
		return nil
	}
	if enclosingServer, _, _ := strings.Cut(tmuxEnv, ","); !SameServer(enclosingServer, server) {
		return nil
	}
	name, err := enclosing()
	if err != nil {
		return &NestedError{Lookup: err}
	}
	if strings.HasPrefix(name, TmuxPrefix) || strings.HasPrefix(name, LegacyTmuxPrefix) {
		return &NestedError{Session: name}
	}
	return nil
}

// CheckNestingFromEnv is CheckNesting wired to the process environment and
// the real enclosing-session lookup, for the tmux server server.
func CheckNestingFromEnv(server string) error {
	return CheckNesting(os.Getenv, EnclosingSessionName, server)
}

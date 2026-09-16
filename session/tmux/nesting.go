package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvAllowNested bypasses CheckNesting when set to "1".
const EnvAllowNested = "LOOM_ALLOW_NESTED"

// NestedError reports that loom was started inside one of its own tmux
// sessions while targeting that same server — the default server, or a
// private socket (LOOM_TMUX_SOCKET) that names the very server this process
// is already running inside.
type NestedError struct {
	// Session is the enclosing loom-managed tmux session.
	Session string
}

// Error implements error.
func (e *NestedError) Error() string {
	return fmt.Sprintf("loom: refusing to start inside a loom-managed tmux session (%s) — its orphan sweep would kill the host's sessions. Use `go run ./tools/loomdev run` or set %s.",
		e.Session, EnvTmuxSocket)
}

// CheckNesting returns a *NestedError when the process runs inside a
// loom-managed tmux session (loom_* or claudesquad_*) on the server this
// process would itself manage. A tmux server copies the environment of the
// client that started it, so every pane of a server started with
// LOOM_TMUX_SOCKET=X inherits both $TMUX (naming server X) and
// LOOM_TMUX_SOCKET=X — trusting a non-empty LOOM_TMUX_SOCKET on its own
// would let the guard wave through a bare run inside its own sandbox
// server. So the lookup is skipped only when LOOM_TMUX_SOCKET names a
// server *other than* the one $TMUX points at (compared against the socket
// basename — the part of $TMUX before the first comma); when they match,
// the enclosing-session lookup still runs, exactly as it would with no
// private socket configured at all. A failing lookup means "not nested":
// with no reachable enclosing server there is nothing for the sweep to
// harm.
func CheckNesting(getenv func(string) string, enclosing func() (string, error)) error {
	if getenv(EnvAllowNested) == "1" {
		return nil
	}
	tmuxEnv := getenv("TMUX")
	if tmuxEnv == "" {
		return nil
	}
	if socket := getenv(EnvTmuxSocket); socket != "" && socket != tmuxSocketBasename(tmuxEnv) {
		return nil
	}
	name, err := enclosing()
	if err != nil {
		return nil
	}
	if strings.HasPrefix(name, TmuxPrefix) || strings.HasPrefix(name, LegacyTmuxPrefix) {
		return &NestedError{Session: name}
	}
	return nil
}

// tmuxSocketBasename extracts the socket name from a $TMUX value such as
// "/tmp/tmux-1000/default,1234,0", returning "default".
func tmuxSocketBasename(tmuxEnv string) string {
	path, _, _ := strings.Cut(tmuxEnv, ",")
	return filepath.Base(path)
}

// CheckNestingFromEnv is CheckNesting wired to the process environment and
// the real enclosing-session lookup.
func CheckNestingFromEnv() error {
	return CheckNesting(os.Getenv, EnclosingSessionName)
}

package tmux

import (
	"fmt"
	"os"
	"strings"
)

// EnvAllowNested bypasses CheckNesting when set to "1".
const EnvAllowNested = "LOOM_ALLOW_NESTED"

// NestedError reports that loom was started inside one of its own tmux
// sessions while targeting that same (default) server.
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
// loom-managed tmux session (loom_* or claudesquad_*) and no private socket
// is configured. A failing lookup means "not nested": with no reachable
// enclosing server there is nothing for the sweep to harm.
func CheckNesting(getenv func(string) string, enclosing func() (string, error)) error {
	if getenv(EnvAllowNested) == "1" || getenv("TMUX") == "" || getenv(EnvTmuxSocket) != "" {
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

// CheckNestingFromEnv is CheckNesting wired to the process environment and
// the real enclosing-session lookup.
func CheckNestingFromEnv() error {
	return CheckNesting(os.Getenv, EnclosingSessionName)
}

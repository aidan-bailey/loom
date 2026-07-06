package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// RemoteControlAuthState is the tri-state result of checking whether the
// active Claude authentication can establish a --remote-control session.
//
// Detection is deliberately conservative: only a clear positive is OK and
// only a clear negative is Blocked. Anything ambiguous is Unknown so
// callers can fail closed (skip the flag) without interrupting the user.
type RemoteControlAuthState int

const (
	// RemoteControlAuthUnknown means auth could not be determined — the
	// program isn't Claude, the `auth status` subcommand is missing, or its
	// output didn't parse. Callers add no flag and show no prompt.
	RemoteControlAuthUnknown RemoteControlAuthState = iota
	// RemoteControlAuthOK means the session is logged in with a claude.ai
	// account and no API-key override — remote control will work.
	RemoteControlAuthOK
	// RemoteControlAuthBlocked means auth was clearly determined to be
	// incompatible with remote control (API-key/console/token auth, an
	// override env var, or simply not logged in).
	RemoteControlAuthBlocked
)

// RemoteControlAuth carries the detection state plus a human-readable
// reason shown to the user when Blocked.
type RemoteControlAuth struct {
	State  RemoteControlAuthState
	Reason string
}

// OK reports whether remote control is confirmed usable.
func (a RemoteControlAuth) OK() bool { return a.State == RemoteControlAuthOK }

// Blocked reports whether auth was clearly determined to be incompatible
// with remote control.
func (a RemoteControlAuth) Blocked() bool { return a.State == RemoteControlAuthBlocked }

// claudeAuthStatus is the subset of `claude auth status` JSON we rely on.
// Field names match the CLI's observed output.
type claudeAuthStatus struct {
	LoggedIn   bool   `json:"loggedIn"`
	AuthMethod string `json:"authMethod"`
}

// remoteControlAuthTimeout bounds the `claude auth status` subprocess so a
// hung CLI can't stall startup.
const remoteControlAuthTimeout = 5 * time.Second

// remoteControlOverrideEnv lists environment variables that force API-key /
// bearer-token auth, overriding an interactive login. Their presence means
// remote control cannot connect — this is the "auth token instead of login"
// case.
var remoteControlOverrideEnv = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

// IsClaudeProgram reports whether program launches the Claude Code agent
// (matching by binary basename, so absolute paths and trailing flags still
// resolve). Used to scope remote-control behavior to Claude sessions.
func IsClaudeProgram(program string) bool {
	return defaultRegistry.Lookup(program).Name() == "claude"
}

// DetectClaudeRemoteControlAuth determines whether the given program's
// Claude authentication can establish a --remote-control session. It is a
// no-op (Unknown) for non-Claude programs. Remote control requires a
// claude.ai OAuth login; API keys, Console accounts, and inference-scoped
// tokens are rejected by Claude, so this reports Blocked for them.
func DetectClaudeRemoteControlAuth(program string, runner internalexec.Executor) RemoteControlAuth {
	if !IsClaudeProgram(program) {
		return RemoteControlAuth{State: RemoteControlAuthUnknown}
	}

	for _, env := range remoteControlOverrideEnv {
		if strings.TrimSpace(os.Getenv(env)) != "" {
			return RemoteControlAuth{
				State:  RemoteControlAuthBlocked,
				Reason: env + " is set — remote control needs a claude.ai login. Unset it, or run `claude auth login`.",
			}
		}
	}

	fields := strings.Fields(program)
	if len(fields) == 0 {
		return RemoteControlAuth{State: RemoteControlAuthUnknown}
	}

	ctx, cancel := context.WithTimeout(context.Background(), remoteControlAuthTimeout)
	defer cancel()
	out, err := runner.Output(exec.CommandContext(ctx, fields[0], "auth", "status"))
	if err != nil && len(out) == 0 {
		// Subcommand missing, or errored with nothing on stdout — can't tell.
		// (An unauthenticated `auth status` still prints its JSON to stdout.)
		return RemoteControlAuth{State: RemoteControlAuthUnknown}
	}

	var status claudeAuthStatus
	if jsonErr := json.Unmarshal(out, &status); jsonErr != nil {
		return RemoteControlAuth{State: RemoteControlAuthUnknown}
	}

	if status.LoggedIn && status.AuthMethod == "claude.ai" {
		return RemoteControlAuth{State: RemoteControlAuthOK}
	}

	reason := "not logged in to Claude — run `claude auth login`."
	if status.LoggedIn {
		reason = "you're authenticated with a non-claude.ai account; remote control needs a claude.ai login. Run `claude auth login`."
	}
	return RemoteControlAuth{State: RemoteControlAuthBlocked, Reason: reason}
}

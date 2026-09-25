package session

import (
	"os"
	"strings"

	"github.com/aidan-bailey/loom/account"
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
	// Identity is what `claude auth status` reported; zero when it did
	// not run or did not parse.
	Identity account.Identity
}

// OK reports whether remote control is confirmed usable.
func (a RemoteControlAuth) OK() bool { return a.State == RemoteControlAuthOK }

// Blocked reports whether auth was clearly determined to be incompatible
// with remote control.
func (a RemoteControlAuth) Blocked() bool { return a.State == RemoteControlAuthBlocked }

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

// DetectClaudeRemoteControlAuth is DetectClaudeRemoteControlAuthEnv for
// the default account.
func DetectClaudeRemoteControlAuth(program string, runner internalexec.Executor) RemoteControlAuth {
	return DetectClaudeRemoteControlAuthEnv(program, nil, runner)
}

// DetectClaudeRemoteControlAuthEnv determines whether the Claude account
// selected by env (nil: the default account) can establish a
// --remote-control session, and carries the identity `claude auth status`
// reported. It is a no-op (Unknown) for non-Claude programs. Remote control
// requires a claude.ai OAuth login; API keys, Console accounts, and
// inference-scoped tokens are rejected by Claude, so this reports Blocked
// for them.
func DetectClaudeRemoteControlAuthEnv(program string, env []string, runner internalexec.Executor) RemoteControlAuth {
	if !IsClaudeProgram(program) {
		return RemoteControlAuth{State: RemoteControlAuthUnknown}
	}

	for _, name := range remoteControlOverrideEnv {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return RemoteControlAuth{
				State:  RemoteControlAuthBlocked,
				Reason: name + " is set — remote control needs a claude.ai login. Unset it, or run `claude auth login`.",
			}
		}
	}

	id, err := account.AuthStatus(program, env, runner)
	if err != nil {
		// Subcommand missing, no output, or unparseable — can't tell.
		return RemoteControlAuth{State: RemoteControlAuthUnknown}
	}
	if id.LoggedIn && id.AuthMethod == "claude.ai" {
		return RemoteControlAuth{State: RemoteControlAuthOK, Identity: id}
	}
	reason := "not logged in to Claude — run `claude auth login`."
	if id.LoggedIn {
		reason = "you're authenticated with a non-claude.ai account; remote control needs a claude.ai login. Run `claude auth login`."
	}
	return RemoteControlAuth{State: RemoteControlAuthBlocked, Reason: reason, Identity: id}
}

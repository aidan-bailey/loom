package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// Identity is the subset of `claude auth status` loom reads. Field names
// match the CLI's JSON (Claude Code 2.1.281).
type Identity struct {
	LoggedIn   bool   `json:"loggedIn"`
	AuthMethod string `json:"authMethod"`
	Email      string `json:"email"`
	OrgName    string `json:"orgName"`
	Plan       string `json:"subscriptionType"`
	// ConfigDir is the config dir the CLI resolved. For the default
	// account it is the main dir extra accounts link to.
	ConfigDir string `json:"configDirectory"`
}

// authTimeout bounds `claude auth status`, a local read. A var, not a
// const, so a test can shrink it to exercise the timeout path without
// waiting out the real budget.
var authTimeout = 5 * time.Second

// execWaitDelay bounds how long Output waits, once the process has exited
// or the context's deadline has passed, for a child's stdout/stderr pipes
// to close. Without it, a grandchild that inherited those pipes and
// outlives the parent (observed in practice) can hold Output blocked well
// past the context timeout.
const execWaitDelay = 2 * time.Second

// ErrAccountDirMissing means an env's CLAUDE_CONFIG_DIR override names a
// directory that does not exist. The claude CLI treats a missing config
// dir as a place to create fresh, logged-out state rather than an error,
// so running it there — a removed account's dir, say — would silently
// start a brand new identity instead of reporting the account gone.
// AuthStatus and ProbeUsage both check for it before running anything.
var ErrAccountDirMissing = errors.New("account config dir does not exist")

// accountDir returns the CLAUDE_CONFIG_DIR override in env, if any: the
// last value, matching how os/exec resolves a duplicated key.
func accountDir(env []string) (string, bool) {
	dir, ok := "", false
	for _, kv := range env {
		if v, found := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); found {
			dir, ok = v, true
		}
	}
	return dir, ok
}

// checkAccountDir refuses to proceed when env overrides CLAUDE_CONFIG_DIR
// to a directory that no longer exists. env without an override (the
// default account, which inherits loom's own environment) is never
// checked: that dir is the user's main config, not one loom manages.
func checkAccountDir(env []string) error {
	dir, ok := accountDir(env)
	if !ok {
		return nil
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrAccountDirMissing, dir)
		}
		return fmt.Errorf("stat account dir %s: %w", dir, err)
	}
	return nil
}

// exitErrorWithStderr enriches err with any stderr an *exec.ExitError
// captured — Output() fills ExitError.Stderr (up to 32KB) whenever the
// command's own Stderr field was left nil, exactly the case here — trimmed
// and appended, so a failure reads as more than a bare "exit status 1".
func exitErrorWithStderr(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
			return fmt.Errorf("%w: %s", err, stderr)
		}
	}
	return err
}

// Binary is the executable of a program string: its first field. The
// account commands run the same CLI the sessions launch, so an absolute or
// Nix store path resolves the same way. Binary itself runs whatever
// program string it is given — it is the caller's job to gate that on the
// Claude adapter (session/agent) before reaching here, the same as
// AuthStatus and ProbeUsage below.
func Binary(program string) string {
	fields := strings.Fields(program)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func runner(r internalexec.Executor) internalexec.Executor {
	if r == nil {
		return internalexec.Default{}
	}
	return r
}

// withEnv gives c loom's own environment plus env. os/exec keeps the last
// value of a duplicated key, so env overrides a CLAUDE_CONFIG_DIR loom
// itself inherited. A nil env leaves c.Env nil, which inherits unchanged.
func withEnv(c *exec.Cmd, env []string) *exec.Cmd {
	if env != nil {
		c.Env = append(os.Environ(), env...)
	}
	return c
}

// AuthStatus runs `claude auth status` under env and decodes it. A logged
// out account still prints its JSON (and exits non-zero), so output is
// decoded whenever there is some. program is run as-is: the caller must
// have already gated it on the Claude adapter.
func AuthStatus(program string, env []string, r internalexec.Executor) (Identity, error) {
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()
	c, err := Command(ctx, program, env, "auth", "status")
	if err != nil {
		return Identity{}, err
	}
	c.WaitDelay = execWaitDelay
	out, err := runner(r).Output(c)
	if err != nil {
		if ctx.Err() != nil {
			return Identity{}, fmt.Errorf("claude auth status: timed out after %s", authTimeout)
		}
		if len(out) == 0 {
			return Identity{}, fmt.Errorf("claude auth status: %w", exitErrorWithStderr(err))
		}
	}
	var id Identity
	if jerr := json.Unmarshal(out, &id); jerr != nil {
		return Identity{}, fmt.Errorf("parse claude auth status: %w", jerr)
	}
	return id, nil
}

// LoginCmd is `claude auth login` under env. It is an interactive browser
// flow, so the caller runs it in the foreground terminal: the CLI
// directly, the TUI through tea.ExecProcess. program is run as-is: the
// caller must have already gated it on the Claude adapter.
func LoginCmd(program string, env []string) *exec.Cmd {
	return withEnv(exec.Command(Binary(program), "auth", "login"), env)
}

// MainDir is the main config dir extra accounts link to, given the default
// account's identity: the dir `claude auth status` reported, else
// $CLAUDE_CONFIG_DIR (which the default account inherits), else ~/.claude.
// "" when none can be found.
func MainDir(id Identity) string {
	if id.ConfigDir != "" {
		return id.ConfigDir
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude")
	}
	return ""
}

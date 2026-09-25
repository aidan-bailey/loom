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

// authTimeout bounds `claude auth status`, a local read.
const authTimeout = 5 * time.Second

// Binary is the executable of a program string: its first field. The
// account commands run the same CLI the sessions launch, so an absolute or
// Nix store path resolves the same way.
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
// decoded whenever there is some.
func AuthStatus(program string, env []string, r internalexec.Executor) (Identity, error) {
	bin := Binary(program)
	if bin == "" {
		return Identity{}, errors.New("no claude program configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()
	out, err := runner(r).Output(withEnv(exec.CommandContext(ctx, bin, "auth", "status"), env))
	if err != nil && len(out) == 0 {
		return Identity{}, fmt.Errorf("claude auth status: %w", err)
	}
	var id Identity
	if jerr := json.Unmarshal(out, &id); jerr != nil {
		return Identity{}, fmt.Errorf("parse claude auth status: %w", jerr)
	}
	return id, nil
}

// LoginCmd is `claude auth login` under env. It is an interactive browser
// flow, so the caller runs it in the foreground terminal: the CLI
// directly, the TUI through tea.ExecProcess.
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

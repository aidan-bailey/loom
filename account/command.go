package account

import (
	"context"
	"errors"
	"os/exec"
)

// Command builds the *exec.Cmd for one of the Claude CLI's own subcommands
// (auth status, agents --json, the headless usage probe, …) run under an
// account's environment. It centralizes the checks every such caller needs
// before anything runs: program resolves to a binary, and env's
// CLAUDE_CONFIG_DIR override, if any, still exists on disk
// (ErrAccountDirMissing) — the CLI treats a missing config dir as a place
// to create a fresh, logged-out identity rather than reporting an error,
// so running it there (a removed account's dir, say) would silently start
// a new identity instead of failing closed. program is run as-is: the
// caller must have already gated it on the Claude adapter.
func Command(ctx context.Context, program string, env []string, args ...string) (*exec.Cmd, error) {
	bin := Binary(program)
	if bin == "" {
		return nil, errors.New("no claude program configured")
	}
	if err := checkAccountDir(env); err != nil {
		return nil, err
	}
	return withEnv(exec.CommandContext(ctx, bin, args...), env), nil
}

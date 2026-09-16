package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EnvTmuxSocket names the environment variable that points every loom tmux
// invocation at a private server via `tmux -L <name>`. Unset (or empty)
// keeps tmux's own server selection: $TMUX when running inside tmux, else
// the default socket.
const EnvTmuxSocket = "LOOM_TMUX_SOCKET"

// Socket returns the private tmux socket name from LOOM_TMUX_SOCKET, or ""
// for the default server. It is read on every call so tests can t.Setenv it.
func Socket() string { return os.Getenv(EnvTmuxSocket) }

// Command builds a tmux invocation bound to ctx on the server selected by
// LOOM_TMUX_SOCKET. Every tmux exec in loom goes through Command or
// CommandOnSocket (TestNoRawTmuxExec enforces it): an explicit -L outranks
// $TMUX, which is what keeps a dev build started inside a loom pane from
// sweeping the host's sessions.
func Command(ctx context.Context, args ...string) *exec.Cmd {
	return CommandOnSocket(ctx, Socket(), args...)
}

// CommandOnSocket is Command with an explicit socket name; "" selects the
// default server. The caller's args slice is never modified.
func CommandOnSocket(ctx context.Context, socket string, args ...string) *exec.Cmd {
	full := make([]string, 0, len(args)+2)
	if socket != "" {
		full = append(full, "-L", socket)
	}
	full = append(full, args...)
	return exec.CommandContext(ctx, "tmux", full...)
}

// EnclosingSessionName returns the name of the tmux session this process
// runs inside, asking the server named by $TMUX (pinned to $TMUX_PANE when
// set). It deliberately ignores LOOM_TMUX_SOCKET — the question is about the
// enclosing server, not the one loom would manage — which is why it lives in
// the one file allowed to exec tmux directly.
func EnclosingSessionName() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	args := []string{"display-message", "-p"}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args, "-t", pane)
	}
	args = append(args, "#S")
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

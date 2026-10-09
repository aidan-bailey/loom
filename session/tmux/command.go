package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// EnvTmuxSocket names the environment variable that points every loom tmux
// invocation at a private server via `tmux -L <name>`. Unset (or empty)
// keeps tmux's own server selection: $TMUX when running inside tmux, else
// the default socket.
const EnvTmuxSocket = "LOOM_TMUX_SOCKET"

// Socket returns the private tmux socket name from LOOM_TMUX_SOCKET, or ""
// for the default server. It is read on every call so tests can t.Setenv it.
func Socket() string { return os.Getenv(EnvTmuxSocket) }

// server is the tmux server UseServer pinned (its socket's path), "" when
// none is.
var server atomic.Pointer[string]

// UseServer pins every later Command to the tmux server whose socket is
// path (`tmux -S path`), whatever LOOM_TMUX_SOCKET and $TMUX say; "" lifts
// the pin. The loom daemon pins the server it resolved at start
// (ResolveServer), and a TUI the one the daemon names in its hello: which
// server a process reaches otherwise depends on its environment, and a
// daemon started from another (an ssh login without $TMUX_TMPDIR, say)
// would see every agent's session dead and start it again on its own.
func UseServer(path string) { server.Store(&path) }

// pinned is the server UseServer pinned, "" when none is.
func pinned() string {
	if p := server.Load(); p != nil {
		return *p
	}
	return ""
}

// Command builds a tmux invocation bound to ctx on the server UseServer
// pinned, else the one LOOM_TMUX_SOCKET selects. Every tmux exec in loom
// goes through Command or CommandOnSocket (TestNoRawTmuxExec enforces it):
// an explicit -S or -L outranks $TMUX, which is what keeps a dev build
// started inside a loom pane from sweeping the host's sessions.
func Command(ctx context.Context, args ...string) *exec.Cmd {
	if path := pinned(); path != "" {
		return tmuxCommand(ctx, []string{"-S", path}, args)
	}
	return CommandOnSocket(ctx, Socket(), args...)
}

// CommandOnSocket is Command with an explicit socket name, which outranks
// UseServer's pin; "" selects the default server. The caller's args slice
// is never modified.
func CommandOnSocket(ctx context.Context, socket string, args ...string) *exec.Cmd {
	var flags []string
	if socket != "" {
		flags = []string{"-L", socket}
	}
	return tmuxCommand(ctx, flags, args)
}

// tmuxCommand is `tmux -u <flags> <args>` bound to ctx, in a slice of its
// own.
//
// -u makes every invocation a UTF-8 client. tmux decides that per client,
// from $TMUX (set at all) or a UTF-8 LC_ALL, LC_CTYPE or LANG, and prints a
// non-UTF-8 command-line client's output through utf8_sanitize, which turns
// every byte outside printable ASCII into '_': a tab, an escape, a
// non-ASCII path. Started outside tmux under no UTF-8 locale (a service, a
// bare SSH login), loom read "#{session_name}\t#{session_path}" as one name
// with no directory, and the held-name guard, finding no session of its
// name, killed another workspace's.
func tmuxCommand(ctx context.Context, flags, args []string) *exec.Cmd {
	full := make([]string, 0, 1+len(flags)+len(args))
	full = append(full, "-u")
	full = append(append(full, flags...), args...)
	return exec.CommandContext(ctx, "tmux", full...)
}

// ResolveServer is the socket path of the tmux server this process's
// environment selects, as tmux itself resolves it: LOOM_TMUX_SOCKET's
// socket in tmux's socket dir; else the server $TMUX names (its first
// comma-separated field), inside tmux; else the default socket in tmux's
// socket dir. The socket dir is tmux-<uid> in $TMUX_TMPDIR when that is
// absolute, else in /tmp. The daemon pins the answer (UseServer) and
// publishes it, so its clients reach its server whatever their own
// environment says.
func ResolveServer() (string, error) {
	if name := Socket(); name != "" {
		return filepath.Join(socketDir(), name), nil
	}
	if env := os.Getenv("TMUX"); env != "" {
		path, _, _ := strings.Cut(env, ",")
		if !filepath.IsAbs(path) {
			return "", fmt.Errorf("$TMUX (%q) names no tmux server", env)
		}
		return path, nil
	}
	return filepath.Join(socketDir(), "default"), nil
}

// ServerRunning reports whether a tmux server answers at the socket path:
// one with sessions, since a server exits with its last. A probe that times
// out says yes: a server too loaded to answer is still there.
func ServerRunning(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	err := tmuxCommand(ctx, []string{"-S", path}, []string{"list-sessions"}).Run()
	return err == nil || ctx.Err() != nil
}

// SameServer reports whether two tmux socket paths name one server: equal
// once cleaned, or once their symlinks are resolved (tmux resolves its
// socket dir, so $TMUX can name /private/tmp where a daemon resolved
// /tmp).
func SameServer(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// socketDir is the directory tmux keeps this user's sockets in.
func socketDir() string {
	base := os.Getenv("TMUX_TMPDIR")
	if !filepath.IsAbs(base) {
		base = "/tmp"
	}
	return filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()))
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

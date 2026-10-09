package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/session/tmux"
)

// connectTimeout bounds the wait for the daemon to answer: one started on
// demand boots the model first, which loads every workspace.
const connectTimeout = 20 * time.Second

// daemonLink is how this loom reaches the daemon, with seams for tests:
// dial connects and says hello, returning the client, the daemon's hello
// (with a *rpc.MismatchError, and no client, for a daemon of another
// protocol: the hello still tells its build) and the daemon's pid (its
// lock record's); stop stops the daemon that is process pid, and nothing
// else (daemon.StopPID); mayReplace refuses to replace a daemon on the
// tmux server it names (replaceGuard); and stderr hears a replacement
// announced.
type daemonLink struct {
	own        rpc.Hello
	dial       func() (*rpc.Client, rpc.Hello, int, error)
	stop       func(pid int) error
	mayReplace func(daemonTmux string) error
	stderr     io.Writer
}

// join connects to the daemon, the newer build winning the handshake
// (rpc.CompareBuilds), and pins this process's tmux server to the
// daemon's, before anything here touches tmux: the daemon's sessions are
// on its server, whatever this environment would pick. The same build is
// used as it is. A newer daemon is refused: this loom must be upgraded. An
// older one is stopped and replaced with this build (daemon.Connect starts
// it), once, unless mayReplace refuses: a loom under development, run in
// a loom pane, must never replace the user's daemon. The stop names the
// daemon compared, by its pid: another loom starting meanwhile may have
// replaced it already, and the daemon it started must be left alone.
func (d daemonLink) join() (*rpc.Client, error) {
	for replaced := false; ; replaced = true {
		c, peer, pid, err := d.dial()
		var mismatch *rpc.MismatchError
		if err != nil && !errors.As(err, &mismatch) {
			return nil, err
		}
		order := rpc.CompareBuilds(d.own, peer)
		if order == rpc.SameBuild && c != nil {
			tmux.UseServer(peer.Tmux)
			return c, nil
		}
		if c != nil {
			c.Close()
		}
		switch {
		case order == rpc.SameBuild:
			return nil, err
		case order == rpc.ServerNewer:
			return nil, fmt.Errorf("the loom daemon is %s, newer than this loom (%s): upgrade loom, or run `loom serve stop`",
				describeBuild(peer), describeBuild(d.own))
		case replaced:
			return nil, fmt.Errorf("the loom daemon is %s, older than this loom (%s), even after replacing it", describeBuild(peer), describeBuild(d.own))
		}
		if refusal := d.mayReplace(peer.Tmux); refusal != nil {
			return nil, fmt.Errorf("the loom daemon is %s, older than this loom (%s), and %w: a loom inside loom never replaces the daemon; run the new loom outside loom, or stop the daemon deliberately first (`loom serve stop`)",
				describeBuild(peer), describeBuild(d.own), refusal)
		}
		// The stop can take a while: the old daemon finishes its
		// lifecycle jobs and saves before it exits.
		fmt.Fprintf(d.stderr, "loom: replacing the loom daemon (%s) with this build…\n", describeBuild(peer))
		if err := d.stop(pid); err != nil && !errors.Is(err, daemon.ErrNotRunning) {
			return nil, fmt.Errorf("replace the loom daemon (%s): %w", describeBuild(peer), err)
		}
	}
}

// joinDaemon connects this TUI to globalDir's daemon (daemonLink.join),
// starting one when none runs.
func joinDaemon(globalDir string) (*rpc.Client, error) {
	return daemonLink{
		own:  rpc.Self(),
		dial: func() (*rpc.Client, rpc.Hello, int, error) { return dialDaemon(globalDir) },
		stop: func(pid int) error { return daemon.StopPID(globalDir, pid, serveStopTimeout) },
		mayReplace: func(daemonTmux string) error {
			return replaceGuard(os.Getenv, tmux.EnclosingSessionName, daemonTmux)
		},
		stderr: os.Stderr,
	}.join()
}

// replaceGuard refuses to replace a daemon whose tmux server is daemonTmux
// from inside loom: from a loom-managed tmux session (loom_* or
// claudesquad_*) on that server (compared with tmux.SameServer), or on any
// server when the daemon did not name its own, unless LOOM_GLOBAL_DIR is
// set. A loom built from a worktree and run in an agent's pane would
// otherwise stop the user's daemon, and whatever its environment says
// (LOOM_TMUX_SOCKET included) changes nothing: the daemon's sessions stay
// where they are. A sandbox (loomdev) still replaces its own daemon, whose
// server is the sandbox's own; a daemon that named no server is taken for
// the sandbox's when the global dir is one. A session it can't name, on
// the daemon's server, counts as loom's: it fails closed. getenv and
// enclosing are the process's environment and tmux.EnclosingSessionName,
// for tests.
func replaceGuard(getenv func(string) string, enclosing func() (string, error), daemonTmux string) error {
	tmuxEnv := getenv("TMUX")
	if tmuxEnv == "" {
		return nil
	}
	enclosingServer, _, _ := strings.Cut(tmuxEnv, ",")
	switch {
	case daemonTmux == "" && getenv(config.EnvGlobalDir) != "":
		return nil
	case daemonTmux != "" && !tmux.SameServer(enclosingServer, daemonTmux):
		return nil
	}
	where := "on the daemon's tmux server"
	if daemonTmux == "" {
		where = "and the daemon did not name its tmux server"
	}
	name, err := enclosing()
	switch {
	case err != nil:
		return fmt.Errorf("this loom runs inside tmux %s, in a session it could not name (%v)", where, err)
	case strings.HasPrefix(name, tmux.TmuxPrefix) || strings.HasPrefix(name, tmux.LegacyTmuxPrefix):
		return fmt.Errorf("this loom runs in loom's tmux session %s, %s", name, where)
	}
	return nil
}

// slowConnect is how long loom waits for the daemon in silence.
const slowConnect = 750 * time.Millisecond

// dialDaemon connects to globalDir's daemon (daemon.Connect) and says
// hello within the connect's bound (handshake). It returns the daemon's
// pid too, from the lock record Connect dialed by.
func dialDaemon(globalDir string) (*rpc.Client, rpc.Hello, int, error) {
	// A daemon starting loads every workspace, which can take seconds:
	// say so rather than sit silent. The TUI has not taken the screen yet.
	waiting := time.AfterFunc(slowConnect, func() {
		fmt.Fprintln(os.Stderr, "loom: waiting for the loom daemon (it loads every workspace as it starts)…")
	})
	nc, rec, err := daemon.Connect(globalDir, connectTimeout)
	waiting.Stop()
	if err != nil {
		return nil, rpc.Hello{}, 0, err
	}
	c, err := handshake(nc, connectTimeout)
	var mismatch *rpc.MismatchError
	switch {
	case errors.As(err, &mismatch) && mismatch.Peer != nil:
		return nil, *mismatch.Peer, rec.PID, err
	case errors.As(err, &mismatch):
		// A daemon from before servers said hello first: no build to tell.
		return nil, rpc.Hello{}, rec.PID, err
	case err != nil:
		return nil, rpc.Hello{}, 0, fmt.Errorf("connect to the loom daemon (see %s): %w", daemon.LogPath(globalDir), err)
	}
	return c, c.Peer(), rec.PID, nil
}

// handshake says hello over nc (rpc.Dial) within timeout: a daemon that
// accepts but never answers must not hang loom. The deadline is lifted
// once the client is up, since it would end the connection.
func handshake(nc net.Conn, timeout time.Duration) (*rpc.Client, error) {
	_ = nc.SetDeadline(time.Now().Add(timeout))
	c, err := rpc.Dial(nc)
	if err != nil {
		return nil, err
	}
	_ = nc.SetDeadline(time.Time{})
	return c, nil
}

// describeBuild names a build for a message: its release, then the module
// version and commit Go stamped.
func describeBuild(h rpc.Hello) string {
	switch {
	case h.Version != "" && h.Build != "":
		return h.Version + " (" + h.Build + ")"
	case h.Version != "":
		return h.Version
	case h.Build != "":
		return h.Build
	}
	return "an unknown build"
}

// refuseWhileServed refuses a command that writes a workspace's state.json
// itself (reset, `workspace migrate`) while a process holds the global
// dir's lock: the daemon would overwrite what it wrote, and it would
// overwrite the daemon's.
func refuseWhileServed() error {
	globalDir, err := config.GetGlobalConfigDir()
	if err != nil {
		return err
	}
	rec, held := daemon.ReadRecord(globalDir)
	switch {
	case !held:
		return nil
	case rec.IsPreDaemon():
		return fmt.Errorf("a loom is running (%s): quit it first", rec)
	}
	return errors.New("the loom daemon is running: stop it first (`loom serve stop`)")
}

package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/internal/daemon"
)

// connectTimeout bounds the wait for the daemon to answer: one started on
// demand boots the model first, which loads every workspace.
const connectTimeout = 20 * time.Second

// daemonLink is how this loom reaches the daemon, with seams for tests:
// dial connects and says hello, returning the client and the daemon's
// hello (with a *rpc.MismatchError, and no client, for a daemon of another
// protocol: the hello still tells its build); stop stops the daemon; and
// nested says whether this process runs inside a loom tmux session.
type daemonLink struct {
	own    rpc.Hello
	dial   func() (*rpc.Client, rpc.Hello, error)
	stop   func() error
	nested func() error
}

// join connects to the daemon, the newer build winning the handshake
// (rpc.CompareBuilds). The same build is used as it is. A newer daemon is
// refused: this loom must be upgraded. An older one is stopped and
// replaced with this build (daemon.Connect starts it), once, except from
// inside a loom tmux session, where a loom under development would replace
// the user's daemon: there it refuses.
func (d daemonLink) join() (*rpc.Client, error) {
	for replaced := false; ; replaced = true {
		c, peer, err := d.dial()
		var mismatch *rpc.MismatchError
		if err != nil && !errors.As(err, &mismatch) {
			return nil, err
		}
		order := rpc.CompareBuilds(d.own, peer)
		if order == rpc.SameBuild && c != nil {
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
		if nestErr := d.nested(); nestErr != nil {
			return nil, fmt.Errorf("the loom daemon is %s, older than this loom (%s), and a loom inside a loom session never replaces it: run this loom outside loom, or `loom serve stop` first (%w)",
				describeBuild(peer), describeBuild(d.own), nestErr)
		}
		if err := d.stop(); err != nil && !errors.Is(err, daemon.ErrNotRunning) {
			return nil, fmt.Errorf("replace the loom daemon (%s): %w", describeBuild(peer), err)
		}
	}
}

// joinDaemon connects this TUI to globalDir's daemon (daemonLink.join),
// starting one when none runs.
func joinDaemon(globalDir string) (*rpc.Client, error) {
	return daemonLink{
		own:    rpc.Self(),
		dial:   func() (*rpc.Client, rpc.Hello, error) { return dialDaemon(globalDir) },
		stop:   func() error { return daemon.Stop(globalDir, serveStopTimeout) },
		nested: nestingCheck,
	}.join()
}

// dialDaemon connects to globalDir's daemon (daemon.Connect) and says
// hello. The handshake shares the connect's bound: a daemon that accepts
// but never answers must not hang loom.
func dialDaemon(globalDir string) (*rpc.Client, rpc.Hello, error) {
	nc, _, err := daemon.Connect(globalDir, connectTimeout)
	if err != nil {
		return nil, rpc.Hello{}, err
	}
	_ = nc.SetDeadline(time.Now().Add(connectTimeout))
	c, err := rpc.Dial(nc)
	var mismatch *rpc.MismatchError
	switch {
	case errors.As(err, &mismatch) && mismatch.Peer != nil:
		return nil, *mismatch.Peer, err
	case errors.As(err, &mismatch):
		// A daemon from before servers said hello first: no build to tell.
		return nil, rpc.Hello{}, err
	case err != nil:
		return nil, rpc.Hello{}, fmt.Errorf("connect to the loom daemon (see %s): %w", daemon.LogPath(globalDir), err)
	}
	_ = nc.SetDeadline(time.Time{})
	return c, c.Peer(), nil
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
// dir's lock: the daemon would overwrite what it wrote, and it the
// daemon's.
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

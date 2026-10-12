// Package cli holds the `loom workspace` and `loom account` subcommands,
// which are clients of the loom daemon (daemon stage 3C+4), and the daemon
// half of `loom reset` (Reset).
//
// Each command joins the daemon as the TUI does (Join, which main sets:
// the daemon is started when none runs, the newer build wins, and a loom
// inside loom never replaces it), makes its request, prints the answer and
// exits. The daemon owns the workspace and account registries while it
// runs, so every change goes through it and it serves the result at once:
// a workspace added is served, one removed is unloaded, one renamed keeps
// its ID. What a command reads of its own environment (the shell's
// CLAUDE_CONFIG_DIR, a credential override, the working directory) stays
// its own, and `account login` runs Claude's interactive login in its own
// terminal. `workspace migrate` is the exception: it lives in cmd and
// writes state.json directly, under the lock.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/spf13/cobra"
)

// Join connects to the loom daemon, starting one when none runs, as the
// TUI's startup join does: the newer build wins (an older daemon is
// replaced, a newer one refuses this loom), and the client uses the
// daemon's tmux server from then on. main sets it; tests serve a model in
// process.
var Join func() (*rpc.Client, error)

// accountWait bounds a command's wait for a round the daemon runs for it
// (an accounts refresh, a usage probe): its answer is printed as the
// daemon has it then.
var accountWait = 10 * time.Second

// client is a command's connection to the daemon.
type client struct {
	*rpc.Client
	// errOut hears the notices the daemon gives this connection.
	errOut io.Writer
	// ctx ends a wait (await) early: the command's context, which
	// `session new` cancels on an interrupt.
	ctx context.Context
}

// withDaemon joins the daemon (Join), runs fn with the client and closes
// it. The notices the daemon gives this connection are written to the
// command's stderr: a command is a client like any TUI, so it can be the
// first to connect, and the daemon then hands it the notices kept for the
// first client (its boot's).
func withDaemon(cmd *cobra.Command, fn func(c *client) error) error {
	if Join == nil {
		return errors.New("loom: no way to reach the loom daemon")
	}
	rc, err := Join()
	if err != nil {
		return err
	}
	defer rc.Close()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	c := &client{Client: rc, errOut: cmd.ErrOrStderr(), ctx: ctx}
	err = fn(c)
	c.events()
	return err
}

// events returns the events that arrived since the last call, the state
// events first (the client's Sync), having written each Notice to errOut.
// A Notice naming a request (Req) is one of this command's own, which the
// daemon routes to it alone: it is returned, not written, so the command
// reports it with its request's outcome and never twice.
func (c *client) events() []core.Event {
	var out []core.Event
	for _, ev := range c.Sync() {
		n, ok := ev.(core.Notice)
		switch {
		case !ok:
			out = append(out, ev)
		case n.Req != 0:
			out = append(out, ev)
		case n.Err != nil:
			fmt.Fprintf(c.errOut, "loom: %v\n", n.Err)
		case n.Info != "":
			fmt.Fprintf(c.errOut, "loom: %s\n", n.Info)
		}
	}
	return out
}

// await waits until done holds, for up to timeout (0: for as long as the
// daemon is reachable), or until the command's context ends (its error).
// done is asked once at once and again with each batch of events that
// arrives (events, which writes the notices). It reports whether done
// held, and the client's loss of the daemon.
func (c *client) await(timeout time.Duration, done func([]core.Event) bool) (bool, error) {
	var expired <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		expired = t.C
	}
	for {
		if done(c.events()) {
			return true, nil
		}
		if err := c.Err(); err != nil {
			return false, fmt.Errorf("lost the loom daemon: %w", err)
		}
		if err := c.ctx.Err(); err != nil {
			return false, err
		}
		select {
		case <-c.Wakes():
		case <-expired:
			return false, nil
		case <-c.ctx.Done():
			return false, c.ctx.Err()
		}
	}
}

// awaitReply waits for the Reply to req, the request this command sent.
func (c *client) awaitReply(req core.ReqID) (core.Reply, error) {
	var reply core.Reply
	_, err := c.await(0, func(events []core.Event) bool {
		for _, ev := range events {
			if r, ok := ev.(core.Reply); ok && r.Req == req {
				reply = r
				return true
			}
		}
		return false
	})
	return reply, err
}

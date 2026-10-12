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

// awaitStart waits for the start the Create req asked for. A refused
// Create is its Reply's Err; then either the start's Started (the daemon
// sends it once the initial prompt is sent) or the errors it reports for
// the request end the wait: a failed start notices its error, and its
// save's first (deliverStart), by which time the daemon has removed the
// instance. One loop waits for all three, since a batch can hold more
// than one. Notices naming req that carry no error are written to errOut.
func (c *client) awaitStart(req core.ReqID) (core.Started, error) {
	var (
		started core.Started
		ok      bool
		errs    []error
	)
	_, err := c.await(0, func(events []core.Event) bool {
		for _, ev := range events {
			switch e := ev.(type) {
			case core.Reply:
				if e.Req == req && e.Err != nil {
					errs = append(errs, e.Err)
				}
			case core.Started:
				if e.Req == req {
					started, ok = e, true
				}
			case core.Notice:
				switch {
				case e.Req != req:
				case e.Err != nil:
					errs = append(errs, e.Err)
				case e.Info != "":
					fmt.Fprintf(c.errOut, "loom: %s\n", e.Info)
				}
			}
		}
		return ok || len(errs) > 0
	})
	switch {
	case err != nil:
		return core.Started{}, err
	case len(errs) > 0:
		return core.Started{}, errors.Join(errs...)
	}
	return started, nil
}

// request makes a command's one request (send, given sessionReq) and
// waits for its Reply: its Err fails the command, and a notice riding a
// success (a kill's undroppable stash, a resume's recovery commands) goes
// to errOut.
func (c *client) request(send func(core.ReqID)) error {
	send(sessionReq)
	reply, err := c.awaitReply(sessionReq)
	switch {
	case err != nil:
		return err
	case reply.Err != nil:
		return reply.Err
	}
	if reply.Notice != nil {
		fmt.Fprintf(c.errOut, "loom: %v\n", reply.Notice)
	}
	return nil
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/spf13/cobra"
)

// rejoinFor bounds how long the run engine and `loom flow run wait` try to
// join a daemon they lost, from the outage's first failure (flow in loom
// part 3b, planning amendment 8); rejoinFirst is the first pause, doubled
// to rejoinMax.
var (
	rejoinFor   = 10 * time.Minute
	rejoinFirst = 2 * time.Second
	rejoinMax   = 30 * time.Second
)

// outOfReachError is rejoin's error when it stopped trying: the daemon has
// been out of reach for away, err its last loss or failed join.
type outOfReachError struct {
	away time.Duration
	err  error
}

func (e *outOfReachError) Error() string {
	return fmt.Sprintf("the loom daemon has been out of reach for %v: %v", e.away, e.err)
}

func (e *outOfReachError) Unwrap() error { return e.err }

// daemonUpgradedError is rejoin's error when a join was refused because the
// daemon is a newer build (rpc.ErrDaemonNewer): only a newer loom can join
// it, so trying again for rejoinFor would only delay the end (thread 16).
type daemonUpgradedError struct{ err error }

func (e *daemonUpgradedError) Error() string {
	return fmt.Sprintf("the loom daemon was upgraded, and only the new loom can join it: %v", e.err)
}

func (e *daemonUpgradedError) Unwrap() error { return e.err }

// exit is the command's end on e: 125 (it can't follow), its line led by
// prefix and ending with what carries on with the new loom.
func (e *daemonUpgradedError) exit(prefix, carryOn string) *ExitError {
	return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("%s: %v; carry on with the new loom: %s", prefix, e, carryOn)}
}

// onUpgrade is what rejoin does when a join is refused because the daemon
// is a newer build.
type onUpgrade bool

const (
	// failOnUpgrade fails fast: the command can't safely start again from
	// its beginning (flow gates start, whose fresh nonce would queue a
	// chain partly queued before the refusal again under new keys).
	failOnUpgrade onUpgrade = false
	// reexecOnUpgrade replaces the process with the daemon's build
	// (reexecInto), which runs the whole command again: for a command that
	// reads its record before acting, so starting again repeats nothing.
	reexecOnUpgrade onUpgrade = true
)

// rejoin runs fn on a client (withDaemon), joining again with backoff while
// the daemon can't be reached or is lost mid-call, as a restart to deploy a
// build does, and says each loss through say. It stops trying at deadline
// (zero: none) or once one outage has lasted outage (0: none), with an
// *outOfReachError; an interrupt stops it at once. A join refused because
// the daemon is newer stops it at once: with reexecOnUpgrade it first
// carries on under the daemon's build (reexecInto, carrying deadline), and
// says why when it can't; returning, it is a *daemonUpgradedError. fn must
// be safe to run again: it reads what it acts on before acting, and writes
// its answer only at its end.
func rejoin(c *cobra.Command, deadline time.Time, outage time.Duration, upgrade onUpgrade, say func(string), fn func(d *client) error) error {
	ctx := c.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	wait := rejoinFirst
	var since time.Time // the outage's first failure
	for {
		var lost error
		err := withDaemon(c, func(d *client) error {
			err := fn(d)
			if err != nil {
				lost = d.Err()
			}
			return err
		})
		cause := lost
		var join *joinError
		if cause == nil && errors.As(err, &join) {
			cause = join.err
		}
		if cause == nil {
			return err
		}
		if lost == nil && errors.Is(cause, rpc.ErrDaemonNewer) {
			if upgrade == reexecOnUpgrade {
				carryOn(cause, deadline, say)
			}
			return &daemonUpgradedError{err: cause}
		}
		now := time.Now()
		if since.IsZero() || lost != nil { // a loss after a join begins an outage
			since = now
		}
		left := time.Duration(1<<63 - 1)
		if !deadline.IsZero() {
			left = deadline.Sub(now)
		}
		if outage > 0 {
			if now.Sub(since) >= outage {
				return &outOfReachError{away: outage, err: cause}
			}
			left = min(left, since.Add(outage).Sub(now))
		}
		if left <= 0 {
			return &outOfReachError{away: now.Sub(since).Round(100 * time.Millisecond), err: cause}
		}
		pause := min(wait, left)
		say(fmt.Sprintf("lost the loom daemon (%v); joining again in %v", cause, pause))
		select {
		case <-time.After(pause):
		case <-ctx.Done():
			return ctx.Err()
		}
		wait = min(wait*2, rejoinMax)
	}
}

// carryOn replaces this process with the build of the newer daemon that
// refused it (refusal, a NewerDaemon), carrying deadline (reexecInto). It
// returns only when it didn't, having said why through say.
func carryOn(refusal error, deadline time.Time, say func(string)) {
	var newer NewerDaemon
	if !errors.As(refusal, &newer) {
		say("can't carry on under the upgraded daemon: the refusal names no build")
		return
	}
	peer, name := newer.NewerDaemon()
	if err := reexecInto(peer, name, deadline, say); err != nil {
		say(fmt.Sprintf("can't carry on under the upgraded daemon: %v", err))
	}
}

// sayOnStderr is rejoin's say for a command: a "loom: " line on its stderr.
func sayOnStderr(c *cobra.Command) func(string) {
	return func(line string) { fmt.Fprintf(c.ErrOrStderr(), "loom: %s\n", line) }
}

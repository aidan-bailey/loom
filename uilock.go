package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/internal/takeover"
	"github.com/aidan-bailey/loom/log"
)

// uiLock is the takeover lock the TUI holds for its lifetime. Package
// level so it stays referenced, and its file open, until the process
// exits; the OS then releases it.
var uiLock *takeover.Lock

// takeoverWait bounds how long a takeover waits for the running loom to
// save and quit. A var so tests can shorten it.
var takeoverWait = 15 * time.Second

// errTakeoverDeclined is the user answering no: loom exits without
// starting.
var errTakeoverDeclined = errors.New("takeover declined")

// acquireUILock takes the takeover lock in dir (see internal/takeover)
// before the TUI reads any state. When another loom holds it, it asks on
// in/out whether to take over and, on yes, asks that loom to save and
// quit and waits for the lock, so this one loads what it saved. Without a
// terminal to ask on, it refuses. A lock that can't be taken for any
// other reason warns on errOut and returns no lock: loom still starts.
func acquireUILock(dir string, in io.Reader, out, errOut io.Writer, interactive bool) (*takeover.Lock, error) {
	self := takeover.Self()
	lock, holder, err := takeover.TryAcquire(dir, self)
	if err == nil {
		return lock, nil
	}
	if !errors.Is(err, takeover.ErrHeld) {
		log.For("main").Warn("takeover.lock_failed", "dir", dir, "err", err)
		fmt.Fprintf(errOut, "loom: warning: can't take the single-instance lock (%v); a second loom would overwrite this one's sessions\n", err)
		return nil, nil
	}
	desc := "another loom"
	if holder.PID != 0 {
		desc = "loom (" + holder.String() + ")"
	}
	if !interactive {
		return nil, fmt.Errorf("%s is already running; quit it first (two looms overwrite each other's sessions)", desc)
	}
	fmt.Fprintf(out, "%s is already running, and two looms overwrite each other's sessions.\nTake over? It will save and quit. [y/N] ", desc)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		fmt.Fprintln(out, "Not started.")
		return nil, errTakeoverDeclined
	}
	if err := takeover.Request(dir, self, 5*time.Second); err != nil {
		return nil, fmt.Errorf("can't take over from %s: %w; quit it there%s", desc, err, killHint(holder))
	}
	fmt.Fprintln(out, "Waiting for it to save and quit…")
	lock, err = takeover.Wait(dir, self, takeoverWait)
	if err != nil {
		return nil, fmt.Errorf("%s didn't quit within %s: it may be in an editor or a login, or showing why its save failed; quit it there%s", desc, takeoverWait, killHint(holder))
	}
	log.For("main").Info("takeover.done", "from_pid", holder.PID, "from_tty", holder.TTY)
	return lock, nil
}

func killHint(h takeover.Holder) string {
	if h.PID == 0 {
		return ""
	}
	return fmt.Sprintf(", or end it with `kill %d`", h.PID)
}

// stdinIsTerminal reports whether there is a terminal to ask on.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

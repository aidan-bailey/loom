package cli

import (
	"fmt"
	"os/exec"
	"time"
)

// openerWait is how long openBrowser waits for the opener. Some openers stay
// up while the browser starts, so one still running then counts as opened.
const openerWait = 5 * time.Second

// openBrowser opens link in the machine's browser. It is a variable so tests
// replace it: the real one starts the user's browser.
var openBrowser = startBrowser

// startBrowser starts the platform's opener (openerCommand) on link and waits
// for it as waitOpener says. The opener's stdio is /dev/null.
func startBrowser(link string) error {
	cmd := openerCommand(link)
	if err := cmd.Start(); err != nil {
		return err
	}
	return waitOpener(cmd, openerWait)
}

// waitOpener waits up to limit for the started opener cmd: nil when it exits 0
// within limit, an error naming its exit status when it exits otherwise, and
// nil when it is still running at limit, when it is left running and never
// killed (killing an opener that hands the URL to a running browser can take
// the browser with it).
func waitOpener(cmd *exec.Cmd, limit time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("the opener failed: %w", err)
		}
		return nil
	case <-timer.C:
		return nil
	}
}

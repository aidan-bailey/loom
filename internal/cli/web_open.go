package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/spf13/cobra"
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

// defaultDesktopName is what `open` calls this browser when --name is not
// given.
const defaultDesktopName = "desktop"

func newWebOpenCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "open [--name <device>]",
		Short: "Open the web app in this machine's browser, pairing it first when the name is new",
		Long: "Open the web app in this machine's browser at the listener's local origin, " +
			"http://loom.localhost:<port>, with no tunnel. For a name that is not paired yet " +
			"(desktop, or --name) it makes a pairing code and opens the link that carries it; " +
			"for a paired name it opens the app itself and says so. The URL is always printed. " +
			"The web app must be on and serving (loom web enable), on 127.0.0.1 or ::1. If no " +
			"browser opens (no display, over SSH) the URL is left for you to open, and a code " +
			"it made works for ten minutes.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			out := c.OutOrStdout()
			return withDaemon(c, func(d *client) error {
				local, err := webLocalOrigin(d.Web())
				if err != nil {
					return err
				}
				device := strings.TrimSpace(name)
				link := local + "/?ui=next"
				p, err := d.PairDevice(device)
				switch {
				case err == nil:
					fmt.Fprintf(out, "Pairing code for %s expires at %s.\n", device, p.Expires.Local().Format("15:04:05"))
					link += "#/pair?code=" + p.Code
				case core.IsDeviceExists(err, device):
					fmt.Fprintf(out, "%s is already paired. If this browser is not, pass --name or run loom web revoke %s.\n", device, device)
				default:
					return err
				}
				fmt.Fprintf(out, "Opening %s\n", link)
				if err := openBrowser(link); err != nil {
					fmt.Fprintf(out, "Could not open a browser (%v); open the URL above yourself.\n", err)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", defaultDesktopName, "Name for this browser's device")
	return cmd
}

// webLocalOrigin is the origin `open` builds its link on, from the web state
// the command joined to: the listener's local origin (core.LocalOrigin of the
// address it serves). Nothing is minted before it is known, so the refusals
// say why in the order the user fixes them: the web is off (and, when web.json
// is left alone, why, since `loom web enable` refuses then), it is not serving
// (its error, when the layer has one), or its address has no local origin.
func webLocalOrigin(w core.WebView) (string, error) {
	switch {
	case w.Listen == "" && w.Error != "":
		return "", fmt.Errorf("The web app is off, and its settings file is left alone (%s); see loom web status.", w.Error)
	case w.Listen == "":
		return "", errors.New("The web app is off; turn it on with loom web enable.")
	case !w.Serving && w.Error != "":
		return "", fmt.Errorf("The web app is not serving (%s); see loom web status.", w.Error)
	case !w.Serving:
		return "", errors.New("The web app is not serving yet; see loom web status.")
	}
	local, err := core.LocalOrigin(w.Addr)
	if err != nil {
		return "", fmt.Errorf("The web app has no local origin: %w.", err)
	}
	return local, nil
}

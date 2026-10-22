package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/spf13/cobra"
)

// WebCmd is `loom web`: turn on the daemon's web app, pair and revoke the
// phones that use it, and see its state, as clients of the daemon. main
// attaches it.
var WebCmd = newWebCmd()

// webWait bounds how long `enable` waits for the daemon's web layer to say
// whether it serves: it reports once it has bound the listener, or failed
// to.
var webWait = 5 * time.Second

// newWebCmd builds the `loom web` command tree, with flags of its own:
// each call is independent (tests build one per run).
func newWebCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Serve loom's fleet to a paired phone: enable, disable, pair, devices, revoke, status",
	}
	cmd.AddCommand(
		newWebEnableCmd(),
		newWebDisableCmd(),
		newWebPairCmd(),
		newWebDevicesCmd(),
		newWebRevokeCmd(),
		newWebStatusCmd(),
	)
	return cmd
}

func newWebEnableCmd() *cobra.Command {
	var listen, origin string
	cmd := &cobra.Command{
		Use:   "enable --listen <addr> --origin <url>",
		Short: "Turn the web app on, or change where it listens and its public address",
		Long: "Turn the web app on: the daemon listens on --listen (a loopback address, " +
			"127.0.0.1:<port> or [::1]:<port>, which the tunnel reaches) and answers only " +
			"requests for --origin (the https URL the phone opens). The daemon applies it " +
			"at once; this command waits a few seconds to say whether it serves. " +
			"Run it again to retry a bind that failed (the port was taken); with the " +
			"same values while it serves, it changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			out := c.OutOrStdout()
			return withDaemon(c, func(d *client) error {
				w := d.Web()
				if servesAlready(w, listen, origin) {
					// A ConfigureWeb would make the layer close the listener
					// and rebind, dropping the open streams for nothing.
					fmt.Fprintf(out, "Serving on %s for %s (nothing changed)\nPair a phone with: loom web pair\n", w.Addr, w.Origin)
					return nil
				}
				// Every ConfigureWeb bumps Rev and the layer rebinds when it
				// changes, so this is also the retry of a failed bind; the
				// wait wants an answer past the Rev read here.
				if err := d.ConfigureWeb(listen, origin); err != nil {
					return err
				}
				return d.awaitServing(out, listen, w.Rev)
			})
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "Loopback address to listen on, 127.0.0.1:<port> or [::1]:<port>")
	cmd.Flags().StringVar(&origin, "origin", "", "Public address of the app, https://<host>, or http://127.0.0.1:<port> or http://localhost:<port> for development")
	_ = cmd.MarkFlagRequired("listen")
	_ = cmd.MarkFlagRequired("origin")
	return cmd
}

// servesAlready reports whether enabling listen and origin would change
// nothing: the daemon holds exactly these settings and is serving them. The
// comparison is exact, as the model's is: a differently spelled address is a
// change on its own. A ConfigureWeb of the same values would still bump Rev
// and make the web layer close the listener and bind again, so the caller
// sends none. Settings that failed to bind, or that the layer has not
// answered yet, are not served.
func servesAlready(w core.WebView, listen, origin string) bool {
	return w.Serving && w.Listen == listen && w.Origin == origin
}

// awaitServing waits, up to webWait, for the daemon to say whether it
// serves listen, and reports which. after is the WebView.Rev the caller read
// before its ConfigureWeb: only an answer with a later Rev counts. The
// settings are stored either way.
func (c *client) awaitServing(out io.Writer, listen string, after uint64) error {
	var w core.WebView
	settled, err := c.await(webWait, func([]core.Event) bool {
		w = c.Web()
		return webSettled(w, listen, after)
	})
	switch {
	case err != nil:
		return err
	case !settled:
		return fmt.Errorf("saved, but the daemon did not say within %s whether it serves %s; see loom web status", webWait, listen)
	case w.Serving && sameAddr(w.Addr, listen):
		fmt.Fprintf(out, "Serving on %s for %s\nPair a phone with: loom web pair\n", w.Addr, w.Origin)
		return nil
	default:
		return fmt.Errorf("saved, but not serving: %s; fix it and run loom web enable again", w.Error)
	}
}

// webSettled reports whether w answers a request to listen at listen: its
// Rev is past after (the Rev read before the request, which every
// ConfigureWeb bumps, so a state from before the request never counts), the
// daemon holds that address, and it either serves it or says why it does
// not. A WebView still serving the previous address, or holding the previous
// error, is not an answer yet.
func webSettled(w core.WebView, listen string, after uint64) bool {
	if w.Rev <= after || !sameAddr(w.Listen, listen) {
		return false
	}
	return w.Error != "" || (w.Serving && sameAddr(w.Addr, listen))
}

// sameAddr reports whether a and b are the same host and port, however
// spelled (an IPv6 address has several); text that is not an address
// compares as text.
func sameAddr(a, b string) bool {
	pa, errA := netip.ParseAddrPort(a)
	pb, errB := netip.ParseAddrPort(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return pa == pb
}

func newWebDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Turn the web app off; paired devices and the origin are kept, pending pairing codes are voided",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			out := c.OutOrStdout()
			return withDaemon(c, func(d *client) error {
				w := d.Web()
				if w.Listen == "" {
					fmt.Fprintln(out, "The web app is already off.")
					return nil
				}
				if err := d.ConfigureWeb("", w.Origin); err != nil {
					return err
				}
				fmt.Fprintln(out, "The web app is off. Paired devices and the origin are kept; pending pairing codes are voided. loom web enable turns it on again.")
				return nil
			})
		},
	}
}

func newWebStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the web app's settings, whether it serves, and how many devices are paired",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			return withDaemon(c, func(d *client) error {
				writeWebStatus(c.OutOrStdout(), d.Web())
				return nil
			})
		},
	}
}

// writeWebStatus prints w: whether the web is on, where it listens and for
// which origin, whether it serves or why not, and the device count.
func writeWebStatus(out io.Writer, w core.WebView) {
	row := func(label, value string) { fmt.Fprintf(out, "%-9s %s\n", label+":", value) }
	if w.Listen == "" {
		row("Web", "off")
	} else {
		row("Web", "on")
		row("Listen", w.Listen)
	}
	if w.Origin != "" {
		row("Origin", w.Origin)
	}
	switch {
	case w.Listen == "":
		if w.Error != "" {
			row("Error", w.Error)
		}
	case w.Serving:
		row("Serving", "yes, on "+w.Addr)
	case w.Error != "":
		row("Serving", "no: "+w.Error)
	default:
		row("Serving", "no")
	}
	row("Devices", strconv.Itoa(len(w.Devices)))
}

// defaultDeviceName is what `pair` calls a phone when --name is not given.
const defaultDeviceName = "iPhone"

// webTimeFormat is how the device list shows a moment, in local time.
const webTimeFormat = "2006-01-02 15:04"

func newWebPairCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "pair [--name <device>]",
		Short: "Make a one-time code that pairs a phone",
		Long: "Make a code the phone trades for its own revocable token. It works once, for ten " +
			"minutes; a second pair for the same name replaces the first code. The web app " +
			"must be on (loom web enable).",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			out := c.OutOrStdout()
			return withDaemon(c, func(d *client) error {
				p, err := d.PairDevice(name)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Pairing code for %s: %s\n", strings.TrimSpace(name), formatPairingCode(p.Code))
				fmt.Fprintf(out, "Open %s on the phone and enter it. It works once and expires at %s.\n",
					d.Web().Origin, p.Expires.Local().Format("15:04:05"))
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", defaultDeviceName, "Name for the device")
	return cmd
}

// formatPairingCode shows an 8-character code as XXXX-XXXX, the way the app
// accepts it (it ignores the dash).
func formatPairingCode(code string) string {
	if len(code) != 8 {
		return code
	}
	return code[:4] + "-" + code[4:]
}

func newWebDevicesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "devices",
		Short: "List the paired devices: when paired and last seen, their notifications and push state",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			return withDaemon(c, func(d *client) error {
				devs := d.Web().Devices
				if asJSON {
					return writeDevicesJSON(c.OutOrStdout(), devs)
				}
				return writeDevicesTable(c.OutOrStdout(), devs, time.Now())
			})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the devices as a JSON array")
	return cmd
}

// webDeviceJSON is a device as `devices --json` prints it: the settings and
// state, never the token.
type webDeviceJSON struct {
	Name     string        `json:"name"`
	Paired   time.Time     `json:"paired"`
	LastSeen *time.Time    `json:"last_seen,omitempty"`
	Notify   webNotifyJSON `json:"notify"`
	// Push is the subscription's state: "" (none), "ok", "expired" or
	// "failing: <status>".
	Push string `json:"push"`
}

type webNotifyJSON struct {
	Waiting     bool       `json:"waiting"`
	Finished    bool       `json:"finished"`
	PausedUntil *time.Time `json:"paused_until,omitempty"`
}

func writeDevicesJSON(out io.Writer, devs []core.WebDevice) error {
	rows := make([]webDeviceJSON, 0, len(devs))
	for _, dev := range devs {
		row := webDeviceJSON{
			Name:   dev.Name,
			Paired: dev.Paired,
			Notify: webNotifyJSON{Waiting: dev.Notify.Waiting, Finished: dev.Notify.Finished},
			Push:   string(dev.Push),
		}
		if !dev.LastSeen.IsZero() {
			t := dev.LastSeen
			row.LastSeen = &t
		}
		if !dev.Notify.PausedUntil.IsZero() {
			t := dev.Notify.PausedUntil
			row.Notify.PausedUntil = &t
		}
		rows = append(rows, row)
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

func writeDevicesTable(out io.Writer, devs []core.WebDevice, now time.Time) error {
	if len(devs) == 0 {
		_, err := fmt.Fprintln(out, "No paired devices.")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPAIRED\tLAST SEEN\tNOTIFY\tPUSH")
	for _, dev := range devs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", dev.Name, dev.Paired.Local().Format(webTimeFormat),
			seenText(dev.LastSeen), notifyText(dev.Notify, now), pushText(dev.Push))
	}
	return w.Flush()
}

// seenText is when a device was last seen, or "never".
func seenText(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format(webTimeFormat)
}

// notifyText lists the kinds of notification a device gets, with "off" for
// none, and until when they are paused while that is still ahead of now.
func notifyText(n core.NotifySettings, now time.Time) string {
	var kinds []string
	if n.Waiting {
		kinds = append(kinds, "waiting")
	}
	if n.Finished {
		kinds = append(kinds, "finished")
	}
	if len(kinds) == 0 {
		return "off"
	}
	s := strings.Join(kinds, ",")
	if n.PausedUntil.After(now) {
		s += " (paused until " + n.PausedUntil.Local().Format("15:04") + ")"
	}
	return s
}

// pushText is a subscription's state as the table shows it.
func pushText(s core.PushState) string {
	if s == core.PushNone {
		return "none"
	}
	return string(s)
}

func newWebRevokeCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "revoke <name> | --all",
		Short: "Unpair a device, or every device: its app is signed out and its push subscription dropped",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			loomErrors(c)
			if all == (len(args) == 1) {
				return errors.New("name one device to revoke, or pass --all")
			}
			out := c.OutOrStdout()
			return withDaemon(c, func(d *client) error {
				if !all {
					if err := d.RevokeDevice(args[0]); err != nil {
						return err
					}
					fmt.Fprintf(out, "Revoked %s\n", args[0])
					return nil
				}
				devs := d.Web().Devices
				if len(devs) == 0 {
					fmt.Fprintln(out, "No paired devices.")
					return nil
				}
				for i, dev := range devs {
					err := d.RevokeDevice(dev.Name)
					if core.IsNoDevice(err, dev.Name) {
						// Revoked meanwhile, elsewhere: what --all wants is done.
						fmt.Fprintf(out, "%s was already revoked\n", dev.Name)
						continue
					}
					if err != nil {
						return fmt.Errorf("revoked %d of %d devices, then %s failed: %w", i, len(devs), dev.Name, err)
					}
					fmt.Fprintf(out, "Revoked %s\n", dev.Name)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Revoke every paired device")
	return cmd
}

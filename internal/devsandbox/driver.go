package devsandbox

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/session/tmux"
)

// DriverSession is the tmux session the headless dev loom runs in. It has
// no loom_ prefix, so loom's orphan sweep never touches it.
const DriverSession = "dev-driver"

// validDriver is what WithDriver accepts: a name tmux targets exactly (no
// ':' or '.') and no session loom's orphan sweep would take for its own.
var validDriver = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// WithDriver is the sandbox driven through the tmux session name rather
// than DriverSession: a second headless loom beside the first, on the same
// server and the same daemon. Everything else is the sandbox's own.
func (s *Sandbox) WithDriver(name string) (*Sandbox, error) {
	if !validDriver.MatchString(name) || strings.HasPrefix(name, tmux.TmuxPrefix) || strings.HasPrefix(name, tmux.LegacyTmuxPrefix) {
		return nil, fmt.Errorf("invalid driver session name %q (want %s, not loom's own prefix)", name, validDriver)
	}
	d := *s
	d.driver = name
	return &d, nil
}

// Driver is the tmux session the driver methods act on.
func (s *Sandbox) Driver() string {
	if s.driver == "" {
		return DriverSession
	}
	return s.driver
}

const (
	defaultWidth  = 160
	defaultHeight = 48
	driverTimeout = 10 * time.Second
	pollInterval  = 100 * time.Millisecond
)

// StartOptions configures Start.
type StartOptions struct {
	// Width and Height size the driver pane (0 means 160×48).
	Width, Height int
	// Restart replaces an already-running driver instead of keeping it.
	Restart bool
	// Command overrides the program (default: the dev loom on the toy
	// workspace). Tests use it to drive plain shell programs.
	Command []string
}

// WaitTimeoutError is returned by WaitFor when the text never appears.
type WaitTimeoutError struct {
	Text    string
	Timeout time.Duration
	// Screen is the last successful capture, for diagnosis.
	Screen string
}

func (e *WaitTimeoutError) Error() string {
	return fmt.Sprintf("timed out after %s waiting for %q", e.Timeout, e.Text)
}

func (s *Sandbox) tmuxCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := tmux.CommandOnSocket(ctx, s.Socket(), args...)
	// The first command starts the private server, whose global environment
	// then carries the sandbox overlay into every pane it spawns.
	cmd.Env = s.Environ()
	return cmd
}

func (s *Sandbox) runTmux(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), driverTimeout)
	defer cancel()
	out, err := s.tmuxCmd(ctx, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (s *Sandbox) driverExists() bool {
	_, err := s.runTmux("has-session", "-t", tmux.SessionTarget(s.Driver()))
	return err == nil
}

// paneDead queries tmux's own pane_dead flag for the driver pane: "1" once
// its program has exited (remain-on-exit keeps the pane itself around), "0"
// while it's still running. Shared by DriverRunning and Screen so there's
// one place that knows the tmux format string. Parsed strictly: tmux prints
// empty output for a display-message whose target no longer exists (the
// server stays up because other sessions keep it alive) instead of
// erroring, so any output other than exactly "1" or "0" (including empty)
// is treated as the driver session not existing, not as "not dead".
func (s *Sandbox) paneDead() (bool, error) {
	out, err := s.runTmux("display-message", "-p", "-t", tmux.PaneTarget(s.Driver()), "#{pane_dead}")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, fmt.Errorf("driver session %s not found (pane_dead=%q)", s.Driver(), out)
	}
}

// DriverRunning reports whether the driver session exists and its program
// is still alive (a dead pane is kept by remain-on-exit). false also
// covers a session that has been fully removed (e.g. after Stop), even
// when paneDead's underlying query returns the empty-output false-success
// some tmux builds give for a missing target — see paneDead.
func (s *Sandbox) DriverRunning() bool {
	dead, err := s.paneDead()
	return err == nil && !dead
}

// Start launches the driver session on the private server. A running
// driver is kept and a dead one replaced, unless opts.Restart is set: that
// stops the driver and the sandbox's daemon first (Stop), so the dev loom
// starts afresh.
func (s *Sandbox) Start(opts StartOptions) error {
	if opts.Restart {
		if err := s.Stop(5 * time.Second); err != nil {
			return err
		}
	} else if s.DriverRunning() {
		return nil
	} else if s.driverExists() {
		if _, err := s.runTmux("kill-session", "-t", tmux.SessionTarget(s.Driver())); err != nil {
			return err
		}
	}
	w, h := opts.Width, opts.Height
	if w <= 0 {
		w = defaultWidth
	}
	if h <= 0 {
		h = defaultHeight
	}
	argv := opts.Command
	if len(argv) == 0 {
		argv = []string{s.LoomBin(), "--workspace", WorkspaceName}
	}
	// status off is set server-wide *before* new-session so the pane starts
	// at exactly w×h (loom turns the status line off on its own sessions
	// anyway). new-session in the same command list starts the server.
	args := []string{"set-option", "-g", "status", "off", ";",
		"new-session", "-d", "-s", s.Driver(),
		"-x", strconv.Itoa(w), "-y", strconv.Itoa(h), "-c", s.RepoDir()}
	for _, e := range s.Env() {
		args = append(args, "-e", e)
	}
	args = append(args, shellJoin(argv),
		";", "set-option", "-w", "-t", tmux.PaneTarget(s.Driver()), "remain-on-exit", "on")
	_, err := s.runTmux(args...)
	return err
}

// Stop quits the dev loom: its TUI (StopDriver), then the sandbox's daemon
// (StopDaemon). Loom's own agent sessions stay on the private server, so
// the next Start boots a fresh daemon that reattaches them: the restore
// path. A daemon serves every driver, so stopping it ends another
// driver's TUI too.
func (s *Sandbox) Stop(grace time.Duration) error {
	if err := s.StopDriver(grace); err != nil {
		return err
	}
	return s.StopDaemon()
}

// StopDriver asks the driver's program to quit with `q`, waits up to grace
// for it to exit, then removes the driver session. The sandbox's daemon
// keeps running, as after a real quit, so the next Start connects to it.
func (s *Sandbox) StopDriver(grace time.Duration) error {
	if !s.driverExists() {
		return nil
	}
	if s.DriverRunning() && s.SendKeys("q") == nil {
		deadline := time.Now().Add(grace)
		for s.DriverRunning() && time.Now().Before(deadline) {
			time.Sleep(pollInterval)
		}
	}
	if _, err := s.runTmux("kill-session", "-t", tmux.SessionTarget(s.Driver())); err != nil && s.driverExists() {
		return err
	}
	return nil
}

// SendKeys sends tmux key names (e.g. "n", "Enter", "Escape", "C-c") to the
// driver pane. A word tmux does not recognize is typed as characters.
func (s *Sandbox) SendKeys(keys ...string) error {
	_, err := s.runTmux(append([]string{"send-keys", "-t", tmux.PaneTarget(s.Driver())}, keys...)...)
	return err
}

// SendText types text literally, with no key-name interpretation.
func (s *Sandbox) SendText(text string) error {
	_, err := s.runTmux("send-keys", "-t", tmux.PaneTarget(s.Driver()), "-l", text)
	return err
}

// Screen captures the driver pane; ansi keeps colors and attributes. A live
// pane is captured exactly as shown, nothing more. Once the pane's program
// has exited, one extra line of scrollback is included: on this tmux,
// writing the remain-on-exit message unconditionally scrolls a dead pane up
// by exactly one line before printing "Pane is dead …", which would
// otherwise drop the program's last line of output from a plain capture.
func (s *Sandbox) Screen(ansi bool) (string, error) {
	args := []string{"capture-pane", "-p", "-t", tmux.PaneTarget(s.Driver())}
	if dead, _ := s.paneDead(); dead {
		args = append(args, "-S", "-1")
	}
	if ansi {
		args = append(args, "-e")
	}
	return s.runTmux(args...)
}

// Text captures the driver pane's text, its scrollback included, with
// lines the pane wrapped joined: a message wider than the pane (an exited
// loom's error, say) reads whole.
func (s *Sandbox) Text() (string, error) {
	return s.runTmux("capture-pane", "-p", "-J", "-S", "-", "-t", tmux.PaneTarget(s.Driver()))
}

// WaitFor polls the driver screen until text appears or timeout elapses.
func (s *Sandbox) WaitFor(text string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		if screen, err := s.Screen(false); err == nil {
			last = screen
			if strings.Contains(screen, text) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return &WaitTimeoutError{Text: text, Timeout: timeout, Screen: last}
		}
		time.Sleep(pollInterval)
	}
}

// ShellQuote single-quotes s for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin single-quotes argv for the shell tmux runs the command with.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = ShellQuote(a)
	}
	return strings.Join(quoted, " ")
}

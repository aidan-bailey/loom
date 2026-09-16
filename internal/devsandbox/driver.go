package devsandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/session/tmux"
)

// DriverSession is the tmux session the headless dev loom runs in. It has
// no loom_ prefix, so loom's orphan sweep never touches it.
const DriverSession = "dev-driver"

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
	_, err := s.runTmux("has-session", "-t="+DriverSession)
	return err == nil
}

// DriverRunning reports whether the driver session exists and its program
// is still alive (a dead pane is kept by remain-on-exit).
func (s *Sandbox) DriverRunning() bool {
	out, err := s.runTmux("display-message", "-p", "-t", DriverSession, "#{pane_dead}")
	return err == nil && strings.TrimSpace(out) == "0"
}

// Start launches the driver session on the private server. A running
// driver is kept unless opts.Restart is set; a dead one is replaced.
func (s *Sandbox) Start(opts StartOptions) error {
	if s.DriverRunning() {
		if !opts.Restart {
			return nil
		}
		if err := s.Stop(5 * time.Second); err != nil {
			return err
		}
	} else if s.driverExists() {
		if _, err := s.runTmux("kill-session", "-t="+DriverSession); err != nil {
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
		"new-session", "-d", "-s", DriverSession,
		"-x", strconv.Itoa(w), "-y", strconv.Itoa(h), "-c", s.RepoDir()}
	for _, e := range s.Env() {
		args = append(args, "-e", e)
	}
	args = append(args, shellJoin(argv),
		";", "set-option", "-w", "-t", DriverSession, "remain-on-exit", "on")
	_, err := s.runTmux(args...)
	return err
}

// Stop asks the driver's program to quit with `q`, waits up to grace for it
// to exit, then removes the driver session. Loom's own agent sessions stay
// on the private server, as after a real quit.
func (s *Sandbox) Stop(grace time.Duration) error {
	if !s.driverExists() {
		return nil
	}
	if s.DriverRunning() && s.SendKeys("q") == nil {
		deadline := time.Now().Add(grace)
		for s.DriverRunning() && time.Now().Before(deadline) {
			time.Sleep(pollInterval)
		}
	}
	if _, err := s.runTmux("kill-session", "-t="+DriverSession); err != nil && s.driverExists() {
		return err
	}
	return nil
}

// SendKeys sends tmux key names (e.g. "n", "Enter", "Escape", "C-c") to the
// driver pane. A word tmux does not recognize is typed as characters.
func (s *Sandbox) SendKeys(keys ...string) error {
	_, err := s.runTmux(append([]string{"send-keys", "-t", DriverSession}, keys...)...)
	return err
}

// SendText types text literally, with no key-name interpretation.
func (s *Sandbox) SendText(text string) error {
	_, err := s.runTmux("send-keys", "-t", DriverSession, "-l", text)
	return err
}

// Screen captures the driver pane; ansi keeps colors and attributes. It
// always includes one line of scrollback above the visible screen: on this
// tmux, writing the remain-on-exit message unconditionally scrolls the pane
// up by exactly one line, so a plain capture of a just-died pane loses its
// last line of output. Grabbing that extra history line keeps a dead pane's
// last output capturable without affecting a live pane, whose top history
// line stays put (and therefore equal across successive captures) as long
// as nothing has scrolled.
func (s *Sandbox) Screen(ansi bool) (string, error) {
	args := []string{"capture-pane", "-p", "-t", DriverSession, "-S", "-1"}
	if ansi {
		args = append(args, "-e")
	}
	return s.runTmux(args...)
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

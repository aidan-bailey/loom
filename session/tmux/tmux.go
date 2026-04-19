package tmux

import (
	"bytes"
	internalexec "claude-squad/internal/exec"
	"claude-squad/log"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

const ProgramClaude = "claude"

const ProgramAider = "aider"
const ProgramGemini = "gemini"

// tmuxTimeout bounds the wall time of a single tmux subprocess invocation.
// These calls run in the metadata tick (capture-pane, has-session), so a
// hung tmux client would freeze the UI without this cap.
const tmuxTimeout = 5 * time.Second

// tmuxStartTimeout applies to session creation, which can spawn the agent
// process and may be slower than other tmux commands.
const tmuxStartTimeout = 10 * time.Second

// TmuxSession represents a managed tmux session
type TmuxSession struct {
	// Initialized by NewTmuxSession
	//
	// The name of the tmux session and the sanitized name used for tmux commands.
	sanitizedName string
	program       string
	// ptyFactory is used to create a PTY for the tmux session.
	ptyFactory PtyFactory
	// cmdExec is used to execute commands in the tmux session.
	cmdExec internalexec.Executor

	// Initialized by Start or Restore
	//
	// ptmx is the detached-mode PTY attached to the tmux session. The UI drives
	// preview rendering, resizing, and keystroke injection through it. Full-screen
	// attach bypasses this PTY entirely via tea.ExecProcess, so ptmx is temporarily
	// closed while the child tmux process owns the real tty (see PausePreview/
	// ResumePreview). This should never be nil outside of those paused windows.
	ptmx *os.File
	// monitor monitors the tmux pane content and sends signals to the UI when it's status changes
	monitor *statusMonitor

	// Output pump — continuously drains PTY output to prevent buffer deadlock.
	// When nothing reads from ptmx, the tmux client blocks on stdout and stops
	// processing stdin, which breaks SendKeysRaw (inline attach).
	pumpMu   sync.Mutex
	pumpDest io.Writer
	pumpDone chan struct{} // closed when the pump goroutine exits
}

const TmuxPrefix = "claudesquad_"

var whiteSpaceRegex = regexp.MustCompile(`\s+`)

func ToClaudeSquadTmuxName(str string) string {
	str = whiteSpaceRegex.ReplaceAllString(str, "")
	str = strings.ReplaceAll(str, ".", "_") // tmux replaces all . with _
	return fmt.Sprintf("%s%s", TmuxPrefix, str)
}

// NewTmuxSession creates a new TmuxSession with the given name and program.
func NewTmuxSession(name string, program string) *TmuxSession {
	return newTmuxSession(name, program, MakePtyFactory(), internalexec.Default{})
}

// NewTmuxSessionWithDeps creates a new TmuxSession with provided dependencies for testing.
func NewTmuxSessionWithDeps(name string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor) *TmuxSession {
	return newTmuxSession(name, program, ptyFactory, cmdExec)
}

func newTmuxSession(name string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor) *TmuxSession {
	return &TmuxSession{
		sanitizedName: ToClaudeSquadTmuxName(name),
		program:       program,
		ptyFactory:    ptyFactory,
		cmdExec:       cmdExec,
	}
}

// Start creates and starts a new tmux session, then attaches to it. Program is the command to run in
// the session (ex. claude). workdir is the git worktree directory.
func (t *TmuxSession) Start(workDir string) error {
	// Check if the session already exists
	if t.DoesSessionExist() {
		return fmt.Errorf("tmux session already exists: %s", t.sanitizedName)
	}

	// Create a new detached tmux session and start claude in it.
	// tmuxStartTimeout allows the agent process's initial exec before tmux
	// returns control; tmux itself is quick, but the wrapped program may not be.
	startCtx, startCancel := context.WithTimeout(context.Background(), tmuxStartTimeout)
	defer startCancel()
	cmd := exec.CommandContext(startCtx, "tmux", "new-session", "-d", "-s", t.sanitizedName, "-c", workDir, t.program)

	ptmx, err := t.ptyFactory.Start(cmd)
	if err != nil {
		// Cleanup any partially created session if any exists.
		if t.DoesSessionExist() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), tmuxTimeout)
			cleanupCmd := exec.CommandContext(cleanupCtx, "tmux", "kill-session", "-t", t.sanitizedName)
			if cleanupErr := t.cmdExec.Run(cleanupCmd); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			cleanupCancel()
		}
		return fmt.Errorf("error starting tmux session: %w", err)
	}

	// Poll for session existence with exponential backoff
	timeout := time.After(2 * time.Second)
	sleepDuration := 5 * time.Millisecond
	for !t.DoesSessionExist() {
		select {
		case <-timeout:
			if cleanupErr := t.Close(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			return fmt.Errorf("timed out waiting for tmux session %s: %v", t.sanitizedName, err)
		default:
			time.Sleep(sleepDuration)
			// Exponential backoff up to 50ms max
			if sleepDuration < 50*time.Millisecond {
				sleepDuration *= 2
			}
		}
	}
	ptmx.Close()

	// Set history limit to enable scrollback (default is 2000, we'll use 10000 for more history)
	histCtx, histCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	historyCmd := exec.CommandContext(histCtx, "tmux", "set-option", "-t", t.sanitizedName, "history-limit", "10000")
	if err := t.cmdExec.Run(historyCmd); err != nil {
		log.WarningLog.Printf("failed to set history-limit for session %s: %v", t.sanitizedName, err)
	}
	histCancel()

	// Enable mouse scrolling for the session
	mouseCtx, mouseCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	mouseCmd := exec.CommandContext(mouseCtx, "tmux", "set-option", "-t", t.sanitizedName, "mouse", "on")
	if err := t.cmdExec.Run(mouseCmd); err != nil {
		log.WarningLog.Printf("failed to enable mouse scrolling for session %s: %v", t.sanitizedName, err)
	}
	mouseCancel()

	// Rebind Ctrl-Q to detach-client for full-screen attach. The default tmux
	// prefix is Ctrl-B + d; our users expect Ctrl-Q because inline attach has
	// always used it. This binding is server-wide, but claude-squad has always
	// assumed ownership of Ctrl-Q as its detach key.
	bindCtx, bindCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	bindCmd := exec.CommandContext(bindCtx, "tmux", "bind-key", "-n", "C-q", "detach-client")
	if err := t.cmdExec.Run(bindCmd); err != nil {
		log.WarningLog.Printf("failed to bind C-q to detach-client: %v", err)
	}
	bindCancel()

	err = t.Restore()
	if err != nil {
		if cleanupErr := t.Close(); cleanupErr != nil {
			err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
		}
		return fmt.Errorf("error restoring tmux session: %w", err)
	}

	return nil
}

// CheckAndHandleTrustPrompt checks the pane content once for a trust prompt and dismisses it if found.
// Returns true if the prompt was found and handled.
func (t *TmuxSession) CheckAndHandleTrustPrompt() bool {
	content, err := t.CapturePaneContent()
	if err != nil {
		return false
	}

	if strings.HasSuffix(t.program, ProgramClaude) {
		if strings.Contains(content, "Do you trust the files in this folder?") ||
			strings.Contains(content, "new MCP server") {
			if err := t.TapEnter(); err != nil {
				log.ErrorLog.Printf("could not tap enter on trust/MCP screen: %v", err)
			}
			return true
		}
	} else {
		if strings.Contains(content, "Open documentation url for more info") {
			if err := t.TapDAndEnter(); err != nil {
				log.ErrorLog.Printf("could not tap enter on trust screen: %v", err)
			}
			return true
		}
	}
	return false
}

// Restore attaches to an existing session and restores the window size.
// It starts a background pump goroutine that drains PTY output to prevent
// buffer deadlock (the tmux client blocks on stdout when the buffer fills,
// which also blocks stdin processing).
func (t *TmuxSession) Restore() error {
	// Close any prior PTY and wait for its pump to exit before creating a new
	// one, otherwise the old pump goroutine leaks and keeps a stale FD alive.
	if t.ptmx != nil {
		_ = t.ptmx.Close()
		t.ptmx = nil
	}
	if t.pumpDone != nil {
		<-t.pumpDone
		t.pumpDone = nil
	}

	ptmx, err := t.ptyFactory.Start(exec.Command("tmux", "attach-session", "-t", t.sanitizedName))
	if err != nil {
		return fmt.Errorf("error opening PTY: %w", err)
	}
	t.ptmx = ptmx
	t.monitor = newStatusMonitor()
	t.startOutputPump(ptmx)
	return nil
}

// startOutputPump launches a goroutine that continuously reads from the PTY
// and writes to pumpDest. This prevents the PTY output buffer from filling up,
// which would cause the tmux client to block and stop processing input.
func (t *TmuxSession) startOutputPump(ptmx *os.File) {
	t.pumpMu.Lock()
	t.pumpDest = io.Discard
	t.pumpMu.Unlock()
	t.pumpDone = make(chan struct{})

	go func() {
		defer close(t.pumpDone)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				t.pumpMu.Lock()
				dest := t.pumpDest
				t.pumpMu.Unlock()
				_, _ = dest.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
}

// setPumpDest changes where the output pump writes to (io.Discard or os.Stdout).
func (t *TmuxSession) setPumpDest(w io.Writer) {
	t.pumpMu.Lock()
	t.pumpDest = w
	t.pumpMu.Unlock()
}

type statusMonitor struct {
	// Store hashes to save memory.
	prevOutputHash []byte
}

func newStatusMonitor() *statusMonitor {
	return &statusMonitor{}
}

// hash hashes the string.
func (m *statusMonitor) hash(s string) []byte {
	h := sha256.New()
	// TODO: this allocation sucks since the string is probably large. Ideally, we hash the string directly.
	h.Write([]byte(s))
	return h.Sum(nil)
}

// TapEnter sends an enter keystroke to the tmux pane.
func (t *TmuxSession) TapEnter() error {
	if t.ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	_, err := t.ptmx.Write([]byte{0x0D})
	if err != nil {
		return fmt.Errorf("error sending enter keystroke to PTY: %w", err)
	}
	return nil
}

// TapDAndEnter sends 'D' followed by an enter keystroke to the tmux pane.
func (t *TmuxSession) TapDAndEnter() error {
	if t.ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	_, err := t.ptmx.Write([]byte{0x44, 0x0D})
	if err != nil {
		return fmt.Errorf("error sending enter keystroke to PTY: %w", err)
	}
	return nil
}

func (t *TmuxSession) SendKeys(keys string) error {
	if t.ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	_, err := t.ptmx.Write([]byte(keys))
	return err
}

// SendKeysRaw writes raw bytes directly to the tmux PTY.
func (t *TmuxSession) SendKeysRaw(b []byte) error {
	if t.ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	_, err := t.ptmx.Write(b)
	return err
}

// HasUpdated checks if the tmux pane content has changed since the last tick. It also returns true if
// the tmux pane has a prompt for aider or claude code.
func (t *TmuxSession) HasUpdated() (updated bool, hasPrompt bool) {
	content, err := t.CapturePaneContent()
	if err != nil {
		log.ErrorLog.Printf("error capturing pane content in status monitor: %v", err)
		return false, false
	}

	// Only set hasPrompt for claude and aider. Use these strings to check for a prompt.
	if t.program == ProgramClaude {
		hasPrompt = strings.Contains(content, "No, and tell Claude what to do differently")
	} else if strings.HasPrefix(t.program, ProgramAider) {
		hasPrompt = strings.Contains(content, "(Y)es/(N)o/(D)on't ask again")
	} else if strings.HasPrefix(t.program, ProgramGemini) {
		hasPrompt = strings.Contains(content, "Yes, allow once")
	}

	if !bytes.Equal(t.monitor.hash(content), t.monitor.prevOutputHash) {
		t.monitor.prevOutputHash = t.monitor.hash(content)
		return true, hasPrompt
	}
	return false, hasPrompt
}

// CaptureAndProcess captures pane content once and runs both trust prompt
// and update detection checks, avoiding duplicate CapturePaneContent calls.
func (t *TmuxSession) CaptureAndProcess() (content string, updated bool, hasPrompt bool, trustHandled bool) {
	var err error
	content, err = t.CapturePaneContent()
	if err != nil {
		log.ErrorLog.Printf("error capturing pane content: %v", err)
		return "", false, false, false
	}

	// Trust prompt detection (from CheckAndHandleTrustPrompt).
	if strings.HasSuffix(t.program, ProgramClaude) {
		if strings.Contains(content, "Do you trust the files in this folder?") ||
			strings.Contains(content, "new MCP server") {
			if err := t.TapEnter(); err != nil {
				log.ErrorLog.Printf("could not tap enter on trust/MCP screen: %v", err)
			}
			trustHandled = true
		}
	} else {
		if strings.Contains(content, "Open documentation url for more info") {
			if err := t.TapDAndEnter(); err != nil {
				log.ErrorLog.Printf("could not tap enter on trust screen: %v", err)
			}
			trustHandled = true
		}
	}

	// Update detection (from HasUpdated).
	if t.program == ProgramClaude {
		hasPrompt = strings.Contains(content, "No, and tell Claude what to do differently")
	} else if strings.HasPrefix(t.program, ProgramAider) {
		hasPrompt = strings.Contains(content, "(Y)es/(N)o/(D)on't ask again")
	} else if strings.HasPrefix(t.program, ProgramGemini) {
		hasPrompt = strings.Contains(content, "Yes, allow once")
	}

	if !bytes.Equal(t.monitor.hash(content), t.monitor.prevOutputHash) {
		t.monitor.prevOutputHash = t.monitor.hash(content)
		updated = true
	}

	return content, updated, hasPrompt, trustHandled
}

// GetContentHash returns the last computed content hash from HasUpdated
// or CaptureAndProcess. Returns nil if no hash has been computed yet.
func (t *TmuxSession) GetContentHash() []byte {
	if t.monitor == nil {
		return nil
	}
	return t.monitor.prevOutputHash
}

// FullScreenAttachCmd returns a command that attaches to this tmux session in
// the foreground. It's intended to be handed to tea.ExecProcess, which
// releases and restores the terminal around the call so the child tmux
// owns the real tty for the duration of the attach. Detach is driven by
// the C-q key binding installed during Start (see bind-key call).
func (t *TmuxSession) FullScreenAttachCmd() *exec.Cmd {
	return exec.Command("tmux", "attach-session", "-t", t.sanitizedName)
}

// PausePreview closes the detached preview PTY and waits for its pump to
// exit. Call this immediately before tea.ExecProcess hands the tty to a
// foreground `tmux attach-session`, so there are no stray readers on the
// session during the attach. ResumePreview re-opens the PTY afterwards.
func (t *TmuxSession) PausePreview() error {
	if t.ptmx != nil {
		if err := t.ptmx.Close(); err != nil {
			return fmt.Errorf("error closing preview PTY: %w", err)
		}
		t.ptmx = nil
	}
	if t.pumpDone != nil {
		<-t.pumpDone
		t.pumpDone = nil
	}
	return nil
}

// ResumePreview reopens the detached preview PTY after a full-screen attach
// returns control. It is a thin wrapper around Restore kept as a named method
// for clarity at the call sites in app.Update.
func (t *TmuxSession) ResumePreview() error {
	return t.Restore()
}

// Close terminates the tmux session and cleans up resources
func (t *TmuxSession) Close() error {
	var errs []error

	if t.ptmx != nil {
		if err := t.ptmx.Close(); err != nil {
			errs = append(errs, fmt.Errorf("error closing PTY: %w", err))
		}
		t.ptmx = nil
	}

	// Wait for pump goroutine to exit after PTY close.
	if t.pumpDone != nil {
		<-t.pumpDone
	}

	killCtx, killCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer killCancel()
	cmd := exec.CommandContext(killCtx, "tmux", "kill-session", "-t", t.sanitizedName)
	if err := t.cmdExec.Run(cmd); err != nil {
		errs = append(errs, fmt.Errorf("error killing tmux session: %w", err))
	}

	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	errMsg := "multiple errors occurred during cleanup:"
	for _, err := range errs {
		errMsg += "\n  - " + err.Error()
	}
	return errors.New(errMsg)
}

// SetDetachedSize set the width and height of the session while detached. This makes the
// tmux output conform to the specified shape.
func (t *TmuxSession) SetDetachedSize(width, height int) error {
	return t.updateWindowSize(width, height)
}

// updateWindowSize updates the window size of the PTY.
func (t *TmuxSession) updateWindowSize(cols, rows int) error {
	if t.ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	return pty.Setsize(t.ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
		X:    0,
		Y:    0,
	})
}

func (t *TmuxSession) DoesSessionExist() bool {
	// Using "-t name" does a prefix match, which is wrong. `-t=` does an exact match.
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	existsCmd := exec.CommandContext(ctx, "tmux", "has-session", fmt.Sprintf("-t=%s", t.sanitizedName))
	return t.cmdExec.Run(existsCmd) == nil
}

// CapturePaneContent captures the content of the tmux pane
func (t *TmuxSession) CapturePaneContent() (string, error) {
	// Add -e flag to preserve escape sequences (ANSI color codes).
	// Note: -J (join wrapped lines) is intentionally omitted so that tmux returns physical
	// screen rows (each bounded by the pane width). Using -J would join wrapped segments into
	// one long logical line; when lipgloss later renders those lines at the same width they
	// re-wrap and produce extra visual rows, causing the pane to overflow its height.
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-e", "-t", t.sanitizedName)
	output, err := t.cmdExec.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("error capturing pane content: %v", err)
	}
	return string(output), nil
}

// CapturePaneContentWithOptions captures the pane content with additional options
// start and end specify the starting and ending line numbers (use "-" for the start/end of history)
func (t *TmuxSession) CapturePaneContentWithOptions(start, end string) (string, error) {
	// Add -e flag to preserve escape sequences (ANSI color codes)
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-e", "-J", "-S", start, "-E", end, "-t", t.sanitizedName)
	output, err := t.cmdExec.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to capture tmux pane content with options: %v", err)
	}
	return string(output), nil
}

// CleanupSessions kills all tmux sessions that start with "session-"
func CleanupSessions(cmdExec internalexec.Executor) error {
	// First try to list sessions
	lsCtx, lsCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer lsCancel()
	cmd := exec.CommandContext(lsCtx, "tmux", "ls")
	output, err := cmdExec.Output(cmd)

	// If there's an error and it's because no server is running, that's fine
	// Exit code 1 typically means no sessions exist
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil // No sessions to clean up
		}
		return fmt.Errorf("failed to list tmux sessions: %v", err)
	}

	re := regexp.MustCompile(fmt.Sprintf(`%s.*:`, TmuxPrefix))
	matches := re.FindAllString(string(output), -1)
	for i, match := range matches {
		matches[i] = match[:strings.Index(match, ":")]
	}

	for _, match := range matches {
		log.InfoLog.Printf("cleaning up session: %s", match)
		killCtx, killCancel := context.WithTimeout(context.Background(), tmuxTimeout)
		if err := cmdExec.Run(exec.CommandContext(killCtx, "tmux", "kill-session", "-t", match)); err != nil {
			killCancel()
			return fmt.Errorf("failed to kill tmux session %s: %v", match, err)
		}
		killCancel()
	}
	return nil
}

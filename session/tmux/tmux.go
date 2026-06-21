package tmux

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/vt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// ProgramClaude, ProgramAider, and ProgramGemini are the canonical
// program identifiers used by trust-prompt detection and adapter lookup.
// They match the literal command names (no path or flags); per-program
// behavior is keyed off these constants in CheckAndHandleTrustPrompt
// and the session/agent registry.
const (
	ProgramClaude = "claude"
	ProgramAider  = "aider"
	ProgramGemini = "gemini"
)

// tmuxTimeout bounds the wall time of a single tmux subprocess invocation.
// These calls run in the metadata tick (capture-pane, has-session), so a
// hung tmux client would freeze the UI without this cap.
const tmuxTimeout = 5 * time.Second

// tmuxStartTimeout applies to session creation, which can spawn the agent
// process and may be slower than other tmux commands.
const tmuxStartTimeout = 10 * time.Second

// pumpWaitTimeout bounds how long lifecycle methods (Close, Restore,
// PausePreview) will wait for the output-pump goroutine to drain after
// ptmx.Close. The pump exits on any Read error, so in the common case
// this receives immediately; a stuck tmux client or platform Read
// pathology could otherwise hang the caller — which through Instance.
// Pause and the daemon's UpdateDiffStats would wedge every other
// tracked instance. On timeout we log and move on: the leaked goroutine
// is isolated to the dying session rather than the whole app.
const pumpWaitTimeout = 2 * time.Second

// TmuxSession is a managed tmux session bound to a single instance.
// The zero value is not usable — construct via [NewTmuxSession] (or
// [NewTmuxSessionWithDeps] in tests) so the PTY factory and executor
// are wired up. One [TmuxSession] owns exactly one tmux session and one
// detached-mode PTY. The ptmx and monitor fields are touched from two
// goroutines — the metadata fan-out (CaptureAndProcess/HasUpdated/keystroke
// injection) and the Update loop's attach lifecycle (Restore/PausePreview/
// Close) — so both are guarded by stateMu (see its doc).
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
	// stateMu guards ptmx and monitor (both pointer and the monitor's
	// fields). The metadata fan-out reads ptmx and updates monitor while
	// the Update goroutine's attach lifecycle reassigns or closes them, so
	// every access goes through stateMu. To avoid stalling the UI, callers
	// snapshot ptmx under the lock and run PTY I/O on the local copy; the
	// lock is never held across ptmx I/O, ptyFactory.Start, or a tmux
	// subprocess. The monitor hash update is CPU-only and does run under it.
	stateMu sync.Mutex
	// ptmx is the detached-mode PTY attached to the tmux session. The UI drives
	// preview rendering, resizing, and keystroke injection through it. Full-screen
	// attach bypasses this PTY entirely via tea.ExecProcess, so ptmx is temporarily
	// closed while the child tmux process owns the real tty (see PausePreview/
	// ResumePreview). This should never be nil outside of those paused windows.
	ptmx *os.File
	// monitor monitors the tmux pane content and sends signals to the UI when it's status changes
	monitor *statusMonitor

	// emu is the in-process terminal emulator for pane DISPLAY (Phase 1). The
	// output pump writes raw ptmx bytes into it; Render reads the visible
	// screen for Preview/terminal content. nil selects the legacy capture-pane
	// path (Windows / LOOM_PANE_RENDERER=snapshot). Guarded by stateMu like
	// ptmx: Restore/Close/PausePreview swap or drop it while the preview path
	// reads it. The emulator's own RWMutex makes concurrent Write vs Render safe.
	emu vt.Emulator
	// lastCols/lastRows track the most recent pane geometry from SetDetachedSize
	// so a freshly built emulator in Restore starts at the correct size.
	lastCols int
	lastRows int

	// Output pump — continuously drains PTY output to prevent buffer deadlock.
	// When nothing reads from ptmx, the tmux client blocks on stdout and stops
	// processing stdin, which breaks SendKeysRaw (inline attach).
	pumpMu     sync.Mutex
	pumpDest   io.Writer
	pumpDone   chan struct{}      // closed when the pump goroutine exits
	pumpCancel context.CancelFunc // nil outside an active pump; cancels the pump's ctx
}

// TmuxPrefix is the tmux session-name prefix applied to every Loom
// session. Orphan sweeps and session lookup rely on this prefix to
// distinguish Loom-owned sessions from sessions owned by other tools
// sharing the user's tmux server.
const TmuxPrefix = "loom_"

// LegacyTmuxPrefix is the tmux session prefix used before the rename
// from claude-squad to loom. Still recognized by orphan-sweep logic
// and by the startup rename pass so in-flight sessions survive the
// upgrade transparently.
const LegacyTmuxPrefix = "claudesquad_"

var whiteSpaceRegex = regexp.MustCompile(`\s+`)

// ToLoomTmuxName returns the canonical tmux session name for a given
// instance title under the current prefix.
func ToLoomTmuxName(str string) string {
	str = whiteSpaceRegex.ReplaceAllString(str, "")
	str = strings.ReplaceAll(str, ".", "_") // tmux replaces all . with _
	return fmt.Sprintf("%s%s", TmuxPrefix, str)
}

// ToLegacyTmuxName returns the pre-rename tmux session name for a
// given instance title. Used only by RenameLegacySessions at startup;
// no production code path should depend on this name otherwise.
func ToLegacyTmuxName(str string) string {
	str = whiteSpaceRegex.ReplaceAllString(str, "")
	str = strings.ReplaceAll(str, ".", "_")
	return fmt.Sprintf("%s%s", LegacyTmuxPrefix, str)
}

// RenameLegacySessions renames any tmux sessions matching the legacy
// claudesquad_* prefix to their loom_* equivalent so that in-flight
// sessions from a pre-rename binary continue to be found by reconcile
// on the next startup. Silent on failure — a missing session or an
// unreachable tmux server is expected and harmless.
//
// Called once from main.go before the reconcile pass. Idempotent: on
// later launches the legacy sessions are gone and the loop is a no-op.
func RenameLegacySessions(titles []string, cmdExec internalexec.Executor) {
	if len(titles) == 0 {
		return
	}
	for _, t := range titles {
		legacy := ToLegacyTmuxName(t)
		target := ToLoomTmuxName(t)
		if legacy == target {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
		cmd := exec.CommandContext(ctx, "tmux", "rename-session", "-t", legacy, target)
		_ = cmdExec.Run(cmd)
		cancel()
	}
}

// NewTmuxSession constructs a TmuxSession wired to the production PTY
// factory and subprocess executor. The tmux session is NOT created at
// this point — call Start (for a fresh session) or Restore (to attach
// to one that already exists on disk).
func NewTmuxSession(name string, program string) *TmuxSession {
	return newTmuxSession(name, program, MakePtyFactory(), internalexec.Default{})
}

// NewTmuxSessionWithDeps is [NewTmuxSession] with injected dependencies
// for tests. Pass a fake [PtyFactory] and [internalexec.Executor] to
// avoid spawning real subprocesses or allocating real PTYs.
func NewTmuxSessionWithDeps(name string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor) *TmuxSession {
	return newTmuxSession(name, program, ptyFactory, cmdExec)
}

func newTmuxSession(name string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor) *TmuxSession {
	return &TmuxSession{
		sanitizedName: ToLoomTmuxName(name),
		program:       program,
		ptyFactory:    ptyFactory,
		cmdExec:       cmdExec,
		// monitor is always non-nil for the session's lifetime so HasUpdated
		// and CaptureAndProcess can read it without a guard. Restore reassigns
		// a fresh instance on every PTY attach, so the initial value is only
		// load-bearing for paused sessions (constructed without Restore).
		monitor: newStatusMonitor(),
		// Default geometry until the first SetDetachedSize; a fresh emulator in
		// Restore starts here so it is never zero-sized.
		lastCols: 80,
		lastRows: 24,
	}
}

// Start creates and starts a new tmux session, then attaches to it. Program is the command to run in
// the session (ex. claude). workdir is the git worktree directory.
func (t *TmuxSession) Start(workDir string) (err error) {
	t0 := time.Now()
	log.For("tmux").Debug("start.begin", "session", t.sanitizedName, "program", t.program, "workdir", workDir)
	defer func() {
		args := []any{"session", t.sanitizedName, "duration_ms", time.Since(t0).Milliseconds()}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		log.For("tmux").Debug("start.end", args...)
	}()

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
		log.For("tmux").Warn("history_limit_failed", "session", t.sanitizedName, "err", err)
	}
	histCancel()

	// Enable mouse scrolling for the session
	mouseCtx, mouseCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	mouseCmd := exec.CommandContext(mouseCtx, "tmux", "set-option", "-t", t.sanitizedName, "mouse", "on")
	if err := t.cmdExec.Run(mouseCmd); err != nil {
		log.For("tmux").Warn("mouse_scroll_failed", "session", t.sanitizedName, "err", err)
	}
	mouseCancel()

	// Disable the tmux status bar. The detached attach stream the emulator
	// consumes includes the status line, but the pane preview must not — it
	// would consume a render row and shift content. tmux still owns the
	// session; only its chrome is hidden.
	statusCtx, statusCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	statusCmd := exec.CommandContext(statusCtx, "tmux", "set-option", "-t", t.sanitizedName, "status", "off")
	if err := t.cmdExec.Run(statusCmd); err != nil {
		log.For("tmux").Warn("status_off_failed", "session", t.sanitizedName, "err", err)
	}
	statusCancel()

	// Rebind Ctrl-Q to detach-client for full-screen attach. The default tmux
	// prefix is Ctrl-B + d; our users expect Ctrl-Q because inline attach has
	// always used it. This binding is server-wide, but claude-squad has always
	// assumed ownership of Ctrl-Q as its detach key.
	bindCtx, bindCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	bindCmd := exec.CommandContext(bindCtx, "tmux", "bind-key", "-n", "C-q", "detach-client")
	if err := t.cmdExec.Run(bindCmd); err != nil {
		log.For("tmux").Warn("bind_cq_failed", "err", err)
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
				log.For("tmux").Error("trust_prompt.tap_enter_failed", "prompt", "claude_trust_or_mcp", "err", err)
			}
			return true
		}
	} else {
		if strings.Contains(content, "Open documentation url for more info") {
			if err := t.TapDAndEnter(); err != nil {
				log.For("tmux").Error("trust_prompt.tap_enter_failed", "prompt", "other_trust", "err", err)
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
	log.For("tmux").Debug("restore", "session", t.sanitizedName)
	// Close any prior PTY and wait for its pump to exit before creating a new
	// one, otherwise the old pump goroutine leaks and keeps a stale FD alive.
	// Snapshot-and-clear under stateMu, then do the (blocking) close outside it.
	t.stateMu.Lock()
	old := t.ptmx
	t.ptmx = nil
	oldEmu := t.emu
	t.emu = nil
	cols, rows := t.lastCols, t.lastRows
	t.stateMu.Unlock()
	if old != nil {
		t.signalPumpStop(old)
		_ = old.Close()
	}
	t.waitPumpExit()
	// Close the old emulator only after the pump has fully exited, so a
	// still-draining pump can't write into a freed emulator.
	if oldEmu != nil {
		_ = oldEmu.Close()
	}

	ptmx, err := t.ptyFactory.Start(exec.Command("tmux", "attach-session", "-t", t.sanitizedName))
	if err != nil {
		return fmt.Errorf("error opening PTY: %w", err)
	}
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	emu := newEmulator(cols, rows)
	t.stateMu.Lock()
	t.ptmx = ptmx
	t.emu = emu
	t.monitor = newStatusMonitor()
	t.stateMu.Unlock()
	t.startOutputPump(ptmx) // defaults pumpDest = emu (Task 6)
	return nil
}

// currentPtmx returns the active PTY under stateMu. Callers perform any I/O
// on the returned handle outside the lock; a concurrent lifecycle method may
// close it, in which case that I/O fails with a benign error rather than
// racing on the pointer.
func (t *TmuxSession) currentPtmx() *os.File {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	return t.ptmx
}

// processContentHash feeds the latest pane content to the monitor and reports
// whether it changed since the previous tick. Runs under stateMu so it is safe
// against Restore swapping the monitor pointer and against a concurrent
// capture; the hash is CPU-only, so holding the lock adds no I/O latency.
func (t *TmuxSession) processContentHash(content string) bool {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	if t.monitor == nil {
		return false
	}
	newHash := t.monitor.hash(content)
	if !bytes.Equal(newHash, t.monitor.prevOutputHash) {
		t.monitor.prevOutputHash = newHash
		return true
	}
	return false
}

// startOutputPump launches a goroutine that continuously reads from the PTY
// and writes to pumpDest. This prevents the PTY output buffer from filling up,
// which would cause the tmux client to block and stop processing input.
func (t *TmuxSession) startOutputPump(ptmx *os.File) {
	ctx, cancel := context.WithCancel(context.Background())
	// Default the pump into the emulator so the visible screen stays current.
	// nil emu (Windows / snapshot kill-switch) keeps the legacy io.Discard drain.
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	var dest io.Writer = io.Discard
	if emu != nil {
		dest = emu
	}
	t.pumpMu.Lock()
	t.pumpDest = dest
	t.pumpCancel = cancel
	t.pumpMu.Unlock()
	t.pumpDone = make(chan struct{})

	go func() {
		defer close(t.pumpDone)
		buf := make([]byte, 4096)
		for {
			if ctx.Err() != nil {
				return
			}
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

// signalPumpStop requests the output pump goroutine to exit promptly.
// It cancels the pump's context AND calls SetReadDeadline(time.Now())
// on the PTY so any in-flight blocked Read returns immediately with
// os.ErrDeadlineExceeded — otherwise the goroutine could wait
// indefinitely on a PTY that has no pending output and no writer
// exiting. Best-effort: on platforms where a file type does not
// support deadlines, SetReadDeadline returns an error we ignore, and
// the pumpWaitTimeout watchdog in waitPumpExit still bounds the wait.
func (t *TmuxSession) signalPumpStop(ptmx *os.File) {
	t.pumpMu.Lock()
	cancel := t.pumpCancel
	t.pumpCancel = nil
	t.pumpMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if ptmx != nil {
		_ = ptmx.SetReadDeadline(time.Now())
	}
}

// setPumpDest changes where the output pump writes to (io.Discard or os.Stdout).
func (t *TmuxSession) setPumpDest(w io.Writer) {
	t.pumpMu.Lock()
	t.pumpDest = w
	t.pumpMu.Unlock()
}

// SimulateStuckPumpForTest replaces any live pump state with a live
// channel nobody closes, modeling a pathological case where the pump
// goroutine has wedged (stuck ptmx.Read, platform-specific Close that
// doesn't interrupt a blocked Read, etc.). Lifecycle methods that
// previously bare-waited on pumpDone would have hung indefinitely;
// with waitPumpExit in place they now return within pumpWaitTimeout.
// Test-only: the name and doc comment are guardrails, nothing about
// the method enforces test-only use.
func (t *TmuxSession) SimulateStuckPumpForTest() {
	t.pumpDone = make(chan struct{})
	t.stateMu.Lock()
	t.ptmx = nil
	t.stateMu.Unlock()
}

// waitPumpExit blocks until the current pump goroutine signals exit or
// pumpWaitTimeout elapses, whichever comes first. Only the pump
// goroutine ever closes pumpDone, so callers must not close it
// themselves. After this returns, t.pumpDone is cleared so a later
// Restore reuses the field safely.
func (t *TmuxSession) waitPumpExit() {
	if t.pumpDone == nil {
		return
	}
	select {
	case <-t.pumpDone:
	case <-time.After(pumpWaitTimeout):
		log.For("tmux").Warn("pump.wait_timeout", "session", t.sanitizedName, "timeout", pumpWaitTimeout.String())
	}
	t.pumpDone = nil
}

type statusMonitor struct {
	// Store hashes to save memory.
	prevOutputHash []byte
	// hashCalls counts hash invocations so tests can assert the dedup
	// guarantee (one hash per HasUpdated / CaptureAndProcess call).
	hashCalls int
}

func newStatusMonitor() *statusMonitor {
	return &statusMonitor{}
}

// hash hashes the string. io.WriteString is used so any future
// StringWriter-aware hasher can feed the string without a []byte copy;
// against stdlib sha256 it still converts, but the single caller site
// now allocates once per update instead of twice.
func (m *statusMonitor) hash(s string) []byte {
	m.hashCalls++
	h := sha256.New()
	_, _ = io.WriteString(h, s)
	return h.Sum(nil)
}

// TapEnter sends an enter keystroke to the tmux pane.
func (t *TmuxSession) TapEnter() error {
	ptmx := t.currentPtmx()
	if ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	_, err := ptmx.Write([]byte{0x0D})
	if err != nil {
		return fmt.Errorf("error sending enter keystroke to PTY: %w", err)
	}
	return nil
}

// TapDAndEnter sends 'D' followed by an enter keystroke to the tmux pane.
func (t *TmuxSession) TapDAndEnter() error {
	ptmx := t.currentPtmx()
	if ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	_, err := ptmx.Write([]byte{0x44, 0x0D})
	if err != nil {
		return fmt.Errorf("error sending enter keystroke to PTY: %w", err)
	}
	return nil
}

// SendKeys writes the given string to the tmux PTY as raw bytes. Unlike
// SendKeysRaw, callers pass a Go string rather than a byte slice; no
// escaping or translation is performed.
func (t *TmuxSession) SendKeys(keys string) error {
	ptmx := t.currentPtmx()
	if ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	_, err := ptmx.Write([]byte(keys))
	return err
}

// SendKeysRaw writes raw bytes directly to the tmux PTY.
func (t *TmuxSession) SendKeysRaw(b []byte) error {
	ptmx := t.currentPtmx()
	if ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	_, err := ptmx.Write(b)
	return err
}

// HasUpdated checks if the tmux pane content has changed since the last tick. It also returns true if
// the tmux pane has a prompt for aider or claude code.
func (t *TmuxSession) HasUpdated() (updated bool, hasPrompt bool) {
	content, err := t.CapturePaneContent()
	if err != nil {
		log.For("tmux").Error("capture_pane_failed", "context", "status_monitor", "session", t.sanitizedName, "err", err)
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

	if t.processContentHash(content) {
		return true, hasPrompt
	}
	return false, hasPrompt
}

// CaptureAndProcess captures pane content once and runs both trust prompt
// and update detection checks, avoiding duplicate CapturePaneContent calls.
// Returns a non-nil err when the pane capture itself failed — callers must
// surface this instead of treating zero values as "no change", which used
// to hide tmux failures as a frozen UI.
func (t *TmuxSession) CaptureAndProcess() (content string, updated bool, hasPrompt bool, trustHandled bool, err error) {
	content, err = t.CapturePaneContent()
	if err != nil {
		return "", false, false, false, fmt.Errorf("capture pane content: %w", err)
	}

	// Trust prompt detection (from CheckAndHandleTrustPrompt).
	if strings.HasSuffix(t.program, ProgramClaude) {
		if strings.Contains(content, "Do you trust the files in this folder?") ||
			strings.Contains(content, "new MCP server") {
			if err := t.TapEnter(); err != nil {
				log.For("tmux").Error("trust_prompt.tap_enter_failed", "prompt", "claude_trust_or_mcp", "err", err)
			}
			trustHandled = true
		}
	} else {
		if strings.Contains(content, "Open documentation url for more info") {
			if err := t.TapDAndEnter(); err != nil {
				log.For("tmux").Error("trust_prompt.tap_enter_failed", "prompt", "other_trust", "err", err)
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

	updated = t.processContentHash(content)

	return content, updated, hasPrompt, trustHandled, nil
}

// GetContentHash returns the last computed content hash from HasUpdated
// or CaptureAndProcess. Returns nil if no hash has been computed yet.
func (t *TmuxSession) GetContentHash() []byte {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
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
	// Snapshot-and-clear in a single critical section (as Close does) so a
	// concurrent Restore can never have its freshly-installed ptmx clobbered
	// by a second nil-write here. On a Close error the handle stays cleared;
	// ResumePreview/Restore reopens a fresh PTY regardless.
	t.stateMu.Lock()
	ptmx := t.ptmx
	t.ptmx = nil
	emu := t.emu
	t.emu = nil
	t.stateMu.Unlock()
	var closeErr error
	if ptmx != nil {
		t.signalPumpStop(ptmx)
		if err := ptmx.Close(); err != nil {
			closeErr = fmt.Errorf("error closing preview PTY: %w", err)
		}
	}
	t.waitPumpExit()
	if emu != nil {
		_ = emu.Close()
	}
	return closeErr
}

// ResumePreview reopens the detached preview PTY after a full-screen attach
// returns control. It is a thin wrapper around Restore kept as a named method
// for clarity at the call sites in app.Update.
func (t *TmuxSession) ResumePreview() error {
	return t.Restore()
}

// Close terminates the tmux session and cleans up resources
func (t *TmuxSession) Close() error {
	log.For("tmux").Debug("close", "session", t.sanitizedName)
	var errs []error

	t.stateMu.Lock()
	ptmx := t.ptmx
	t.ptmx = nil
	emu := t.emu
	t.emu = nil
	t.stateMu.Unlock()
	if ptmx != nil {
		t.signalPumpStop(ptmx)
		if err := ptmx.Close(); err != nil {
			errs = append(errs, fmt.Errorf("error closing PTY: %w", err))
		}
	}

	// Wait for pump goroutine to exit after PTY close.
	t.waitPumpExit()
	// Close the emulator after the pump has exited (no write-after-close).
	if emu != nil {
		_ = emu.Close()
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
	// Record geometry and resize the emulator first so its grid matches the
	// new pane before tmux repaints into the resized PTY. In-memory, cheap.
	t.stateMu.Lock()
	t.lastCols = width
	t.lastRows = height
	emu := t.emu
	t.stateMu.Unlock()
	if emu != nil {
		emu.Resize(width, height)
	}
	return t.updateWindowSize(width, height)
}

// updateWindowSize updates the window size of the PTY.
func (t *TmuxSession) updateWindowSize(cols, rows int) error {
	ptmx := t.currentPtmx()
	if ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	return pty.Setsize(ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
		X:    0,
		Y:    0,
	})
}

// DoesSessionExist reports whether the backing tmux session is still
// alive on the tmux server. Used as a sanity check before attach and
// for orphan detection during reconcile.
func (t *TmuxSession) DoesSessionExist() bool {
	// Using "-t name" does a prefix match, which is wrong. `-t=` does an exact match.
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	existsCmd := exec.CommandContext(ctx, "tmux", "has-session", fmt.Sprintf("-t=%s", t.sanitizedName))
	return t.cmdExec.Run(existsCmd) == nil
}

// RenderEmulator returns the current visible screen from the in-process
// emulator as an ANSI-styled string, or ("", false) if no emulator is wired
// (callers then fall back to CapturePaneContent).
func (t *TmuxSession) RenderEmulator() (string, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return "", false
	}
	return emu.Render(), true
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

// CaptureHistory returns the full pane buffer — scrollback history plus the
// visible screen — as physical rows with ANSI escapes, via capture-pane -S -.
// Returns ("", false) on error. This is tmux's AUTHORITATIVE scrollback: the
// in-process emulator only ever sees the visible screen from the tmux client
// stream (tmux paints clients with redraws, not scroll-through history), so the
// windowed scroll-back must be sourced here rather than from emu.Scrollback().
func (t *TmuxSession) CaptureHistory() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-p", "-e", "-S", "-", "-E", "-", "-t", t.sanitizedName)
	output, err := t.cmdExec.Output(cmd)
	if err != nil {
		return "", false
	}
	return string(output), true
}

// IsAlternateScreen reports whether the pane's foreground app is on the
// alternate screen (a full-screen TUI like Claude), which keeps NO tmux
// scrollback. Callers use this to decide whether scroll-back can be windowed
// from CaptureHistory or must instead be forwarded into the app itself.
func (t *TmuxSession) IsAlternateScreen() bool {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", t.sanitizedName, "#{alternate_on}")
	out, err := t.cmdExec.Output(cmd)
	if err != nil {
		return false
	}
	return len(out) > 0 && out[0] == '1'
}

// ForwardWheel injects n mouse-wheel events (up or down) into the pane's
// foreground app via `tmux send-keys -l`, so a full-screen TUI agent scrolls its
// OWN view — the alternate screen has no tmux scrollback to window. The bytes go
// straight to the app (bypassing tmux's own mouse handling) using SGR mouse
// encoding at the pane's top-left; mouse-aware apps decode them as wheel input.
func (t *TmuxSession) ForwardWheel(up bool, n int) error {
	if n < 1 {
		n = 1
	}
	button := 65 // SGR wheel down
	if up {
		button = 64 // SGR wheel up
	}
	seq := strings.Repeat(fmt.Sprintf("\x1b[<%d;1;1M", button), n)
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "send-keys", "-t", t.sanitizedName, "-l", seq)
	if err := t.cmdExec.Run(cmd); err != nil {
		return fmt.Errorf("forward wheel: %w", err)
	}
	return nil
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
		log.For("tmux").Info("orphan_cleanup", "session", match)
		killCtx, killCancel := context.WithTimeout(context.Background(), tmuxTimeout)
		if err := cmdExec.Run(exec.CommandContext(killCtx, "tmux", "kill-session", "-t", match)); err != nil {
			killCancel()
			return fmt.Errorf("failed to kill tmux session %s: %v", match, err)
		}
		killCancel()
	}
	return nil
}

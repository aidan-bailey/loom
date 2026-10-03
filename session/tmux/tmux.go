package tmux

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/agent"
	"github.com/aidan-bailey/loom/session/vt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/creack/pty"
)

// ProgramClaude, ProgramAider, and ProgramGemini are the canonical
// program identifiers for the built-in agents. They match the literal
// command names (no path or flags). Per-program behavior — trust-prompt
// and pending-prompt detection — is resolved through the session/agent
// registry, which matches on the basename of the program's first token
// so paths and launch flags don't defeat the lookup.
const (
	ProgramClaude = "claude"
	ProgramAider  = "aider"
	ProgramGemini = "gemini"
)

// adapterRegistry resolves a program string to its agent adapter once
// per Session. Shared and read-only after init.
var adapterRegistry = agent.DefaultRegistry()

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
// Pause and the app tick loop's UpdateDiffStats would wedge every other
// tracked instance. On timeout we log and move on: the leaked goroutine
// is isolated to the dying session rather than the whole app.
const pumpWaitTimeout = 2 * time.Second

// TmuxSession is an attach client of one tmux session. It holds a PTY
// running `tmux attach-session`, an output pump draining it, and (on the
// emulator path) an in-process emulator mirroring its screen, plus the
// embedded Session for the session itself. The TUI renders every pane from
// one. Session lifecycle holds only a Session and never attaches. The zero
// value is not usable: construct via NewTmuxSession or NewAttachClient (or
// their WithDeps variants in tests). Two goroutines touch the ptmx and
// monitor fields: the metadata fan-out (CaptureAndProcess/HasUpdated/
// keystroke injection) and the Update loop's attach lifecycle
// (Restore/PausePreview/Close). Both are therefore guarded by stateMu (see
// its doc). Input written to its PTY (SendKeys, SendKeysRaw, TapEnter,
// Paste, the Forward* methods) is the interactive path, while the promoted
// TypeText, PressKeys and SendPrompt go through tmux commands and need no
// attach.
type TmuxSession struct {
	*Session

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
	// pumpExited is set by the output pump draining ptmx when its read loop
	// ends with no stop requested: the session died, or its tmux client
	// exited. It belongs to that one pump. startOutputPump installs a fresh
	// one for each pump, and Restore, PausePreview and Close drop it with
	// the ptmx, so an old pump that exits late (after waitPumpExit gave up
	// on it) can never mark a newer attach dead. Guarded by stateMu like
	// ptmx. See Attached.
	pumpExited *atomic.Bool
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
	// seedHistory holds pre-attach scrollback rows captured once per
	// Restore (history-only: capture-pane -S - -E -1, so it can never
	// overlap what the emulator mirrors post-attach). Immutable after
	// assignment; guarded by stateMu.
	seedHistory []string

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

// targetSeparators maps the characters tmux's target syntax splits on
// (session:window.pane) to '_'. tmux creates a session named
// "loom_fix:login" literally, but no target can name it exactly:
// "=loom_fix:login" means window "login" of "loom_fix". Such a session
// could not be probed, attached or killed.
var targetSeparators = strings.NewReplacer(":", "_", ".", "_")

// sessionNameSuffix is the part of a session name derived from title:
// whitespace dropped, target separators mapped to '_'.
func sessionNameSuffix(title string) string {
	return targetSeparators.Replace(whiteSpaceRegex.ReplaceAllString(title, ""))
}

// ToLoomTmuxName returns the canonical tmux session name for a given
// instance title under the current prefix. The result never holds ':' or
// '.', so SessionTarget and PaneTarget can always name it exactly.
func ToLoomTmuxName(str string) string {
	return TmuxPrefix + sessionNameSuffix(str)
}

// ToLegacyTmuxName returns the pre-rename tmux session name for a
// given instance title. Used only by RenameLegacySessions at startup;
// no production code path should depend on this name otherwise.
func ToLegacyTmuxName(str string) string {
	return LegacyTmuxPrefix + sessionNameSuffix(str)
}

// terminalSessionPrefix distinguishes a terminal pane's tmux session from
// its instance's agent session, which otherwise share the same title.
const terminalSessionPrefix = "term_"

// TerminalSessionName returns the raw (pre-ToLoomTmuxName) tmux session
// name for the terminal pane belonging to the instance with the given
// title. Centralized here so session.Instance can tear this session down
// by name — without depending on ui.TerminalPane — and ui/terminal.go can
// construct the identical name when creating it.
func TerminalSessionName(title string) string {
	return terminalSessionPrefix + title
}

// RenameLegacySessions renames any tmux sessions matching the legacy
// claudesquad_* prefix to their loom_* equivalent so that in-flight
// sessions from a pre-rename binary continue to be found by reconcile
// on the next startup.
//
// Called from Storage.LoadAndReconcile immediately before that storage's
// per-record reconcile pass — once per storage, so in multi-tab restore
// it runs once per slot over that slot's titles. Idempotent: on later
// launches the legacy sessions are gone and the loop is a no-op.
//
// Rename failures are logged at debug rather than swallowed. The common
// causes (no such legacy session, unreachable tmux server) are harmless,
// but a genuine collision — both claudesquad_<t> and loom_<t> exist, so
// tmux refuses the rename — would otherwise leave the legacy session
// alive and unrenamed, which the orphan sweep (CleanupOrphanedSessions)
// may then kill. The log makes that diagnosable.
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
		cmd := Command(ctx, "rename-session", "-t", SessionTarget(legacy), target)
		if err := cmdExec.Run(cmd); err != nil {
			log.For("tmux").Debug("rename_legacy_session_failed", "legacy", legacy, "target", target, "err", err.Error())
		}
		cancel()
	}
}

// NewTmuxSession constructs a TmuxSession wired to the production PTY
// factory and subprocess executor. The tmux session is NOT created at
// this point — call Start (for a fresh session) or Restore (to attach
// to one that already exists on disk). env, if given, is a set of
// "KEY=VALUE" pairs applied to the tmux session via `new-session -e`.
func NewTmuxSession(name string, program string, env ...string) *TmuxSession {
	return newTmuxSession(name, program, MakePtyFactory(), internalexec.Default{}, env...)
}

// NewTmuxSessionWithDeps is [NewTmuxSession] with injected dependencies
// for tests. Pass a fake [PtyFactory] and [internalexec.Executor] to
// avoid spawning real subprocesses or allocating real PTYs.
func NewTmuxSessionWithDeps(name string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *TmuxSession {
	return newTmuxSession(name, program, ptyFactory, cmdExec, env...)
}

func newTmuxSession(name string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *TmuxSession {
	return newSanitizedTmuxSession(ToLoomTmuxName(name), program, ptyFactory, cmdExec, env...)
}

// newSanitizedTmuxSession is newTmuxSession for a name that has already
// been through ToLoomTmuxName.
func newSanitizedTmuxSession(sanitizedName string, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *TmuxSession {
	return &TmuxSession{
		Session: newSanitizedSession(sanitizedName, program, ptyFactory, cmdExec, env...),
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

// Start launches the session (Session.Start) and attaches this client to
// it. The terminal pane starts its shells this way. Agent sessions are
// different: session.Instance launches one through its own Session, and
// the TUI then attaches a client to it by name (NewAttachClient).
func (t *TmuxSession) Start(workDir string) error {
	if err := t.Session.Start(workDir); err != nil {
		return err
	}
	if err := t.Restore(); err != nil {
		if cleanupErr := t.Close(); cleanupErr != nil {
			err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
		}
		return fmt.Errorf("error restoring tmux session: %w", err)
	}
	return nil
}

// ansiSeqRe matches CSI sequences (SGR colors etc. — statusContent carries
// them on both the emulator and capture-pane -e paths) and OSC sequences,
// so pattern scans see plain text.
var ansiSeqRe = regexp.MustCompile(`\x1b\[[0-9;:?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// normalizeForPatternMatch strips ANSI escapes and ALL whitespace so
// prompt/trust patterns match content the terminal wrapped at the pane
// width — wrapping splits patterns mid-word with a bare newline (plus any
// row padding), which defeats plain substring matching. Patterns are long
// enough that whitespace-free matching stays unambiguous.
func normalizeForPatternMatch(s string) string {
	s = ansiSeqRe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
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
	t.pumpExited = nil
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

	// A session launched by an older loom never got detach-on-destroy
	// (Session.Start sets it); without it, under a global `off`, this
	// client would be switched to another session when this one dies.
	t.setDetachOnDestroy()

	// Seed pre-attach history. Rows that scroll off between this capture
	// and the attach are lost from scroll-back (tiny window, accepted by
	// design) — the alternative, capturing after attach, would duplicate
	// rows the emulator also observes.
	seed, seedOK := t.captureHistoryRowsOnly()
	if !seedOK {
		// Drop any seed from a previous Restore too — stale pre-attach
		// history from an older attach would misalign with what the new
		// emulator observes.
		log.For("tmux").Warn("seed_history_capture_failed", "session", t.sanitizedName)
	}
	t.stateMu.Lock()
	t.seedHistory = seed
	t.stateMu.Unlock()

	// Exact (see SessionTarget): the session may have died since the caller
	// last looked, and a prefix match would attach this preview — and
	// every keystroke, paste and prompt later written to it — to another
	// agent's session. A missing session makes the attach client exit at
	// once; the pump then reports the session dead.
	ptmx, err := t.ptyFactory.Start(Command(context.Background(), "attach-session", "-t", SessionTarget(t.sanitizedName)))
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
	if emu != nil {
		// Bell rides the notifier, NOT the coalescer: bells are rare,
		// discrete signals that must never be swallowed by rate limiting.
		emu.SetBellFunc(func() {
			if f := currentNotifier().Bell; f != nil {
				f(t.sanitizedName)
			}
		})
	}
	t.stateMu.Lock()
	t.ptmx = ptmx
	t.emu = emu
	t.monitor = newStatusMonitor()
	t.stateMu.Unlock()
	t.startOutputPump(ptmx) // defaults pumpDest = alt-screen-filtered emu (Task 6)
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

// PtmxAlive reports whether a PTY handle is currently open. This is distinct
// from DoesSessionExist: the tmux session can be alive on the server while
// this is false — e.g. Restore's reattach failed (attach-session errored
// after the prior ptmx was already cleared) or a full-screen attach has
// PausePreview'd this session and not yet resumed. Callers that want to
// self-heal a dead-ptmx-but-alive-session instance should re-run Restore;
// callers checking during a legitimate PausePreview window must not. It
// stays true after the pump hit EOF, so it says only that there is a
// handle to close; whether the client still reads its session is Attached.
func (t *TmuxSession) PtmxAlive() bool {
	return t.currentPtmx() != nil
}

// Attached reports whether this client is attached and still reading its
// session: a PTY is open (PtmxAlive) and the output pump draining it has
// not stopped on its own. A pump stops on its own when a read fails with
// no stop requested, at EOF once the session or its tmux client is gone.
// PtmxAlive still reads true then, and a client that went by it would
// keep showing a dead session's last screen, even after a new session of
// the same name has started. Whoever decides whether a client is usable
// (the TUI's pane registry) asks this instead; Restore re-attaches a
// client that is not.
func (t *TmuxSession) Attached() bool {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	return t.ptmx != nil && (t.pumpExited == nil || !t.pumpExited.Load())
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
	// This pump's own exit flag (see pumpExited), installed before the
	// goroutine starts so it can never miss its pump's exit.
	exited := new(atomic.Bool)
	t.stateMu.Lock()
	emu := t.emu
	t.pumpExited = exited
	t.stateMu.Unlock()
	var dest io.Writer = io.Discard
	var co *coalescer
	if emu != nil {
		// Strip alt-screen enter/exit before the emulator sees it: a tmux
		// client attach-session stream enters the alt screen immediately and
		// never leaves, which would otherwise make x/vt accumulate
		// scrollback in the unreachable alt-screen buffer (see
		// docs/superpowers/specs/2026-07-15-emulator-scrollback-design.md,
		// Amendment 1). t.emu itself stays the raw emulator — Render/
		// statusContent read it directly, unaware of the filter.
		dest = vt.NewAltScreenFilter(emu)
		// Event-driven pane updates ride the pump: dirty/quiet notifications
		// only exist on the emulator path — snapshot mode stays tick-polled.
		co = newCoalescer(t.sanitizedName)
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
				if co != nil {
					co.stop()
				}
				return
			}
			n, err := ptmx.Read(buf)
			if n > 0 {
				t.pumpMu.Lock()
				dest := t.pumpDest
				t.pumpMu.Unlock()
				_, _ = dest.Write(buf[:n])
				if co != nil {
					co.touch()
				}
			}
			if err != nil {
				// Dead only on a genuine EOF/read failure. Deliberate stops
				// (Close/Restore/PausePreview) cancel ctx first via
				// signalPumpStop, and must not look like a died session.
				unrequested := ctx.Err() == nil
				if co != nil {
					co.stop()
					if unrequested {
						if f := currentNotifier().Dead; f != nil {
							f(t.sanitizedName)
						}
					}
				}
				if unrequested {
					// Marked last, once the Dead notification has been
					// delivered: by the time Attached reads false this pump
					// is about to close pumpDone, so a Restore on the
					// Update goroutine (PaneClients.Ensure) finds it gone
					// at once rather than waiting out pumpWaitTimeout on a
					// pump still blocked in tea.Program.Send, which waits
					// for that same Update. The snapshot path (no
					// coalescer, no Dead event) is marked too.
					exited.Store(true)
				}
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

// statusContent returns the pane content that status detection (update hash,
// pending-prompt, trust-prompt) scans. With an emulator wired it reads the
// in-process visible screen — no subprocess; the capture-pane fallback keeps
// the snapshot/Windows path working. Both sources carry SGR escapes
// (capture-pane is invoked with -e), so downstream pattern scans see the
// same shape of content either way.
func (t *TmuxSession) statusContent() (string, error) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu != nil {
		return emu.Render(), nil
	}
	return t.CapturePaneContent()
}

// HasUpdated checks if the tmux pane content has changed since the last tick. It also returns true if
// the tmux pane has a prompt for aider or claude code.
func (t *TmuxSession) HasUpdated() (updated bool, hasPrompt bool) {
	content, err := t.statusContent()
	if err != nil {
		log.For("tmux").Error("capture_pane_failed", "context", "status_monitor", "session", t.sanitizedName, "err", err)
		return false, false
	}

	hasPrompt = t.pendingPrompt(content)

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
	content, err = t.statusContent()
	if err != nil {
		return "", false, false, false, fmt.Errorf("capture pane content: %w", err)
	}

	trustHandled = t.DismissTrustPrompt(content)
	hasPrompt = t.pendingPrompt(content)
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
	t.pumpExited = nil
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

// Close detaches this client (PTY, output pump, emulator) and then kills
// the session (Session.Close).
func (t *TmuxSession) Close() error {
	var errs []error

	t.stateMu.Lock()
	ptmx := t.ptmx
	t.ptmx = nil
	t.pumpExited = nil
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

	if err := t.Session.Close(); err != nil {
		errs = append(errs, err)
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

// HasEmulator reports whether this session renders through the in-process
// emulator (event-driven path) or the legacy capture-pane snapshot path.
func (t *TmuxSession) HasEmulator() bool {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	return t.emu != nil
}

// SetEmulatorForTest wires an emulator directly, bypassing Restore's attach
// lifecycle. Test-only: the name and doc comment are guardrails, nothing
// about the method enforces test-only use.
func (t *TmuxSession) SetEmulatorForTest(emu vt.Emulator) {
	t.stateMu.Lock()
	t.emu = emu
	t.stateMu.Unlock()
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

// CursorState returns the pane's live cursor (position, visibility, shape,
// blink) from the in-process emulator, or ok=false when no emulator is
// wired (snapshot/Windows path — those panes show no cursor).
func (t *TmuxSession) CursorState() (vt.Cursor, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return vt.Cursor{}, false
	}
	return emu.Cursor(), true
}

// PaneTitle returns the inner app's OSC-set window title, or ok=false when
// no emulator is wired, no title was ever set, or the title is invalid
// UTF-8. The last case guards a known vendored charmbracelet/x/vt parser bug
// that can truncate a title mid multi-byte rune (e.g. Claude Code's "✳ ..."
// status) — better to fall back to the instance title than forward a
// mangled byte sequence into the host terminal's own title escape sequence.
func (t *TmuxSession) PaneTitle() (string, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return "", false
	}
	title := emu.Title()
	if title == "" || !utf8.ValidString(title) {
		return "", false
	}
	return title, true
}

// ForwardFocus writes a focus-in (CSI I) or focus-out (CSI O) event into the
// pane's PTY — but only when the inner app enabled focus reporting (mode
// 1004). No-op (nil) otherwise: apps that never asked must not receive it.
func (t *TmuxSession) ForwardFocus(in bool) error {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil || !emu.FocusReportingEnabled() {
		return nil
	}
	seq := []byte("\x1b[O")
	if in {
		seq = []byte("\x1b[I")
	}
	return t.SendKeysRaw(seq)
}

// SeedHistory returns the pre-attach history rows captured at the last
// Restore. Callers must treat the slice as immutable.
func (t *TmuxSession) SeedHistory() []string {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	return t.seedHistory
}

// ScrollbackLen returns the emulator's scrollback line count; ok=false on
// the snapshot path (no emulator).
func (t *TmuxSession) ScrollbackLen() (int, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return 0, false
	}
	return emu.ScrollbackLen(), true
}

// RenderWindow renders `rows` lines ending `offset` lines above the live
// bottom from the emulator's scrollback + screen; ok=false without an
// emulator.
func (t *TmuxSession) RenderWindow(offset, rows int) (string, bool) {
	t.stateMu.Lock()
	emu := t.emu
	t.stateMu.Unlock()
	if emu == nil {
		return "", false
	}
	return emu.RenderWindow(offset, rows), true
}

// ForwardWheel writes n mouse-wheel events (up or down) into the attach PTY, so
// a full-screen TUI agent scrolls its OWN view — the alternate screen has no
// tmux scrollback to window. This is the same path full-screen attach uses to
// deliver real mouse input to the app: tmux forwards the client's mouse bytes to
// the mouse-aware foreground app. Writing in-process (no `tmux send-keys`
// subprocess per notch) keeps rapid wheel scrolling smooth. All n notches go in
// one write, using SGR mouse encoding at the pane's top-left.
func (t *TmuxSession) ForwardWheel(up bool, n int) error {
	if n < 1 {
		n = 1
	}
	ptmx := t.currentPtmx()
	if ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	button := 65 // SGR wheel down
	if up {
		button = 64 // SGR wheel up
	}
	seq := strings.Repeat(fmt.Sprintf("\x1b[<%d;1;1M", button), n)
	if _, err := ptmx.Write([]byte(seq)); err != nil {
		return fmt.Errorf("forward wheel: %w", err)
	}
	return nil
}

// ForwardMouse writes one SGR mouse event into the attach PTY so a focused TUI
// agent receives a click/drag/release at (col,row), 1-indexed. cb is the SGR
// button code (0=left, +32 = motion/drag, 64/65 = wheel up/down); press=true
// emits the 'M' (press) final byte, false emits 'm' (release).
func (t *TmuxSession) ForwardMouse(cb, col, row int, press bool) error {
	ptmx := t.currentPtmx()
	if ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	if col < 1 {
		col = 1
	}
	if row < 1 {
		row = 1
	}
	final := byte('M')
	if !press {
		final = 'm'
	}
	seq := fmt.Sprintf("\x1b[<%d;%d;%d%c", cb, col, row, final)
	if _, err := ptmx.Write([]byte(seq)); err != nil {
		return fmt.Errorf("forward mouse: %w", err)
	}
	return nil
}

// Paste writes text to the attach PTY wrapped in bracketed-paste markers
// (ESC[200~ … ESC[201~), so the focused agent treats it as a paste rather than
// typed input. No-op for empty text.
func (t *TmuxSession) Paste(text string) error {
	if text == "" {
		return nil
	}
	ptmx := t.currentPtmx()
	if ptmx == nil {
		return fmt.Errorf("PTY is not available")
	}
	if _, err := ptmx.Write([]byte("\x1b[200~" + text + "\x1b[201~")); err != nil {
		return fmt.Errorf("paste: %w", err)
	}
	return nil
}

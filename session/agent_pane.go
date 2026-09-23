package session

import (
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/agent"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/session/vt"
)

// AgentPane is the I/O and probe surface of an instance's agent pane:
// screen and scroll-back reads, keystrokes, paste and forwarded mouse/focus
// events, content/status probes, and tmux/PTY liveness. Every method keeps
// exactly the guard it had as an Instance method (instance started, not
// paused, tmux session attached), so on an instance with no live pane it is
// the same no-op, zero value or error as before.
//
// It is a cheap value wrapper around the *Instance: take one with
// inst.Pane() at the call site rather than storing it. Instance itself keeps
// lifecycle, persistence, diff/git and attention state.
type AgentPane struct{ i *Instance }

// Pane returns the agent pane's I/O and probe surface. It is never nil — a
// value wrapping i — and each of its methods applies its own guard.
func (i *Instance) Pane() AgentPane { return AgentPane{i: i} }

// Preview returns the current visible tmux pane content. Returns empty
// string (not an error) when the instance is not started or paused —
// the live tail is only meaningful for running sessions.
func (p AgentPane) Preview() (string, error) {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", nil
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return "", nil
	}
	// Phase 1: source the live screen from the in-process emulator (in-memory,
	// no subprocess). Fall back to capture-pane when no emulator is wired
	// (Windows / LOOM_PANE_RENDERER=snapshot).
	if s, ok := ts.RenderEmulator(); ok {
		return s, nil
	}
	return ts.CapturePaneContent()
}

// EmulatorScreen returns the current visible screen from the in-process
// VT emulator, or ok=false when the instance is not started, is paused,
// or has no emulator wired (snapshot path / Windows). Unlike Preview it
// performs no subprocess work — no tmux has-session liveness probe and
// no capture-pane fallback — so it is safe to call once per visible
// card per render frame (the rail's card tails). Two deliberate
// trade-offs versus Preview: a just-died session may serve one stale
// screen (the dead-event path handles liveness), and the snapshot path
// yields no tail at all rather than degrading to a per-frame
// capture-pane fork (cards fall back to their status label instead).
func (p AgentPane) EmulatorScreen() (string, bool) {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return "", false
	}
	return ts.RenderEmulator()
}

// CaptureHistory returns the agent pane's full tmux buffer (scrollback +
// visible screen) for windowed scroll-back, or ("",false) if unavailable.
func (p AgentPane) CaptureHistory() (string, bool) {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", false
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return "", false
	}
	return ts.CaptureHistory()
}

// IsAlternateScreen reports whether the agent is a full-screen TUI on the
// alternate screen (no tmux scrollback). Scroll-back for such agents must be
// forwarded into the app via ForwardWheel rather than windowed from history.
func (p AgentPane) IsAlternateScreen() bool {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return false
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return false
	}
	return ts.IsAlternateScreen()
}

// CursorState returns the agent pane's live cursor state, or ok=false when
// the instance has no running emulator-backed session.
func (p AgentPane) CursorState() (vt.Cursor, bool) {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return vt.Cursor{}, false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return vt.Cursor{}, false
	}
	return ts.CursorState()
}

// PaneTitle returns the agent's OSC-set window title, or ok=false.
func (p AgentPane) PaneTitle() (string, bool) {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return "", false
	}
	return ts.PaneTitle()
}

// HasEmulator reports whether this instance's pane renders through the
// in-process emulator (event-driven path).
func (p AgentPane) HasEmulator() bool {
	i := p.i
	ts := i.getTmuxSession()
	if ts == nil {
		return false
	}
	return ts.HasEmulator()
}

// SetPreviewSize resizes the detached tmux pane so capture output
// matches the preview viewport. Returns an error if the instance is
// not running; paused instances have no live pane to resize.
func (p AgentPane) SetPreviewSize(width, height int) error {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return fmt.Errorf("cannot set preview size for instance that has not been started or " +
			"is paused")
	}
	return i.getTmuxSession().SetDetachedSize(width, height)
}

// SendKeys sends keys to the tmux session
func (p AgentPane) SendKeys(keys string) error {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return fmt.Errorf("cannot send keys to instance that has not been started or is paused")
	}
	return i.getTmuxSession().SendKeys(keys)
}

// SendKeysRaw writes raw bytes to the tmux PTY. Used by inline attach mode.
func (p AgentPane) SendKeysRaw(b []byte) error {
	i := p.i
	if !i.isStarted() {
		return fmt.Errorf("instance not started or tmux session not initialized")
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return fmt.Errorf("instance not started or tmux session not initialized")
	}
	return ts.SendKeysRaw(b)
}

// SendPrompt sends a prompt to the tmux session
func (p AgentPane) SendPrompt(prompt string) error {
	i := p.i
	if !i.isStarted() {
		return fmt.Errorf("instance not started")
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return fmt.Errorf("tmux session not initialized")
	}
	if err := ts.SendKeys(prompt); err != nil {
		return fmt.Errorf("error sending keys to tmux session: %w", err)
	}

	// Brief pause to prevent carriage return from being interpreted as newline
	time.Sleep(100 * time.Millisecond)
	if err := ts.TapEnter(); err != nil {
		return fmt.Errorf("error tapping enter: %w", err)
	}

	return nil
}

// TapEnter sends a single Enter keystroke to the tmux session when the
// instance is running. No-op otherwise. Exposed to Lua scripts as
// inst:tap_enter().
func (p AgentPane) TapEnter() {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return
	}
	if err := ts.TapEnter(); err != nil {
		log.For("session").Error("tap_enter_failed", "err", err)
	}
}

// Paste sends text to the agent's pane as a bracketed paste. No-op for
// non-started/paused instances.
func (p AgentPane) Paste(text string) error {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return nil
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return nil
	}
	return ts.Paste(text)
}

// ForwardWheel forwards n mouse-wheel events (up or down) to the agent's pane so
// a TUI agent scrolls its own view. No-op for non-started/paused instances.
func (p AgentPane) ForwardWheel(up bool, n int) error {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return nil
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return nil
	}
	return ts.ForwardWheel(up, n)
}

// ForwardMouse forwards one SGR mouse event (click/drag/release) to the agent's
// pane at (col,row). No-op for non-started/paused instances.
func (p AgentPane) ForwardMouse(cb, col, row int, press bool) error {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return nil
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return nil
	}
	return ts.ForwardMouse(cb, col, row, press)
}

// ForwardFocus forwards a host focus in/out event to the agent pane, gated
// on the app having enabled focus reporting. Errors are logged, not
// returned — focus is best-effort.
func (p AgentPane) ForwardFocus(in bool) {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return
	}
	if err := ts.ForwardFocus(in); err != nil {
		log.For("session").Warn("forward_focus_failed", "instance", i.Title, "err", err)
	}
}

// HasUpdated reports whether the tmux pane content has changed since
// the last call and whether an auto-yes-eligible prompt is currently
// visible. Returns (false, false) for non-started instances.
func (p AgentPane) HasUpdated() (updated bool, hasPrompt bool) {
	i := p.i
	if !i.isStarted() {
		return false, false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return false, false
	}
	return ts.HasUpdated()
}

// GetContentHash returns the content hash of the last captured tmux pane.
func (p AgentPane) GetContentHash() []byte {
	i := p.i
	if !i.isStarted() {
		return nil
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return nil
	}
	return ts.GetContentHash()
}

// CheckAndHandleTrustPrompt detects and dismisses an agent-specific
// trust prompt (e.g., Claude's folder-trust dialog) when the agent
// adapter declares a TrustPromptResponse. Returns true when a prompt
// was detected and dismissed. No-op for unknown programs.
func (p AgentPane) CheckAndHandleTrustPrompt() bool {
	i := p.i
	if !i.isStarted() {
		return false
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return false
	}
	// The adapter registry tells us whether this program has a trust
	// prompt to dismiss; the default fallback returns TrustPromptNone,
	// which short-circuits here so unknown programs get no handling.
	if defaultRegistry.Lookup(i.Program).TrustPromptResponse() == agent.TrustPromptNone {
		return false
	}
	return ts.CheckAndHandleTrustPrompt()
}

// CaptureAndProcessStatus captures tmux pane content once and checks for
// trust prompts and content updates. Avoids duplicate CapturePaneContent calls.
// Returns a non-nil err when the underlying capture failed — previously
// the error was swallowed inside tmux.CaptureAndProcess and surfaced only
// in the log file, so callers saw "no updates" instead of a genuine
// capture failure.
func (p AgentPane) CaptureAndProcessStatus() (updated bool, hasPrompt bool, err error) {
	i := p.i
	if !i.isStarted() {
		return false, false, nil
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return false, false, nil
	}

	// Unknown programs (no adapter match beyond fallback) don't have
	// trust/prompt patterns — skip the combined scan and just check for
	// pane updates. Supported agents flow through CaptureAndProcess,
	// which handles both trust dismissal and prompt detection in one
	// CapturePaneContent call.
	ad := defaultRegistry.Lookup(i.Program)
	if ad.Name() == "default" {
		updated, hasPrompt = ts.HasUpdated()
		return updated, hasPrompt, nil
	}

	_, updated, hasPrompt, _, err = ts.CaptureAndProcess()
	return updated, hasPrompt, err
}

// TmuxAlive returns true if the tmux session is alive. This is a sanity check before attaching.
func (p AgentPane) TmuxAlive() bool {
	i := p.i
	ts := i.getTmuxSession()
	if ts == nil {
		return false
	}
	return ts.DoesSessionExist()
}

// TmuxLiveness reports the session's liveness, distinguishing "tmux
// answered no" from "the probe never got an answer". Callers that change
// an instance's state on a negative must use this rather than TmuxAlive,
// which collapses both cases into false.
func (p AgentPane) TmuxLiveness() tmux.Liveness {
	i := p.i
	ts := i.getTmuxSession()
	if ts == nil {
		return tmux.LivenessDead
	}
	return ts.SessionLiveness()
}

// PtmxAlive reports whether the instance's tmux session currently has an
// attached PTY. TmuxAlive can be true (the session exists on the server)
// while this is false — e.g. a reattach failed after a full-screen attach
// returned — leaving every keystroke/resize on this instance silently fail
// forever with no periodic check ever noticing, since TmuxAlive is the only
// thing the metadata tick otherwise watches. See RepairPtmx.
func (p AgentPane) PtmxAlive() bool {
	i := p.i
	ts := i.getTmuxSession()
	if ts == nil {
		return false
	}
	return ts.PtmxAlive()
}

// RepairPtmx re-attaches this instance's tmux session PTY. Intended for the
// metadata tick to call when it observes TmuxAlive()==true but
// PtmxAlive()==false: the session is healthy but Loom's own handle to it is
// gone, and nothing else will ever retry the attach. A no-op error if there
// is no tmux session at all.
func (p AgentPane) RepairPtmx() error {
	i := p.i
	ts := i.getTmuxSession()
	if ts == nil {
		return fmt.Errorf("no tmux session for %q", i.Title)
	}
	return ts.Restore()
}

// TmuxSessionName returns the sanitized tmux session name backing this
// instance, or "" when no session is attached. Pane events carry this name.
func (p AgentPane) TmuxSessionName() string {
	i := p.i
	ts := i.getTmuxSession()
	if ts == nil {
		return ""
	}
	return ts.SessionName()
}

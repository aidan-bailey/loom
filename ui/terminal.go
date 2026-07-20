package ui

import (
	"fmt"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/session/vt"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
)

var (
	terminalPaneStyle, terminalFooterStyle lipgloss.Style
)

func init() { RegisterThemeHook(rebuildTerminalStyles) }

func rebuildTerminalStyles() {
	terminalPaneStyle = lipgloss.NewStyle().Foreground(Text)
	terminalFooterStyle = lipgloss.NewStyle().Foreground(Highlight)
}

// terminalSession holds a cached tmux session for a specific instance.
type terminalSession struct {
	tmuxSession  *tmux.TmuxSession
	worktreePath string
}

// TerminalPane manages shell tmux sessions in the worktree directory of selected instances.
// Sessions are cached per instance so switching between instances preserves terminal state.
type TerminalPane struct {
	mu            sync.Mutex
	width, height int
	sessions      map[string]*terminalSession // instanceTitle → session
	currentTitle  string                      // currently displayed instance
	content       string
	fallback      bool
	fallbackText  string

	// scroll owns offset/anchoring/wheel/alt-probe state on the emulator
	// path; snapFallback carries the legacy capture-pane windowing state
	// for the no-emulator path. Both guarded by t.mu (ScrollModel is
	// unlocked by design; this pane's mutex is its guard).
	scroll       ScrollModel
	snapFallback snapshotScroll
	// newLinesBelowRender is what String()'s scrolled footer shows; written
	// by UpdateContent on whichever path produced the window.
	newLinesBelowRender int
	// src caches the emulator scroll source resolved in UpdateContent so the
	// per-render ScrollPercent can query it WITHOUT re-resolving the session
	// (which runs a has-session subprocess). Guarded by t.mu; nil on the
	// snapshot / no-session path. Mirrors PreviewPane.src.
	src scrollSource

	// sel is the current mouse selection; displayedPlain holds the plain lines
	// most recently rendered by String(). Both guarded by t.mu.
	sel            selection
	displayedPlain []string
}

// NewTerminalPane constructs a TerminalPane with an empty session cache at the
// live tail. The caller must SetSize before the first render and feed instances
// via UpdateContent.
func NewTerminalPane() *TerminalPane {
	return &TerminalPane{
		sessions: make(map[string]*terminalSession),
	}
}

// SetSize resizes the pane under the internal mutex. The internal tmux
// session owned by TerminalPane is resized lazily on the next tick so
// this call remains cheap.
func (t *TerminalPane) SetSize(width, height int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// A width change re-wraps the emulator/capture buffer, invalidating any
	// line-based scroll anchor, so a resize while scrolled returns to live tail.
	if width != t.width {
		t.scroll.Reset()
		t.snapFallback = snapshotScroll{}
		t.newLinesBelowRender = 0
	}
	t.width = width
	t.height = height
	// Resize all cached sessions so that no session has a stale width. A stale
	// width causes captured lines to be wider than width, which re-wraps when
	// rendered and overflows the pane's height constraint.
	for title, s := range t.sessions {
		if s.tmuxSession == nil {
			continue
		}
		if err := s.tmuxSession.SetDetachedSize(width, height); err != nil {
			log.For("ui").Info("terminal.set_detached_size_failed", "title", title, "err", err)
		}
	}
}

// setFallbackState sets the terminal pane to display a fallback message.
// Caller must hold t.mu.
func (t *TerminalPane) setFallbackState(message string) {
	t.fallback = true
	t.fallbackText = lipgloss.JoinVertical(lipgloss.Center, FallBackText, "", message)
	t.content = ""
}

// currentSessionLocked returns the live cached session for the current
// instance, or nil. Caller must hold t.mu.
func (t *TerminalPane) currentSessionLocked() *tmux.TmuxSession {
	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil || !s.tmuxSession.DoesSessionExist() {
		return nil
	}
	return s.tmuxSession
}

// snapshotScrollByLocked applies a lines-from-bottom delta to the legacy
// capture-pane offset (no-emulator path). It floors at 0, marks the start of a
// gesture when leaving the tail, and clears the new-lines counters at the tail.
// The real top-of-buffer clamp happens in updateContentSnapshotLocked, which
// has the captured line count. Mirrors PreviewPane.snapshotScrollBy. Caller
// must hold t.mu.
func (t *TerminalPane) snapshotScrollByLocked(delta int) {
	off := t.snapFallback.offset + delta
	if off < 0 {
		off = 0
	}
	wasBottom := t.snapFallback.offset == 0
	t.snapFallback.offset = off
	if wasBottom && off > 0 {
		t.snapFallback.starting = true
	}
	if off == 0 {
		t.newLinesBelowRender = 0
		t.snapFallback.lastTotal = 0
	}
}

// UpdateContent captures the terminal pane output. At the live tail it tails
// the live emulator screen (capture-pane fallback when no emulator); when
// scrolled it windows the emulator's scrollback in-process (ScrollModel), or,
// on the no-emulator path, tmux's authoritative history at the current offset.
func (t *TerminalPane) UpdateContent(instance *session.Instance) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if instance == nil {
		t.setFallbackState("Select an instance to open a terminal")
		return nil
	}
	if instance.GetStatus() == session.Paused {
		t.setFallbackState("Session is paused. Resume to use terminal.")
		return nil
	}
	if instance.GetStatus() == session.Recoverable {
		t.setFallbackState("Recoverable session. Press 'r' to recover.")
		return nil
	}
	if !instance.Started() {
		t.setFallbackState("Instance is not started yet.")
		return nil
	}

	// Reset to live tail when the instance changes (currentTitle is still the
	// previous instance until ensureSessionLocked updates it below).
	if instance.Title != t.currentTitle {
		t.scroll.Reset()
		t.snapFallback = snapshotScroll{}
		t.newLinesBelowRender = 0
		t.src = nil
	}

	// Ensure we have a terminal session for this instance.
	if err := t.ensureSessionLocked(instance); err != nil {
		return err
	}

	s := t.currentSessionLocked()
	if s == nil {
		t.setFallbackState("Terminal session not available.")
		return nil
	}

	if !t.scroll.IsScrolling() && t.snapFallback.offset == 0 {
		return t.liveTailLocked(s)
	}

	rows := t.height - 1
	if rows < 1 {
		rows = 1
	}
	// Emulator path: window in-process from the emulator's scrollback.
	// AdvanceAndRender reads only in-process emulator state (no subprocess),
	// so calling it under t.mu is fine; ok=false means no emulator → the
	// legacy capture-pane windowing below.
	w, live, ok := t.scroll.AdvanceAndRender(s, rows)
	if ok {
		t.src = s
		if live {
			return t.liveTailLocked(s)
		}
		t.fallback = false
		t.content = w
		t.newLinesBelowRender = t.scroll.NewLinesBelow()
		return nil
	}
	return t.updateContentSnapshotLocked(s, rows)
}

// liveTailLocked renders the session's live emulator screen (capture-pane
// fallback when no emulator) at the live tail. Caller must hold t.mu.
func (t *TerminalPane) liveTailLocked(s *tmux.TmuxSession) error {
	content, rok := s.RenderEmulator()
	if !rok {
		var err error
		content, err = s.CapturePaneContent()
		if err != nil {
			return fmt.Errorf("terminal pane: failed to capture content: %w", err)
		}
	}
	t.fallback = false
	t.content = content
	t.newLinesBelowRender = 0
	return nil
}

// updateContentSnapshotLocked windows tmux's authoritative capture-pane buffer
// at t.snapFallback.offset — the no-emulator path (snapshot mode / Windows).
// It anchors the view to its content as live output accrues below and, unlike
// the emulator path, snaps back to the live tail when the captured buffer
// SHRINKS (clear-history / alt-screen flip / re-wrap), where the offset anchor
// is meaningless. Caller must hold t.mu on entry and exit; the CaptureHistory
// subprocess runs with the lock RELEASED (never hold t.mu across a tmux
// subprocess).
func (t *TerminalPane) updateContentSnapshotLocked(s *tmux.TmuxSession, rows int) error {
	// Snapshot the displayed identity, capture unlocked, re-validate after
	// re-locking: a stale window for a switched-away instance must not
	// overwrite the new instance's view.
	//
	// CAREFUL: UpdateContent holds t.mu via a deferred Unlock. Between this
	// Unlock and the Lock below the mutex is NOT held, so we must not return
	// here — an early return would let the deferred Unlock double-unlock
	// (fatal: "unlock of unlocked mutex"). A panic in CaptureHistory has the
	// same effect (the deferred Unlock runs during unwind on an unlocked
	// mutex). Keep the unlock / capture / relock trio strictly paired with no
	// return and no panicking call between them beyond the capture itself.
	title := t.currentTitle
	t.mu.Unlock()
	hist, hok := s.CaptureHistory()
	t.mu.Lock()
	if t.currentTitle != title {
		return nil
	}
	if !hok {
		t.snapFallback.offset = 0
		return t.liveTailLocked(s)
	}
	lines := strings.Split(strings.TrimRight(hist, "\n"), "\n")
	total := len(lines)

	switch {
	case t.snapFallback.starting:
		// First tick of this scroll gesture: baseline the new-lines counter.
		t.snapFallback.totalAtScrollStart = total
		t.snapFallback.lastTotal = total
		t.snapFallback.starting = false
	case t.snapFallback.lastTotal > 0 && total < t.snapFallback.lastTotal:
		// Buffer shrank: the anchor is meaningless — snap back to live.
		t.snapFallback = snapshotScroll{}
		t.newLinesBelowRender = 0
		return t.liveTailLocked(s)
	case t.snapFallback.lastTotal > 0 && total > t.snapFallback.lastTotal:
		// New output appended below while scrolled: bump the offset so the
		// content under the cursor stays put.
		t.snapFallback.offset += total - t.snapFallback.lastTotal
	}
	t.snapFallback.lastTotal = total

	maxOff := total - rows
	if maxOff < 0 {
		maxOff = 0
	}
	if t.snapFallback.offset > maxOff {
		t.snapFallback.offset = maxOff
	}
	if t.snapFallback.offset <= 0 {
		t.snapFallback.offset = 0
		return t.liveTailLocked(s)
	}

	t.fallback = false
	t.content = strings.Join(windowLines(lines, t.snapFallback.offset, rows), "\n")
	if newBelow := total - t.snapFallback.totalAtScrollStart; newBelow > 0 {
		t.newLinesBelowRender = newBelow
	} else {
		t.newLinesBelowRender = 0
	}
	return nil
}

// ensureSession creates or reuses a cached terminal tmux session for the given instance.
func (t *TerminalPane) ensureSession(instance *session.Instance) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ensureSessionLocked(instance)
}

// ensureSessionLocked is the lock-free implementation of ensureSession.
// Caller must hold t.mu.
func (t *TerminalPane) ensureSessionLocked(instance *session.Instance) error {
	if instance == nil || !instance.Started() || instance.GetStatus() == session.Paused {
		return nil
	}

	worktreePath := instance.GetWorktreePath()
	if worktreePath == "" {
		return nil
	}

	t.currentTitle = instance.Title

	// Check if we already have a cached session for this instance
	if s, ok := t.sessions[instance.Title]; ok {
		if s.tmuxSession != nil && s.tmuxSession.DoesSessionExist() {
			return nil
		}
		// Session died, remove stale entry and recreate below
		delete(t.sessions, instance.Title)
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	termName := tmux.TerminalSessionName(instance.Title)
	ts := tmux.NewTmuxSession(termName, shell)

	// Check if session already exists (e.g. from a previous run)
	if ts.DoesSessionExist() {
		if err := ts.Restore(); err != nil {
			// Session exists but can't restore, kill it and start fresh
			_ = ts.Close()
			ts = tmux.NewTmuxSession(termName, shell)
			if err := ts.Start(worktreePath); err != nil {
				return fmt.Errorf("terminal pane: failed to start session: %w", err)
			}
		}
	} else {
		if err := ts.Start(worktreePath); err != nil {
			return fmt.Errorf("terminal pane: failed to start session: %w", err)
		}
	}

	t.sessions[instance.Title] = &terminalSession{
		tmuxSession:  ts,
		worktreePath: worktreePath,
	}

	// Set the size
	if t.width > 0 && t.height > 0 {
		if err := ts.SetDetachedSize(t.width, t.height); err != nil {
			log.For("ui").Info("terminal.set_size_failed", "err", err)
		}
	}

	return nil
}

// InjectSessionForTest installs ts as the cached terminal session for the
// given instance title, bypassing ensureSessionLocked's normal lazy-spawn
// path so a test can pin a specific (possibly dead) session in place.
// Test-only: the name and doc comment are guardrails, nothing about the
// method enforces test-only use.
func (t *TerminalPane) InjectSessionForTest(title string, ts *tmux.TmuxSession, worktreePath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessions[title] = &terminalSession{tmuxSession: ts, worktreePath: worktreePath}
	t.currentTitle = title
}

// CurrentTmuxSession returns the cached tmux session for the currently
// displayed instance, or nil if none exists or the session is dead. Intended
// for callers that drive full-screen attach via tea.ExecProcess.
func (t *TerminalPane) CurrentTmuxSession() *tmux.TmuxSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil {
		return nil
	}
	if !s.tmuxSession.DoesSessionExist() {
		return nil
	}
	return s.tmuxSession
}

// ShowingFallback reports whether the pane is displaying fallback text
// instead of live terminal content (no cursor applies there).
func (t *TerminalPane) ShowingFallback() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fallback
}

// CursorState returns the current terminal session's live cursor state, or
// ok=false when no live emulator-backed session is displayed.
func (t *TerminalPane) CursorState() (vt.Cursor, bool) {
	t.mu.Lock()
	s, ok := t.sessions[t.currentTitle]
	t.mu.Unlock()
	if !ok || s.tmuxSession == nil {
		return vt.Cursor{}, false
	}
	return s.tmuxSession.CursorState()
}

// ForwardFocus forwards a host focus in/out event to the current terminal
// session, gated on mode 1004. Best-effort.
func (t *TerminalPane) ForwardFocus(in bool) {
	t.mu.Lock()
	s, ok := t.sessions[t.currentTitle]
	t.mu.Unlock()
	if !ok || s.tmuxSession == nil {
		return
	}
	if err := s.tmuxSession.ForwardFocus(in); err != nil {
		log.For("ui").Info("terminal.forward_focus_failed", "err", err)
	}
}

// SendPrompt sends text followed by Enter to the current terminal session.
func (t *TerminalPane) SendPrompt(text string) error {
	t.mu.Lock()
	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil {
		t.mu.Unlock()
		return fmt.Errorf("no terminal session for %s", t.currentTitle)
	}
	if !s.tmuxSession.DoesSessionExist() {
		t.mu.Unlock()
		return fmt.Errorf("terminal session for %s no longer exists", t.currentTitle)
	}
	ts := s.tmuxSession
	t.mu.Unlock()

	if err := ts.SendKeys(text); err != nil {
		return fmt.Errorf("error sending keys to terminal: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := ts.TapEnter(); err != nil {
		return fmt.Errorf("error sending enter to terminal: %w", err)
	}
	return nil
}

// SendKeysToInstance sends text followed by Enter to the cached terminal
// session for the named instance, regardless of which instance is currently
// displayed. Returns an error if no session is cached for that title or the
// session has died — callers (typically scripts) should surface the error
// rather than silently no-op'ing so the user knows the keystroke didn't land.
func (t *TerminalPane) SendKeysToInstance(title, text string) error {
	t.mu.Lock()
	s, ok := t.sessions[title]
	if !ok || s.tmuxSession == nil {
		t.mu.Unlock()
		return fmt.Errorf("no terminal session for %s", title)
	}
	if !s.tmuxSession.DoesSessionExist() {
		t.mu.Unlock()
		return fmt.Errorf("terminal session for %s no longer exists", title)
	}
	ts := s.tmuxSession
	t.mu.Unlock()

	if err := ts.SendKeys(text); err != nil {
		return fmt.Errorf("error sending keys to terminal: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := ts.TapEnter(); err != nil {
		return fmt.Errorf("error sending enter to terminal: %w", err)
	}
	return nil
}

// SendKeysRaw writes raw bytes to the current terminal tmux PTY.
func (t *TerminalPane) SendKeysRaw(b []byte) error {
	t.mu.Lock()
	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil {
		t.mu.Unlock()
		return fmt.Errorf("no terminal session for %s", t.currentTitle)
	}
	if !s.tmuxSession.DoesSessionExist() {
		t.mu.Unlock()
		return fmt.Errorf("terminal session for %s no longer exists", t.currentTitle)
	}
	ts := s.tmuxSession
	t.mu.Unlock()

	return ts.SendKeysRaw(b)
}

// ForwardMouse forwards one SGR mouse event to the current terminal session.
func (t *TerminalPane) ForwardMouse(cb, col, row int, press bool) error {
	t.mu.Lock()
	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil || !s.tmuxSession.DoesSessionExist() {
		t.mu.Unlock()
		return fmt.Errorf("no terminal session for %s", t.currentTitle)
	}
	ts := s.tmuxSession
	t.mu.Unlock()
	return ts.ForwardMouse(cb, col, row, press)
}

// Paste sends text to the current terminal session as a bracketed paste.
func (t *TerminalPane) Paste(text string) error {
	t.mu.Lock()
	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil || !s.tmuxSession.DoesSessionExist() {
		t.mu.Unlock()
		return fmt.Errorf("no terminal session for %s", t.currentTitle)
	}
	ts := s.tmuxSession
	t.mu.Unlock()
	return ts.Paste(text)
}

// Close kills all cached terminal tmux sessions and cleans up.
func (t *TerminalPane) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for title, s := range t.sessions {
		if s.tmuxSession != nil {
			if err := s.tmuxSession.Close(); err != nil {
				log.For("ui").Info("terminal.close_session_failed", "title", title, "err", err)
			}
		}
	}
	t.sessions = make(map[string]*terminalSession)
	t.currentTitle = ""
	t.content = ""
	t.fallback = false
	t.fallbackText = ""
}

// DetachSessionForInstance removes the cached terminal entry for the given title
// and returns the extracted tmux session so the caller can Close() it off the
// update goroutine. Returns nil if no session was cached. This is pure state
// bookkeeping — no blocking I/O — so it is safe to call from Update.
func (t *TerminalPane) DetachSessionForInstance(title string) *tmux.TmuxSession {
	t.mu.Lock()
	defer t.mu.Unlock()

	var ts *tmux.TmuxSession
	if s, ok := t.sessions[title]; ok {
		ts = s.tmuxSession
		delete(t.sessions, title)
	}
	if t.currentTitle == title {
		t.currentTitle = ""
		t.content = ""
		t.fallback = false
		t.fallbackText = ""
	}
	return ts
}

func (t *TerminalPane) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	width := t.width
	height := t.height

	if width == 0 || height == 0 {
		return strings.Repeat("\n", max(height, 0)) // height may be negative on a tiny terminal
	}

	fallback := t.fallback
	fallbackText := t.fallbackText
	content := t.content

	if fallback {
		// 3 = tab bar height (border + padding + text), 4 = window style frame (top/bottom border + padding)
		availableHeight := height - 3 - 4
		fallbackLines := len(strings.Split(fallbackText, "\n"))
		totalPadding := availableHeight - fallbackLines
		topPadding := 0
		bottomPadding := 0
		if totalPadding > 0 {
			topPadding = totalPadding / 2
			bottomPadding = totalPadding - topPadding
		}

		var lines []string
		if topPadding > 0 {
			lines = append(lines, strings.Repeat("\n", topPadding))
		}
		lines = append(lines, fallbackText)
		if bottomPadding > 0 {
			lines = append(lines, strings.Repeat("\n", bottomPadding))
		}

		return terminalPaneStyle.
			Width(width).
			Align(lipgloss.Center).
			Render(strings.Join(lines, ""))
	}

	// Scrolled: render the windowed history with a jump-to-bottom footer.
	if t.scroll.IsScrolling() || t.snapFallback.offset > 0 {
		wlines := strings.Split(content, "\n")
		display, plain := renderWithSelection(wlines, t.sel)
		t.displayedPlain = plain
		footer := terminalFooterStyle.Render(scrollFooter(t.newLinesBelowRender))
		body := lipgloss.JoinVertical(lipgloss.Left, strings.Join(display, "\n"), footer)
		return terminalPaneStyle.Width(width).Render(body)
	}

	// Live tail: show captured content.
	lines := strings.Split(content, "\n")

	if height > 0 {
		if len(lines) > height {
			lines = lines[len(lines)-height:]
		} else {
			padding := height - len(lines)
			lines = append(lines, make([]string, padding)...)
		}
	}

	display, plain := renderWithSelection(lines, t.sel)
	t.displayedPlain = plain
	contentStr := strings.Join(display, "\n")
	return terminalPaneStyle.Width(width).Render(contentStr)
}

// BeginSelection starts a selection anchored at content (row, col).
func (t *TerminalPane) BeginSelection(row, col int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sel = selection{active: true, anchorRow: row, anchorCol: col, curRow: row, curCol: col}
}

// ExtendSelection moves the active selection's cursor to content (row, col).
func (t *TerminalPane) ExtendSelection(row, col int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.sel.active {
		return
	}
	t.sel.curRow = row
	t.sel.curCol = col
}

// ClearSelection clears any active selection.
func (t *TerminalPane) ClearSelection() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sel = selection{}
}

// SelectedText returns the currently selected text (plain), or "" if none.
func (t *TerminalPane) SelectedText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return extractSelection(t.displayedPlain, t.sel)
}

// emuSourceLocked resolves the current session and whether it is
// emulator-backed. currentSessionLocked runs a has-session probe (the file's
// pervasive convention under t.mu) and ScrollbackLen reads only in-process
// emulator state, so this is safe under the lock. Returns nil when no live
// session is displayed. Caller must hold t.mu.
func (t *TerminalPane) emuSourceLocked() (*tmux.TmuxSession, bool) {
	s := t.currentSessionLocked()
	if s == nil {
		return nil, false
	}
	_, emuOK := s.ScrollbackLen()
	return s, emuOK
}

// routeEmuScroll runs an emulator-path routing op (ScrollUp/Down, PageUp/Down)
// with the alt-screen probe kept OUT of t.mu. When the TTL cache is fresh it
// takes the lock once and runs op; when stale it drops the lock for the
// IsAlternateScreen subprocess, stores the result, then runs op on the
// now-fresh cache so op's internal isAltScreen never re-probes under the lock.
//
// op mutates ScrollModel state and may ForwardWheel — a tiny in-process PTY
// write, bounded to ≤1 per wheelEventsPerNotch alt-screen ticks. Holding t.mu
// across THAT is deliberate and safe: it is not a tmux subprocess, it takes a
// DIFFERENT mutex (the session's stateMu, via currentPtmx), and the only lock
// order taken anywhere is t.mu → stateMu, so there is no ordering inversion.
// This is intentionally unlike snapScroll, which keeps its ForwardWheel
// off-lock only because it must also run the IsAlternateScreen SUBPROCESS on
// the same path.
func (t *TerminalPane) routeEmuScroll(s *tmux.TmuxSession, op func() error) error {
	t.mu.Lock()
	if !t.scroll.NeedsAltProbe() {
		defer t.mu.Unlock()
		return op()
	}
	t.mu.Unlock()
	alt := s.IsAlternateScreen() // tmux subprocess — OUTSIDE t.mu
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scroll.SetAltProbe(alt)
	return op()
}

// snapScroll routes a scroll on the no-emulator path: it probes alt-screen
// OUTSIDE t.mu (a tmux subprocess), forwarding `notches` wheel events to a
// full-screen TUI, or else moving the snapshot window offset by `delta` under
// the lock. up selects the forward direction.
func (t *TerminalPane) snapScroll(s *tmux.TmuxSession, up bool, notches, delta int) error {
	if s.IsAlternateScreen() { // subprocess — OUTSIDE t.mu
		return s.ForwardWheel(up, notches)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snapshotScrollByLocked(delta)
	return nil
}

// ScrollUp scrolls one line up into history (or forwards a damped wheel-up to a
// TUI agent on the alternate screen).
func (t *TerminalPane) ScrollUp() error {
	t.mu.Lock()
	s, emuOK := t.emuSourceLocked()
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	if emuOK {
		return t.routeEmuScroll(s, func() error { return t.scroll.ScrollUp(s) })
	}
	return t.snapScroll(s, true, 1, +1)
}

// ScrollDown scrolls one line down toward the live tail (or forwards a damped
// wheel-down to a TUI agent).
func (t *TerminalPane) ScrollDown() error {
	t.mu.Lock()
	s, emuOK := t.emuSourceLocked()
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	if emuOK {
		return t.routeEmuScroll(s, func() error { return t.scroll.ScrollDown(s) })
	}
	return t.snapScroll(s, false, 1, -1)
}

// PageUp scrolls up by half a pane height (or forwards a burst of wheel-ups).
func (t *TerminalPane) PageUp() error {
	t.mu.Lock()
	s, emuOK := t.emuSourceLocked()
	half := t.height / 2
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	if emuOK {
		return t.routeEmuScroll(s, func() error { return t.scroll.PageUp(s, t.height) })
	}
	return t.snapScroll(s, true, agentPageNotches, +half)
}

// PageDown scrolls down by half a pane height (or forwards a burst of wheel-downs).
func (t *TerminalPane) PageDown() error {
	t.mu.Lock()
	s, emuOK := t.emuSourceLocked()
	half := t.height / 2
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	if emuOK {
		return t.routeEmuScroll(s, func() error { return t.scroll.PageDown(s, t.height) })
	}
	return t.snapScroll(s, false, agentPageNotches, -half)
}

// GotoTop jumps to the oldest line of history (TUI: a large wheel-up burst).
func (t *TerminalPane) GotoTop() error {
	t.mu.Lock()
	s, emuOK := t.emuSourceLocked()
	if s == nil {
		t.mu.Unlock()
		return nil
	}
	if emuOK {
		t.scroll.GotoTop(s) // pure window move, no alt probe (mirrors PreviewPane)
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()
	if s.IsAlternateScreen() { // subprocess — OUTSIDE t.mu
		return s.ForwardWheel(true, 30)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snapshotScrollByLocked(scrollToTopOffset)
	return nil
}

// GotoBottom returns to the live tail on both paths.
func (t *TerminalPane) GotoBottom() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scroll.Reset()
	t.snapFallback = snapshotScroll{}
	t.newLinesBelowRender = 0
}

// ScrollPercent returns the scroll position as a fraction [0, 1]; 1.0 == live
// tail (bottom).
func (t *TerminalPane) ScrollPercent() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Use the cached src (no per-render session resolution / has-session
	// subprocess); it is set only on the emulator path. in-process reads only.
	if t.scroll.IsScrolling() && t.src != nil {
		return t.scroll.ScrollPercent(t.src)
	}
	if t.snapFallback.offset <= 0 || t.snapFallback.lastTotal <= 0 {
		return 1.0
	}
	return 1.0 - float64(t.snapFallback.offset)/float64(t.snapFallback.lastTotal)
}

// ResetToNormalMode returns the pane to the live tail on both paths.
func (t *TerminalPane) ResetToNormalMode() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scroll.Reset()
	t.snapFallback = snapshotScroll{}
	t.newLinesBelowRender = 0
}

// IsScrolling reports whether the pane is scrolled away from the live tail.
func (t *TerminalPane) IsScrolling() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scroll.IsScrolling() || t.snapFallback.offset > 0
}

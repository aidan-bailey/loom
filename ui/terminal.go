package ui

import (
	"fmt"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

var terminalPaneStyle = lipgloss.NewStyle().
	Foreground(compat.AdaptiveColor{Light: lipgloss.Color("#1a1a1a"), Dark: lipgloss.Color("#dddddd")})

var terminalFooterStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#FFD700"))

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

	// scrollOffset is lines-from-bottom into the captured history buffer; 0 =
	// live tail. All scroll state is guarded by t.mu.
	scrollOffset       int
	scrollStarting     bool
	totalAtScrollStart int
	lastTotal          int
	newLinesBelow      int
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

// setOffsetLocked floors a new lines-from-bottom offset at 0 and marks the start
// of a scroll gesture. The real top-of-buffer clamp happens in UpdateContent,
// which has the captured line count. Caller must hold t.mu.
func (t *TerminalPane) setOffsetLocked(off int) {
	if off < 0 {
		off = 0
	}
	wasBottom := t.scrollOffset == 0
	t.scrollOffset = off
	if wasBottom && off > 0 {
		t.scrollStarting = true
	}
	if off == 0 {
		t.newLinesBelow = 0
		t.lastTotal = 0
	}
}

// UpdateContent captures the terminal pane output. At scrollOffset 0 it tails
// the live emulator screen (capture-pane fallback when no emulator); when
// scrolled it paints a window of the session's scrollback at the offset.
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
	if !instance.Started() {
		t.setFallbackState("Instance is not started yet.")
		return nil
	}

	// Reset to live tail when the instance changes (currentTitle is still the
	// previous instance until ensureSessionLocked updates it below).
	if instance.Title != t.currentTitle {
		t.scrollOffset = 0
		t.newLinesBelow = 0
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

	if t.scrollOffset == 0 {
		// Live tail: emulator visible screen; fall back to capture-pane when no
		// emulator is wired (Windows / LOOM_PANE_RENDERER=snapshot).
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
		t.newLinesBelow = 0
		return nil
	}

	// Scrolled: window into tmux's authoritative history (capture-pane -S -),
	// anchoring the view to its content as live output accrues below.
	hist, hok := s.CaptureHistory()
	if !hok {
		t.scrollOffset = 0
		content, _ := s.CapturePaneContent()
		t.fallback = false
		t.content = content
		t.newLinesBelow = 0
		return nil
	}
	lines := strings.Split(strings.TrimRight(hist, "\n"), "\n")
	total := len(lines)
	rows := t.height - 1
	if rows < 1 {
		rows = 1
	}

	switch {
	case t.scrollStarting:
		t.totalAtScrollStart = total
		t.lastTotal = total
		t.scrollStarting = false
	case t.lastTotal > 0 && total > t.lastTotal:
		t.scrollOffset += total - t.lastTotal
	}
	t.lastTotal = total

	maxOff := total - rows
	if maxOff < 0 {
		maxOff = 0
	}
	if t.scrollOffset > maxOff {
		t.scrollOffset = maxOff
	}
	if t.scrollOffset <= 0 {
		t.scrollOffset = 0
		content, rok := s.RenderEmulator()
		if !rok {
			content, _ = s.CapturePaneContent()
		}
		t.fallback = false
		t.content = content
		t.newLinesBelow = 0
		return nil
	}

	t.fallback = false
	t.content = strings.Join(windowLines(lines, t.scrollOffset, rows), "\n")
	if newBelow := total - t.totalAtScrollStart; newBelow > 0 {
		t.newLinesBelow = newBelow
	} else {
		t.newLinesBelow = 0
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

	termName := "term_" + instance.Title
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
		return strings.Repeat("\n", height)
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
	if t.scrollOffset > 0 {
		footer := terminalFooterStyle.Render(scrollFooter(t.newLinesBelow))
		body := lipgloss.JoinVertical(lipgloss.Left, content, footer)
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

	contentStr := strings.Join(lines, "\n")
	return terminalPaneStyle.Width(width).Render(contentStr)
}

// ScrollUp scrolls one line up into scrollback.
func (t *TerminalPane) ScrollUp() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setOffsetLocked(t.scrollOffset + 1)
	return nil
}

// ScrollDown scrolls one line down toward the live tail.
func (t *TerminalPane) ScrollDown() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setOffsetLocked(t.scrollOffset - 1)
	return nil
}

// PageUp scrolls up by half a pane height.
func (t *TerminalPane) PageUp() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setOffsetLocked(t.scrollOffset + t.height/2)
	return nil
}

// PageDown scrolls down by half a pane height.
func (t *TerminalPane) PageDown() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setOffsetLocked(t.scrollOffset - t.height/2)
	return nil
}

// GotoTop jumps to the oldest scrollback line.
func (t *TerminalPane) GotoTop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setOffsetLocked(scrollToTopOffset)
	return nil
}

// GotoBottom returns to the live tail.
func (t *TerminalPane) GotoBottom() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setOffsetLocked(0)
}

// ScrollPercent returns the scroll position as a fraction [0, 1]; 1.0 == live
// tail (bottom).
func (t *TerminalPane) ScrollPercent() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.scrollOffset <= 0 || t.lastTotal <= 0 {
		return 1.0
	}
	return 1.0 - float64(t.scrollOffset)/float64(t.lastTotal)
}

// ResetToNormalMode returns the pane to the live tail.
func (t *TerminalPane) ResetToNormalMode() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setOffsetLocked(0)
}

// IsScrolling reports whether the pane is scrolled away from the live tail.
func (t *TerminalPane) IsScrolling() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrollOffset > 0
}

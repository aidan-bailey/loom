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

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

var terminalPaneStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

var terminalFooterStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#808080", Dark: "#808080"})

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

	isScrolling bool
	viewport    viewport.Model
}

// NewTerminalPane constructs a TerminalPane with an empty session
// cache and zero-sized viewport. The caller must SetSize before the
// first render and feed instances via UpdateContent.
func NewTerminalPane() *TerminalPane {
	return &TerminalPane{
		sessions: make(map[string]*terminalSession),
		viewport: viewport.New(0, 0),
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
	t.viewport.Width = width
	t.viewport.Height = height
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

// UpdateContent captures the tmux pane output for the terminal session.
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

	// Reset scroll mode when the instance changes or viewport is at the bottom.
	if t.isScrolling {
		if instance.Title != t.currentTitle || t.viewport.AtBottom() {
			t.isScrolling = false
			t.viewport.SetContent("")
			t.viewport.GotoTop()
		}
	}

	// Skip content updates while in scroll mode
	if t.isScrolling {
		return nil
	}

	// Ensure we have a terminal session for this instance
	if err := t.ensureSessionLocked(instance); err != nil {
		return err
	}

	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil || !s.tmuxSession.DoesSessionExist() {
		t.setFallbackState("Terminal session not available.")
		return nil
	}

	content, err := s.tmuxSession.CapturePaneContent()
	if err != nil {
		return fmt.Errorf("terminal pane: failed to capture content: %w", err)
	}

	t.fallback = false
	t.content = content
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

	if t.isScrolling {
		return t.viewport.View()
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

	// Normal mode: show captured content
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

// enterScrollMode captures the full terminal history and seeds the viewport.
// Callers must apply a motion (LineUp, HalfViewUp, etc.) after to keep
// AtBottom() false — otherwise the next UpdateContent auto-exits.
// Caller must hold t.mu.
func (t *TerminalPane) enterScrollMode() error {
	s, ok := t.sessions[t.currentTitle]
	if !ok || s.tmuxSession == nil || !s.tmuxSession.DoesSessionExist() {
		return nil
	}

	content, err := s.tmuxSession.CapturePaneContentWithOptions("-", "-")
	if err != nil {
		return fmt.Errorf("terminal pane: failed to capture full history: %w", err)
	}

	footer := terminalFooterStyle.Render("ESC to exit scroll mode")
	t.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, content, footer))
	t.viewport.GotoBottom()
	t.isScrolling = true
	return nil
}

// ScrollUp enters scroll mode (if not already) and scrolls up.
func (t *TerminalPane) ScrollUp() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		if err := t.enterScrollMode(); err != nil {
			return err
		}
	}
	t.viewport.LineUp(1)
	return nil
}

// ScrollDown scrolls down in the viewport. Does not enter scroll mode from normal mode.
func (t *TerminalPane) ScrollDown() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		return nil
	}
	t.viewport.LineDown(1)
	return nil
}

// PageUp scrolls up by half a viewport height.
func (t *TerminalPane) PageUp() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		if err := t.enterScrollMode(); err != nil {
			return err
		}
	}
	t.viewport.HalfViewUp()
	return nil
}

// PageDown scrolls down by half a viewport height.
func (t *TerminalPane) PageDown() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		return nil
	}
	t.viewport.HalfViewDown()
	return nil
}

// GotoTop jumps the viewport to the start of captured history.
func (t *TerminalPane) GotoTop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		if err := t.enterScrollMode(); err != nil {
			return err
		}
	}
	t.viewport.GotoTop()
	return nil
}

// GotoBottom exits scroll mode and returns to live tail.
func (t *TerminalPane) GotoBottom() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		return
	}
	t.isScrolling = false
	t.viewport.SetContent("")
	t.viewport.GotoTop()
}

// ScrollPercent returns the viewport position as a fraction [0, 1].
// Returns 1.0 when not in scroll mode (live tail is "at the bottom").
func (t *TerminalPane) ScrollPercent() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		return 1.0
	}
	return t.viewport.ScrollPercent()
}

// ResetToNormalMode exits scroll mode and restores normal content display.
func (t *TerminalPane) ResetToNormalMode() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isScrolling {
		return
	}
	t.isScrolling = false
	t.viewport.SetContent("")
	t.viewport.GotoTop()
}

// IsScrolling returns whether the terminal pane is in scroll mode.
func (t *TerminalPane) IsScrolling() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.isScrolling
}

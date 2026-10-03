package ui

import (
	"errors"
	"fmt"
	"sync"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/session/vt"
)

// PaneClients holds the TUI's attach clients: one *tmux.TmuxSession per
// live agent tmux session, keyed by the session's name. Each client is
// attached by name rather than owned by the session's instance. Every
// pane render, scroll, cursor, mouse, paste and key forward goes through
// a client here (Pane), and so does the status scrape that reads a pane's
// screen. That is why session lifecycle never needs a client of its own.
//
// Safe for concurrent use: Get and For may be called from Cmd goroutines.
// Ensure and Replace attach (a PTY spawn and a capture-pane), and they run
// on the Update goroutine as the attach paths before them did. A client
// that Replace or Retain hands back must be closed off the Update
// goroutine: PausePreview waits for the client's output pump, which blocks
// in tea.Program.Send until Update returns. Every method is nil-receiver
// safe; a nil registry holds no clients.
type PaneClients struct {
	mu      sync.Mutex
	clients map[string]*tmux.TmuxSession
	// cols, rows is the agent pane size new clients attach at
	// (SetDefaultSize); 0 before the first layout.
	cols, rows int
	// newClient builds an unattached client for a session name.
	newClient func(sessionName, program string) *tmux.TmuxSession
}

// NewPaneClients returns an empty registry whose clients attach through
// the production PTY factory and executor.
func NewPaneClients() *PaneClients {
	return &PaneClients{
		clients:   make(map[string]*tmux.TmuxSession),
		newClient: tmux.NewAttachClient,
	}
}

// SetClientFactoryForTest replaces how new clients are built, so that a
// test's Ensure/Replace attach through a fake PTY. Test-only: the name and
// doc comment are guardrails; nothing about the method enforces test-only
// use.
func (p *PaneClients) SetClientFactoryForTest(f func(sessionName, program string) *tmux.TmuxSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.newClient = f
}

// InjectForTest registers c as sessionName's client, as Ensure would have.
// Test-only, like SetClientFactoryForTest.
func (p *PaneClients) InjectForTest(sessionName string, c *tmux.TmuxSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[sessionName] = c
}

// Get returns sessionName's client, or nil.
func (p *PaneClients) Get(sessionName string) *tmux.TmuxSession {
	if p == nil || sessionName == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clients[sessionName]
}

// Alive reports whether sessionName has a client with an open PTY.
func (p *PaneClients) Alive(sessionName string) bool {
	c := p.Get(sessionName)
	return c != nil && c.PtmxAlive()
}

// For returns inst's pane: its session's client here when inst is started
// and not paused, else the zero Pane. Until the instance stops attaching
// its own client (daemon stage 1A, Package C), a session with nothing
// registered falls back to that client, so every pane renders exactly as
// before.
func (p *PaneClients) For(inst *session.Instance) Pane {
	if inst == nil || !inst.Started() || inst.Paused() {
		return Pane{}
	}
	if c := p.Get(inst.Pane().TmuxSessionName()); c != nil {
		return Pane{c: c}
	}
	return Pane{c: inst.TmuxSession()}
}

// SetDefaultSize records the agent pane size that clients attach at.
func (p *PaneClients) SetDefaultSize(cols, rows int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cols, p.rows = cols, rows
}

// Ensure gives sessionName a client attached at the default size. If the
// session has no client, Ensure builds and attaches one. If its client's
// PTY is gone (a failed attach, or one paused for a full-screen attach),
// Ensure re-attaches it. A client with an open PTY is left alone. program
// selects a new client's agent adapter for its status scan. The client
// stays registered even when attaching fails, so a later Ensure retries
// it. Update goroutine only.
func (p *PaneClients) Ensure(sessionName, program string) error {
	if p == nil || sessionName == "" {
		return nil
	}
	p.mu.Lock()
	c := p.clients[sessionName]
	if c == nil {
		c = p.newClient(sessionName, program)
		p.clients[sessionName] = c
	}
	cols, rows := p.cols, p.rows
	p.mu.Unlock()
	if c.PtmxAlive() {
		return nil
	}
	sized := cols > 0 && rows > 0
	if sized {
		// No PTY yet, so this only records the geometry Restore builds the
		// emulator at. Its "PTY is not available" error is expected.
		_ = c.SetDetachedSize(cols, rows)
	}
	if err := c.Restore(); err != nil {
		return fmt.Errorf("attach to tmux session %s: %w", sessionName, err)
	}
	if sized {
		if err := c.SetDetachedSize(cols, rows); err != nil {
			log.For("ui").Debug("pane.resize_failed", "session", sessionName, "err", err.Error())
		}
	}
	return nil
}

// Replace gives sessionName a fresh client. It is for a session just
// (re)launched under that name, whose previous client was watching the
// session it replaced. The old client, if any, is returned for the caller
// to close off the Update goroutine. Update goroutine only.
func (p *PaneClients) Replace(sessionName, program string) (*tmux.TmuxSession, error) {
	if p == nil || sessionName == "" {
		return nil, nil
	}
	p.mu.Lock()
	old := p.clients[sessionName]
	delete(p.clients, sessionName)
	p.mu.Unlock()
	return old, p.Ensure(sessionName, program)
}

// Retain drops every client whose session name is not in keep and returns
// them for the caller to close off the Update goroutine.
func (p *PaneClients) Retain(keep map[string]bool) []*tmux.TmuxSession {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var dropped []*tmux.TmuxSession
	for name, c := range p.clients {
		if !keep[name] {
			dropped = append(dropped, c)
			delete(p.clients, name)
		}
	}
	return dropped
}

// Pane is one agent pane's display surface. It covers its attach client's
// screen, scroll-back and cursor, input forwarded through the client's
// PTY, and the status scrape that reads the screen. The zero value has no
// client: the instance is not started, is paused, or has none attached
// yet. It answers every read with a zero value and every forward with nil,
// just as the instance's own guards did for an instance with no live pane.
type Pane struct{ c *tmux.TmuxSession }

// errNoClient is what SendKeysRaw returns for a pane with no client, so
// inline attach logs a dropped key rather than losing it silently.
var errNoClient = errors.New("pane has no attach client")

// Client returns the pane's attach client, or nil.
func (p Pane) Client() *tmux.TmuxSession { return p.c }

// Preview returns the pane's visible screen: the emulator's, or
// capture-pane's on the snapshot path. It is empty, with no error, when
// there is no live session.
func (p Pane) Preview() (string, error) {
	if p.c == nil || !p.c.DoesSessionExist() {
		return "", nil
	}
	if s, ok := p.c.RenderEmulator(); ok {
		return s, nil
	}
	return p.c.CapturePaneContent()
}

// EmulatorScreen returns the emulator's visible screen without running a
// subprocess, which makes it cheap enough to call for every visible card
// each frame. ok is false without an emulator (snapshot path) or a client.
func (p Pane) EmulatorScreen() (string, bool) {
	if p.c == nil {
		return "", false
	}
	return p.c.RenderEmulator()
}

// CaptureHistory returns the pane's tmux buffer (scroll-back plus screen)
// for windowing on the snapshot path, or ("", false).
func (p Pane) CaptureHistory() (string, bool) {
	if p.c == nil || !p.c.DoesSessionExist() {
		return "", false
	}
	return p.c.CaptureHistory()
}

// IsAlternateScreen reports whether the agent is a full-screen TUI on the
// alternate screen. Such an agent's scroll-back must be forwarded as wheel
// events.
func (p Pane) IsAlternateScreen() bool {
	if p.c == nil || !p.c.DoesSessionExist() {
		return false
	}
	return p.c.IsAlternateScreen()
}

// CursorState returns the pane's live cursor, or ok=false.
func (p Pane) CursorState() (vt.Cursor, bool) {
	if p.c == nil {
		return vt.Cursor{}, false
	}
	return p.c.CursorState()
}

// PaneTitle returns the window title the agent set via OSC, or ok=false.
func (p Pane) PaneTitle() (string, bool) {
	if p.c == nil {
		return "", false
	}
	return p.c.PaneTitle()
}

// HasEmulator reports whether the pane renders through the in-process
// emulator, the event-driven path.
func (p Pane) HasEmulator() bool { return p.c != nil && p.c.HasEmulator() }

// PtmxAlive reports whether the pane's client has an open PTY.
func (p Pane) PtmxAlive() bool { return p.c != nil && p.c.PtmxAlive() }

// SetPreviewSize resizes the pane's client, and with it the session's
// window. Without a client it does nothing; Ensure attaches at the default
// size.
func (p Pane) SetPreviewSize(width, height int) error {
	if p.c == nil {
		return nil
	}
	return p.c.SetDetachedSize(width, height)
}

// SendKeysRaw writes raw key bytes through the client's PTY. Inline attach
// uses it.
func (p Pane) SendKeysRaw(b []byte) error {
	if p.c == nil {
		return errNoClient
	}
	return p.c.SendKeysRaw(b)
}

// Paste sends text as a bracketed paste. Without a live session it does
// nothing.
func (p Pane) Paste(text string) error {
	if p.c == nil || !p.c.DoesSessionExist() {
		return nil
	}
	return p.c.Paste(text)
}

// ForwardWheel forwards n wheel events so a TUI agent scrolls its own
// view. Without a live session it does nothing.
func (p Pane) ForwardWheel(up bool, n int) error {
	if p.c == nil || !p.c.DoesSessionExist() {
		return nil
	}
	return p.c.ForwardWheel(up, n)
}

// ForwardMouse forwards one SGR mouse event at (col,row). Without a live
// session it does nothing.
func (p Pane) ForwardMouse(cb, col, row int, press bool) error {
	if p.c == nil || !p.c.DoesSessionExist() {
		return nil
	}
	return p.c.ForwardMouse(cb, col, row, press)
}

// ForwardFocus forwards a focus in/out event when the agent has asked for
// them. It is best-effort: a failure is logged.
func (p Pane) ForwardFocus(in bool) {
	if p.c == nil {
		return
	}
	if err := p.c.ForwardFocus(in); err != nil {
		log.For("ui").Warn("forward_focus_failed", "session", p.c.SessionName(), "err", err)
	}
}

// GetContentHash returns the hash of the screen that the last status scan
// saw.
func (p Pane) GetContentHash() []byte {
	if p.c == nil {
		return nil
	}
	return p.c.GetContentHash()
}

// DetectStatus runs one status scan of the pane's screen (see
// tmux.TmuxSession.DetectStatus). Without a client there is no screen to
// scan, so it gives no opinion.
func (p Pane) DetectStatus() (updated, hasPrompt bool, err error) {
	if p.c == nil {
		return false, false, nil
	}
	return p.c.DetectStatus()
}

// scrollSource adapts the pane's client to the scroll state machine. ok
// is false when there is no client.
func (p Pane) scrollSource() (scrollSource, bool) {
	if p.c == nil {
		return nil, false
	}
	return p.c, true
}

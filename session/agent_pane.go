package session

import (
	"fmt"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
)

// AgentPane is what session lifecycle does with an instance's agent pane
// without an attach client: liveness probes, the session name, a
// capture-pane read of the screen, typing text and prompts through
// load-buffer + paste-buffer (tmux.Session.TypeText), and pressing Enter
// through send-keys. Rendering the pane and forwarding input through a
// PTY belong to the TUI's client (ui.Pane), which the instance never
// holds. Each method keeps its guard: started and not paused for the
// screen read and keys (SendPrompt checks only started), and only "has a
// tmux session" for the liveness probes.
//
// It is a cheap value wrapper around the *Instance: take one with
// inst.Pane() at the call site rather than storing it.
type AgentPane struct{ i *Instance }

// Pane returns the agent pane's lifecycle surface. It is never nil; each
// of its methods applies its own guard.
func (i *Instance) Pane() AgentPane { return AgentPane{i: i} }

// Preview returns the pane's visible screen through capture-pane (no
// client needed). It is empty, not an error, when the instance is not
// started, is paused, or its session is gone. Used by Lua's inst:preview().
func (p AgentPane) Preview() (string, error) {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return "", nil
	}
	ts := i.getTmuxSession()
	if ts == nil || !ts.DoesSessionExist() {
		return "", nil
	}
	return ts.CapturePaneContent()
}

// SendKeys types keys, as text, into the agent's tmux session through
// load-buffer + paste-buffer (tmux.Session.TypeText).
func (p AgentPane) SendKeys(keys string) error {
	i := p.i
	if !i.isStarted() || i.GetStatus() == Paused {
		return fmt.Errorf("cannot send keys to instance that has not been started or is paused")
	}
	return i.getTmuxSession().TypeText(keys)
}

// SendPrompt types prompt into the agent's tmux session through
// load-buffer + paste-buffer and submits it with Enter through send-keys
// (tmux.Session.SendPrompt).
func (p AgentPane) SendPrompt(prompt string) error {
	i := p.i
	if !i.isStarted() {
		return fmt.Errorf("instance not started")
	}
	ts := i.getTmuxSession()
	if ts == nil {
		return fmt.Errorf("tmux session not initialized")
	}
	return ts.SendPrompt(prompt)
}

// TapEnter presses Enter in the tmux session when the instance is
// running; otherwise it does nothing. Exposed to Lua scripts as
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
	if err := ts.PressKeys("Enter"); err != nil {
		log.For("session").Error("tap_enter_failed", "err", err)
	}
}

// TmuxAlive reports whether the tmux session is alive: a sanity check
// before acting on the pane.
func (p AgentPane) TmuxAlive() bool {
	ts := p.i.getTmuxSession()
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
	ts := p.i.getTmuxSession()
	if ts == nil {
		return tmux.LivenessDead
	}
	return ts.SessionLiveness()
}

// TmuxSessionName returns the tmux session name backing this instance, or
// "" when it has no session. Pane events and the TUI's pane clients are
// keyed by it.
func (p AgentPane) TmuxSessionName() string {
	ts := p.i.getTmuxSession()
	if ts == nil {
		return ""
	}
	return ts.SessionName()
}

// SessionProgram returns the command line the agent's tmux session was
// launched with ("" without a session). A pane client's status scan must
// use it: after a reattach it can differ from Instance.Program.
func (p AgentPane) SessionProgram() string {
	ts := p.i.getTmuxSession()
	if ts == nil {
		return ""
	}
	return ts.Program()
}

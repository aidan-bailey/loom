package core

import "github.com/aidan-bailey/loom/session"

// Event is something the TUI must apply to its view after a model call.
// The TUI applies them in order (app.applyCoreEvent).
type Event interface{ coreEvent() }

// Notice is a line for the error bar: Err as an error (shown longer the
// longer it is, and logged), or else Info as an info line.
type Notice struct {
	Err  error
	Info string
}

func (Notice) coreEvent() {}

// InstancesChanged reports that instances were added, removed or replaced,
// or that a lifecycle operation moved their status. The TUI repoints its
// panes and menu at the selection (instanceChanged); with Relayout it also
// asks for the window size again (tea.RequestWindowSize), as the old
// completion did.
type InstancesChanged struct{ Relayout bool }

func (InstancesChanged) coreEvent() {}

// ClientsStale reports that instances stopped being active (killed,
// paused, reverted): the TUI releases the pane clients no active instance
// needs (prunePanes).
type ClientsStale struct{}

func (ClientsStale) coreEvent() {}

// SessionLaunched reports that Instance's tmux session was (re)launched
// or reattached while a loaded workspace holds it, so any pane client
// from before watched the session it replaced: the TUI gives it a fresh
// one (replacePane).
type SessionLaunched struct{ Instance *session.Instance }

func (SessionLaunched) coreEvent() {}

// Reactivated reports that a failed operation put Instance back to its
// previous status. A tick may have pruned its client while it was
// Deleting or Loading, so the TUI ensures one (ensurePane, a no-op unless
// the instance is active).
type Reactivated struct{ Instance *session.Instance }

func (Reactivated) coreEvent() {}

// Started reports a start that succeeded. Owner is the workspace that
// holds the instance (nil when unknown), Loaded whether it is still
// loaded. The TUI attaches its client when Loaded, and selects it or says
// where it started.
type Started struct {
	Instance *session.Instance
	Owner    *Workspace
	Loaded   bool
}

func (Started) coreEvent() {}

// Recovered reports an orphan adopted into its placeholder's row. Owner
// and Loaded are as for Started.
type Recovered struct {
	Instance *session.Instance
	Owner    *Workspace
	Loaded   bool
}

func (Recovered) coreEvent() {}

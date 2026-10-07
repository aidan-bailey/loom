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

// StatusesChanged reports that instance statuses may have moved (a probe,
// a hook scan, a roster answer): the TUI refreshes its tab statuses and
// peer sections (updateTabBarStatuses).
type StatusesChanged struct{}

func (StatusesChanged) coreEvent() {}

// Alive lists instances whose tmux session a probe found alive. The TUI
// re-attaches the client of any whose client is not attached (a reattach
// failed after a full-screen attach, or the client's pump hit EOF on a
// session since relaunched under its name), unless a full-screen attach
// owns it. Source names the probe for the TUI's log ("tick",
// "dead_event").
type Alive struct {
	Instances []*session.Instance
	Source    string
}

func (Alive) coreEvent() {}

// HealthChecked reports that a health tick's probe landed and was
// applied: the TUI arms the next tick then, so probes never overlap.
type HealthChecked struct{}

func (HealthChecked) coreEvent() {}

// GitHubChanged reports a GitHub poll applied: the TUI refreshes an open
// issue picker.
type GitHubChanged struct{}

func (GitHubChanged) coreEvent() {}

// AccountsChanged reports that the account registry, an account's auth,
// sync report or usage changed: the TUI refreshes every view showing
// accounts (refreshAccountViews) and whether account UI shows at all
// (ui.SetShowAccounts).
type AccountsChanged struct{}

func (AccountsChanged) coreEvent() {}

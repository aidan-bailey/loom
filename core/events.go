package core

import (
	"github.com/aidan-bailey/loom/session/github"
)

// Event is something the TUI must apply to its view after a model call.
// The TUI applies them in order (app.applyCoreEvent).
type Event interface{ coreEvent() }

// Notice is a line for the error bar: Err as an error (shown longer the
// longer it is, and logged), or else Info as an info line. Req names the
// request whose job it reports on (a failed job, a stash it forgot), 0 for
// one the model raised on its own: a server sends a request's notices to
// the client that made it alone.
type Notice struct {
	Err  error
	Info string
	Req  ReqID
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

// SessionLaunched reports that the tmux session of the instance ID was
// (re)launched or reattached while a loaded workspace holds it, so any
// pane client from before watched the session it replaced: the TUI gives
// it a fresh one (replacePane).
type SessionLaunched struct {
	ID InstanceID
}

func (SessionLaunched) coreEvent() {}

// Reactivated reports that a failed operation put the instance ID back to
// its previous status. A tick may have pruned its client while it was
// Deleting or Loading, so the TUI ensures one (ensurePane, a no-op unless
// the instance is active).
type Reactivated struct {
	ID InstanceID
}

func (Reactivated) coreEvent() {}

// Started reports a start that succeeded. Owner names the workspace that
// holds the instance (0 when unknown); the TUI attaches its client, and
// selects it or says where it started. ID and Title are the instance's.
// Req is the request that started it (a Create), 0 for none: only the
// client that asked selects the row and attaches inline, while every
// client attaches its pane.
type Started struct {
	Req   ReqID
	ID    InstanceID
	Title string
	Owner WorkspaceID
	// OwnerLabel names the owner in notices ("global" when unknown).
	OwnerLabel string
}

func (Started) coreEvent() {}

// Recovered reports an orphan adopted into its placeholder's row. Req,
// Owner, ID and Title are as for Started. Paused is set when adoption
// could only mark the record Paused (its session and worktree were gone).
type Recovered struct {
	Req    ReqID
	ID     InstanceID
	Title  string
	Owner  WorkspaceID
	Paused bool
	// OwnerLabel is as for Started.
	OwnerLabel string
}

func (Recovered) coreEvent() {}

// StatusesChanged reports that instance statuses may have moved (a probe,
// a hook scan, a roster answer): the TUI refreshes its tab statuses and
// peer sections (updateTabBarStatuses).
type StatusesChanged struct{}

func (StatusesChanged) coreEvent() {}

// Alive lists the IDs of instances whose tmux session a probe found
// alive. The TUI re-attaches the client of any whose client is not
// attached (a reattach failed after a full-screen attach, or the client's
// pump hit EOF on a session since relaunched under its name), unless a
// full-screen attach owns it. Source names the probe for the TUI's log
// ("tick", "dead_event").
type Alive struct {
	IDs    []InstanceID
	Source string
}

func (Alive) coreEvent() {}

// HealthChecked reports that a health tick's probe landed and was
// applied. The loop arms the next tick then, so probes never overlap; the
// TUI refreshes the workbench's diff tab.
type HealthChecked struct{}

func (HealthChecked) coreEvent() {}

// GitHubChanged carries the GitHub poll's state whenever a poll lands
// (and on the first Sync): the TUI refreshes an open issue picker, and a
// replica answers the GitHub queries from it.
type GitHubChanged struct {
	View GitHubView
}

func (GitHubChanged) coreEvent() {}

// AccountsChanged carries the account state whenever the registry, an
// account's auth, sync report or usage, or the Claude program changed
// (and on the first Sync): the TUI refreshes every view showing accounts
// (refreshAccountViews) and whether account UI shows at all
// (ui.SetShowAccounts), and a replica answers the account queries from
// it.
type AccountsChanged struct {
	View AccountsView
}

// ModelChanged carries the model's own state (the default account's
// remote-control auth and the workspace registry) whenever it changed, and
// on the first Sync. The TUI does nothing with it; a replica answers those
// queries from it.
type ModelChanged struct {
	View ModelView
}

func (ModelChanged) coreEvent() {}

func (AccountsChanged) coreEvent() {}

// WorkspacesChanged carries every served workspace's view, in serve order,
// whenever any of them (or the served set) changed since the last Sync.
// Sync puts it first, ahead of ViewsChanged, so the appliers of everything
// after it see the new workspace views. Which of them a client shows is
// its own state.
type WorkspacesChanged struct {
	Views []WorkspaceView
}

func (WorkspacesChanged) coreEvent() {}

// ViewsChanged carries a served workspace's instance views, in display
// order, whenever any of them changed since the last Sync. The TUI replaces
// its store for that workspace with Views, when it shows the workspace. Sync puts these first in the
// events it returns, so the appliers of the events that follow see the new
// views.
type ViewsChanged struct {
	// WS names the workspace: the TUI's slot for it.
	WS    WorkspaceID
	Views []InstanceView
}

func (ViewsChanged) coreEvent() {}

// Reply answers a request made with a ReqID: at once when the request is
// refused, when it finishes for a long operation. ID names the instance it
// concerns (for Create the new one, for Recover the adopted one). Err is
// the failure. Notice is what a success must still show (a stash it could
// not drop). Issue is FetchIssue's result. The notices the TUI shows for an
// operation are emitted as usual; the Reply is for the requester that waits
// on it.
type Reply struct {
	Req    ReqID
	ID     InstanceID
	Err    error
	Notice error
	Issue  github.Issue
}

func (Reply) coreEvent() {}

// EventTypes returns a zero value of every concrete Event type: what a
// codec decodes events into, by type name. TestAllEventsListsEveryEvent
// keeps it complete.
func EventTypes() []Event {
	return []Event{
		AccountsChanged{}, Alive{}, ClientsStale{}, GitHubChanged{},
		HealthChecked{}, InstancesChanged{}, ModelChanged{}, Notice{},
		Reactivated{}, Recovered{}, Reply{}, SessionLaunched{}, Started{},
		StatusesChanged{}, ViewsChanged{}, WorkspacesChanged{},
	}
}

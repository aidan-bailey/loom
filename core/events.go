package core

import (
	"github.com/aidan-bailey/loom/session/github"
)

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
// holds the instance (0 when unknown), Loaded whether it is still
// loaded. The TUI attaches its client when Loaded, and selects it or says
// where it started. ID and Title are the instance's: the title is for the
// notice, since a Started for an owner that was closed has no view left in
// the TUI.
type Started struct {
	ID     InstanceID
	Title  string
	Owner  WorkspaceID
	Loaded bool
	// OwnerLabel names the owner in notices ("global" when unknown).
	// ClosedNote, set when !Loaded, says where the owner went ("which is no
	// longer open", or "which was closed and reopened meanwhile").
	OwnerLabel string
	ClosedNote string
}

func (Started) coreEvent() {}

// Recovered reports an orphan adopted into its placeholder's row. Owner,
// Loaded, ID and Title are as for Started. Paused is set when adoption
// could only mark the record Paused (its session and worktree were gone):
// the notice says so even when no loaded workspace shows the row.
type Recovered struct {
	ID     InstanceID
	Title  string
	Owner  WorkspaceID
	Loaded bool
	Paused bool
	// OwnerLabel and ClosedNote are as for Started.
	OwnerLabel string
	ClosedNote string
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

// WorkspacesChanged carries every loaded workspace's view, in Loaded
// order, whenever any of them (or the loaded set) changed since the last
// Sync. Sync puts it first, ahead of ViewsChanged, so the appliers of
// everything after it see the new workspace views.
type WorkspacesChanged struct {
	Views []WorkspaceView
}

func (WorkspacesChanged) coreEvent() {}

// ViewsChanged carries a loaded workspace's instance views, in display
// order, whenever any of them changed since the last Sync. The TUI replaces
// its store for that workspace with Views. Sync puts these first in the
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

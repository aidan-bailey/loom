package rpc

import (
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)

// replica is a client's copy of the model's published state, kept by the
// state events (and the snapshot it is sent when it connects). It
// answers every rpc:local query, through the same view methods the
// model's own queries agree with, and hands out copies.
type replica struct {
	workspaces core.WorkspacesView
	model      core.ModelView
	accounts   core.AccountsView
	github     core.GitHubView
	views      map[core.WorkspaceID][]core.InstanceView
}

// state is what changed in the replica since the client's last Sync: the
// state events to hand the TUI next, newest state only.
type state struct {
	workspaces, model, accounts, github bool
	views                               map[core.WorkspaceID]bool
}

// apply takes a state event into r, marking it in changed, and reports
// whether ev was one.
func (r *replica) apply(ev core.Event, changed *state) bool {
	switch ev := ev.(type) {
	case core.WorkspacesChanged:
		r.workspaces = core.WorkspacesView{Views: ev.Views}
		loaded := map[core.WorkspaceID]bool{}
		for _, v := range ev.Views {
			loaded[v.ID] = true
		}
		for id := range r.views {
			if !loaded[id] {
				delete(r.views, id)
			}
		}
		changed.workspaces = true
	case core.ModelChanged:
		r.model, changed.model = ev.View, true
	case core.AccountsChanged:
		r.accounts, changed.accounts = ev.View, true
	case core.GitHubChanged:
		r.github, changed.github = ev.View, true
	case core.ViewsChanged:
		// A workspace the replica does not hold has no views to keep. The
		// queue coalesces a WorkspacesChanged in place, so one that drops a
		// workspace (a test's, or a future unregister) can come ahead of a
		// ViewsChanged queued for it, which would bring the dropped
		// workspace's views back for good. A ViewsChanged never comes ahead
		// of the WorkspacesChanged that serves its workspace: the server
		// publishes them in that order.
		if !r.workspaces.IsLoaded(ev.WS) {
			break
		}
		if r.views == nil {
			r.views = map[core.WorkspaceID][]core.InstanceView{}
		}
		r.views[ev.WS] = ev.Views
		if changed.views == nil {
			changed.views = map[core.WorkspaceID]bool{}
		}
		changed.views[ev.WS] = true
	default:
		return false
	}
	return true
}

// events are the state events for what changed, newest state, in the
// order the model publishes them: the workspaces, the model, account and
// GitHub state, then each changed workspace's instance views, in
// workspace order.
func (r *replica) events(changed state) []core.Event {
	var out []core.Event
	if changed.workspaces {
		out = append(out, core.WorkspacesChanged{Views: core.CloneWorkspaceViews(r.workspaces.Views)})
	}
	if changed.model {
		out = append(out, core.ModelChanged{View: r.model.Clone()})
	}
	if changed.accounts {
		out = append(out, core.AccountsChanged{View: r.accounts.Clone()})
	}
	if changed.github {
		out = append(out, core.GitHubChanged{View: r.github.Clone()})
	}
	for _, w := range r.workspaces.Views {
		if changed.views[w.ID] {
			out = append(out, core.ViewsChanged{WS: w.ID, Views: core.CloneViews(r.views[w.ID])})
		}
	}
	return out
}

// The rpc:local queries (core.Core), answered from the replica.

func (r *replica) Workspaces() []core.WorkspaceView { return r.workspaces.Workspaces() }
func (r *replica) Workspace(id core.WorkspaceID) (core.WorkspaceView, bool) {
	return r.workspaces.Workspace(id)
}
func (r *replica) IsLoaded(id core.WorkspaceID) bool { return r.workspaces.IsLoaded(id) }
func (r *replica) Registry() core.RegistryView       { return r.model.Clone().Registry }

// Views is the served workspace id's instance views; nil for one not
// served, as the model answers.
func (r *replica) Views(id core.WorkspaceID) []core.InstanceView {
	if !r.workspaces.IsLoaded(id) {
		return nil
	}
	views, ok := r.views[id]
	if !ok {
		return []core.InstanceView{}
	}
	return core.CloneViews(views)
}

// View is the view of the instance id, which a served workspace holds.
func (r *replica) View(id core.InstanceID) (core.InstanceView, bool) {
	for _, w := range r.workspaces.Views {
		for _, v := range r.views[w.ID] {
			if v.ID == id {
				return core.CloneViews([]core.InstanceView{v})[0], true
			}
		}
	}
	return core.InstanceView{}, false
}

func (r *replica) RCAuth() session.RemoteControlAuth { return r.model.RCAuth }
func (r *replica) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	return r.github.GitHubSnapshot(repo)
}
func (r *replica) GitHubErr(repo string) error       { return r.github.GitHubErr(repo) }
func (r *replica) GitHubUnavailable() bool           { return r.github.GitHubUnavailable() }
func (r *replica) GitHubUnavailableReason() string   { return r.github.GitHubUnavailableReason() }
func (r *replica) AccountNames() core.AccountNames   { return r.accounts.AccountNames() }
func (r *replica) AccountsLoaded() bool              { return r.accounts.AccountsLoaded() }
func (r *replica) HasExtraAccounts() bool            { return r.accounts.HasExtraAccounts() }
func (r *replica) ClaudeProgram() string             { return r.accounts.ClaudeProgram }
func (r *replica) CredentialOverride() string        { return r.accounts.CredentialOverride }
func (r *replica) AccountLoggedOut(acct string) bool { return r.accounts.AccountLoggedOut(acct) }
func (r *replica) Account(name string) (account.Account, bool) {
	return r.accounts.Account(name)
}
func (r *replica) RCAuthFor(acct string) session.RemoteControlAuth {
	return r.accounts.RCAuthFor(acct)
}
func (r *replica) AccountSync(name string) (account.SyncReport, bool) {
	return r.accounts.AccountSync(name)
}
func (r *replica) AccountUsage(name string) (account.Usage, error) {
	return r.accounts.AccountUsage(name)
}
func (r *replica) AccountEnv(name string) ([]string, error) { return r.accounts.AccountEnv(name) }

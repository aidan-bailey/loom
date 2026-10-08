package core

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)

// The published state a client keeps a replica of (daemon stage 2): every
// query in Core is answered from these views, the instance views
// (ViewsChanged) and the workspace views (WorkspacesChanged), each served
// workspace's. The model publishes each when it changes (publishState, at
// Sync) and all of them on demand (Snapshot). Their methods replicate the logic of the model's
// own query methods, which stay as they are (several run on every tick,
// and building a view for each would be waste).
// TestStateViews_AnswerAsTheModel keeps the two in step, so a replica
// answers exactly as the model would.

// WorkspacesView is every served workspace's view, in serve order.
type WorkspacesView struct {
	Views []WorkspaceView
}

// Workspaces are the served workspaces' views, in serve order
// (Core.Workspaces): never nil.
func (w WorkspacesView) Workspaces() []WorkspaceView {
	if len(w.Views) == 0 {
		return []WorkspaceView{}
	}
	return cloneWorkspaceViews(w.Views)
}

// Workspace is the view of the served workspace id (Core.Workspace).
func (w WorkspacesView) Workspace(id WorkspaceID) (WorkspaceView, bool) {
	if id == 0 {
		return WorkspaceView{}, false
	}
	for _, v := range w.Views {
		if v.ID == id {
			return cloneWorkspaceViews([]WorkspaceView{v})[0], true
		}
	}
	return WorkspaceView{}, false
}

// IsLoaded reports whether the workspace id is served (Core.IsLoaded).
func (w WorkspacesView) IsLoaded(id WorkspaceID) bool {
	_, ok := w.Workspace(id)
	return ok
}

// ModelView is the model's own state: the default account's remote-control
// auth and the workspace registry (published as ModelChanged).
type ModelView struct {
	RCAuth   session.RemoteControlAuth
	Registry RegistryView
}

// Clone deep-copies v's slices.
func (v ModelView) Clone() ModelView {
	v.Registry = RegistryView{
		Workspaces: slices.Clone(v.Registry.Workspaces),
		Open:       slices.Clone(v.Registry.Open),
	}
	return v
}

// AccountsView is the account state (published as AccountsChanged): the
// registry's accounts and default, each account's remote-control auth,
// link report, usage and launch env, and the Claude program they run.
type AccountsView struct {
	// Names is AccountNames(): Present is false without a registry.
	Names AccountNames
	// Loaded: the registry exists and loaded without error.
	Loaded bool
	// Extra: an account besides default is registered.
	Extra bool
	// ClaudeProgram is the Claude program the accounts run with ("" when
	// neither the agent program nor an active session is Claude).
	ClaudeProgram string
	// Accounts are the registered accounts, by name.
	Accounts map[string]account.Account
	// DefaultAuth is the default account's remote-control auth, and Auth
	// each extra account's that a refresh has read.
	DefaultAuth session.RemoteControlAuth
	Auth        map[string]session.RemoteControlAuth
	// Sync is each extra account's last link report.
	Sync map[string]account.SyncReport
	// Usage is each account's latest usage sample, and UsageErr its last
	// probe's failure, if any.
	Usage    map[string]account.Usage
	UsageErr map[string]string
}

// AccountNames is the registry's names, default first (Core.AccountNames).
func (v AccountsView) AccountNames() AccountNames {
	n := v.Names
	n.Names = slices.Clone(n.Names)
	return n
}

// AccountsLoaded reports that the registry exists and loaded
// (Core.AccountsLoaded).
func (v AccountsView) AccountsLoaded() bool { return v.Loaded }

// HasExtraAccounts reports that an account besides default is registered
// (Core.HasExtraAccounts).
func (v AccountsView) HasExtraAccounts() bool { return v.Extra }

// Account is the registered account called name (Core.Account).
func (v AccountsView) Account(name string) (account.Account, bool) {
	a, ok := v.Accounts[name]
	return a, ok
}

// RCAuthFor is acct's remote-control auth: the default account's for ""
// or default, an extra account's as its last refresh read it, else
// Unknown (Core.RCAuthFor).
func (v AccountsView) RCAuthFor(acct string) session.RemoteControlAuth {
	if acct == "" || acct == account.DefaultName {
		return v.DefaultAuth
	}
	if a, ok := v.Auth[acct]; ok {
		return a
	}
	return session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}
}

// AccountLoggedOut reports that acct's last auth read found it logged
// out (Core.AccountLoggedOut).
func (v AccountsView) AccountLoggedOut(acct string) bool {
	id := v.RCAuthFor(acct).Identity
	return id.ConfigDir != "" && !id.LoggedIn
}

// AccountSync is name's last link report (Core.AccountSync).
func (v AccountsView) AccountSync(name string) (account.SyncReport, bool) {
	rep, ok := v.Sync[name]
	return rep.Clone(), ok
}

// AccountUsage is name's latest usage sample and its last probe's
// failure (Core.AccountUsage).
func (v AccountsView) AccountUsage(name string) (account.Usage, error) {
	var err error
	if msg, ok := v.UsageErr[name]; ok {
		err = errors.New(msg)
	}
	return v.Usage[name].Clone(), err
}

// AccountEnv is the environment that runs Claude as name: nil for the
// default account, CLAUDE_CONFIG_DIR for a registered one, an error
// otherwise (Core.AccountEnv; account.Registry.Env).
func (v AccountsView) AccountEnv(name string) ([]string, error) {
	if !v.Names.Present {
		return nil, errNoRegistry
	}
	if name == "" || name == account.DefaultName {
		return nil, nil
	}
	a, ok := v.Accounts[name]
	if !ok {
		return nil, fmt.Errorf("account %q is not registered", name)
	}
	return account.EnvFor(a.Dir), nil
}

// Clone deep-copies v's maps and the reports and samples in them.
func (v AccountsView) Clone() AccountsView {
	v.Names.Names = slices.Clone(v.Names.Names)
	v.Accounts = maps.Clone(v.Accounts)
	v.Auth = maps.Clone(v.Auth)
	if v.Sync != nil {
		sync := make(map[string]account.SyncReport, len(v.Sync))
		for k, rep := range v.Sync {
			sync[k] = rep.Clone()
		}
		v.Sync = sync
	}
	if v.Usage != nil {
		usage := make(map[string]account.Usage, len(v.Usage))
		for k, u := range v.Usage {
			usage[k] = u.Clone()
		}
		v.Usage = usage
	}
	v.UsageErr = maps.Clone(v.UsageErr)
	return v
}

// GitHubView is the GitHub poll's state (published as GitHubChanged):
// each open repo's snapshot or its last poll's failure, and whether gh is
// usable.
type GitHubView struct {
	Snapshots map[string]github.Snapshot
	Errs      map[string]string
	// Unavailable: gh was checked and found unusable, for Reason.
	Unavailable bool
	Reason      string
}

// GitHubSnapshot is repo's latest snapshot (Core.GitHubSnapshot).
func (v GitHubView) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	s, ok := v.Snapshots[repo]
	return s.Clone(), ok
}

// GitHubErr is repo's last poll failure (Core.GitHubErr).
func (v GitHubView) GitHubErr(repo string) error {
	if msg, ok := v.Errs[repo]; ok {
		return errors.New(msg)
	}
	return nil
}

// GitHubUnavailable reports that gh was found unusable
// (Core.GitHubUnavailable).
func (v GitHubView) GitHubUnavailable() bool { return v.Unavailable }

// GitHubUnavailableReason is why gh was found unusable
// (Core.GitHubUnavailableReason).
func (v GitHubView) GitHubUnavailableReason() string { return v.Reason }

// Clone deep-copies v's maps and snapshots.
func (v GitHubView) Clone() GitHubView {
	if v.Snapshots != nil {
		snaps := make(map[string]github.Snapshot, len(v.Snapshots))
		for k, s := range v.Snapshots {
			snaps[k] = s.Clone()
		}
		v.Snapshots = snaps
	}
	v.Errs = maps.Clone(v.Errs)
	return v
}

// CloneViews deep-copies instance views (cloneViews), for a client that
// hands out its replica's views: the copies share no memory with it.
func CloneViews(views []InstanceView) []InstanceView { return cloneViews(views) }

// CloneWorkspaceViews deep-copies workspace views (cloneWorkspaceViews).
func CloneWorkspaceViews(views []WorkspaceView) []WorkspaceView {
	return cloneWorkspaceViews(views)
}

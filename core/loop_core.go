package core

import (
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
)

// The Core methods of Loop: each runs its namesake on the model, on the
// loop's goroutine, and returns its results to the caller
// (TestLoopForwardsEachMethodToItsNamesake). Sync runs syncEvents, since
// the loop starts the jobs itself.

func (l *Loop) Sync() []Event { return get(l, (*Model).syncEvents) }

func (l *Loop) Workspaces() []WorkspaceView { return get(l, (*Model).Workspaces) }
func (l *Loop) Workspace(id WorkspaceID) (WorkspaceView, bool) {
	return get2(l, func(m *Model) (WorkspaceView, bool) { return m.Workspace(id) })
}
func (l *Loop) IsLoaded(id WorkspaceID) bool {
	return get(l, func(m *Model) bool { return m.IsLoaded(id) })
}
func (l *Loop) Open(id WorkspaceID) (WorkspaceView, error) {
	return get2(l, func(m *Model) (WorkspaceView, error) { return m.Open(id) })
}
func (l *Loop) SaveForQuit() error     { return get(l, (*Model).SaveForQuit) }
func (l *Loop) Registry() RegistryView { return get(l, (*Model).Registry) }
func (l *Loop) ReloadRegistry() error  { return get(l, (*Model).ReloadRegistry) }
func (l *Loop) Register(name, dir string) (WorkspaceView, error) {
	return get2(l, func(m *Model) (WorkspaceView, error) { return m.Register(name, dir) })
}
func (l *Loop) PersistOpenList(names []string) { l.do(func(m *Model) { m.PersistOpenList(names) }) }
func (l *Loop) SetLastUsed(name string) error {
	return get(l, func(m *Model) error { return m.SetLastUsed(name) })
}
func (l *Loop) SaveSettings(id WorkspaceID, s config.Settings) error {
	return get(l, func(m *Model) error { return m.SaveSettings(id, s) })
}
func (l *Loop) SetUIPrefs(id WorkspaceID, p config.UIPrefs) error {
	return get(l, func(m *Model) error { return m.SetUIPrefs(id, p) })
}
func (l *Loop) SetHelpScreensSeen(id WorkspaceID, seen uint32) error {
	return get(l, func(m *Model) error { return m.SetHelpScreensSeen(id, seen) })
}

func (l *Loop) Views(id WorkspaceID) []InstanceView {
	return get(l, func(m *Model) []InstanceView { return m.Views(id) })
}
func (l *Loop) View(id InstanceID) (InstanceView, bool) {
	return get2(l, func(m *Model) (InstanceView, bool) { return m.View(id) })
}
func (l *Loop) Create(id WorkspaceID, spec NewInstance, req ReqID) {
	l.do(func(m *Model) { m.Create(id, spec, req) })
}
func (l *Loop) Kill(id InstanceID, req ReqID)   { l.do(func(m *Model) { m.Kill(id, req) }) }
func (l *Loop) Pause(id InstanceID, req ReqID)  { l.do(func(m *Model) { m.Pause(id, req) }) }
func (l *Loop) Resume(id InstanceID, req ReqID) { l.do(func(m *Model) { m.Resume(id, req) }) }
func (l *Loop) ResumeWith(id InstanceID, opts launch.Options, base string, req ReqID) {
	l.do(func(m *Model) { m.ResumeWith(id, opts, base, req) })
}
func (l *Loop) Recover(id InstanceID, req ReqID) { l.do(func(m *Model) { m.Recover(id, req) }) }
func (l *Loop) Merge(target, source InstanceID, req ReqID) {
	l.do(func(m *Model) { m.Merge(target, source, req) })
}
func (l *Loop) Push(id InstanceID, req ReqID) { l.do(func(m *Model) { m.Push(id, req) }) }
func (l *Loop) SendPrompt(id InstanceID, text string, req ReqID) {
	l.do(func(m *Model) { m.SendPrompt(id, text, req) })
}
func (l *Loop) FetchIssue(repo string, n int, req ReqID) {
	l.do(func(m *Model) { m.FetchIssue(repo, n, req) })
}

func (l *Loop) SetSelected(id InstanceID)     { l.do(func(m *Model) { m.SetSelected(id) }) }
func (l *Loop) MarkOutput(sessionName string) { l.do(func(m *Model) { m.MarkOutput(sessionName) }) }
func (l *Loop) PaneOutput(id InstanceID)      { l.do(func(m *Model) { m.PaneOutput(id) }) }
func (l *Loop) PaneQuiet(id InstanceID)       { l.do(func(m *Model) { m.PaneQuiet(id) }) }
func (l *Loop) VerifyDead(id InstanceID)      { l.do(func(m *Model) { m.VerifyDead(id) }) }

func (l *Loop) RCAuth() session.RemoteControlAuth { return get(l, (*Model).RCAuth) }

func (l *Loop) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	return get2(l, func(m *Model) (github.Snapshot, bool) { return m.GitHubSnapshot(repo) })
}
func (l *Loop) GitHubErr(repo string) error {
	return get(l, func(m *Model) error { return m.GitHubErr(repo) })
}
func (l *Loop) GitHubUnavailable() bool         { return get(l, (*Model).GitHubUnavailable) }
func (l *Loop) GitHubUnavailableReason() string { return get(l, (*Model).GitHubUnavailableReason) }
func (l *Loop) ExpediteGitHub()                 { l.do((*Model).ExpediteGitHub) }
func (l *Loop) WatchGitHub(repo string)         { l.do(func(m *Model) { m.WatchGitHub(repo) }) }

func (l *Loop) AccountNames() AccountNames { return get(l, (*Model).AccountNames) }
func (l *Loop) AccountsLoaded() bool       { return get(l, (*Model).AccountsLoaded) }
func (l *Loop) HasExtraAccounts() bool     { return get(l, (*Model).HasExtraAccounts) }
func (l *Loop) Account(name string) (account.Account, bool) {
	return get2(l, func(m *Model) (account.Account, bool) { return m.Account(name) })
}
func (l *Loop) RCAuthFor(acct string) session.RemoteControlAuth {
	return get(l, func(m *Model) session.RemoteControlAuth { return m.RCAuthFor(acct) })
}
func (l *Loop) AccountLoggedOut(acct string) bool {
	return get(l, func(m *Model) bool { return m.AccountLoggedOut(acct) })
}
func (l *Loop) AccountSync(name string) (account.SyncReport, bool) {
	return get2(l, func(m *Model) (account.SyncReport, bool) { return m.AccountSync(name) })
}
func (l *Loop) AccountUsage(name string) (account.Usage, error) {
	return get2(l, func(m *Model) (account.Usage, error) { return m.AccountUsage(name) })
}
func (l *Loop) AccountEnv(name string) ([]string, error) {
	return get2(l, func(m *Model) ([]string, error) { return m.AccountEnv(name) })
}
func (l *Loop) ClaudeProgram() string      { return get(l, (*Model).ClaudeProgram) }
func (l *Loop) CredentialOverride() string { return get(l, (*Model).CredentialOverride) }
func (l *Loop) RunningAsAccount() string   { return get(l, (*Model).RunningAsAccount) }
func (l *Loop) ReloadAccounts()            { l.do((*Model).ReloadAccounts) }
func (l *Loop) RequestAccountsRefresh(withDefault bool) {
	l.do(func(m *Model) { m.RequestAccountsRefresh(withDefault) })
}
func (l *Loop) RequestUsageProbe() { l.do((*Model).RequestUsageProbe) }
func (l *Loop) AddAccount(name string) (string, error) {
	return get2(l, func(m *Model) (string, error) { return m.AddAccount(name) })
}
func (l *Loop) RemoveAccount(name string) error {
	return get(l, func(m *Model) error { return m.RemoveAccount(name) })
}
func (l *Loop) SetDefaultAccount(name string) error {
	return get(l, func(m *Model) error { return m.SetDefaultAccount(name) })
}

package core

import (
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
)

// Core is the session model as its clients use it: every method the TUI
// calls. In stage 1C the instance half is final: requests by InstanceID,
// InstanceView values, Reply. The workspace and account half still passes
// the model's own objects (*Workspace, *config.WorkspaceRegistry,
// *account.Registry, …), and stage 1D converts it to values before the
// model moves to its own goroutine. *Model is the only implementation.
type Core interface {
	// The loop: the TUI drains the model after every message (Sync), hands
	// it every job's result (Deliver), and starts its first background
	// jobs (Begin).
	Sync() Out
	Deliver(msg any)
	Begin()

	// Startup: the classic workspace's load, the account registry and the
	// default account's remote-control auth, which newHome sets up.
	LoadClassic(sweepTmux bool) error
	InitAccounts()
	SetRCAuth(a session.RemoteControlAuth)

	// Workspaces: the loaded ones, their transitions, saves and the
	// registry.
	Classic() *Workspace
	Tabs() []*Workspace
	IsLoaded(ws *Workspace) bool
	ClosedNote(ws *Workspace) string
	OpenTab(def config.Workspace) (*Workspace, error)
	CloseTab(name string) (*Workspace, error)
	EnterGlobal(focused *Workspace) (*Workspace, error)
	StayGlobal()
	RestoreSaved(saved []config.Workspace) int
	RestoreFailed() []string
	KeepRestoreFailed(desired map[string]bool)
	OpenNames() []string
	PersistOpenList()
	Register(name, dir string) (config.Workspace, error)
	Registry() *config.WorkspaceRegistry
	SetLastUsed(name string) error
	Save(ws *Workspace) error
	SaveForQuit() error

	// Instances: their views, and every lifecycle action as a request by
	// ID, answered by a Reply when it carries a ReqID.
	Views(ws *Workspace) []InstanceView
	View(id InstanceID) (InstanceView, bool)
	Create(ws *Workspace, spec NewInstance, req ReqID)
	Kill(id InstanceID, req ReqID)
	Pause(id InstanceID, req ReqID)
	Resume(id InstanceID, req ReqID)
	ResumeWith(id InstanceID, opts launch.Options, base string, req ReqID)
	Recover(id InstanceID, req ReqID)
	Merge(target, source InstanceID, req ReqID)
	Push(id InstanceID, req ReqID)
	SendPrompt(id InstanceID, text string, req ReqID)
	FetchIssue(repo string, n int, req ReqID)

	// Claude status and the tick: the health tick, and what the TUI's pane
	// events tell the model.
	Tick(selected InstanceID)
	MarkOutput(sessionName string)
	PaneOutput(id InstanceID)
	PaneQuiet(id InstanceID)
	VerifyDead(id InstanceID)

	// The agent program, and the remote-control auth it launches with.
	Program() string
	SetProgram(p string)
	RCAuth() session.RemoteControlAuth

	// GitHub: the poll's results, and a poll sooner.
	GitHubSnapshot(repo string) (github.Snapshot, bool)
	GitHubErr(repo string) error
	GitHubUnavailable() bool
	GitHubUnavailableReason() string
	ExpediteGitHub()

	// Accounts: the registry, each account's auth, sync, usage and env,
	// and the account requests.
	Accounts() *account.Registry
	AccountsLoaded() bool
	HasExtraAccounts() bool
	Account(name string) (account.Account, bool)
	RCAuthFor(acct string) session.RemoteControlAuth
	AccountLoggedOut(acct string) bool
	AccountSync(name string) (account.SyncReport, bool)
	AccountUsage(name string) (account.Usage, error)
	AccountEnv(name string) ([]string, error)
	ClaudeProgram() string
	ReloadAccounts()
	RequestAccountsRefresh(withDefault bool)
	RequestUsageProbe()
	AddAccount(name string) (string, error)
	RemoveAccount(name string) error
	SetDefaultAccount(name string) error
}

var _ Core = (*Model)(nil)

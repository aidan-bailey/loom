package core

import (
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
)

// Core is the session model as its clients use it: every method the TUI
// calls. It is value-typed: every parameter and result, and every Event,
// is plain data a client could be sent from another process, never the
// model's own objects (TestCoreIsValueTyped). Instances are named by
// InstanceID and seen as InstanceView values, workspaces by WorkspaceID
// and WorkspaceView; the registry and accounts cross as copies, and every
// change is a request. *Loop, the model on its own goroutine, is the only
// implementation: every call is a round trip over that goroutine, the
// model runs its own jobs and tick, and Loop.Wakes says when to Sync.
type Core interface {
	// The loop: the TUI drains the model when the loop wakes it and after
	// every message (Sync), and starts its first background jobs and its
	// health tick (Begin).
	Sync() []Event
	Begin()

	// Startup: the classic workspace's load, the account registry and the
	// default account's remote-control auth, which newHome sets up.
	LoadClassic(sweepTmux bool) error
	InitAccounts()
	SetRCAuth(a session.RemoteControlAuth)

	// Workspaces: the loaded ones, their transitions, saves and the
	// registry.
	StayGlobal()
	RestoreSaved(saved []config.Workspace) int
	RestoreFailed() []string
	KeepRestoreFailed(desired map[string]bool)
	OpenNames() []string
	PersistOpenList()
	Register(name, dir string) (config.Workspace, error)
	SetLastUsed(name string) error
	SaveForQuit() error

	// Workspaces by ID: their views, transitions and saves, the registry
	// as a copy, and the requests that change a workspace's settings, UI
	// prefs and help screens.
	Workspace(id WorkspaceID) (WorkspaceView, bool)
	Classic() (WorkspaceView, bool)
	Tabs() []WorkspaceView
	IsLoaded(id WorkspaceID) bool
	OpenTab(def config.Workspace) (WorkspaceView, error)
	CloseTab(name string) error
	EnterGlobal(focused WorkspaceID) (WorkspaceView, error)
	Save(id WorkspaceID) error
	Registry() RegistryView
	ReloadRegistry() error
	SaveSettings(id WorkspaceID, s config.Settings) error
	SetUIPrefs(id WorkspaceID, p config.UIPrefs) error
	SetHelpScreensSeen(id WorkspaceID, seen uint32) error

	// Instances: their views, and every lifecycle action as a request by
	// ID, answered by a Reply when it carries a ReqID.
	Views(id WorkspaceID) []InstanceView
	View(id InstanceID) (InstanceView, bool)
	Create(id WorkspaceID, spec NewInstance, req ReqID)
	Kill(id InstanceID, req ReqID)
	Pause(id InstanceID, req ReqID)
	Resume(id InstanceID, req ReqID)
	ResumeWith(id InstanceID, opts launch.Options, base string, req ReqID)
	Recover(id InstanceID, req ReqID)
	Merge(target, source InstanceID, req ReqID)
	Push(id InstanceID, req ReqID)
	SendPrompt(id InstanceID, text string, req ReqID)
	FetchIssue(repo string, n int, req ReqID)

	// Claude status and the tick: what the TUI's pane events tell the
	// model, and the selected row the model's health tick favours.
	SetSelected(id InstanceID)
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
	AccountNames() AccountNames
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

var _ Core = (*Loop)(nil)

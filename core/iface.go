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
// change is a request. *Loop, the model on its own goroutine, implements
// it in process: every call is a round trip over that goroutine, the
// model runs its own jobs and tick, and Loop.Wakes says when to Sync. The
// core/rpc client implements it over a connection (daemon stage 2). The
// model is booted before any client connects (Model.Boot) and starts its
// background work when its loop does (Loop.Begin): neither is a client's.
//
// The rpc package is generated from this file. A method's line comment
// says how a client serves it:
//   - rpc:local answers from the client's replica of the published state
//     (Snapshot, then the state events);
//   - rpc:cast is sent one way, with no reply, and publishes nothing, so a
//     cast must change no published state: it marks, names or starts what
//     publishes on its own when it lands;
//   - rpc:client never leaves the client (Sync returns the events it
//     received);
//   - every other method is a request, whose reply follows the events it
//     produced.
//
// Parameter names are the wire's field names.
type Core interface {
	// The loop: the TUI drains the model when the loop wakes it and after
	// every message.
	Sync() []Event // rpc:client

	// Workspaces: every one the model serves, by ID, and the requests that
	// open one, save them all, register one and write the registry. Which
	// of them a client shows (its tabs, or the workspace it shows while
	// none is open) is the client's own state.
	Workspaces() []WorkspaceView                    // rpc:local
	Workspace(id WorkspaceID) (WorkspaceView, bool) // rpc:local
	IsLoaded(id WorkspaceID) bool                   // rpc:local
	Open(id WorkspaceID) (WorkspaceView, error)
	SaveForQuit() error
	Registry() RegistryView // rpc:local
	ReloadRegistry() error
	Register(name, dir string) (WorkspaceView, error)
	PersistOpenList(names []string)
	SetLastUsed(name string) error

	// A workspace's settings, UI prefs and help screens.
	SaveSettings(id WorkspaceID, settings config.Settings) error
	SetUIPrefs(id WorkspaceID, prefs config.UIPrefs) error
	SetHelpScreensSeen(id WorkspaceID, seen uint32) error

	// Instances: their views, and every lifecycle action as a request by
	// ID, answered by a Reply when it carries a ReqID.
	Views(id WorkspaceID) []InstanceView     // rpc:local
	View(id InstanceID) (InstanceView, bool) // rpc:local
	Create(id WorkspaceID, spec NewInstance, req ReqID)
	Kill(id InstanceID, req ReqID)
	Pause(id InstanceID, req ReqID)
	Resume(id InstanceID, req ReqID)
	ResumeWith(id InstanceID, opts launch.Options, base string, req ReqID)
	Recover(id InstanceID, req ReqID)
	Merge(target, source InstanceID, req ReqID)
	Push(id InstanceID, req ReqID)
	SendPrompt(id InstanceID, text string, req ReqID)
	FetchIssue(repo string, number int, req ReqID)

	// Claude status and the tick: what the TUI's pane events tell the
	// model, and the selected row the model's health tick favours.
	SetSelected(id InstanceID)     // rpc:cast
	MarkOutput(sessionName string) // rpc:cast
	PaneOutput(id InstanceID)      // rpc:cast
	PaneQuiet(id InstanceID)       // rpc:cast
	VerifyDead(id InstanceID)      // rpc:cast

	// The default account's remote-control auth, which sessions launch
	// with.
	RCAuth() session.RemoteControlAuth // rpc:local

	// GitHub: the poll's results, and a poll sooner.
	GitHubSnapshot(repo string) (github.Snapshot, bool) // rpc:local
	GitHubErr(repo string) error                        // rpc:local
	GitHubUnavailable() bool                            // rpc:local
	GitHubUnavailableReason() string                    // rpc:local
	ExpediteGitHub()                                    // rpc:cast

	// Accounts: the registry, each account's auth, sync, usage and env,
	// and the account requests.
	AccountNames() AccountNames                         // rpc:local
	AccountsLoaded() bool                               // rpc:local
	HasExtraAccounts() bool                             // rpc:local
	Account(name string) (account.Account, bool)        // rpc:local
	RCAuthFor(acct string) session.RemoteControlAuth    // rpc:local
	AccountLoggedOut(acct string) bool                  // rpc:local
	AccountSync(name string) (account.SyncReport, bool) // rpc:local
	AccountUsage(name string) (account.Usage, error)    // rpc:local
	AccountEnv(name string) ([]string, error)           // rpc:local
	ClaudeProgram() string                              // rpc:local
	ReloadAccounts()
	RequestAccountsRefresh(withDefault bool) // rpc:cast
	RequestUsageProbe()                      // rpc:cast
	AddAccount(name string) (string, error)
	RemoveAccount(name string) error
	SetDefaultAccount(name string) error
}

var _ Core = (*Loop)(nil)

package core

import (
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/feedback"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/buildsweep"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/aidan-bailey/loom/session/transcript"
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
	// open one, save them all, register, unregister, rename or reset one
	// and write the registry. Which of them a client shows (its tabs, or
	// the workspace it shows while none is open) is the client's own
	// state.
	Workspaces() []WorkspaceView                    // rpc:local
	Workspace(id WorkspaceID) (WorkspaceView, bool) // rpc:local
	IsLoaded(id WorkspaceID) bool                   // rpc:local
	Open(id WorkspaceID) (WorkspaceView, error)
	SaveForQuit() error
	Registry() RegistryView // rpc:local
	ReloadRegistry() error
	Register(name, dir string) (WorkspaceView, error)
	Unregister(name string) error
	RenameWorkspace(from, to string) error
	Reset(id WorkspaceID, req ReqID) error
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
	Send(id InstanceID, msg Message, req ReqID)
	// Answer answers a dialog the session's Claude holds open (an
	// InstanceView.Asks entry) from the phone; the Reply follows once the
	// session's loom mod took the answer.
	Answer(id InstanceID, ask string, answer AskAnswer, req ReqID)
	FetchIssue(repo string, number int, req ReqID)
	// A session's conversation, its worktree's diff and its brainstorm
	// companion's page, read for the client that asks: the Reply's Turns
	// (transcript.Read), Diff (Instance.ReadPatch) and Mockup
	// (mockup.Fetch).
	ReadTurns(id InstanceID, query transcript.Query, req ReqID)
	ReadDiff(id InstanceID, req ReqID)
	ReadMockup(id InstanceID, req ReqID)
	// A job of session id's output.log, the Reply's JobLog (sched.ReadLog):
	// its tail when after < 0, else the bytes from after.
	ReadJobLog(id InstanceID, job sched.JobID, after int64, req ReqID)
	// Delete idle and paused sessions' unused Rust executables now, or
	// with DryRun list them: the Reply's Sweep (buildsweep.Report).
	SweepBuilds(opts buildsweep.Options, req ReqID)
	// The build sweep's setting, the global config's threshold (0 for
	// off), and the request that sets it whichever workspace asks.
	BuildSweep() BuildSweepView // rpc:local
	ConfigureBuildSweep(afterHours int) error

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

	// GitHub: the poll's results, a poll sooner, and a repository the
	// poll covers from now on.
	GitHubSnapshot(repo string) (github.Snapshot, bool) // rpc:local
	GitHubErr(repo string) error                        // rpc:local
	GitHubUnavailable() bool                            // rpc:local
	GitHubUnavailableReason() string                    // rpc:local
	ExpediteGitHub()                                    // rpc:cast
	WatchGitHub(repo string)                            // rpc:cast

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
	CredentialOverride() string                         // rpc:local
	RunningAsAccount() string                           // rpc:local
	ReloadAccounts()
	RequestAccountsRefresh(withDefault bool) // rpc:cast
	RequestUsageProbe()                      // rpc:cast
	AddAccount(name string) (string, error)
	RemoveAccount(name string, force bool) error
	SetDefaultAccount(name string) error

	// The web layer (internal/web): its settings and the devices paired to
	// it, and the requests that change them. Web answers from the replica
	// (the view travels in ModelView, so no event of its own); a pairing
	// code, the one secret a request returns, lives in the model's memory
	// only.
	Web() WebView // rpc:local
	ConfigureWeb(listen, origin string) error
	PairDevice(name string) (Pairing, error)
	RevokeDevice(name string) error

	// The work queue (package sched, owned by the model): its published
	// state, which a replica answers, and the requests that change it. Each
	// request is answered at once, with its refusal when it is refused.
	// SubmitJob's Reply carries the new job's ID (Job) and, in Notice, the
	// warning for a submission from a launch no served session holds.
	Queue() QueueView // rpc:local
	SubmitJob(spec sched.Submit, req ReqID)
	CancelJob(id sched.JobID, from CancelFrom, req ReqID)
	MoveJobToFront(id sched.JobID, req ReqID)
	NotifyJob(id sched.JobID, req ReqID)
	SetSchedulerCaps(s config.SchedulerSettings, req ReqID)
	// Turn job borrowing (scheduler.borrow) on or off in the global
	// config.json, whichever workspace asks (borrow setting spec).
	ConfigureBorrow(on bool) error
	// The work queue's beliefs (smart scheduler spec §7): the Reply's
	// Beliefs holds the beliefs q selects and the advisor's track record;
	// ForgetBelief drops one.
	ReadBeliefs(q sched.BeliefQuery, req ReqID)
	ForgetBelief(id sched.SignatureID, req ReqID)
	// The agent's state and its last limit calls, newest first: the
	// Reply's Agent (smart scheduler spec §16).
	ReadAgent(limit int, req ReqID)

	// Feedback from sessions (session feedback spec): the inbox, which a
	// replica answers, and the requests that change it. FileFeedback's
	// Reply carries the new note (Note); FixThread's Reply and Started are
	// a Create's.
	Feedback() FeedbackView // rpc:local
	FileFeedback(note FeedbackNote, req ReqID)
	CloseThread(thread feedback.ThreadID, req ReqID)
	FixThread(thread feedback.ThreadID, workspace string, req ReqID)
}

var _ Core = (*Loop)(nil)

package core

import (
	"fmt"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// Job is work the model hands its caller to run off the model's
// goroutine. It reads no model state and returns one message (nil for
// none), which the caller hands back to Deliver.
type Job func() any

// Out is what the model produced since the last Drain: events for the
// TUI and jobs to run, each in the order produced.
type Out struct {
	Events []Event
	Jobs   []Job
}

// Empty reports whether o holds nothing.
func (o Out) Empty() bool { return len(o.Events) == 0 && len(o.Jobs) == 0 }

// Options configure a Model.
type Options struct {
	// Registry is the workspace registry; nil in bare tests.
	Registry *config.WorkspaceRegistry
	// Program is the agent command the process was started with (-p).
	Program string
	// CmdExec replaces cmd.MakeExecutor() on the workspace load paths: a
	// test seam. nil in production.
	CmdExec cmd2.Executor
	// Ctx and Config are the startup context and its config: the classic
	// workspace's, shown while no tab is open.
	Ctx    *config.WorkspaceContext
	Config *config.Config
}

// Model is the session model (see the package doc). Methods must be
// called on one goroutine: in stage 1B, the TUI's Update goroutine.
type Model struct {
	registry *config.WorkspaceRegistry
	program  string
	cmdExec  cmd2.Executor

	// classic is the workspace shown while no tab is open: the startup
	// context's (classic startup, or the fallback when no tab could be
	// restored) or the global one EnterGlobal built. nil while tabs are
	// open: the first tab opened replaces it.
	classic *Workspace
	// tabs are the open workspace tabs, in tab order.
	tabs []*Workspace
	// restoreFailed names the workspaces the registry's open list held but
	// RestoreSaved could not open. Their live sessions were spared only
	// because that launch skipped the orphan sweep, so they stay in the
	// persisted open list (PersistOpenList) until one opens (OpenTab) or
	// is deselected (KeepRestoreFailed); EnterGlobal and StayGlobal clear
	// them all.
	restoreFailed []string

	// rcAuth is the default account's remote-control auth: detected at
	// startup and refreshed by the accounts refresh
	// (app.handleAccountsRefreshed), both through SetRCAuth, and read by
	// every launch decision.
	rcAuth session.RemoteControlAuth

	out Out
}

// New builds the model and its classic workspace: the startup context's
// state and storage, not yet loaded (LoadClassic loads it, or
// RestoreSaved's fallback). It first syncs the process-wide session flags
// from Config and writes the loom-context prompt files, covering both the
// classic path and the tab path (OpenTab re-syncs per workspace); without
// it a classic launch would never set the flag. Formerly the start of
// app.newHome.
func New(o Options) (*Model, error) {
	cfgDir := ""
	if o.Ctx != nil {
		cfgDir = o.Ctx.ConfigDir
	}
	session.SetLoomContextEnabled(o.Config.LoomContextEnabled())
	session.SetSubagentTrackingEnabled(o.Config.SubagentTrackingEnabled())
	if err := session.WriteLoomContextFiles(cfgDir); err != nil {
		log.For("core").Warn("loom_context.write_failed", "err", err.Error())
	}
	state := config.LoadStateFrom(cfgDir)
	storage, err := session.NewStorage(state, cfgDir)
	if err != nil {
		return nil, fmt.Errorf("initialize storage: %w", err)
	}
	m := NewForTest(o)
	m.classic = NewWorkspace(WorkspaceParts{Ctx: o.Ctx, Storage: storage, Config: o.Config, State: state})
	return m, nil
}

// NewForTest builds a model with no workspace and no side effects; a test
// installs its fixture's workspaces with SetWorkspacesForTest.
func NewForTest(o Options) *Model {
	return &Model{registry: o.Registry, program: o.Program, cmdExec: o.CmdExec}
}

// SetWorkspacesForTest installs a fixture's workspaces: classic when tabs
// is empty, else tabs in order (classic is then ignored, as the first tab
// replaces it).
func (m *Model) SetWorkspacesForTest(classic *Workspace, tabs []*Workspace) {
	if len(tabs) > 0 {
		m.classic, m.tabs = nil, append([]*Workspace(nil), tabs...)
		return
	}
	m.classic, m.tabs = classic, nil
}

// SetExecForTest replaces the executor of the workspace load paths.
func (m *Model) SetExecForTest(e cmd2.Executor) { m.cmdExec = e }

// SetRegistryForTest replaces the workspace registry.
func (m *Model) SetRegistryForTest(r *config.WorkspaceRegistry) { m.registry = r }

// SetRestoreFailedForTest replaces the workspaces that failed to restore.
func (m *Model) SetRestoreFailedForTest(names []string) { m.restoreFailed = names }

// executor returns the executor for the workspace load paths: the test
// seam when set, the production executor otherwise.
func (m *Model) executor() cmd2.Executor {
	if m.cmdExec != nil {
		return m.cmdExec
	}
	return cmd2.MakeExecutor()
}

// Program is the agent command new sessions launch: the one the process
// was started with (-p), until a settings save replaces it (SetProgram).
func (m *Model) Program() string { return m.program }

// SetProgram replaces the agent command new sessions launch (a settings
// save of the default program).
func (m *Model) SetProgram(p string) { m.program = p }

// RCAuth is the default account's remote-control auth.
func (m *Model) RCAuth() session.RemoteControlAuth { return m.rcAuth }

// SetRCAuth records the default account's remote-control auth.
func (m *Model) SetRCAuth(a session.RemoteControlAuth) { m.rcAuth = a }

// emit queues an event for the TUI.
func (m *Model) emit(e Event) { m.out.Events = append(m.out.Events, e) }

// spawn queues a job for the caller to run; nil is ignored.
func (m *Model) spawn(j Job) {
	if j != nil {
		m.out.Jobs = append(m.out.Jobs, j)
	}
}

// notifyErr queues err for the error bar.
func (m *Model) notifyErr(err error) {
	if err != nil {
		m.emit(Notice{Err: err})
	}
}

// notifyInfo queues an info line for the error bar.
func (m *Model) notifyInfo(s string) { m.emit(Notice{Info: s}) }

// Drain returns everything produced since the last Drain, and forgets it.
func (m *Model) Drain() Out {
	out := m.out
	m.out = Out{}
	return out
}

// Deliver hands the model a job's result. Results the model does not know
// are logged and dropped.
func (m *Model) Deliver(msg any) {
	switch msg := msg.(type) {
	case nil:
	case StartResult:
		m.deliverStart(msg)
	case ResumeResult:
		m.deliverResume(msg)
	case RecoverResult:
		m.deliverRecover(msg)
	case KillResult:
		m.deliverKill(msg)
	case PauseResult:
		m.deliverPause(msg)
	case OpFailed:
		m.deliverOpFailed(msg)
	case MergeResult:
		m.notifyErr(msg.Err)
	case promptFailed:
		m.notifyErr(msg.err)
	default:
		log.For("core").Error("deliver.unknown_result", "type", fmt.Sprintf("%T", msg))
	}
}

package core

import (
	"fmt"
	"slices"

	"github.com/aidan-bailey/loom/account"
	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)

// Job is work the model queues for its loop to run off the model's
// goroutine. It reads no model state and returns one message (nil for
// none), which the loop (Loop) delivers back to Deliver.
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
	// Program is the agent program the process was started with: the -p
	// flag, else the startup workspace's. The model detects the default
	// account's remote-control auth with it (Boot) and runs the accounts'
	// Claude commands with it (ClaudeProgram) until a settings save
	// replaces it; each client keeps the program its own drafts default to.
	Program string
	// CmdExec replaces cmd.MakeExecutor() on the workspace load paths: a
	// test seam. nil in production.
	CmdExec cmd2.Executor
}

// Model is the session model (see the package doc). Methods must be
// called on one goroutine: its loop's (Loop), or a core test's.
type Model struct {
	registry *config.WorkspaceRegistry
	program  string
	cmdExec  cmd2.Executor

	// workspaces are every workspace the model serves, loaded once (Boot):
	// the global one and each registered one, in that order, plus any
	// registered or reread later. None is ever dropped, and every one is
	// published: which of them a client shows (its tabs, or the workspace
	// it shows while none is open) is the client's own state.
	workspaces []*Workspace
	booted     bool

	// rcAuth is the default account's remote-control auth: detected at
	// boot (Boot) and refreshed by the accounts refresh
	// (deliverAccountsRefreshed), and read by every launch decision.
	rcAuth session.RemoteControlAuth

	// cause is the request on whose behalf the model is applying a job's
	// result (a tracked job's, or one a job serving that request spawned),
	// 0 for none. The Started, Recovered and Notice events emitted
	// meanwhile carry it (emit), so a server can route them to the client
	// that made the request, and jobs spawned meanwhile serve it too
	// (spawn).
	cause ReqID
	// selected are the instances whose full diff the health tick's probe
	// refreshes: each client's selected row (SetSelected, SetSelection).
	selected []InstanceID

	// gates throttle the background jobs riding the health tick (roster
	// query, subagent scan, GitHub poll, account usage probe, accounts
	// refresh), one pollGate per gateKind (see gate.go; resolve with
	// m.gate). The zero value is ready to use: intervals come from
	// gateIntervals. Loop-goroutine only.
	gates [numGateKinds]pollGate

	// claudeTmpPending holds the Claude temp-dir sweeps workspace loads
	// queued (requestClaudeTmpSweep), keyed by config dir, until the
	// health tick dispatches them. Loop-goroutine only.
	claudeTmpPending map[string]claudeTmpJob

	// dirtySessions records tmux session names that emitted output since the
	// last health tick (event mode only). Consumed by takeDirty to gate
	// diff-stat refreshes. Loop-goroutine only.
	dirtySessions map[string]bool

	// roster is Claude's own view of its live sessions, keyed by working
	// directory, refreshed once per health tick (see rosterQueryJob). It is
	// authoritative where the pane scraper is inferential, so status events
	// consult it first and fall back when it has no entry for a session.
	// Loop-goroutine only.
	roster map[string]session.RosterEntry
	// rosterByAccount is each extra account's roster, keyed by account then
	// working directory: `claude agents --json` lists only its own config
	// dir's sessions, so each account is queried as itself. The default
	// account's stays in roster. Loop-goroutine only.
	rosterByAccount map[string]map[string]session.RosterEntry

	// ghAvailable caches gh's install/auth check, resolved by the first
	// poll. Until checked, polls proceed (the poll itself checks).
	ghAvailable ghAvailability
	// ghState is the latest GitHub snapshot per open repo path. Replaced
	// wholesale on every ghResult; a repo whose query failed is absent.
	ghState map[string]github.Snapshot
	// ghErrs is the last poll error per open repo, replaced wholesale
	// alongside ghState. A repo can fail every poll forever while
	// ghAvailable stays ok — CheckCLI is not repo-scoped, so a repo with
	// no GitHub remote never flips availability — and without this the
	// picker would sit on "loading…" with nothing to show for it.
	ghErrs map[string]error
	// ghBases is the resolved base ref name per repo ("origin/main"),
	// refreshed by the poll and read by probeJob for parity.
	ghBases map[string]string

	// accounts is the Claude account registry (account/), loaded from the
	// global config dir at startup. Loop-goroutine only: launches read the
	// published dir map (session.SetAccountDirs) instead.
	accounts *account.Registry
	// accountAuth is each extra account's remote-control auth, with the
	// identity `claude auth status` reported, filled by accountsRefreshed.
	// The default account's lives in rcAuth.
	accountAuth map[string]session.RemoteControlAuth
	// accountSync is each extra account's last link report.
	accountSync map[string]account.SyncReport
	// usage is each account's latest probe state (usage.go).
	usage map[string]accountUsage
	// accountsStamp is the accounts.json version the registry was last read
	// (or written) at, and accountsSeen the state it held then; the health
	// tick rereads the file only when its stat differs from the stamp, and
	// acts only when the state differs (see maybeReloadAccounts).
	accountsStamp accountsFileStamp
	accountsSeen  string
	// syncRefusalLogged is the last reason syncMainDir refused the main
	// config dir, so each reason is logged once.
	syncRefusalLogged string
	// refreshDefaultAuth asks the next accounts refresh to reread the
	// default account's auth too; kept until one dispatches, so a request
	// made while another refresh is in flight is not lost.
	refreshDefaultAuth bool

	// ids is each reported instance's ID (idOf). An instance no loaded
	// workspace holds is forgotten at the next publish; IDs are never reused
	// (nextID only grows).
	ids    map[*session.Instance]InstanceID
	nextID InstanceID
	// published is each loaded workspace's views as last published (Sync).
	published map[*Workspace][]InstanceView
	// wsIDs is each reported workspace's ID (wsIDOf). A workspace no
	// longer loaded loses its entry at the next publish, and its ID is
	// never reused (nextWSID only grows).
	wsIDs    map[*Workspace]WorkspaceID
	nextWSID WorkspaceID
	// publishedWS is every served workspace's view as last published
	// (Sync), in Loaded order.
	publishedWS []WorkspaceView
	// publishedModel and publishedAccounts are the model and account views
	// as last published (publishState); nil before the first.
	publishedModel    *ModelView
	publishedAccounts *AccountsView
	// ghGen counts the GitHub polls applied (deliverGH), and
	// ghPublishedGen is the count GitHubChanged last published (ghPublished
	// once it has been).
	ghGen, ghPublishedGen uint64
	ghPublished           bool
	// usageGen counts the usage probe rounds applied (deliverUsage), and
	// usagePublishedGen is the count AccountsChanged last published. The
	// TUI renders a usage sample's age when it refreshes its account
	// views, so every round must reach it, even one that left the account
	// view unchanged (the same probe failing again).
	usageGen, usagePublishedGen uint64

	// out holds the events and the jobs queued since the last take; bgJobs
	// the background jobs (spawnBackground), kept apart so a loop stopping
	// can wait for the others alone (takeJobsSplit).
	out    Out
	bgJobs []Job
}

// New builds the model, serving no workspace yet: Boot loads them, before
// the model's loop starts.
func New(o Options) *Model { return newModel(o) }

// newModel builds a model with no workspace and no side effects.
func newModel(o Options) *Model {
	return &Model{
		registry:  o.Registry,
		program:   o.Program,
		cmdExec:   o.CmdExec,
		ids:       make(map[*session.Instance]InstanceID),
		published: make(map[*Workspace][]InstanceView),
		wsIDs:     make(map[*Workspace]WorkspaceID),
	}
}

// NewForTest builds a model with no workspace and no side effects; a test
// installs its fixture's workspaces with SetWorkspacesForTest.
func NewForTest(o Options) *Model { return newModel(o) }

// SetWorkspacesForTest installs a fixture's workspaces, served and opened,
// in serve order (a nil one and a repeat are skipped). The model has not
// booted: a Boot loads the global and registered workspaces beside them.
func (m *Model) SetWorkspacesForTest(wss ...*Workspace) {
	m.workspaces = nil
	for _, ws := range wss {
		if ws != nil && !slices.Contains(m.workspaces, ws) {
			ws.opened = true
			m.workspaces = append(m.workspaces, ws)
		}
	}
}

// SetExecForTest replaces the executor of the workspace load paths.
func (m *Model) SetExecForTest(e cmd2.Executor) { m.cmdExec = e }

// SetRegistryForTest replaces the workspace registry.
func (m *Model) SetRegistryForTest(r *config.WorkspaceRegistry) { m.registry = r }

// executor returns the executor for the workspace load paths: the test
// seam when set, the production executor otherwise.
func (m *Model) executor() cmd2.Executor {
	if m.cmdExec != nil {
		return m.cmdExec
	}
	return cmd2.MakeExecutor()
}

// RCAuth is the default account's remote-control auth.
func (m *Model) RCAuth() session.RemoteControlAuth { return m.rcAuth }

// SetRCAuth records the default account's remote-control auth, as Boot's
// detection does.
func (m *Model) SetRCAuth(a session.RemoteControlAuth) { m.rcAuth = a }

// emit queues an event for the TUI.
func (m *Model) emit(e Event) {
	if m.cause != 0 {
		switch ev := e.(type) {
		case Notice:
			ev.Req = m.cause
			e = ev
		case Started:
			ev.Req = m.cause
			e = ev
		case Recovered:
			ev.Req = m.cause
			e = ev
		}
	}
	m.out.Events = append(m.out.Events, e)
}

// spawn queues a job for the caller to run; nil is ignored. A job spawned
// on a request's behalf (m.cause) serves that request too: its result is
// delivered as caused, so the chain's last event (a Create's start, then
// its prompt send, then Started) still names the request.
func (m *Model) spawn(j Job) {
	if j == nil {
		return
	}
	if req := m.cause; req != 0 {
		inner := j
		j = func() any { return caused{req: req, result: inner()} }
	}
	m.out.Jobs = append(m.out.Jobs, j)
}

// spawnBackground queues a job the model runs on its own behalf (the
// tick's probe, a gated job, a dead-session check), which serves no
// request even when one's delivery queues it.
func (m *Model) spawnBackground(j Job) {
	if j != nil {
		m.bgJobs = append(m.bgJobs, j)
	}
}

// caused is a job's result delivered on behalf of the request req: see
// spawn and Model.cause.
type caused struct {
	req    ReqID
	result any
}

// causedBy runs f on behalf of req: the Started, Recovered and Notice
// events f emits carry req, and the jobs it spawns serve req too.
func (m *Model) causedBy(req ReqID, f func()) {
	prev := m.cause
	m.cause = req
	defer func() { m.cause = prev }()
	f()
}

// SetSelected names the instance whose full diff the health tick's probe
// refreshes: the TUI's selected row, 0 for none. The loop's tick reads it
// (Loop). With several clients a server sets them all (SetSelection).
func (m *Model) SetSelected(id InstanceID) {
	if id == 0 {
		m.SetSelection(nil)
		return
	}
	m.SetSelection([]InstanceID{id})
}

// SetSelection names every instance whose full diff the health tick's
// probe refreshes: each client's selected row. A server serving several
// clients keeps each one's SetSelected and sets their union here.
func (m *Model) SetSelection(ids []InstanceID) { m.selected = slices.Clone(ids) }

// takeJobs returns the jobs queued since the last take, and forgets them.
// The loop starts them after every step.
func (m *Model) takeJobs() []Job {
	fg, bg := m.takeJobsSplit()
	return append(fg, bg...)
}

// takeJobsSplit takes the jobs queued, the ones serving a request or a
// change of the model's own (spawn) apart from its background work
// (spawnBackground): a loop stopping waits for the first (Loop.Quiesce) and
// drops the second.
func (m *Model) takeJobsSplit() (fg, bg []Job) {
	fg, bg = m.out.Jobs, m.bgJobs
	m.out.Jobs, m.bgJobs = nil, nil
	return fg, bg
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
	out.Jobs = append(out.Jobs, m.bgJobs...)
	m.out, m.bgJobs = Out{}, nil
	return out
}

// Deliver hands the model a job's result. Results the model does not know
// are logged and dropped.
func (m *Model) Deliver(msg any) {
	switch msg := msg.(type) {
	case nil:
	case caused:
		m.causedBy(msg.req, func() { m.Deliver(msg.result) })
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
	case resumeSkipped:
		log.For("core").Warn("resume.skipped", "err", msg.err)
	case promptFailed:
		m.notifyErr(msg.err)
	case promptSent:
		m.deliverPromptSent(msg)
	case gatedResult:
		m.deliverGated(msg)
	case HealthResult:
		m.deliverHealth(msg)
	case DeadVerified:
		m.deliverDeadVerified(msg)
	case terminalChecked:
		m.deliverTerminalChecked(msg)
	case rosterResult:
		m.deliverRoster(msg)
	case hookScanResults:
		m.deliverHookScan(msg)
	case ghResult:
		m.deliverGH(msg)
	case pushResult:
		m.deliverPush(msg)
	case accountsRefreshed:
		m.deliverAccountsRefreshed(msg)
	case usageResult:
		m.deliverUsage(msg)
	case tracked:
		m.deliverTracked(msg)
	case issueResult:
		// Only its Reply carries it (deliverTracked).
	default:
		log.For("core").Error("deliver.unknown_result", "type", fmt.Sprintf("%T", msg))
	}
}

// Begin starts the model's first background jobs, each when due: an
// accounts refresh and a usage probe. Its loop runs it when it starts
// serving (Loop.Begin).
func (m *Model) Begin() {
	m.maybeAccountsRefresh()
	m.maybeUsageProbe()
}

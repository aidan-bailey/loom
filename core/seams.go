package core

import (
	"slices"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)

// Test seams. The TUI's tests drive the model through these where a test
// of the TUI's response needs a job result or a piece of state the model
// keeps unexported. Production code never calls them.

// RosterResultForTest is a finished roster query's result, for Deliver:
// entries is the default account's roster, err its failure, at when the
// query started.
func RosterResultForTest(entries map[string]session.RosterEntry, err error, at time.Time) any {
	return rosterResult{entries: entries, err: err, at: at}
}

// GitHubResultForTest is a finished GitHub poll's result, for Deliver: gh
// checked and found available or not (reason says why not), and each
// repo's snapshot or error.
func GitHubResultForTest(available bool, reason string, snapshots map[string]github.Snapshot, errs map[string]error) any {
	return ghResult{
		available: ghAvailability{checked: true, ok: available, reason: reason, checkedAt: time.Now()},
		snapshots: snapshots,
		errs:      errs,
	}
}

// UsageResultForTest is a finished round of usage probes, for Deliver.
func UsageResultForTest(results map[string]account.Usage, errs map[string]error) any {
	return usageResult{results: results, errs: errs}
}

// AccountsRefreshedForTest is a finished accounts refresh, for Deliver:
// the default account's auth (nil when not reread) and each extra
// account's auth and link report.
func AccountsRefreshedForTest(defaultAuth *session.RemoteControlAuth, auth map[string]session.RemoteControlAuth, sync map[string]account.SyncReport) any {
	return accountsRefreshed{defaultAuth: defaultAuth, auth: auth, sync: sync}
}

// AdoptAccountsForTest installs reg as the registry and publishes it, as
// InitAccounts does with the one it loads.
func (m *Model) AdoptAccountsForTest(reg *account.Registry) { m.adoptAccounts(reg) }

// SetAccountsForTest replaces the registry without publishing it.
func (m *Model) SetAccountsForTest(reg *account.Registry) { m.accounts = reg }

// SetAccountAuthForTest replaces every extra account's remote-control
// auth.
func (m *Model) SetAccountAuthForTest(auth map[string]session.RemoteControlAuth) {
	m.accountAuth = auth
}

// SetAccountSyncForTest records name's last link report.
func (m *Model) SetAccountSyncForTest(name string, rep account.SyncReport) {
	m.ensureAccountMaps()
	m.accountSync[name] = rep
}

// SetAccountUsageForTest records name's usage sample and last probe error.
func (m *Model) SetAccountUsageForTest(name string, u account.Usage, err error) {
	m.ensureAccountMaps()
	m.usage[name] = accountUsage{last: u, err: err}
}

// IDOfForTest returns inst's ID (assigning one): for tests that build
// instances in the model and then find them through the TUI.
func (m *Model) IDOfForTest(inst *session.Instance) InstanceID { return m.idOf(inst) }

// InstanceForTest returns the instance id names, nil when no loaded
// workspace holds it: for tests that act on the instance behind a row.
func (m *Model) InstanceForTest(id InstanceID) *session.Instance {
	inst, _ := m.lookup(id)
	return inst
}

// TrackedForTest is result as the job of request req, for the instance id,
// delivers it, so its Reply follows: a test delivers it in place of
// running the job, whose real work (adopting an orphan, say) it can't do.
func TrackedForTest(req ReqID, id InstanceID, result any) any {
	return tracked{req: req, id: id, result: result}
}

// FetchedIssueForTest is the result FetchIssue's job delivers for request
// req: issue, or err. A test delivers it in place of running the job, whose
// gh call it can't make.
func FetchedIssueForTest(req ReqID, issue github.Issue, err error) any {
	return tracked{req: req, result: issueResult{issue: issue, err: err}}
}

// AddForTest adds inst to w as the model's own edits do (Workspace.add):
// for tests that build instances and install them in a fixture workspace.
func (w *Workspace) AddForTest(inst *session.Instance) { w.add(inst) }

// RemoveForTest removes inst from w by identity, as the model's own edits
// do (Workspace.remove): for tests that stand in for a removal.
func (w *Workspace) RemoveForTest(inst *session.Instance) bool { return w.remove(inst) }

// ApplyClaudeStatusForTest moves inst to the status its hooks or the
// roster last reported, as the model does when a hook scan or a roster
// answer lands (applyClaudeStatus).
func (m *Model) ApplyClaudeStatusForTest(inst *session.Instance) { m.applyClaudeStatus(inst) }

// InstancesForTest returns w's instances, in display order (the model's own
// slice; a test must not modify it).
func (w *Workspace) InstancesForTest() []*session.Instance { return w.instances() }

// AccountsRegistryForTest is the model's account registry (nil before
// InitAccounts): app tests check what an account request did to it.
func (m *Model) AccountsRegistryForTest() *account.Registry { return m.accounts }

// WorkspaceForTest resolves a loaded workspace's ID to the model's
// workspace, nil when none is loaded: app tests reach a fixture's
// instances through it.
func (m *Model) WorkspaceForTest(id WorkspaceID) *Workspace { return m.wsLookup(id) }

// WorkspaceIDForTest is ws's ID, assigned on first use.
func (m *Model) WorkspaceIDForTest(ws *Workspace) WorkspaceID { return m.wsIDOf(ws) }

// UntrackedForTest is a job's result with a request's tracking removed: a
// request's job (Kill, Merge, SendPrompt, …) answers with its operation's
// result wrapped for its Reply, and a job serving a request (a Create's
// start) with its result wrapped as caused; any other result comes back as
// it is.
func UntrackedForTest(result any) any {
	switch r := result.(type) {
	case tracked:
		return UntrackedForTest(r.result)
	case caused:
		return UntrackedForTest(r.result)
	}
	return result
}

// CausedForTest is result as a job serving the request req delivers it:
// the Started, Recovered and Notice events it produces name req, as when
// the TUI's own Create or Recover lands. A test that hands the model an
// operation's result directly wraps it so, or the TUI treats the result
// as another client's.
func CausedForTest(req ReqID, result any) any { return caused{req: req, result: result} }

// ViewForTest is the view the model would publish for inst, with ID id:
// for ui tests that build an instance and render it.
func ViewForTest(inst *session.Instance, id InstanceID) InstanceView {
	m := &Model{ids: map[*session.Instance]InstanceID{inst: id}}
	return m.viewOf(inst)
}

// gateNamed resolves a gate kind's String to the kind; it panics on a
// name no kind has, which is a broken test.
func gateNamed(kind string) gateKind {
	for k := gateKind(0); k < numGateKinds; k++ {
		if k.String() == kind {
			return k
		}
	}
	panic("core: no gate kind named " + kind)
}

// GateForTest reports the gate of the job kind names (gateKind's String:
// "roster", "hook_scan", "github", "usage", "accounts_refresh"): whether a
// dispatch is in flight, a request is pending, and the gate is due at now.
func (m *Model) GateForTest(kind string, now time.Time) (inFlight, pending, due bool) {
	k := gateNamed(kind)
	g := m.gate(k)
	return g.inFlight, g.pending, m.gateDue(k, now)
}

// SetGateForTest sets the in-flight flag and last dispatch of the gate of
// the job kind names.
func (m *Model) SetGateForTest(kind string, inFlight bool, last time.Time) {
	g := m.gate(gateNamed(kind))
	g.inFlight, g.last = inFlight, last
}

// RefreshDefaultAuthForTest reports whether the next accounts refresh
// rereads the default account's auth too.
func (m *Model) RefreshDefaultAuthForTest() bool { return m.refreshDefaultAuth }

// OutputMarkedForTest reports whether sessionName's output is recorded
// for the next health tick's diff refresh (MarkOutput).
func (m *Model) OutputMarkedForTest(sessionName string) bool { return m.dirtySessions[sessionName] }

// StartForTest runs m on a loop that keeps every job for the test to run
// (JobsForTest, then DeliverForTest) and never ticks on its own
// (TickForTest). Between calls its goroutine is idle, so the test may
// also reach m directly (ModelForTest): the loop's channel operations
// order those accesses for the race detector.
func StartForTest(m *Model) *Loop { return startLoop(m, true, 0) }

// ModelForTest returns the loop's model, for its seams. Only a loop that
// is idle between calls (StartForTest, or a production loop with nothing
// in flight) may be reached this way.
func (l *Loop) ModelForTest() *Model { return l.m }

// JobsForTest starts what the model has queued (a seam called on it
// directly may have queued jobs no step has taken yet), then takes the
// jobs a StartForTest loop kept, in the order queued.
func (l *Loop) JobsForTest() []Job {
	l.do(func(*Model) {})
	l.heldMu.Lock()
	defer l.heldMu.Unlock()
	jobs := l.held
	l.held = nil
	return jobs
}

// DeliverForTest delivers a job's result on the loop, as the goroutine
// that ran the job would (the health probe's result arming the next
// tick), and returns once it is applied. Its client is not woken.
func (l *Loop) DeliverForTest(result any) {
	l.do(func(*Model) { l.deliverResult(result) })
}

// SpawnForTest queues job on the loop as the model would, a foreground job
// (spawn) or a background one (spawnBackground), and starts it: a test
// of what waits for jobs in flight (Quiesce, a daemon stopping).
func (l *Loop) SpawnForTest(job Job, background bool) {
	l.do(func(m *Model) {
		if background {
			m.spawnBackground(job)
		} else {
			m.spawn(job)
		}
	})
}

// SelectedForTest is the model's selection (Model.SelectedForTest), read
// on the loop, so a test may read it while a server is calling the loop.
func (l *Loop) SelectedForTest() []InstanceID {
	var ids []InstanceID
	l.do(func(m *Model) { ids = m.SelectedForTest() })
	return ids
}

// SetRCAuthForTest records the default account's remote-control auth on
// the loop (Model.SetRCAuth), as Boot's detection does: a change straight
// to the model, which only the next publish sends to its clients.
func (l *Loop) SetRCAuthForTest(a session.RemoteControlAuth) {
	l.do(func(m *Model) { m.SetRCAuth(a) })
}

// TickForTest runs the model's health tick on the loop, as its timer
// would.
func (l *Loop) TickForTest() {
	l.do(func(m *Model) { m.Tick() })
}

// SelectedForTest returns the instances the probe refreshes the full diff
// of (SetSelected, SetSelection).
func (m *Model) SelectedForTest() []InstanceID { return slices.Clone(m.selected) }

package core

import (
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

// UntrackedForTest is a job's result with a request's tracking removed: a
// request's job (Kill, Merge, SendPrompt, …) answers with its operation's
// result wrapped for its Reply; any other result comes back as it is.
func UntrackedForTest(result any) any {
	if t, ok := result.(tracked); ok {
		return t.result
	}
	return result
}

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

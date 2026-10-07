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

// IDForTest returns inst's ID (assigning one): for tests that build
// instances in the model and then find them through the TUI.
func (m *Model) IDForTest(inst *session.Instance) InstanceID { return m.idOf(inst) }

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

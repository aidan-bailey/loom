package core

import (
	"time"
)

// gateKind names one gated background job. A gated job's result carries
// its kind, so delivery never depends on which gate value a copy holds.
type gateKind int

const (
	// gateRoster throttles the `claude agents --json` query (maybeRosterQuery).
	gateRoster gateKind = iota
	// gateHookScan throttles the hook-event scan (maybeHookScan).
	gateHookScan
	// gateGH throttles the GitHub poll (maybeGHQuery).
	gateGH
	// gateUsage throttles the account usage probes (maybeUsageProbe).
	gateUsage
	// gateAccountsRefresh keeps one account auth refresh in flight
	// (maybeAccountsRefresh).
	gateAccountsRefresh
	// gateClaudeTmp keeps one Claude temp-dir sweep in flight
	// (maybeClaudeTmpSweep).
	gateClaudeTmp

	numGateKinds
)

// String names the kind for logs.
func (k gateKind) String() string {
	switch k {
	case gateRoster:
		return "roster"
	case gateHookScan:
		return "hook_scan"
	case gateGH:
		return "github"
	case gateUsage:
		return "usage"
	case gateAccountsRefresh:
		return "accounts_refresh"
	case gateClaudeTmp:
		return "claude_tmp"
	}
	return "unknown"
}

// gateIntervals is each job's minimum time between dispatches. It is keyed
// by kind rather than stored on the gate so a zero-value Model — however it
// was constructed — is throttled exactly like a production one.
var gateIntervals = [numGateKinds]time.Duration{
	gateRoster:   rosterInterval,
	gateHookScan: hookScanInterval,
	gateGH:       ghInterval,
	gateUsage:    usageInterval,
	// gateAccountsRefresh stays 0: refreshes run on events (an account
	// appeared, a login, a probe losing access), never on a cadence.
	// gateClaudeTmp stays 0 too: sweeps run when a workspace loads.
}

// pollGate throttles one background job: at most one dispatch in flight,
// and at least its kind's gateIntervals entry between dispatches. It makes
// the two invariants every such job shares structural rather than a
// comment at each site:
//
//  1. Arm nothing when nothing is dispatched. dispatchGated arms the gate
//     only when build returns a Job — no Job means no result, so
//     nothing would ever come back to disarm it.
//  2. Disarm on every delivery, errors included. The Job dispatchGated
//     queues wraps its result in a gatedResult, and Deliver disarms the gate
//     before the inner result reaches its handler, so no handler can
//     forget to.
//
// Breaking either latches the job off for the rest of the session. The
// zero value is a gate that has never dispatched. Update-goroutine only.
type pollGate struct {
	last     time.Time
	inFlight bool
	// pending records a request() made while a dispatch was in flight;
	// deliverGated dispatches once more when that flight lands.
	pending bool
}

// due reports whether a dispatch may start at now: none in flight, and at
// least interval since the last dispatch (a zero last is always due). A
// zero interval only dedupes.
func (g *pollGate) due(now time.Time, interval time.Duration) bool {
	if g.inFlight {
		return false
	}
	return g.last.IsZero() || now.Sub(g.last) >= interval
}

// expedite makes the next dispatch due as soon as nothing is in flight.
// An in-flight dispatch is not cut short: its delivery disarms the gate
// and the following tick dispatches immediately.
func (g *pollGate) expedite() {
	g.last = time.Time{}
}

// request asks for the job to run as soon as possible: it is due at once,
// and a request made while a dispatch is in flight is kept, so
// deliverGated dispatches once more after that flight lands. The caller
// still makes its own maybe… call, which dispatches at once when nothing
// is in flight. Requests during one flight collapse into one follow-up.
// For triggers that must not wait for the next health tick: a pane going
// quiet, or a status change the roster should confirm.
func (g *pollGate) request() {
	g.expedite()
	if g.inFlight {
		g.pending = true
	}
}

// gate resolves kind to the model's gate for it.
func (m *Model) gate(kind gateKind) *pollGate { return &m.gates[kind] }

// gateDue reports whether kind's gate is due at now under its interval.
func (m *Model) gateDue(kind gateKind, now time.Time) bool {
	return m.gate(kind).due(now, gateIntervals[kind])
}

// gatedResult carries a gated job's result back to Deliver, which disarms
// the gate for kind and then delivers result as if it had arrived on its
// own.
type gatedResult struct {
	kind   gateKind
	result any
}

// dispatchGated calls build only when kind's gate is due at now. A nil job
// from build arms nothing: no job means no result to disarm the gate.
// Otherwise it arms the gate and queues the job, its result wrapped in a
// gatedResult. Reports whether it dispatched. build runs on the model's
// goroutine, so it may read model state; the job it returns must not.
// (A job returns one value, so the old rule against a tea.Batch result is
// gone: nothing can hide a second result from the disarm.)
func (m *Model) dispatchGated(kind gateKind, now time.Time, build func() Job) bool {
	if !m.gateDue(kind, now) {
		return false
	}
	job := build()
	if job == nil {
		return false
	}
	g := m.gate(kind)
	g.inFlight = true
	g.last = now
	m.spawn(func() any { return gatedResult{kind: kind, result: job()} })
	return true
}

// deliverGated disarms r's gate before anything else, then delivers the
// inner result. A nil result still disarms. A request() made during the
// flight dispatches the job once more, after the result is handled.
func (m *Model) deliverGated(r gatedResult) {
	g := m.gate(r.kind)
	g.inFlight = false
	again := g.pending
	g.pending = false
	m.Deliver(r.result)
	if again {
		m.redispatch(r.kind)
	}
}

// redispatch runs kind's job again for a request() made mid-flight. Only
// the jobs that request() have an entry.
func (m *Model) redispatch(kind gateKind) {
	switch kind {
	case gateRoster:
		m.maybeRosterQuery(m.ActiveInstances())
	case gateHookScan:
		m.maybeHookScan(m.ActiveInstances())
	case gateUsage:
		m.maybeUsageProbe()
	case gateAccountsRefresh:
		m.maybeAccountsRefresh()
	case gateClaudeTmp:
		m.maybeClaudeTmpSweep()
	}
}

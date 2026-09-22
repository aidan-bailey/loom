package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// gateKind names one gated background job. gatedMsg carries a kind rather
// than a *pollGate so delivery never depends on home not having been
// copied since dispatch.
type gateKind int

const (
	// gateRoster throttles the `claude agents --json` query (maybeRosterQuery).
	gateRoster gateKind = iota
	// gateSubagent throttles the hook-event scan (maybeSubagentScan).
	gateSubagent
	// gateGH throttles the GitHub poll (maybeGHQuery).
	gateGH
	// gateRatioSave dedupes the split-ratio flush tick (maybeArmRatioSave).
	gateRatioSave

	numGateKinds
)

// pollGate throttles one background job: at most one dispatch in flight,
// and at least interval between dispatches. It makes the two invariants
// every such job shares structural rather than a comment at each site:
//
//  1. Arm nothing when nothing is dispatched. dispatchGated arms the gate
//     only when build returns a Cmd — no Cmd means no result message, so
//     nothing would ever come back to disarm it.
//  2. Disarm on every delivery, errors included. The Cmd dispatchGated
//     returns wraps its result in a gatedMsg, and Update disarms the gate
//     before the inner message reaches its handler, so no handler can
//     forget to.
//
// Breaking either latches the job off for the rest of the session.
// Update-goroutine only.
type pollGate struct {
	// interval is the minimum time between dispatches. Zero means the
	// gate only dedupes (due whenever nothing is in flight) — which is
	// also what a gate left at its zero value does, so constructors must
	// install the real intervals via newPollGates.
	interval time.Duration
	last     time.Time
	inFlight bool
}

// newPollGates returns every gate with its production interval.
func newPollGates() [numGateKinds]pollGate {
	return [numGateKinds]pollGate{
		gateRoster:   {interval: rosterInterval},
		gateSubagent: {interval: subagentInterval},
		gateGH:       {interval: ghInterval},
		// The ratio flush paces itself with its own tick
		// (ratioSaveDelay); the gate only keeps one tick in flight.
		gateRatioSave: {},
	}
}

// due reports whether a dispatch may start at now: none in flight, and at
// least interval since the last dispatch (a zero last is always due).
func (g *pollGate) due(now time.Time) bool {
	if g.inFlight {
		return false
	}
	return g.last.IsZero() || now.Sub(g.last) >= g.interval
}

// expedite makes the next dispatch due as soon as nothing is in flight.
// An in-flight dispatch is not cut short: its delivery disarms the gate
// and the following tick dispatches immediately.
func (g *pollGate) expedite() {
	g.last = time.Time{}
}

// gate resolves kind to the model's gate for it.
func (m *home) gate(kind gateKind) *pollGate {
	return &m.gates[kind]
}

// gatedMsg carries a gated Cmd's result back to Update, which disarms the
// gate for kind and then handles msg as if it had arrived on its own.
type gatedMsg struct {
	kind gateKind
	msg  tea.Msg
}

// dispatchGated calls build only when kind's gate is due at now. A nil Cmd
// from build arms nothing. Otherwise it arms the gate and returns a Cmd
// whose result comes back wrapped in a gatedMsg.
//
// build runs on the Update goroutine, so it may read model state; the Cmd
// it returns runs off it and must not. That Cmd must produce exactly one
// message: a tea.Tick is fine (calling it blocks until the timer fires),
// but a tea.Batch or tea.Sequence yields a message only the runtime can
// expand, and wrapped it would reach Update as an unhandled type — the
// gate would disarm, but the batched Cmds would never run.
func (m *home) dispatchGated(kind gateKind, now time.Time, build func() tea.Cmd) tea.Cmd {
	g := m.gate(kind)
	if !g.due(now) {
		return nil
	}
	cmd := build()
	if cmd == nil {
		return nil
	}
	g.inFlight = true
	g.last = now
	return func() tea.Msg {
		return gatedMsg{kind: kind, msg: cmd()}
	}
}

// deliverGated disarms msg's gate before anything else, then routes the
// inner message back through Update so it reaches the same handler it
// would have reached unwrapped. A nil inner message still disarms.
func (m *home) deliverGated(msg gatedMsg) (tea.Model, tea.Cmd) {
	m.gate(msg.kind).inFlight = false
	if msg.msg == nil {
		return m, nil
	}
	return m.Update(msg.msg)
}

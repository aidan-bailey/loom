package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/log"
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

// String names the kind for logs.
func (k gateKind) String() string {
	switch k {
	case gateRoster:
		return "roster"
	case gateSubagent:
		return "subagent"
	case gateGH:
		return "github"
	case gateRatioSave:
		return "ratio_save"
	}
	return "unknown"
}

// gateIntervals is each job's minimum time between dispatches. It is keyed
// by kind rather than stored on the gate so a zero-value home — however it
// was constructed — is throttled exactly like a production one.
var gateIntervals = [numGateKinds]time.Duration{
	gateRoster:   rosterInterval,
	gateSubagent: subagentInterval,
	gateGH:       ghInterval,
	// gateRatioSave stays 0: the flush paces itself with its own tick
	// (ratioSaveDelay), so the gate only keeps one tick in flight.
}

// pollGate throttles one background job: at most one dispatch in flight,
// and at least its kind's gateIntervals entry between dispatches. It makes
// the two invariants every such job shares structural rather than a
// comment at each site:
//
//  1. Arm nothing when nothing is dispatched. dispatchGated arms the gate
//     only when build returns a Cmd — no Cmd means no result message, so
//     nothing would ever come back to disarm it.
//  2. Disarm on every delivery, errors included. The Cmd dispatchGated
//     returns wraps its result in a gatedMsg, and Update disarms the gate
//     before the inner message reaches its handler, so no handler can
//     forget to.
//
// Breaking either latches the job off for the rest of the session. The
// zero value is a gate that has never dispatched. Update-goroutine only.
type pollGate struct {
	last     time.Time
	inFlight bool
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

// gate resolves kind to the model's gate for it.
func (m *home) gate(kind gateKind) *pollGate {
	return &m.gates[kind]
}

// gateDue reports whether kind's gate is due at now under its interval.
func (m *home) gateDue(kind gateKind, now time.Time) bool {
	return m.gate(kind).due(now, gateIntervals[kind])
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
// gate would disarm, but the batched Cmds would never run (deliverGated
// logs a wrapped tea.BatchMsg).
func (m *home) dispatchGated(kind gateKind, now time.Time, build func() tea.Cmd) tea.Cmd {
	if !m.gateDue(kind, now) {
		return nil
	}
	cmd := build()
	if cmd == nil {
		return nil
	}
	g := m.gate(kind)
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
	switch inner := msg.msg.(type) {
	case nil:
		return m, nil
	case tea.BatchMsg:
		// A builder broke dispatchGated's single-message rule. Update has
		// no case for a BatchMsg, so its Cmds would silently never run.
		log.For("app").Error("gated_batch_msg", "kind", msg.kind.String(), "cmds", len(inner))
		return m, nil
	}
	return m.Update(msg.msg)
}

package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A job spawned while the model applies a request's result serves that
// request too, so a chain's last event still names it: a Create replies at
// once, then its start lands, then its prompt send, then Started.
func TestCause_JobsSpawnedOnARequestsBehalfServeIt(t *testing.T) {
	m := NewForTest(Options{})
	m.causedBy(5, func() { m.spawn(func() any { return "done" }) })
	out := m.Drain()
	require.Len(t, out.Jobs, 1)
	assert.Equal(t, caused{req: 5, result: "done"}, out.Jobs[0]())

	m.spawn(func() any { return "plain" })
	out = m.Drain()
	require.Len(t, out.Jobs, 1)
	assert.Equal(t, "plain", out.Jobs[0](), "outside a request nothing is wrapped")
}

// The model's own jobs serve no request, even when a request's delivery
// queues one: a gated job's notice (a GitHub poll's error, say) is for
// every client.
func TestCause_BackgroundJobsServeNoRequest(t *testing.T) {
	m := NewForTest(Options{})
	m.causedBy(5, func() {
		m.spawnBackground(func() any { return "bg" })
		m.dispatchGated(gateRoster, time.Now(), func() Job { return func() any { return "gated" } })
	})
	out := m.Drain()
	require.Len(t, out.Jobs, 2)
	assert.Equal(t, "bg", out.Jobs[0]())
	assert.Equal(t, gatedResult{kind: gateRoster, result: "gated"}, out.Jobs[1]())
}

// Started, and the notices of a request's job, name the request they
// serve, so a server can send them to the client that made it.
func TestCause_EventsNameTheRequestTheirResultServes(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "s")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)

	m.Deliver(caused{req: 7, result: StartResult{Instance: inst, Owner: ws}})
	started := startedEvents(m.Drain().Events)
	require.Len(t, started, 1)
	assert.Equal(t, ReqID(7), started[0].Req)

	m.Deliver(caused{req: 8, result: promptFailed{err: assert.AnError}})
	assert.Contains(t, m.Drain().Events, Event(Notice{Err: assert.AnError, Req: 8}))

	m.Deliver(StartResult{Instance: inst, Owner: ws})
	started = startedEvents(m.Drain().Events)
	require.Len(t, started, 1)
	assert.Zero(t, started[0].Req, "a start no request asked for names none")
}

func startedEvents(evs []Event) []Started {
	var out []Started
	for _, ev := range evs {
		if s, ok := ev.(Started); ok {
			out = append(out, s)
		}
	}
	return out
}

// A Create replies at once, but its start serves it: the Started at the end
// of the chain (or the start's failure) must name the Create.
func TestCause_ACreatesStartServesTheCreate(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(ws)

	m.createWS(ws, NewInstance{Title: "new", Path: t.TempDir(), Program: "claude", Start: true}, 3)
	out := m.Drain()
	require.Len(t, out.Jobs, 1)
	res, ok := out.Jobs[0]().(caused) // not a repository: the start fails fast
	require.True(t, ok, "the start job serves the Create")
	assert.Equal(t, ReqID(3), res.req)
	assert.IsType(t, StartResult{}, res.result)
}

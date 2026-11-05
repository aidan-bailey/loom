package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callModel is a queue model holding a started Claude session on launch L1
// whose mod (poller "p") said hello and can send to subagents.
func callModel(t *testing.T) *Model {
	t.Helper()
	withMod(t)
	m := wqModel(t)
	modInst(t, m, "owner")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Poller: "p", Agents: true}))
	return m
}

// reportCall has L1's mod report c starting.
func reportCall(t *testing.T, m *Model, c ModCall) {
	t.Helper()
	require.NoError(t, m.ModCall("L1", "p", c, false))
}

// submitFromL1 submits argv from launch L1 and returns the job's agent.
func submitFromL1(t *testing.T, m *Model, argv ...string) string {
	t.Helper()
	sub := wqJob(t, argv...)
	sub.Launch = "L1"
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	return wqRecord(t, m, id).Agent
}

// A submit takes its agent from the one reported call that holds its whole
// argv, and from nothing else (subagent job results spec §5.2): no call, two
// calls, a call missing an argument, and the main loop's own call all leave
// the job the session's.
func TestSubmit_TakesItsAgentFromTheOneCallThatMadeIt(t *testing.T) {
	exit0 := "loom job submit -d x -- sh -c 'exit 0'"
	cases := []struct {
		name  string
		calls []ModCall
		want  string
	}{
		{"one subagent call", []ModCall{{ID: "c1", Agent: "a1", Command: exit0}}, "a1"},
		{"the main loop's call", []ModCall{{ID: "c1", Command: exit0}}, ""},
		{"no call", nil, ""},
		{"two calls could have made it", []ModCall{{ID: "c1", Agent: "a1", Command: exit0}, {ID: "c2", Agent: "a2", Command: exit0}}, ""},
		{"only one holds the argv", []ModCall{{ID: "c1", Agent: "a1", Command: "loom job run -- cargo test"}, {ID: "c2", Agent: "a2", Command: exit0}}, "a2"},
		{"an argument missing", []ModCall{{ID: "c1", Agent: "a1", Command: "loom job submit -- sh -c 'exit 1'"}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := callModel(t)
			for _, call := range c.calls {
				reportCall(t, m, call)
			}
			assert.Equal(t, c.want, submitFromL1(t, m, "sh", "-c", "exit 0"))
		})
	}
}

// A call accounts for as many submits as its command runs, and no more: a
// third submit of the same command came from somewhere loom can't see.
func TestSubmit_ACallAccountsForAsManySubmitsAsItRuns(t *testing.T) {
	m := callModel(t)
	reportCall(t, m, ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true && loom job submit -- true"})
	got := []string{submitFromL1(t, m, "true"), submitFromL1(t, m, "true"), submitFromL1(t, m, "true")}
	assert.Equal(t, []string{"a1", "a1", ""}, got)
}

// The job view carries the agent the record holds.
func TestSubmit_TheJobViewCarriesTheAgent(t *testing.T) {
	m := callModel(t)
	reportCall(t, m, ModCall{ID: "c1", Agent: "a1b2c3d4e5f6", Command: "loom job submit -- true"})
	submitFromL1(t, m, "true")
	q := m.Queue()
	require.Len(t, q.Jobs, 1)
	assert.Equal(t, "a1b2c3d4e5f6", q.Jobs[0].Agent)
	assert.Equal(t, "a1b2c3d", ShortAgent(q.Jobs[0].Agent))
}

func TestModCall_AnEndedCallMatchesNothing(t *testing.T) {
	m := callModel(t)
	reportCall(t, m, ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true"})
	require.NoError(t, m.ModCall("L1", "p", ModCall{ID: "c1"}, true))
	assert.Equal(t, "", submitFromL1(t, m, "true"))
}

// A command that runs no submit is not kept: `loom job list` submits nothing,
// so it takes no room under the cap from a call that does, and can't take a
// submit that a script made.
func TestModCall_ACommandThatSubmitsNothingIsNotKept(t *testing.T) {
	m := callModel(t)
	reportCall(t, m, ModCall{ID: "c0", Agent: "a0", Command: "loom job submit -- true"})
	for i := range maxModCalls {
		reportCall(t, m, ModCall{ID: fmt.Sprint(i), Agent: "a1", Command: "loom job list && true"})
	}
	assert.Equal(t, "a0", submitFromL1(t, m, "true"), "the calls that submit nothing left the one that does")
	assert.Equal(t, "", submitFromL1(t, m, "true"), "and take no submit themselves")
}

func TestModCall_ARetriedReportChangesNothing(t *testing.T) {
	m := callModel(t)
	c := ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true"}
	reportCall(t, m, c)
	assert.Equal(t, "a1", submitFromL1(t, m, "true"))
	reportCall(t, m, c) // the same report again: its use stays spent
	assert.Equal(t, "", submitFromL1(t, m, "true"))
}

func TestModCall_TheCapDropsTheOldest(t *testing.T) {
	m := callModel(t)
	for i := range maxModCalls + 1 {
		reportCall(t, m, ModCall{ID: fmt.Sprint(i), Agent: fmt.Sprintf("a%d", i), Command: fmt.Sprintf("loom job submit -- echo n%03d", i)})
	}
	assert.Equal(t, "", submitFromL1(t, m, "echo", "n000"), "the oldest was dropped")
	assert.Equal(t, fmt.Sprintf("a%d", maxModCalls), submitFromL1(t, m, "echo", fmt.Sprintf("n%03d", maxModCalls)))
}

func TestModCall_AnEndedLinkForgetsItsCalls(t *testing.T) {
	m := callModel(t)
	reportCall(t, m, ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true"})
	inst := m.instByLaunch("L1")
	m.endLink(inst, m.mods[inst], "the session stopped")
	assert.Equal(t, "", submitFromL1(t, m, "true"))
}

func TestModCall_RefusesAnUnknownLaunchAndASupersededPoller(t *testing.T) {
	m := callModel(t)
	c := ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true"}
	assert.ErrorIs(t, m.ModCall("L9", "p", c, false), ErrModUnknownLaunch)
	assert.ErrorIs(t, m.ModCall("L1", "old", c, false), ErrModSuperseded)
	assert.Equal(t, "", submitFromL1(t, m, "true"), "a refused report records nothing")
}

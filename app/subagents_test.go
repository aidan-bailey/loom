package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/subagent"

	"github.com/stretchr/testify/require"
)

func TestSubagentScanDispatchesForClaude(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-first", "claude", "x")
	m := homeWithAppState(t)

	require.NotNil(t, m.maybeSubagentScan([]*session.Instance{inst}))
	require.True(t, m.subagentInFlight)
}

func TestSubagentScanThrottledWithinInterval(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-window", "claude", "x")
	m := homeWithAppState(t)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeSubagentScan(active))
	m.subagentInFlight = false
	require.Nil(t, m.maybeSubagentScan(active))

	m.lastSubagentScan = time.Now().Add(-subagentInterval - time.Second)
	require.NotNil(t, m.maybeSubagentScan(active))
}

func TestSubagentScanNotStackedWhileInFlight(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-inflight", "claude", "x")
	m := homeWithAppState(t)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeSubagentScan(active))
	m.lastSubagentScan = time.Now().Add(-subagentInterval - time.Second)
	require.Nil(t, m.maybeSubagentScan(active))
}

// Same deadlock guard as the roster: no dispatch must arm nothing, or no
// message would ever clear the flag.
func TestSubagentScanNoClaudeDoesNotLatch(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-aider", "aider", "x")
	m := homeWithAppState(t)

	require.Nil(t, m.maybeSubagentScan([]*session.Instance{inst}))
	require.False(t, m.subagentInFlight)
	require.True(t, m.lastSubagentScan.IsZero())
}

func TestSubagentScanMsgClearsInFlightOnEveryDelivery(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-clear", "claude", "x")
	m := homeWithAppState(t)

	m.subagentInFlight = true
	m.Update(subagentScanMsg{})
	require.False(t, m.subagentInFlight)

	m.subagentInFlight = true
	m.Update(subagentScanMsg{results: []subagentScanResult{
		{instance: inst, err: errors.New("disk on fire")},
		{instance: inst, err: subagent.ErrNoHooks},
	}})
	require.False(t, m.subagentInFlight, "errors must re-arm scanning too")
}

func explorerResult(launchID string, replayed bool, extra ...subagent.Event) subagent.Result {
	events := append([]subagent.Event{{
		Name: subagent.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: "/p/s.jsonl",
	}}, extra...)
	return subagent.Result{
		LaunchID: launchID,
		Replayed: replayed,
		Events:   events,
		Meta:     map[string]subagent.Meta{"a1": {AgentType: "Explore", Description: "map code"}},
	}
}

func TestSubagentScanMsgAppliesAndGatesByLaunch(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-apply", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	m.Update(subagentScanMsg{results: []subagentScanResult{{instance: inst, result: explorerResult("L1", true)}}})
	require.Equal(t, []subagent.View{{Name: "Explore", Description: "map code"}}, inst.Subagents())

	// A result from another launch, even one that would clear everything, is dropped.
	m.Update(subagentScanMsg{results: []subagentScanResult{{instance: inst, result: explorerResult("L2", true,
		subagent.Event{Name: subagent.EventSessionEnd})}}})
	require.Len(t, inst.Subagents(), 1)
}

// A warm instance whose hooks folder disappears mid-run drops its rows
// instead of keeping them frozen.
func TestSubagentScanMsgNoHooksForgetsWarmRows(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-gone", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.Update(subagentScanMsg{results: []subagentScanResult{{instance: inst, result: explorerResult("L1", true)}}})
	require.Len(t, inst.Subagents(), 1)

	m.Update(subagentScanMsg{results: []subagentScanResult{{instance: inst, err: subagent.ErrNoHooks}}})

	require.Empty(t, inst.Subagents())
	require.False(t, m.subagentInFlight)
}

func TestSubagentScanCmdEndToEnd(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-e2e", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	dir := session.SubagentHooksDir(inst.ConfigDir, inst.Title)
	_, err := subagent.Prepare(dir)
	require.NoError(t, err)
	root := t.TempDir()
	sub := filepath.Join(root, "sess", "subagents")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "agent-a1.meta.json"),
		[]byte(`{"agentType":"Explore","description":"map code"}`), 0o600))
	payload := fmt.Sprintf(`{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore","transcript_path":%q}`,
		filepath.Join(root, "sess.jsonl"))
	require.NoError(t, os.WriteFile(filepath.Join(subagent.EventsDir(dir), "1-1.json"), []byte(payload), 0o600))

	cmd := m.maybeSubagentScan([]*session.Instance{inst})
	require.NotNil(t, cmd)
	m.Update(cmd())

	require.Equal(t, []subagent.View{{Name: "Explore", Description: "map code"}}, inst.Subagents())
	require.False(t, m.subagentInFlight)
}

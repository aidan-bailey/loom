package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubagentScanDispatchesForClaude(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-first", "claude", "x")
	m := homeWithAppState(t)

	require.NotNil(t, m.maybeHookScan([]*session.Instance{inst}))
	require.True(t, m.gate(gateHookScan).inFlight)
}

func TestSubagentScanThrottledWithinInterval(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-window", "claude", "x")
	m := homeWithAppState(t)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeHookScan(active))
	m.gate(gateHookScan).inFlight = false
	require.Nil(t, m.maybeHookScan(active))

	m.gate(gateHookScan).last = time.Now().Add(-hookScanInterval - time.Second)
	require.NotNil(t, m.maybeHookScan(active))
}

func TestSubagentScanNotStackedWhileInFlight(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-inflight", "claude", "x")
	m := homeWithAppState(t)
	active := []*session.Instance{inst}

	require.NotNil(t, m.maybeHookScan(active))
	m.gate(gateHookScan).last = time.Now().Add(-hookScanInterval - time.Second)
	require.Nil(t, m.maybeHookScan(active))
}

// Same deadlock guard as the roster: no dispatch must arm nothing, or no
// message would ever clear the flag.
func TestSubagentScanNoClaudeDoesNotLatch(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-aider", "aider", "x")
	m := homeWithAppState(t)

	require.Nil(t, m.maybeHookScan([]*session.Instance{inst}))
	require.False(t, m.gate(gateHookScan).inFlight)
	require.True(t, m.gate(gateHookScan).last.IsZero())
}

func TestSubagentScanMsgClearsInFlightOnEveryDelivery(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-clear", "claude", "x")
	m := homeWithAppState(t)

	m.gate(gateHookScan).inFlight = true
	m.Update(gatedMsg{kind: gateHookScan, msg: hookScanMsg{}})
	require.False(t, m.gate(gateHookScan).inFlight)

	m.gate(gateHookScan).inFlight = true
	m.Update(gatedMsg{kind: gateHookScan, msg: hookScanMsg{results: []hookScanResult{
		{instance: inst, err: errors.New("disk on fire")},
		{instance: inst, err: hooks.ErrNoHooks},
	}}})
	require.False(t, m.gate(gateHookScan).inFlight, "errors must re-arm scanning too")
}

func explorerResult(launchID string, replayed bool, extra ...hooks.Event) session.HookScanResult {
	events := append([]hooks.Event{{
		Name: hooks.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: "/p/s.jsonl",
	}}, extra...)
	return session.HookScanResult{
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

	m.Update(hookScanMsg{results: []hookScanResult{{instance: inst, result: explorerResult("L1", true)}}})
	require.Equal(t, []subagent.View{{Name: "Explore", Description: "map code"}}, inst.Subagents())

	// A result from another launch, even one that would clear everything, is dropped.
	m.Update(hookScanMsg{results: []hookScanResult{{instance: inst, result: explorerResult("L2", true,
		hooks.Event{Name: hooks.EventSessionEnd})}}})
	require.Len(t, inst.Subagents(), 1)
}

// A warm instance whose hooks folder disappears mid-run drops its rows
// instead of keeping them frozen.
func TestSubagentScanMsgNoHooksForgetsWarmRows(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-gone", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.Update(hookScanMsg{results: []hookScanResult{{instance: inst, result: explorerResult("L1", true)}}})
	require.Len(t, inst.Subagents(), 1)

	m.gate(gateHookScan).inFlight = true
	m.Update(gatedMsg{kind: gateHookScan, msg: hookScanMsg{results: []hookScanResult{{instance: inst, err: hooks.ErrNoHooks}}}})

	require.Empty(t, inst.Subagents())
	require.False(t, m.gate(gateHookScan).inFlight)
}

func TestSubagentScanCmdEndToEnd(t *testing.T) {
	inst := startedInstanceWithProgram(t, "sub-e2e", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)

	dir := session.SubagentHooksDir(inst.ConfigDir, inst.Title)
	_, err := hooks.Prepare(dir)
	require.NoError(t, err)
	root := t.TempDir()
	sub := filepath.Join(root, "sess", "subagents")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "agent-a1.meta.json"),
		[]byte(`{"agentType":"Explore","description":"map code"}`), 0o600))
	payload := fmt.Sprintf(`{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore","transcript_path":%q}`,
		filepath.Join(root, "sess.jsonl"))
	require.NoError(t, os.WriteFile(filepath.Join(hooks.EventsDir(dir), "1-1.json"), []byte(payload), 0o600))

	cmd := m.maybeHookScan([]*session.Instance{inst})
	require.NotNil(t, cmd)
	m.Update(cmd())

	require.Equal(t, []subagent.View{{Name: "Explore", Description: "map code"}}, inst.Subagents())
	require.False(t, m.gate(gateHookScan).inFlight, "the gated delivery must disarm the scan")
}

func TestHookScanOnOutputHonoursInterval(t *testing.T) {
	inst := startedInstanceWithProgram(t, "scan-dirty", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)
	require.True(t, inst.HooksLaunched())

	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	require.True(t, m.gate(gateHookScan).inFlight, "output on a hooked session scans")

	m.gate(gateHookScan).inFlight = false
	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	assert.False(t, m.gate(gateHookScan).inFlight, "a second scan inside hookScanInterval is not dispatched")
}

func TestHookScanOnQuietIgnoresInterval(t *testing.T) {
	inst := startedInstanceWithProgram(t, "scan-quiet", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	require.NotNil(t, m.maybeHookScan(m.activeInstances()))

	m.Update(paneQuietMsg{session: inst.Pane().TmuxSessionName()})
	assert.True(t, m.gate(gateHookScan).pending, "a quiet during a scan asks for one more")

	m.gate(gateHookScan).inFlight, m.gate(gateHookScan).pending = false, false
	m.Update(paneQuietMsg{session: inst.Pane().TmuxSessionName()})
	assert.True(t, m.gate(gateHookScan).inFlight, "a quiet scans even inside hookScanInterval")
}

func TestHookScanStatusChangeMovesInstanceAndAsksRoster(t *testing.T) {
	inst := startedInstanceWithProgram(t, "scan-status", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	// The test instance starts on a mock tmux session, so no launch
	// prepared its folder; it adopts this one's launch ID, as a restored
	// instance would.
	dir := session.SubagentHooksDir(inst.ConfigDir, inst.Title)
	_, err := hooks.Prepare(dir)
	require.NoError(t, err)
	name := fmt.Sprintf("%d-1.json", time.Now().UnixNano())
	require.NoError(t, os.WriteFile(filepath.Join(hooks.EventsDir(dir), name),
		[]byte(`{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`), 0o600))

	cmd := m.maybeHookScan(m.activeInstances())
	require.NotNil(t, cmd)
	_, follow := m.Update(cmd())

	assert.Equal(t, session.Prompting, inst.GetStatus())
	assert.Equal(t, "permission: Bash", inst.WaitReason())
	require.NotNil(t, follow, "a status change asks the roster to confirm")
	assert.True(t, m.gate(gateRoster).inFlight)
}

package core

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
	inst := startedInst(t, "sub-first", "claude")
	m := NewForTest(Options{})

	require.True(t, m.maybeHookScan([]*session.Instance{inst}))
	require.True(t, m.gate(gateHookScan).inFlight)
}

func TestSubagentScanThrottledWithinInterval(t *testing.T) {
	inst := startedInst(t, "sub-window", "claude")
	m := NewForTest(Options{})
	active := []*session.Instance{inst}

	require.True(t, m.maybeHookScan(active))
	m.gate(gateHookScan).inFlight = false
	require.False(t, m.maybeHookScan(active))

	m.gate(gateHookScan).last = time.Now().Add(-hookScanInterval - time.Second)
	require.True(t, m.maybeHookScan(active))
}

func TestSubagentScanNotStackedWhileInFlight(t *testing.T) {
	inst := startedInst(t, "sub-inflight", "claude")
	m := NewForTest(Options{})
	active := []*session.Instance{inst}

	require.True(t, m.maybeHookScan(active))
	m.gate(gateHookScan).last = time.Now().Add(-hookScanInterval - time.Second)
	require.False(t, m.maybeHookScan(active))
}

// Same deadlock guard as the roster: no dispatch must arm nothing, or no
// result would ever clear the flag.
func TestSubagentScanNoClaudeDoesNotLatch(t *testing.T) {
	inst := startedInst(t, "sub-aider", "aider")
	m := NewForTest(Options{})

	require.False(t, m.maybeHookScan([]*session.Instance{inst}))
	require.False(t, m.gate(gateHookScan).inFlight)
	require.True(t, m.gate(gateHookScan).last.IsZero())
}

func TestSubagentScanResultClearsInFlightOnEveryDelivery(t *testing.T) {
	inst := startedInst(t, "sub-clear", "claude")
	m := NewForTest(Options{})

	m.gate(gateHookScan).inFlight = true
	m.Deliver(gatedResult{kind: gateHookScan, result: hookScanResults{}})
	require.False(t, m.gate(gateHookScan).inFlight)

	m.gate(gateHookScan).inFlight = true
	m.Deliver(gatedResult{kind: gateHookScan, result: hookScanResults{results: []hookScanResult{
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

func TestSubagentScanResultAppliesAndGatesByLaunch(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "sub-apply")

	m.Deliver(hookScanResults{results: []hookScanResult{{instance: inst, result: explorerResult("L1", true)}}})
	require.Equal(t, []subagent.View{{Name: "Explore", Description: "map code"}}, inst.Subagents())

	// A result from another launch, even one that would clear everything, is dropped.
	m.Deliver(hookScanResults{results: []hookScanResult{{instance: inst, result: explorerResult("L2", true,
		hooks.Event{Name: hooks.EventSessionEnd})}}})
	require.Len(t, inst.Subagents(), 1)
}

// A warm instance whose hooks folder disappears mid-run drops its rows
// instead of keeping them frozen.
func TestSubagentScanResultNoHooksForgetsWarmRows(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "sub-gone")
	m.Deliver(hookScanResults{results: []hookScanResult{{instance: inst, result: explorerResult("L1", true)}}})
	require.Len(t, inst.Subagents(), 1)

	m.gate(gateHookScan).inFlight = true
	m.Deliver(gatedResult{kind: gateHookScan, result: hookScanResults{results: []hookScanResult{{instance: inst, err: hooks.ErrNoHooks}}}})

	require.Empty(t, inst.Subagents())
	require.False(t, m.gate(gateHookScan).inFlight)
}

func TestSubagentScanJobEndToEnd(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "sub-e2e")

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

	dispatched := m.maybeHookScan([]*session.Instance{inst})
	require.True(t, dispatched)
	m.Deliver(m.Drain().Jobs[0]())

	require.Equal(t, []subagent.View{{Name: "Explore", Description: "map code"}}, inst.Subagents())
	require.False(t, m.gate(gateHookScan).inFlight, "the gated delivery must disarm the scan")
}

func TestHookScanOnOutputHonoursInterval(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "scan-dirty")
	require.True(t, inst.HooksLaunched())

	m.PaneOutput(inst)
	require.True(t, m.gate(gateHookScan).inFlight, "output on a hooked session scans")

	m.gate(gateHookScan).inFlight = false
	m.PaneOutput(inst)
	assert.False(t, m.gate(gateHookScan).inFlight, "a second scan inside hookScanInterval is not dispatched")
}

func TestHookScanOnQuietIgnoresInterval(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "scan-quiet")
	require.True(t, m.maybeHookScan(m.ActiveInstances()))

	m.PaneQuiet(inst)
	assert.True(t, m.gate(gateHookScan).pending, "a quiet during a scan asks for one more")

	m.gate(gateHookScan).inFlight, m.gate(gateHookScan).pending = false, false
	m.PaneQuiet(inst)
	assert.True(t, m.gate(gateHookScan).inFlight, "a quiet scans even inside hookScanInterval")
}

func TestHookScanStatusChangeMovesInstanceAndAsksRoster(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "scan-status")
	// The test instance starts on a mock tmux session, so no launch
	// prepared its folder; it adopts this one's launch ID, as a restored
	// instance would.
	dir := session.SubagentHooksDir(inst.ConfigDir, inst.Title)
	_, err := hooks.Prepare(dir)
	require.NoError(t, err)
	name := fmt.Sprintf("%d-1.json", time.Now().UnixNano())
	require.NoError(t, os.WriteFile(filepath.Join(hooks.EventsDir(dir), name),
		[]byte(`{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`), 0o600))

	dispatched := m.maybeHookScan(m.ActiveInstances())
	require.True(t, dispatched)
	m.Deliver(m.Drain().Jobs[0]())

	assert.Equal(t, session.Prompting, inst.GetStatus())
	assert.Equal(t, "permission: Bash", inst.WaitReason())
	require.NotEmpty(t, m.Drain().Jobs, "a status change asks the roster to confirm")
	assert.True(t, m.gate(gateRoster).inFlight)
}

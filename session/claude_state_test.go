package session

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)

func hookObs(st Status, reason string, dt time.Duration) observation {
	return observation{status: st, reason: reason, at: t0.Add(dt), source: obsHook, valid: true}
}

func rosterObs(st Status, reason string, dt time.Duration) observation {
	return observation{status: st, reason: reason, at: t0.Add(dt), source: obsRoster, valid: true}
}

// describe renders a state's status for table assertions: "-" for no
// opinion, else the status and any wait reason.
func describe(s claudeState) string {
	st, reason, ok := s.status()
	if !ok {
		return "-"
	}
	if reason != "" {
		return st.String() + ": " + reason
	}
	return st.String()
}

func TestClaudeState_NewestWinsAcrossSources(t *testing.T) {
	var s claudeState
	assert.True(t, s.offer(hookObs(Ready, "", 0)))
	assert.True(t, s.offer(rosterObs(Running, "", time.Second)), "a newer roster answer replaces a hook observation")
	assert.True(t, s.offer(hookObs(Ready, "", 2*time.Second)), "a newer hook event replaces a roster answer")
	assert.False(t, s.offer(rosterObs(Running, "", 1500*time.Millisecond)), "an older roster answer delivered late is dropped")
	assert.Equal(t, "Ready", describe(s))
}

func TestClaudeState_EqualTimeIsNotNewer(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Ready, "", 0))
	assert.False(t, s.offer(rosterObs(Running, "", 0)))
	assert.Equal(t, "Ready", describe(s))
}

func TestClaudeState_SameStatusIsNotAChange(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Running, "", 0))
	assert.False(t, s.offer(rosterObs(Running, "", time.Second)))
	assert.Equal(t, obsRoster, s.obs.source, "the newer observation is still stored")
}

func TestClaudeState_RosterKeepsHookReason(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Prompting, "permission: Bash", 0))
	assert.False(t, s.offer(rosterObs(Prompting, "permission prompt", time.Second)))
	assert.Equal(t, "Prompting: permission: Bash", describe(s), "the hook names the tool, the roster only the kind of wait")

	s.offer(rosterObs(Running, "", 2*time.Second))
	s.offer(rosterObs(Prompting, "sandbox request", 3*time.Second))
	assert.Equal(t, "Prompting: sandbox request", describe(s), "with no hook observation the roster's reason stands")
}

func TestClaudeState_NoOpinionStillOrdersByTime(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Ready, "", 0))
	assert.True(t, s.offer(observation{at: t0.Add(time.Second), source: obsHook}), "SessionEnd: no opinion")
	assert.Equal(t, "-", describe(s))
	assert.False(t, s.offer(rosterObs(Running, "", 500*time.Millisecond)),
		"an answer from before the SessionEnd must not revive a status")
}

func TestClaudeState_RosterSilence(t *testing.T) {
	var s claudeState
	s.offer(rosterObs(Running, "", 0))
	assert.False(t, s.rosterSilent(t0.Add(-time.Second)), "an older silence changes nothing")
	assert.True(t, s.rosterSilent(t0.Add(time.Second)))
	assert.Equal(t, "-", describe(s), "a roster status must not outlive the roster")

	s.offer(hookObs(Ready, "", 2*time.Second))
	assert.False(t, s.rosterSilent(t0.Add(3*time.Second)), "silence never voids a hook observation")
	assert.Equal(t, "Ready", describe(s))
}

func TestClaudeState_EventMapping(t *testing.T) {
	subagent := hooks.Task{ID: "a1", Type: "subagent", Status: "running"}
	cases := []struct {
		name string
		ev   hooks.Event
		want string
	}{
		{"startup", hooks.Event{Name: hooks.EventSessionStart, Source: "startup"}, "Ready"},
		{"resume", hooks.Event{Name: hooks.EventSessionStart, Source: "resume"}, "Ready"},
		{"clear", hooks.Event{Name: hooks.EventSessionStart, Source: "clear"}, "Ready"},
		{"compact runs mid-turn", hooks.Event{Name: hooks.EventSessionStart, Source: "compact"}, "-"},
		{"unknown source", hooks.Event{Name: hooks.EventSessionStart, Source: "teleport"}, "-"},
		{"prompt", hooks.Event{Name: hooks.EventUserPromptSubmit}, "Running"},
		{"subagent prompt ignored", hooks.Event{Name: hooks.EventUserPromptSubmit, AgentID: "a1"}, "-"},
		{"permission", hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Edit"}, "Prompting: permission: Edit"},
		{"subagent permission shows in the parent", hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", AgentID: "a1"}, "Prompting: permission: Bash"},
		{"permission without a tool", hooks.Event{Name: hooks.EventPermissionRequest}, "Prompting: permission"},
		{"elicitation", hooks.Event{Name: hooks.EventNotification, NotificationType: "elicitation_dialog", Message: "Claude needs your input"}, "Prompting: Claude needs your input"},
		{"idle notification", hooks.Event{Name: hooks.EventNotification, NotificationType: "idle_prompt"}, "-"},
		{"stop", hooks.Event{Name: hooks.EventStop, HasTasks: true}, "Ready"},
		{"stop without a task list", hooks.Event{Name: hooks.EventStop}, "Ready"},
		{"stop while a subagent runs", hooks.Event{Name: hooks.EventStop, HasTasks: true, Tasks: []hooks.Task{subagent}}, "Running"},
		{"stop after a subagent finished", hooks.Event{Name: hooks.EventStop, HasTasks: true, Tasks: []hooks.Task{{ID: "a1", Type: "subagent", Status: "completed"}}}, "Ready"},
		{"idle teammates stay listed as running", hooks.Event{Name: hooks.EventStop, HasTasks: true, Tasks: []hooks.Task{{ID: "tk", Type: "teammate", Status: "running"}}}, "Ready"},
		{"a background shell runs while Claude waits", hooks.Event{Name: hooks.EventStop, HasTasks: true, Tasks: []hooks.Task{{ID: "b1", Type: "shell", Status: "running"}}}, "Ready"},
		{"subagent stop", hooks.Event{Name: hooks.EventSubagentStop, AgentID: "a1"}, "-"},
		{"session end", hooks.Event{Name: hooks.EventSessionEnd}, "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s claudeState
			tc.ev.At = t0
			s.applyEvent(tc.ev)
			assert.Equal(t, tc.want, describe(s))
		})
	}
}

// The Notification for a prompt arrives about six seconds after its
// PermissionRequest, with a generic message; the specific reason stays.
func TestClaudeState_NotificationKeepsPermissionReason(t *testing.T) {
	var s claudeState
	s.applyEvent(hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: t0})
	s.applyEvent(hooks.Event{Name: hooks.EventNotification, NotificationType: "permission_prompt",
		Message: "Claude needs your permission", At: t0.Add(6 * time.Second)})
	assert.Equal(t, "Prompting: permission: Bash", describe(s))
}

func TestClaudeState_SessionIDOnlyFromParentSessionStart(t *testing.T) {
	var s claudeState
	s.applyEvent(hooks.Event{Name: hooks.EventSessionStart, Source: "startup", SessionID: "one", TranscriptPath: "/t/one.jsonl", At: t0})
	s.applyEvent(hooks.Event{Name: hooks.EventSessionStart, Source: "startup", SessionID: "sub", AgentID: "a1", At: t0.Add(time.Second)})
	s.applyEvent(hooks.Event{Name: hooks.EventSessionEnd, At: t0.Add(2 * time.Second)})
	assert.Equal(t, "one", s.sessionID)

	s.applyEvent(hooks.Event{Name: hooks.EventSessionStart, Source: "compact", SessionID: "two", TranscriptPath: "/t/two.jsonl", At: t0.Add(3 * time.Second)})
	assert.Equal(t, "two", s.sessionID, "compact records the ID even though it sets no status")
	assert.Equal(t, "/t/two.jsonl", s.transcriptPath)
}

func TestClaudeState_LastMessageWindow(t *testing.T) {
	var s claudeState
	s.applyEvent(hooks.Event{Name: hooks.EventStop, LastAssistantMessage: "done", At: t0})
	assert.Equal(t, "done", s.lastMessage)
	assert.True(t, s.lastMsgValid)

	s.applyEvent(hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: t0.Add(time.Second)})
	assert.False(t, s.lastMsgValid, "a permission request is mid-turn: the message belongs to the previous turn")

	s.applyEvent(hooks.Event{Name: hooks.EventStop, LastAssistantMessage: "again", At: t0.Add(2 * time.Second)})
	assert.True(t, s.lastMsgValid)
	s.applyEvent(hooks.Event{Name: hooks.EventUserPromptSubmit, At: t0.Add(3 * time.Second)})
	assert.False(t, s.lastMsgValid)
	assert.Equal(t, "again", s.lastMessage)
}

// probeEvents reads the 2026-09-23 probe's payloads in the order their
// hooks ran, stamped the way Scan stamps them.
func probeEvents(t *testing.T) []hooks.Event {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("hooks", "testdata", "probe-2.1.280", "*.json"))
	require.NoError(t, err)
	require.Len(t, files, 34)
	sort.Strings(files)
	events := make([]hooks.Event, 0, len(files))
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		ev, err := hooks.ParseEvent(data)
		require.NoError(t, err, f)
		prefix, _, _ := strings.Cut(filepath.Base(f), "-")
		n, err := strconv.ParseInt(prefix, 10, 64)
		require.NoError(t, err)
		ev.At = time.Unix(0, n)
		events = append(events, ev)
	}
	return events
}

// TestClaudeState_ProbeReplay pins what hooks alone conclude after each
// event of the live probe (see testdata/probe-2.1.280/README.md). Two
// entries are known to be wrong without a roster, and say so.
func TestClaudeState_ProbeReplay(t *testing.T) {
	want := []string{
		"Ready",            // SessionStart startup
		"Running", "Ready", // plain turn
		"Running", "Prompting: permission: Bash", "Prompting: permission: Bash", "Ready", // Bash prompt, its Notification, approved
		"Ready",              // an internal helper's SubagentStop
		"Running", "Running", // prompt, SubagentStart
		"Running",                                                    // parent Stop while the background subagent runs
		"Prompting: permission: Bash", "Prompting: permission: Bash", // the subagent's prompt and its Notification
		"Prompting: permission: Bash", "Prompting: permission: Bash", // SubagentStops: hooks cannot see the approval; the roster can
		"Running", "Ready", // the <task-notification> prompt resumes the parent; final Stop
		"Ready",      // helper SubagentStop
		"-", "Ready", // /clear: SessionEnd, SessionStart
		"Running", "Ready", "-", // a turn, then /exit
		"Ready",                                    // --resume
		"Running", "Running", "Running", "Running", // teammate: prompt, start, stop, idle
		"Ready", "Ready", // the lead's intermediate Stop (the roster corrects it) and its final Stop
		"Ready", "Ready", // helper SubagentStops
		"-", "-", // /exit, then a failed --resume
	}
	events := probeEvents(t)
	require.Len(t, want, len(events))

	var s claudeState
	for i, ev := range events {
		s.applyEvent(ev)
		assert.Equal(t, want[i], describe(s), "after event %d (%s)", i, ev.Name)
	}
	assert.Equal(t, "487f460e-49ff-4961-a883-6b2c290c9aec", s.sessionID,
		"the failed resume's SessionEnd carries another ID and must not replace this one")
	assert.Equal(t, "/home/user/.claude/projects/-probe-work/487f460e-49ff-4961-a883-6b2c290c9aec.jsonl", s.transcriptPath)
}

func TestApplyHookScan_DrivesClaudeState(t *testing.T) {
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventSessionStart, Source: "startup", SessionID: "s1", TranscriptPath: "/t/s1.jsonl", At: t0},
		{Name: hooks.EventUserPromptSubmit, At: t0.Add(time.Second)},
		{Name: hooks.EventStop, HasTasks: true, LastAssistantMessage: "done", At: t0.Add(2 * time.Second)},
	}}))

	st, _, ok := inst.ClaudeStatus()
	require.True(t, ok)
	assert.Equal(t, Ready, st)
	msg, valid := inst.LastMessage()
	assert.Equal(t, "done", msg)
	assert.True(t, valid)
	id, transcript := inst.ClaudeSession()
	assert.Equal(t, "s1", id)
	assert.Equal(t, "/t/s1.jsonl", transcript)

	assert.True(t, inst.ObserveRoster(Running, "", true, t0.Add(3*time.Second)))
	st, _, _ = inst.ClaudeStatus()
	assert.Equal(t, Running, st)
	assert.True(t, inst.ObserveRoster(Ready, "", false, t0.Add(4*time.Second)), "silence voids the roster's own status")
	_, _, ok = inst.ClaudeStatus()
	assert.False(t, ok)
}

func TestResetHookLaunch_KeepsConversationClearsStatus(t *testing.T) {
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventSessionStart, Source: "startup", SessionID: "s1", TranscriptPath: "/t/s1.jsonl", At: t0},
		{Name: hooks.EventStop, LastAssistantMessage: "done", At: t0.Add(time.Second)},
	}}))

	inst.resetHookLaunch()

	_, _, ok := inst.ClaudeStatus()
	assert.False(t, ok)
	_, valid := inst.LastMessage()
	assert.False(t, valid)
	id, _ := inst.ClaudeSession()
	assert.Equal(t, "s1", id, "the relaunch resumes this conversation, so the reset keeps it")
	assert.False(t, inst.ObserveRoster(Running, "", true, time.Now().Add(-time.Minute)),
		"a roster answer from before the relaunch must not revive the old process's status")
}

func TestForgetSubagentsWithoutHooks_ClearsHookState(t *testing.T) {
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "0123456789abcdef", Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventStop, LastAssistantMessage: "done", At: time.Now().Add(-time.Minute)},
	}}))

	inst.ForgetSubagentsWithoutHooks()

	_, _, ok := inst.ClaudeStatus()
	assert.False(t, ok, "nothing refreshes a hook observation once its folder is gone")
	_, valid := inst.LastMessage()
	assert.False(t, valid)
}

func TestHooksLaunched(t *testing.T) {
	assert.False(t, hooksInstance(t, "aider").HooksLaunched())
	inst := hooksInstance(t, "claude")
	assert.True(t, inst.HooksLaunched(), "a restored instance that has not adopted an ID yet counts")
	inst.hookLaunchID = noHooksLaunchID
	assert.False(t, inst.HooksLaunched())
	inst.hookLaunchID = "0123456789abcdef"
	assert.True(t, inst.HooksLaunched())
	inst.ConfigDir = ""
	assert.False(t, inst.HooksLaunched())
}

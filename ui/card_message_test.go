package ui

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageTailLines(t *testing.T) {
	msg := "# Summary\n\nDone:\n```go\nx := 1\n```\n- fixed #12\n## Next?\nShould I push?\n"
	assert.Equal(t, []string{"- fixed #12", "Next?", "Should I push?"}, MessageTailLines(msg, 3))
	assert.Equal(t, []string{"Should I push?"}, MessageTailLines(msg, 1))
	assert.Equal(t, []string{"#12 is fixed"}, MessageTailLines("#12 is fixed", 1), "an issue reference is not a heading")
	assert.Nil(t, MessageTailLines("```\n\n```", 2))
	assert.Nil(t, MessageTailLines("text", 0))

	got := MessageTailLines("\x1b]52;c;aGk=\x07pwn", 1)
	require.Len(t, got, 1)
	assert.NotContains(t, got[0], "\x1b", "model-written text must not reach the terminal as escapes")
}

func messageInstance(t *testing.T, events ...hooks.Event) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: "lm", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.True(t, inst.ApplyHookScan(session.HookScanResult{LaunchID: "L", Replayed: true, Events: events}))
	return inst
}

func TestBuildCardData_LastMessageReplacesTailWhenStopped(t *testing.T) {
	inst := messageInstance(t, hooks.Event{Name: hooks.EventStop, HasTasks: true,
		LastAssistantMessage: "All tests pass.\nShould I push?", At: time.Now()})
	require.NoError(t, inst.TransitionTo(session.Ready))

	d := BuildCardData(inst, false, "", 2)
	assert.Equal(t, []string{"All tests pass.", "Should I push?"}, d.TailLines)

	require.NoError(t, inst.TransitionTo(session.Running))
	d = BuildCardData(inst, false, "", 2)
	assert.Empty(t, d.TailLines, "a working session shows the live tail (none here: no emulator)")
}

func TestBuildCardData_PermissionRequestShowsLiveTail(t *testing.T) {
	now := time.Now()
	inst := messageInstance(t,
		hooks.Event{Name: hooks.EventStop, HasTasks: true, LastAssistantMessage: "Done!", At: now},
		hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: now.Add(time.Second)})
	require.NoError(t, inst.TransitionTo(session.Prompting))

	d := BuildCardData(inst, false, "", 2)
	assert.Empty(t, d.TailLines, "the stored message belongs to the previous turn; the dialog is on screen")
}

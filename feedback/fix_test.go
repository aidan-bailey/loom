package feedback

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFixTitle(t *testing.T) {
	assert.Equal(t, "fb-3-loom-job-run-after-refuses", FixTitle(3, "loom job run: --after refuses!"))
	assert.Equal(t, "fb-12-loom-session-send-wait-tim", FixTitle(12, "loom session send --wait timed out"))
	assert.Equal(t, "fb-4-caf-ok", FixTitle(4, "Café ok"), "non-ASCII letters drop out")
	assert.Equal(t, "fb-3", FixTitle(3, "!!!"))
	assert.Equal(t, "fb-7-abc", FixTitle(7, "abc-"))
	for _, title := range []string{"a-b-c-d-e-f-g-h-i-j-k-l-m-n-o", "x                              y"} {
		got := FixTitle(1, title)
		assert.LessOrEqual(t, len(got), FixTitleWidth)
		assert.NotEqual(t, byte('-'), got[len(got)-1])
	}
}

func TestFixPrompt_HoldsEveryNote(t *testing.T) {
	in := Empty()
	a := note(1, "--after refused", 0)
	a.From = Session{ID: 1, Title: "builder", Workspace: "loom", ClaudeSession: "c-123", Transcript: "/t/c-123.jsonl", Model: "claude-opus-5-5", Effort: "xhigh"}
	a.Build = "0.15.0 abc123"
	th, _ := in.OpenThread(in.Add(a), "loom job: --after refused")
	b := note(2, "after again", 3)
	b.From.Title = "tester"
	_ = in.Attach(in.Add(b), th)
	thread, _ := in.Thread(th)
	p := FixPrompt(in, thread)
	for _, want := range []string{"feedback thread 1, bug", "loom job: --after refused", "First check whether the base branch already fixes this",
		`from "builder" in loom`, "Loom build: 0.15.0 abc123", "Ran on: claude-opus-5-5 at xhigh effort", "Claude session c-123 at /t/c-123.jsonl", "--after refused: what happened",
		`from "tester" in loom`, "after again: what happened"} {
		assert.Contains(t, p, want)
	}
}

func TestModelEffort(t *testing.T) {
	assert.Equal(t, "opus at high effort", ModelEffort(Session{Model: "opus", Effort: "high"}))
	assert.Equal(t, "opus", ModelEffort(Session{Model: "opus"}))
	assert.Equal(t, "high effort", ModelEffort(Session{Effort: "high"}))
	assert.Empty(t, ModelEffort(Session{}))
}

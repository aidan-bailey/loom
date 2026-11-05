package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/feedback"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runFeedback runs `loom feedback args…`.
func runFeedback(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newFeedbackCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// seedInbox writes an inbox to the isolated global dir before the daemon
// boots: thread 1 (two notes from two sessions), thread 2 (closed), and
// note 4, which no thread holds. The grouper is off, so nothing moves.
func seedInbox(t *testing.T) feedback.Inbox {
	t.Helper()
	global := os.Getenv(config.EnvGlobalDir)
	require.NotEmpty(t, global, "isolateWorkspaces first")
	require.NoError(t, os.WriteFile(filepath.Join(global, config.ConfigFileName), []byte(`{"feedback":{"grouper":{"enabled":false}}}`), 0o600))
	at := time.Now().Add(-time.Hour).Round(0)
	in := feedback.Empty()
	n1 := in.Add(feedback.Note{Kind: feedback.KindBug, Title: "--after refused", Text: "loom job run --after 3 said no such job",
		From: feedback.Session{ID: 1, Title: "builder", Workspace: "ws-a", Model: "claude-opus-5-5", Effort: "xhigh"}, Build: "0.15.0 abc", At: at})
	t1, err := in.OpenThread(n1, "loom job run: --after refuses another session's job")
	require.NoError(t, err)
	require.NoError(t, in.Attach(in.Add(feedback.Note{Kind: feedback.KindBug, Title: "after again", Text: "same",
		From: feedback.Session{ID: 2, Title: "tester", Workspace: "ws-a"}, At: at.Add(time.Minute)}), t1))
	t2, err := in.OpenThread(in.Add(feedback.Note{Kind: feedback.KindDocs, Title: "old", Text: "x", From: feedback.Session{ID: 3}, At: at}), "")
	require.NoError(t, err)
	require.NoError(t, in.Close(t2))
	in.Add(feedback.Note{Kind: feedback.KindMissing, Title: "a loose note", Text: "y", From: feedback.Session{ID: 3, Title: "s3"}, At: at})
	require.NoError(t, feedback.Save(filepath.Join(global, "feedback"), in))
	return in
}

func TestFeedbackNote_CarriesTheLaunchAndEffort(t *testing.T) {
	t.Setenv(claudemod.EnvLaunch, "L1")
	t.Setenv(envEffort, "xhigh")
	n, err := feedbackNote("bug", "t", "x")
	require.NoError(t, err)
	assert.Equal(t, core.FeedbackNote{Kind: feedback.KindBug, Title: "t", Text: "x", Launch: "L1", Effort: "xhigh"}, n)

	t.Setenv(claudemod.EnvLaunch, "")
	_, err = feedbackNote("bug", "t", "x")
	assert.EqualError(t, err, "loom feedback add runs inside a session loom launched: $LOOM_LAUNCH is not set")
}

func TestFeedbackAdd_FlagsAreChecked(t *testing.T) {
	t.Setenv(claudemod.EnvLaunch, "L1")
	for name, args := range map[string][]string{
		"no kind":       {"add", "--title", "t", "--text", "x"},
		"no title":      {"add", "--kind", "bug", "--text", "x"},
		"no text":       {"add", "--kind", "bug", "--title", "t"},
		"text and file": {"add", "--kind", "bug", "--title", "t", "--text", "x", "--file", "f"},
	} {
		_, _, err := runFeedback(t, args...)
		assert.Error(t, err, name)
	}
}

func TestFeedbackAdd_AnUnknownLaunchIsTheDaemonsRefusal(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	serveDaemon(t, "true")
	t.Setenv(claudemod.EnvLaunch, "no-such-launch")
	_, _, err := runFeedback(t, "add", "--kind", "bug", "--title", "t", "--text", "x")
	assert.ErrorContains(t, err, "this launch is no session the loom daemon serves")
	t.Setenv(claudemod.EnvLaunch, "no-such-launch")
	_, _, err = runFeedback(t, "add", "--kind", "wrong", "--title", "t", "--text", "x")
	assert.Error(t, err)
}

func TestFeedbackList_RanksAndShowsLooseNotes(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	serveDaemon(t, "true")
	out, _, err := runFeedback(t, "list")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 4, out)
	assert.Regexp(t, `^ID +KIND +SESSIONS +NOTES +LAST +STATUS +TITLE$`, lines[0])
	assert.Regexp(t, `^1 +bug +2 +2 +59m +open +loom job run: --after refuses another session's job$`, lines[1])
	assert.Regexp(t, `^n4 +missing +1 +1 +1h +pending +a loose note$`, lines[2])
	assert.Equal(t, "grouper: turned off (feedback.grouper.enabled)", lines[3])

	out, _, err = runFeedback(t, "list", "--all")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^2 +docs +1 +1 +1h +closed +old$`, out)

	out, _, err = runFeedback(t, "list", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"thread": 1`)
}

func TestFeedbackShow_AThreadAndANote(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	serveDaemon(t, "true")
	out, _, err := runFeedback(t, "show", "1")
	require.NoError(t, err)
	for _, want := range []string{"thread 1 (bug, open, 2 sessions): loom job run: --after refuses another session's job",
		`n1 (bug) from "builder" in ws-a`, "ran on:   claude-opus-5-5 at xhigh effort", "loom:     0.15.0 abc",
		"loom job run --after 3 said no such job", `n2 (bug) from "tester" in ws-a`} {
		assert.Contains(t, out, want)
	}
	out, _, err = runFeedback(t, "show", "n4")
	require.NoError(t, err)
	assert.Contains(t, out, `n4 (missing) from "s3"`)
	assert.Contains(t, out, "pending")

	_, _, err = runFeedback(t, "show", "9")
	assert.EqualError(t, err, "thread 9 doesn't exist")
	_, _, err = runFeedback(t, "show", "x")
	assert.Error(t, err)
}

func TestFeedbackClose_ClosesAndRefusesTwice(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	serveDaemon(t, "true")
	out, _, err := runFeedback(t, "close", "1")
	require.NoError(t, err)
	assert.Equal(t, "Closed thread 1\n", out)
	_, _, err = runFeedback(t, "close", "1")
	assert.ErrorContains(t, err, "thread 1 is already closed")
	_, _, err = runFeedback(t, "close", "n4")
	assert.ErrorContains(t, err, "only a thread can be fixed or closed")
}

func TestFeedbackFix_StartsTheSession(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)

	out, _, err := runFeedback(t, "fix", "1", "-w", "ws-a")

	require.NoError(t, err)
	title := feedback.FixTitle(1, "loom job run: --after refuses another session's job")
	assert.Contains(t, out, `Started "`+title+`" in ws-a`)
	_, ok := d.view(t, "ws-a", title)
	assert.True(t, ok)
	out, _, err = runFeedback(t, "list")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^1 +bug +2 +2 +59m +fixing `+title+` `, out)

	_, _, err = runFeedback(t, "fix", "1", "-w", "ws-a")
	assert.ErrorContains(t, err, "is still there", "a live fix session")
}

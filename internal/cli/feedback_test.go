package cli

import (
	"bytes"
	"encoding/json"
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
	require.NoError(t, in.Close(t2, "", at))
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
	assert.ErrorContains(t, err, "only a thread can be fixed, published or closed")
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

func TestFeedbackFix_LaunchesOnTheModelAndEffortAsked(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)

	_, _, err := runFeedback(t, "fix", "1", "-w", "ws-a", "--model", "gpt")
	assert.ErrorContains(t, err, `model "gpt": want one of default`)
	_, _, err = runFeedback(t, "fix", "1", "-w", "ws-a", "--effort", "huge")
	assert.ErrorContains(t, err, `effort "huge"`)
	title := feedback.FixTitle(1, "loom job run: --after refuses another session's job")
	_, ok := d.view(t, "ws-a", title)
	require.False(t, ok, "a refused fix starts nothing")

	_, _, err = runFeedback(t, "fix", "1", "-w", "ws-a", "--model", "opus", "--effort", "high")
	require.NoError(t, err)
	v, ok := d.view(t, "ws-a", title)
	require.True(t, ok)
	assert.Contains(t, v.Program, "--model 'opus'")
	assert.Contains(t, v.Program, "--effort high")
}

func TestFeedbackPublish_LinksTheThreadAndListShowsIt(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	serveDaemon(t, "true")
	const want = "[loom] thread 1 published as aidan-bailey/loom#57\n"
	out, _, err := runFeedback(t, "publish", "1", "--issue", "aidan-bailey/loom#57", "--through", "n2")
	require.NoError(t, err)
	assert.Equal(t, want, out)

	out, _, err = runFeedback(t, "list")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^1 +bug +2 +2 +\S+ +published #57 +loom job run: --after refuses another session's job$`, out)

	out, _, err = runFeedback(t, "publish", "1", "--issue", "aidan-bailey/loom#57", "--through", "n2")
	require.NoError(t, err, "a retry")
	assert.Equal(t, want, out)

	out, _, err = runFeedback(t, "publish", "1", "--issue", "https://github.com/aidan-bailey/loom/issues/57", "--through", "n1")
	require.NoError(t, err, "the URL form of the same issue")
	assert.Equal(t, want, out)
	out, _, err = runFeedback(t, "list")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^1 +bug +2 +2 +\S+ +published #57 +loom job`, out)
	assert.NotContains(t, out, " new")
}

func TestFeedbackPublish_ShowsNotesPastThroughAsNew(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	serveDaemon(t, "true")
	_, _, err := runFeedback(t, "publish", "1", "--issue", "aidan-bailey/loom#57", "--through", "n1")
	require.NoError(t, err)
	out, _, err := runFeedback(t, "list")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^1 +bug +2 +2 +\S+ +published #57 \+1 new +loom job`, out)
}

func TestFeedbackPublish_RefusalsAreTheModels(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	serveDaemon(t, "true")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "--issue", "aidan-bailey/loom#57", "--through", "n4"}, "note n4 is not in thread 1"},
		{[]string{"2", "--issue", "aidan-bailey/loom#57", "--through", "n3"}, "thread 2 is already closed"},
		{[]string{"9", "--issue", "aidan-bailey/loom#57", "--through", "n1"}, "thread 9 doesn't exist"},
	} {
		out, _, err := runFeedback(t, append([]string{"publish"}, c.args...)...)
		assert.ErrorContains(t, err, c.want)
		assert.Empty(t, out)
	}
	_, _, err := runFeedback(t, "publish", "1", "--issue", "aidan-bailey/loom#57", "--through", "n1")
	require.NoError(t, err)
	out, _, err := runFeedback(t, "publish", "1", "--issue", "aidan-bailey/loom#58", "--through", "n1")
	assert.ErrorContains(t, err, "thread 1 is linked to aidan-bailey/loom#57")
	assert.Empty(t, out)
}

func TestFeedbackPublish_ChecksItsArgumentsBeforeTheDaemon(t *testing.T) {
	isolateWorkspaces(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "--issue", "not-a-ref", "--through", "n1"}, "issue reference not-a-ref: want https://github.com/<owner>/<name>/issues/<N> or <owner>/<name>#<N>"},
		{[]string{"1", "--issue", "aidan-bailey/loom#57"}, `required flag(s) "through" not set`},
		{[]string{"1", "--through", "n1"}, `"issue"`},
		{[]string{"n4", "--issue", "aidan-bailey/loom#57", "--through", "n1"}, "only a thread can be fixed, published or closed"},
		{[]string{"1", "--issue", "aidan-bailey/loom#57", "--through", "1"}, `--through: "1" is not a note`},
	} {
		_, _, err := runFeedback(t, append([]string{"publish"}, c.args...)...)
		assert.ErrorContains(t, err, c.want, c.args)
	}
}

func TestFeedbackClose_StoresTheReason(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	serveDaemon(t, "true")
	out, _, err := runFeedback(t, "close", "1", "--reason", "fixed in 0.16")
	require.NoError(t, err)
	assert.Equal(t, "Closed thread 1\n", out)

	out, _, err = runFeedback(t, "list", "--all", "--json")
	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &rows))
	var found bool
	for _, r := range rows {
		if r["thread"] == float64(1) {
			found = true
			assert.Equal(t, "fixed in 0.16", r["closed"].(map[string]any)["reason"])
		}
	}
	assert.True(t, found, out)

	_, _, err = runFeedback(t, "close", "1")
	assert.ErrorContains(t, err, "thread 1 is already closed")
}

func TestFeedbackClose_TheModelChecksTheReason(t *testing.T) {
	isolateWorkspaces(t)
	seedInbox(t)
	serveDaemon(t, "true")
	_, _, err := runFeedback(t, "close", "1", "--reason", strings.Repeat("x", 301))
	assert.ErrorContains(t, err, "the close reason is 301 bytes: at most 300")
	_, _, err = runFeedback(t, "close", "1", "--reason", "a\nb")
	assert.ErrorContains(t, err, "the close reason must be one line")
	_, _, err = runFeedback(t, "close", "1", "--reason", strings.Repeat("x", 300))
	assert.NoError(t, err, "the refusals left thread 1 open")
}

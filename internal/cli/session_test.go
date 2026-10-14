package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSession runs `loom session args…` with stdin, on a fresh command tree
// (newSessionCmd), so no flag carries over from another test.
func runSession(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newSessionCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// pausedRecords are two Paused sessions, alpha and beta, as state.json
// holds them.
const pausedRecords = `[{"title":"alpha","status":3,"program":"true","branch":"me/alpha","worktree":{"worktree_path":"/tmp/wt-alpha"}},` +
	`{"title":"beta","status":3,"program":"true","worktree":{"worktree_path":"/tmp/wt-beta"}}]`

func TestSessionList_ShowsTheWorkspacesSessions(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	registered(t, "ws-b", `[{"title":"other","status":3,"program":"true"}]`)
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a")

	require.NoError(t, err)
	assert.Regexp(t, `TITLE\s+STATUS\s+BRANCH\s+ACCOUNT\s+SINCE`, out)
	assert.Regexp(t, `alpha\s+Paused\s+me/alpha`, out)
	assert.Regexp(t, `beta\s+Paused\s+-`, out)
	assert.NotContains(t, out, "other", "another workspace's session")
}

func TestSessionList_TheWorkingDirectoryPicksTheWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	ws := registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")
	t.Chdir(ws.Path)

	out, _, err := runSession(t, "", "list")

	require.NoError(t, err)
	assert.Contains(t, out, "alpha")
}

func TestSessionList_RefusesADirectoryNoWorkspaceHolds(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")
	t.Chdir(t.TempDir())

	_, _, err := runSession(t, "", "list")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workspace matches current directory")
}

func TestSessionList_RefusesAnUnknownWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	serveDaemon(t, "true")

	_, _, err := runSession(t, "", "list", "-w", "nope")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `workspace "nope" not found`)
}

func TestSessionList_SaysWhenThereAreNone(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, "No sessions in ws-a")
}

// The JSON's field names are a contract (scrum's board reads them).
func TestSessionList_JSONFieldNames(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a", "--json")

	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &rows))
	require.Len(t, rows, 2)
	var keys []string
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert.Equal(t, []string{"account", "branch", "id", "issue", "pr", "program", "repo", "status", "status_since",
		"title", "wait_reason", "worktree"}, keys)
	assert.Equal(t, "alpha", rows[0]["title"])
	assert.Equal(t, "Paused", rows[0]["status"])
	assert.Equal(t, "me/alpha", rows[0]["branch"])
	assert.NotZero(t, rows[0]["id"])
}

func TestSessionList_JSONOfNoSessionsIsAnEmptyArray(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")

	out, _, err := runSession(t, "", "list", "-w", "ws-a", "--json")

	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestFindSession(t *testing.T) {
	views := []core.InstanceView{
		{ID: 1, Title: "ws-a", IsWorkspaceTerminal: true},
		{ID: 2, Title: "alpha"},
		{ID: 3, Title: "twin"},
		{ID: 4, Title: "twin"},
	}
	for _, tc := range []struct {
		name   string
		title  string
		id     core.InstanceID
		want   core.InstanceID
		errHas string
	}{
		{"an exact title", "alpha", 0, 2, ""},
		{"never a prefix", "alp", 0, 0, `no session "alp"`},
		{"never the workspace terminal", "ws-a", 0, 0, `no session "ws-a"`},
		{"a shared title is refused", "twin", 0, 0, "2 sessions are titled \"twin\": name one with --id"},
		{"--id picks among them", "twin", 4, 4, ""},
		{"--id must match the title", "alpha", 4, 0, `no session "alpha" with id 4`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findSession(views, tc.title, tc.id)
			if tc.errHas != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errHas)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.ID)
		})
	}
}

func TestSince(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "-", since(time.Time{}, now))
	assert.Equal(t, "<1m", since(now.Add(-30*time.Second), now))
	assert.Equal(t, "5m", since(now.Add(-5*time.Minute), now))
	assert.Equal(t, "3h", since(now.Add(-3*time.Hour), now))
	assert.Equal(t, "2d", since(now.Add(-50*time.Hour), now))
}

// A started session: `new` returns once it runs, its prompt sent.
func TestSessionNew_ReturnsOnceTheSessionRuns(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)

	out, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a", "--prompt", "hello there")

	require.NoError(t, err)
	assert.Contains(t, out, `Started "alpha" in ws-a`)
	v, ok := d.view(t, "ws-a", "alpha")
	require.True(t, ok)
	assert.NotEqual(t, session.Loading, v.Status, "it returned before the start finished")
	assert.NotEmpty(t, v.WorktreePath)
	assert.True(t, tmuxHas("alpha"))
	assert.Eventually(t, func() bool {
		out, err := tmux.Command(context.Background(), "capture-pane", "-p", "-t", tmux.PaneTarget(tmux.ToLoomTmuxName("alpha"))).Output()
		return err == nil && strings.Contains(string(out), "hello there")
	}, 5*time.Second, 50*time.Millisecond, "the prompt reached the agent's pane")
}

// A start that fails is the command's failure, and leaves no session.
func TestSessionNew_AFailedStartFailsAndLeavesNoSession(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)

	out, errOut, err := runSession(t, "", "new", "alpha", "-w", "ws-a", "--branch", "no-such-branch")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-branch", "the daemon's error for the start")
	assert.NotContains(t, out, "Usage:", "a failure is loom's, not a usage mistake")
	assert.Empty(t, errOut, "main prints the error, once")
	assert.Eventually(t, func() bool {
		_, ok := d.view(t, "ws-a", "alpha")
		return !ok
	}, 5*time.Second, 50*time.Millisecond, "the daemon removed the failed start")
}

// An interrupt while `new` waits ends the wait at once, not the start:
// the daemon carries on. A post-checkout hook holds the start's worktree
// setup for seconds, so the interrupt lands mid-wait.
func TestSessionNew_AnInterruptLeavesTheDaemonStarting(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	ws := liveWorkspace(t, "ws-a", agent, nil)
	hook := filepath.Join(ws.Path, ".git", "hooks", "post-checkout")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nsleep 3\n"), 0o755))
	d := serveDaemonLive(t, agent)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)

	cmd := newSessionCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"new", "alpha", "-w", "ws-a"})
	began := time.Now()
	err := cmd.ExecuteContext(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `interrupted: the loom daemon is still starting "alpha"`)
	assert.Less(t, time.Since(began), 2500*time.Millisecond, "the interrupt ended the wait, not the start")
	assert.Eventually(t, func() bool {
		v, ok := d.view(t, "ws-a", "alpha")
		return ok && v.Status != session.Loading && tmuxHas("alpha")
	}, 10*time.Second, 50*time.Millisecond)
}

// What `new` refuses, it refuses before asking the daemon for anything.
func TestSessionNew_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"a title the workspace holds", []string{"alpha"}, `ws-a already has a session titled "alpha"`},
		{"a preserved title", []string{"future"}, `title "future" belongs to a saved session`},
		{"an empty title", []string{""}, "title cannot be empty"},
		{"a title too wide", []string{strings.Repeat("a", 33)}, "title cannot be longer than 32 characters"},
		{"an unknown model", []string{"gamma", "--model", "gpt"}, `--model "gpt": want one of default, sonnet`},
		{"an unknown permission mode", []string{"gamma", "--permission-mode", "yolo"}, `--permission-mode "yolo"`},
		{"an unknown effort", []string{"gamma", "--effort", "huge"}, `--effort "huge"`},
		{"both prompt flags", []string{"gamma", "--prompt", "x", "--prompt-file", "y"}, "prompt"},
		{"an unregistered account", []string{"gamma", "--account", "nope"}, `account "nope" is not registered`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateWorkspaces(t)
			registered(t, "ws-a", `[{"title":"alpha","status":3,"program":"true"},{"schema_version":9999,"title":"future"}]`)
			d := serveDaemon(t, fakeClaude(t, ""))

			_, _, err := runSession(t, "", append([]string{"new", "-w", "ws-a"}, tc.args...)...)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			views := d.client.Views(d.workspace(t, "ws-a").ID)
			assert.Len(t, views, 1, "nothing was created")
		})
	}
}

// Remote control on, for an account whose login can't use it: `new`
// refuses with the reason, and --no-remote-control starts it without.
// manual is Claude's ask mode; "default" passes no flag and leaves the mode
// to Claude, whose own default is auto.
func TestSessionNew_AcceptsManualPermissionMode(t *testing.T) {
	assert.NoError(t, newFlags{permissionMode: "manual"}.validate())
}

func TestSessionNew_RemoteControlBlocked(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "console")
	on := true
	liveWorkspace(t, "ws-a", agent, func(c *config.Config) { c.ClaudeRemoteControl = &on })
	d := serveDaemonLive(t, agent)
	require.Eventually(t, func() bool {
		require.NoError(t, d.client.FlushForTest())
		d.client.Sync()
		return d.client.RCAuthFor(account.DefaultName).Blocked()
	}, 10*time.Second, 50*time.Millisecond, "fixture: the daemon learned the login can't use remote control")

	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote control is unavailable: you're authenticated with a non-claude.ai account")
	assert.Contains(t, err.Error(), "--no-remote-control")
	_, ok := d.view(t, "ws-a", "alpha")
	assert.False(t, ok, "nothing was created")

	_, _, err = runSession(t, "", "new", "alpha", "-w", "ws-a", "--no-remote-control")
	require.NoError(t, err)
	assert.True(t, tmuxHas("alpha"))
}

// The workspace's settings are the defaults; the flags override them; a
// non-Claude program takes no account.
func TestLaunchOptions_SettingsThenFlags(t *testing.T) {
	isolateWorkspaces(t)
	plan := "plan"
	ws := registered(t, "ws-a", "")
	cfg := config.DefaultConfig()
	cfg.ClaudePermissionMode = &plan
	require.NoError(t, config.SaveConfigTo(cfg, config.WorkspaceConfigDir(&ws)))
	d := serveDaemon(t, fakeClaude(t, ""))
	v := d.workspace(t, "ws-a")
	var warn bytes.Buffer
	c := &client{Client: d.client, errOut: &warn, ctx: context.Background()}

	opts, err := c.launchOptions(v, "claude", newFlags{})
	require.NoError(t, err)
	assert.Equal(t, "plan", opts.PermissionMode, "the workspace's setting")

	opts, err = c.launchOptions(v, "claude", newFlags{permissionMode: "acceptEdits", model: "opus", effort: "high"})
	require.NoError(t, err)
	assert.Equal(t, "acceptEdits", opts.PermissionMode)
	assert.Equal(t, "opus", opts.Model)
	assert.Equal(t, "high", opts.Effort)

	opts, err = c.launchOptions(v, "aider", newFlags{account: "work"})
	require.NoError(t, err)
	assert.Empty(t, opts.Account)
	assert.Contains(t, warn.String(), "--account ignored")
}

func TestSessionKill_AsksFirst(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)

	for _, answer := range []string{"n\n", ""} {
		out, _, err := runSession(t, answer, "kill", "alpha", "-w", "ws-a")
		require.Error(t, err, "answer %q", answer)
		assert.Contains(t, err.Error(), "aborted: nothing was killed")
		assert.Contains(t, out, "Kill session 'alpha' in ws-a? [y/N]")
		assert.True(t, tmuxHas("alpha"), "answer %q killed it", answer)
	}

	out, _, err := runSession(t, "y\n", "kill", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	assert.Contains(t, out, `Killed "alpha" in ws-a`)
	assert.False(t, tmuxHas("alpha"))
	_, ok := d.view(t, "ws-a", "alpha")
	assert.False(t, ok)
}

func TestSessionKill_YesSkipsTheQuestion(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)

	out, _, err := runSession(t, "", "kill", "alpha", "-w", "ws-a", "-y")

	require.NoError(t, err)
	assert.NotContains(t, out, "[y/N]")
	assert.False(t, tmuxHas("alpha"))
}

func TestSessionPauseResume_RoundTrip(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)

	out, _, err := runSession(t, "", "pause", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	assert.Contains(t, out, `Paused "alpha" in ws-a`)
	v, _ := d.view(t, "ws-a", "alpha")
	assert.Equal(t, session.Paused, v.Status)
	assert.False(t, tmuxHas("alpha"))

	_, _, err = runSession(t, "", "pause", "alpha", "-w", "ws-a")
	require.Error(t, err, "a paused session can't be paused")
	assert.Contains(t, err.Error(), "the session is not running (Paused)")

	out, _, err = runSession(t, "", "resume", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	assert.Contains(t, out, `Resumed "alpha" in ws-a`)
	v, _ = d.view(t, "ws-a", "alpha")
	assert.NotEqual(t, session.Paused, v.Status)
	assert.True(t, tmuxHas("alpha"))

	_, _, err = runSession(t, "", "resume", "alpha", "-w", "ws-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the session is not paused")
}

func TestSessionOps_NameASessionExactly(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")

	_, _, err := runSession(t, "", "resume", "alp", "-w", "ws-a")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `no session "alp"`)
}

// launchOf is the hooks launch ID of the session title in ws.
func launchOf(t *testing.T, d *testDaemon, ws, title string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(session.SubagentHooksDir(d.workspace(t, ws).ConfigDir, title), "launch-id"))
	require.NoError(t, err)
	return string(b)
}

// playMod answers launch's inbox as the loom mod would, once: it reports the
// first message queued and, unless reason is "", its turn started and ended
// for reason with answer, and hands back the text it carried.
func playMod(t *testing.T, host core.ModHost, launch, answer, reason string) <-chan string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			msgs, wake, err := host.ModInbox(launch)
			if err != nil || wake == nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if len(msgs) == 0 {
				select {
				case <-wake:
				case <-time.After(time.Second):
				}
				continue
			}
			m := msgs[0]
			events := []core.ModEvent{{Kind: "queued", ID: m.ID}}
			if reason != "" {
				events = append(events,
					core.ModEvent{Kind: "started", ID: m.ID, TurnID: "t1"},
					core.ModEvent{Kind: "complete", ID: m.ID, TurnID: "t1", Text: answer, Reason: reason})
			}
			_ = host.ModEvents(launch, events)
			got <- m.Text
			return
		}
		got <- ""
	}()
	return got
}

// withModSocket has the test's daemon give launches the mod.
func withModSocket(t *testing.T) {
	t.Helper()
	session.SetModSocket("/tmp/loom-cli-test.mod.sock")
	t.Cleanup(func() { session.SetModSocket("") })
}

// modSession starts alpha in ws-a on a daemon that gives launches the mod,
// and has its mod say hello: the daemon counts it live.
func modSession(t *testing.T) (*testDaemon, string) {
	t.Helper()
	isolateWorkspaces(t)
	withModSocket(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	launch := launchOf(t, d, "ws-a", "alpha")
	require.NoError(t, d.loop.ModHello(launch, core.ModHello{Mod: 1}))
	return d, launch
}

func TestSessionSend_WaitPrintsTheAnswer(t *testing.T) {
	d, launch := modSession(t)
	carried := playMod(t, d.loop, launch, "all green", "answer")

	out, _, err := runSession(t, "", "send", "alpha", "-w", "ws-a", "--text", "status?", "--from", "po", "--wait")

	require.NoError(t, err)
	assert.Equal(t, "all green\n", out, "only the answer on stdout")
	assert.Equal(t, "From po:\n\nstatus?", <-carried)
}

func TestSessionSend_ReturnsOnceQueued(t *testing.T) {
	d, launch := modSession(t)
	carried := playMod(t, d.loop, launch, "", "")

	out, _, err := runSession(t, "from stdin\n\n", "send", "alpha", "-w", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, `Queued for "alpha" in ws-a`)
	assert.Equal(t, "from stdin", <-carried, "stdin's text, trailing newlines dropped, no From line")
}

// An interrupt while --wait waits for the turn ends the wait, not the
// delivery: the message was queued, and the command says so.
func TestSessionSend_AnInterruptAfterQueuedSaysItWasDelivered(t *testing.T) {
	d, launch := modSession(t)
	carried := playMod(t, d.loop, launch, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-carried
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	cmd := newSessionCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"send", "alpha", "-w", "ws-a", "--text", "hi", "--wait"})
	err := cmd.ExecuteContext(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `interrupted: the message to "alpha" was delivered; its reply will not be printed`)
}

// A turn that ends without an answer (aborted, an error) prints what it
// said and fails, naming why.
func TestSessionSend_WaitFailsWhenTheTurnEndsWithoutAnAnswer(t *testing.T) {
	d, launch := modSession(t)
	playMod(t, d.loop, launch, "partial", "aborted")

	out, _, err := runSession(t, "", "send", "alpha", "-w", "ws-a", "--text", "go", "--wait")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "the turn ended: aborted")
	assert.Equal(t, "partial\n", out)
}

// An interrupt before the mod has queued the message ends the wait, and says
// the message may still be delivered: the daemon still holds it.
func TestSessionSend_AnInterruptBeforeQueuedSaysItMayStillBeDelivered(t *testing.T) {
	modSession(t) // its mod never polls: the message waits in the daemon
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(300*time.Millisecond, cancel)

	cmd := newSessionCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"send", "alpha", "-w", "ws-a", "--text", "hi", "--wait"})
	err := cmd.ExecuteContext(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `interrupted: the message to "alpha" may still be delivered`)
}

// Losing the daemon after the message was queued, while --wait waits for
// the turn, says the message was delivered.
func TestSessionSend_LosingTheDaemonAfterQueuedSaysItWasDelivered(t *testing.T) {
	d, launch := modSession(t)
	carried := playMod(t, d.loop, launch, "", "")
	go func() {
		<-carried
		time.Sleep(200 * time.Millisecond)
		d.srv.Close()
	}()

	_, _, err := runSession(t, "", "send", "alpha", "-w", "ws-a", "--text", "hi", "--wait")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `the message to "alpha" was delivered, but its reply will not be printed`)
	assert.Contains(t, err.Error(), "lost the loom daemon")
}

// A session that stops after its mod queued the message ends --wait's wait
// with the daemon's Notice, saying the message was delivered.
func TestSessionSend_AStopAfterQueuedSaysItWasDelivered(t *testing.T) {
	d, launch := modSession(t)
	v, ok := d.view(t, "ws-a", "alpha")
	require.True(t, ok)
	carried := playMod(t, d.loop, launch, "", "")
	go func() {
		<-carried
		d.client.Pause(v.ID, 0)
	}()

	_, _, err := runSession(t, "", "send", "alpha", "-w", "ws-a", "--text", "hi", "--wait")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `the message to "alpha" was delivered, but its reply will not be printed`)
	assert.Contains(t, err.Error(), "the session stopped before its turn ended")
}

// With no daemon serving the mod the launch has none, and send refuses at
// once rather than paste into the pane.
func TestSessionSend_RefusesWithoutAMod(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)

	_, _, err = runSession(t, "", "send", "alpha", "-w", "ws-a", "--text", "hi", "--wait")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "this loom daemon serves no loom mod")
	assert.NotContains(t, err.Error(), "delivered", "a refusal is no delivery")
}

func TestSessionSend_RefusesAnEmptyMessage(t *testing.T) {
	_, _, err := runSession(t, "\n\n", "send", "alpha")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the message is empty")
}

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runWatch runs `loom watch args…` on a fresh command tree (newWatchCmd), so
// no flag carries over from another test.
func runWatch(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newWatchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestWatchOnOff_SwitchTheNamedWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	registered(t, "ws-b", "")
	d := serveDaemon(t, "true")

	out, _, err := runWatch(t, "on", "-w", "ws-a")

	require.NoError(t, err)
	assert.Equal(t, "watch on: ws-a\n", out)
	assert.True(t, d.workspace(t, "ws-a").Watch.On, "the named workspace")
	assert.False(t, d.workspace(t, "ws-b").Watch.On, "another workspace")

	out, _, err = runWatch(t, "off", "-w", "ws-a")

	require.NoError(t, err)
	assert.Equal(t, "watch off: ws-a\n", out)
	assert.False(t, d.workspace(t, "ws-a").Watch.On)
}

func TestWatchOn_TheWorkingDirectoryPicksTheWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	ws := registered(t, "ws-a", "")
	d := serveDaemon(t, "true")
	t.Chdir(ws.Path)

	out, _, err := runWatch(t, "on")

	require.NoError(t, err)
	assert.Equal(t, "watch on: ws-a\n", out)
	assert.True(t, d.workspace(t, "ws-a").Watch.On)
}

func TestWatchOn_RefusesWhatNoWorkspaceHolds(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	d := serveDaemon(t, "true")

	_, _, err := runWatch(t, "on", "-w", "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `workspace "nope" not found`)

	t.Chdir(t.TempDir())
	_, _, err = runWatch(t, "on")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workspace matches current directory")

	assert.False(t, d.workspace(t, "ws-a").Watch.On, "a refused command switches nothing on")
}

func TestWatchOnOff_TakeNoArgumentsAndNoJSONFlag(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")

	_, _, err := runWatch(t, "on", "extra", "-w", "ws-a")
	require.Error(t, err)

	_, _, err = runWatch(t, "off", "--json", "-w", "ws-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --json")
}

// sampleWatchView is a watcher mid-flight: issue 12 has a session and a PR
// with two ci nudges and one conflict nudge sent, a third ci nudge due and
// its last action failed,
// issue 13 waits for room, and issue 14 is closed, escalated, and its session
// to be killed.
func sampleWatchView() core.WatchView {
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	return core.WatchView{
		On:           true,
		Viewer:       "octocat",
		Considered:   3,
		Unconsidered: 1,
		Issues: []core.WatchIssueView{
			{Number: 12, State: "open", Session: "heal 12", PR: 34, Failed: "send failed: pane gone", Nudges: map[string]int{"ci": 2, "conflict": 1}},
			{Number: 13, State: "open", Waiting: "the cap is full"},
			{Number: 14, State: "closed", Session: "heal 14", Escalated: true},
		},
		Actions: []watch.Action{
			{Kind: watch.ActSend, Issue: 12, Session: 7, PR: 34, Nudge: watch.Nudge{Kind: "ci", Key: "abc"}, N: 3},
			{Kind: watch.ActWait, Issue: 13, Reason: "the cap is full"},
			{Kind: watch.ActKill, Issue: 14, Session: 9},
		},
		Escalations: []core.WatchEscalation{{Issue: 14, Reason: "PR #40: ci nudge limit (3) reached", At: at}},
	}
}

func TestWriteWatchStatus_ShowsEachIssueItsDecisionAndWhyItWaits(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, writeWatchStatus(&out, "ws-a", sampleWatchView()))

	got := out.String()
	assert.Contains(t, got, "Watch on for ws-a\n")
	assert.Contains(t, got, "Viewer: octocat\n")
	assert.Contains(t, got, "Issues considered: 3 (at least 1 not considered)\n")
	assert.Regexp(t, `ISSUE\s+STATE\s+SESSION\s+PR\s+NUDGES\s+ESCALATED\s+WAITING\s+FAILED\s+DECISION`, got)
	assert.Regexp(t, `#12\s+open\s+heal 12\s+#34\s+ci=2,conflict=1\s+-\s+-\s+send failed: pane gone\s+nudge ci \(PR #34, nudge 3\)`, got)
	assert.Regexp(t, `#13\s+open\s+-\s+-\s+-\s+-\s+the cap is full\s+-\s+-\n`, got, "a wait is the reason, not a decision")
	assert.Regexp(t, `#14\s+closed\s+heal 14\s+-\s+-\s+yes\s+-\s+-\s+kill session 9`, got)
	assert.Contains(t, got, "Escalations:\n  #14  2026-11-13T09:30:00Z  PR #40: ci nudge limit (3) reached\n")
	assert.NotContains(t, got, "Error:")
}

func TestWriteWatchStatus_NamesTheFaultAndAnOffWatcher(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, writeWatchStatus(&out, "ws-a", core.WatchView{Err: "no loom-watch.json on origin/main"}))

	assert.Equal(t, "Watch off for ws-a\nError: no loom-watch.json on origin/main\nNo issues.\n", out.String())
}

func TestWriteWatchStatus_SaysWhenAnOnWatcherHasNoIssues(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, writeWatchStatus(&out, "ws-a", core.WatchView{On: true, Viewer: "octocat"}))

	assert.Equal(t, "Watch on for ws-a\nViewer: octocat\nNo issues.\n", out.String())
}

func TestWriteWatchDryRun_ListsTheActionsAndTheWaitsApart(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, writeWatchDryRun(&out, "ws-a", sampleWatchView()))

	assert.Equal(t, "Dry run for ws-a: nothing was done.\n"+
		"The next tick would:\n"+
		"  #12  nudge ci (PR #34, nudge 3)\n"+
		"  #14  kill session 9\n"+
		"Waiting:\n"+
		"  #13  the cap is full\n"+
		"Failed:\n"+
		"  #12  send failed: pane gone\n", out.String())
}

func TestWriteWatchDryRun_SaysWhenThereIsNothingToDo(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, writeWatchDryRun(&out, "ws-a", core.WatchView{On: true}))
	assert.Equal(t, "Dry run for ws-a: nothing was done.\nThe next tick would take no action.\n", out.String())

	out.Reset()
	require.NoError(t, writeWatchDryRun(&out, "ws-a", core.WatchView{On: true, Err: "gh: not logged in"}))
	assert.Equal(t, "Dry run for ws-a: nothing was done.\nError: gh: not logged in\nThe next tick would take no action.\n", out.String())

	out.Reset()
	require.NoError(t, writeWatchDryRun(&out, "ws-a", core.WatchView{}))
	assert.Equal(t, "Dry run for ws-a: nothing was done.\nWatch is off: the next tick would take no action.\n", out.String())
}

func TestDescribeWatchAction_NamesEveryKind(t *testing.T) {
	for want, a := range map[string]watch.Action{
		"start":                           {Kind: watch.ActStart, Issue: 5},
		"start (resume PR #8 on me/x)":    {Kind: watch.ActStart, Issue: 5, Resume: true, PR: 8, Branch: "me/x"},
		"kill session 9":                  {Kind: watch.ActKill, Issue: 5, Session: 9},
		"close PR #8: the issue closed":   {Kind: watch.ActClosePR, Issue: 5, PR: 8, Reason: "the issue closed"},
		"nudge conflict (PR #8, nudge 1)": {Kind: watch.ActSend, Issue: 5, PR: 8, Nudge: watch.Nudge{Kind: "conflict"}, N: 1},
		"escalate: stuck":                 {Kind: watch.ActEscalate, Issue: 5, Reason: "stuck"},
		"wait: the cap is full":           {Kind: watch.ActWait, Issue: 5, Reason: "the cap is full"},
		"mystery":                         {Kind: "mystery", Issue: 5},
	} {
		assert.Equal(t, want, describeWatchAction(a))
	}
}

func TestWatchStatus_PrintsTheDaemonsView(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")

	out, _, err := runWatch(t, "status", "-w", "ws-a")
	require.NoError(t, err)
	assert.Contains(t, out, "Watch off for ws-a\n", "never switched on")

	_, _, err = runWatch(t, "on", "-w", "ws-a")
	require.NoError(t, err)
	out, _, err = runWatch(t, "status", "-w", "ws-a")
	require.NoError(t, err)
	assert.Contains(t, out, "Watch on for ws-a\n")
}

func TestWatchStatus_JSONIsTheView(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", "")
	serveDaemon(t, "true")
	_, _, err := runWatch(t, "on", "-w", "ws-a")
	require.NoError(t, err)

	out, _, err := runWatch(t, "status", "--json", "-w", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, `"on": true`, "the contract's snake_case keys")
	var v core.WatchView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.True(t, v.On)
}

func TestWriteWatchJSON_CarriesAFailedActionsText(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, writeWatchJSON(&out, sampleWatchView()))

	assert.Contains(t, out.String(), `"failed": "send failed: pane gone"`, "the contract's snake_case key for WatchIssueView.Failed")
	var v core.WatchView
	require.NoError(t, json.Unmarshal(out.Bytes(), &v))
	assert.Equal(t, "send failed: pane gone", v.Issues[0].Failed)
	assert.Empty(t, v.Issues[1].Failed)
}

// watchableRepo registers a repository as name whose origin holds a
// loom-watch.json and its prompt on main, and puts a fake gh first on PATH
// that finds no PRs and no issues: a poll of it, dry run or not, succeeds
// without a network. It returns the path of the file the fake gh appends a
// line to on every call, so a test can tell a command that polls from one
// that does not. (A bare `git init` repository has no commit, and the daemon
// refuses a dry run of it.)
func watchableRepo(t *testing.T, name string) (ghLog string) {
	t.Helper()
	git := func(dir string, args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	origin := t.TempDir()
	git(origin, "init", "-q", "--bare", "-b", "main")
	repo := t.TempDir()
	git(repo, "init", "-q", "-b", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".loom-watch"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "loom-watch.json"),
		[]byte(`{"version":1,"label":"drift","escalate_label":"needs-human","title":"heal {issue}","session":{"prompt":".loom-watch/start.md"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".loom-watch", "start.md"), []byte("Fix issue {issue}.\n"), 0o644))
	git(repo, "add", "-A")
	git(repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-m", "init")
	git(repo, "remote", "add", "origin", origin)
	git(repo, "push", "-q", "-u", "origin", "main")
	git(repo, "remote", "set-head", "origin", "main")

	bin := t.TempDir()
	ghLog = filepath.Join(t.TempDir(), "gh.log")
	script := "#!/bin/sh\necho \"$*\" >> '" + ghLog + "'\nif [ \"$1\" = api ]; then echo octocat; else echo '[]'; fi\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	registeredAt(t, name, repo, "")
	return ghLog
}

// ghCalls counts the calls the fake gh at path has logged.
func ghCalls(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(b), "\n")
}

func TestWatchDryRun_TakesNoActionAndPrintsThePlan(t *testing.T) {
	isolateWorkspaces(t)
	ghLog := watchableRepo(t, "ws-a")
	serveDaemon(t, "true")
	_, _, err := runWatch(t, "on", "-w", "ws-a")
	require.NoError(t, err)

	// Switching on starts a poll at once, and a dry run is refused while one
	// is out: ask again until it ends, and only on that refusal.
	var out string
	var lastErr error
	require.Eventually(t, func() bool {
		out, _, lastErr = runWatch(t, "dry-run", "-w", "ws-a")
		if lastErr == nil {
			return true
		}
		if !strings.Contains(lastErr.Error(), "a watcher poll is already running") {
			require.NoError(t, lastErr)
		}
		return false
	}, 10*time.Second, 20*time.Millisecond, "last error: %v", lastErr)
	assert.True(t, strings.HasPrefix(out, "Dry run for ws-a: nothing was done.\n"), out)

	// A dry run polls (it asks gh); status reads the daemon's view and does not.
	afterDry := ghCalls(t, ghLog)
	assert.Positive(t, afterDry, "the dry run polled")
	_, _, err = runWatch(t, "status", "-w", "ws-a")
	require.NoError(t, err)
	assert.Equal(t, afterDry, ghCalls(t, ghLog), "status takes no poll")

	out, _, err = runWatch(t, "dry-run", "--json", "-w", "ws-a")
	require.NoError(t, err)
	assert.Greater(t, ghCalls(t, ghLog), afterDry, "a second dry run polls again")
	var v core.WatchView
	require.NoError(t, json.Unmarshal([]byte(out), &v))
	assert.True(t, v.On)
}

func TestWatchStatusAndDryRun_RefuseAnUnknownWorkspace(t *testing.T) {
	isolateWorkspaces(t)
	serveDaemon(t, "true")

	for _, sub := range []string{"status", "dry-run"} {
		_, _, err := runWatch(t, sub, "-w", "nope")
		require.Error(t, err, sub)
		assert.Contains(t, err.Error(), `workspace "nope" not found`, sub)
	}
}

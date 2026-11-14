package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wactIssue is a labelled open issue.
func wactIssue(n int) github.WatchIssue {
	return github.WatchIssue{Number: n, Title: fmt.Sprintf("Drift %d", n), URL: fmt.Sprintf("https://github.test/o/r/issues/%d", n), Labels: []string{"drift"}}
}

// wactStartModel is wactModel with issue 7 in the snapshot and both prompt
// templates set.
func wactStartModel(t *testing.T, o Options) (*Model, *Workspace, *watchState) {
	t.Helper()
	m, ws, st := wactModel(t, o)
	wactGitRepo(t, ws.ctx.RepoPath)
	st.snap.Issues = []github.WatchIssue{wactIssue(7)}
	st.prompt = "Fix {issue_title} ({issue_url}) on {base}"
	st.resumePrompt = "Resume #{pr} on {branch}"
	return m, ws, st
}

// wactGitRepo makes dir a git repository with a commit on main and a bare
// origin, so a fresh start's check for an existing branch has a real answer.
func wactGitRepo(t *testing.T, dir string) {
	t.Helper()
	gitIn := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.email=t@example.com", "-c", "user.name=T"}, args...)...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	gitIn(dir, "init", "-q", "-b", "main")
	gitIn(dir, "commit", "-q", "--allow-empty", "-m", "first")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(dir, "clone", "-q", "--bare", dir, origin)
	gitIn(dir, "remote", "add", "origin", origin)
}

// wactHook is a hook command that saves its stdin next to itself, as in.json,
// then runs body; the second result is that file's path.
func wactHook(t *testing.T, body string) (*watch.HookConfig, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hook.sh")
	script := "#!/bin/sh\ncat > \"$(dirname \"$0\")/in.json\"\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return &watch.HookConfig{Command: []string{path}, Timeout: 20 * time.Second}, filepath.Join(dir, "in.json")
}

type wactHookSession struct {
	Title string            `json:"title"`
	Issue int               `json:"issue"`
	Vars  map[string]string `json:"vars"`
}

type wactHookPR struct {
	Number int `json:"number"`
	Issue  int `json:"issue"`
}

type wactHookIn struct {
	Point     string            `json:"point"`
	Workspace string            `json:"workspace"`
	Repo      string            `json:"repo"`
	BaseSHA   string            `json:"base_sha"`
	PR        *wactHookPR       `json:"pr"`
	Sessions  []wactHookSession `json:"sessions"`
	PRs       []wactHookPR      `json:"prs"`
}

// wactRunStart dispatches one start action, runs its job and returns the
// job's result without delivering it.
func wactRunStart(t *testing.T, m *Model, ws *Workspace, st *watchState, a watch.Action) watchActionResult {
	t.Helper()
	m.dispatchWatchActions(ws, st, []watch.Action{a})
	out := m.Drain()
	require.Len(t, out.Jobs, 1, "the hook and render job")
	assert.True(t, st.inFlight[watchActKey(a)])
	res, ok := out.Jobs[0]().(watchActionResult)
	require.True(t, ok)
	return res
}

func TestWatchStart_CreatesTheSessionCutFromTheBaseWithTheRenderedPrompt(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	a := watch.Action{Kind: watch.ActStart, Issue: 7}

	res := wactRunStart(t, m, ws, st, a)

	assert.Equal(t, "abc123", res.start.baseRef, "cut from origin/<base>'s SHA")
	assert.Empty(t, res.start.branch)
	m.Deliver(res)
	out := m.Drain()
	require.Len(t, ws.insts, 1)
	inst := ws.insts[0]
	assert.Equal(t, "heal 7", inst.Title)
	assert.Equal(t, 7, inst.IssueNumber())
	assert.True(t, strings.HasPrefix(inst.Prompt(), "Fix Drift 7 (https://github.test/o/r/issues/7) on main"), inst.Prompt())
	assert.Contains(t, inst.Prompt(), watch.PromptFooter(7))
	assert.Len(t, out.Jobs, 1, "the session's own start job")
	assert.Equal(t, watch.IssueSeen(wactIssue(7)), st.mem.Issues[7].Seen)
	onDisk, err := watch.LoadMemory(st.memPath)
	require.NoError(t, err)
	assert.Equal(t, st.mem.Issues[7].Seen, onDisk.Issues[7].Seen)
	assert.Empty(t, st.inFlight)
	assert.Empty(t, st.actErrs)
}

func TestWatchStart_AFreshStartNeverReusesABranch(t *testing.T) {
	for _, where := range []string{"locally", "on origin"} {
		t.Run(where, func(t *testing.T) {
			m, ws, st := wactStartModel(t, Options{})
			branch := git.SanitizeBranchName(st.snap.Prefix + "heal 7")
			gitIn := func(args ...string) {
				c := exec.Command("git", args...)
				c.Dir = ws.ctx.RepoPath
				out, err := c.CombinedOutput()
				require.NoError(t, err, "%s", out)
			}
			gitIn("branch", branch)
			if where == "on origin" {
				gitIn("push", "-q", "origin", branch)
				gitIn("branch", "-D", branch)
			}

			res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7})
			m.Deliver(res)

			assert.Empty(t, ws.insts, "nothing was started on the old branch")
			assert.Contains(t, st.actErrs[7], "branch "+branch+" already exists "+where)
			_, failed := st.mem.PRs[0]
			assert.False(t, failed, "a fresh start's failure is no resume's")
		})
	}
}

func TestWatchStart_AFailedBranchCheckRefusesTheStart(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	c := exec.Command("git", "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	c.Dir = ws.ctx.RepoPath
	require.NoError(t, c.Run())

	res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7})
	m.Deliver(res)

	assert.Empty(t, ws.insts)
	assert.Contains(t, st.actErrs[7], "the check for an existing branch")
}

func TestWatchStart_AResumesFailuresAreRecordedUntilOneSucceeds(t *testing.T) {
	hook, _ := wactHook(t, `exit 1`)
	m, ws, st := wactStartModel(t, Options{})
	st.cfg.Hook = hook
	pr := wactPR(7, 5, "OPEN")
	st.snap.PRs = []github.WatchPR{pr}
	a := watch.Action{Kind: watch.ActStart, Issue: 7, Resume: true, Branch: pr.HeadRefName, PR: 5}

	m.Deliver(wactRunStart(t, m, ws, st, a))
	m.Drain()
	require.NotNil(t, st.mem.PRs[5].ResumeFailed, "the first failure is recorded")
	since := st.mem.PRs[5].ResumeFailed.Since
	onDisk, err := watch.LoadMemory(st.memPath)
	require.NoError(t, err)
	require.NotNil(t, onDisk.PRs[5].ResumeFailed, "and saved")

	m.Deliver(wactRunStart(t, m, ws, st, a))
	m.Drain()
	assert.Equal(t, since, st.mem.PRs[5].ResumeFailed.Since, "a later failure keeps the first one's time")

	// A fresh start's failure is recorded nowhere.
	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))
	m.Drain()
	assert.Len(t, st.mem.PRs, 1)
}

func TestWatchStart_AResumeThatCreatesItsSessionClearsItsFailures(t *testing.T) {
	do := func(c *exec.Cmd) ([]byte, error) { return []byte("deadbeef\n"), nil }
	mock := cmd_test.MockCmdExec{
		RunFunc:            func(c *exec.Cmd) error { _, err := do(c); return err },
		OutputFunc:         do,
		CombinedOutputFunc: do,
	}
	m, ws, st := wactStartModel(t, Options{CmdExec: mock})
	pr := wactPR(7, 5, "OPEN")
	st.snap.PRs = []github.WatchPR{pr}
	st.mem.RecordResumeFailure(5, "the hook failed", time.Now().Add(-time.Hour))

	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7, Resume: true, Branch: pr.HeadRefName, PR: 5}))

	require.Len(t, ws.insts, 1)
	assert.Nil(t, st.mem.PRs[5].ResumeFailed)
	onDisk, err := watch.LoadMemory(st.memPath)
	require.NoError(t, err)
	assert.Nil(t, onDisk.PRs[5].ResumeFailed, "and the clearing is saved")
}

func TestWatchStart_AResumesHookInputCarriesTheBranchsVariables(t *testing.T) {
	hook, inPath := wactHook(t, `echo '{"start": false, "reason": "just looking"}'`)
	m, ws, st := wactStartModel(t, Options{})
	tmpl, err := watch.ParseTitleTemplate("heal {spec} {issue}")
	require.NoError(t, err)
	st.tmpl, st.cfg.Hook = tmpl, hook
	pr := wactPR(7, 5, "OPEN")
	pr.HeadRefName = git.SanitizeBranchName(st.snap.Prefix + "heal Kalshi 7")
	st.snap.PRs = []github.WatchPR{pr}

	wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7, Resume: true, Branch: pr.HeadRefName, PR: 5})

	data, err := os.ReadFile(inPath)
	require.NoError(t, err)
	var in struct {
		PR  struct{ Vars map[string]string }   `json:"pr"`
		PRs []struct{ Vars map[string]string } `json:"prs"`
	}
	require.NoError(t, json.Unmarshal(data, &in))
	assert.Equal(t, map[string]string{"spec": "kalshi"}, in.PR.Vars, "as the branch spells them")
	require.Len(t, in.PRs, 1)
	assert.Equal(t, map[string]string{"spec": "kalshi"}, in.PRs[0].Vars)
}

func TestWatchStart_TheHooksVarsNameTheSessionAndItsInputDescribesTheWorkspace(t *testing.T) {
	hook, inPath := wactHook(t, `echo '{"start": true, "vars": {"spec": "kalshi"}}'`)
	m, ws, st := wactStartModel(t, Options{})
	tmpl, err := watch.ParseTitleTemplate("heal {spec} {issue}")
	require.NoError(t, err)
	st.tmpl, st.cfg.Hook = tmpl, hook
	st.prompt = "Fix {issue} for {spec}"
	existing := wactPaused(t, ws, "heal kalshi 3", 3)
	st.snap.Sessions = []watch.Session{{ID: uint64(m.idOf(existing)), Title: "heal kalshi 3", Status: "Paused", Branch: "heal-kalshi-3", Issue: 3}}
	pr := wactPR(3, 31, "OPEN")
	pr.HeadRefName = git.SanitizeBranchName("heal kalshi 3")
	st.snap.PRs = []github.WatchPR{pr}

	res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7})

	assert.Equal(t, "heal kalshi 7", res.start.title)
	assert.True(t, strings.HasPrefix(res.start.prompt, "Fix 7 for kalshi"), res.start.prompt)
	data, err := os.ReadFile(inPath)
	require.NoError(t, err)
	var in wactHookIn
	require.NoError(t, json.Unmarshal(data, &in))
	assert.Equal(t, "start", in.Point)
	assert.Equal(t, "a", in.Workspace)
	assert.Equal(t, "o/r", in.Repo)
	assert.Equal(t, "abc123", in.BaseSHA)
	assert.Nil(t, in.PR, "a start has no PR")
	assert.Equal(t, []wactHookSession{{Title: "heal kalshi 3", Issue: 3, Vars: map[string]string{"spec": "kalshi"}}}, in.Sessions)
	assert.Equal(t, []wactHookPR{{Number: 31, Issue: 3}}, in.PRs)
}

func TestWatchStart_AHookThatDeclinesLeavesTheIssueWaitingWithItsReason(t *testing.T) {
	hook, _ := wactHook(t, `echo '{"start": false, "reason": "no row free"}'`)
	m, ws, st := wactStartModel(t, Options{})
	st.cfg.Hook = hook

	res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7})
	m.Deliver(res)

	assert.Empty(t, ws.insts, "nothing was created")
	assert.Contains(t, st.actErrs[7], "no row free")
	assert.Empty(t, st.inFlight)
}

func TestWatchStart_AHookThatFailsLeavesTheIssueWaiting(t *testing.T) {
	hook, _ := wactHook(t, "exit 3")
	m, ws, st := wactStartModel(t, Options{})
	st.cfg.Hook = hook

	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

	assert.Empty(t, ws.insts)
	assert.Contains(t, st.actErrs[7], "hook start")
}

func TestWatchStart_RefusesATitleOrPromptItCannotUse(t *testing.T) {
	t.Run("a title wider than the limit", func(t *testing.T) {
		hook, _ := wactHook(t, `echo '{"start": true, "vars": {"spec": "abcdefghijklmnopqrstuvwxyz"}}'`)
		m, ws, st := wactStartModel(t, Options{})
		tmpl, err := watch.ParseTitleTemplate("heal {spec} {issue}")
		require.NoError(t, err)
		st.tmpl, st.cfg.Hook = tmpl, hook

		m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

		assert.Empty(t, ws.insts)
		assert.Contains(t, st.actErrs[7], "title")
	})
	t.Run("a prompt over 64 KiB", func(t *testing.T) {
		m, ws, st := wactStartModel(t, Options{})
		st.prompt = strings.Repeat("x", watchTextCap)

		m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

		assert.Empty(t, ws.insts)
		assert.Contains(t, st.actErrs[7], "over the")
	})
	t.Run("a prompt naming a variable nobody supplies", func(t *testing.T) {
		m, ws, st := wactStartModel(t, Options{})
		st.prompt = "Fix {nope}"

		m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

		assert.Empty(t, ws.insts)
		assert.Contains(t, st.actErrs[7], "prompt")
	})
}

func TestWatchStart_StartsNothingInATickWhoseFetchFailedOrForAnIssueNotInTheSnapshot(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	st.startOK = false
	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActStart, Issue: 7}})
	assert.Empty(t, m.Drain().Jobs)
	assert.Contains(t, st.actErrs[7], "fetch")

	st.startOK = true
	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActStart, Issue: 9}})
	assert.Empty(t, m.Drain().Jobs)
	assert.Contains(t, st.actErrs[9], "snapshot")
}

func TestWatchStart_OneStartAtATimeEvenAcrossTicks(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	st.snap.Issues = []github.WatchIssue{wactIssue(7), wactIssue(8)}

	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActStart, Issue: 7}, {Kind: watch.ActStart, Issue: 8}})

	assert.Len(t, m.Drain().Jobs, 1, "the second start waits for the first's session to exist")
}

func TestWatchStart_AResumeFetchesThePRsBranchAndTakesItsPrompt(t *testing.T) {
	var calls []string
	do := func(c *exec.Cmd) ([]byte, error) {
		calls = append(calls, strings.Join(c.Args, " "))
		return []byte("deadbeef\n"), nil
	}
	mock := cmd_test.MockCmdExec{
		RunFunc:            func(c *exec.Cmd) error { _, err := do(c); return err },
		OutputFunc:         do,
		CombinedOutputFunc: do,
	}
	m, ws, st := wactStartModel(t, Options{CmdExec: mock})
	pr := wactPR(7, 5, "OPEN")
	st.snap.PRs = []github.WatchPR{pr}
	a := watch.Action{Kind: watch.ActStart, Issue: 7, Resume: true, Branch: pr.HeadRefName, PR: 5}

	res := wactRunStart(t, m, ws, st, a)

	assert.Equal(t, pr.HeadRefName, res.start.branch)
	assert.Empty(t, res.start.baseRef, "a resume checks out the PR's branch, it is cut from nothing")
	assert.True(t, slices.ContainsFunc(calls, func(c string) bool { return strings.Contains(c, "fetch") && strings.Contains(c, pr.HeadRefName) }), "calls: %v", calls)
	m.Deliver(res)
	require.Len(t, ws.insts, 1)
	assert.True(t, strings.HasPrefix(ws.insts[0].Prompt(), "Resume #5 on "+pr.HeadRefName), ws.insts[0].Prompt())
}

func TestWatchStart_AResumeWhoseRenderedBranchIsNotThePRsIsRefusedWithoutAFetch(t *testing.T) {
	var calls []string
	do := func(c *exec.Cmd) ([]byte, error) {
		calls = append(calls, strings.Join(c.Args, " "))
		return nil, nil
	}
	mock := cmd_test.MockCmdExec{
		RunFunc:            func(c *exec.Cmd) error { _, err := do(c); return err },
		OutputFunc:         do,
		CombinedOutputFunc: do,
	}
	hook, _ := wactHook(t, `echo '{"start": true, "vars": {"spec": "other"}}'`)
	m, ws, st := wactStartModel(t, Options{CmdExec: mock})
	tmpl, err := watch.ParseTitleTemplate("heal {spec} {issue}")
	require.NoError(t, err)
	st.tmpl, st.cfg.Hook = tmpl, hook
	pr := wactPR(7, 5, "OPEN")
	pr.HeadRefName = git.SanitizeBranchName("heal kalshi 7")
	st.snap.PRs = []github.WatchPR{pr}

	res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7, Resume: true, Branch: pr.HeadRefName, PR: 5})
	m.Deliver(res)

	assert.Empty(t, ws.insts)
	assert.Contains(t, st.actErrs[7], "resume refused")
	assert.Empty(t, calls, "no fetch")
}

func TestWatchStart_AResumeOfAPRThatIsNotItsOwnIsRefusedBeforeAnyJob(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	pr := wactPR(7, 5, "OPEN")
	pr.Author = "someone"
	st.snap.PRs = []github.WatchPR{pr}

	m.dispatchWatchActions(ws, st, []watch.Action{{Kind: watch.ActStart, Issue: 7, Resume: true, Branch: pr.HeadRefName, PR: 5}})

	assert.Empty(t, m.Drain().Jobs)
	assert.NotEmpty(t, st.actErrs[7])
}

func TestWatchStart_ReChecksWhatTheHooksRuntimeMayHaveChanged(t *testing.T) {
	foreign := func(t *testing.T, ws *Workspace) { wactPaused(t, ws, "manual", 7) }
	owned := func(t *testing.T, ws *Workspace) { wactPaused(t, ws, "heal 3", 3) }
	sameTitle := func(t *testing.T, ws *Workspace) { wactPaused(t, ws, "heal 7", 0) }
	cases := []struct {
		name  string
		setup func(t *testing.T, ws *Workspace)
		cap   int
		want  string
	}{
		{"a session for the issue appeared", foreign, 2, "#7"},
		{"the cap filled", owned, 1, "cap"},
		{"the title is taken", sameTitle, 2, "already has a session titled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ws, st := wactStartModel(t, Options{})
			st.cfg.Cap = c.cap
			res := wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7})
			c.setup(t, ws)
			before := len(ws.insts)

			m.Deliver(res)

			assert.Len(t, ws.insts, before, "nothing was created")
			assert.Contains(t, st.actErrs[7], c.want)
			assert.Empty(t, st.mem.Issues, "seen is not recorded")
		})
	}
}

func TestWatchStart_ANamedAccountThatIsNotRegisteredRefusesTheStartAndNeverFallsBack(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	withAccounts(t, m, "max-2")

	st.cfg.Session.Account = "ghost"
	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))
	assert.Empty(t, ws.insts, "the default account is never used in its place")
	assert.Contains(t, st.actErrs[7], "ghost")

	st.cfg.Session.Account = "max-2"
	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))
	require.Len(t, ws.insts, 1)
	assert.Equal(t, "max-2", ws.insts[0].Account())
}

func TestWatchStart_TheConfigsModelReachesTheLaunch(t *testing.T) {
	m, ws, st := wactStartModel(t, Options{})
	st.cfg.Session.Model = "opus"

	m.Deliver(wactRunStart(t, m, ws, st, watch.Action{Kind: watch.ActStart, Issue: 7}))

	require.Len(t, ws.insts, 1)
	assert.Contains(t, ws.insts[0].Program(), "opus")
}

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readyInst builds a Running instance titled "a" on a mock tmux session
// (has-session answers alive; no tmux server contacted), held by m's
// classic workspace (app's addReadyInstance).
func readyInst(t *testing.T, m *Model) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   "a",
		Path:    t.TempDir(),
		Program: "claude",
	})
	require.NoError(t, err)
	hold(m, inst)
	require.NoError(t, inst.TransitionTo(session.Running))
	inst.SetTmuxSession(tmux.NewSessionWithDeps("a", "true", fakePtyFactory{t: t}, aliveExec()))
	return inst
}

// ghModel is a model with one opened workspace with no repository of its
// own (as the global one) and one session in it, so the poll covers the
// repository that session runs in.
func ghModel(t *testing.T) *Model {
	t.Helper()
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(storedWorkspace(t, "a"))
	hold(m, newInst(t, "x"))
	return m
}

// TestGHQuery_OnlyOpenedWorkspacesArePolled: the model serves every
// workspace, but polls GitHub only for the ones a client has opened: a git
// fetch and gh calls per registered repository every minute would cost far
// more than the badges nobody is looking at are worth.
func TestGHQuery_OnlyOpenedWorkspacesArePolled(t *testing.T) {
	m := NewForTest(Options{})
	assert.False(t, m.maybeGHQuery(), "nothing opened, nothing to poll")

	ws := storedWorkspace(t, "a")
	ws.ctx.RepoPath = t.TempDir()
	m.SetWorkspacesForTest(ws)
	ws.opened = false
	assert.Empty(t, m.openRepoPaths(), "served but never opened")

	require.NoError(t, m.open(ws))
	assert.Equal(t, []string{ws.ctx.RepoPath}, m.openRepoPaths())
}

// The global workspace has no repository of its own: it stands for the
// repositories its sessions run in, each once, against the global config's
// base branch, never for the directory the daemon happens to run in (some
// client's, when it spawned it). With no session it polls nothing.
func TestOpenedRepos_TheGlobalWorkspaceStandsForItsSessionsRepositories(t *testing.T) {
	t.Chdir(t.TempDir()) // the daemon's working directory: no client's
	cwd, err := os.Getwd()
	require.NoError(t, err)
	global := storedWorkspace(t, "")
	global.cfg.BaseBranch = "trunk"
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(global)

	assert.Empty(t, m.openedRepos(), "no session, nothing to poll")
	assert.False(t, m.maybeGHQuery())

	r1, r2 := t.TempDir(), t.TempDir()
	at := func(title, repo string) *session.Instance {
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: repo, Program: "claude"})
		require.NoError(t, err)
		return inst
	}
	hold(m, at("x", r1), at("y", r2), at("z", r1))

	got := m.openedRepos()
	assert.Equal(t, []openedRepo{{path: r1, base: "trunk"}, {path: r2, base: "trunk"}}, got)
	for _, r := range got {
		assert.NotEqual(t, cwd, r.path, "the working directory is not polled")
	}
}

// A global session in a registered repository names that repository too:
// the repository's own workspace wins, so it is polled once, against its
// own base branch, however the session's path is spelled.
func TestOpenedRepos_ARepositorysOwnWorkspaceWinsOverTheGlobalSessions(t *testing.T) {
	repo := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(repo, link))
	for name, path := range map[string]string{"run in it": repo, "run through a symlink to it": link} {
		t.Run(name, func(t *testing.T) {
			global := storedWorkspace(t, "") // no repository: it stands for its sessions'
			global.cfg.BaseBranch = "trunk"
			x := storedWorkspace(t, "x")
			x.ctx.RepoPath = repo
			x.cfg.BaseBranch = "develop"
			m := NewForTest(Options{})
			m.SetWorkspacesForTest(global, x) // the global workspace is served first
			inst, err := session.NewInstance(session.InstanceOptions{Title: "g", Path: path, Program: "claude"})
			require.NoError(t, err)
			hold(m, inst)

			assert.Equal(t, []openedRepo{{path: repo, base: "develop"}}, m.openedRepos())
		})
	}
}

// TestWatchGitHub_PollsARepositoryNothingElseCovers: a client's issue
// picker in global mode, in a repository no global session runs in, asks
// the poll to cover it, which it does from then on, after every opened
// workspace's repository and global session's, against the global
// config's base branch, once however often it is asked, from the next
// tick. A path that is not absolute names no repository, and one an
// opened workspace covers is polled once, as that workspace's.
func TestWatchGitHub_PollsARepositoryNothingElseCovers(t *testing.T) {
	global := storedWorkspace(t, "")
	global.cfg.BaseBranch = "trunk"
	x := storedWorkspace(t, "x")
	x.ctx.RepoPath = t.TempDir()
	x.cfg.BaseBranch = "develop"
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(global, x)
	running := t.TempDir()
	inst, err := session.NewInstance(session.InstanceOptions{Title: "g", Path: running, Program: "claude"})
	require.NoError(t, err)
	hold(m, inst)
	m.gate(gateGH).last = time.Now()
	require.False(t, m.gateDue(gateGH, time.Now()), "fixture: a poll just went")

	asked := t.TempDir()
	m.WatchGitHub(asked)
	m.WatchGitHub(asked)
	m.WatchGitHub("relative/dir")
	m.WatchGitHub(x.ctx.RepoPath)

	assert.Equal(t, []openedRepo{{path: x.ctx.RepoPath, base: "develop"}, {path: running, base: "trunk"}, {path: asked, base: "trunk"}},
		m.openedRepos())
	assert.Equal(t, []string{asked, x.ctx.RepoPath}, watchedRepos(m), "each kept once, however often a picker opens")
	assert.True(t, m.gateDue(gateGH, time.Now()), "polled at the next tick")
}

// watchedRepos lists the repositories clients asked the poll to cover.
func watchedRepos(m *Model) []string {
	var out []string
	for _, w := range m.ghWatched {
		out = append(out, w.repo)
	}
	return out
}

// TestWatchGitHub_AWatchExpires: a repository a client asked for is
// polled until ghWatchTTL has passed since the last ask, not for the
// daemon's life: the picker asks on every open, so one in use stays
// polled. Asking again renews a watch, without polling at once when the
// poll has answered for it already; an expired one asked again is polled
// again, at the next tick.
func TestWatchGitHub_AWatchExpires(t *testing.T) {
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(storedWorkspace(t, ""))
	stale, renewed := t.TempDir(), t.TempDir()
	m.WatchGitHub(stale)
	m.WatchGitHub(renewed)
	require.Equal(t, []string{stale, renewed}, m.openRepoPaths())

	m.ghWatched[0].at = time.Now().Add(-ghWatchTTL - time.Second)
	m.ghWatched[1].at = time.Now().Add(-ghWatchTTL + time.Minute)
	assert.Equal(t, []string{renewed}, m.openRepoPaths(), "a watch nobody renewed within ghWatchTTL is no longer polled")

	m.ghState = map[string]github.Snapshot{canonicalDir(renewed): {}}
	m.gate(gateGH).last = time.Now()
	m.WatchGitHub(renewed)
	assert.Equal(t, []string{renewed}, watchedRepos(m), "the expired watch was dropped")
	assert.WithinDuration(t, time.Now(), m.ghWatched[0].at, time.Second, "renewed")
	assert.False(t, m.gateDue(gateGH, time.Now()), "a repository the poll has answered for is not polled again at once")
	m.ghWatched[0].at = time.Now().Add(-ghWatchTTL + time.Minute)
	assert.Equal(t, []string{renewed}, m.openRepoPaths(), "still polled: renewed a moment before it expired")

	m.WatchGitHub(stale)
	assert.Equal(t, []string{renewed, stale}, m.openRepoPaths(), "asked again: polled again")
	assert.True(t, m.gateDue(gateGH, time.Now()), "at the next tick, having no answer for it")
}

func TestGHQueryDispatchesOnFirstCall(t *testing.T) {
	m := ghModel(t)
	require.True(t, m.maybeGHQuery())
	assert.True(t, m.gate(gateGH).inFlight)
}

func TestGHQueryThrottledWithinInterval(t *testing.T) {
	m := ghModel(t)
	require.True(t, m.maybeGHQuery())
	m.gate(gateGH).inFlight = false
	assert.False(t, m.maybeGHQuery())
}

func TestGHQueryResumesAfterInterval(t *testing.T) {
	m := ghModel(t)
	require.True(t, m.maybeGHQuery())
	m.gate(gateGH).inFlight = false
	m.gate(gateGH).last = time.Now().Add(-ghInterval - time.Second)
	assert.True(t, m.maybeGHQuery())
}

func TestGHQueryNotStackedWhileInFlight(t *testing.T) {
	m := ghModel(t)
	require.True(t, m.maybeGHQuery())
	m.gate(gateGH).last = time.Now().Add(-ghInterval - time.Second)
	assert.False(t, m.maybeGHQuery())
}

func TestGHQueryDisabledWhenCLIUnavailable(t *testing.T) {
	m := NewForTest(Options{})
	m.ghAvailable = ghAvailability{checked: true, ok: false, reason: "no gh", checkedAt: time.Now()}
	assert.False(t, m.maybeGHQuery(), "a known-unavailable gh must not spawn subprocesses")
	assert.False(t, m.gate(gateGH).inFlight)
}

func TestGHQueryRechecksAfterBackoff(t *testing.T) {
	m := ghModel(t)
	m.ghAvailable = ghAvailability{checked: true, ok: false, reason: "no gh", checkedAt: time.Now().Add(-ghRecheckInterval - time.Second)}
	assert.True(t, m.maybeGHQuery(), "an unavailable gh is re-probed after the backoff, not disabled forever")
}

func TestGHResultClearsInFlightAndReplacesWholesale(t *testing.T) {
	m := NewForTest(Options{})
	m.gate(gateGH).inFlight = true
	m.ghState = map[string]github.Snapshot{"/old": {}}

	m.Deliver(gatedResult{kind: gateGH, result: ghResult{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{"/repo": {PRs: map[string]github.PR{}}},
		errs:      map[string]error{"/old": errors.New("boom")},
	}})
	assert.False(t, m.gate(gateGH).inFlight)
	_, hasOld := m.ghState["/old"]
	assert.False(t, hasOld, "an errored repo is dropped, not retained")
	_, hasNew := m.ghState["/repo"]
	assert.True(t, hasNew)
}

func TestGHResultJoinsOntoInstances(t *testing.T) {
	m := NewForTest(Options{})
	inst := readyInst(t, m)
	inst.Branch = "u/b"
	inst.SetIssue(12)
	repo := inst.Path

	m.Deliver(ghResult{
		available: ghAvailability{checked: true, ok: true},
		snapshots: map[string]github.Snapshot{repo: {
			PRs:    map[string]github.PR{"u/b": {Number: 45, State: github.PROpen}},
			Issues: map[int]github.Issue{12: {Number: 12, Title: "Fix"}},
		}},
	})
	s := inst.GitHubState()
	assert.True(t, s.Known)
	assert.Equal(t, 45, s.PRNumber)
	assert.Equal(t, "Fix", s.IssueTitle)
}

func TestGHResultUnknownRepoLeavesStateUnknown(t *testing.T) {
	m := NewForTest(Options{})
	inst := readyInst(t, m)
	m.Deliver(ghResult{available: ghAvailability{checked: true, ok: true}, snapshots: map[string]github.Snapshot{}})
	assert.False(t, inst.GitHubState().Known)
}

func TestExpediteGitHubZeroesWindow(t *testing.T) {
	m := NewForTest(Options{})
	m.gate(gateGH).last = time.Now()
	m.ExpediteGitHub()
	assert.True(t, m.gateDue(gateGH, time.Now()), "a refresh makes the next tick poll")
}

func TestLinkedIssuesCollectsNonZero(t *testing.T) {
	m := NewForTest(Options{})
	a := readyInst(t, m)
	a.SetIssue(3)
	b, err := session.NewInstance(session.InstanceOptions{Title: "b", Path: a.Path, Program: "claude"})
	require.NoError(t, err)
	hold(m, b)
	assert.Equal(t, []int{3}, m.linkedIssues(a.Path))
}

// ghFakeCall records one subprocess invocation for assertions. repo is
// the target directory regardless of how the real command expresses
// it: git passes the repo via a "-C <path>" argv pair (no Cmd.Dir),
// while gh sets Cmd.Dir directly and carries no repo path in argv —
// ghRepoOfCmd normalizes both to the same key.
type ghFakeCall struct {
	repo string
	argv string
}

// ghFakeAnswer is one scripted response, matched by an argv substring
// within its repo's bucket, tried in order.
type ghFakeAnswer struct {
	substr string
	out    string
	err    error
}

// ghFakeExec answers git/gh invocations from a per-repo answer list
// and fails loudly on anything unscripted, so this test cannot pass
// vacuously on a command it never meant to exercise.
type ghFakeExec struct {
	answers map[string][]ghFakeAnswer
	calls   []ghFakeCall
}

func ghRepoOfCmd(c *exec.Cmd) string {
	if c.Dir != "" {
		return c.Dir
	}
	for i, a := range c.Args {
		if a == "-C" && i+1 < len(c.Args) {
			return c.Args[i+1]
		}
	}
	return ""
}

func (f *ghFakeExec) find(c *exec.Cmd) (string, error) {
	repo := ghRepoOfCmd(c)
	argv := strings.Join(c.Args, " ")
	f.calls = append(f.calls, ghFakeCall{repo: repo, argv: argv})
	for _, a := range f.answers[repo] {
		if strings.Contains(argv, a.substr) {
			return a.out, a.err
		}
	}
	return "", fmt.Errorf("unscripted command for repo %q: %s", repo, argv)
}

func (f *ghFakeExec) Run(c *exec.Cmd) error { _, err := f.find(c); return err }
func (f *ghFakeExec) Output(c *exec.Cmd) ([]byte, error) {
	out, err := f.find(c)
	return []byte(out), err
}
func (f *ghFakeExec) CombinedOutput(c *exec.Cmd) ([]byte, error) { return f.Output(c) }

// TestGHPollJobResolvesPerRepoBaseAndBucketsErrors runs ghPollJob's
// closure directly (the earlier tests only ever check it is non-nil).
// It is the regression test for the per-repo BaseBranch bug: repo /a
// has its own configured base ("develop") while /b has none and must
// auto-detect — a shared single string across the batch would resolve
// /b against /a's setting instead. It also pins the wholesale-replace
// fail-closed contract: /b's failing gh query lands it in errs and
// out of snapshots without disturbing /a's successful result.
func TestGHPollJobResolvesPerRepoBaseAndBucketsErrors(t *testing.T) {
	fake := &ghFakeExec{answers: map[string][]ghFakeAnswer{
		"/a": {
			{substr: "rev-parse --show-toplevel", out: "/a\n"},
			{substr: "rev-parse --verify --quiet refs/heads/develop", out: "shaaaaa1\n"},
			{substr: "pr list", out: "[]"},
			{substr: "issue list", out: "[]"},
		},
		"/b": {
			{substr: "rev-parse --show-toplevel", out: "/b\n"},
			{substr: "symbolic-ref --short refs/remotes/origin/HEAD", err: errors.New("no origin HEAD")},
			{substr: "rev-parse --verify --quiet refs/heads/main", out: "shabbbbb2\n"},
			{substr: "pr list", err: errors.New("boom")},
		},
	}}

	req := ghPollRequest{
		repos:      []string{"/a", "/b"},
		linked:     map[string][]int{"/a": nil, "/b": nil},
		configured: map[string]string{"/a": "develop", "/b": ""},
	}

	msg, ok := ghPollJob(req, fake)().(ghResult)
	require.True(t, ok)

	// Assertion 1: each repo resolves against ITS OWN configured base
	// branch. /a's git argv must reference its configured "develop";
	// /b (unconfigured) must never see /a's setting leak onto it.
	var aResolvedDevelop, bMentionsDevelop bool
	for _, c := range fake.calls {
		if c.repo == "/a" && strings.Contains(c.argv, "refs/heads/develop") {
			aResolvedDevelop = true
		}
		if c.repo == "/b" && strings.Contains(c.argv, "develop") {
			bMentionsDevelop = true
		}
	}
	assert.True(t, aResolvedDevelop, "/a must resolve against its own configured base branch")
	assert.False(t, bMentionsDevelop, "/b has no configured base branch and must never see /a's leak onto it")
	assert.Equal(t, "develop", msg.bases["/a"])
	assert.Equal(t, "main", msg.bases["/b"], "/b auto-detects since it has no configured base")

	// Assertion 2: a failed gh query lands its repo in errs and out of
	// snapshots, without disturbing a sibling repo's successful result.
	_, aOK := msg.snapshots["/a"]
	assert.True(t, aOK, "/a's successful query must produce a snapshot")
	_, bOK := msg.snapshots["/b"]
	assert.False(t, bOK, "/b's failed query must not leave a snapshot")
	require.Error(t, msg.errs["/b"])
}

// TestGHPollJob_PollsARepositoryOnceUnderItsTopLevel: a global session
// in a subdirectory of an opened workspace's repository, or a picker
// opened there, asks the poll for the subdirectory too. It is one
// repository: queried and fetched once, under its top level, against the
// first path's configured base branch, for the issues both paths link,
// and the subdirectory is reported as an alias of the top level.
func TestGHPollJob_PollsARepositoryOnceUnderItsTopLevel(t *testing.T) {
	fake := &ghFakeExec{answers: map[string][]ghFakeAnswer{
		"/r": {
			{substr: "rev-parse --show-toplevel", out: "/r\n"},
			{substr: "rev-parse --verify --quiet refs/heads/develop", out: "shaaaaa1\n"},
			{substr: "pr list", out: "[]"},
			{substr: "issue list", out: "[]"},
		},
		"/r/sub": {{substr: "rev-parse --show-toplevel", out: "/r\n"}},
	}}
	req := ghPollRequest{
		repos:      []string{"/r", "/r/sub"},
		linked:     map[string][]int{"/r": {1}, "/r/sub": {7, 1}},
		configured: map[string]string{"/r": "develop", "/r/sub": "trunk"},
	}

	msg, ok := ghPollJob(req, fake)().(ghResult)
	require.True(t, ok)

	var lists []string
	views := map[string]int{}
	for _, c := range fake.calls {
		if strings.Contains(c.argv, "pr list") {
			lists = append(lists, c.repo)
		}
		for _, n := range []string{"1", "7"} {
			if strings.Contains(c.argv, "issue view "+n+" ") {
				views[c.repo+" #"+n]++
			}
		}
		assert.NotContains(t, c.argv, "trunk", "the subdirectory's base never reaches its repository")
	}
	assert.Equal(t, []string{"/r"}, lists, "one repository, one query")
	assert.Equal(t, map[string]int{"/r #1": 1, "/r #7": 1}, views, "both paths' linked issues, each once")
	assert.Equal(t, map[string]string{"/r/sub": "/r"}, msg.aliases)
	assert.Equal(t, map[string]string{"/r": "develop"}, msg.bases)
	assert.Contains(t, msg.snapshots, "/r")
	assert.Len(t, msg.snapshots, 1)
}

// A session whose path reaches a polled repository through a symlink, or
// lies in a subdirectory the poll found inside it, gets that repository's
// state and its linked issue, and a client finds the snapshot by any of
// those spellings.
func TestGHState_AnySpellingOfAPolledRepository(t *testing.T) {
	repo := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(repo, link))
	sub := filepath.Join(repo, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	key := canonicalDir(repo)

	m := NewForTest(Options{})
	at := func(title, path string, issue int) *session.Instance {
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: path, Program: "claude"})
		require.NoError(t, err)
		inst.Branch = "u/" + title
		inst.SetIssue(issue)
		hold(m, inst)
		return inst
	}
	viaLink, inSub := at("l", link, 7), at("s", sub, 8)
	assert.Equal(t, []int{7}, m.linkedIssues(repo), "the symlinked session's issue is linked")

	m.Deliver(GitHubAliasesForTest(GitHubResultForTest(true, "", map[string]github.Snapshot{key: {
		PRs:    map[string]github.PR{"u/l": {Number: 45, State: github.PROpen}, "u/s": {Number: 46, State: github.PROpen}},
		Issues: map[int]github.Issue{7: {Number: 7, Title: "Fix"}, 8: {Number: 8, Title: "Tidy"}},
	}}, nil), map[string]string{canonicalDir(sub): key}))

	for inst, want := range map[*session.Instance][2]any{viaLink: {45, "Fix"}, inSub: {46, "Tidy"}} {
		s := inst.GitHubState()
		assert.True(t, s.Known, inst.Path)
		assert.Equal(t, want, [2]any{s.PRNumber, s.IssueTitle}, inst.Path)
	}
	for _, spelling := range []string{repo, link, sub, link + "/"} {
		_, ok := m.GitHubSnapshot(spelling)
		assert.True(t, ok, "GitHubSnapshot(%q)", spelling)
		_, ok = m.githubView().GitHubSnapshot(spelling)
		assert.True(t, ok, "the view's GitHubSnapshot(%q)", spelling)
	}
}

func TestBaseFor_ReadsGHBases(t *testing.T) {
	m := NewForTest(Options{})
	m.ghBases = map[string]string{"/r": "origin/main"}
	assert.Equal(t, "origin/main", m.baseFor("/r"))
	assert.Equal(t, "", m.baseFor("/other"))
}

// TestPush_ErrorPathReportsTheError: an unstarted instance has no
// worktree, so the job reports the error; a success instead expedites the
// GitHub poll (deliverPush). This pins that the two outcomes are
// distinguishable.
func TestPush_ErrorPathReportsTheError(t *testing.T) {
	m := NewForTest(Options{})
	inst := readyInst(t, m)
	msg := m.pushInst(inst)()
	r, isResult := msg.(pushResult)
	assert.True(t, isResult && r.err != nil, "no worktree on a test instance: expected an error, got %#v", msg)
}

func TestDeliverPush_SuccessExpeditesGitHubAndAnErrorIsANotice(t *testing.T) {
	m := NewForTest(Options{})
	m.gate(gateGH).last = time.Now()

	m.Deliver(pushResult{})
	assert.True(t, m.gateDue(gateGH, time.Now()), "a push makes the next tick poll")

	m.Deliver(pushResult{err: errors.New("rejected")})
	out := m.Drain()
	require.Len(t, out.Events, 1)
	assert.Equal(t, Notice{Err: errors.New("rejected")}, out.Events[0])
}

// TestOpenExpeditesGitHub: a newly opened workspace's repo was not in
// openRepoPaths until now, so its first open makes the next tick poll. The load
// runs on a mock executor; its workspace terminal (a fake claude) starts
// on the private tmux server TestMain sets up.
func TestOpenExpeditesGitHub(t *testing.T) {
	def := config.Workspace{Name: "gh-expedite", Path: t.TempDir()}
	cfgDir := config.WorkspaceConfigDir(&def)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.StateFileName), []byte(`{"instances":[]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, config.ConfigFileName),
		[]byte(`{"default_program":"`+fakeClaude(t)+`"}`), 0o644))
	t.Cleanup(func() {
		_ = tmux.Command(context.Background(), "kill-session", "-t", tmux.SessionTarget(tmux.ToLoomTmuxName(def.Name))).Run()
	})
	m := NewForTest(Options{Registry: &config.WorkspaceRegistry{}, CmdExec: aliveExec()})
	ws, err := m.ensureLoaded(def)
	require.NoError(t, err)
	m.gate(gateGH).last = time.Now()
	require.False(t, m.gateDue(gateGH, time.Now()))

	_, err = m.Open(m.wsIDOf(ws))
	require.NoError(t, err)

	assert.True(t, m.gateDue(gateGH, time.Now()), "a newly opened workspace's repo is polled on the next tick")
}

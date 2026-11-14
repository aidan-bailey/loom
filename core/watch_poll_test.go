package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watchWorkspace builds a workspace over repo ("" for the global one) with
// the watcher switched on or off and the branch prefix "loom/".
func watchWorkspace(t *testing.T, repo string, on bool) *Workspace {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mutate(func(c *config.Config) {
		c.Watch = &on
		c.BranchPrefix = "loom/"
	})
	return NewWorkspace(WorkspaceParts{
		Ctx:    &config.WorkspaceContext{Name: "polyoxide", ConfigDir: t.TempDir(), RepoPath: repo},
		Config: cfg,
	})
}

func TestWatchFor_CreatesTheStateOnceWithItsMaps(t *testing.T) {
	m := NewForTest(Options{})
	ws := watchWorkspace(t, t.TempDir(), true)
	assert.Nil(t, ws.watch, "nothing until first use")

	st := m.watchFor(ws)
	require.NotNil(t, st)
	assert.Same(t, st, ws.watch)
	assert.Same(t, st, m.watchFor(ws), "the same state every time")
	assert.NotNil(t, st.inFlight, "Story 9 writes to it")
	assert.NotNil(t, st.actErrs, "Story 9 writes to it")
	assert.False(t, st.polling)
	assert.Nil(t, st.snap, "no tick decided yet")
}

func TestWatchOf_IsTheStateOfAServedWorkspaceWithTheWatcherOn(t *testing.T) {
	m := NewForTest(Options{})
	ws := watchWorkspace(t, t.TempDir(), true)
	m.SetWorkspacesForTest(ws)
	assert.Nil(t, m.watchOf(nil))
	assert.Nil(t, m.watchOf(ws), "no state until a poll made it")

	st := m.watchFor(ws)
	assert.Same(t, st, m.watchOf(ws))

	off := false
	ws.cfg.Mutate(func(c *config.Config) { c.Watch = &off })
	assert.Nil(t, m.watchOf(ws), "switched off: a late result records nothing")

	on := true
	ws.cfg.Mutate(func(c *config.Config) { c.Watch = &on })
	m.SetWorkspacesForTest()
	assert.Nil(t, m.watchOf(ws), "no longer served: a late result records nothing")
}

const watchTestConfig = `{"version":1,"label":"drift","escalate_label":"needs-human","title":"heal {issue}","session":{"prompt":".loom-watch/start.md"}}`

const watchHookTestConfig = `{"version":1,"label":"drift","escalate_label":"needs-human","title":"heal {issue}","session":{"prompt":".loom-watch/start.md"},"hook":{"command":["sh",".loom-watch/hook.sh"],"timeout":"10s"}}`

const watchTestPrompt = "START {issue}\n"

// watchFiles is origin's tree for a rig: the config and the start prompt.
func watchFiles(cfg string) map[string]string {
	return map[string]string{"loom-watch.json": cfg, ".loom-watch/start.md": watchTestPrompt}
}

// watchGH answers every gh command from a script and hands every other
// command (git) to the real executor, so a poll's git half runs against
// real repositories.
type watchGH struct {
	answer func(argv string) (string, error)
	calls  []string
}

func isGHCmd(c *exec.Cmd) bool { return len(c.Args) > 0 && c.Args[0] == "gh" }

func (w *watchGH) ask(c *exec.Cmd) ([]byte, error) {
	argv := strings.Join(c.Args[1:], " ")
	w.calls = append(w.calls, argv)
	out, err := w.answer(argv)
	return []byte(out), err
}

func (w *watchGH) Output(c *exec.Cmd) ([]byte, error) {
	if isGHCmd(c) {
		return w.ask(c)
	}
	return internalexec.Default{}.Output(c)
}

func (w *watchGH) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	if isGHCmd(c) {
		return w.ask(c)
	}
	return internalexec.Default{}.CombinedOutput(c)
}

func (w *watchGH) Run(c *exec.Cmd) error {
	if isGHCmd(c) {
		_, err := w.ask(c)
		return err
	}
	return internalexec.Default{}.Run(c)
}

// callsTo lists the recorded gh commands containing sub.
func (w *watchGH) callsTo(sub string) []string {
	var out []string
	for _, c := range w.calls {
		if strings.Contains(c, sub) {
			out = append(out, c)
		}
	}
	return out
}

// watchRig is a workspace over a real clone of a real origin, with a
// stubbed gh.
type watchRig struct {
	t      *testing.T
	m      *Model
	ws     *Workspace
	gh     *watchGH
	origin string
	repo   string

	// What the stubbed gh answers: the viewer's login, the PR and labelled
	// issue objects (gh's native JSON), and the issue views by number.
	viewer    string
	viewerErr error
	prs       []string
	prsErr    error
	issues    []string
	views     map[int]string
}

var watchViewRE = regexp.MustCompile(`issue view (\d+)`)

func (r *watchRig) answer(argv string) (string, error) {
	switch {
	case strings.Contains(argv, "api user"):
		return r.viewer + "\n", r.viewerErr
	case strings.Contains(argv, "pr list"):
		return "[" + strings.Join(r.prs, ",") + "]", r.prsErr
	case strings.Contains(argv, "issue list"):
		return "[" + strings.Join(r.issues, ",") + "]", nil
	}
	if sub := watchViewRE.FindStringSubmatch(argv); sub != nil {
		n, _ := strconv.Atoi(sub[1])
		if body, ok := r.views[n]; ok {
			return body, nil
		}
		return "", fmt.Errorf("gh: no issue %d", n)
	}
	return "", fmt.Errorf("unscripted gh %s", argv)
}

// newWatchRig builds a bare origin holding files on main, a clone of it as
// the workspace's repository, and a model serving that workspace with the
// watcher on. The viewer is "bot".
func newWatchRig(t *testing.T, files map[string]string) *watchRig {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)
	seed := t.TempDir()
	runGit(t, seed, "init", "-q", "-b", "main")
	for name, body := range files {
		path := filepath.Join(seed, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-qm", "seed")
	runGit(t, seed, "remote", "add", "origin", origin)
	runGit(t, seed, "push", "-q", "origin", "main")
	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, t.TempDir(), "clone", "-q", origin, repo)

	rig := &watchRig{t: t, origin: origin, repo: repo, viewer: "bot", views: map[int]string{}}
	rig.gh = &watchGH{answer: rig.answer}
	rig.ws = watchWorkspace(t, repo, true)
	rig.m = NewForTest(Options{GHExec: rig.gh})
	rig.m.SetWorkspacesForTest(rig.ws)
	return rig
}

// job runs one tick's poll job to its result, as the loop would.
func (r *watchRig) job() watchResult {
	r.t.Helper()
	res, ok := watchPollJob(r.m.watchRequest(r.ws, false, 0), r.m.watchExec())().(watchResult)
	require.True(r.t, ok, "a poll job answers with a watchResult")
	return res
}

// addSession adds a session titled title, linked to issue, in status, to
// the workspace (built from a record, so no tmux is involved).
func (r *watchRig) addSession(title string, issue int, status session.Status) *session.Instance {
	r.t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude",
		Branch: "loom/" + strings.ReplaceAll(title, " ", "-"),
	}, r.t.TempDir())
	require.NoError(r.t, err)
	inst.SetIssue(issue)
	if status != session.Paused {
		require.NoError(r.t, inst.TransitionTo(session.Loading))
		require.NoError(r.t, inst.TransitionTo(status))
	}
	r.ws.insts = append(r.ws.insts, inst)
	return inst
}

// gitOut runs git in dir and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := internalexec.GitCommand(context.Background(), dir, args...).Output()
	require.NoError(t, err, "git %v", args)
	return strings.TrimSpace(string(out))
}

func TestWatchPoll_ReadsTheConfigAndPromptsFromOriginNotTheCheckout(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	// An unpushed local commit rewrites the label: origin's file must win.
	local := strings.Replace(watchTestConfig, `"drift"`, `"local"`, 1)
	require.NoError(t, os.WriteFile(filepath.Join(rig.repo, "loom-watch.json"), []byte(local), 0o644))
	runGit(t, rig.repo, "commit", "-qam", "local edit")

	res := rig.job()

	require.Empty(t, res.err)
	assert.True(t, res.cfgOK)
	assert.Equal(t, "drift", res.cfg.Label, "the config is origin's, not the working tree's")
	assert.Equal(t, watchTestPrompt, res.prompt)
	assert.Equal(t, watchTestPrompt, res.resumePrompt, "the resume prompt defaults to the start prompt")
	issue, _, ok := res.tmpl.Parse("heal 7")
	assert.True(t, ok)
	assert.Equal(t, 7, issue)
	assert.Equal(t, "main", res.base)
	assert.Equal(t, gitOut(t, rig.origin, "rev-parse", "main"), res.baseSHA, "the fetched origin/main, not the local main")
	assert.True(t, res.startOK)
	assert.Equal(t, filepath.Join(rig.ws.ctx.ConfigDir, "watch-checkout"), res.checkout)
	assert.FileExists(t, filepath.Join(res.checkout, ".loom-watch", "start.md"), "the checkout was refreshed at the fetched commit")
	assert.Equal(t, filepath.Join(rig.ws.ctx.ConfigDir, "watch.json"), res.memPath)
	assert.NotNil(t, res.mem, "the first poll loads the memory")
}

func TestWatchPoll_AMissingOrInvalidConfigDecidesNothing(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		rig := newWatchRig(t, map[string]string{"README.md": "x"})
		res := rig.job()
		assert.Equal(t, "no loom-watch.json on origin/main", res.err)
		assert.False(t, res.cfgOK)
		assert.Nil(t, res.mem, "nothing after the config is read")
		assert.Empty(t, rig.gh.calls, "no gh read without a config")
	})
	t.Run("invalid", func(t *testing.T) {
		rig := newWatchRig(t, watchFiles(`{"version":1}`))
		res := rig.job()
		assert.Contains(t, res.err, "loom-watch.json")
		assert.False(t, res.cfgOK)
		assert.Empty(t, rig.gh.calls)
	})
	t.Run("a variable title with no hook", func(t *testing.T) {
		rig := newWatchRig(t, watchFiles(strings.Replace(watchTestConfig, "heal {issue}", "heal {issue} {name}", 1)))
		res := rig.job()
		assert.Contains(t, res.err, "title uses {name}")
		assert.False(t, res.cfgOK)
		assert.Empty(t, rig.gh.calls)
	})
	t.Run("a variable title with a hook", func(t *testing.T) {
		rig := newWatchRig(t, watchFiles(strings.Replace(watchHookTestConfig, "heal {issue}", "heal {issue} {name}", 1)))
		assert.True(t, rig.job().cfgOK)
	})
	t.Run("prompt missing", func(t *testing.T) {
		rig := newWatchRig(t, map[string]string{"loom-watch.json": watchTestConfig})
		res := rig.job()
		assert.Contains(t, res.err, ".loom-watch/start.md")
		assert.False(t, res.cfgOK, "a config whose prompt cannot be read is no config")
	})
}

func TestWatchPoll_APromptOver64KiBIsRefused(t *testing.T) {
	over := watchFiles(watchTestConfig)
	over[".loom-watch/start.md"] = strings.Repeat("a", watchFileLimit+1)
	res := newWatchRig(t, over).job()
	assert.Contains(t, res.err, "64 KiB")
	assert.False(t, res.cfgOK)

	exact := watchFiles(watchTestConfig)
	exact[".loom-watch/start.md"] = strings.Repeat("a", watchFileLimit)
	res = newWatchRig(t, exact).job()
	assert.Empty(t, res.err, "64 KiB exactly is allowed")
	assert.Len(t, res.prompt, watchFileLimit)
}

func TestWatchPoll_AFailedFetchStartsNothingButStillReadsTheConfig(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	runGit(t, rig.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	res := rig.job()

	require.Empty(t, res.err, "a failed fetch is not a failed tick: rules 1 to 8 run")
	assert.False(t, res.startOK)
	assert.Contains(t, res.startWhy, "git fetch origin main failed")
	assert.Empty(t, res.baseSHA)
	assert.True(t, res.cfgOK, "the config comes from origin/main as it stands")
	assert.Equal(t, "drift", res.cfg.Label)
	assert.NoDirExists(t, res.checkout, "no refresh without a fetch")
}

func TestWatchPoll_TheMemoryIsLoadedOnceAndABadFileStopsTheWatcher(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	path := filepath.Join(rig.ws.ctx.ConfigDir, "watch.json")

	require.NoError(t, os.WriteFile(path, []byte(`{not json`), 0o644))
	res := rig.job()
	assert.Contains(t, res.err, "watch.json")
	assert.Nil(t, res.mem)
	kept, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{not json`, string(kept), "a file that cannot be read is left as it is")

	require.NoError(t, watch.SaveMemory(path, watch.Memory{Version: 1, PRs: map[int]watch.PRMemory{5: {Settled: true}}}))
	res = rig.job()
	require.Empty(t, res.err)
	require.NotNil(t, res.mem)
	assert.True(t, res.mem.PRs[5].Settled, "the file's contents")

	rq := rig.m.watchRequest(rig.ws, false, 0)
	rq.loadMem = false
	res, ok := watchPollJob(rq, rig.m.watchExec())().(watchResult)
	require.True(t, ok)
	assert.Nil(t, res.mem, "once loaded, the memory belongs to the loop: the job does not reread it")
}

func TestWatchPoll_OriginsSlugIsReadOnlyForAHook(t *testing.T) {
	files := watchFiles(watchHookTestConfig)
	files[".loom-watch/hook.sh"] = "exit 0\n"
	res := newWatchRig(t, files).job()
	require.Empty(t, res.err)
	assert.True(t, strings.HasSuffix(res.slug, "/origin"), "the last two parts of origin's URL: %q", res.slug)

	res = newWatchRig(t, watchFiles(watchTestConfig)).job()
	require.Empty(t, res.err)
	assert.Empty(t, res.slug, "nothing reads the remote when no hook needs it")
}

func TestRepoSlug(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:aidan-bailey/loom.git":       "aidan-bailey/loom",
		"https://github.com/aidan-bailey/loom":       "aidan-bailey/loom",
		"https://github.com/aidan-bailey/loom.git/":  "aidan-bailey/loom",
		"ssh://git@github.com/aidan-bailey/loom.git": "aidan-bailey/loom",
		"/srv/git/origin.git":                        "git/origin",
		"loom":                                       "",
		"":                                           "",
	} {
		assert.Equal(t, want, repoSlug(remote), remote)
	}
}

func TestWatchRequest_CopiesWhatTheJobNeedsOffTheLoop(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.addSession("heal 7", 7, session.Paused)
	st := rig.m.watchFor(rig.ws)
	st.viewer, st.repo = "cached", "o/r"

	rq := rig.m.watchRequest(rig.ws, true, 9)

	assert.Same(t, rig.ws, rq.ws)
	assert.True(t, rq.dry)
	assert.Equal(t, ReqID(9), rq.req)
	assert.Equal(t, "polyoxide", rq.name)
	assert.Equal(t, rig.repo, rq.repo)
	assert.Equal(t, rig.ws.ctx.ConfigDir, rq.configDir)
	assert.Equal(t, "loom/", rq.prefix)
	assert.Equal(t, "cached", rq.viewer)
	assert.Equal(t, "o/r", rq.slug)
	assert.True(t, rq.loadMem, "the memory is not loaded yet")
	require.Len(t, rq.sessions, 1)
	assert.Equal(t, "heal 7", rq.sessions[0].Title)

	st.memLoaded = true
	st.mem.Settle([]int{5})
	rq = rig.m.watchRequest(rig.ws, false, 0)
	assert.False(t, rq.loadMem)
	assert.Equal(t, map[int]bool{5: true}, rq.settled)
}

func TestWatchSessions(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	terminal := rig.addSession("terminal", 0, session.Paused)
	terminal.IsWorkspaceTerminal = true
	paused := rig.addSession("heal 7", 7, session.Paused)
	ready := rig.addSession("scratch", 0, session.Ready)

	got := rig.m.watchSessions(rig.ws)

	require.Len(t, got, 2, "the workspace terminal is no session")
	assert.Equal(t, watch.Session{
		ID: uint64(rig.m.idOf(paused)), Title: "heal 7", Status: "Paused",
		Since: watchEpoch, Branch: "loom/heal-7", Issue: 7,
	}, got[0], "a transition no one saw this process starts at the process start, not the year 1")
	assert.Equal(t, uint64(rig.m.idOf(ready)), got[1].ID)
	assert.Equal(t, "Ready", got[1].Status)
	assert.True(t, got[1].Since.After(watchEpoch) || got[1].Since.Equal(watchEpoch))
	assert.Less(t, time.Since(got[1].Since), time.Minute, "an observed transition keeps its time")
}

// ghPRJSON is one PR as `gh pr list --json` prints it.
func ghPRJSON(number int, state, branch, author string, closes ...int) string {
	refs := make([]string, len(closes))
	for i, n := range closes {
		refs[i] = fmt.Sprintf(`{"number":%d,"repository":{"name":"r","owner":{"login":"o"}}}`, n)
	}
	return fmt.Sprintf(`{"number":%d,"state":%q,"headRefName":%q,"baseRefName":"main","headRefOid":"head%d","baseRefOid":"base1","mergeable":"MERGEABLE","author":{"login":%q},"isCrossRepository":false,"closingIssuesReferences":[%s],"statusCheckRollup":[],"body":"","url":"https://github.com/o/r/pull/%d"}`,
		number, state, branch, number, author, strings.Join(refs, ","), number)
}

// ghIssueJSON is one issue as `gh issue list|view --json` prints it.
func ghIssueJSON(number int, state string) string {
	return fmt.Sprintf(`{"number":%d,"title":"issue %d","body":"body","url":"https://github.com/o/r/issues/%d","labels":[{"name":"drift"}],"state":%q,"comments":[]}`,
		number, number, number, state)
}

func issueNumbers(is []github.WatchIssue) []int {
	out := make([]int, len(is))
	for i, x := range is {
		out[i] = x.Number
	}
	return out
}

func TestWatchPoll_ReadsTheViewerThePRsAndTheLabelledIssues(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}
	rig.prs = []string{ghPRJSON(40, "OPEN", "loom/heal-7", "bot", 7)}

	res := rig.job()

	require.Empty(t, res.err)
	assert.Equal(t, "bot", res.viewer)
	assert.Equal(t, "bot", res.snap.Viewer)
	assert.Equal(t, "loom/", res.snap.Prefix)
	assert.Equal(t, []int{7}, issueNumbers(res.snap.Issues))
	require.Len(t, res.snap.PRs, 1)
	assert.Equal(t, 40, res.snap.PRs[0].Number)
	assert.Equal(t, 1, res.snap.Considered)
	assert.Zero(t, res.snap.Unconsidered)
	assert.Len(t, rig.gh.callsTo("api user"), 1)
	assert.Contains(t, rig.gh.callsTo("issue list")[0], "drift", "the issue list is of the config's label")

	rq := rig.m.watchRequest(rig.ws, false, 0)
	rq.viewer = "cached"
	rig.gh.calls = nil
	res, ok := watchPollJob(rq, rig.m.watchExec())().(watchResult)
	require.True(t, ok)
	assert.Empty(t, rig.gh.callsTo("api user"), "a cached viewer is not read again")
	assert.Equal(t, "cached", res.snap.Viewer)
}

func TestWatchPoll_AFailedGHReadDecidesNothing(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.prsErr = fmt.Errorf("boom")
	res := rig.job()
	assert.Contains(t, res.err, "pr list")
	assert.Empty(t, res.snap.Issues, "nothing is half-read")

	rig = newWatchRig(t, watchFiles(watchTestConfig))
	rig.viewerErr = fmt.Errorf("not logged in")
	res = rig.job()
	assert.Contains(t, res.err, "viewer")
	assert.Empty(t, rig.gh.callsTo("pr list"), "ownership is unknown without the viewer: no further read")
}

func TestWatchPoll_KeepsFiftyIssuesAndCountsTheFiftyFirst(t *testing.T) {
	many := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = ghIssueJSON(i+1, "OPEN")
		}
		return out
	}
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.issues = many(51)
	res := rig.job()
	require.Empty(t, res.err)
	assert.Equal(t, 50, res.snap.Considered)
	assert.Equal(t, 1, res.snap.Unconsidered, "a 51st came back: more were not considered")
	assert.Len(t, res.snap.Issues, 50)
	assert.Equal(t, 50, res.snap.Issues[49].Number, "the 50 lowest-numbered")

	rig = newWatchRig(t, watchFiles(watchTestConfig))
	rig.issues = many(50)
	res = rig.job()
	require.Empty(t, res.err)
	assert.Equal(t, 50, res.snap.Considered)
	assert.Zero(t, res.snap.Unconsidered, "exactly 50 is all of them")
}

func TestWatchPoll_ViewsOnlyTheClosedIssuesItsOwnSessionsAndPRsName(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}
	// Issue 8 is named by its session, 9 by its open PR: both are viewed.
	rig.addSession("heal 8", 8, session.Paused)
	// 12's session title is not its template's, 11's PR is another author's
	// and 13's branch is not its pattern: none is viewed.
	rig.addSession("other 12", 12, session.Ready)
	rig.prs = []string{
		ghPRJSON(41, "OPEN", "loom/heal-9", "bot", 9),
		ghPRJSON(42, "MERGED", "loom/heal-11", "someone", 11),
		ghPRJSON(43, "MERGED", "other/branch", "bot", 13),
	}
	rig.views = map[int]string{8: ghIssueJSON(8, "CLOSED"), 9: ghIssueJSON(9, "CLOSED")}

	res := rig.job()

	require.Empty(t, res.err)
	assert.Equal(t, []int{7, 8, 9}, issueNumbers(res.snap.Issues), "the labelled issue and the two it viewed, in number order")
	assert.Equal(t, 1, res.snap.Considered, "Considered counts the labelled issues only")
	views := rig.gh.callsTo("issue view")
	require.Len(t, views, 2)
	assert.Contains(t, views[0], "issue view 8")
	assert.Contains(t, views[1], "issue view 9")
	assert.True(t, res.snap.Issues[1].Closed)
}

func TestWatchPoll_ViewsNothingForIssuesOnlyMergedOrSettledPRsName(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	st := rig.m.watchFor(rig.ws)
	st.memLoaded = true
	st.mem.Settle([]int{52})
	rig.prs = []string{
		ghPRJSON(51, "MERGED", "loom/heal-1", "bot", 1),
		ghPRJSON(52, "CLOSED", "loom/heal-2", "bot", 2), // settled in memory
		ghPRJSON(53, "CLOSED", "loom/heal-3", "bot", 3), // not settled yet
	}
	rig.views = map[int]string{3: ghIssueJSON(3, "OPEN")}

	res := rig.job()

	require.Empty(t, res.err)
	views := rig.gh.callsTo("issue view")
	require.Len(t, views, 1, "only the closed PR memory has not settled names an issue worth viewing")
	assert.Contains(t, views[0], "issue view 3")
	assert.Equal(t, []int{3}, issueNumbers(res.snap.Issues))
}

func TestWatchPoll_SettledIsReadFromThePollsOwnMemoryBeforeTheLoopHoldsIt(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	mem := watch.Memory{Version: 1}
	mem.Settle([]int{53})
	require.NoError(t, watch.SaveMemory(filepath.Join(rig.ws.ctx.ConfigDir, watch.MemoryFile), mem))
	rig.prs = []string{ghPRJSON(53, "CLOSED", "loom/heal-3", "bot", 3)}

	res := rig.job()

	require.Empty(t, res.err)
	require.NotNil(t, res.mem, "this poll loaded the memory")
	assert.Empty(t, rig.gh.callsTo("issue view"))
}

func TestWatchPoll_OldMergedIssuesDoNotCrowdOutALaterIssueWithASession(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	for n := 1; n <= 12; n++ {
		rig.prs = append(rig.prs, ghPRJSON(100+n, "MERGED", fmt.Sprintf("loom/heal-%d", n), "bot", n))
	}
	rig.prs = append(rig.prs, ghPRJSON(200, "MERGED", "loom/heal-90", "bot", 90))
	rig.addSession("heal 90", 90, session.Ready)
	rig.views[90] = ghIssueJSON(90, "CLOSED")

	res := rig.job()

	require.Empty(t, res.err)
	assert.Equal(t, []int{90}, issueNumbers(res.snap.Issues), "the merged issue with a live session is viewed, the twelve old ones are not")
	assert.Len(t, rig.gh.callsTo("issue view"), 1)
}

func TestWatchPoll_SessionsAreViewedBeforeThePRsOfIssuesWithoutOne(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	for n := 1; n <= 10; n++ {
		rig.prs = append(rig.prs, ghPRJSON(100+n, "OPEN", fmt.Sprintf("loom/heal-%d", n), "bot", n))
		rig.views[n] = ghIssueJSON(n, "CLOSED")
	}
	rig.addSession("heal 90", 90, session.Paused)
	rig.views[90] = ghIssueJSON(90, "CLOSED")

	res := rig.job()

	require.Empty(t, res.err)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 90}, issueNumbers(res.snap.Issues), "the session's issue takes a place, then the lowest nine")
}

func TestWatchPoll_ViewsAtMostTenClosedIssuesLowestFirst(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	for n := 21; n <= 32; n++ {
		rig.addSession(fmt.Sprintf("heal %d", n), n, session.Paused)
		rig.views[n] = ghIssueJSON(n, "CLOSED")
	}

	res := rig.job()

	require.Empty(t, res.err)
	assert.Len(t, rig.gh.callsTo("issue view"), 10)
	assert.Equal(t, []int{21, 22, 23, 24, 25, 26, 27, 28, 29, 30}, issueNumbers(res.snap.Issues), "the lowest ten; the rest wait for a later tick")
}

func TestWatchPoll_AFailedViewSkipsOnlyThatIssue(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.addSession("heal 8", 8, session.Paused)
	rig.addSession("heal 9", 9, session.Paused)
	rig.views = map[int]string{9: ghIssueJSON(9, "CLOSED")} // 8's view fails

	res := rig.job()

	require.Empty(t, res.err, "a failed view is not a failed tick")
	assert.Equal(t, []int{9}, issueNumbers(res.snap.Issues))
}

// watchHookFiles is a rig's origin tree with a hook script that records its
// calls, its stdin and its environment beside the checkout, and answers.
func watchHookFiles(script string) map[string]string {
	files := watchFiles(watchHookTestConfig)
	files[".loom-watch/hook.sh"] = script
	return files
}

const watchNudgingHook = `echo "$1" >> ../hook-calls.txt
cat > ../hook-input.json
echo "$LOOM_WATCH_WORKSPACE" > ../hook-workspace.txt
echo "$LOOM_WATCH_REPO" > ../hook-repo.txt
echo '{"nudges":[{"kind":"redrift","key":"k1","text":"look again"}]}'
`

func TestWatchPoll_TheHookNudgesAReadySessionWithAnOpenPR(t *testing.T) {
	rig := newWatchRig(t, watchHookFiles(watchNudgingHook))
	rig.issues = []string{ghIssueJSON(7, "OPEN"), ghIssueJSON(9, "OPEN"), ghIssueJSON(10, "OPEN")}
	rig.prs = []string{
		ghPRJSON(40, "OPEN", "loom/heal-7", "bot", 7),
		ghPRJSON(42, "OPEN", "loom/heal-9", "bot", 9),
		ghPRJSON(43, "MERGED", "loom/heal-10", "bot", 10),
	}
	// 7 is Ready with an open PR: asked. 9 has an open PR but its session
	// is Paused, and 10's is Ready but its PR is merged: neither is asked.
	rig.addSession("heal 7", 7, session.Ready)
	rig.addSession("heal 9", 9, session.Paused)
	rig.addSession("heal 10", 10, session.Ready)

	res := rig.job()

	require.Empty(t, res.err)
	assert.Equal(t, map[int][]watch.Nudge{7: {{Kind: "redrift", Key: "k1", Text: "look again"}}}, res.snap.HookNudges)
	assert.Empty(t, res.snap.HookFailed)
	cfgDir := rig.ws.ctx.ConfigDir
	calls, err := os.ReadFile(filepath.Join(cfgDir, "hook-calls.txt"))
	require.NoError(t, err)
	assert.Equal(t, "nudges\n", string(calls), "one call, for the one issue that qualifies")
	input, err := os.ReadFile(filepath.Join(cfgDir, "hook-input.json"))
	require.NoError(t, err)
	assert.Contains(t, string(input), `"point":"nudges"`)
	assert.Contains(t, string(input), `"head_sha":"head40"`)
	workspace, err := os.ReadFile(filepath.Join(cfgDir, "hook-workspace.txt"))
	require.NoError(t, err)
	assert.Equal(t, "polyoxide\n", string(workspace), "the hook's environment names the workspace")
	repo, err := os.ReadFile(filepath.Join(cfgDir, "hook-repo.txt"))
	require.NoError(t, err)
	assert.Equal(t, res.slug+"\n", string(repo), "and the repository's owner/name")
}

func TestWatchPoll_AFailedHookCostsOnlyThatIssuesNudges(t *testing.T) {
	rig := newWatchRig(t, watchHookFiles("echo boom >&2\nexit 3\n"))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}
	rig.prs = []string{ghPRJSON(40, "OPEN", "loom/heal-7", "bot", 7)}
	rig.addSession("heal 7", 7, session.Ready)

	res := rig.job()

	require.Empty(t, res.err, "the tick still decides")
	assert.Empty(t, res.snap.HookNudges)
	assert.Contains(t, res.snap.HookFailed[7], "boom", "the hook's stderr is kept for status")
}

func TestWatchPoll_ASpentHookBudgetFailsTheRemainingIssuesWithoutCallingTheHook(t *testing.T) {
	rig := newWatchRig(t, watchHookFiles(watchNudgingHook))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}
	rig.prs = []string{ghPRJSON(40, "OPEN", "loom/heal-7", "bot", 7)}
	rig.addSession("heal 7", 7, session.Ready)
	was := watchHookBudget
	watchHookBudget = 0
	t.Cleanup(func() { watchHookBudget = was })

	res := rig.job()

	require.Empty(t, res.err, "the tick still decides")
	assert.Empty(t, res.snap.HookNudges)
	assert.Contains(t, res.snap.HookFailed[7], "time budget", "the issue is told why it got no nudges")
	_, err := os.Stat(filepath.Join(rig.ws.ctx.ConfigDir, "hook-calls.txt"))
	assert.True(t, os.IsNotExist(err), "the hook was never run")
}

// actionsOfKind lists the actions of kind k.
func actionsOfKind(as []watch.Action, k watch.ActionKind) []watch.Action {
	var out []watch.Action
	for _, a := range as {
		if a.Kind == k {
			out = append(out, a)
		}
	}
	return out
}

// tick runs one poll the way the loop would and delivers its result.
func (r *watchRig) tick() watchResult {
	r.t.Helper()
	r.m.watchFor(r.ws).polling = true
	res := r.job()
	r.m.Deliver(res)
	return res
}

func TestDeliverWatch_DecidesOnTheLoopAndKeepsTheTick(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}

	rig.tick()

	st := rig.ws.watch
	require.NotNil(t, st.snap)
	assert.Empty(t, st.err)
	assert.Equal(t, []watch.Action{{Kind: watch.ActStart, Issue: 7}}, st.actions, "rule 9: a labelled issue with nothing yet is started")
	assert.Equal(t, 1, st.snap.Considered)
	assert.False(t, st.snap.Now.IsZero(), "the loop stamps the tick")
	assert.Equal(t, st.snap.Now, st.at)
	assert.Equal(t, "bot", st.viewer)
	assert.True(t, st.memLoaded)
	assert.Equal(t, "main", st.base)
	assert.Equal(t, gitOut(t, rig.origin, "rev-parse", "main"), st.baseSHA)
	assert.True(t, st.startOK)
	assert.Equal(t, watchTestPrompt, st.prompt)
	assert.Equal(t, watchTestPrompt, st.resumePrompt)
	assert.Equal(t, "drift", st.cfg.Label)
	assert.Equal(t, filepath.Join(rig.ws.ctx.ConfigDir, "watch-checkout"), st.checkout)
	assert.Equal(t, filepath.Join(rig.ws.ctx.ConfigDir, "watch.json"), st.memPath)
	assert.False(t, st.polling, "every delivery clears the poll's in-flight bit")
	assert.Len(t, rig.m.Drain().Jobs, 1, "the tick dispatches its start: the hook and render job")
	assert.True(t, st.inFlight[watchActKey(watch.Action{Kind: watch.ActStart, Issue: 7})])
}

func TestDeliverWatch_ATickWithoutAFetchWaitsInsteadOfStartingAndStillKills(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	runGit(t, rig.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}
	merged := rig.addSession("heal 8", 8, session.Paused)
	rig.prs = []string{ghPRJSON(41, "MERGED", "loom/heal-8", "bot", 8)}
	rig.views = map[int]string{8: ghIssueJSON(8, "CLOSED")}

	rig.tick()

	st := rig.ws.watch
	require.Empty(t, st.err)
	assert.False(t, st.startOK)
	assert.Empty(t, actionsOfKind(st.actions, watch.ActStart), "no start without a fetch")
	waits := actionsOfKind(st.actions, watch.ActWait)
	require.Len(t, waits, 1)
	assert.Equal(t, 7, waits[0].Issue)
	assert.Contains(t, waits[0].Reason, "no start this tick")
	assert.Contains(t, waits[0].Reason, "git fetch origin main failed")
	assert.Equal(t, []watch.Action{{Kind: watch.ActKill, Issue: 8, Session: uint64(rig.m.idOf(merged))}},
		actionsOfKind(st.actions, watch.ActKill), "rule 1 still runs: the merged PR's session is killed")
}

func TestDeliverWatch_AFailedTickClearsWhatTheLastOneDecided(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}
	rig.tick()
	require.NotEmpty(t, rig.ws.watch.actions)

	rig.prsErr = fmt.Errorf("boom")
	rig.tick()

	st := rig.ws.watch
	assert.Contains(t, st.err, "pr list")
	assert.Nil(t, st.snap, "stale decisions are worse than none")
	assert.Nil(t, st.actions)
	assert.False(t, st.polling, "errors clear the in-flight bit too")
	assert.Equal(t, "bot", st.viewer, "the viewer read before the failure is kept")
}

func TestDeliverWatch_ABadMemoryFileKeepsTheWatcherStopped(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}
	path := filepath.Join(rig.ws.ctx.ConfigDir, "watch.json")
	require.NoError(t, os.WriteFile(path, []byte(`{not json`), 0o644))

	rig.tick()
	st := rig.ws.watch
	assert.Contains(t, st.err, "watch.json")
	assert.Nil(t, st.actions)
	assert.False(t, st.memLoaded, "the next poll reads the file again")

	require.NoError(t, os.Remove(path))
	rig.tick()
	assert.Empty(t, st.err)
	assert.True(t, st.memLoaded)
	assert.NotEmpty(t, st.actions)
}

func TestDeliverWatch_AMemoryTheLoopOwnsIsNotReplacedByAnotherPoll(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.tick()
	st := rig.ws.watch
	st.mem.RecordEscalation(7, "stuck", time.Now()) // what Story 9's actions do

	rig.tick()

	assert.Contains(t, st.mem.Issues, 7, "the second poll did not reread the file over the loop's memory")
}

func TestDeliverWatch_AWorkspaceSwitchedOffMidFlightRecordsNothing(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}
	st := rig.m.watchFor(rig.ws)
	st.polling = true
	res := rig.job()

	off := false
	rig.ws.cfg.Mutate(func(c *config.Config) { c.Watch = &off })
	rig.m.Deliver(res)

	assert.False(t, st.polling, "the in-flight bit clears even for a result that is dropped")
	assert.Nil(t, st.snap)
	assert.Nil(t, st.actions)
	assert.Empty(t, st.viewer)
	assert.False(t, st.memLoaded)
}

func TestDeliverWatch_AWorkspaceNoLongerServedRecordsNothing(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	st := rig.m.watchFor(rig.ws)
	st.polling = true
	res := rig.job()

	rig.m.SetWorkspacesForTest()
	rig.m.Deliver(res)

	assert.False(t, st.polling)
	assert.Nil(t, st.snap)
	assert.False(t, st.memLoaded)
}

func TestDeliverWatch_ADryResultStoresNothingButDecides(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	rig.issues = []string{ghIssueJSON(7, "OPEN")}
	st := rig.m.watchFor(rig.ws)
	st.polling = true
	res, ok := watchPollJob(rig.m.watchRequest(rig.ws, true, 9), rig.m.watchExec())().(watchResult)
	require.True(t, ok)
	require.True(t, res.dry)
	assert.Equal(t, ReqID(9), res.req)

	_, actions := rig.m.decideWatch(res)
	assert.Equal(t, []watch.Action{{Kind: watch.ActStart, Issue: 7}}, actions, "a dry run decides what the next tick would")

	rig.m.Deliver(res)

	assert.False(t, st.polling, "a dry poll clears the in-flight bit too")
	assert.Nil(t, st.snap, "a dry run records no tick")
	assert.Nil(t, st.actions)
	assert.Empty(t, st.viewer)
	assert.False(t, st.memLoaded)
}

func TestDeliverWatch_ADryResultIsHandledWithNoWatchStateAndAnOffWatcher(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))
	off := false
	rig.ws.cfg.Mutate(func(c *config.Config) { c.Watch = &off })
	require.Nil(t, rig.ws.watch, "no poll ever ran for this workspace")

	rig.m.Deliver(watchResult{ws: rig.ws, dry: true, req: 3})

	assert.Nil(t, rig.ws.watch, "a dry result creates no state and stores nothing")
	assert.Nil(t, rig.m.watchOf(rig.ws), "a switched-off watcher has no state to answer from")
}

func TestStartWatchPoll_OnePollPerWorkspaceAtATime(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))

	require.True(t, rig.m.startWatchPoll(rig.ws, false, 0))
	assert.True(t, rig.ws.watch.polling)
	assert.Len(t, rig.m.bgJobs, 1, "the tick's poll is background work")
	assert.Empty(t, rig.m.out.Jobs, "and no request's follow-on")
	jobs := rig.m.Drain().Jobs
	require.Len(t, jobs, 1)
	_, isResult := jobs[0]().(watchResult)
	assert.True(t, isResult, "the tick's poll is a background job: its result is not wrapped for a request")

	assert.False(t, rig.m.startWatchPoll(rig.ws, false, 0), "one is out")
	assert.False(t, rig.m.startWatchPoll(rig.ws, true, 9), "a dry run waits for it too")
	assert.Empty(t, rig.m.Drain().Jobs)
}

func TestStartWatchPoll_ADryRunServesItsRequest(t *testing.T) {
	rig := newWatchRig(t, watchFiles(watchTestConfig))

	require.True(t, rig.m.startWatchPoll(rig.ws, true, 9), "ReadWatch calls it this way, outside causedBy")
	assert.Len(t, rig.m.out.Jobs, 1, "a dry run is spawned as a follow-on")
	assert.Empty(t, rig.m.bgJobs, "not as background work")

	jobs := rig.m.Drain().Jobs
	require.Len(t, jobs, 1, "a dry run is a request's follow-on (spawn), so Drain returns it and Quiesce waits for it")
	res, ok := jobs[0]().(watchResult)
	require.True(t, ok, "the result carries the request itself and is answered through res.req, not wrapped as caused")
	assert.True(t, res.dry)
	assert.Equal(t, ReqID(9), res.req)
}

func TestStartWatchPoll_RefusesAWorkspaceItCannotPoll(t *testing.T) {
	m := NewForTest(Options{})
	global := watchWorkspace(t, "", true)
	served := watchWorkspace(t, t.TempDir(), true)
	m.SetWorkspacesForTest(global)

	assert.False(t, m.startWatchPoll(global, false, 0), "the global workspace has no repository")
	assert.False(t, m.startWatchPoll(served, false, 0), "a workspace the model does not serve")
	assert.False(t, m.startWatchPoll(nil, false, 0))
	assert.Nil(t, global.watch)
	assert.Nil(t, served.watch)
	assert.Empty(t, m.Drain().Jobs)
}

func TestMaybeWatch_PollsOnlyWorkspacesWithARepositoryAndTheWatcherOn(t *testing.T) {
	on := watchWorkspace(t, t.TempDir(), true)
	off := watchWorkspace(t, t.TempDir(), false)
	global := watchWorkspace(t, "", true)
	broken := watchWorkspace(t, t.TempDir(), true)
	broken.loadErr = fmt.Errorf("unreadable")
	resetting := watchWorkspace(t, t.TempDir(), true)
	resetting.resetting = true
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(on, off, global, broken, resetting)

	require.True(t, m.maybeWatch())

	assert.Len(t, m.Drain().Jobs, 1, "one poll, for the one workspace that qualifies")
	require.NotNil(t, on.watch)
	assert.True(t, on.watch.polling)
	assert.Nil(t, off.watch, "switched off: nothing is created or polled")
	assert.Nil(t, global.watch)
	assert.Nil(t, broken.watch, "a workspace that failed to load is not polled")
	assert.Nil(t, resetting.watch, "a workspace being reset is not polled")
}

func TestMaybeWatch_KeepsItsCadenceAndArmsNothingWithoutAJob(t *testing.T) {
	ws := watchWorkspace(t, t.TempDir(), true)
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)

	require.True(t, m.maybeWatch())
	m.Drain()
	ws.watch.polling = false
	assert.False(t, m.maybeWatch(), "inside watchInterval")
	assert.Empty(t, m.Drain().Jobs)

	m.gate(gateWatch).last = time.Now().Add(-watchInterval)
	assert.True(t, m.maybeWatch(), "the interval has passed")
	m.Drain()

	m.gate(gateWatch).last = time.Time{}
	assert.False(t, m.maybeWatch(), "the workspace's poll is still out")
	assert.True(t, m.gate(gateWatch).last.IsZero(), "no job was dispatched, so nothing is armed")

	none := NewForTest(Options{})
	assert.False(t, none.maybeWatch(), "no workspace at all")
	assert.True(t, none.gate(gateWatch).last.IsZero())
}

func TestTickInst_PollsTheWatcher(t *testing.T) {
	ws := watchWorkspace(t, t.TempDir(), true)
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(ws)

	m.tickInst(nil)

	require.NotNil(t, ws.watch)
	assert.True(t, ws.watch.polling, "the health tick starts the watcher's poll")
}

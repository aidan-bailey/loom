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
	assert.False(t, rig.m.watchRequest(rig.ws, false, 0).loadMem)
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

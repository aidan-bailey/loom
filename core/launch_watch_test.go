package core

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeTrustScreen is Claude's trust prompt as capture-pane shows it.
const claudeTrustScreen = "Do you trust the files in this folder?\n❯ 1. Yes, proceed\n  2. No, exit"

// paneServer is a tmux whose pane shows screens[i] at the i-th capture
// (the last one from then on), and that records every key it is sent.
type paneServer struct {
	mu       sync.Mutex
	screens  []string
	dead     bool // capture-pane and has-session fail
	captures int
	keys     [][]string // each send-keys' argv, from the subcommand on
}

func (p *paneServer) runner() cmd_test.MockCmdExec {
	output := func(c *exec.Cmd) ([]byte, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch cmd_test.TmuxSubcommand(c.Args) {
		case "capture-pane":
			if p.dead {
				return nil, errors.New("can't find pane: loom_w")
			}
			screen := p.screens[min(p.captures, len(p.screens)-1)]
			p.captures++
			return []byte(screen), nil
		case "send-keys":
			p.keys = append(p.keys, c.Args[slices.Index(c.Args, "send-keys"):])
		}
		return nil, nil
	}
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.dead && cmd_test.TmuxSubcommand(c.Args) == "has-session" {
				return errors.New("can't find session: loom_w")
			}
			return nil
		},
		OutputFunc:         output,
		CombinedOutputFunc: output,
	}
}

func (p *paneServer) counts() (captures int, keys [][]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.captures, p.keys
}

// shortWatch shortens the launch watch for the test.
func shortWatch(t *testing.T, every, within time.Duration) {
	t.Helper()
	prevEvery, prevFor := launchWatchEvery, launchWatchFor
	launchWatchEvery, launchWatchFor = every, within
	t.Cleanup(func() { launchWatchEvery, launchWatchFor = prevEvery, prevFor })
}

// watchJobs takes the background jobs m queued, the launch watches among
// them, running none.
func watchJobs(m *Model) []Job {
	_, bg := m.takeJobsSplit()
	return bg
}

// Claude's MCP prompt as capture-pane shows it, after its trust prompt
// when the repository has a .mcp.json; Claude's late-2026 safety check,
// its menu's cursor on "No, exit" and then on the option that answers it;
// and aider's prompt line, which lingers after its answer.
const (
	claudeMCPScreen   = "  New MCP server found in this project: db\n  MCP servers may execute code or access system resources.\n    Use this MCP server\n    Use this and all future MCP servers in this project\n  ❯ Continue without using this MCP server\n  Enter to confirm · Esc to cancel"
	claudeMCPsScreen  = "  2 new MCP servers found in this project\n  Select any you wish to enable.\n  ❯ [✔] db\n    [✔] search\n       Enable selected\n Space to select · Esc to reject all"
	safetyCheckOnNo   = "Quick safety check: Is this a project you created or one you trust?\n\n❯ No, exit\n  Yes, I trust this folder\n\nEnter to confirm · Esc to cancel"
	safetyCheckOnYes  = "Quick safety check: Is this a project you created or one you trust?\n\n  No, exit\n❯ Yes, I trust this folder\n\nEnter to confirm · Esc to cancel"
	aiderTrustScreen  = "Open documentation url for more info? (Y)es/(N)o/(D)on't ask again [Yes]: d\n> "
	safetyCheckNoMenu = "Quick safety check: Is this a project you created or one you trust?\n\n  No, exit\n  Yes, I trust this folder"
)

// sent is the send-keys argv a paneServer records for keys to loom_w.
func sent(keys ...string) []string {
	return append([]string{"send-keys", "-t", "=loom_w:"}, keys...)
}

// runWatch runs a launch watch of loom_w running program, capturing every
// millisecond for within, over srv, after an ended watch that settled
// settled.
func runWatch(srv *paneServer, program string, within time.Duration, settled string) launchWatched {
	ts := tmux.NewSessionNamedWithDeps("loom_w", program, tmux.MakePtyFactory(), srv.runner())
	return launchWatchJob(ts, time.Millisecond, within, settled)().(launchWatched)
}

func TestLaunchWatch_AnswersATrustPromptOnce(t *testing.T) {
	srv := &paneServer{screens: []string{"", "Claude Code starting…", claudeTrustScreen}}

	result := runWatch(srv, "claude", 100*time.Millisecond, "")

	assert.Equal(t, launchWatched{session: "loom_w", answered: true, settled: "Do you trust the files in this folder?"}, result)
	captures, keys := srv.counts()
	assert.Greater(t, captures, 3, "it watches on to its deadline")
	assert.Equal(t, [][]string{sent("Enter")}, keys, "one answer while the prompt stays on screen")
}

// One prompt can follow another: Claude shows its MCP prompt after its
// trust prompt. Each is answered once, but an MCP server dialog is left to
// the person; a prompt that lingers on screen after its answer is not
// answered again.
func TestLaunchWatch_AnswersEachPromptItShows(t *testing.T) {
	t.Run("the trust prompt, then the MCP prompt", func(t *testing.T) {
		srv := &paneServer{screens: []string{claudeTrustScreen, claudeTrustScreen, "", claudeMCPScreen}}

		result := runWatch(srv, "claude", 100*time.Millisecond, "")

		assert.True(t, result.answered)
		_, keys := srv.counts()
		assert.Equal(t, [][]string{sent("Enter")}, keys, "the trust prompt's answer alone")
		assert.Equal(t, "New MCP server found in this project", result.settled, "the MCP prompt left settled")
	})

	t.Run("one straight after the other", func(t *testing.T) {
		srv := &paneServer{screens: []string{claudeTrustScreen, claudeMCPScreen}}

		runWatch(srv, "claude", 100*time.Millisecond, "")

		_, keys := srv.counts()
		assert.Equal(t, [][]string{sent("Enter")}, keys)
	})

	t.Run("aider's lingering prompt line", func(t *testing.T) {
		srv := &paneServer{screens: []string{aiderTrustScreen}}

		result := runWatch(srv, "aider", 100*time.Millisecond, "")

		_, keys := srv.counts()
		assert.Equal(t, [][]string{sent("D", "Enter")}, keys, "answered once")
		assert.Equal(t, "Open documentation url for more info", result.settled)
	})

	t.Run("a watch after one that settled it", func(t *testing.T) {
		srv := &paneServer{screens: []string{aiderTrustScreen, "", aiderTrustScreen}}

		runWatch(srv, "aider", 100*time.Millisecond, "Open documentation url for more info")

		_, keys := srv.counts()
		assert.Equal(t, [][]string{sent("D", "Enter")}, keys, "not while it lingers, only once it showed again")
	})
}

// An MCP server dialog, for one server or several, is the person's to
// answer: approving one starts a process the repository's .mcp.json names.
// The watch sends it nothing, and leaves it settled.
func TestLaunchWatch_LeavesMCPServerDialogsToThePerson(t *testing.T) {
	for _, tc := range []struct{ name, screen, pattern string }{
		{"one server", claudeMCPScreen, "New MCP server found in this project"},
		{"several servers", claudeMCPsScreen, "new MCP server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &paneServer{screens: []string{"", tc.screen}}

			result := runWatch(srv, "claude", 50*time.Millisecond, "")

			_, keys := srv.counts()
			assert.Empty(t, keys, "no key sent")
			assert.Equal(t, launchWatched{session: "loom_w", settled: tc.pattern}, result, "seen, and left")
		})
	}
}

// Claude's safety check selects "No, exit" first, which Enter would
// confirm: the watch moves the menu down until a capture shows its cursor
// on "Yes, I trust this folder", waiting for each move to be drawn, and
// only then presses Enter. A menu that never gets there is left to the
// user, as is one with no cursor to read.
func TestLaunchWatch_AnswersTheSafetyCheckOnlyOnItsOption(t *testing.T) {
	t.Run("moved, then confirmed", func(t *testing.T) {
		srv := &paneServer{screens: []string{safetyCheckOnNo, safetyCheckOnNo, safetyCheckOnNo, safetyCheckOnYes, ""}}

		result := runWatch(srv, "claude", 100*time.Millisecond, "")

		assert.True(t, result.answered)
		_, keys := srv.counts()
		assert.Equal(t, [][]string{sent("Down"), sent("Enter")}, keys, "one move, waited out until drawn")
	})

	t.Run("already on its option", func(t *testing.T) {
		srv := &paneServer{screens: []string{safetyCheckOnYes, ""}}

		runWatch(srv, "claude", 100*time.Millisecond, "")

		_, keys := srv.counts()
		assert.Equal(t, [][]string{sent("Enter")}, keys)
	})

	t.Run("a menu that never moves", func(t *testing.T) {
		srv := &paneServer{screens: []string{safetyCheckOnNo}}

		result := runWatch(srv, "claude", 100*time.Millisecond, "")

		assert.False(t, result.answered)
		assert.Equal(t, "Quick safety check", result.settled, "left to the user")
		_, keys := srv.counts()
		assert.Equal(t, [][]string{sent("Down")}, keys, "no Enter")
	})

	t.Run("a menu that never reaches its option", func(t *testing.T) {
		other := strings.Replace(safetyCheckOnNo, "❯ No, exit", "  No, exit\n❯ Maybe later", 1)
		srv := &paneServer{screens: []string{safetyCheckOnNo, other, safetyCheckOnNo, other, safetyCheckOnNo}}

		result := runWatch(srv, "claude", 100*time.Millisecond, "")

		assert.False(t, result.answered)
		_, keys := srv.counts()
		assert.Equal(t, [][]string{sent("Down"), sent("Down"), sent("Down"), sent("Down")}, keys, "no Enter")
	})

	t.Run("no cursor to read", func(t *testing.T) {
		srv := &paneServer{screens: []string{safetyCheckNoMenu}}

		result := runWatch(srv, "claude", 100*time.Millisecond, "")

		assert.False(t, result.answered)
		_, keys := srv.counts()
		assert.Empty(t, keys)
	})
}

func TestLaunchWatch_GivesUpAtItsDeadline(t *testing.T) {
	shortWatch(t, time.Millisecond, 50*time.Millisecond)
	srv := &paneServer{screens: []string{"> "}}
	m := NewForTest(Options{CmdExec: srv.runner()})
	inst := startedInst(t, "w", "claude")
	hold(m, inst)

	m.watchLaunch(inst)
	jobs := watchJobs(m)
	require.Len(t, jobs, 1)
	start := time.Now()
	result := jobs[0]()

	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Equal(t, launchWatched{session: "loom_w"}, result)
	captures, keys := srv.counts()
	assert.Greater(t, captures, 1, "it kept looking until the deadline")
	assert.Empty(t, keys, "no prompt, no answer")

	m.Deliver(result)
	assert.Empty(t, m.watching, "the delivery ends the watch")
	assert.Empty(t, watchJobs(m))
}

func TestLaunchWatch_EndsWhenTheSessionDies(t *testing.T) {
	srv := &paneServer{dead: true}
	ts := tmux.NewSessionNamedWithDeps("loom_w", "claude", tmux.MakePtyFactory(), srv.runner())

	result := launchWatchJob(ts, time.Hour, time.Hour, "")()

	assert.Equal(t, launchWatched{session: "loom_w"}, result, "returned at once, not after an hour")
}

// Two launches of one session start one watch; the second, made while the
// first watch runs, is watched afresh when the first ends.
func TestLaunchWatch_OneWatchPerSession(t *testing.T) {
	m := NewForTest(Options{CmdExec: aliveExec()})
	inst := startedInst(t, "w", "claude")
	hold(m, inst)

	m.watchLaunch(inst)
	m.watchLaunch(inst)

	require.Len(t, watchJobs(m), 1, "one watch per session")
	require.Contains(t, m.watching, "loom_w")

	m.Deliver(launchWatched{session: "loom_w"})
	assert.Len(t, watchJobs(m), 1, "the relaunch is watched afresh")
	assert.Contains(t, m.watching, "loom_w")

	m.Deliver(launchWatched{session: "loom_w"})
	assert.Empty(t, watchJobs(m))
	assert.Empty(t, m.watching)
}

// The watch of a relaunch leaves alone the prompt the ended watch settled
// while it lingers on screen: aider's answered prompt line would get a
// second "D" typed into its input.
func TestLaunchWatch_TheRelaunchsWatchLeavesTheSettledPrompt(t *testing.T) {
	shortWatch(t, time.Millisecond, 50*time.Millisecond)
	srv := &paneServer{screens: []string{aiderTrustScreen}}
	m := NewForTest(Options{CmdExec: srv.runner()})
	inst := startedInst(t, "w", "aider")
	hold(m, inst)
	m.watchLaunch(inst)
	m.watchLaunch(inst)
	require.Len(t, watchJobs(m), 1)

	m.Deliver(launchWatched{session: "loom_w", answered: true, settled: "Open documentation url for more info"})
	jobs := watchJobs(m)
	require.Len(t, jobs, 1, "the relaunch is watched")
	jobs[0]()

	_, keys := srv.counts()
	assert.Empty(t, keys, "the lingering line is not answered again")
}

func TestLaunchWatch_AnAgentWithNoTrustPromptIsNotWatched(t *testing.T) {
	m := NewForTest(Options{CmdExec: aliveExec()})
	inst := startedInst(t, "sh", "bash")
	hold(m, inst)

	m.watchLaunch(inst)

	assert.Empty(t, watchJobs(m))
	assert.Empty(t, m.watching)
}

// Every real launch is watched: a start, a resume that relaunched, a
// crash restart, a workspace terminal's relaunch (at a first open or by
// the health tick) and its creation. A failed start and a resume that
// reattached launched nothing.
func TestLaunchWatch_EveryLaunchIsWatched(t *testing.T) {
	watched := func(m *Model, inst *session.Instance) bool {
		_, ok := m.watching[inst.Pane().TmuxSessionName()]
		return ok
	}

	t.Run("a start", func(t *testing.T) {
		m := NewForTest(Options{CmdExec: aliveExec()})
		ws := storedWorkspace(t, "a")
		inst := startedInst(t, "s", "claude")
		ws.add(inst)
		m.SetWorkspacesForTest(ws)

		m.Deliver(StartResult{Instance: inst, Owner: ws})

		assert.True(t, watched(m, inst))
	})

	t.Run("a failed start", func(t *testing.T) {
		m := NewForTest(Options{CmdExec: aliveExec()})
		ws := storedWorkspace(t, "a")
		inst := startedInst(t, "s", "claude")
		ws.add(inst)
		m.SetWorkspacesForTest(ws)

		m.Deliver(StartResult{Instance: inst, Owner: ws, Err: errors.New("boom")})

		assert.False(t, watched(m, inst))
	})

	for _, relaunched := range []bool{true, false} {
		name := map[bool]string{true: "a resume that relaunched", false: "a resume that reattached"}[relaunched]
		t.Run(name, func(t *testing.T) {
			m := NewForTest(Options{CmdExec: aliveExec()})
			ws := storedWorkspace(t, "a")
			inst := startedInst(t, "r", "claude")
			ws.add(inst)
			m.SetWorkspacesForTest(ws)

			m.Deliver(ResumeResult{Instance: inst, Owner: ws, Relaunched: relaunched})

			assert.Equal(t, relaunched, watched(m, inst))
		})
	}

	for _, crashed := range []bool{true, false} {
		name := map[bool]string{true: "a crash restart", false: "a paused terminal relaunched at a first open"}[crashed]
		t.Run(name, func(t *testing.T) {
			f := newUnsettledTerminal(t, "watch-"+map[bool]string{true: "crashed", false: "paused"}[crashed], crashed)
			*f.starved = false

			f.m.ensureTerminal(f.ws)

			require.Equal(t, session.Running, f.term.GetStatus(), "fixture: it relaunched")
			assert.True(t, watched(f.m, f.term))
		})
	}

	t.Run("a workspace terminal relaunched by the health tick", func(t *testing.T) {
		m := NewForTest(Options{})
		inst, err := session.NewInstance(session.InstanceOptions{
			Title: "ws-restart", Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true,
		})
		require.NoError(t, err)
		hold(m, inst)
		running := false
		cmdExec := cmd_test.MockCmdExec{
			RunFunc: func(cmd *exec.Cmd) error {
				switch s := cmd.String(); {
				case strings.Contains(s, "has-session") && !running:
					return errors.New("no session")
				case strings.Contains(s, "new-session"):
					running = true
				}
				return nil
			},
			OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
		}
		inst.SetTmuxSessionForTest(tmux.NewSessionWithDeps("ws-restart", "claude", runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec))
		require.NoError(t, inst.EnsureRunning())

		m.Deliver(HealthResult{Results: []ProbeResult{{Instance: inst, TmuxLive: tmux.LivenessDead}}})

		require.Contains(t, m.Drain().Events, Event(SessionLaunched{ID: m.idOf(inst)}), "fixture: it relaunched")
		assert.True(t, watched(m, inst))
	})

	t.Run("a workspace terminal created at a first open", func(t *testing.T) {
		isolateTmux(t)
		ws := storedWorkspace(t, "created")
		ws.ctx.RepoPath = t.TempDir()
		ws.cfg.DefaultProgram = fakeClaude(t)
		killSessionAtEnd(t, ws.terminalTitle())
		m := NewForTest(Options{CmdExec: noTmuxExec()})
		m.SetWorkspacesForTest(ws)

		m.ensureTerminal(ws)

		term := ws.terminal()
		require.NotNil(t, term)
		require.Equal(t, session.Running, term.GetStatus(), "fixture: it started")
		assert.True(t, watched(m, term))
	})
}

// HookTick scans only while an active Claude instance exists: with none it
// queues nothing and arms no gate, which is what lets it run four times a
// second.
func TestHookTick_ScansOnlyWithAnActiveClaudeInstance(t *testing.T) {
	t.Run("no Claude instance", func(t *testing.T) {
		m := NewForTest(Options{})
		hold(m, startedInst(t, "a", "aider"))

		m.HookTick()

		assert.Empty(t, m.Drain().Jobs)
		assert.False(t, m.gate(gateHookScan).inFlight, "no job, nothing armed")
	})

	t.Run("a Claude instance", func(t *testing.T) {
		m := NewForTest(Options{})
		hold(m, startedInst(t, "a", "aider"), startedInst(t, "c", "claude"))

		m.HookTick()

		jobs := m.Drain().Jobs
		require.Len(t, jobs, 1)
		assert.True(t, m.gate(gateHookScan).inFlight)
		r, ok := jobs[0]().(gatedResult)
		require.True(t, ok)
		assert.IsType(t, hookScanResults{}, r.result)

		m.HookTick()
		assert.Empty(t, m.Drain().Jobs, "never two scans at once")
	})

	t.Run("a paused Claude instance", func(t *testing.T) {
		m := NewForTest(Options{})
		hold(m, pausedInst(t, "p"))

		m.HookTick()

		assert.Empty(t, m.Drain().Jobs)
	})
}

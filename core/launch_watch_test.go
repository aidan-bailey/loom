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

func TestLaunchWatch_AnswersATrustPromptOnce(t *testing.T) {
	srv := &paneServer{screens: []string{"", "Claude Code starting…", claudeTrustScreen}}
	ts := tmux.NewSessionNamedWithDeps("loom_w", "claude", tmux.MakePtyFactory(), srv.runner())

	result := launchWatchJob(ts, time.Millisecond, 5*time.Second)()

	assert.Equal(t, launchWatched{session: "loom_w", answered: true}, result)
	captures, keys := srv.counts()
	assert.Equal(t, 3, captures, "it stops capturing once it answered")
	assert.Equal(t, [][]string{{"send-keys", "-t", "=loom_w:", "Enter"}}, keys, "one answer")
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

	result := launchWatchJob(ts, time.Hour, time.Hour)()

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
		inst.SetTmuxSession(tmux.NewSessionWithDeps("ws-restart", "claude", runningPtyFactory{t: t, cmdExec: cmdExec}, cmdExec))
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

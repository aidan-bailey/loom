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
// settled. It waits no time before a key (no settle): srv's screens change
// per capture, not per key.
func runWatch(srv *paneServer, program string, within time.Duration, settled string) launchWatched {
	ts := tmux.NewSessionNamedWithDeps("loom_w", program, tmux.MakePtyFactory(), srv.runner())
	return launchWatchJob(ts, time.Millisecond, within, 0, settled)().(launchWatched)
}

// menuPane is Claude's safety check as a tmux pane: a menu of options whose
// cursor starts on the first and moves down on Down, wrapping, and that
// closes on Enter. Like Claude's, it refuses every key for refuseFor after
// it first shows; a frozen one never moves. A key it takes shows its effect
// only lag later, in order, as a loaded Claude draws late. It records each
// key, when it came (since the menu showed) and what Enter confirmed.
type menuPane struct {
	mu        sync.Mutex
	options   []string
	cursor    int
	refuseFor time.Duration
	frozen    bool
	lag       time.Duration
	shown     time.Time
	taken     []takenKey
	keys      []string
	at        []time.Duration
	confirmed string
}

// takenKey is a key the menu took, whose effect shows at due.
type takenKey struct {
	key string
	due time.Time
}

// draw applies the effects of the keys taken that are due by now, in order.
func (p *menuPane) draw(now time.Time) {
	for len(p.taken) > 0 && !p.taken[0].due.After(now) {
		switch p.taken[0].key {
		case "Down":
			if p.confirmed == "" {
				p.cursor = (p.cursor + 1) % len(p.options)
			}
		case "Enter":
			if p.confirmed == "" {
				p.confirmed = p.options[p.cursor]
			}
		}
		p.taken = p.taken[1:]
	}
}

func (p *menuPane) screen() string {
	if p.confirmed != "" {
		return "❯ \n? for shortcuts"
	}
	var b strings.Builder
	b.WriteString("Quick safety check: Is this a project you created or one you trust?\n\n")
	for i, o := range p.options {
		if i == p.cursor {
			b.WriteString("❯ " + o + "\n")
		} else {
			b.WriteString("  " + o + "\n")
		}
	}
	b.WriteString("\nEnter to confirm · Esc to cancel")
	return b.String()
}

func (p *menuPane) runner() cmd_test.MockCmdExec {
	output := func(c *exec.Cmd) ([]byte, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch cmd_test.TmuxSubcommand(c.Args) {
		case "capture-pane":
			if p.shown.IsZero() {
				p.shown = time.Now()
			}
			p.draw(time.Now())
			return []byte(p.screen()), nil
		case "send-keys":
			key, since := c.Args[len(c.Args)-1], time.Since(p.shown)
			p.keys, p.at = append(p.keys, key), append(p.at, since)
			if p.confirmed != "" || p.frozen || since < p.refuseFor {
				break
			}
			p.taken = append(p.taken, takenKey{key: key, due: time.Now().Add(p.lag)})
			p.draw(time.Now())
		}
		return nil, nil
	}
	return cmd_test.MockCmdExec{RunFunc: func(*exec.Cmd) error { return nil }, OutputFunc: output, CombinedOutputFunc: output}
}

func (p *menuPane) seen() (keys []string, at []time.Duration, confirmed string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.draw(time.Now().Add(time.Hour)) // whatever was taken lands
	return append([]string(nil), p.keys...), append([]time.Duration(nil), p.at...), p.confirmed
}

// runMenuWatch runs a launch watch of Claude over menu, capturing every
// millisecond for within, waiting settle before a key.
func runMenuWatch(menu *menuPane, within, settle time.Duration) launchWatched {
	ts := tmux.NewSessionNamedWithDeps("loom_w", "claude", tmux.MakePtyFactory(), menu.runner())
	return launchWatchJob(ts, time.Millisecond, within, settle, "")().(launchWatched)
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
// on "Yes, I trust this folder", and only then presses Enter. Claude refuses
// every key for a moment after a dialog opens, so no key goes before the
// settle has passed since the watch first saw it, and a Down the menu did
// not draw is pressed again after another settle. A menu that never gets
// there is left to the user, as is one with no cursor to read.
func TestLaunchWatch_AnswersTheSafetyCheckOnlyOnItsOption(t *testing.T) {
	safety := func() []string { return []string{"No, exit", "Yes, I trust this folder"} }
	t.Run("moved, then confirmed", func(t *testing.T) {
		menu := &menuPane{options: safety()}

		result := runMenuWatch(menu, time.Second, 5*time.Millisecond)

		assert.True(t, result.answered)
		keys, _, confirmed := menu.seen()
		assert.Equal(t, []string{"Down", "Enter"}, keys, "one move, drawn, then confirmed")
		assert.Equal(t, "Yes, I trust this folder", confirmed)
	})

	t.Run("a menu that refuses keys at first, then moves", func(t *testing.T) {
		menu := &menuPane{options: safety(), refuseFor: 60 * time.Millisecond}

		result := runMenuWatch(menu, 2*time.Second, 20*time.Millisecond)

		assert.True(t, result.answered, "answered once the menu took its keys")
		keys, at, confirmed := menu.seen()
		assert.Equal(t, "Yes, I trust this folder", confirmed)
		require.GreaterOrEqual(t, len(keys), 3)
		assert.Less(t, at[0], menu.refuseFor, "fixture: the first Down came while it refused keys")
		assert.Equal(t, "Enter", keys[len(keys)-1])
		for _, k := range keys[:len(keys)-1] {
			assert.Equal(t, "Down", k, "a refused Down is pressed again, never an Enter on a guess")
		}
	})

	t.Run("no key before the settle", func(t *testing.T) {
		menu := &menuPane{options: []string{"Yes, I trust this folder", "No, exit"}}

		result := runMenuWatch(menu, time.Second, 50*time.Millisecond)

		assert.True(t, result.answered)
		keys, at, _ := menu.seen()
		assert.Equal(t, []string{"Enter"}, keys, "already on its option: Enter alone")
		assert.GreaterOrEqual(t, at[0], 50*time.Millisecond, "even an Enter waits out the settle")
	})

	t.Run("a menu that never moves", func(t *testing.T) {
		menu := &menuPane{options: safety(), frozen: true}

		result := runMenuWatch(menu, 100*time.Millisecond, 10*time.Millisecond)

		assert.False(t, result.answered)
		assert.Equal(t, "Quick safety check", result.settled, "left to the user when the watch ends")
		keys, at, _ := menu.seen()
		assert.Greater(t, len(keys), 1, "Down pressed again")
		for i := 1; i < len(at); i++ {
			// Half the settle: the menu stamps a key as it lands, the watch
			// times it from the capture before, so a gap can fall a little
			// short; a Down pressed again at once lands within a capture.
			assert.GreaterOrEqual(t, at[i]-at[i-1], 5*time.Millisecond, "each after the settle")
		}
		assert.NotContains(t, keys, "Enter")
	})

	t.Run("a menu that never reaches its option", func(t *testing.T) {
		menu := &menuPane{options: []string{"No, exit", "Maybe later"}}

		result := runMenuWatch(menu, time.Second, 5*time.Millisecond)

		assert.False(t, result.answered)
		assert.Equal(t, "Quick safety check", result.settled)
		keys, _, _ := menu.seen()
		assert.Equal(t, []string{"Down", "Down", "Down", "Down"}, keys, "no Enter")
	})

	t.Run("a Down drawn late never ends in No", func(t *testing.T) {
		// A loaded Claude takes a Down at once but draws it late. A Down
		// pressed again meanwhile lands after it, and an Enter sent on the
		// first one's draw would land after both: on "No, exit", wrapped.
		for _, lag := range []time.Duration{10 * time.Millisecond, 30 * time.Millisecond, 70 * time.Millisecond} {
			menu := &menuPane{options: safety(), lag: lag}

			result := runMenuWatch(menu, 3*time.Second, 20*time.Millisecond)

			keys, _, confirmed := menu.seen()
			assert.Equal(t, "Yes, I trust this folder", confirmed, "lag %v: keys %v", lag, keys)
			assert.True(t, result.answered, "lag %v", lag)
		}
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

	result := launchWatchJob(ts, time.Hour, time.Hour, 0, "")()

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

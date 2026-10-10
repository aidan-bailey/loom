package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The link tests run a TUI over a daemon in this process: a booted model
// on a loop that holds its jobs (core.StartForTest), served over a pipe to
// a production client (rpc.Dial), as the TUI dials the daemon. Those
// clients are not synchronous, so a test waits for the client's state
// (require.Eventually on Stopping or Err) and then hands the TUI its wake
// (coreWakeMsg). Tests never run a tea.Tick Cmd: they send rejoinTickMsg
// themselves and run the attempt's Cmd on their own goroutine.

// daemonStack is a daemon in this process and its client.
type daemonStack struct {
	loop *core.Loop
	srv  *rpc.Server
	c    *rpc.Client
}

// bootDaemon boots a model over the test's LOOM_GLOBAL_DIR and the
// registry there, as the daemon does, serves it, and dials it. It is
// stopped when the test ends.
func bootDaemon(t *testing.T, exec cmd2.Executor) (daemonStack, []core.Event) {
	t.Helper()
	return bootDaemonOn(t, exec, "")
}

// bootDaemonOn is bootDaemon for a daemon whose hello names tmuxServer as
// the tmux server its sessions run on.
func bootDaemonOn(t *testing.T, exec cmd2.Executor, tmuxServer string) (daemonStack, []core.Event) {
	t.Helper()
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	model := core.NewForTest(core.Options{Registry: reg, Program: "true", CmdExec: exec})
	notices := model.Boot()
	loop := core.StartForTest(model)
	srv := rpc.NewServer(loop)
	srv.SetTmux(tmuxServer)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := rpc.Dial(b)
	require.NoError(t, err)
	t.Cleanup(func() {
		c.Close()
		srv.Close()
		loop.Stop()
	})
	return daemonStack{loop: loop, srv: srv, c: c}, notices
}

// linkedHome is a TUI as Run builds it (startHome) over a daemon booted
// from the test's disk, sized 120×40, showing startupName ("" the global
// workspace), or the registry's saved tabs. Its rejoin is unset: offline,
// it stays offline until the test sets one.
func linkedHome(t *testing.T, startupName string) (*home, daemonStack) {
	t.Helper()
	st, notices := bootDaemon(t, &recordingExec{})
	m, err := startHome(context.Background(), st.c, st.c.Close, notices, startupName, "true", "", true)
	require.NoError(t, err)
	m.ctx = cancelledCtx() // the error bar's hide timer returns at once
	m.errBox.SetSize(400, 1)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m, st
}

// pausedRecord is a stored session, Paused, created at created: a row
// whose ID every daemon over the disk agrees on.
func pausedRecord(title, created string) string {
	return fmt.Sprintf(`{"title":%q,"status":3,"program":"true","created_at":%q,"worktree":{"worktree_path":"/tmp/wt-%s"}}`,
		title, created, title)
}

// globalDisk points LOOM_GLOBAL_DIR at a fresh directory whose global
// workspace holds records, and returns it.
func globalDisk(t *testing.T, records ...string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.StateFileName),
		[]byte(`{"instances":[`+strings.Join(records, ",")+`]}`), 0o644))
	return dir
}

// twoRows is a global disk with two paused sessions, alpha and beta.
func twoRows(t *testing.T) {
	t.Helper()
	globalDisk(t, pausedRecord("alpha", "2026-10-01T10:00:00Z"), pausedRecord("beta", "2026-10-01T11:00:00Z"))
}

// wake hands m the client's wake once cond holds, as forwardWakes would,
// and returns the Update's Cmd.
func wake(t *testing.T, m *home, cond func() bool) tea.Cmd {
	t.Helper()
	require.Eventually(t, cond, 5*time.Second, 5*time.Millisecond)
	_, cmd := m.Update(coreWakeMsg{})
	return cmd
}

// bye has the daemon say bye, and m take it.
func bye(t *testing.T, m *home, st daemonStack) tea.Cmd {
	t.Helper()
	st.srv.Bye()
	return wake(t, m, st.c.Stopping)
}

// crash closes the daemon's end with no bye, and has m take the loss.
func crash(t *testing.T, m *home, st daemonStack) tea.Cmd {
	t.Helper()
	st.srv.Close()
	return wake(t, m, func() bool { return st.c.Err() != nil })
}

// stop has the daemon say bye and close, and m take the graceful loss.
func stop(t *testing.T, m *home, st daemonStack) tea.Cmd {
	t.Helper()
	bye(t, m, st)
	st.srv.Close()
	return wake(t, m, func() bool { return st.c.Err() != nil })
}

// fakeRejoin is a Rejoin whose answers a test scripts; it records the spawn
// flag of every call.
type fakeRejoin struct {
	spawns []bool
	answer func(n int) (*rpc.Client, error)
}

func (f *fakeRejoin) rejoin(spawn bool, say func(string)) (*rpc.Client, error) {
	f.spawns = append(f.spawns, spawn)
	return f.answer(len(f.spawns))
}

// failing is a fakeRejoin that always fails with err.
func failing(err error) *fakeRejoin {
	return &fakeRejoin{answer: func(int) (*rpc.Client, error) { return nil, err }}
}

// tick is the rejoin timer's turn, as tea.Tick would deliver it, and runs
// the attempt it starts on this goroutine; it returns the attempt's result
// (nil when the tick started none).
func tick(t *testing.T, m *home) *rejoinedMsg {
	t.Helper()
	cmd := m.rejoinTick(rejoinTickMsg{gen: m.link.gen})
	if cmd == nil {
		return nil
	}
	msg, ok := cmd().(rejoinedMsg)
	require.True(t, ok)
	return &msg
}

// press sends key through Update as the runtime does, the menu
// highlighting's replay included, and returns the Cmd of the press that
// reached the key's handler.
func press(m *home, key tea.KeyPressMsg) tea.Cmd {
	_, cmd := m.Update(key)
	if m.keySent {
		_, cmd = m.Update(key)
	}
	return cmd
}

// pressScript presses a key bound in the script engine and applies what
// its script did (scriptDoneMsg), as the runtime would.
func pressScript(t *testing.T, m *home, key tea.KeyPressMsg) {
	t.Helper()
	for _, msg := range runCmds(t, press(m, key)) {
		if done, ok := msg.(scriptDoneMsg); ok {
			m.Update(done)
		}
	}
}

// viewText is m's screen, unstyled.
func viewText(m *home) string { return ansi.Strip(m.View().Content) }

// rowID is the ID of the focused list's row titled title.
func rowID(t *testing.T, m *home, title string) core.InstanceID {
	t.Helper()
	id := titleID(m.list, title)
	require.NotZero(t, id, "no row %q", title)
	return id
}

func TestRejoinBackoff(t *testing.T) {
	for n, want := range map[int]time.Duration{
		0: time.Second, 1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second,
		5: 16 * time.Second, 6: 30 * time.Second, 7: 30 * time.Second, 50: 30 * time.Second,
	} {
		assert.Equal(t, want, rejoinBackoff(n), "attempt %d", n)
	}
}

// TestLink_ByeEntersStopping: a bye takes the TUI to stopping: the banner
// shows (taking a row from the content), keys that stay in the TUI still
// work, and one that needs the model says so and sends nothing.
func TestLink_ByeEntersStopping(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, st := linkedHome(t, "")
	alpha, beta := rowID(t, m, "alpha"), rowID(t, m, "beta")
	m.list.SelectID(alpha)
	m.Update(coreWakeMsg{})
	require.Eventually(t, func() bool {
		sel := st.loop.SelectedForTest()
		return len(sel) == 1 && sel[0] == alpha
	}, 5*time.Second, 5*time.Millisecond)

	bye(t, m, st)
	assert.Equal(t, linkStopping, m.link.state)
	lines := strings.Split(viewText(m), "\n")
	assert.True(t, strings.HasPrefix(lines[0], "⚠ the loom daemon is stopping: finishing its jobs in flight"), "first row: %q", lines[0])
	assert.Len(t, lines, 40, "the banner's row is taken out of the content height")

	pressScript(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.Equal(t, beta, selID(m.list), "j still moves the selection")

	cmd := press(m, tea.KeyPressMsg{Code: 'D', Text: "D"})
	assert.Nil(t, cmd)
	assert.Contains(t, m.errBox.String(), "the loom daemon is stopping: D needs it")
	assert.Equal(t, stateDefault, m.state, "no confirmation opened")
	assert.Empty(t, m.pending, "no request made")
	assert.Empty(t, st.loop.JobsForTest(), "nothing reached the model")
	assert.Never(t, func() bool {
		sel := st.loop.SelectedForTest()
		return len(sel) != 1 || sel[0] != alpha
	}, 200*time.Millisecond, 10*time.Millisecond, "the model's selection is as it was at the bye")
}

// TestLink_StrandedRequestsFail: every request nothing will answer gets a
// Reply that it is unavailable, through the flow's own Reply handling: at
// the bye only those made after it (the daemon still answers the ones in
// flight), at the loss the rest. One subtest per kind of request.
func TestLink_StrandedRequestsFail(t *testing.T) {
	type kind struct {
		name string
		// make records a request of the kind for the session id titled
		// title, as its flow does, and returns its ID.
		make func(m *home, id core.InstanceID, title string) core.ReqID
		// failed checks the request made for title failed, given what the
		// Update's Cmds produced.
		failed func(t *testing.T, m *home, id core.InstanceID, title string, msgs []tea.Msg)
	}
	errShows := func(text string) func(*testing.T, *home, core.InstanceID, string, []tea.Msg) {
		return func(t *testing.T, m *home, _ core.InstanceID, title string, _ []tea.Msg) {
			assert.Contains(t, m.errBox.String(), fmt.Sprintf(text, title))
		}
	}
	kinds := []kind{
		{
			name:   "a lifecycle request",
			make:   func(m *home, _ core.InstanceID, title string) core.ReqID { return m.opReq("kill", title) },
			failed: errShows("kill %s: the loom daemon is unavailable"),
		},
		{
			name: "a prompt send",
			make: func(m *home, id core.InstanceID, title string) core.ReqID {
				m.holdInput(id)
				return m.newReq(pendingReq{send: &pendingSend{id: id, title: title}})
			},
			failed: func(t *testing.T, m *home, id core.InstanceID, title string, _ []tea.Msg) {
				assert.False(t, m.sending[id], "the send's hold is released")
				assert.Contains(t, m.errBox.String(), "prompt not sent to "+title+": the loom daemon is unavailable")
			},
		},
		{
			name: "a Lua call",
			make: func(m *home, id core.InstanceID, _ string) core.ReqID {
				return m.newReq(pendingReq{script: &pendingScript{intent: 40, trace: fmt.Sprint(id), op: "kill"}})
			},
			failed: func(t *testing.T, _ *home, id core.InstanceID, _ string, msgs []tea.Msg) {
				var resumed []scriptResumeMsg
				for _, msg := range msgs {
					if r, ok := msg.(scriptResumeMsg); ok && r.trace == fmt.Sprint(id) {
						resumed = append(resumed, r)
					}
				}
				require.Len(t, resumed, 1, "the coroutine resumes")
				assert.Equal(t, "kill: the loom daemon is unavailable", resumed[0].value.Err, "raising an error naming the daemon")
			},
		},
		{
			name: "a create",
			make: func(m *home, _ core.InstanceID, title string) core.ReqID {
				return m.newReq(pendingReq{create: &pendingCreate{slot: m.workspaceSlot, title: title + "-new"}})
			},
			failed: errShows("create %s-new: the loom daemon is unavailable"),
		},
		{
			name: "an issue fetch",
			make: func(m *home, _ core.InstanceID, _ string) core.ReqID {
				return m.newReq(pendingReq{issue: &pendingIssue{picked: &issuePickedMsg{repo: m.repoPath()}}})
			},
			failed: func(t *testing.T, m *home, _ core.InstanceID, _ string, _ []tea.Msg) {
				assert.Contains(t, m.errBox.String(), "fetch issue: the loom daemon is unavailable")
			},
		},
	}
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			isolateTmux(t)
			twoRows(t)
			m, st := linkedHome(t, "")
			alpha, beta := rowID(t, m, "alpha"), rowID(t, m, "beta")

			inFlight := k.make(m, alpha, "alpha")
			bye(t, m, st)
			after := k.make(m, beta, "beta")
			_, cmd := m.Update(coreWakeMsg{})
			k.failed(t, m, beta, "beta", runCmds(t, cmd))
			assert.NotContains(t, m.pending, after)
			assert.Contains(t, m.pending, inFlight, "a request in flight at the bye is still answered")

			st.srv.Close()
			cmd = wake(t, m, func() bool { return st.c.Err() != nil })
			k.failed(t, m, alpha, "alpha", runCmds(t, cmd))
			assert.Empty(t, m.pending)
		})
	}
}

// TestLink_ARequestRefusedAroundTheByeFailsAtOnce: a request made before
// the TUI saw the bye (numbered at or below its watermark) but refused as
// unavailable, by the client that heard the bye or by the server it
// crossed, never reached the model: it fails as soon as the TUI sees the
// bye, not once the daemon has finished stopping.
func TestLink_ARequestRefusedAroundTheByeFailsAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before func(t *testing.T, st daemonStack)
	}{
		{"refused by the client", func(t *testing.T, st daemonStack) {
			st.srv.Bye()
			require.Eventually(t, st.c.Stopping, 5*time.Second, 5*time.Millisecond)
		}},
		{"crossing the bye", func(t *testing.T, st daemonStack) { st.srv.Bye() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateTmux(t)
			twoRows(t)
			m, st := linkedHome(t, "")
			alpha := rowID(t, m, "alpha")
			tc.before(t, st)
			req := m.opReq("kill", "alpha")
			m.core.Kill(alpha, req)

			cmd := wake(t, m, st.c.Stopping)
			runCmds(t, cmd)
			require.Equal(t, linkStopping, m.link.state)
			assert.LessOrEqual(t, req, m.link.byeReq, "made before the TUI saw the bye")
			assert.NotContains(t, m.pending, req)
			assert.Contains(t, m.errBox.String(), "kill alpha: the loom daemon is unavailable")
			assert.Empty(t, st.loop.JobsForTest(), "the model never saw it")
		})
	}
}

// TestLink_ARepliedRequestIsNotStranded: a Reply that arrived before the
// connection closed is applied, not failed: Update checks the link after
// its drain, and the loss drains once more.
func TestLink_ARepliedRequestIsNotStranded(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, st := linkedHome(t, "")
	// A kill the model refuses at once (no session has the ID): its Reply
	// comes before the connection closes.
	req := m.opReq("kill", "ghost")
	m.core.Kill(core.InstanceID(12345), req)
	st.srv.Close()
	cmd := wake(t, m, func() bool { return st.c.Err() != nil })
	runCmds(t, cmd)
	assert.Contains(t, m.errBox.String(), "kill ghost: no such session", "the model's own refusal, not unavailable")
}

// TestLink_GracefulLossWaitsAndNeverSpawns: a loss after a bye is a stop:
// the TUI waits for a daemon, polling without ever starting one.
func TestLink_GracefulLossWaitsAndNeverSpawns(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, st := linkedHome(t, "")
	f := failing(daemon.ErrNoDaemon)
	m.rejoin = f.rejoin

	stop(t, m, st)
	require.Equal(t, linkWaiting, m.link.state)
	assert.Equal(t, waitPoll, m.nextRejoinDelay())
	assert.Contains(t, viewText(m), "⚠ the loom daemon stopped: waiting for one to start (ctrl+r starts it)")

	for range 3 {
		res := tick(t, m)
		require.NotNil(t, res)
		m.Update(*res)
	}
	assert.Equal(t, []bool{false, false, false}, f.spawns, "a waiting TUI never starts a daemon")
	assert.Equal(t, linkWaiting, m.link.state)
	assert.Equal(t, waitPoll, m.nextRejoinDelay())
	assert.Empty(t, m.link.note, "no daemon is the normal case")
}

// TestLink_CrashReconnectsSpawning: a loss with no bye is a crash: the TUI
// reconnects, starting a daemon, on a growing backoff.
func TestLink_CrashReconnectsSpawning(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, st := linkedHome(t, "")
	f := failing(errors.New("the loom daemon (pid 7) does not answer"))
	m.rejoin = f.rejoin

	crash(t, m, st)
	require.Equal(t, linkReconnecting, m.link.state)
	assert.Equal(t, time.Second, m.nextRejoinDelay())
	for _, want := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		res := tick(t, m)
		require.NotNil(t, res)
		m.Update(*res)
		assert.Equal(t, want, m.nextRejoinDelay())
	}
	assert.Equal(t, []bool{true, true, true}, f.spawns, "a crash's TUI starts a daemon")
	text := viewText(m)
	assert.Contains(t, text, "⚠ lost the loom daemon: reconnecting (attempt 4)")
	assert.Contains(t, text, "the loom daemon (pid 7) does not answer")
	assert.Contains(t, text, "ctrl+r retries now")

	assert.Nil(t, m.rejoinTick(rejoinTickMsg{gen: m.link.gen - 1}), "a stale tick starts nothing")
}

// TestLink_ThreeFailedSpawnsDropToWaiting: a daemon the TUI starts that
// does not start, three times in a row, has it stop starting them: it
// waits, naming serve.log and ctrl+r.
func TestLink_ThreeFailedSpawnsDropToWaiting(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, st := linkedHome(t, "")
	f := failing(fmt.Errorf("%w: exit status 1", ErrDaemonDidNotStart))
	m.rejoin = f.rejoin

	crash(t, m, st)
	for i := 1; i <= maxSpawnFails; i++ {
		require.Equal(t, linkReconnecting, m.link.state, "attempt %d", i)
		res := tick(t, m)
		require.NotNil(t, res)
		m.Update(*res)
	}
	assert.Equal(t, linkWaiting, m.link.state)
	text := viewText(m)
	assert.Contains(t, text, "ctrl+r starts it")
	assert.Contains(t, text, "serve.log")

	res := tick(t, m)
	require.NotNil(t, res)
	m.Update(*res)
	assert.Equal(t, []bool{true, true, true, false}, f.spawns, "then it waits, starting none")
	assert.Contains(t, viewText(m), "serve.log", "the note stays while it waits")
}

// TestLink_ADaemonThatDiesSoonAfterARejoinCounts: a daemon the TUI
// rejoins that is lost again within stableLink is no success: it counts
// toward maxSpawnFails, and the backoff goes on from its last attempt, so
// three quick fatal-after-join cycles drop the TUI to waiting, starting no
// more daemons, instead of restarting one every second for good.
func TestLink_ADaemonThatDiesSoonAfterARejoinCounts(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, a := linkedHome(t, "")
	clock := time.Unix(1_000_000, 0)
	m.now = func() time.Time { return clock }
	crash(t, m, a)
	for i, delay := range []time.Duration{2 * time.Second, 4 * time.Second, 0} {
		b, _ := bootDaemon(t, &recordingExec{})
		rejoinOnto(t, m, b)
		clock = clock.Add(5 * time.Second)
		crash(t, m, b)
		if delay == 0 {
			break
		}
		require.Equal(t, linkReconnecting, m.link.state, "cycle %d", i+1)
		assert.Equal(t, i+1, m.link.spawnFails, "cycle %d", i+1)
		assert.Equal(t, delay, m.nextRejoinDelay(), "cycle %d: the backoff goes on", i+1)
	}
	require.Equal(t, linkWaiting, m.link.state, "three daemons that did not stay up")
	assert.Contains(t, viewText(m), "it did not stay up 3 times: see serve.log")
	f := failing(daemon.ErrNoDaemon)
	m.rejoin = f.rejoin
	res := tick(t, m)
	require.NotNil(t, res)
	assert.Equal(t, []bool{false}, f.spawns, "waiting: it starts no daemon")
}

// TestLink_AStableLinkResetsTheCounts: a rejoined link that stayed up for
// stableLink was a success: its loss starts the backoff and the counts
// over.
func TestLink_AStableLinkResetsTheCounts(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, a := linkedHome(t, "")
	clock := time.Unix(1_000_000, 0)
	m.now = func() time.Time { return clock }
	crash(t, m, a)
	b, _ := bootDaemon(t, &recordingExec{})
	rejoinOnto(t, m, b)
	clock = clock.Add(stableLink)
	crash(t, m, b)
	require.Equal(t, linkReconnecting, m.link.state)
	assert.Zero(t, m.link.spawnFails)
	assert.Zero(t, m.link.attempt)
	assert.Equal(t, time.Second, m.nextRejoinDelay())
}

// TestLink_CtrlRSpawnsWhileWaiting: ctrl+r joins a daemon now, starting
// one, even while waiting; the tick already scheduled is dropped. While
// the daemon is stopping it only says so.
func TestLink_CtrlRSpawnsWhileWaiting(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, st := linkedHome(t, "")
	f := failing(daemon.ErrNoDaemon)
	m.rejoin = f.rejoin
	ctrlR := tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}

	bye(t, m, st)
	assert.Nil(t, press(m, ctrlR))
	assert.Contains(t, m.errBox.String(), "the loom daemon is still stopping")
	assert.Empty(t, f.spawns)

	st.srv.Close()
	wake(t, m, func() bool { return st.c.Err() != nil })
	require.Equal(t, linkWaiting, m.link.state)
	scheduled := m.link.gen

	cmd := press(m, ctrlR)
	require.NotNil(t, cmd)
	assert.True(t, m.link.busy)
	assert.Nil(t, m.rejoinTick(rejoinTickMsg{gen: scheduled}), "the tick scheduled before is stale")
	res, ok := cmd().(rejoinedMsg)
	require.True(t, ok)
	assert.Equal(t, []bool{true}, f.spawns, "ctrl+r starts a daemon")
	m.Update(res)
	assert.False(t, m.link.busy)
	assert.Equal(t, linkWaiting, m.link.state)
}

// TestLink_ANewerDaemonExits: a rejoin that finds a newer loom's daemon
// ends the TUI, with the error Run returns.
func TestLink_ANewerDaemonExits(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, st := linkedHome(t, "")
	newer := fmt.Errorf("the loom daemon is 0.14.0: %w", ErrDaemonNewer)
	m.rejoin = failing(newer).rejoin

	stop(t, m, st)
	res := tick(t, m)
	require.NotNil(t, res)
	_, cmd := m.Update(*res)
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	assert.ErrorIs(t, m.exitErr, ErrDaemonNewer)
	_, cmd = m.Update(coreWakeMsg{})
	assert.Nil(t, cmd, "nothing more is handled")
}

// TestOfflineKeyGate: offline, the keys that talk to tmux or stay in the
// TUI pass; every other is refused with an info line and dispatches
// nothing, in focus, overview and workbench alike. Connected, the gate
// refuses nothing.
func TestOfflineKeyGate(t *testing.T) {
	m := newTestHome(t)
	m.errBox.SetSize(400, 1)
	for key := range offlineKeyAllowed {
		assert.False(t, m.offlineKeyRefused(key), "connected: %q", key)
	}
	assert.False(t, m.offlineKeyRefused("n"), "connected")

	m.link.state = linkWaiting
	for key := range offlineKeyAllowed {
		keyFor(t, key) // a key a terminal can send
		assert.False(t, m.offlineKeyRefused(key), "offline, %q passes", key)
	}
	for _, mode := range []viewMode{viewFocus, viewOverview, viewWorkbench} {
		for _, key := range []string{"n", "N", "I", "D", "p", "s", "m", "r", "R", "W", "S", "a", "x", "ctrl+o"} {
			m.viewMode = mode
			m.errBox.Clear()
			_, cmd := handleStateDefaultKey(m, keyFor(t, key))
			assert.Nil(t, cmd, "mode %d: %q dispatches nothing", mode, key)
			assert.Equal(t, stateDefault, m.state, "mode %d: %q opens nothing", mode, key)
			assert.Contains(t, m.errBox.String(), "the loom daemon is stopped: "+key+" needs it", "mode %d", mode)
		}
	}
	m.viewMode = viewFocus
	m.link.state = linkReconnecting
	m.errBox.Clear()
	handleStateDefaultKey(m, keyFor(t, "n"))
	assert.Contains(t, m.errBox.String(), "the loom daemon is unreachable: n needs it")
}

// TestOfflineKeyGate_TheWorkbenchsPanesTakeTheirKeysFirst: offline, the
// markdown editor still takes every key as text and the review its own
// (s toggles its sidebar, all in the TUI); a key they decline meets the
// gate.
func TestOfflineKeyGate_TheWorkbenchsPanesTakeTheirKeysFirst(t *testing.T) {
	t.Run("the review", func(t *testing.T) {
		m, _ := newReviewWorkbenchHome(t)
		m.errBox.SetSize(400, 1)
		enterReview(t, m)
		m.link.state = linkWaiting
		handleStateDefaultKey(m, keyFor(t, "s"))
		assert.NotContains(t, m.errBox.String(), "needs it", "the review's own key")
		_, cmd := handleStateDefaultKey(m, keyFor(t, "D"))
		assert.Nil(t, cmd)
		assert.Contains(t, m.errBox.String(), "the loom daemon is stopped: D needs it", "a key the review declines")
	})
	t.Run("the editor", func(t *testing.T) {
		m, _ := newReviewWorkbenchHome(t)
		m.errBox.SetSize(400, 1)
		require.True(t, m.workbench.Markdown.StartEdit())
		m.link.state = linkWaiting
		handleStateDefaultKey(m, keyFor(t, "n"))
		assert.NotContains(t, m.errBox.String(), "needs it", "typed as text")
		assert.True(t, m.workbench.Markdown.EditDirty())
	})
}

// keyFor is the key press whose String is s.
func keyFor(t *testing.T, s string) tea.KeyPressMsg {
	t.Helper()
	named := map[string]rune{
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"tab": tea.KeyTab, "enter": tea.KeyEnter, "esc": tea.KeyEsc,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
	}
	var k tea.KeyPressMsg
	name := s
	for prefix, mod := range map[string]tea.KeyMod{"ctrl+": tea.ModCtrl, "alt+": tea.ModAlt, "shift+": tea.ModShift} {
		if rest, ok := strings.CutPrefix(name, prefix); ok && rest != "" {
			k.Mod, name = mod, rest
		}
	}
	if code, ok := named[name]; ok {
		k.Code = code
	} else {
		k.Code = rune(name[0])
		if k.Mod == 0 {
			k.Text = name
		}
	}
	require.Equal(t, s, k.String())
	return k
}

// TestOffline_UIPrefsApplyAndAreSentOnResync: a pref changed offline
// applies at once and waits; the resync sends it to the daemon joined, so
// the TUI's own value wins over the one the daemon loaded.
func TestOffline_UIPrefsApplyAndAreSentOnResync(t *testing.T) {
	isolateTmux(t)
	twoRows(t)
	m, st := linkedHome(t, "")
	crash(t, m, st)

	pressScript(t, m, keyFor(t, "\\"))
	assert.True(t, m.railHidden, "the rail hides offline")
	assert.True(t, m.uiPrefs().RailHidden)
	require.NotNil(t, m.unsentPrefs)

	b, _ := bootDaemon(t, &recordingExec{})
	m.rejoin = func(bool, func(string)) (*rpc.Client, error) { return b.c, nil }
	res := tick(t, m)
	require.NotNil(t, res)
	_, cmd := m.Update(*res)
	runCmds(t, cmd)
	require.Equal(t, linkConnected, m.link.state)
	assert.Nil(t, m.unsentPrefs, "sent")
	assert.True(t, m.uiPrefs().RailHidden, "the TUI's own value, over the one the daemon loaded")
	v, ok := b.c.Workspace(m.id)
	require.True(t, ok)
	assert.True(t, v.UIPrefs.RailHidden, "the new daemon has it")
}

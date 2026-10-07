package app

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui/overlay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runningInstance adds a Running (never actually started) instance to
// m's focused list.
func runningInstance(t *testing.T, m *home, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	m.ws.Add(inst)
	m.syncViews()
	return inst
}

// TestCompletionDuringNaming_CancelKillsOnlyThePendingInstance is the
// reviewer's repro: a start completing while a second new instance is
// being named moved the selection onto the started one, and ctrl+c — which
// popped the selection — then killed it (worktree, branch -D).
func TestCompletionDuringNaming_CancelKillsOnlyThePendingInstance(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	m.errBox.SetSize(400, 1)
	first := runningInstance(t, m, "first")
	finishStart(t, first) // active, so only the open flow keeps the completion off the selection
	_, _ = runNewInstance(m)
	require.Equal(t, stateNew, m.state)
	require.NotEqual(t, idOf(m, first), selID(m.list), "the draft's row (ID 0) is selected")

	deliver(t, m, core.StartResult{Instance: first, Owner: m.ws})
	assert.Equal(t, core.InstanceID(0), selID(m.list), "a completion must not move the selection under the naming flow")
	assert.Equal(t, stateNew, m.state)
	assert.Contains(t, m.errBox.String(), "first", "the start is still announced")

	_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	assert.Contains(t, listIDs(m.list), idOf(m, first), "cancel must not kill the started session")
	assert.NotContains(t, listIDs(m.list), core.InstanceID(0), "cancel removes the draft's row")
	assert.Equal(t, session.Running, first.GetStatus())
}

// TestCompletionDuringInlineAttach_KeepsTheAttachTarget: inline attach
// forwards each key to the selected instance, so a completion that moved
// the selection retargeted the user's typing.
func TestCompletionDuringInlineAttach_KeepsTheAttachTarget(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	attached := runningInstance(t, m, "attached")
	first := runningInstance(t, m, "first")
	// Both active, so only inline attach keeps the completion off the
	// selection.
	finishStart(t, attached)
	finishStart(t, first)
	selectIn(m, m.list, attached)
	m.state = stateInlineAttach

	deliver(t, m, core.StartResult{Instance: first, Owner: m.ws})
	assert.Equal(t, idOf(m, attached), selID(m.list), "keys must keep going to the attached session")
	assert.Equal(t, stateInlineAttach, m.state)
}

// TestRecoverDuringNaming_LeavesThePendingInstanceAlone: a recovered row
// appended during naming became the list's last row — the one the naming
// flow edited — and was selected, so cancel then killed it.
func TestRecoverDuringNaming_LeavesThePendingInstanceAlone(t *testing.T) {
	m, _, _ := ownerTestHome(t)
	placeholder, err := session.FromInstanceData(session.InstanceData{
		Title: "orphan", Status: session.Recoverable, Program: "claude", IsWorkspaceTerminal: true,
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, placeholder.TransitionTo(session.Loading))
	m.ws.Add(placeholder)
	m.syncViews()
	_, _ = runNewInstance(m)
	pending := m.draft
	recovered, err := session.NewInstance(session.InstanceOptions{Title: "orphan", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, recovered.TransitionTo(session.Running))

	deliver(t, m, core.RecoverResult{OldTitle: "orphan", Recovered: recovered, Placeholder: placeholder, Owner: m.ws})
	assert.Equal(t, core.InstanceID(0), selID(m.list), "the recover must not move the selection under the naming flow")

	typeTitle(t, m, "x")
	assert.Equal(t, "x", pending.title, "typing names the draft")
	assert.Equal(t, "orphan", recovered.Title, "not the recovered row")

	_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	assert.Contains(t, listIDs(m.list), idOf(m, recovered), "cancel must not remove the recovered session")
	assert.NotContains(t, listIDs(m.list), core.InstanceID(0))
}

// fakeTmuxServer is a scripted tmux server: has-session answers from a set
// of live session names, and kill-session removes one and is recorded.
// Enough for reconcile, liveness probes and Kill; nothing real is touched.
type fakeTmuxServer struct {
	mu    sync.Mutex
	live  map[string]bool
	kills []string
}

func newFakeTmuxServer(liveTitles ...string) *fakeTmuxServer {
	f := &fakeTmuxServer{live: map[string]bool{}}
	for _, title := range liveTitles {
		f.live[tmux.ToLoomTmuxName(title)] = true
	}
	return f
}

func (f *fakeTmuxServer) exec() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			name := tmuxTarget(c.Args)
			f.mu.Lock()
			defer f.mu.Unlock()
			switch {
			case slices.Contains(c.Args, "has-session"):
				if !f.live[name] {
					return exec.ErrNotFound
				}
			case slices.Contains(c.Args, "kill-session"):
				f.kills = append(f.kills, name)
				delete(f.live, name)
			}
			return nil
		},
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
}

// killed reports whether anything ran kill-session on title's session.
func (f *fakeTmuxServer) killed(title string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.kills, tmux.ToLoomTmuxName(title))
}

// tmuxTarget extracts the session name a tmux command's -t names: loom
// passes "-t", tmux.SessionTarget(name) ("=name") or tmux.PaneTarget(name)
// ("=name:").
func tmuxTarget(args []string) string {
	for i, a := range args {
		if a == "-t" && i+1 < len(args) {
			return strings.TrimSuffix(strings.TrimPrefix(args[i+1], "="), ":")
		}
	}
	return ""
}

// startedWorktreeInstance is a started, Running session with a git worktree
// record at wtPath and no client: a start attaches none — what a start
// leaves behind — whose tmux session lives on srv.
func startedWorktreeInstance(t *testing.T, title, wtPath string, srv *fakeTmuxServer) *session.Instance {
	t.Helper()
	srv.mu.Lock()
	srv.live[tmux.ToLoomTmuxName(title)] = true
	srv.mu.Unlock()
	inst, err := session.FromInstanceData(worktreeRecord(title, wtPath, session.Paused), t.TempDir())
	require.NoError(t, err)
	inst.SetTmuxSession(tmux.NewSessionWithDeps(title, "claude", fakePtyFactory{t: t}, srv.exec()))
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst
}

func worktreeRecord(title, wtPath string, status session.Status) session.InstanceData {
	return session.InstanceData{
		Title: title, Status: status, Program: "claude", Branch: "loom/" + title,
		Worktree: session.GitWorktreeData{RepoPath: filepath.Dir(wtPath), WorktreePath: wtPath, BranchName: "loom/" + title, SessionName: title},
	}
}

// reopenedHome closes fleetHome's "afocus" tab and reopens the workspace as
// a new slot whose list holds the reconciled copy of a Loading record for
// title at twinWorktree — built through the real ReconcileAndRestore path
// against reopenExec, the tmux server as the reopen saw it. Returns the
// closed owner, the twin, and recorders for the owner's and the reopened
// slot's storage.
func reopenedHome(t *testing.T, title, twinWorktree string, reopenExec cmd_test.MockCmdExec) (m *home, owner *workspaceSlot, twin *session.Instance, recA, recC *recordingInstanceStorage) {
	t.Helper()
	m, recA, _ = ownerTestHome(t)
	owner = m.workspaceSlot
	drainCmd(m.applyWorkspaceToggle([]config.Workspace{{Name: "bpeer"}}))
	reopened := fleetSlot(t, "afocus")
	recC = &recordingInstanceStorage{}
	storageC, err := session.NewStorage(recC, t.TempDir())
	require.NoError(t, err)
	reworkspace(t, m, reopened, func(p *core.WorkspaceParts) { p.Storage = storageC })
	twin, err = session.ReconcileAndRestore(worktreeRecord(title, twinWorktree, session.Loading), t.TempDir(), reopenExec)
	require.NoError(t, err)
	require.True(t, twin.Paused(), "fixture: a reconciled Loading record comes back Paused")
	reopened.ws.Add(twin)
	m.slots = append(m.slots, reopened)
	wireCore(t, m)
	recA.calls = 0
	return m, owner, twin, recA, recC
}

// TestInstanceStarted_OwnerReopened: the owner was closed and the same
// workspace reopened while the start ran. The reopened slot reconciled the
// start's Loading record into a twin (Paused, unattached) — marking it
// paused if the tmux session wasn't up yet, killing the session first if
// it was.
func TestInstanceStarted_OwnerReopened(t *testing.T) {
	isolateTmux(t)
	wtPath := filepath.Join(t.TempDir(), "late-wt")
	late := tmux.ToLoomTmuxName("late")

	t.Run("success takes the twin's place", func(t *testing.T) {
		m, owner, twin, recA, recC := reopenedHome(t, "late", wtPath, deadCmdExecForTest())
		twinID := idOf(m, twin) // captured while loaded: a removal forgets it
		started := startedWorktreeInstance(t, "late", wtPath, newFakeTmuxServer())
		owner.ws.Add(started)
		m.syncViews()

		cmd := deliver(t, m, core.StartResult{Instance: started, Owner: owner.ws})
		drainCmd(cmd)

		reopened := m.slots[1]
		assert.Equal(t, idOf(m, started), titleID(reopened.list, "late"), "the started instance replaces the twin")
		assert.NotContains(t, listIDs(reopened.list), twinID)
		assert.GreaterOrEqual(t, recC.calls, 1, "the reopened slot is saved")
		assert.Zero(t, recA.calls, "the closed owner's stale copy is not")
		assert.True(t, m.panes.Alive(late), "it is displayed again, so it gets a client")
	})

	t.Run("failure leaves the twin's worktree and branch alone", func(t *testing.T) {
		m, owner, twin, _, _ := reopenedHome(t, "late", wtPath, deadCmdExecForTest())
		srv := newFakeTmuxServer()
		started := startedWorktreeInstance(t, "late", wtPath, srv)
		owner.ws.Add(started)
		m.syncViews()

		cmd := deliver(t, m, core.StartResult{Instance: started, Err: errors.New("boom"), Owner: owner.ws})
		drainCmd(cmd)

		assert.False(t, srv.killed("late"), "not killed: the reopened record owns its worktree and branch")
		assert.Nil(t, m.panes.Get(late), "nothing attaches a failed start")
		assert.Equal(t, idOf(m, twin), titleID(m.slots[1].list, "late"))
	})

	t.Run("a namesake with another worktree is not a twin", func(t *testing.T) {
		m, owner, namesake, _, recC := reopenedHome(t, "late", filepath.Join(t.TempDir(), "other-wt"), deadCmdExecForTest())
		m.errBox.SetSize(400, 1)
		started := startedWorktreeInstance(t, "late", wtPath, newFakeTmuxServer())
		owner.ws.Add(started)
		m.syncViews()

		cmd := deliver(t, m, core.StartResult{Instance: started, Owner: owner.ws})
		drainCmd(cmd)

		assert.Equal(t, idOf(m, namesake), titleID(m.slots[1].list, "late"), "an unrelated same-titled session is untouched")
		assert.Zero(t, recC.calls)
		assert.Nil(t, m.panes.Get(late), "the start stays with its closed owner, so nothing attaches it")
		// The notice used to say the workspace is no longer open, while
		// its reopened tab sat right there.
		assert.NotContains(t, m.errBox.String(), "no longer open")
		assert.Contains(t, m.errBox.String(), "reopened")
	})

	t.Run("a session the reopen killed is not adopted", func(t *testing.T) {
		// The start's session was already up when the reopen reconciled
		// the Loading record: ActionKillAndPause killed it.
		srv := newFakeTmuxServer()
		started := startedWorktreeInstance(t, "late", wtPath, srv)
		m, owner, twin, _, recC := reopenedHome(t, "late", wtPath, srv.exec())
		require.True(t, srv.killed("late"), "fixture: reconcile killed the live session")
		owner.ws.Add(started)
		m.syncViews()

		cmd := deliver(t, m, core.StartResult{Instance: started, Owner: owner.ws})
		drainCmd(cmd)

		assert.Equal(t, idOf(m, twin), titleID(m.slots[1].list, "late"), "the twin stays: its record is the live truth")
		assert.Zero(t, recC.calls)
		assert.Nil(t, m.panes.Get(late), "nothing attaches the dead start")
	})
}

// TestScriptWorkspaceSwitchDuringNaming_IsIgnored: deferred script actions
// apply whenever their message lands, in any state. A script that runs
// new_instance (which parks the handler until the naming flow has opened)
// and then switches workspace used to move focus while the naming overlay
// was open, so the new session was started — and stamped — against the
// wrong workspace.
func TestScriptWorkspaceSwitchDuringNaming_IsIgnored(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "switch.lua"), []byte(`
cs.bind("Z", function(ctx)
  cs.actions.new_instance() -- deferred: parks until the intent ran
  cs.actions.workspace_next()
end)
`), 0o644))
	m, _, _ := ownerTestHome(t)
	initScriptsIn(m, dir, false)
	owner := m.workspaceSlot

	cmd, ok := m.dispatchScript("Z")
	require.True(t, ok)
	pumpScript(t, m, cmd)

	require.Equal(t, stateNew, m.state, "the intent opened the naming flow")
	require.NotNil(t, m.draft)
	assert.Same(t, owner, m.workspaceSlot, "focus must not move while naming is open")
	assert.Contains(t, listIDs(m.list), core.InstanceID(0), "the draft's row is in the focused list")
}

// pumpScript feeds a script's Cmds and their script messages back through
// Update until the dispatch and every resume have run.
func pumpScript(t *testing.T, m *home, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		require.Less(t, steps, 100, "script pump did not settle")
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case scriptDoneMsg, scriptResumeMsg:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}

// TestIssueExpanded_ForReplacedDraftIsDropped: after the #n dispatch the
// draft stays open, its row shown, for up to ~20s; a newer creation flow
// can replace it meanwhile (formerly: D could kill the unstarted instance).
// The expansion must not reopen the flow for a draft no rail shows.
func TestIssueExpanded_ForReplacedDraftIsDropped(t *testing.T) {
	m := newTestHome(t)
	m.errBox.SetSize(400, 1)
	gone := &draft{slot: m.workspaceSlot, path: m.repoPath(), title: "gone"}
	require.Nil(t, m.draft, "fixture: the expansion's draft is no longer open")

	_, _ = m.Update(issueExpandedMsg{draft: gone, repo: m.repoPath(), number: 5})

	assert.Equal(t, stateDefault, m.state, "no launch options for a replaced draft")
	assert.Nil(t, m.draft)
	assert.Contains(t, m.errBox.String(), "#5")
}

// TestKillAction_UsesTheDispatchSlotsStorage: the kill's job runs for
// seconds in a Cmd. It read m.storage() there, so after a tab switch it
// deleted the record from whichever workspace was focused by then.
func TestKillAction_UsesTheDispatchSlotsStorage(t *testing.T) {
	isolateTmux(t)
	repo := t.TempDir()
	out, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").CombinedOutput()
	require.NoError(t, err, string(out))

	m, recA, recB := ownerTestHome(t)
	a1, err := session.FromInstanceData(session.InstanceData{
		Title: "a1", Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: t.TempDir(), BranchName: "loom/a1", SessionName: "a1"},
	}, t.TempDir())
	require.NoError(t, err)
	m.ws.Add(a1)
	m.syncViews()
	seed, err := json.Marshal([]session.InstanceData{a1.ToInstanceData()})
	require.NoError(t, err)
	recA.lastData = seed

	_, killAction := m.core.KillInst(m.ws, a1, nil)
	m.switchWorkspaceSlot(1)
	_ = killAction()

	assert.GreaterOrEqual(t, recA.calls, 1, "the record is deleted from its own workspace")
	assert.NotContains(t, string(recA.lastData), `"a1"`)
	assert.Zero(t, recB.calls, "the workspace focused at completion is untouched")
}

// TestCreationCancelPaths_KillThePendingInstanceByIdentity is the defence
// in depth: whatever moved the selection mid-flow, every creation-flow
// cancel path discards the draft (by identity, never the selection) and
// leaves the selected session.
func TestCreationCancelPaths_KillThePendingInstanceByIdentity(t *testing.T) {
	type flow struct {
		name   string
		open   func(m *home)
		cancel func(m *home)
	}
	naming := func(m *home) { _, _ = runNewInstance(m) }
	toLaunchOptions := func(m *home) {
		naming(m)
		typeTitle(t, m, "abc")
		_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, stateLaunchOptions, m.state)
	}
	toPrompt := func(m *home) {
		_, _ = runPromptNewInstance(m)
		typeTitle(t, m, "abc")
		_, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, statePrompt, m.state)
	}
	for _, f := range []flow{
		{"ctrl+c while naming", naming, func(m *home) { _, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}) }},
		{"esc while naming", naming, func(m *home) { _, _ = handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEsc}) }},
		{"launch options cancelled", toLaunchOptions, func(m *home) { _, _ = m.cancelLaunchOptions() }},
		{"prompt overlay cancelled", toPrompt, func(m *home) { _ = m.cancelPromptOverlay() }},
		{"remote-control confirm cancelled", toLaunchOptions, func(m *home) {
			_ = m.promptRemoteControlBlocked(overlay.ConfirmationTask{}, "")
			m.confirmation().OnCancel()
			drainCmd(m.pendingConfirmation.Async)
		}},
	} {
		t.Run(f.name, func(t *testing.T) {
			m := newTestHome(t)
			first := runningInstance(t, m, "first")
			f.open(m)
			require.NotNil(t, m.draft)
			selectIn(m, m.list, first) // however it moved
			f.cancel(m)
			assert.Contains(t, listIDs(m.list), idOf(m, first), "the selected session must survive the cancel")
			assert.NotContains(t, listIDs(m.list), core.InstanceID(0), "the draft's row is removed")
			assert.Nil(t, m.draft)
		})
	}
}

// TestDiscardDraft_NeverKillsAStartedInstance is a belt: a draft is not a
// session, so a cancel has nothing to kill, and a live session in the list
// is left alone.
func TestDiscardDraft_NeverKillsAStartedInstance(t *testing.T) {
	isolateTmux(t)
	m, _, _ := ownerTestHome(t)
	live := liveInstance(t, "live")
	m.ws.Add(live)
	m.syncViews()
	m.newDraft("pending", "", 0)

	m.discardDraft()
	assert.Empty(t, requestResults(t, m), "a cancel queues no job: there is nothing to kill")
	assert.Contains(t, listIDs(m.list), idOf(m, live))
	assert.Nil(t, m.draft)
}

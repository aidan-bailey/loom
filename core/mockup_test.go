package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/mockup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const companionHTML = `<html><body>Which layout?<script>window.brainstorm = {};</script></body></html>`

// companionIn writes a live companion on port into tree, its one screen
// changed at at.
func companionIn(t *testing.T, tree string, port int, screen string, at time.Time) {
	t.Helper()
	dir := filepath.Join(tree, ".superpowers", "brainstorm", "1-1")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "state"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "content"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state", "server-info"), []byte(fmt.Sprintf(`{"port":%d}`, port)), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state", "server.pid"), []byte("4242\n"), 0o644))
	p := filepath.Join(dir, "content", screen)
	require.NoError(t, os.WriteFile(p, []byte("<h2>x</h2>"), 0o644))
	require.NoError(t, os.Chtimes(p, at, at))
}

// companionServer serves companionHTML on loopback and returns its port.
func companionServer(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(companionHTML))
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return port
}

// writeHook drops one hook event into inst's hooks folder. The test
// instance starts on a mock tmux session, so no launch prepared the folder;
// it adopts this one's launch ID, as a restored instance would.
func writeHook(t *testing.T, inst *session.Instance, payload string) {
	t.Helper()
	dir := session.SubagentHooksDir(inst.ConfigDir, inst.Title)
	_, err := hooks.Prepare(dir, "")
	require.NoError(t, err)
	name := fmt.Sprintf("%d-1.json", time.Now().UnixNano())
	require.NoError(t, os.WriteFile(filepath.Join(hooks.EventsDir(dir), name), []byte(payload), 0o600))
}

// scanNow runs one hook scan of m's active instances as the loop would,
// inside hookScanInterval of the last one too. What an earlier delivery
// queued (a status change's roster query) is dropped unrun.
func scanNow(t *testing.T, m *Model) {
	t.Helper()
	_ = m.Drain()
	m.gate(gateHookScan).last = time.Time{}
	require.True(t, m.maybeHookScan(m.activeInstances()))
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	m.Deliver(jobs[0]())
}

// TestHookScan_AStopFindsTheMockupInTheSameDelivery: the push trigger reads
// the projection the status change lands in, so the screen must land in
// the same one for a push to link the tab.
func TestHookScan_AStopFindsTheMockupInTheSameDelivery(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "brainstorm")
	companionIn(t, inst.GetWorktreePath(), 50001, "layout.html", time.Now())
	writeHook(t, inst, `{"hook_event_name":"Stop"}`)

	scanNow(t, m)

	v := m.viewOf(inst)
	assert.Equal(t, session.Ready, v.Status)
	assert.Equal(t, "layout.html", v.Mockup.Screen, "the status and the mockup land in one delivery")
	assert.True(t, v.Mockup.Fresh)
}

func TestHookScan_OnlyAHandBackLooks(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "brainstorm")
	looks := 0
	old := findMockup
	findMockup = func(tree string) (mockup.Companion, bool, error) { looks++; return mockup.Find(tree) }
	t.Cleanup(func() { findMockup = old })

	writeHook(t, inst, `{"hook_event_name":"UserPromptSubmit"}`)
	scanNow(t, m)
	assert.Zero(t, looks, "a scan that hands nothing back reads no more than before")

	writeHook(t, inst, `{"hook_event_name":"Stop","agent_id":"a1"}`)
	scanNow(t, m)
	assert.Zero(t, looks, "a subagent's Stop ends no turn of the session's")

	writeHook(t, inst, `{"hook_event_name":"Stop"}`)
	scanNow(t, m)
	assert.Equal(t, 1, looks)

	writeHook(t, inst, `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
	scanNow(t, m)
	assert.Equal(t, 2, looks, "a permission prompt hands the turn back too")
}

// TestHookScan_AStoppedCompanionClearsTheMockup: a hand-back that finds no
// companion clears what an earlier one found.
func TestHookScan_AStoppedCompanionClearsTheMockup(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "brainstorm")
	inst.SetMockup("layout.html", time.Now())
	writeHook(t, inst, `{"hook_event_name":"Stop"}`)

	scanNow(t, m)

	assert.Empty(t, m.viewOf(inst).Mockup.Screen)
}

func TestMockup_FreshUntilTheNextTurn(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "brainstorm")
	start := inst.TurnStart()
	require.False(t, start.IsZero())

	inst.SetMockup("layout.html", start.Add(time.Second))
	assert.True(t, m.viewOf(inst).Mockup.Fresh, "changed in the latest turn")

	require.NoError(t, inst.TransitionTo(session.Prompting))
	require.NoError(t, inst.TransitionTo(session.Running))
	assert.True(t, m.viewOf(inst).Mockup.Fresh, "a prompt inside the turn keeps it fresh")

	require.NoError(t, inst.TransitionTo(session.Ready))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, inst.TransitionTo(session.Running))
	inst.SetMockup("layout.html", start.Add(time.Millisecond))
	v := m.viewOf(inst)
	assert.Equal(t, "layout.html", v.Mockup.Screen)
	assert.False(t, v.Mockup.Fresh, "a new turn leaves the old screen behind")

	stale := &session.Instance{Title: "s", Status: session.Ready}
	stale.SetMockup("layout.html", time.Now())
	assert.False(t, m.viewOf(stale).Mockup.Fresh, "no turn seen: never fresh")
	assert.Equal(t, MockupView{}, m.viewOf(&session.Instance{Title: "none"}).Mockup)
}

func TestReadMockup_AnswersThePageAndRefreshesTheView(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst, _ := worktreeInst(t, "brainstorm")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)
	at := time.Now().Add(-time.Minute).Round(time.Second)
	companionIn(t, inst.GetWorktreePath(), companionServer(t), "layout.html", at)

	m.ReadMockup(m.idOf(inst), 7)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	assert.Equal(t, ReqID(7), rs[0].Req)
	assert.Equal(t, "layout.html", rs[0].Mockup.Screen)
	assert.True(t, rs[0].Mockup.Changed.Equal(at))
	assert.Equal(t, companionHTML, rs[0].Mockup.HTML)
	assert.Equal(t, "layout.html", m.viewOf(inst).Mockup.Screen)
}

func TestReadMockup_AnswersWhyWithNoCompanionAndClearsTheView(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst, _ := worktreeInst(t, "brainstorm")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)
	inst.SetMockup("old.html", time.Now())

	m.ReadMockup(m.idOf(inst), 7)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.Error(t, rs[0].Err)
	assert.Equal(t, "read the mockup of brainstorm: no brainstorm companion is running in this session's worktree", rs[0].Err.Error())
	assert.Empty(t, rs[0].Mockup.HTML)
	assert.Empty(t, m.viewOf(inst).Mockup.Screen)
}

func TestReadMockup_AnswersWhyThePageWasRefused(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst, _ := worktreeInst(t, "brainstorm")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>another server</html>"))
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	companionIn(t, inst.GetWorktreePath(), port, "layout.html", time.Now())

	m.ReadMockup(m.idOf(inst), 7)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.Error(t, rs[0].Err)
	assert.Equal(t, fmt.Sprintf("read the mockup of brainstorm: port %d is not the brainstorm companion", port), rs[0].Err.Error())
	assert.Equal(t, "layout.html", m.viewOf(inst).Mockup.Screen, "the companion was found; only its page was refused")
}

// TestReadMockup_ACompanionThatIsGoneClearsTheView: a companion killed
// without its shutdown leaves its files, so Find still names it; nothing
// listening on its port proves it gone.
func TestReadMockup_ACompanionThatIsGoneClearsTheView(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst, _ := worktreeInst(t, "brainstorm")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)
	srv := httptest.NewServer(http.NotFoundHandler())
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	srv.Close()
	companionIn(t, inst.GetWorktreePath(), port, "layout.html", time.Now())
	inst.SetMockup("layout.html", time.Now())

	m.ReadMockup(m.idOf(inst), 7)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.ErrorIs(t, rs[0].Err, mockup.ErrNotRunning)
	assert.Empty(t, m.viewOf(inst).Mockup.Screen)
}

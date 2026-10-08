package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspace is a fixture workspace over a fresh config dir, holding insts.
func workspace(t *testing.T, name string, insts ...*session.Instance) *core.Workspace {
	t.Helper()
	dir := t.TempDir()
	state := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(state, dir)
	require.NoError(t, err)
	ws := core.NewWorkspace(core.WorkspaceParts{
		Ctx:     &config.WorkspaceContext{Name: name, ConfigDir: dir, RepoPath: t.TempDir()},
		Storage: storage, Config: config.DefaultConfig(), State: state,
	})
	for _, inst := range insts {
		ws.AddForTest(inst)
	}
	return ws
}

// running is a fixture instance marked Running, never started.
func running(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst
}

// pair serves loop to a client over a pipe (not synchronous: what
// production gets), both stopped when the test ends.
func pair(t *testing.T, loop *core.Loop) *Client {
	t.Helper()
	c, stop, err := inProcess(loop, false)
	require.NoError(t, err)
	t.Cleanup(stop)
	return c
}

// catch is the value f panicked with, nil if it returned.
func catch(f func()) (p any) {
	defer func() { p = recover() }()
	f()
	return nil
}

// within fails the test unless f returns within d: a hang is a failure, not
// a stuck test binary.
func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v", what, d)
	}
}

// raised is the value f panicked with, nil if it returned; f hanging fails
// the test.
func raised(t *testing.T, what string, f func()) (p any) {
	t.Helper()
	within(t, 5*time.Second, what, func() { p = catch(f) })
	return p
}

// TestReplica_AnswersAsTheModel: every rpc:local query the client answers
// from its replica gives what the model gives, over the wire, for known
// names and misses alike.
func TestReplica_AnswersAsTheModel(t *testing.T) {
	model := core.NewForTest(core.Options{Program: "claude"})
	x, y := running(t, "x"), running(t, "y")
	a, b := workspace(t, "a", x), workspace(t, "b", y)
	model.SetWorkspacesForTest(nil, []*core.Workspace{a, b})
	reg := account.LoadRegistry(t.TempDir())
	_, _, err := reg.Create("max-2", t.TempDir())
	require.NoError(t, err)
	model.AdoptAccountsForTest(reg)
	t.Cleanup(func() { session.SetAccountDirs(nil, nil) })
	model.SetAccountAuthForTest(map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "r"}})
	model.SetAccountUsageForTest("max-2", account.Usage{Available: true, Plan: "max", At: time.Unix(10, 0)}, errors.New("probe failed"))
	loop := core.StartForTest(model)
	loop.DeliverForTest(core.GitHubResultForTest(true, "", map[string]github.Snapshot{
		"/r": {Issues: map[int]github.Issue{7: {Number: 7, Title: "t"}}},
	}, map[string]error{"/s": errors.New("no remote")}))
	c := pair(t, loop)

	wsIDs := []core.WorkspaceID{0, 99}
	for _, v := range loop.Tabs() {
		wsIDs = append(wsIDs, v.ID)
	}
	var instIDs []core.InstanceID
	for _, id := range wsIDs {
		for _, v := range loop.Views(id) {
			instIDs = append(instIDs, v.ID)
		}
	}
	require.Len(t, instIDs, 2, "fixture: both sessions are listed")
	instIDs = append(instIDs, 0, 99)

	same := func(name string, ask func(core.Core) []any) {
		t.Helper()
		assert.Equal(t, normalize(ask(loop)), normalize(ask(c)), name)
	}
	errText := func(err error) string { return fmt.Sprint(err) }
	same("Tabs", func(k core.Core) []any { return []any{k.Tabs()} })
	same("Classic", func(k core.Core) []any { v, ok := k.Classic(); return []any{v, ok} })
	for _, id := range wsIDs {
		same(fmt.Sprintf("Workspace(%d)", id), func(k core.Core) []any { v, ok := k.Workspace(id); return []any{v, ok} })
		same(fmt.Sprintf("IsLoaded(%d)", id), func(k core.Core) []any { return []any{k.IsLoaded(id)} })
		same(fmt.Sprintf("Views(%d)", id), func(k core.Core) []any { return []any{k.Views(id)} })
	}
	for _, id := range instIDs {
		same(fmt.Sprintf("View(%d)", id), func(k core.Core) []any { v, ok := k.View(id); return []any{v, ok} })
	}
	same("Registry", func(k core.Core) []any { return []any{k.Registry()} })
	same("RestoreFailed", func(k core.Core) []any { return []any{k.RestoreFailed()} })
	same("OpenNames", func(k core.Core) []any { return []any{k.OpenNames()} })
	same("Program", func(k core.Core) []any { return []any{k.Program()} })
	same("RCAuth", func(k core.Core) []any { return []any{k.RCAuth()} })
	same("AccountNames", func(k core.Core) []any { return []any{k.AccountNames()} })
	same("AccountsLoaded", func(k core.Core) []any { return []any{k.AccountsLoaded()} })
	same("HasExtraAccounts", func(k core.Core) []any { return []any{k.HasExtraAccounts()} })
	same("ClaudeProgram", func(k core.Core) []any { return []any{k.ClaudeProgram()} })
	for _, name := range []string{"", account.DefaultName, "max-2", "nobody"} {
		same("Account "+name, func(k core.Core) []any { v, ok := k.Account(name); return []any{v, ok} })
		same("RCAuthFor "+name, func(k core.Core) []any { return []any{k.RCAuthFor(name)} })
		same("AccountLoggedOut "+name, func(k core.Core) []any { return []any{k.AccountLoggedOut(name)} })
		same("AccountSync "+name, func(k core.Core) []any { v, ok := k.AccountSync(name); return []any{v, ok} })
		same("AccountUsage "+name, func(k core.Core) []any { v, err := k.AccountUsage(name); return []any{v, errText(err)} })
		same("AccountEnv "+name, func(k core.Core) []any { v, err := k.AccountEnv(name); return []any{v, errText(err)} })
	}
	for _, repo := range []string{"/r", "/s", "/nowhere"} {
		same("GitHubSnapshot "+repo, func(k core.Core) []any { v, ok := k.GitHubSnapshot(repo); return []any{v, ok} })
		same("GitHubErr "+repo, func(k core.Core) []any { return []any{errText(k.GitHubErr(repo))} })
	}
	same("GitHubUnavailable", func(k core.Core) []any { return []any{k.GitHubUnavailable()} })
	same("GitHubUnavailableReason", func(k core.Core) []any { return []any{k.GitHubUnavailableReason()} })
}

// TestRequest_ItsEventsArriveBeforeItsReply: a read right after a request
// sees the request's effect, with no barrier.
func TestRequest_ItsEventsArriveBeforeItsReply(t *testing.T) {
	model := core.NewForTest(core.Options{})
	x := running(t, "x")
	model.SetWorkspacesForTest(workspace(t, "a", x), nil)
	loop := core.StartForTest(model)
	c := pair(t, loop)
	id := model.IDOfForTest(x)

	c.Kill(id, 0)
	v, ok := c.View(id)
	require.True(t, ok)
	assert.Equal(t, session.Deleting, v.Status, "the kill's Deleting reached the replica before its reply")
}

// TestCast_ReachesTheModel: a cast is applied in order, with no reply.
func TestCast_ReachesTheModel(t *testing.T) {
	model := core.NewForTest(core.Options{})
	loop := core.StartForTest(model)
	c := pair(t, loop)

	c.MarkOutput("loom_x")
	require.NoError(t, c.FlushForTest())
	assert.True(t, model.OutputMarkedForTest("loom_x"))
}

// TestSync_CoalescesStateAndKeepsTheRestInOrder: Sync hands out the newest
// state, once per kind, ahead of the other events in the order they came.
func TestSync_CoalescesStateAndKeepsTheRestInOrder(t *testing.T) {
	model := core.NewForTest(core.Options{Program: "a"})
	loop := core.StartForTest(model)
	c := pair(t, loop)
	c.Sync() // the snapshot

	c.SetProgram("b")
	loop.DeliverForTest(core.RosterResultForTest(nil, errors.New("no roster"), time.Now()))
	c.SetProgram("c")
	events := c.Sync()
	var models []string
	for _, ev := range events {
		if m, ok := ev.(core.ModelChanged); ok {
			models = append(models, m.View.Program)
		}
	}
	assert.Equal(t, []string{"c"}, models, "one ModelChanged, the newest")
	_, first := events[0].(core.ModelChanged)
	assert.True(t, first, "state first")
}

// TestWake_ALoopResultReachesTheClient: on a production loop, a job's
// result lands on the loop, the server publishes on its wake, and the
// client wakes with the events.
func TestWake_ALoopResultReachesTheClient(t *testing.T) {
	model := core.NewForTest(core.Options{})
	// An empty model polls GitHub for the cwd's repository: hold that
	// poll, so the probe is the tick's only job and nothing runs gh.
	model.SetGateForTest("github", true, time.Now())
	loop := core.Start(model)
	c := pair(t, loop)
	c.Sync()

	loop.TickForTest() // its probe runs on a goroutine of its own
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-c.Wakes():
		case <-deadline:
			t.Fatal("no wake")
		}
		for _, ev := range c.Sync() {
			if _, ok := ev.(core.HealthChecked); ok {
				return
			}
		}
	}
}

// panicky is a backend whose SetProgram panics.
type panicky struct{ *core.Loop }

func (panicky) SetProgram(string) { panic("boom") }

// TestPanic_ReachesTheCallerAndEveryLaterCall: the model's panic is raised
// in the caller, and every call after it panics too, a cast and a request
// included, without leaving the client's lock held: Close waits for the
// reader, which needs it. A synchronous client pings before a local read, so
// it takes the request's path on every read.
func TestPanic_ReachesTheCallerAndEveryLaterCall(t *testing.T) {
	for _, synchronous := range []bool{false, true} {
		t.Run(fmt.Sprintf("synchronous=%v", synchronous), func(t *testing.T) {
			loop := core.StartForTest(core.NewForTest(core.Options{}))
			t.Cleanup(loop.Stop)
			srv := NewServer(panicky{loop})
			t.Cleanup(srv.Close)
			a, b := net.Pipe()
			srv.Serve(a)
			c, err := dial(b, synchronous)
			require.NoError(t, err)

			p := catch(func() { c.SetProgram("x") })
			require.NotNil(t, p, "the caller panics")
			w, ok := p.(*core.WireError)
			require.True(t, ok, "with the wire's panic, got %T", p)
			assert.Equal(t, core.CodePanic, w.Code)
			assert.Contains(t, w.Message, "boom")
			assert.NotNil(t, raised(t, "a local read", func() { c.Program() }), "a later local read panics")
			assert.NotNil(t, raised(t, "Sync", func() { c.Sync() }), "and Sync")
			assert.NotNil(t, raised(t, "a cast", func() { c.MarkOutput("x") }), "and a cast")
			assert.NotNil(t, raised(t, "a request", func() { c.Kill(1, 0) }), "and a request")
			within(t, 5*time.Second, "Close after a panic", c.Close)
		})
	}
}

// TestConnectionLost_IsFatal: a connection the client did not close is the
// model lost, which the next call raises, and Wakes tells the TUI to make.
// A void request returning errClosed in silence would let the TUI run on
// against a model it cannot reach.
func TestConnectionLost_IsFatal(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := Dial(b)
	require.NoError(t, err)
	c.Sync()
	select {
	case <-c.Wakes():
	default:
	}

	a.Close() // the server's end, not the client's Close
	select {
	case <-c.Wakes():
	case <-time.After(5 * time.Second):
		t.Fatal("the loss did not wake the client")
	}
	p := catch(func() { c.Program() })
	w, ok := p.(*core.WireError)
	require.True(t, ok, "the next call panics with the wire's error, got %T", p)
	assert.Equal(t, core.CodeError, w.Code)
	assert.Contains(t, w.Message, "connection to the model lost")
	assert.NotNil(t, catch(func() { c.Kill(1, 0) }), "and so does a request")
	within(t, 5*time.Second, "Close after the loss", c.Close)
}

// TestClose_IsNotALoss: a client that closes itself raises nothing after.
func TestClose_IsNotALoss(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := Dial(b)
	require.NoError(t, err)
	within(t, 5*time.Second, "Close", c.Close)
	assert.Nil(t, catch(func() { c.Program() }), "a read after Close is not a panic")
	assert.Nil(t, catch(func() { c.Kill(1, 0) }), "nor a request")
}

// breaker is a backend that publishes an event no codec can carry: a NaN
// in a usage window, in its Sync once armed, or in the snapshot it sends a
// connecting client.
type breaker struct {
	*core.Loop
	sync     atomic.Bool
	snapshot bool
}

var unencodable = core.AccountsChanged{View: core.AccountsView{
	Usage: map[string]account.Usage{"x": {FiveHour: &account.Window{Pct: math.NaN()}}},
}}

func (b *breaker) Sync() []core.Event {
	events := b.Loop.Sync()
	if b.sync.Load() {
		events = append(events, unencodable)
	}
	return events
}

func (b *breaker) SyncAndSnapshot() (published, snapshot []core.Event) {
	published, snapshot = b.Loop.SyncAndSnapshot()
	if b.snapshot {
		snapshot = append(snapshot, unencodable)
	}
	return published, snapshot
}

// TestEncodeFailure_IsFatal: an event that will not encode ends the model
// for every client: dropped, a Reply would strand its requester and a state
// event would leave a replica stale.
func TestEncodeFailure_IsFatal(t *testing.T) {
	fatalOf := func(t *testing.T, p any) {
		t.Helper()
		w, ok := p.(*core.WireError)
		require.True(t, ok, "the call panics with the wire's error, got %T", p)
		assert.Equal(t, core.CodeProtocol, w.Code)
		assert.Contains(t, w.Message, "AccountsChanged", "it names the event")
	}
	serve := func(t *testing.T, bk *breaker) (net.Conn, *Server) {
		loop := core.StartForTest(core.NewForTest(core.Options{}))
		t.Cleanup(loop.Stop)
		bk.Loop = loop
		srv := NewServer(bk)
		t.Cleanup(srv.Close)
		a, b := net.Pipe()
		srv.Serve(a)
		return b, srv
	}

	t.Run("publishing", func(t *testing.T) {
		bk := &breaker{}
		b, _ := serve(t, bk)
		c, err := Dial(b)
		require.NoError(t, err)
		bk.sync.Store(true)
		c.SetProgram("x") // the request's publish meets the event
		fatalOf(t, catch(func() { c.Program() }))
		within(t, 5*time.Second, "Close", c.Close)
	})
	t.Run("snapshot", func(t *testing.T) {
		bk := &breaker{snapshot: true}
		b, _ := serve(t, bk)
		c, err := Dial(b)
		if err != nil { // the fatal frame beat Dial's barrier
			var w *core.WireError
			require.ErrorAs(t, err, &w)
			fatalOf(t, w)
			return
		}
		fatalOf(t, catch(func() { c.Program() }))
		within(t, 5*time.Second, "Close", c.Close)
	})
}

// TestHello_RefusesAnotherProtocol: a server answers a client speaking
// another protocol with a mismatch.
func TestHello_RefusesAnotherProtocol(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	srv.Serve(a)
	t.Cleanup(func() { b.Close() })
	require.NoError(t, json.NewEncoder(b).Encode(Frame{Hello: &Hello{Protocol: Protocol + 1}}))
	var f Frame
	require.NoError(t, json.NewDecoder(b).Decode(&f))
	require.NotNil(t, f.Error)
	assert.Equal(t, core.CodeMismatch, f.Error.Code)
}

// TestClosed_CallsReturn: once the server is gone, calls return at once. A
// call that meets the loss before the reader has seen it returns the closed
// connection's error; one after it raises the loss, which
// TestConnectionLost_IsFatal pins.
func TestClosed_CallsReturn(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := Dial(b)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	srv.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		catch(func() { c.Kill(1, 0) })
		var err error
		p := catch(func() { err = c.Save(1) })
		assert.True(t, err != nil || p != nil, "a call on a closed connection fails: it returns an error or raises the loss")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a call blocked on a closed connection")
	}
}

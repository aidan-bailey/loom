package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
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

// assertLost asserts that c, a daemon's client (Dial), has lost the model
// with code, and reports it without panicking: Err says so, a local read
// answers from the last replica, a request fails with the loss, and a cast
// is dropped. It returns the loss.
func assertLost(t *testing.T, c *Client, code string) *core.WireError {
	t.Helper()
	var w *core.WireError
	require.ErrorAs(t, c.Err(), &w)
	assert.Equal(t, code, w.Code)
	assert.Nil(t, raised(t, "a local read", func() { c.RCAuth() }), "a local read does not panic")
	assert.Nil(t, raised(t, "Sync", func() { c.Sync() }), "nor Sync")
	assert.Nil(t, raised(t, "a cast", func() { c.MarkOutput("x") }), "nor a cast")
	var err error
	assert.Nil(t, raised(t, "a request", func() { _, err = c.Open(1) }), "nor a request")
	var got *core.WireError
	require.ErrorAs(t, err, &got, "the request fails with the loss")
	assert.Equal(t, code, got.Code)
	return w
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
	for _, v := range account.CredentialOverrides {
		t.Setenv(v, "")
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "tok")
	model := core.NewForTest(core.Options{Program: "claude", Registry: &config.WorkspaceRegistry{
		Workspaces:     []config.Workspace{{Name: "a", Path: "/a"}, {Name: "b", Path: "/b"}},
		OpenWorkspaces: []string{"b", "a"},
		LastUsed:       "b",
	}})
	x, y := running(t, "x"), running(t, "y")
	a, b := workspace(t, "a", x), workspace(t, "b", y)
	model.SetWorkspacesForTest(a, b)
	reg := account.LoadRegistry(t.TempDir())
	_, _, err := reg.Create("max-2", t.TempDir())
	require.NoError(t, err)
	model.AdoptAccountsForTest(reg)
	t.Cleanup(func() { session.SetAccountDirs(nil, nil) })
	model.SetAccountAuthForTest(map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "r"}})
	model.SetAccountUsageForTest("max-2", account.Usage{Available: true, Plan: "max", At: time.Unix(10, 0)}, errors.New("probe failed"))
	model.SetRunningAsAccountForTest(`loom is running as account "max-2"`)
	loop := core.StartForTest(model)
	// A repository reached through a symlink and from a subdirectory: the
	// replica finds its snapshot by any spelling, as the model does.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	loop.DeliverForTest(core.GitHubAliasesForTest(core.GitHubResultForTest(true, "", map[string]github.Snapshot{
		"/r":                        {Issues: map[int]github.Issue{7: {Number: 7, Title: "t"}}},
		session.CanonicalPath(real): {Issues: map[int]github.Issue{8: {Number: 8, Title: "u"}}},
	}, map[string]error{"/s": errors.New("no remote")}), map[string]string{"/r/sub": "/r"}))
	c := pair(t, loop)

	wsIDs := []core.WorkspaceID{0, 99}
	for _, v := range loop.Workspaces() {
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
	same("Workspaces", func(k core.Core) []any { return []any{k.Workspaces()} })
	for _, id := range wsIDs {
		same(fmt.Sprintf("Workspace(%d)", id), func(k core.Core) []any { v, ok := k.Workspace(id); return []any{v, ok} })
		same(fmt.Sprintf("IsLoaded(%d)", id), func(k core.Core) []any { return []any{k.IsLoaded(id)} })
		same(fmt.Sprintf("Views(%d)", id), func(k core.Core) []any { return []any{k.Views(id)} })
	}
	for _, id := range instIDs {
		same(fmt.Sprintf("View(%d)", id), func(k core.Core) []any { v, ok := k.View(id); return []any{v, ok} })
	}
	same("Registry", func(k core.Core) []any { return []any{k.Registry()} })
	same("RCAuth", func(k core.Core) []any { return []any{k.RCAuth()} })
	same("AccountNames", func(k core.Core) []any { return []any{k.AccountNames()} })
	same("AccountsLoaded", func(k core.Core) []any { return []any{k.AccountsLoaded()} })
	same("HasExtraAccounts", func(k core.Core) []any { return []any{k.HasExtraAccounts()} })
	same("ClaudeProgram", func(k core.Core) []any { return []any{k.ClaudeProgram()} })
	same("CredentialOverride", func(k core.Core) []any { return []any{k.CredentialOverride()} })
	same("RunningAsAccount", func(k core.Core) []any { return []any{k.RunningAsAccount()} })
	for _, name := range []string{"", account.DefaultName, "max-2", "nobody"} {
		same("Account "+name, func(k core.Core) []any { v, ok := k.Account(name); return []any{v, ok} })
		same("RCAuthFor "+name, func(k core.Core) []any { return []any{k.RCAuthFor(name)} })
		same("AccountLoggedOut "+name, func(k core.Core) []any { return []any{k.AccountLoggedOut(name)} })
		same("AccountSync "+name, func(k core.Core) []any { v, ok := k.AccountSync(name); return []any{v, ok} })
		same("AccountUsage "+name, func(k core.Core) []any { v, err := k.AccountUsage(name); return []any{v, errText(err)} })
		same("AccountEnv "+name, func(k core.Core) []any { v, err := k.AccountEnv(name); return []any{v, errText(err)} })
	}
	for _, repo := range []string{"/r", "/r/sub", "/s", "/nowhere", real, link} {
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
	model.SetWorkspacesForTest(workspace(t, "a", x))
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
	model := core.NewForTest(core.Options{})
	ws := workspace(t, "a")
	model.SetWorkspacesForTest(ws)
	id := model.WorkspaceIDForTest(ws)
	loop := core.StartForTest(model)
	c := pair(t, loop)
	c.Sync() // the snapshot

	save := func(program string) {
		t.Helper()
		s := ws.Config().Snapshot()
		s.DefaultProgram = program
		require.NoError(t, c.SaveSettings(id, s))
	}
	save("b")
	loop.DeliverForTest(core.RosterResultForTest(nil, errors.New("no roster"), time.Now()))
	save("c")
	events := c.Sync()
	var programs []string
	for _, ev := range events {
		if wc, ok := ev.(core.WorkspacesChanged); ok {
			programs = append(programs, wc.Views[0].Settings.DefaultProgram)
		}
	}
	assert.Equal(t, []string{"c"}, programs, "one WorkspacesChanged, the newest")
	_, first := events[0].(core.WorkspacesChanged)
	assert.True(t, first, "state first")
}

// TestInProcess_BeginsTheLoop: the model production serves starts its
// background work (core.Loop.Begin): its health tick fires with nothing
// else asking, and the probe's HealthChecked reaches the client. A loop
// that never began would never tick, and the TUI would see no session
// die, no diff change and no account refresh.
func TestInProcess_BeginsTheLoop(t *testing.T) {
	t.Setenv("LOOM_PANE_RENDERER", "snapshot") // the 500ms tick
	model := core.NewForTest(core.Options{})
	// Hold the GitHub poll, so nothing runs git or gh.
	model.SetGateForTest("github", true, time.Now())
	c, stop, err := InProcess(model)
	require.NoError(t, err)
	t.Cleanup(stop)

	deadline := time.After(10 * time.Second)
	for {
		for _, ev := range c.Sync() {
			if _, ok := ev.(core.HealthChecked); ok {
				return
			}
		}
		select {
		case <-c.Wakes():
		case <-deadline:
			t.Fatal("no health tick: the loop never began")
		}
	}
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

// panicky is a backend whose PersistOpenList panics.
type panicky struct{ *core.Loop }

func (panicky) PersistOpenList([]string) { panic("boom") }

// TestPanic_ReachesTheCallerAndEveryLaterCall: an in-process client raises
// the model's panic in the caller, and every call after it panics too, a
// cast and a request included, without leaving the client's lock held:
// Close waits for the reader, which needs it. A synchronous client pings
// before a local read, so it takes the request's path on every read.
func TestPanic_ReachesTheCallerAndEveryLaterCall(t *testing.T) {
	for _, synchronous := range []bool{false, true} {
		t.Run(fmt.Sprintf("synchronous=%v", synchronous), func(t *testing.T) {
			loop := core.StartForTest(core.NewForTest(core.Options{}))
			t.Cleanup(loop.Stop)
			srv := NewServer(panicky{loop})
			t.Cleanup(srv.Close)
			a, b := net.Pipe()
			srv.Serve(a)
			c, err := dial(b, synchronous, true)
			require.NoError(t, err)

			p := catch(func() { c.PersistOpenList(nil) })
			require.NotNil(t, p, "the caller panics")
			w, ok := p.(*core.WireError)
			require.True(t, ok, "with the wire's panic, got %T", p)
			assert.Equal(t, core.CodePanic, w.Code)
			assert.Contains(t, w.Message, "boom")
			assert.NotNil(t, raised(t, "a local read", func() { c.RCAuth() }), "a later local read panics")
			assert.NotNil(t, raised(t, "Sync", func() { c.Sync() }), "and Sync")
			assert.NotNil(t, raised(t, "a cast", func() { c.MarkOutput("x") }), "and a cast")
			assert.NotNil(t, raised(t, "a request", func() { c.Kill(1, 0) }), "and a request")
			within(t, 5*time.Second, "Close after a panic", c.Close)
		})
	}
}

// TestPanic_ADaemonsClientReportsIt: a daemon's client (Dial) reports the
// model's panic instead of raising it, so the TUI can quit cleanly: the
// call that met it fails with it, and Wakes tells the TUI to look.
func TestPanic_ADaemonsClientReportsIt(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(panicky{loop})
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := Dial(b)
	require.NoError(t, err)
	select {
	case <-c.Wakes():
	default:
	}

	assert.Nil(t, raised(t, "the call", func() { c.PersistOpenList(nil) }), "the caller does not panic")
	select {
	case <-c.Wakes():
	case <-time.After(5 * time.Second):
		t.Fatal("the panic did not wake the client")
	}
	w := assertLost(t, c, core.CodePanic)
	assert.Contains(t, w.Message, "boom")
	within(t, 5*time.Second, "Close after a panic", c.Close)
}

// TestInProcess_RaisesTheModelsPanic: an in-process client raises the
// model's panic in the call that meets it, so a model bug fails the test
// (or reaches the recovery) of whoever called.
func TestInProcess_RaisesTheModelsPanic(t *testing.T) {
	c, loop, stop, err := InProcessForTest(core.NewForTest(core.Options{}))
	require.NoError(t, err)
	t.Cleanup(stop)
	catch(func() { loop.DeliverForTest(core.StartResult{}) }) // a nil instance panics the model
	p := raised(t, "a local read", func() { c.RCAuth() })
	w, ok := p.(*core.WireError)
	require.True(t, ok, "the call panics with the wire's error, got %T", p)
	assert.Equal(t, core.CodePanic, w.Code)
}

// TestConnectionLost_IsFatal: a connection the client did not close is the
// model lost, and Wakes tells the TUI. A daemon's client reports it (Err);
// an in-process one raises it in the next call. A void request returning
// errClosed in silence would let the TUI run on against a model it cannot
// reach.
func TestConnectionLost_IsFatal(t *testing.T) {
	lose := func(t *testing.T, raise bool) *Client {
		loop := core.StartForTest(core.NewForTest(core.Options{}))
		t.Cleanup(loop.Stop)
		srv := NewServer(loop)
		t.Cleanup(srv.Close)
		a, b := net.Pipe()
		srv.Serve(a)
		c, err := dial(b, false, raise)
		require.NoError(t, err)
		c.Sync()
		select {
		case <-c.Wakes():
		default:
		}
		require.NoError(t, c.Err())

		a.Close() // the server's end, not the client's Close
		select {
		case <-c.Wakes():
		case <-time.After(5 * time.Second):
			t.Fatal("the loss did not wake the client")
		}
		return c
	}

	t.Run("a daemon's client reports it", func(t *testing.T) {
		c := lose(t, false)
		w := assertLost(t, c, core.CodeError)
		assert.Contains(t, w.Message, "connection to the model lost")
		within(t, 5*time.Second, "Close after the loss", c.Close)
	})
	t.Run("an in-process client raises it", func(t *testing.T) {
		c := lose(t, true)
		p := catch(func() { c.RCAuth() })
		w, ok := p.(*core.WireError)
		require.True(t, ok, "the next call panics with the wire's error, got %T", p)
		assert.Equal(t, core.CodeError, w.Code)
		assert.Contains(t, w.Message, "connection to the model lost")
		assert.NotNil(t, catch(func() { c.Kill(1, 0) }), "and so does a request")
		within(t, 5*time.Second, "Close after the loss", c.Close)
	})
}

// TestClose_IsNotALoss: a client that closes itself raises nothing after.
func TestClose_IsNotALoss(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := dial(b, false, true)
	require.NoError(t, err)
	within(t, 5*time.Second, "Close", c.Close)
	assert.Nil(t, catch(func() { c.RCAuth() }), "a read after Close is not a panic")
	assert.Nil(t, catch(func() { c.Kill(1, 0) }), "nor a request")
	assert.NoError(t, c.Err(), "nor a loss")
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
	namesTheEvent := func(t *testing.T, w *core.WireError) {
		t.Helper()
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
		c.PersistOpenList(nil) // the request's publish meets the event
		require.Eventually(t, func() bool { return c.Err() != nil }, 5*time.Second, 10*time.Millisecond)
		namesTheEvent(t, assertLost(t, c, core.CodeProtocol))
		within(t, 5*time.Second, "Close", c.Close)
	})
	t.Run("snapshot", func(t *testing.T) {
		bk := &breaker{snapshot: true}
		b, _ := serve(t, bk)
		_, err := Dial(b)
		var w *core.WireError
		require.ErrorAs(t, err, &w, "the fatal frame comes ahead of Dial's barrier: Dial fails")
		assert.Equal(t, core.CodeProtocol, w.Code)
		namesTheEvent(t, w)
	})
}

// TestHello_RefusesAnotherProtocol: a server answers a client speaking
// another protocol with its own hello, so the client learns its build,
// then a mismatch.
func TestHello_RefusesAnotherProtocol(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	srv.SetTmux("/tmp/tmux-1000/default")
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	srv.Serve(a)
	t.Cleanup(func() { b.Close() })
	require.NoError(t, json.NewEncoder(b).Encode(Frame{Hello: &Hello{Protocol: Protocol + 1}}))
	dec := json.NewDecoder(b)
	var hello, refusal Frame
	require.NoError(t, dec.Decode(&hello))
	require.NotNil(t, hello.Hello, "the server's hello comes first")
	assert.Equal(t, Protocol, hello.Hello.Protocol)
	assert.Equal(t, Self().Exe, hello.Hello.Exe)
	assert.Equal(t, "/tmp/tmux-1000/default", hello.Hello.Tmux)
	require.NoError(t, dec.Decode(&refusal))
	require.NotNil(t, refusal.Error)
	assert.Equal(t, core.CodeMismatch, refusal.Error.Code)
}

// TestDial_TheServersHello: a client learns the server's build and tmux
// server from its hello, and a server of another protocol is a
// MismatchError carrying it, so the client can still tell which is newer.
func TestDial_TheServersHello(t *testing.T) {
	t.Run("same protocol", func(t *testing.T) {
		loop := core.StartForTest(core.NewForTest(core.Options{}))
		t.Cleanup(loop.Stop)
		srv := NewServer(loop)
		srv.SetTmux("/run/user/1000/tmux-1000/default")
		t.Cleanup(srv.Close)
		a, b := net.Pipe()
		srv.Serve(a)
		c, err := Dial(b)
		require.NoError(t, err)
		t.Cleanup(c.Close)
		assert.Equal(t, "/run/user/1000/tmux-1000/default", c.Peer().Tmux)
		assert.Equal(t, SameBuild, CompareBuilds(Self(), c.Peer()), "one process, one build")
	})

	t.Run("another protocol", func(t *testing.T) {
		a, b := net.Pipe()
		t.Cleanup(func() { a.Close() })
		go func() {
			dec, enc := json.NewDecoder(a), json.NewEncoder(a)
			var hello Frame
			if dec.Decode(&hello) != nil {
				return
			}
			_ = enc.Encode(Frame{Hello: &Hello{Protocol: Protocol + 1, Version: "9.0.0"}})
			_ = enc.Encode(Frame{Error: &core.WireError{Code: core.CodeMismatch, Message: "rpc: protocol mismatch"}})
		}()
		_, err := Dial(b)
		var mm *MismatchError
		require.ErrorAs(t, err, &mm)
		require.NotNil(t, mm.Peer)
		assert.Equal(t, "9.0.0", mm.Peer.Version)
	})

	t.Run("another protocol, from before servers said hello first", func(t *testing.T) {
		a, b := net.Pipe()
		t.Cleanup(func() { a.Close() })
		go func() {
			var hello Frame
			if json.NewDecoder(a).Decode(&hello) != nil {
				return
			}
			_ = json.NewEncoder(a).Encode(Frame{Error: &core.WireError{Code: core.CodeMismatch, Message: "rpc: protocol mismatch"}})
		}()
		_, err := Dial(b)
		var mm *MismatchError
		require.ErrorAs(t, err, &mm)
		assert.Nil(t, mm.Peer)
	})
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
		p := catch(func() { _, err = c.Open(1) })
		assert.True(t, err != nil || p != nil, "a call on a closed connection fails: it returns an error or raises the loss")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a call blocked on a closed connection")
	}
}

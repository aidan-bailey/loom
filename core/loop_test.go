package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// catch runs f and returns the LoopPanic it panicked with, nil for none.
func catch(t *testing.T, f func()) (p *LoopPanic) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			lp, ok := r.(*LoopPanic)
			require.True(t, ok, "a call re-raises a *LoopPanic, got %T", r)
			p = lp
		}
	}()
	f()
	return nil
}

// waitWake waits for l's wake.
func waitWake(t *testing.T, l *Loop) {
	t.Helper()
	select {
	case <-l.Wakes():
	case <-time.After(5 * time.Second):
		t.Fatal("no wake")
	}
}

// recordDeliveries replaces l's delivery with a recorder and returns a
// reader of what it delivered, in order (each read is a call).
func recordDeliveries(l *Loop) func() []any {
	var got []any
	l.do(func(*Model) { l.deliver = func(r any) { got = append(got, r) } })
	return func() []any {
		return get(l, func(*Model) []any { return append([]any(nil), got...) })
	}
}

// TestLoop_SerializesCalls: calls from many goroutines run one at a time
// on the loop. The race detector is the check that matters here.
func TestLoop_SerializesCalls(t *testing.T) {
	l := StartForTest(NewForTest(Options{}))
	t.Cleanup(l.Stop)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.do(func(m *Model) { m.program += "x" })
		}()
	}
	wg.Wait()
	assert.Len(t, get(l, func(m *Model) string { return m.program }), 50)
}

// TestLoop_RunsJobsAndDeliversTheirResults: a job runs off the loop (the
// call that queued it returns while it still runs), and its result is
// delivered on the loop, then a wake.
func TestLoop_RunsJobsAndDeliversTheirResults(t *testing.T) {
	l := startLoop(NewForTest(Options{}), false, 0)
	t.Cleanup(l.Stop)
	delivered := recordDeliveries(l)
	release := make(chan struct{})
	l.do(func(m *Model) { m.spawn(func() any { <-release; return "done" }) })
	assert.Empty(t, delivered(), "the call returned while its job still ran")
	close(release)
	waitWake(t, l)
	assert.Equal(t, []any{"done"}, delivered())
}

// TestLoop_WakesCoalesce: however many results land before the client
// drains, one wake waits.
func TestLoop_WakesCoalesce(t *testing.T) {
	l := startLoop(NewForTest(Options{}), false, 0)
	t.Cleanup(l.Stop)
	delivered := recordDeliveries(l)
	l.do(func(m *Model) {
		for i := 0; i < 3; i++ {
			m.spawn(func() any { return i })
		}
	})
	require.Eventually(t, func() bool { return len(delivered()) == 3 }, 5*time.Second, time.Millisecond)
	waitWake(t, l)
	select {
	case <-l.Wakes():
		t.Fatal("a second wake for one drain")
	default:
	}
}

// TestLoop_SyncReturnsEventsAndTheJobsRun: Sync returns the events, not
// the jobs, which went to the runner.
func TestLoop_SyncReturnsEventsAndTheJobsRun(t *testing.T) {
	l := StartForTest(NewForTest(Options{}))
	t.Cleanup(l.Stop)
	l.do(func(m *Model) {
		m.notifyInfo("hello")
		m.spawn(func() any { return nil })
	})
	assert.Contains(t, l.Sync(), Event(Notice{Info: "hello"}))
	assert.Len(t, l.JobsForTest(), 1, "the job went to the runner, not to Sync")
	assert.NotContains(t, l.Sync(), Event(Notice{Info: "hello"}), "Sync forgets what it returned")
}

// TestLoop_SyncAndSnapshotIsOneCall: the first result is what a Sync would
// have published (a pending change), the second the whole state after it,
// and the baseline moves with them, so a Sync after finds nothing new.
func TestLoop_SyncAndSnapshotIsOneCall(t *testing.T) {
	l := StartForTest(NewForTest(Options{}))
	t.Cleanup(l.Stop)
	l.do(func(m *Model) { m.SetRCAuth(session.RemoteControlAuth{Reason: "a"}) })
	l.Sync() // the baseline
	l.do(func(m *Model) { m.SetRCAuth(session.RemoteControlAuth{Reason: "b"}) })

	reasonsOf := func(events []Event) []string {
		var out []string
		for _, ev := range events {
			if m, ok := ev.(ModelChanged); ok {
				out = append(out, m.View.RCAuth.Reason)
			}
		}
		return out
	}
	published, snapshot := l.SyncAndSnapshot()
	assert.Equal(t, []string{"b"}, reasonsOf(published), "the pending change, as Sync would have published it")
	var kinds []string
	for _, ev := range snapshot {
		kinds = append(kinds, reflect.TypeOf(ev).Name())
	}
	assert.Equal(t, []string{"WorkspacesChanged", "ModelChanged", "AccountsChanged", "GitHubChanged"}, kinds, "the whole state")
	assert.Equal(t, []string{"b"}, reasonsOf(snapshot), "as it stands after the change")
	assert.Empty(t, reasonsOf(l.Sync()), "the baseline moved with the snapshot")
}

// TestLoop_PanicInACallReachesItsCaller: the caller panics with the
// loop's stack, and every later call re-raises it.
func TestLoop_PanicInACallReachesItsCaller(t *testing.T) {
	l := StartForTest(NewForTest(Options{}))
	t.Cleanup(l.Stop)
	p := catch(t, func() { l.do(func(*Model) { panic("boom") }) })
	require.NotNil(t, p, "the caller panics")
	assert.Equal(t, "boom", p.Value)
	assert.Contains(t, p.Stack, "loop_test.go", "the stack is where it panicked")
	assert.Same(t, p, catch(t, func() { _ = l.RCAuth() }), "every later call re-raises it")
}

// TestLoop_PanicInAJobReachesTheNextCall: a job's panic wakes the client,
// whose next call re-raises it.
func TestLoop_PanicInAJobReachesTheNextCall(t *testing.T) {
	l := startLoop(NewForTest(Options{}), false, 0)
	t.Cleanup(l.Stop)
	l.do(func(m *Model) { m.spawn(func() any { panic("job boom") }) })
	waitWake(t, l)
	p := catch(t, func() { _ = l.Sync() })
	require.NotNil(t, p)
	assert.Equal(t, "job boom", p.Value)
}

// TestLoop_PanicInADeliveryReachesTheNextCall: a delivery's panic wakes
// the client, whose next call re-raises it.
func TestLoop_PanicInADeliveryReachesTheNextCall(t *testing.T) {
	l := startLoop(NewForTest(Options{}), false, 0)
	t.Cleanup(l.Stop)
	l.do(func(m *Model) {
		l.deliver = func(any) { panic("deliver boom") }
		m.spawn(func() any { return "result" })
	})
	waitWake(t, l)
	p := catch(t, func() { _ = l.Sync() })
	require.NotNil(t, p)
	assert.Equal(t, "deliver boom", p.Value)
}

// TestLoop_StopEndsCallsAndDropsLateResults: after Stop a call runs
// nothing, a late result never blocks its goroutine, and Wakes is closed.
func TestLoop_StopEndsCallsAndDropsLateResults(t *testing.T) {
	l := startLoop(NewForTest(Options{}), false, 0)
	l.do(func(m *Model) { m.SetRCAuth(session.RemoteControlAuth{Reason: "p"}) })
	l.Stop()
	l.Stop() // idempotent
	assert.Zero(t, l.RCAuth(), "a call after Stop runs nothing")
	returned := make(chan struct{})
	go func() {
		l.post(jobDone{result: "late"})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("a result posted after Stop blocked its goroutine")
	}
	for range l.Wakes() { // ends: Stop closed it
	}
}

// TestLoop_BeginArmsTheTickOnce: Begin starts the tick; a second Begin
// does not start another.
func TestLoop_BeginArmsTheTickOnce(t *testing.T) {
	l := startLoop(NewForTest(Options{}), true, time.Hour)
	t.Cleanup(l.Stop)
	l.Begin()
	l.Begin()
	assert.Equal(t, 1, get(l, func(*Model) int { return l.armed }))
}

// TestLoop_TickRearmsOnlyAfterItsProbeLands: the timer fires the model's
// tick, whose probe must land before the next tick is armed, so probes
// never overlap.
func TestLoop_TickRearmsOnlyAfterItsProbeLands(t *testing.T) {
	m := NewForTest(Options{})
	// An empty model still polls GitHub for the cwd's repository: hold that
	// poll in flight, so the probe is the tick's only job and nothing runs
	// git or gh.
	m.SetGateForTest("github", true, time.Now())
	l := startLoop(m, true, 5*time.Millisecond)
	t.Cleanup(l.Stop)
	l.do(func(*Model) { l.armTick() })
	var jobs []Job
	require.Eventually(t, func() bool {
		jobs = append(jobs, l.JobsForTest()...)
		return len(jobs) > 0
	}, 5*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond) // ten periods
	jobs = append(jobs, l.JobsForTest()...)
	require.Len(t, jobs, 1, "no second tick while the probe is out")
	probe := jobs[0]()
	require.IsType(t, HealthResult{}, probe)
	l.DeliverForTest(probe)
	require.Eventually(t, func() bool { return len(l.JobsForTest()) > 0 }, 5*time.Second, time.Millisecond,
		"the probe's landing armed the next tick")
}

// TestLoop_TickProbesTheSelection: the loop's tick passes the selection
// SetSelected stored.
func TestLoop_TickProbesTheSelection(t *testing.T) {
	l := StartForTest(NewForTest(Options{}))
	t.Cleanup(l.Stop)
	l.SetSelected(7)
	assert.Equal(t, []InstanceID{7}, get(l, (*Model).SelectedForTest))
}

// TestLoopForwardsEachMethodToItsNamesake: every method in loop_core.go
// runs its namesake on the model and nothing else (Sync runs syncEvents),
// so a copied forwarder can't call the wrong one.
func TestLoopForwardsEachMethodToItsNamesake(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "loop_core.go", nil, 0)
	require.NoError(t, err)
	pkgs := map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		pkgs[path[strings.LastIndex(path, "/")+1:]] = true
	}
	n := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		n++
		want := fn.Name.Name
		if want == "Sync" {
			want = "syncEvents"
		}
		called := map[string]bool{}
		ast.Inspect(fn.Body, func(x ast.Node) bool {
			sel, ok := x.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name == "do" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && pkgs[id.Name] {
				return true // a type: config.Workspace, session.RemoteControlAuth, …
			}
			called[sel.Sel.Name] = true
			return true
		})
		assert.Equal(t, map[string]bool{want: true}, called, "%s forwards to its namesake only", fn.Name.Name)
	}
	assert.Equal(t, reflect.TypeOf((*Core)(nil)).Elem().NumMethod(), n,
		"every Core method is a forwarder in loop_core.go")
}

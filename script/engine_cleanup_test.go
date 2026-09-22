package script

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// TestCleanupAllCoroutinesDrainsMap verifies the shutdown drain hook:
// every suspended handler coroutine gets resumed with lua.LNil so any
// deferred work (defers, finalizers) runs before the LState closes.
// Before this hook existed, coroutines parked by cs.await at exit time
// leaked — not a resource leak (process exit reaps them) but a violated
// "every coroutine gets resumed" invariant that hides real leaks as
// the engine grows.
func TestCleanupAllCoroutinesDrainsMap(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	// record increments a Go-side counter so we can observe that the
	// coroutine body actually ran past the yield point after cleanup.
	resumedCount := 0
	e.L.SetGlobal("record", e.L.NewFunction(func(L *lua.LState) int {
		resumedCount++
		return 0
	}))

	err := e.L.DoString(`
		handler = function()
			coroutine.yield(42)
			record()
		end
	`)
	require.NoError(t, err)

	fn := e.L.GetGlobal("handler").(*lua.LFunction)
	co, _ := e.L.NewThread()
	st, _, _ := e.L.Resume(co, fn)
	require.Equal(t, lua.ResumeYield, st)
	e.track(IntentID(42), co)
	require.Len(t, e.coroutines, 1)

	e.CleanupAllCoroutines()
	assert.Empty(t, e.coroutines, "coroutines must be drained after cleanup")
	assert.Equal(t, 1, resumedCount, "cleanup must resume each suspended coroutine so post-yield work runs")
}

// TestCleanupAllCoroutinesNoopOnEmpty asserts the helper is safe to
// call on a fresh engine with no suspended coroutines — the shutdown
// path always calls it, so it must not panic when the happy-path drain
// found nothing to do.
func TestCleanupAllCoroutinesNoopOnEmpty(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	assert.NotPanics(t, func() { e.CleanupAllCoroutines() })
}

// startedHost signals when a handler first calls ctx:notify, so a test
// knows the handler is running before it shuts the engine down.
type startedHost struct {
	*fakeHost
	started chan struct{}
}

func (s *startedHost) Notify(string) { close(s.started) }

// shutdownWithin runs e.Shutdown(timeout) and fails if it takes longer
// than limit.
func shutdownWithin(t *testing.T, e *Engine, timeout, limit time.Duration) {
	t.Helper()
	returned := make(chan struct{})
	go func() {
		e.Shutdown(timeout)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(limit):
		t.Fatalf("Shutdown(%v) still running after %v", timeout, limit)
	}
}

// TestShutdown_StopsRunawayHandler: a handler stuck in a pure-Lua loop
// holds e.mu forever. Shutdown must cancel the LState context so the
// loop errors out, then clean up and return within its bound.
func TestShutdown_StopsRunawayHandler(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	require.NoError(t, e.LoadFromString("loop.lua",
		`cs.bind("x", function(ctx) ctx:notify("go"); while true do end end)`))

	h := &startedHost{fakeHost: &fakeHost{}, started: make(chan struct{})}
	dispatched := make(chan error, 1)
	go func() {
		_, err := e.Dispatch(context.Background(), "x", h)
		dispatched <- err
	}()
	<-h.started

	shutdownWithin(t, e, 1500*time.Millisecond, 2*time.Second)
	select {
	case err := <-dispatched:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	case <-time.After(time.Second):
		t.Fatal("runaway handler kept running after Shutdown")
	}
	assert.Nil(t, e.L, "an engine freed by the cancel must still be closed")
}

// TestShutdown_GivesUpOnHandlerBlockedInGo: cancellation can't interrupt
// a Go call, so Shutdown must stop waiting at its bound and skip cleanup
// rather than hang quit. Once the call returns, the cancelled context
// ends the handler at its next Lua instruction.
func TestShutdown_GivesUpOnHandlerBlockedInGo(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	require.NoError(t, e.LoadFromString("slow.lua",
		`cs.bind("x", function(ctx) ctx:config_dir(); local n = 0; while true do n = n + 1 end end)`))

	h := &blockingHost{fakeHost: &fakeHost{}, entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(h.release) }) }
	defer release()

	dispatched := make(chan error, 1)
	go func() {
		_, err := e.Dispatch(context.Background(), "x", h)
		dispatched <- err
	}()
	<-h.entered

	start := time.Now()
	shutdownWithin(t, e, 400*time.Millisecond, time.Second)
	assert.GreaterOrEqual(t, time.Since(start), 400*time.Millisecond,
		"Shutdown must wait out its bound for a busy handler before giving up")

	release()
	select {
	case err := <-dispatched:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	case <-time.After(time.Second):
		t.Fatal("handler kept running after its Go call returned into a cancelled context")
	}
}

// TestShutdown_IdleEngineDrainsAndCloses: with nothing running, Shutdown
// resumes parked coroutines on a live context (so their post-yield work
// runs, as it does on the QuitIntent path) and closes the LState.
func TestShutdown_IdleEngineDrainsAndCloses(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	resumed := 0
	e.L.SetGlobal("record", e.L.NewFunction(func(*lua.LState) int {
		resumed++
		return 0
	}))
	require.NoError(t, e.LoadFromString("park.lua",
		`cs.bind("x", function() cs.actions.show_help(); record() end)`))
	_, err := e.Dispatch(context.Background(), "x", &fakeHost{})
	require.NoError(t, err)
	require.Len(t, e.coroutines, 1, "the handler must be parked on its intent")

	shutdownWithin(t, e, 1500*time.Millisecond, 500*time.Millisecond)
	assert.Equal(t, 1, resumed, "post-yield work must run on shutdown")
	assert.Empty(t, e.coroutines)
	assert.Nil(t, e.L)
}

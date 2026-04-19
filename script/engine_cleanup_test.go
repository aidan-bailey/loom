package script

import (
	"testing"

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

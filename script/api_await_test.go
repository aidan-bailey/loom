package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// TestAwaitYieldsAndResumes drives cs.await directly: a Lua coroutine
// calls test_intent() (which enqueues via fakeHost and returns an id),
// then cs.await(id) suspends the coroutine. The test then drives
// Engine.Resume as the host would and verifies the value flows back
// into cs.await's return position.
func TestAwaitYieldsAndResumes(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	h := &fakeHost{}

	// Stand-in for a cs.actions.* primitive: enqueue + return the id.
	e.L.SetGlobal("test_intent", e.L.NewFunction(func(L *lua.LState) int {
		id := h.Enqueue(QuitIntent{})
		e.lastEnqueued = id
		L.Push(lua.LNumber(id))
		return 1
	}))

	err := e.L.DoString(`
		handler = function()
			local r = cs.await(test_intent())
			return r
		end
	`)
	assert.NoError(t, err)

	co, _ := e.L.NewThread()
	fn := e.L.GetGlobal("handler").(*lua.LFunction)
	st, _, vals := e.L.Resume(co, fn)
	assert.Equal(t, lua.ResumeYield, st)
	// cs.await yields the id back so Resume can route.
	require := assert.New(t)
	require.Len(vals, 1)
	require.Equal(lua.LNumber(h.enqueuedIDs[0]), vals[0])

	// Track under the yielded id so Engine.Resume finds the coroutine.
	e.track(h.enqueuedIDs[0], co)

	// Host resumes with 7 → cs.await returns 7 → handler returns 7.
	out, err := e.Resume(h.enqueuedIDs[0], lua.LNumber(7))
	assert.NoError(t, err)
	assert.Equal(t, lua.LNumber(7), out)
}

// TestAwait_NoArg_UsesLastEnqueued drives the bare cs.await() sugar:
// a handler enqueues via a non-yielding primitive (which sets
// e.lastEnqueued), then calls cs.await() without arguments. The
// coroutine must park under the enqueued id so the host's Resume
// routes correctly.
func TestAwait_NoArg_UsesLastEnqueued(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	var h *fakeHost

	e.BeginLoad("t.lua")
	// _test_enqueue_no_yield stands in for a primitive that enqueues
	// and returns an id without yielding. The production cs.actions.*
	// always yield, but bare cs.await() is designed for primitives
	// that let control return to Lua before the await point.
	e.L.SetGlobal("_test_enqueue_no_yield", e.L.NewFunction(func(L *lua.LState) int {
		id := h.Enqueue(QuitIntent{})
		e.lastEnqueued = id
		return 0
	}))
	require.NoError(t, e.L.DoString(`
		cs.bind("x", function()
			_test_enqueue_no_yield()
			local v = cs.await()
			_G.after = v
		end)
	`))
	e.EndLoad()

	h = &fakeHost{}
	_, err := e.Dispatch("x", h)
	require.NoError(t, err)
	require.Len(t, h.enqueuedIDs, 1)

	// Coroutine must be tracked under the enqueued id, not under 0.
	_, err = e.Resume(h.enqueuedIDs[0], lua.LString("done"))
	require.NoError(t, err)
	assert.Equal(t, "done", e.L.GetGlobal("after").String())
}

// TestAwait_NoArg_NoEnqueuePending_RaisesError asserts that calling
// cs.await() with no argument and no prior enqueue raises a Lua
// error. The alternative — silently parking a coroutine under id 0 —
// leaks the coroutine and surfaces later as a confusing "no coroutine
// awaiting intent 0" error at the next Resume.
func TestAwait_NoArg_NoEnqueuePending_RaisesError(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`
		cs.bind("x", function()
			cs.await()
		end)
	`))
	e.EndLoad()

	_, err := e.Dispatch("x", &fakeHost{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no intent has been enqueued")
}

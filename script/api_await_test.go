package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

// TestAwaitNoArgsConsumesLastEnqueued verifies the documented sugar
// for cs.await(): calling it with no argument awaits the most recently
// enqueued intent on the current dispatch. The yielded id must match
// e.lastEnqueued so Engine.Resume can route the host's reply back.
// Before the fix, bare cs.await() yielded 0 and orphaned the coroutine.
func TestAwaitNoArgsConsumesLastEnqueued(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	h := &fakeHost{}

	e.L.SetGlobal("test_intent", e.L.NewFunction(func(L *lua.LState) int {
		id := h.Enqueue(QuitIntent{})
		e.lastEnqueued = id
		L.Push(lua.LNumber(id))
		return 1
	}))

	err := e.L.DoString(`
		handler = function()
			test_intent()
			local r = cs.await()
			return r
		end
	`)
	assert.NoError(t, err)

	co, _ := e.L.NewThread()
	fn := e.L.GetGlobal("handler").(*lua.LFunction)
	st, _, vals := e.L.Resume(co, fn)
	assert.Equal(t, lua.ResumeYield, st)
	assert.Len(t, vals, 1)
	assert.Equal(t, lua.LNumber(h.enqueuedIDs[0]), vals[0],
		"cs.await() with no args must yield e.lastEnqueued, not 0")
}

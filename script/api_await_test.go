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

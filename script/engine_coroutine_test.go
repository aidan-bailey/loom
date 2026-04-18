package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
	lua "github.com/yuin/gopher-lua"
)

// TestEngineResumeContinuesCoroutine drives the raw coroutine-tracking
// machinery without going through cs.await or cs.bind — those layers
// come in later tasks. A coroutine yields with an IntentID; the host
// is expected to later call Engine.Resume(id, value) and receive the
// coroutine's return value back.
func TestEngineResumeContinuesCoroutine(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()

	err := e.L.DoString(`
		co_fn = function(id)
			local v = coroutine.yield(id)
			return v + 1
		end
	`)
	assert.NoError(t, err)

	id := newIntentID()
	co, _ := e.L.NewThread()

	// First run: kick off the coroutine and let it yield with id.
	fn := e.L.GetGlobal("co_fn").(*lua.LFunction)
	st, rerr, vals := e.L.Resume(co, fn, lua.LNumber(id))
	assert.Equal(t, lua.ResumeYield, st)
	assert.NoError(t, rerr)
	assert.Equal(t, lua.LNumber(id), vals[0])

	// Register the suspended coroutine under the yielded id so the host
	// can resume it by that handle.
	e.track(id, co)

	// Resume with 41 → coroutine returns 42 and terminates.
	out, err := e.Resume(id, lua.LNumber(41))
	assert.NoError(t, err)
	assert.Equal(t, lua.LNumber(42), out)
}

package script

import (
	"context"
	"testing"

	"github.com/aidan-bailey/loom/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// track registers a bare coroutine under id, as runAction does for a
// yielded handler. Test-only: the slot has no ctx, so ResumeWithHost's
// ctx rebind is skipped. Tests that need ctx go through Dispatch (see
// TestResumeWithHostRebindsCtx).
func (e *Engine) track(id IntentID, co *lua.LState) {
	e.coroutines[id] = coroutineSlot{co: co}
}

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

// TestResumeWithHostRebindsCtx pins that a handler's ctx follows the
// resume host across a yield. The app drains each host once, right
// after its Dispatch or ResumeWithHost returns, so ctx calls after a
// yield must land on the resume host: reads see its snapshot, and
// notices and queued instances reach the buffers that get drained.
func TestResumeWithHostRebindsCtx(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`
		cs.bind("x", function(ctx)
			ctx:notify("before:" .. ctx:selected():title())
			cs.actions.show_help()
			ctx:notify("after:" .. ctx:selected():title())
			ctx:new_instance{title = "made-after"}
		end)
	`))
	e.EndLoad()

	newInst := func(title string) *session.Instance {
		inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
		require.NoError(t, err)
		return inst
	}
	dispatchHost := &fakeHost{selected: newInst("a"), repoPath: t.TempDir(), defaultProgram: "claude"}
	resumeHost := &fakeHost{selected: newInst("b"), repoPath: t.TempDir(), defaultProgram: "claude"}

	_, err := e.Dispatch(context.Background(), "x", dispatchHost)
	require.NoError(t, err)
	require.Len(t, dispatchHost.enqueuedIDs, 1, "show_help must yield on an intent")

	require.NoError(t, e.ResumeWithHost(context.Background(), dispatchHost.enqueuedIDs[0], resumeHost))

	assert.Equal(t, []string{"before:a"}, dispatchHost.notices,
		"post-resume calls must not land on the already-drained dispatch host")
	assert.Empty(t, dispatchHost.queuedInstances)
	assert.Equal(t, []string{"after:b"}, resumeHost.notices,
		"ctx:selected() after a resume reads the resume host's snapshot")
	require.Len(t, resumeHost.queuedInstances, 1)
	assert.Equal(t, "made-after", resumeHost.queuedInstances[0].Title)
}

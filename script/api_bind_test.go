package script

import (
	"bytes"
	"claude-squad/log"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

func TestCsBindRegistersAction(t *testing.T) {
	e := NewEngine(map[string]bool{"ctrl+c": true})
	defer e.Close()
	e.BeginLoad("test.lua")
	err := e.L.DoString(`cs.bind("x", function() _G.hit = 1 end, {help="xhelp"})`)
	assert.NoError(t, err)
	e.EndLoad()

	assert.True(t, e.HasAction("x"))
	reg := e.Registrations()
	require.Len(t, reg, 1)
	assert.Equal(t, "x", reg[0].Key)
	assert.Equal(t, "xhelp", reg[0].Help)
}

func TestCsBindOverridesExisting(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("x", function() _G.first = true end)`))
	// Second bind on same key replaces the first — unlike
	// cs.register_action which keeps the first.
	require.NoError(t, e.L.DoString(`cs.bind("x", function() _G.second = true end)`))
	e.EndLoad()

	_, err := e.Dispatch(context.Background(), "x", &fakeHost{})
	require.NoError(t, err)
	assert.Equal(t, "nil", e.L.GetGlobal("first").String())
	assert.Equal(t, "true", e.L.GetGlobal("second").String())
}

func TestCsUnbindRemovesBinding(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("x", function() end)`))
	require.NoError(t, e.L.DoString(`cs.unbind("x")`))
	e.EndLoad()
	assert.False(t, e.HasAction("x"))
}

// TestCsBindReservedIsDroppedWithWarning is the regression guard for
// the hard-reservation contract: cs.bind on a reserved key must NOT
// install the binding, must NOT propagate an error to the script, and
// MUST log a warning so the situation is discoverable in the log file.
// If a future refactor drops the reserved check in Engine.bind, this
// test catches it silently regressing the ctrl+c safety net.
func TestCsBindReservedIsDroppedWithWarning(t *testing.T) {
	// The warning is emitted through the Structured logger (log.For("script")),
	// so we swap log.Structured with a text-handler writing to our buffer for the
	// duration of the test. The legacy log.WarningLog writer path no longer
	// carries this record after the subsystem migration.
	var buf bytes.Buffer
	prev := log.Structured
	log.Structured = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { log.Structured = prev })

	e := NewEngine(map[string]bool{"ctrl+c": true})
	defer e.Close()
	e.BeginLoad("reserved.lua")
	// Binding to a reserved key must not error — it is silently
	// dropped so scripts that defensively attempt the bind continue
	// to load.
	require.NoError(t, e.L.DoString(`cs.bind("ctrl+c", function() _G.stole = true end)`))
	e.EndLoad()

	assert.False(t, e.HasAction("ctrl+c"), "reserved key must not be bound via cs.bind")
	for _, reg := range e.Registrations() {
		assert.NotEqual(t, "ctrl+c", reg.Key, "reserved key must not appear in Registrations()")
	}
	out := buf.String()
	assert.Contains(t, out, "reserved_key_bind_skipped", "expected warning event for reserved-key bind attempt")
	assert.Contains(t, out, "key=ctrl+c", "warning must identify the reserved key")
	assert.Contains(t, out, "reserved.lua", "warning must identify the offending script")
}

func TestCsUnbindReservedIsNoop(t *testing.T) {
	e := NewEngine(map[string]bool{"ctrl+c": true})
	defer e.Close()
	e.BeginLoad("t.lua")
	// Unbinding a reserved key must not error — scripts should be able
	// to call cs.unbind defensively without needing to know which keys
	// the engine protects.
	require.NoError(t, e.L.DoString(`cs.unbind("ctrl+c")`))
	e.EndLoad()
}

// TestCsBindCoroutineCanAwait ensures a handler registered through
// cs.bind can call cs.await without error. The engine is expected to
// wrap each run in an implicit coroutine at dispatch time.
func TestCsBindCoroutineCanAwait(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	h := &fakeHost{}
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`
		-- Stand-in primitive that enqueues and returns an id.
		cs.bind("x", function()
			local id = _test_enqueue()
			local v = cs.await(id)
			_G.after = v
		end)
	`))
	e.L.SetGlobal("_test_enqueue", e.L.NewFunction(func(L *lua.LState) int {
		id := h.Enqueue(QuitIntent{})
		L.Push(lua.LNumber(id))
		return 1
	}))
	e.EndLoad()

	_, err := e.Dispatch(context.Background(), "x", h)
	require.NoError(t, err)
	// After dispatch the coroutine should still be waiting on id.
	require.Len(t, h.enqueuedIDs, 1)
	_, err = e.Resume(h.enqueuedIDs[0], lua.LString("done"))
	require.NoError(t, err)
	assert.Equal(t, "done", e.L.GetGlobal("after").String())
}

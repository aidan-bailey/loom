package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

// TestRegisterActionIsAliasForBind verifies cs.register_action walks
// through the same internal bind path as cs.bind. A second register
// should overwrite the first — the pre-migration first-wins policy is
// gone now that users compose bindings via cs.unbind + cs.bind.
func TestRegisterActionIsAliasForBind(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	err := e.L.DoString(`
		cs.register_action{key="y", help="hello", run=function() end}
	`)
	assert.NoError(t, err)
	e.EndLoad()

	reg := e.Registrations()
	require.Len(t, reg, 1)
	assert.Equal(t, "y", reg[0].Key)
	assert.Equal(t, "hello", reg[0].Help)
}

// TestRegisterActionPreconditionGatesRun preserves the precondition
// contract: a falsy precondition must skip the run body without
// raising an error, even though the new implementation routes through
// cs.bind.
func TestRegisterActionPreconditionGatesRun(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.BeginLoad("t.lua")
	err := e.L.DoString(`
		_G.ran = false
		cs.register_action{
			key="z",
			precondition = function() return false end,
			run          = function() _G.ran = true end,
		}
	`)
	assert.NoError(t, err)
	e.EndLoad()

	matched, err := e.Dispatch("z", &fakeHost{})
	assert.True(t, matched)
	assert.NoError(t, err)
	assert.Equal(t, lua.LFalse, e.L.GetGlobal("ran"))
}

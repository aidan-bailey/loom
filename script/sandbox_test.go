package script

import (
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/stretchr/testify/assert"
)

// TestOpenSandbox_StripsEscapeHatches pins the sandbox's allow-list
// contract (docs/specs/scripting.md §security): every global gopher-lua's
// base library could use to escape the sandbox — read arbitrary files,
// execute or serialize/deserialize arbitrary code, walk the calling
// environment, or reach a library we never open in the first place — must
// come back nil after openSandbox runs. Names that openSandbox doesn't
// explicitly nil (io, os, debug, package) are covered too: they read nil
// simply because we never open those libraries, but a future change that
// opens one for a legitimate reason must not silently leave it reachable
// without this test failing.
func TestOpenSandbox_StripsEscapeHatches(t *testing.T) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	openSandbox(L)

	globals := []string{
		"dofile", "loadfile", "load", "loadstring", "require",
		"collectgarbage", "setfenv", "getfenv", "newproxy",
		"io", "os", "debug", "package",
	}
	for _, name := range globals {
		assert.Equal(t, lua.LNil, L.GetGlobal(name), "global %q must be nil after openSandbox", name)
	}

	strLib, ok := L.GetGlobal("string").(*lua.LTable)
	if assert.True(t, ok, "string library must be open") {
		assert.Equal(t, lua.LNil, strLib.RawGetString("dump"), "string.dump must be nil after openSandbox")
	}
}

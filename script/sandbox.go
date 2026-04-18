package script

import (
	lua "github.com/yuin/gopher-lua"
)

// openSandbox initializes a fresh LState with an allow-listed set of
// standard libraries and strips every function that could escape the
// sandbox. The allow-list is deliberately narrow: scripts get
// arithmetic, string/table manipulation, and coroutines — nothing
// else. File I/O, shell access, bytecode loading, and the debug/
// package libraries are never opened.
//
// Per docs/specs/scripting.md §security, this is an allow-list
// sandbox: new gopher-lua versions cannot widen the surface without
// an explicit code change here.
func openSandbox(L *lua.LState) {
	// Allow-list: base, string, table, math, coroutine.
	// The signature below matches lua.LState.OpenLibs' internal
	// format — (lua.LoadLibName, lua.OpenPackage) etc. are skipped.
	allowed := []struct {
		name string
		open lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
		{lua.CoroutineLibName, lua.OpenCoroutine},
	}
	for _, lib := range allowed {
		L.Push(L.NewFunction(lib.open))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}

	// Strip escape hatches from the base library. Each of these can
	// either execute arbitrary source (load/loadstring) or pull in a
	// file from disk (dofile/loadfile). require pulls modules through
	// package, which we never open, but nil it anyway for defense in
	// depth.
	for _, name := range []string{
		"dofile",
		"loadfile",
		"load",
		"loadstring",
		"require",
		"collectgarbage",
	} {
		L.SetGlobal(name, lua.LNil)
	}

	// string.dump lets a script serialize a function to bytecode,
	// which gopher-lua can then execute — skipping our source-only
	// load path. Strip it.
	if strLib, ok := L.GetGlobal("string").(*lua.LTable); ok {
		strLib.RawSetString("dump", lua.LNil)
	}
}

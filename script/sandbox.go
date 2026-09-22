package script

import (
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// openSandbox initializes a fresh LState with an allow-listed set of
// standard libraries, strips every function that could escape the
// sandbox, and replaces print so it can't corrupt the TUI. The
// allow-list is deliberately narrow: scripts get arithmetic,
// string/table manipulation, and coroutines — nothing else. File I/O,
// shell access, bytecode loading, and the debug/package libraries are
// never opened.
//
// Per docs/specs/scripting.md §security, this is an allow-list
// sandbox: new gopher-lua versions cannot widen the surface without
// an explicit code change here. e is used only to route the replaced
// print's output into the script log (see the end of this function);
// callers must construct it before calling openSandbox.
func openSandbox(L *lua.LState, e *Engine) {
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

	// Strip escape hatches from the base library. dofile/loadfile pull a
	// file from disk; load/loadstring execute arbitrary source; require
	// pulls modules through package, which we never open, but nil it
	// anyway for defense in depth; collectgarbage exposes GC internals
	// (count/step/etc.) with no sandboxing use. setfenv/getfenv let a
	// script read or replace another function's environment table,
	// reaching past whatever scope handed it a closure; newproxy creates
	// a bare userdata a script can attach its own metatable to, which
	// could otherwise be used to forge a type our Go-side registrations
	// treat as trusted.
	for _, name := range []string{
		"dofile",
		"loadfile",
		"load",
		"loadstring",
		"require",
		"collectgarbage",
		"setfenv",
		"getfenv",
		"newproxy",
	} {
		L.SetGlobal(name, lua.LNil)
	}

	// string.dump lets a script serialize a function to bytecode,
	// which gopher-lua can then execute — skipping our source-only
	// load path. Strip it.
	if strLib, ok := L.GetGlobal("string").(*lua.LTable); ok {
		strLib.RawSetString("dump", lua.LNil)
	}

	// The base library's print (and _printregs, its lower-level twin)
	// write straight to process stdout, which would corrupt the TUI's
	// alt-screen the moment a user script called print("debug"). Replace
	// it with a Go function that routes to the engine's script log — the
	// same sink cs.log/ctx:log use (Engine.logScript), which writes
	// straight to log.For("script") — at info level. Joins its arguments
	// with tabs and runs tostring on each (respecting __tostring
	// metamethods), exactly as Lua's own print does.
	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		top := L.GetTop()
		var b strings.Builder
		for i := 1; i <= top; i++ {
			if i > 1 {
				b.WriteByte('\t')
			}
			b.WriteString(L.ToStringMeta(L.Get(i)).String())
		}
		e.logScript("info", b.String())
		return 0
	}))
	L.SetGlobal("_printregs", lua.LNil)
}

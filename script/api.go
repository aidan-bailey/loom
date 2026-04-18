package script

import (
	"fmt"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// installAPI constructs the `cs` global table and wires every
// top-level script entry point. The table is built as a userdata-
// adjacent construct because we want methods to carry a pointer to
// the Engine without scripts being able to inspect or overwrite the
// reference.
//
// API surface:
//
//	cs.register_action{key=..., help=..., precondition=fn, run=fn}
//	cs.log(level, msg)     -- buffered, drained by app
//	cs.notify(msg)         -- routed to error/info bar
//	cs.now()               -- unix time in seconds (number)
//	cs.version()           -- app version string (informational)
//	cs.sprintf(fmt, ...)   -- alias for string.format for ergonomics
func installAPI(L *lua.LState, e *Engine) {
	cs := L.NewTable()

	cs.RawSetString("register_action", L.NewClosure(apiRegisterAction(e), L.NewUserData()))
	cs.RawSetString("log", L.NewClosure(apiLog(e), L.NewUserData()))
	cs.RawSetString("notify", L.NewClosure(apiNotify(e), L.NewUserData()))
	cs.RawSetString("now", L.NewFunction(apiNow))
	cs.RawSetString("sprintf", L.NewFunction(apiSprintf))

	L.SetGlobal("cs", cs)
}

// apiRegisterAction validates the options table and appends a new
// scriptAction to the engine. Every required field raises a Lua
// error on mismatch so script authors learn about typos at load
// time, not at dispatch.
func apiRegisterAction(e *Engine) lua.LGFunction {
	return func(L *lua.LState) int {
		opts := L.CheckTable(1)

		key := luaTableString(opts, "key", "")
		if key == "" {
			L.RaiseError("cs.register_action: key is required (string)")
		}
		help := luaTableString(opts, "help", "")

		var pre, run *lua.LFunction
		if v := opts.RawGetString("precondition"); v != lua.LNil {
			fn, ok := v.(*lua.LFunction)
			if !ok {
				L.RaiseError("cs.register_action: precondition must be a function")
			}
			pre = fn
		}
		if v := opts.RawGetString("run"); v != lua.LNil {
			fn, ok := v.(*lua.LFunction)
			if !ok {
				L.RaiseError("cs.register_action: run must be a function")
			}
			run = fn
		}
		if run == nil {
			L.RaiseError("cs.register_action: run is required (function)")
		}

		act := &scriptAction{
			key:          key,
			help:         help,
			file:         e.currentFile(),
			precondition: pre,
			run:          run,
		}
		if err := e.register(act); err != nil {
			L.RaiseError("%s", err.Error())
		}
		return 0
	}
}

// apiLog is the stand-alone form of ctx:log, available without a
// context argument so scripts can log before they've been handed a
// ctx (e.g. during load).
func apiLog(e *Engine) lua.LGFunction {
	return func(L *lua.LState) int {
		level := L.CheckString(1)
		msg := L.CheckString(2)
		e.logScript(level, msg)
		return 0
	}
}

// apiNotify routes a message to the host's error/info bar. Unlike
// ctx:notify this overload is available without a ctx argument so
// user scripts can toast a status string from hot paths without
// having to thread the context through helper functions. When called
// outside a dispatch (e.g. at load time there is no TUI yet) we
// downgrade to a log entry so the message still surfaces somewhere.
func apiNotify(e *Engine) lua.LGFunction {
	return func(L *lua.LState) int {
		msg := L.CheckString(1)
		if e.curHost != nil {
			e.curHost.Notify(msg)
			return 0
		}
		e.logScript("info", fmt.Sprintf("notify: %s", msg))
		return 0
	}
}

func apiNow(L *lua.LState) int {
	L.Push(lua.LNumber(time.Now().Unix()))
	return 1
}

// apiSprintf is a convenience alias for string.format so scripts
// don't need to chase the nested namespace for a common operation.
func apiSprintf(L *lua.LState) int {
	n := L.GetTop()
	if n == 0 {
		L.Push(lua.LString(""))
		return 1
	}
	format := L.CheckString(1)
	args := make([]interface{}, 0, n-1)
	for i := 2; i <= n; i++ {
		args = append(args, forceString(L.Get(i)))
	}
	L.Push(lua.LString(fmt.Sprintf(format, args...)))
	return 1
}

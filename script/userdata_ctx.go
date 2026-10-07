package script

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

const ctxTypeName = "cs.ctx"

// ctxState bundles the per-dispatch Host (rebound to the resume host
// each time a suspended handler resumes) plus the Engine pointer so
// ctx methods can both query host state and queue side effects back
// to the engine (e.g. pending log lines).
type ctxState struct {
	engine *Engine
	host   Host
}

func registerCtxType(L *lua.LState) {
	mt := L.NewTypeMetatable(ctxTypeName)
	idx := L.SetFuncs(L.NewTable(), ctxMethods)
	raiseReturnedErrors(L, idx, []string{"new_instance"})
	L.SetField(mt, "__index", idx)
}

// pushCtx creates a fresh ctx userdata for the given dispatch and
// returns its state too, so the engine can rebind the host when a
// suspended handler resumes (see coroutineSlot).
func pushCtx(L *lua.LState, e *Engine, h Host) (lua.LValue, *ctxState) {
	state := &ctxState{engine: e, host: h}
	ud := L.NewUserData()
	ud.Value = state
	L.SetMetatable(ud, L.GetTypeMetatable(ctxTypeName))
	return ud, state
}

func checkCtx(L *lua.LState, n int) *ctxState {
	ud := L.CheckUserData(n)
	if c, ok := ud.Value.(*ctxState); ok {
		return c
	}
	L.ArgError(n, "context expected")
	return nil
}

var ctxMethods = map[string]lua.LGFunction{
	"selected":        ctxSelected,
	"instances":       ctxInstances,
	"config_dir":      ctxConfigDir,
	"repo_path":       ctxRepoPath,
	"default_program": ctxDefaultProgram,
	"branch_prefix":   ctxBranchPrefix,
	"new_instance":    ctxNewInstance,
	"log":             ctxLog,
	"notify":          ctxNotify,
	"find":            ctxFind,
}

func ctxSelected(L *lua.LState) int {
	c := checkCtx(L, 1)
	v, ok := c.host.SelectedInstance()
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	L.Push(pushInstance(L, &v))
	return 1
}

// ctxInstances returns a Lua-indexed array (1-based) of instance
// userdata. Modifying the array in Lua does not affect the live
// list — mutations must go through per-instance methods.
func ctxInstances(L *lua.LState) int {
	c := checkCtx(L, 1)
	views := c.host.Instances()
	t := L.CreateTable(len(views), 0)
	for i := range views {
		t.Append(pushInstance(L, &views[i]))
	}
	L.Push(t)
	return 1
}

func ctxConfigDir(L *lua.LState) int {
	c := checkCtx(L, 1)
	L.Push(lua.LString(c.host.ConfigDir()))
	return 1
}

func ctxRepoPath(L *lua.LState) int {
	c := checkCtx(L, 1)
	L.Push(lua.LString(c.host.RepoPath()))
	return 1
}

func ctxDefaultProgram(L *lua.LState) int {
	c := checkCtx(L, 1)
	L.Push(lua.LString(c.host.DefaultProgram()))
	return 1
}

func ctxBranchPrefix(L *lua.LState) int {
	c := checkCtx(L, 1)
	L.Push(lua.LString(c.host.BranchPrefix()))
	return 1
}

// ctxNewInstance accepts a table with at least a `title` key and
// optional `program`, `prompt`, `branch`, and `path` fields. It asks the
// host to create the instance, unstarted, through the model
// (CreateInstanceIntent) and yields until it exists; the wrapper
// (raiseReturnedErrors) then returns the instance, or raises the error
// the host resumed it with. Like the lifecycle methods it returns its own
// errors (a bad argument, no host) for the wrapper to raise.
func ctxNewInstance(L *lua.LState) int {
	c, bad := ctxArg(L, "new_instance")
	var opts *lua.LTable
	if bad == "" {
		opts, bad = tableArg(L, 2, "new_instance")
	}
	title := ""
	if bad == "" {
		if title = luaTableString(opts, "title", ""); title == "" {
			bad = argError(2, "new_instance", "new_instance: title is required")
		}
	}
	if bad == "" && c.engine.curHost == nil {
		bad = "new_instance: no host context"
	}
	if bad == "" && !c.engine.yieldable(L) {
		bad = "new_instance: " + errNotYieldable
	}
	if bad != "" {
		L.Push(lua.LString(bad))
		return 1
	}
	program := luaTableString(opts, "program", c.host.DefaultProgram())
	path := luaTableString(opts, "path", c.host.RepoPath())
	prompt := luaTableString(opts, "prompt", "")
	branch := luaTableString(opts, "branch", "")

	return c.engine.waitIn(L, "new_instance", CreateInstanceIntent{Title: title, Program: program, Path: path, Prompt: prompt, Branch: branch})
}

// ctxArg is checkCtx (argument 1) for ctx's yielding method: the error
// comes back as the message it would raise (see argError).
func ctxArg(L *lua.LState, method string) (*ctxState, string) {
	ud, ok := L.Get(1).(*lua.LUserData)
	if !ok {
		return nil, typeError(L, 1, method, lua.LTUserData)
	}
	c, ok := ud.Value.(*ctxState)
	if !ok {
		return nil, argError(1, method, "context expected")
	}
	return c, ""
}

// tableArg is L.CheckTable for ctx's yielding method, likewise.
func tableArg(L *lua.LState, n int, method string) (*lua.LTable, string) {
	t, ok := L.Get(n).(*lua.LTable)
	if !ok {
		return nil, typeError(L, n, method, lua.LTTable)
	}
	return t, ""
}

// ctxLog routes script log output through the engine's logScript sink
// (log.For("script")) so messages appear in the app's logs/ directory
// alongside loom's own log records.
func ctxLog(L *lua.LState) int {
	c := checkCtx(L, 1)
	level := L.CheckString(2)
	msg := L.CheckString(3)
	c.engine.logScript(level, msg)
	return 0
}

func ctxNotify(L *lua.LState) int {
	c := checkCtx(L, 1)
	msg := L.CheckString(2)
	c.host.Notify(msg)
	return 0
}

// ctxFind returns the first instance whose title matches the given
// string, or nil. Convenience for scripts like "find a session by
// name and send it a keystroke".
func ctxFind(L *lua.LState) int {
	c := checkCtx(L, 1)
	needle := L.CheckString(2)
	views := c.host.Instances()
	for i := range views {
		if views[i].Title == needle {
			L.Push(pushInstance(L, &views[i]))
			return 1
		}
	}
	L.Push(lua.LNil)
	return 1
}

// luaTableString reads an optional string field from a Lua table.
// Missing fields and non-string types both fall back to def — this
// keeps the userdata API forgiving without silently masking typos in
// known keys. Scripts that want strict checks can use CheckString on
// the result themselves.
func luaTableString(t *lua.LTable, key, def string) string {
	v := t.RawGetString(key)
	if v == lua.LNil {
		return def
	}
	if s, ok := v.(lua.LString); ok {
		return string(s)
	}
	return def
}

// forceString is used by the api layer to render arbitrary LValues
// as their tostring() representation for log messages.
func forceString(v lua.LValue) string {
	if v.Type() == lua.LTNil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

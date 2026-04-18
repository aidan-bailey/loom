package script

import (
	"claude-squad/session"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

const ctxTypeName = "cs.ctx"

// ctxState bundles the per-dispatch Host plus the Engine pointer so
// ctx methods can both query live state and queue side effects back
// to the engine (e.g. pending log lines).
type ctxState struct {
	engine *Engine
	host   Host
}

func registerCtxType(L *lua.LState) {
	mt := L.NewTypeMetatable(ctxTypeName)
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), ctxMethods))
}

// pushCtx installs a fresh ctx userdata for the given dispatch. The
// engine calls this at the top of Dispatch and pops it at the end so
// that ctxState pointers never outlive a single invocation.
func pushCtx(L *lua.LState, e *Engine, h Host) lua.LValue {
	ud := L.NewUserData()
	ud.Value = &ctxState{engine: e, host: h}
	L.SetMetatable(ud, L.GetTypeMetatable(ctxTypeName))
	return ud
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
	L.Push(pushInstance(L, c.host.SelectedInstance()))
	return 1
}

// ctxInstances returns a Lua-indexed array (1-based) of instance
// userdata. Modifying the array in Lua does not affect the live
// list — mutations must go through per-instance methods.
func ctxInstances(L *lua.LState) int {
	c := checkCtx(L, 1)
	insts := c.host.Instances()
	t := L.CreateTable(len(insts), 0)
	for _, inst := range insts {
		t.Append(pushInstance(L, inst))
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
// optional `program`, `prompt`, `auto_yes`, `branch`, and `path`
// fields. It creates the instance immediately and queues it with the
// host so the main goroutine can finalize its addition to the list.
func ctxNewInstance(L *lua.LState) int {
	c := checkCtx(L, 1)
	opts := L.CheckTable(2)

	title := luaTableString(opts, "title", "")
	if title == "" {
		L.ArgError(2, "new_instance: title is required")
	}
	program := luaTableString(opts, "program", c.host.DefaultProgram())
	path := luaTableString(opts, "path", c.host.RepoPath())
	prompt := luaTableString(opts, "prompt", "")
	branch := luaTableString(opts, "branch", "")
	autoYes := luaTableBool(opts, "auto_yes", false)

	inst, err := session.NewInstance(session.InstanceOptions{
		Title:     title,
		Path:      path,
		Program:   program,
		AutoYes:   autoYes,
		Branch:    branch,
		ConfigDir: c.host.ConfigDir(),
	})
	if err != nil {
		L.RaiseError("new_instance: %s", err.Error())
		return 0
	}
	inst.Prompt = prompt
	c.host.QueueInstance(inst)
	L.Push(pushInstance(L, inst))
	return 1
}

// ctxLog routes script log output through the engine's buffered log
// channel so messages appear in the app's logs/ directory alongside
// claude-squad's own log records.
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
	for _, inst := range c.host.Instances() {
		if inst.Title == needle {
			L.Push(pushInstance(L, inst))
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

func luaTableBool(t *lua.LTable, key string, def bool) bool {
	v := t.RawGetString(key)
	if v == lua.LNil {
		return def
	}
	if b, ok := v.(lua.LBool); ok {
		return bool(b)
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

package script

import (
	"fmt"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/tmux"

	lua "github.com/yuin/gopher-lua"
)

const instanceTypeName = "cs.instance"

// lifecycleMethods are the instance methods that run through the model:
// each yields to the TUI until the model replies (lifecycleOp).
var lifecycleMethods = []string{"kill", "pause", "resume", "send_prompt"}

// registerInstanceType creates the metatable exposed as the type for
// instance userdata values, each a core.InstanceView. Scripts access it
// through the methods defined below — no direct field access. Methods
// that need the active Host (e.g. send_terminal_keys, which targets the
// UI-owned terminal pane rather than the instance's own tmux session)
// are added as closures over e so they can read e.curHost at dispatch
// time.
func registerInstanceType(L *lua.LState, e *Engine) {
	mt := L.NewTypeMetatable(instanceTypeName)
	idx := L.SetFuncs(L.NewTable(), instanceMethods)
	idx.RawSetString("send_terminal_keys", L.NewFunction(func(L *lua.LState) int {
		v := checkInstance(L, 1)
		text := L.CheckString(2)
		if e.curHost == nil {
			L.RaiseError("send_terminal_keys: no host context")
			return 0
		}
		if err := e.curHost.SendTerminalKeys(v, text); err != nil {
			L.RaiseError("send_terminal_keys: %s", err.Error())
		}
		return 0
	}))
	for _, op := range lifecycleMethods {
		idx.RawSetString(op, L.NewFunction(lifecycleOp(e, op)))
	}
	raiseReturnedErrors(L, idx, lifecycleMethods)
	L.SetField(mt, "__index", idx)
	L.SetField(mt, "__tostring", L.NewFunction(instanceToString))
}

// lifecycleOp is the yielding half of inst:kill(), :pause(), :resume() and
// :send_prompt(): it enqueues the operation for the host, which runs it
// through the model and resumes this coroutine with its outcome. Without a
// host (no dispatch) it raises.
//
// It raises through the wrapper raiseReturnedErrors installs: an error
// found here (a bad argument, no host) is returned as its message, as the
// host's error is resumed with, and the wrapper raises either at the
// script's line.
func lifecycleOp(e *Engine, op string) lua.LGFunction {
	return func(L *lua.LState) int {
		v, bad := instanceArg(L, op)
		text := ""
		if bad == "" && op == "send_prompt" {
			text, bad = stringArg(L, 2, op)
		}
		if bad == "" && e.curHost == nil {
			bad = op + ": no host context"
		}
		if bad == "" && !e.yieldable(L) {
			bad = op + ": " + errNotYieldable
		}
		if bad != "" {
			L.Push(lua.LString(bad))
			return 1
		}
		return e.waitIn(L, op, InstanceOpIntent{ID: v.ID, Title: v.Title, Op: op, Text: text})
	}
}

// errNotYieldable is why a yielding method refuses to run where it can't
// yield: gopher-lua can't yield across a Go call (pcall, xpcall, a
// table.sort comparator, a gsub callback), from a precondition, which
// runs outside the handler's coroutine, or from a coroutine the script
// made. Such a yield returns through the Go call as if the method had
// returned: the operation would run while the script went on unaware, and
// its Reply would find no coroutine to resume. So the method refuses
// before it enqueues anything.
const errNotYieldable = "cannot be called inside pcall or a callback (it waits for the TUI)"

// yieldable reports whether the Go function running on L can yield its
// handler's coroutine: L is the handler coroutine the engine is running
// (curCo), and no Go function sits between this one and the coroutine's
// entry (level 0 is this function itself).
func (e *Engine) yieldable(L *lua.LState) bool {
	if L != e.curCo {
		return false
	}
	for level := 1; level < maxYieldDepth; level++ {
		dbg, ok := L.GetStack(level)
		if !ok {
			return true
		}
		if _, err := L.GetInfo("S", dbg, lua.LNil); err != nil || dbg.What == "G" {
			return false
		}
	}
	return false
}

// maxYieldDepth bounds yieldable's walk: a stack deeper than this is
// treated as not yieldable rather than walked to its end.
const maxYieldDepth = 256

// raiseReturnedErrorsLua wraps the methods that yield to the TUI. Each
// returns an error message rather than raising it, whether it found the
// error before yielding or was resumed with it; the wrapper raises it at
// the line that called the method, so the methods keep "void (or a
// value), raise on error". It keeps its own type, error and ipairs, which
// a script may reassign.
const raiseReturnedErrorsLua = `
-- loom: these calls yield to the TUI, which resumes them with nil (done) or
-- an error message, raised here so the methods keep raising on error.
local type, error, ipairs = type, error, ipairs
local methods, names = ...
for _, name in ipairs(names) do
  local yielding = methods[name]
  methods[name] = function(...)
    local r = yielding(...)
    if type(r) == "string" then error(r, 3) end
    return r
  end
end
`

// raiseReturnedErrors installs raiseReturnedErrorsLua over the named
// methods of methods, once, when their type is registered. The chunk is a
// constant, so a failure is a bug in it.
func raiseReturnedErrors(L *lua.LState, methods *lua.LTable, names []string) {
	fn, err := L.LoadString(raiseReturnedErrorsLua)
	if err != nil {
		panic(fmt.Sprintf("script: loading the yielding-method wrapper: %v", err))
	}
	list := L.NewTable()
	for _, name := range names {
		list.Append(lua.LString(name))
	}
	if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, methods, list); err != nil {
		panic(fmt.Sprintf("script: installing the yielding-method wrapper: %v", err))
	}
}

// argError is the message L.ArgError raises for argument n of method. The
// yielding methods return their errors (raiseReturnedErrors), and raised
// from inside the wrapper L.ArgError would name the wrapper's call and
// line, not method and the script's.
func argError(n int, method, msg string) string {
	return fmt.Sprintf("bad argument #%d to %s (%s)", n, method, msg)
}

// typeError is the message L.TypeError raises, likewise.
func typeError(L *lua.LState, n int, method string, want lua.LValueType) string {
	return argError(n, method, fmt.Sprintf("%s expected, got %s", want, L.Get(n).Type()))
}

// instanceArg is checkInstance (argument 1) for the yielding methods: the
// error comes back as the message it would raise.
func instanceArg(L *lua.LState, method string) (core.InstanceView, string) {
	ud, ok := L.Get(1).(*lua.LUserData)
	if !ok {
		return core.InstanceView{}, typeError(L, 1, method, lua.LTUserData)
	}
	v, ok := ud.Value.(core.InstanceView)
	if !ok {
		return core.InstanceView{}, argError(1, method, "instance expected")
	}
	return v, ""
}

// stringArg is L.CheckString for the yielding methods, likewise.
func stringArg(L *lua.LState, n int, method string) (string, string) {
	v := L.Get(n)
	if s, ok := v.(lua.LString); ok {
		return string(s), ""
	}
	if lua.LVCanConvToString(v) {
		return L.ToString(n), ""
	}
	return "", typeError(L, n, method, lua.LTString)
}

// pushInstance wraps a copy of *v as Lua userdata with the registered
// metatable. Returns lua.LNil if v is nil so scripts can use
// `if ctx:selected() then ... end` naturally. It only builds the value;
// nothing is pushed on L's stack.
func pushInstance(L *lua.LState, v *core.InstanceView) lua.LValue {
	if v == nil {
		return lua.LNil
	}
	ud := L.NewUserData()
	ud.Value = *v
	L.SetMetatable(ud, L.GetTypeMetatable(instanceTypeName))
	return ud
}

// checkInstance extracts the core.InstanceView from argument n or
// raises a Lua error if the value is not an instance userdata.
func checkInstance(L *lua.LState, n int) core.InstanceView {
	ud := L.CheckUserData(n)
	if v, ok := ud.Value.(core.InstanceView); ok {
		return v
	}
	L.ArgError(n, "instance expected")
	return core.InstanceView{}
}

var instanceMethods = map[string]lua.LGFunction{
	"title":      instanceTitle,
	"status":     instanceStatus,
	"branch":     instanceBranch,
	"path":       instancePath,
	"program":    instanceProgram,
	"started":    instanceStarted,
	"paused":     instancePaused,
	"diff_stats": instanceDiffStats,
	"preview":    instancePreview,
	"send_keys":  instanceSendKeys,
	"tap_enter":  instanceTapEnter,
	"worktree":   instanceWorktree,
}

func instanceToString(L *lua.LState) int {
	v := checkInstance(L, 1)
	L.Push(lua.LString(fmt.Sprintf("instance(%s, %s)", v.Title, v.Status)))
	return 1
}

// instanceTitle reads the view's title. The view is a copy taken with the
// host's snapshot, so the read cannot race the model's changes on the
// main goroutine; it shows the title as it was when the dispatch or
// resume began.
func instanceTitle(L *lua.LState) int {
	v := checkInstance(L, 1)
	L.Push(lua.LString(v.Title))
	return 1
}

// instanceStatus returns the status as a lowercase string so scripts
// can compare against literals without importing a Go-side constant.
func instanceStatus(L *lua.LState) int {
	v := checkInstance(L, 1)
	L.Push(lua.LString(v.Status.String()))
	return 1
}

func instanceBranch(L *lua.LState) int {
	v := checkInstance(L, 1)
	L.Push(lua.LString(v.Branch))
	return 1
}

func instancePath(L *lua.LState) int {
	v := checkInstance(L, 1)
	L.Push(lua.LString(v.RepoPath))
	return 1
}

func instanceProgram(L *lua.LState) int {
	v := checkInstance(L, 1)
	L.Push(lua.LString(v.Program))
	return 1
}

func instanceStarted(L *lua.LState) int {
	v := checkInstance(L, 1)
	L.Push(lua.LBool(v.Started))
	return 1
}

func instancePaused(L *lua.LState) int {
	v := checkInstance(L, 1)
	L.Push(lua.LBool(v.Paused()))
	return 1
}

// instanceDiffStats returns a table {added=N, removed=N} or nil if
// stats have not been computed yet.
func instanceDiffStats(L *lua.LState) int {
	v := checkInstance(L, 1)
	if !v.HasDiff {
		L.Push(lua.LNil)
		return 1
	}
	t := L.NewTable()
	t.RawSetString("added", lua.LNumber(v.Diff.Added))
	t.RawSetString("removed", lua.LNumber(v.Diff.Removed))
	t.RawSetString("content", lua.LString(v.Diff.Content))
	L.Push(t)
	return 1
}

// paneOf is the agent's tmux session, by name (no client, no instance).
func paneOf(v core.InstanceView) *tmux.Session {
	return tmux.NewSessionNamed(v.TmuxSession, v.SessionProgram)
}

// paneUsable is the guard AgentPane put on its preview and input: a
// session that has started, is not paused, and has a tmux session name.
// The view is the dispatch-time snapshot, so a session that died since
// makes tmux fail, and the method returns or raises its error.
func paneUsable(v core.InstanceView) bool {
	return v.Started && !v.Paused() && v.TmuxSession != ""
}

func instancePreview(L *lua.LState) int {
	v := checkInstance(L, 1)
	if !paneUsable(v) {
		L.Push(lua.LString(""))
		return 1
	}
	s := paneOf(v)
	if !s.DoesSessionExist() {
		L.Push(lua.LString(""))
		return 1
	}
	out, err := s.CapturePaneContent()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LString(out))
	return 1
}

func instanceSendKeys(L *lua.LState) int {
	v := checkInstance(L, 1)
	keys := L.CheckString(2)
	if !paneUsable(v) {
		L.RaiseError("send_keys: cannot send keys to instance that has not been started or is paused")
		return 0
	}
	if err := paneOf(v).TypeText(keys); err != nil {
		L.RaiseError("send_keys: %s", err.Error())
	}
	return 0
}

func instanceTapEnter(L *lua.LState) int {
	v := checkInstance(L, 1)
	if !paneUsable(v) {
		return 0
	}
	if err := paneOf(v).PressKeys("Enter"); err != nil {
		log.For("session").Error("tap_enter_failed", "err", err)
	}
	return 0
}

// instanceWorktree returns a handle on the instance's worktree, built from
// the view (GitWorktree's persisted fields, as FromInstanceData rebuilds
// it): nil before the instance has started, and for a workspace terminal,
// which has none. The handle's methods read only its repository, worktree
// path and branch.
func instanceWorktree(L *lua.LState) int {
	v := checkInstance(L, 1)
	if !v.Started || v.IsWorkspaceTerminal || v.WorktreePath == "" {
		L.Push(lua.LNil)
		return 1
	}
	gw := git.NewGitWorktreeFromStorage(v.WorktreeRepoPath, v.WorktreePath, v.Title, v.Branch, "", false, "")
	L.Push(pushWorktree(L, gw))
	return 1
}

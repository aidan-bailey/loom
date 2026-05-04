package script

import (
	"fmt"
	"github.com/aidan-bailey/loom/session"

	lua "github.com/yuin/gopher-lua"
)

const instanceTypeName = "cs.instance"

// registerInstanceType creates the metatable exposed as the type for
// *session.Instance userdata values. Scripts access it through the
// methods defined below — no direct field access. Methods that need
// the active Host (e.g. send_terminal_keys, which targets the
// UI-owned terminal pane rather than the instance's own tmux session)
// are added as closures over e so they can read e.curHost at dispatch
// time.
func registerInstanceType(L *lua.LState, e *Engine) {
	mt := L.NewTypeMetatable(instanceTypeName)
	idx := L.SetFuncs(L.NewTable(), instanceMethods)
	idx.RawSetString("send_terminal_keys", L.NewFunction(func(L *lua.LState) int {
		inst := checkInstance(L, 1)
		text := L.CheckString(2)
		if e.curHost == nil {
			L.RaiseError("send_terminal_keys: no host context")
			return 0
		}
		if err := e.curHost.SendTerminalKeys(inst, text); err != nil {
			L.RaiseError("send_terminal_keys: %s", err.Error())
		}
		return 0
	}))
	L.SetField(mt, "__index", idx)
	L.SetField(mt, "__tostring", L.NewFunction(instanceToString))
}

// pushInstance wraps a *session.Instance as Lua userdata with the
// registered metatable. Returns lua.LNil if inst is nil so scripts
// can use `if ctx:selected() then ... end` naturally.
func pushInstance(L *lua.LState, inst *session.Instance) lua.LValue {
	if inst == nil {
		return lua.LNil
	}
	ud := L.NewUserData()
	ud.Value = inst
	L.SetMetatable(ud, L.GetTypeMetatable(instanceTypeName))
	return ud
}

// checkInstance extracts the *session.Instance from argument n or
// raises a Lua error if the value is not an instance userdata.
func checkInstance(L *lua.LState, n int) *session.Instance {
	ud := L.CheckUserData(n)
	if inst, ok := ud.Value.(*session.Instance); ok {
		return inst
	}
	L.ArgError(n, "instance expected")
	return nil
}

var instanceMethods = map[string]lua.LGFunction{
	"title":       instanceTitle,
	"status":      instanceStatus,
	"branch":      instanceBranch,
	"path":        instancePath,
	"program":     instanceProgram,
	"auto_yes":    instanceAutoYes,
	"started":     instanceStarted,
	"paused":      instancePaused,
	"diff_stats":  instanceDiffStats,
	"preview":     instancePreview,
	"send_keys":   instanceSendKeys,
	"send_prompt": instanceSendPrompt,
	"tap_enter":   instanceTapEnter,
	"pause":       instancePause,
	"resume":      instanceResume,
	"kill":        instanceKill,
	"worktree":    instanceWorktree,
}

func instanceToString(L *lua.LState) int {
	inst := checkInstance(L, 1)
	L.Push(lua.LString(fmt.Sprintf("instance(%s, %s)", inst.Title, inst.GetStatus())))
	return 1
}

// instanceTitle reads inst.Title unlocked. Title is mutable via
// Instance.SetTitle (only before the instance is started) and is not
// guarded by inst.mu, so in principle a concurrent SetTitle from the
// main goroutine could race a script read. In practice scripts dispatch
// via a tea.Cmd goroutine serialized under engine.mu, and SetTitle is
// only invoked from the title-input overlay on the main loop — the
// race is theoretical rather than practical. Worth revisiting if
// Instance ever grows a locked GetTitle accessor.
func instanceTitle(L *lua.LState) int {
	inst := checkInstance(L, 1)
	L.Push(lua.LString(inst.Title))
	return 1
}

// instanceStatus returns the status as a lowercase string so scripts
// can compare against literals without importing a Go-side constant.
func instanceStatus(L *lua.LState) int {
	inst := checkInstance(L, 1)
	L.Push(lua.LString(inst.GetStatus().String()))
	return 1
}

func instanceBranch(L *lua.LState) int {
	inst := checkInstance(L, 1)
	L.Push(lua.LString(inst.GetBranch()))
	return 1
}

func instancePath(L *lua.LState) int {
	inst := checkInstance(L, 1)
	L.Push(lua.LString(inst.Path))
	return 1
}

func instanceProgram(L *lua.LState) int {
	inst := checkInstance(L, 1)
	L.Push(lua.LString(inst.Program))
	return 1
}

func instanceAutoYes(L *lua.LState) int {
	inst := checkInstance(L, 1)
	L.Push(lua.LBool(inst.AutoYes))
	return 1
}

func instanceStarted(L *lua.LState) int {
	inst := checkInstance(L, 1)
	L.Push(lua.LBool(inst.Started()))
	return 1
}

func instancePaused(L *lua.LState) int {
	inst := checkInstance(L, 1)
	L.Push(lua.LBool(inst.Paused()))
	return 1
}

// instanceDiffStats returns a table {added=N, removed=N} or nil if
// stats have not been computed yet.
func instanceDiffStats(L *lua.LState) int {
	inst := checkInstance(L, 1)
	stats := inst.GetDiffStats()
	if stats == nil {
		L.Push(lua.LNil)
		return 1
	}
	t := L.NewTable()
	t.RawSetString("added", lua.LNumber(stats.Added))
	t.RawSetString("removed", lua.LNumber(stats.Removed))
	t.RawSetString("content", lua.LString(stats.Content))
	L.Push(t)
	return 1
}

func instancePreview(L *lua.LState) int {
	inst := checkInstance(L, 1)
	out, err := inst.Preview()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LString(out))
	return 1
}

func instanceSendKeys(L *lua.LState) int {
	inst := checkInstance(L, 1)
	keys := L.CheckString(2)
	if err := inst.SendKeys(keys); err != nil {
		L.RaiseError("send_keys: %s", err.Error())
	}
	return 0
}

func instanceSendPrompt(L *lua.LState) int {
	inst := checkInstance(L, 1)
	prompt := L.CheckString(2)
	if err := inst.SendPrompt(prompt); err != nil {
		L.RaiseError("send_prompt: %s", err.Error())
	}
	return 0
}

func instanceTapEnter(L *lua.LState) int {
	inst := checkInstance(L, 1)
	inst.TapEnter()
	return 0
}

func instancePause(L *lua.LState) int {
	inst := checkInstance(L, 1)
	// Pause takes a save-state callback; scripts don't persist on
	// their own (the engine doesn't have access to storage), so pass
	// a no-op. The app-level action path owns persistence.
	if err := inst.Pause(func() error { return nil }); err != nil {
		L.RaiseError("pause: %s", err.Error())
	}
	return 0
}

func instanceResume(L *lua.LState) int {
	inst := checkInstance(L, 1)
	if err := inst.Resume(func() error { return nil }); err != nil {
		L.RaiseError("resume: %s", err.Error())
	}
	return 0
}

func instanceKill(L *lua.LState) int {
	inst := checkInstance(L, 1)
	if err := inst.Kill(); err != nil {
		L.RaiseError("kill: %s", err.Error())
	}
	return 0
}

func instanceWorktree(L *lua.LState) int {
	inst := checkInstance(L, 1)
	gw, err := inst.GetGitWorktree()
	if err != nil || gw == nil {
		L.Push(lua.LNil)
		return 1
	}
	L.Push(pushWorktree(L, gw))
	return 1
}

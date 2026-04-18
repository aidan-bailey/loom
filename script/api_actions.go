package script

import lua "github.com/yuin/gopher-lua"

// installActions wires cs.actions.* onto the already-constructed cs
// table. The primitives split into two categories:
//
//   - Sync: execute on the dispatch goroutine by calling a Host
//     method directly (no overlay, no tea.Cmd). Safe because the
//     engine mutex serializes dispatches.
//
//   - Deferred: enqueue an Intent on the host and yield the current
//     coroutine with the resulting IntentID so cs.await routes the
//     resume back in. Any UI work that opens an overlay or produces a
//     tea.Cmd goes through this path (added in Tasks 7-9).
//
// Deferred variants installed here always call L.Yield so a handler
// that forgets cs.await still suspends cleanly — the yielded
// coroutine is simply abandoned rather than running synchronously.
func installActions(L *lua.LState, e *Engine) {
	actions := L.NewTable()

	installSyncActions(L, e, actions)

	cs, ok := L.GetGlobal("cs").(*lua.LTable)
	if !ok {
		// Should never happen — installAPI runs before installActions.
		return
	}
	cs.RawSetString("actions", actions)
}

// installSyncActions attaches the primitives that run on the dispatch
// goroutine. Each delegates to a method on the currently-dispatching
// Host, which the engine stores in curHost for the duration of
// runAction.
func installSyncActions(L *lua.LState, e *Engine, actions *lua.LTable) {
	actions.RawSetString("cursor_up", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.CursorUp()
		}
		return 0
	}))
	actions.RawSetString("cursor_down", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.CursorDown()
		}
		return 0
	}))
	actions.RawSetString("toggle_diff", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ToggleDiff()
		}
		return 0
	}))
	actions.RawSetString("workspace_prev", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.WorkspacePrev()
		}
		return 0
	}))
	actions.RawSetString("workspace_next", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.WorkspaceNext()
		}
		return 0
	}))
}

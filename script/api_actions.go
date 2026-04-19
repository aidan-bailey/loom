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
	installDeferredActions(L, e, actions)

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

	// Scroll actions — route through the active pane (diff > focused).
	actions.RawSetString("scroll_line_up", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollLineUp()
		}
		return 0
	}))
	actions.RawSetString("scroll_line_down", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollLineDown()
		}
		return 0
	}))
	actions.RawSetString("scroll_page_up", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollPageUp()
		}
		return 0
	}))
	actions.RawSetString("scroll_page_down", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollPageDown()
		}
		return 0
	}))
	actions.RawSetString("scroll_top", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollTop()
		}
		return 0
	}))
	actions.RawSetString("scroll_bottom", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollBottom()
		}
		return 0
	}))

	// Explicit terminal scroll — ignores diff-visible and focused-pane state.
	actions.RawSetString("scroll_terminal_line_up", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollTerminalLineUp()
		}
		return 0
	}))
	actions.RawSetString("scroll_terminal_line_down", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollTerminalLineDown()
		}
		return 0
	}))
	actions.RawSetString("scroll_terminal_page_up", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollTerminalPageUp()
		}
		return 0
	}))
	actions.RawSetString("scroll_terminal_page_down", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ScrollTerminalPageDown()
		}
		return 0
	}))

	// List jump actions.
	actions.RawSetString("list_page_up", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ListPageUp()
		}
		return 0
	}))
	actions.RawSetString("list_page_down", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ListPageDown()
		}
		return 0
	}))
	actions.RawSetString("list_top", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ListTop()
		}
		return 0
	}))
	actions.RawSetString("list_bottom", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ListBottom()
		}
		return 0
	}))
}

// installDeferredActions attaches primitives that can't complete on
// the dispatch goroutine — they open overlays, emit tea.Cmds, or
// otherwise need the main loop. Each enqueues an Intent on the Host,
// records the returned IntentID on e.lastEnqueued so bare cs.await()
// can consume it, and yields the running coroutine with the id so
// runAction can re-track it under that id. Even if a handler forgets
// cs.await, the yield leaves the coroutine parked cleanly rather than
// racing ahead into host-owned state.
func installDeferredActions(L *lua.LState, e *Engine, actions *lua.LTable) {
	enqueue := func(L *lua.LState, intent Intent) int {
		if e.curHost == nil {
			return 0
		}
		id := e.curHost.Enqueue(intent)
		e.lastEnqueued = id
		return L.Yield(lua.LNumber(id))
	}

	actions.RawSetString("quit", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, QuitIntent{})
	}))

	actions.RawSetString("push_selected", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, PushSelectedIntent{Confirm: optBool(L, "confirm", true)})
	}))

	actions.RawSetString("kill_selected", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, KillSelectedIntent{Confirm: optBool(L, "confirm", true)})
	}))

	actions.RawSetString("checkout_selected", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, CheckoutIntent{
			Confirm: optBool(L, "confirm", true),
			Help:    optBool(L, "help", true),
		})
	}))

	actions.RawSetString("resume_selected", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, ResumeIntent{})
	}))

	actions.RawSetString("new_instance", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, NewInstanceIntent{
			Prompt: optBool(L, "prompt", false),
			Title:  optString(L, "title", ""),
		})
	}))

	actions.RawSetString("show_help", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, ShowHelpIntent{})
	}))

	actions.RawSetString("open_workspace_picker", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, WorkspacePickerIntent{})
	}))

	actions.RawSetString("inline_attach_agent", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, InlineAttachIntent{Pane: AttachPaneAgent})
	}))
	actions.RawSetString("inline_attach_terminal", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, InlineAttachIntent{Pane: AttachPaneTerminal})
	}))
	actions.RawSetString("fullscreen_attach_agent", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, FullscreenAttachIntent{Pane: AttachPaneAgent})
	}))
	actions.RawSetString("fullscreen_attach_terminal", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, FullscreenAttachIntent{Pane: AttachPaneTerminal})
	}))
	actions.RawSetString("quick_input_agent", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, QuickInputIntent{Pane: AttachPaneAgent})
	}))
	actions.RawSetString("quick_input_terminal", L.NewFunction(func(L *lua.LState) int {
		return enqueue(L, QuickInputIntent{Pane: AttachPaneTerminal})
	}))
}

// optBool reads a boolean field from the single table argument at
// index 1. Missing table or missing field returns def. The opt-table
// lets callers write cs.actions.push_selected{confirm=false} without a
// positional-argument ordering contract.
func optBool(L *lua.LState, field string, def bool) bool {
	if L.GetTop() < 1 {
		return def
	}
	t, ok := L.Get(1).(*lua.LTable)
	if !ok {
		return def
	}
	v := t.RawGetString(field)
	if v == lua.LNil {
		return def
	}
	return lua.LVAsBool(v)
}

// optString mirrors optBool for string fields. Non-string values
// (e.g. numbers) fall back to def rather than coercing, so a script
// that accidentally passes `title=123` gets predictable behavior.
func optString(L *lua.LState, field string, def string) string {
	if L.GetTop() < 1 {
		return def
	}
	t, ok := L.Get(1).(*lua.LTable)
	if !ok {
		return def
	}
	v := t.RawGetString(field)
	s, ok := v.(lua.LString)
	if !ok {
		return def
	}
	return string(s)
}

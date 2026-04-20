// Package app is the Bubble Tea application layer: the controller
// that owns the home model, routes keyboard input, and composes the
// [ui] view components.
//
// Keyboard dispatch is two-staged: handleKeyPress in app.go selects a
// per-state handler in state_*.go, and within the default state keys
// flow through the Lua script engine via app_scripts.go's scriptHost
// adapter. The canonical keymap is script/defaults.lua, which users
// can extend or override from ~/.loom/scripts/*.lua.
//
// Concurrency: the Lua [script.Engine] is not goroutine-safe, so
// every dispatch runs under the engine's mutex via a tea.Cmd
// goroutine; the goroutine emits a scriptDoneMsg that the main loop
// consumes in handleScriptDone. Never touch Bubble Tea state from
// inside the Lua engine — queue it on scriptHost and finalize on the
// main goroutine.
package app

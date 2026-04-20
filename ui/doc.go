// Package ui holds Loom's Bubble Tea view components.
//
// The layout splits the screen into a left-hand [List] of instances
// (≈20 % width) and a right-hand [SplitPane] (≈80 %) with stacked
// Agent and Terminal panes plus a hotkey-toggled diff overlay.
// Secondary widgets — the workspace tab bar, quick-input bar, and
// menu bar — compose around that main split.
//
// Modal dialogs (text input, confirmation, branch/profile/workspace
// pickers) live in [ui/overlay]. Renderers here are pure: they read
// [session.Instance] state and emit strings; they do not mutate
// instances or spawn goroutines. Event handling happens in the app
// layer.
package ui

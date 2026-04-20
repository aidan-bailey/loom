// Package keys defines the enumerated [KeyName] values that identify
// built-in actions and the [GlobalkeyBindings] map used to render
// them in the menu bar.
//
// This package is not the dispatch authority. At runtime, key input
// is routed through [script.Engine] using the bindings declared in
// script/defaults.lua plus any user scripts under
// ~/.loom/scripts/*.lua. [KeyForString] is a reverse lookup used
// only for menu-bar highlighting, so scripts can rebind a key
// without disturbing menu rendering.
package keys

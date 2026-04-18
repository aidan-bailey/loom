package script

import (
	"claude-squad/log"
	"fmt"
	"sort"
	"sync"

	lua "github.com/yuin/gopher-lua"
)

// Engine owns the single gopher-lua state and the set of registered
// script actions. All Lua work runs under e.mu because *lua.LState is
// not goroutine-safe. Scripts themselves are invoked from tea.Cmd
// goroutines in the app layer — the mutex serializes dispatches so a
// slow script blocks only subsequent script calls, not the TUI.
type Engine struct {
	mu       sync.Mutex
	L        *lua.LState
	actions  map[string]*scriptAction
	order    []string        // insertion order for Registrations()
	loading  bool            // true only inside Load(); gates cs.register_action
	curFile  string          // script file currently being compiled (empty outside Load)
	reserved map[string]bool // raw key strings the built-in map owns

	// curHost is the Host active for the current dispatch. Set in
	// runAction, cleared on return. Read by cs.notify (standalone) so
	// a script can reach the live error-bar without holding a ctx
	// reference. Always accessed under mu, same as the rest of Engine.
	curHost Host

	// logs buffers structured log entries emitted via ctx:log() so the
	// app can drain them on its own schedule. Using the app's log
	// package directly from script land would bypass the TUI's
	// error-bar surfacing, which is why ctx:notify() exists as a
	// separate channel.
	logs []LogEntry
}

// LogEntry is a single script-emitted log record.
type LogEntry struct {
	Level   string
	Message string
}

// Registration describes an action for the help panel. Matches the
// shape the app layer expects without leaking a scriptAction pointer.
type Registration struct {
	Key  string
	Help string
}

type scriptAction struct {
	key          string
	help         string
	file         string // source file, for log output on errors
	precondition *lua.LFunction
	run          *lua.LFunction
}

// NewEngine constructs a fresh Engine. The LState is opened with the
// sandbox applied immediately so callers can never accidentally load
// a script before the sandbox is in place. reserved lists the raw
// key strings owned by the built-in keymap; scripts trying to bind
// one are rejected at load time with a warning.
func NewEngine(reserved map[string]bool) *Engine {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	openSandbox(L)

	e := &Engine{
		L:        L,
		actions:  map[string]*scriptAction{},
		reserved: reserved,
	}

	registerInstanceType(L)
	registerWorktreeType(L)
	registerCtxType(L)
	installAPI(L, e)
	return e
}

// Close releases the LState. Must be called on shutdown.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.L != nil {
		e.L.Close()
		e.L = nil
	}
}

// Load walks dir and compiles every .lua file it finds. One bad file
// never fails the whole load — errors go to the app log. Actions
// registered by prior files are preserved; a partial load is better
// than no scripts at all. Missing dir is a no-op.
func (e *Engine) Load(dir string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	loadScripts(e, dir)
}

// HasAction reports whether any script has registered for the given
// raw key string. The app layer calls this before scheduling a
// script dispatch Cmd so it can short-circuit unhandled keys without
// queuing a goroutine.
func (e *Engine) HasAction(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.actions[key]
	return ok
}

// Dispatch looks up key in the registered action map, runs the
// precondition (if any), and on pass runs the action's run function.
// Returns (matched, err). matched=false means no script owns this
// key; matched=true err=nil is a success; matched=true err!=nil is a
// runtime script error the caller should surface.
func (e *Engine) Dispatch(key string, h Host) (matched bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	act, ok := e.actions[key]
	if !ok {
		return false, nil
	}
	return true, e.runAction(act, h)
}

// runAction executes a scriptAction under the already-held engine
// mutex. It installs a ctx userdata, calls the precondition (if any),
// bails quietly when the precondition returns falsy, and otherwise
// calls run(ctx). Lua errors become Go errors; Go panics in userdata
// are recovered and wrapped.
func (e *Engine) runAction(act *scriptAction, h Host) (err error) {
	e.curHost = h
	defer func() {
		e.curHost = nil
		if r := recover(); r != nil {
			err = fmt.Errorf("script %s panic: %v", act.file, r)
			// Drain the Lua stack so subsequent dispatches see a
			// clean state.
			e.L.SetTop(0)
		}
	}()

	ctx := pushCtx(e.L, e, h)

	if act.precondition != nil {
		e.L.Push(act.precondition)
		e.L.Push(ctx)
		if err := e.L.PCall(1, 1, nil); err != nil {
			return fmt.Errorf("%s: precondition: %w", act.file, err)
		}
		res := e.L.Get(-1)
		e.L.Pop(1)
		if !lua.LVAsBool(res) {
			return nil
		}
	}

	e.L.Push(act.run)
	e.L.Push(ctx)
	if err := e.L.PCall(1, 0, nil); err != nil {
		return fmt.Errorf("%s: %w", act.file, err)
	}
	return nil
}

// register binds key to action. Rules:
//   - Collision with a built-in key ⇒ warn + skip.
//   - Collision with an already-registered script key ⇒ warn + skip
//     (first-registered wins).
//   - Called outside Load() ⇒ Lua error (scripts cannot mutate
//     bindings at runtime).
func (e *Engine) register(act *scriptAction) error {
	if !e.loading {
		return fmt.Errorf("cs.register_action can only be called at load time")
	}
	if e.reserved[act.key] {
		log.WarningLog.Printf("script %s: key %q is reserved by built-in; skipping", act.file, act.key)
		return nil
	}
	if prior, ok := e.actions[act.key]; ok {
		log.WarningLog.Printf("script %s: key %q already bound by %s; skipping", act.file, act.key, prior.file)
		return nil
	}
	e.actions[act.key] = act
	e.order = append(e.order, act.key)
	return nil
}

// Registrations returns a stable, insertion-ordered slice of the
// currently bound script actions for use by the help panel.
func (e *Engine) Registrations() []Registration {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Registration, 0, len(e.order))
	for _, key := range e.order {
		if act, ok := e.actions[key]; ok {
			out = append(out, Registration{Key: act.key, Help: act.help})
		}
	}
	return out
}

// DrainLogs returns and clears the buffered log entries emitted via
// ctx:log() since the last drain. The app layer calls this on a
// schedule to forward messages to the log package.
func (e *Engine) DrainLogs() []LogEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.logs) == 0 {
		return nil
	}
	out := e.logs
	e.logs = nil
	return out
}

// logScript buffers a script log entry. Called under the engine
// mutex because every code path that reaches it is already inside a
// Lua callback on the engine thread.
func (e *Engine) logScript(level, msg string) {
	e.logs = append(e.logs, LogEntry{Level: level, Message: msg})
}

// actionKeys returns the current action keys in a deterministic
// order. Only used by tests — production code uses Registrations.
func (e *Engine) actionKeys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.actions))
	for k := range e.actions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

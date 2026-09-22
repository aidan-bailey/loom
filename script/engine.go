package script

import (
	"context"
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Engine owns the single gopher-lua state and the set of registered
// script actions. All Lua work runs under e.mu because *lua.LState is
// not goroutine-safe. Scripts themselves are invoked from tea.Cmd
// goroutines in the app layer — the mutex serializes dispatches so a
// slow script blocks only subsequent script calls, not the TUI. The
// Update-goroutine queries (HasAction, Registrations) never take mu:
// they read bindings, which bind/unbind republish.
type Engine struct {
	mu       sync.Mutex
	L        *lua.LState
	actions  map[string]*scriptAction
	order    []string        // insertion order for Registrations()
	loading  bool            // true only inside Load(); gates cs.register_action
	curFile  string          // script file currently being compiled (empty outside Load)
	reserved map[string]bool // raw key strings the built-in map owns

	// bindings is what HasAction and Registrations read, without mu.
	// Rebuilt by publishBindingsLocked on every action-table change.
	bindings atomic.Pointer[bindingSnapshot]

	// cancel cancels the context set on L (and inherited by every
	// handler coroutine). gopher-lua checks it before each instruction,
	// so Shutdown can stop a handler stuck in a Lua loop. Set once in
	// NewEngine; safe to call from any goroutine.
	cancel context.CancelFunc

	// curHost is the Host active for the current dispatch. Set in
	// runAction, cleared on return. Read by cs.notify (standalone) so
	// a script can reach the live error-bar without holding a ctx
	// reference. Always accessed under mu, same as the rest of Engine.
	curHost Host

	// coroutines tracks suspended handler coroutines awaiting a host
	// Resume. Each slot is keyed by the IntentID the coroutine last
	// yielded; resuming re-keys under the next yielded id when the
	// coroutine awaits again. Access always under e.mu.
	coroutines map[IntentID]coroutineSlot

	// lastEnqueued records the most recent IntentID the active Lua
	// callback enqueued via the host. cs.await consumes it so scripts
	// can write `cs.await(cs.actions.quit())` without a separate id
	// return value plumbing step. Valid only during a Lua callback.
	lastEnqueued IntentID

	// logs buffers structured log entries emitted via ctx:log() so the
	// app can drain them on its own schedule. Using the app's log
	// package directly from script land would bypass the TUI's
	// error-bar surfacing, which is why ctx:notify() exists as a
	// separate channel.
	logs []LogEntry
}

// coroutineSlot holds a suspended handler thread. Stored as a struct
// rather than a bare *lua.LState so later fields (e.g. deadline) can
// be added without touching every callsite.
type coroutineSlot struct {
	co *lua.LState
	// ctx is the handler's ctx state. ResumeWithHost points it at the
	// resume host, which the app drains after the resume; the dispatch
	// host was drained when the handler first yielded.
	ctx *ctxState
}

// LogEntry is a single script-emitted log record.
type LogEntry struct {
	Level   string
	Message string
}

// bindingSnapshot is an immutable copy of the bound keys and their help
// text. Dispatch holds e.mu for a handler's whole run, so the queries the
// app makes on its Update goroutine read this instead of waiting.
type bindingSnapshot struct {
	keys map[string]struct{}
	regs []Registration
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
	// Must precede any NewThread: handler coroutines inherit a child of
	// this context. Nothing cancels it before Shutdown or Close.
	luaCtx, cancel := context.WithCancel(context.Background())
	L.SetContext(luaCtx)

	e := &Engine{
		L:          L,
		actions:    map[string]*scriptAction{},
		reserved:   reserved,
		coroutines: map[IntentID]coroutineSlot{},
		cancel:     cancel,
	}

	// openSandbox needs e to route print's replacement into the script
	// log, so e must exist first; nothing above touches L before the
	// sandbox is in place, so no unsandboxed Lua code can run in between.
	openSandbox(L, e)

	e.publishBindingsLocked()

	registerInstanceType(L, e)
	registerWorktreeType(L)
	registerCtxType(L)
	installAPI(L, e)
	installActions(L, e)
	return e
}

// errEngineClosed is returned by calls that reach the engine after
// Close, e.g. a dispatch Cmd still in flight when the app shuts down.
var errEngineClosed = errors.New("script: engine closed")

// Close releases the LState. The app shuts down through Shutdown, which
// bounds the wait for a running handler; Close waits indefinitely.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeLocked()
}

// closeLocked is Close minus the lock. Caller holds e.mu.
func (e *Engine) closeLocked() {
	e.cancel()
	if e.L != nil {
		e.L.Close()
		e.L = nil
	}
}

// Shutdown releases the engine at process exit and returns within
// timeout even if a handler is still running. It first tries the normal
// path: drain parked coroutines (so their post-yield work runs) and
// Close. If that hasn't finished by half the budget, because a handler
// holds e.mu or a drained coroutine is itself stuck, it cancels the Lua
// context, which makes Lua raise at its next instruction, and waits out
// the rest. A handler blocked inside a Go call can't be interrupted:
// then Shutdown logs engine_busy_at_shutdown and returns without
// cleaning up, and process exit reclaims everything.
func (e *Engine) Shutdown(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.mu.Lock()
		defer e.mu.Unlock()
		e.cleanupAllCoroutinesLocked()
		e.closeLocked()
	}()

	grace := time.NewTimer(timeout / 2)
	defer grace.Stop()
	select {
	case <-done:
		return
	case <-grace.C:
	}
	e.cancel()
	rest := time.NewTimer(timeout - timeout/2)
	defer rest.Stop()
	select {
	case <-done:
	case <-rest.C:
		log.For("script").Warn("engine_busy_at_shutdown", "timeout_ms", timeout.Milliseconds())
	}
}

// CleanupAllCoroutines resumes every tracked coroutine with lua.LNil
// so any deferred work (defers, finalizers, logging) runs before the
// LState closes. Shutdown runs it before closing the engine. A
// coroutine that yields again mid-drain is dropped — cleanup is
// best-effort, not a full dispatch cycle, since the TUI is already gone
// and there is no host left to service further intents. Ignored errors
// are logged.
func (e *Engine) CleanupAllCoroutines() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cleanupAllCoroutinesLocked()
}

// cleanupAllCoroutinesLocked is CleanupAllCoroutines minus the lock.
// Caller holds e.mu.
func (e *Engine) cleanupAllCoroutinesLocked() {
	if e.L == nil {
		return
	}
	for id, slot := range e.coroutines {
		delete(e.coroutines, id)
		st, rerr, _ := e.L.Resume(slot.co, nil, lua.LNil)
		if rerr != nil {
			log.For("script").Warn("cleanup_resume_failed", "intent_id", int(id), "err", rerr)
		}
		if st == lua.ResumeYield {
			log.For("script").Warn("cleanup_resume_yielded_again", "intent_id", int(id))
		}
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

// BeginLoad brackets a manual load session (tests and the embedded
// defaults loader). It acquires e.mu and flips the loading flag so
// cs.bind / cs.register_action accept registrations. Callers must
// balance every BeginLoad with an EndLoad — the mutex stays held in
// between. Nested BeginLoad calls panic rather than silently deadlock.
func (e *Engine) BeginLoad(file string) {
	e.mu.Lock()
	if e.loading {
		e.mu.Unlock()
		panic("script: BeginLoad called while already loading")
	}
	e.loading = true
	e.curFile = file
}

// EndLoad terminates a BeginLoad session.
func (e *Engine) EndLoad() {
	e.loading = false
	e.curFile = ""
	e.mu.Unlock()
}

// HasAction reports whether any script has registered for the given
// raw key string. The app layer calls this before scheduling a
// script dispatch Cmd so it can short-circuit unhandled keys without
// queuing a goroutine. Reads the published snapshot, so it never waits
// for a running handler.
func (e *Engine) HasAction(key string) bool {
	_, ok := e.bindings.Load().keys[key]
	return ok
}

// Dispatch looks up key in the registered action map, runs the
// precondition (if any), and on pass runs the action's run function.
// Returns (matched, err). matched=false means no script owns this
// key; matched=true err=nil is a success; matched=true err!=nil is a
// runtime script error the caller should surface. After Close it
// returns errEngineClosed.
//
// ctx carries an optional trace ID (see log.WithTrace) — when
// present it is emitted on every DebugKV record the handler produces,
// so the whole dispatch is greppable by one trace ID.
func (e *Engine) Dispatch(ctx context.Context, key string, h Host) (matched bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.L == nil {
		return false, errEngineClosed
	}
	act, ok := e.actions[key]
	if !ok {
		return false, nil
	}
	trace := log.TraceID(ctx)
	start := time.Now()
	log.For("script").Debug("handler.begin", "trace", trace, "key", key, "file", act.file)
	err = e.runAction(act, h)
	log.For("script").Debug("handler.end", "trace", trace, "key", key, "duration_ms", time.Since(start).Milliseconds(), "err", errString(err))
	return true, err
}

// ResumeWithHost is the host-facing entry point for continuing a
// suspended handler coroutine. It sets curHost for the duration of
// the resume so any deferred cs.actions the coroutine calls next can
// still reach a live Host, and rebinds the handler's ctx to h so ctx
// reads after the yield see h's state and ctx side effects (notify,
// new_instance) reach h rather than the already-drained dispatch
// host. The engine always resumes with lua.LNil — handlers that need
// a typed value should keep their state in closures rather than in
// await's return. Errors propagate from the underlying Resume.
//
// curHost swap and the resume itself run under a single critical
// section so a concurrent Dispatch can't observe the host slot during
// the window between "curHost = h" and the coroutine actually using
// it. The resume body runs via resumeLocked, not Resume, to avoid
// double-locking.
func (e *Engine) ResumeWithHost(ctx context.Context, id IntentID, h Host) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	prevHost := e.curHost
	e.curHost = h
	defer func() { e.curHost = prevHost }()
	if slot, ok := e.coroutines[id]; ok && slot.ctx != nil {
		slot.ctx.host = h
	}

	trace := log.TraceID(ctx)
	log.For("script").Debug("handler.resume", "trace", trace, "intent_id", int(id))
	_, err := e.resumeLocked(id, lua.LNil)
	if err != nil {
		log.For("script").Debug("handler.resume_err", "trace", trace, "intent_id", int(id), "err", err.Error())
	}
	return err
}

// errString formats err for DebugKV attributes. Returns "" for nil so
// the attribute appears as `err=` in text mode and `"err":""` in JSON,
// both of which are trivial to grep-out while still showing failed
// records inline.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Resume wakes the coroutine registered under id with value. If the
// coroutine completes, the first return value flows back. If it
// yields again (e.g. because the script chained another cs.await),
// the slot is re-tracked under the newly-yielded IntentID and Resume
// returns nil — the host should expect another incoming Enqueue call
// to have already produced that id.
func (e *Engine) Resume(id IntentID, value lua.LValue) (lua.LValue, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resumeLocked(id, value)
}

// resumeLocked is the body of Resume minus the lock. Callers must
// already hold e.mu.
func (e *Engine) resumeLocked(id IntentID, value lua.LValue) (lua.LValue, error) {
	if e.L == nil {
		return lua.LNil, errEngineClosed
	}
	slot, ok := e.coroutines[id]
	if !ok {
		return lua.LNil, fmt.Errorf("script: no coroutine awaiting intent %d", id)
	}
	delete(e.coroutines, id)

	// Clear lastEnqueued so a coroutine body that enqueues during this
	// resume leaves a fresh value behind for cs.await to consume.
	e.lastEnqueued = 0

	st, rerr, vals := e.L.Resume(slot.co, nil, value)
	switch st {
	case lua.ResumeOK:
		if len(vals) > 0 {
			return vals[0], nil
		}
		return lua.LNil, nil
	case lua.ResumeYield:
		// The coroutine awaited another intent. The yielded value is
		// the id to re-track under.
		if len(vals) == 0 {
			return lua.LNil, fmt.Errorf("script: coroutine yielded without an intent id")
		}
		next, ok := vals[0].(lua.LNumber)
		if !ok {
			return lua.LNil, fmt.Errorf("script: coroutine yielded non-numeric intent id %v", vals[0])
		}
		e.coroutines[IntentID(next)] = slot
		return lua.LNil, nil
	default:
		return lua.LNil, rerr
	}
}

// runAction executes a scriptAction under the already-held engine
// mutex. It installs a ctx userdata, calls the precondition (if any),
// bails quietly when the precondition returns falsy, and otherwise
// runs act.run inside a coroutine so cs.await can yield without
// unwinding to the host.
//
// On ResumeOK the coroutine finished synchronously. On ResumeYield the
// handler awaited a host intent; the coroutine is re-tracked under the
// yielded IntentID so Engine.Resume can continue it when the host
// posts back. Panics and Lua errors are wrapped with the source file.
func (e *Engine) runAction(act *scriptAction, h Host) (err error) {
	e.curHost = h
	defer func() {
		e.curHost = nil
		if r := recover(); r != nil {
			err = fmt.Errorf("script %s panic: %v", act.file, r)
			e.L.SetTop(0)
		}
	}()

	ctx, ctxSt := pushCtx(e.L, e, h)

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

	co, _ := e.L.NewThread()
	e.lastEnqueued = 0
	st, rerr, vals := e.L.Resume(co, act.run, ctx)
	switch st {
	case lua.ResumeOK:
		return nil
	case lua.ResumeYield:
		if len(vals) == 0 {
			return fmt.Errorf("%s: handler yielded without an intent id", act.file)
		}
		next, ok := vals[0].(lua.LNumber)
		if !ok {
			return fmt.Errorf("%s: handler yielded non-numeric intent id %v", act.file, vals[0])
		}
		e.coroutines[IntentID(next)] = coroutineSlot{co: co, ctx: ctxSt}
		return nil
	default:
		return fmt.Errorf("%s: %w", act.file, rerr)
	}
}

// bind installs act under act.key. A reserved key is rejected with a
// log warning. Unlike the legacy register() policy, an existing
// binding is overwritten — scripts are expected to compose via
// cs.unbind + cs.bind. Must be called under e.mu with e.loading true.
func (e *Engine) bind(act *scriptAction) error {
	if !e.loading {
		return fmt.Errorf("cs.bind can only be called at load time")
	}
	if e.reserved[act.key] {
		log.For("script").Warn("reserved_key_bind_skipped", "file", act.file, "key", act.key)
		return nil
	}
	if _, ok := e.actions[act.key]; !ok {
		e.order = append(e.order, act.key)
	}
	e.actions[act.key] = act
	e.publishBindingsLocked()
	return nil
}

// unbind removes key from the action map. Reserved keys are left
// alone — scripts that try to unbind a hard-reserved key (e.g.
// ctrl+c) get a log warning but no error so a defensive
// `cs.unbind("ctrl+c")` never breaks script loading.
func (e *Engine) unbind(key string) {
	if e.reserved[key] {
		log.For("script").Warn("reserved_key_unbind_skipped", "key", key)
		return
	}
	if _, ok := e.actions[key]; !ok {
		return
	}
	delete(e.actions, key)
	for i, k := range e.order {
		if k == key {
			e.order = append(e.order[:i], e.order[i+1:]...)
			break
		}
	}
	e.publishBindingsLocked()
}

// publishBindingsLocked rebuilds the snapshot HasAction and
// Registrations read. bind and unbind are the only writers of the
// action table and both call it, so every path (defaults, user
// scripts, a handler's runtime cs.unbind) republishes. Caller holds
// e.mu.
func (e *Engine) publishBindingsLocked() {
	snap := &bindingSnapshot{
		keys: make(map[string]struct{}, len(e.order)),
		regs: make([]Registration, 0, len(e.order)),
	}
	for _, key := range e.order {
		if act, ok := e.actions[key]; ok {
			snap.keys[key] = struct{}{}
			snap.regs = append(snap.regs, Registration{Key: act.key, Help: act.help})
		}
	}
	e.bindings.Store(snap)
}

// Registrations returns a stable, insertion-ordered slice of the
// currently bound script actions for use by the help panel. It copies
// the published snapshot, so it never waits for a running handler.
func (e *Engine) Registrations() []Registration {
	return append([]Registration(nil), e.bindings.Load().regs...)
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

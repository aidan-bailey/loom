package script

import (
	"context"
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/log"
	"sort"
	"strings"
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

	// curActionFile is the source file of the handler running now, set
	// for the length of runAction/resumeLocked (and a shutdown drain) so
	// runtime log lines name their file. Separate from curFile, which
	// means "being compiled"; logScript prefers curFile.
	curActionFile string

	// inFlight names the handler holding e.mu, recorded when Dispatch or
	// ResumeWithHost (or a shutdown drain) starts running one and
	// cleared when it returns. Atomic because Shutdown reads it without
	// e.mu, which the stuck handler holds, to name it in its warning.
	inFlight atomic.Pointer[inFlightAction]

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
	// callback enqueued via the host. A bare cs.await() consumes it, so
	// a primitive that enqueues without yielding can be awaited without
	// returning its id to the script. Valid only during a Lua callback.
	lastEnqueued IntentID

	// logs is a small, bounded capture of the most recent script-emitted
	// log entries (ctx:log/cs.log/print), for tests only — logScript's
	// real destination is the structured logger (log.For("script")),
	// which is where these entries actually end up in production. Nothing
	// production calls DrainLogs; capped at maxBufferedScriptLogs so it
	// can never grow without bound over a long-lived engine.
	logs []LogEntry
}

// maxBufferedScriptLogs bounds the logs slice DrainLogs reads. Only the
// most recent entries are kept; older ones are dropped as new ones
// arrive.
const maxBufferedScriptLogs = 64

// coroutineSlot holds a suspended handler thread. Stored as a struct
// rather than a bare *lua.LState so later fields (e.g. deadline) can
// be added without touching every callsite.
type coroutineSlot struct {
	co *lua.LState
	// cancel cancels co's context, a child of the engine's that
	// NewThread derived. gopher-lua cancels it itself when the coroutine
	// finishes or errors; a coroutine the engine drops while it is still
	// suspended must be cancelled through drop, or the child stays
	// registered on the engine's context until Shutdown. Nil for slots
	// tests build by hand.
	cancel context.CancelFunc
	// ctx is the handler's ctx state. ResumeWithHost points it at the
	// resume host, which the app drains after the resume; the dispatch
	// host was drained when the handler first yielded.
	ctx *ctxState
	// key and file identify the handler, for runtime log lines and
	// Shutdown's busy warning after a resume.
	key, file string
}

// drop releases a coroutine the engine abandons without finishing it.
func (s coroutineSlot) drop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// inFlightAction names a running handler for Shutdown's busy warning.
type inFlightAction struct {
	key, file string
}

// markInFlight records key/file as the handler holding e.mu and returns
// the func that clears the record. Caller holds e.mu.
func (e *Engine) markInFlight(key, file string) (unmark func()) {
	e.inFlight.Store(&inFlightAction{key: key, file: file})
	return func() { e.inFlight.Store(nil) }
}

// enterActionFile sets curActionFile for a handler run and returns the
// func that restores the previous value. Caller holds e.mu.
func (e *Engine) enterActionFile(file string) (restore func()) {
	prev := e.curActionFile
	e.curActionFile = file
	return func() { e.curActionFile = prev }
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
// then Shutdown logs engine_busy_at_shutdown, naming the stuck
// handler's key and file (inFlight), and returns without cleaning up,
// and process exit reclaims everything.
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
		attrs := []any{"timeout_ms", timeout.Milliseconds()}
		if busy := e.inFlight.Load(); busy != nil {
			attrs = append(attrs, "key", busy.key, "file", busy.file)
		}
		log.For("script").Warn("engine_busy_at_shutdown", attrs...)
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
		unmark := e.markInFlight(slot.key, slot.file)
		restoreFile := e.enterActionFile(slot.file)
		st, rerr, _ := e.L.Resume(slot.co, nil, lua.LNil)
		restoreFile()
		unmark()
		if rerr != nil {
			log.For("script").Warn("cleanup_resume_failed", "intent_id", int(id), "err", rerr)
		}
		if st == lua.ResumeYield {
			log.For("script").Warn("cleanup_resume_yielded_again", "intent_id", int(id))
			slot.drop()
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
	defer e.markInFlight(key, act.file)()
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
	if slot, ok := e.coroutines[id]; ok {
		if slot.ctx != nil {
			slot.ctx.host = h
		}
		defer e.markInFlight(slot.key, slot.file)()
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

	defer e.enterActionFile(slot.file)()
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
			slot.drop()
			return lua.LNil, fmt.Errorf("script: coroutine yielded without an intent id")
		}
		next, ok := vals[0].(lua.LNumber)
		if !ok {
			slot.drop()
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
	defer e.enterActionFile(act.file)()
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

	co, cancel := e.L.NewThread()
	slot := coroutineSlot{co: co, cancel: cancel, ctx: ctxSt, key: act.key, file: act.file}
	e.lastEnqueued = 0
	st, rerr, vals := e.L.Resume(co, act.run, ctx)
	switch st {
	case lua.ResumeOK:
		return nil
	case lua.ResumeYield:
		if len(vals) == 0 {
			slot.drop()
			return fmt.Errorf("%s: handler yielded without an intent id", act.file)
		}
		next, ok := vals[0].(lua.LNumber)
		if !ok {
			slot.drop()
			return fmt.Errorf("%s: handler yielded non-numeric intent id %v", act.file, vals[0])
		}
		e.coroutines[IntentID(next)] = slot
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

// DrainLogs returns and clears the bounded test capture of recent
// script-emitted log entries (see the logs field doc). It is NOT how
// these entries reach loom.log or the TUI — logScript writes straight to
// the structured logger for that, synchronously, every time. Nothing in
// production calls DrainLogs; it exists so tests can assert what a
// script logged without standing up a real logger sink. Do not call it
// from the app's Update loop: it takes e.mu, which a running handler can
// hold for the length of its dispatch, and calling it from Update would
// block the UI on that handler exactly the way the engine's other
// Update-goroutine queries (HasAction, Registrations) are designed to
// avoid.
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

// logScript is the single sink for every script-emitted log line —
// ctx:log, cs.log, cs.notify's host-less fallback, and the sandboxed
// print replacement all funnel through here. It writes straight to
// log.For("script") at the level requested (case-insensitively matching
// info/warn/warning/error/err/debug; anything else, including an empty
// or unrecognized string, logs at info), tagging the record with the
// source file: the one being compiled (e.curFile, set only inside a
// Load call) or else the one whose handler is running (e.curActionFile,
// set for a dispatch or resume). The structured logger is goroutine-safe and cheap, so calling
// it here — under e.mu, since every caller already reached this from
// inside a Lua callback on the engine thread — is fine; it does not
// block on the app or the TUI the way routing through a Cmd would.
//
// It also appends to the bounded capture DrainLogs reads (see the logs
// field doc) purely so tests can assert on what was logged.
func (e *Engine) logScript(level, msg string) {
	logger := log.For("script")
	var attrs []any
	if e.curFile != "" {
		attrs = []any{"file", e.curFile}
	} else if e.curActionFile != "" {
		attrs = []any{"file", e.curActionFile}
	}
	switch strings.ToLower(level) {
	case "warn", "warning":
		logger.Warn(msg, attrs...)
	case "error", "err":
		logger.Error(msg, attrs...)
	case "debug":
		logger.Debug(msg, attrs...)
	default:
		logger.Info(msg, attrs...)
	}

	e.logs = append(e.logs, LogEntry{Level: level, Message: msg})
	if len(e.logs) > maxBufferedScriptLogs {
		e.logs = e.logs[len(e.logs)-maxBufferedScriptLogs:]
	}
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

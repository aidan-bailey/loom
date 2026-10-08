# Daemon Stage 1E: the Model's Own Loop — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `core.Model` runs on a goroutine of its own. It runs its own jobs, delivers their results, ticks its own health tick, and wakes the TUI when it has something to drain. The TUI's calls become synchronous round trips over that goroutine, and `core.Core` loses its last non-value methods (`Deliver`, and the `Jobs` in `Sync`'s result).

**Architecture:**
- **`core.Loop`** (new) is the concurrency shell around an unchanged, single-goroutine `Model`.
  - It serves every `Core` method as a round trip over its goroutine.
  - It starts the jobs the model queues on goroutines of their own and delivers their results on the loop.
  - It fires the model's health tick from its own timer.
  - It forwards a panic to the caller's goroutine, so the TUI's Bubble Tea recovery still restores the terminal.
  - It signals a coalesced wake after anything it did unprompted.
- **`Loop` implements `Core`; `Model` no longer does.** Core's tests keep driving the `Model` directly, as today.
- **The TUI forwards wakes into the program.** A goroutine started in `Run` turns each wake into a `coreWakeMsg`. Update's existing drain (`drainCore`) then applies the events. `coreCmd` and `coreResultMsg` are deleted.

**Tech Stack:** Go 1.25 (generics for the round-trip helpers), Bubble Tea v2, testify, go/ast for the forwarding check.

---

## Where this stage fits

The daemon spec is `docs/superpowers/specs/2026-10-03-loom-daemon-design.md`, Rollout stage 1. What has landed so far:
- **1A** split the panes.
- **1B** extracted `core.Model`.
- **1C** drew the instance boundary.
- **1D** drew the workspace boundary. `core.Core` is value-typed except for `Sync`'s jobs and `Deliver` (1D plan, decision 14).

What's left:
- **1E (this plan)** puts the model on its own loop.
- **Stage 2** swaps the in-process round trips for a codec over `net.Pipe`. Its transport must keep the calls synchronous, or revisit the read-after-write sites (decision 12).
- **Stage 3** is the daemon process.

## Decisions

1. **Scope.** The model runs on its own goroutine (`core.Loop`), runs its own jobs, ticks its own health tick, and wakes the TUI with a coalesced message. The TUI's calls stay synchronous round trips. No socket, no codec, no process split.
2. **The tick moves with the model (the user's choice, 2026-10-08: "put it in the daemon").**
   - The loop's timer fires the model's half of the health tick: the probe and the gated jobs, as `Model.Tick` does today.
   - The period is `tickInterval()`: 3s in event mode, 500ms on the snapshot path, the cadence `tickUpdateMetadataCmd` has today.
   - `Begin` arms the first tick. Each later tick is armed when the previous probe's `HealthResult` is delivered, so probes still never overlap.
   - The TUI keeps its own tick for its own half: toast expiry, pane-client prune, the inline-attach backstop, the snapshot scan and the workbench scan. It now re-arms itself.
   - `HealthChecked` stays as an event, which the TUI uses only for the workbench diff refresh.
   - The probe's selected instance (the one whose full diff it refreshes) reaches the model as a request, `SetSelected(id)`. `Update` sends it whenever the selection moved (`publishSelection`).
3. **Calls are serialized one at a time, not per Update.**
   - Each `Core` call is its own round trip. A job's result may therefore land between two calls the TUI makes in one Update, which could not happen before.
   - This is the daemon's semantics: several clients, one request at a time. It was recommended to the user on 2026-10-08, alongside the tick question. They decided the tick and left this to the recommendation.
   - The alternative, holding the loop for a whole Update, would be a lock that stage 2 has to take apart.
   - The TUI reads its own cached stores (slot views, `slot.info`) everywhere except a few live queries. `admit` re-validates every request. The audit (B6) records each multi-call site and why it is safe.
4. **Layering.** `Model` stays a synchronous, single-goroutine object.
   - It keeps `Sync() Out`, `Drain`, `Deliver` and `Tick`, so core's tests are untouched.
   - `Loop` (`core/loop.go`) owns it. Its `Core` methods (`core/loop_core.go`) each forward to their namesake on the loop's goroutine; `TestLoopForwardsEachMethodToItsNamesake` checks that by AST.
   - `var _ Core = (*Loop)(nil)` replaces `var _ Core = (*Model)(nil)`.
5. **Jobs.**
   - After every step (a call, a result, a tick), the loop takes the model's queued jobs (`takeJobs`) and starts each on a goroutine of its own.
   - A job's result is posted to the loop's inbox and delivered on the loop (`Model.Deliver`). A nil result is dropped.
   - Jobs now start during the TUI's Update, not after it. Nothing depends on the old timing: a job reads no model state, and its effects reach the TUI only through events, which still apply at the drain.
6. **Wake.**
   - `Loop.Wakes()` is a buffered channel of one. The loop signals it, without blocking, after every inbox item: a result, a tick, a job's panic. Calls don't signal, because the TUI drains at the end of its own Update.
   - `Run` starts `forwardWakes`, which `p.Send`s a `coreWakeMsg` per wake. It runs on its own goroutine because `Send` blocks until Update takes the message, and the model's goroutine must never wait on the TUI. That is the lesson of the bell deadlock (CLAUDE.md's x/vt callbacks gotcha).
   - **Behaviour change:** the model keeps ticking and applying results while the TUI's event loop is blocked (a full-screen attach, `$EDITOR`, `claude auth login`). Before, it stalled with it. Its events wait, coalesced into one wake.
7. **Panics.**
   - Bubble Tea recovers a panic in `Update` and in its Cmd goroutines, and restores the terminal. A panic on the loop's goroutine or a job's goroutine would kill the process with the terminal left raw. So the loop recovers every panic into a `*LoopPanic`, which holds the value and the panicking goroutine's stack.
   - A panic in a call is re-raised on the caller's goroutine. A panic in a job or a delivery is kept, and the loop wakes the TUI.
   - Every call from then on re-panics with it, so the TUI's next drain panics inside Update, where Bubble Tea restores the terminal. After a panic the model's state is unknown, so nothing runs on it again.
8. **Stop.**
   - `Loop.Stop()` waits for the step in progress, then ends the loop. Later results are dropped, as Bubble Tea dropped a Cmd's result after quit, and `Wakes` is closed, which ends `forwardWakes`.
   - A call after `Stop` runs nothing, returns zero values and logs `loop.call_after_stop`. `Run` defers `stopCore`.
   - The quit's save stays in `SaveForQuit`. A result that lands between it and `Stop` is applied normally, including any save it makes, which is newer.
9. **`core.Core` after 1E:**
   - `Sync() []Event` (the jobs are already running);
   - `Deliver` removed;
   - `Tick` removed (the loop ticks);
   - `SetSelected(id InstanceID)` added.

   `TestCoreIsValueTyped` loses its exemptions. `TestTUIHoldsNoModelObject` adds `core.Loop`, `core.Job` and `core.Out`: the TUI holds a `Core` and handles no jobs.
10. **Test seams.** All live in `core/seams.go` and are named `…ForTest`.
    - `StartForTest(m)` gives a loop that keeps every job (`JobsForTest`) and never ticks on its own (`TickForTest`). Between calls its goroutine is idle, so a test may also reach the model directly (`ModelForTest`), with channel operations ordering its accesses for the race detector.
    - `DeliverForTest(result)` delivers on the loop and returns once it is applied.
    - `SelectedForTest()` reads what `SetSelected` stored.
    - App's tests install hold-mode loops everywhere: `startModel = core.StartForTest` in `TestMain`, `testLoop(t, model)` in fixtures. The production loop's wiring is covered by `TestRealLoop_…`, e2e and the smoke run.
11. **What does not change:** `session`, the model's operations and completions, the events (apart from `HealthChecked`'s role), `state.json`, and the pane event paths (`MarkOutput`, `PaneOutput`, `PaneQuiet`, `VerifyDead` are calls as before). Hook scans still ride pane events plus the model's tick; spec assumption 4 stays open for stage 3.
12. **What 1E leaves for stage 2 and later:**
    - **Read-after-write sites.** These keep their meaning because calls stay synchronous: `syncViews`, `syncWorkspaces`, the drains after `OpenTab`/`EnterGlobal`/`RestoreSaved`/`LoadClassic`/`ReloadAccounts`, the nested drain in `newLaunchOptionsOverlay`, and `mutateUIPrefs` keeping its own write. Stage 2's transport must keep them synchronous or revisit them.
    - **The merge dirty check** is git I/O on Update, done by the TUI, not the model. It stays.
    - **The selection** is one value. Stage 3's multi-client daemon needs one per client.

## Out of scope

- The codec, the socket and `net.Pipe` (stage 2), and the daemon process (stage 3).
- Loading every registered workspace (stage 3).
- The pollable PTY (its own follow-up stage, the user's choice).
- The 1C and 1D follow-ups (send-hold gaps, the shell leak, `cs.actions` under pcall, a busy session's silent refusal, a new workspace's missing `config.json`).

## Packages

| Pkg | Delivers | Commit |
|---|---|---|
| **A** | Core, additive: `core.Loop` (round trips, jobs, deliveries, wake, tick, panics, Stop), its forwarders, `Model.SetSelected`/`syncEvents`/`takeJobs`, `tickInterval`, the loop's test seams and tests | `feat(core): the model's own loop (core.Loop)` |
| **B** | The switch: `Core` narrows and `Loop` implements it; the TUI holds a loop, forwards wakes, splits its tick, publishes its selection; `coreCmd`/`coreResultMsg` deleted; test plumbing over held jobs; enforcement; the multi-call audit | `refactor(core,app): the model runs on its own loop` |
| **C** | CLAUDE.md, the spec, full verification, a sandbox smoke run, the outcome | `docs: CLAUDE.md and spec for the model loop (daemon stage 1E)` |

## Conventions for every package

- **Build and test.** Work from the repo root. Build with `CGO_ENABLED=0`. For race runs, use `CC=clang CGO_ENABLED=1 go test -race …`; this stage is about concurrency, so `-race` on `./core` and `./app` is part of every package's verification. Format with `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') <new files>`, never `gofmt -w .`. Run `go vet`, not golangci-lint.
- **Git.** Never use `git stash`. Stay on the branch. Never run `./loom` (use `go run ./tools/loomdev`). No test may reach the developer's tmux server or `~/.loom`. Never write the word `exec` immediately followed by `(` in any file (a security hook rejects it).
- **Moved and renamed code.** Keep each comment and update only the names it mentions.
- **Assertions.** Never weaken or delete one. Get the count with `git grep -h 'assert\.\|require\.' -- '*_test.go' ':!vendor' | wc -l` (8586 at d071973). A conversion that keeps an assertion's meaning is fine. List every assertion that moved (B3's health-tick re-arm moves to core) or whose wording changed.
- **The plan is a first draft.** Where the code differs from what it assumes, adapt minimally and record the deviation. If a difference needs a design decision, stop and ask.
- **Tests run in the foreground** with a generous timeout. A background run can fail to wake you.
- **Commit trailers.** Commits end with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_016BYqFKpSvoVwusd7gJdYPr
  ```
- **The loop's one rule:** nothing runs on the loop's goroutine except inside a step, and nothing on it ever waits on the TUI or calls the loop (a call from inside a call deadlocks).

## File structure

| File | Package | Responsibility |
|---|---|---|
| `core/loop.go` (new) | A | `Loop`, `Start`, `startLoop`, `run`, `step`, `handle`, jobs, `post`, `signal`, `Wakes`, `Stop`, `Begin`, `armTick`, `LoopPanic`, the `do`/`get`/`get2` helpers |
| `core/loop_core.go` (new) | A | Every other `Core` method of `Loop`, each forwarding to its namesake |
| `core/loop_test.go` (new) | A | Loop mechanics, the forwarding check |
| `core/model.go` | A, B | `selected`, `SetSelected`, `takeJobs`; B: doc, `var _ Core` moves |
| `core/views.go` | A | `syncEvents`; `Sync` built on it and `takeJobs` |
| `core/tick.go` | A | `tickInterval`; comments |
| `core/events.go` | B | `HealthChecked`'s doc |
| `core/seams.go` | A | `StartForTest`, `ModelForTest`, `JobsForTest`, `DeliverForTest`, `TickForTest`, `SelectedForTest` |
| `core/iface.go` | B | The narrowed `Core` |
| `core/value_boundary_test.go` | B | No exemptions |
| `internal/testenv/instance_enforce_test.go` | B | `Loop`, `Job`, `Out` flagged |
| `app/core_glue.go` | B | `coreWakeMsg`, `forwardWakes`, `drainCore` over events, `publishSelection`, `HealthChecked` applier |
| `app/app.go` | B | `home` fields, `Update`, the `coreWakeMsg` case, the TUI's tick |
| `app/app_init.go` | B | `startModel`, `newHome`, `Run` |
| `app/testcore_test.go`, `app/app_test.go` and the converted tests | B | Test plumbing over held jobs |
| `app/core_glue_test.go` (new) | B | Wake forwarding, selection, the TUI tick, a production loop end to end |
| `CLAUDE.md`, the daemon spec, this plan | C | Docs, outcome |

---

## Package A: the model's own loop (core, additive)

Nothing in app changes. `Model` still implements today's `Core`, and `Loop` sits beside it. One commit at the end.

### A1. Model hooks for the loop

**Files:** `core/model.go`, `core/views.go`, `core/tick.go`

- [ ] **Step 1: `selected`, `SetSelected`, `takeJobs`.** In `core/model.go`, add a field to `Model` after `rcAuth`:

```go
	// selected is the instance whose full diff the health tick's probe
	// refreshes: the TUI's selected row (SetSelected), 0 for none.
	selected InstanceID
```

and these methods after `spawn`:

```go
// SetSelected names the instance whose full diff the health tick's probe
// refreshes: the TUI's selected row, 0 for none. The loop's tick reads it
// (Loop).
func (m *Model) SetSelected(id InstanceID) { m.selected = id }

// takeJobs returns the jobs queued since the last take, and forgets them.
// The loop starts them after every step.
func (m *Model) takeJobs() []Job {
	jobs := m.out.Jobs
	m.out.Jobs = nil
	return jobs
}
```

- [ ] **Step 2: `syncEvents`.** In `core/views.go`, replace `Sync` with:

```go
// syncEvents publishes the workspace views that changed (WorkspacesChanged,
// first), then the instance views that changed (ViewsChanged), and returns
// them ahead of every other event produced since the last call, which it
// forgets. It leaves the jobs: the loop starts those after every step.
func (m *Model) syncEvents() []Event {
	published := append(m.publishWorkspaces(), m.publishViews()...)
	events := m.out.Events
	m.out.Events = nil
	return append(published, events...)
}

// Sync is syncEvents plus the jobs queued since the last call: everything
// the model produced, for a caller that runs its jobs itself (core's own
// tests; until stage 1E, the TUI). The loop (Loop.Sync) uses syncEvents.
func (m *Model) Sync() Out {
	return Out{Events: m.syncEvents(), Jobs: m.takeJobs()}
}
```

This is the same result as before: the published views, then `Drain`'s events, then its jobs.

- [ ] **Step 3: `tickInterval`.** In `core/tick.go`, add `"time"` to the imports, and add after `maxWorkspaceTerminalRestartFailures`:

```go
// tickInterval is the health tick's period: a slow belt-and-braces sweep
// in event mode (the emulator path), where status rides pane events, and
// the legacy 500ms on the snapshot path. The loop arms the next tick this
// long after the previous probe lands (Loop.armTick). Formerly the sleep
// in app's tickUpdateMetadataCmd, which keeps the same cadence for the
// TUI's own half.
func tickInterval() time.Duration {
	if tmux.EmulatorEnabled() {
		return 3 * time.Second
	}
	return 500 * time.Millisecond
}
```

In `maxWorkspaceTerminalRestartFailures`'s comment, change "tick cadence — 500ms tickUpdateMetadataCmd below, so ~1.5s of thrash" to "tick cadence — 500ms on the snapshot path (tickInterval), so ~1.5s of thrash".

- [ ] **Step 4:** `CGO_ENABLED=0 go test ./core/` passes unchanged.

### A2. `core.Loop`

**Files:** Create `core/loop.go`, `core/loop_core.go`. Modify `core/seams.go`.

- [ ] **Step 1: `core/loop.go`.** Write it in full:

```go
package core

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// Loop runs a Model on a goroutine of its own (daemon stage 1E). It
// serves every Core method as a round trip over that goroutine, starts the
// jobs the model queues on goroutines of their own and delivers their
// results on the loop, fires the model's health tick from its own timer,
// and signals Wakes when something it did unprompted (a result, a tick)
// may have produced events for its client to drain (Sync). It implements
// Core; nothing else touches its model.
//
// Nothing runs on the loop's goroutine except inside a step, and nothing
// on it waits on its client or calls the loop: a call made from inside a
// call deadlocks.
type Loop struct {
	m *Model

	calls  chan call
	inbox  chan any
	wake   chan struct{}
	done   chan struct{}
	exited chan struct{}
	stop   sync.Once

	// hold keeps each job for the test to run (StartForTest: JobsForTest)
	// instead of running it on a goroutine of its own.
	hold   bool
	heldMu sync.Mutex
	held   []Job

	// interval is the health tick's period; 0 never ticks on its own
	// (StartForTest: TickForTest).
	interval time.Duration

	// The loop goroutine's own state.

	// deliver applies a job's result: the model's Deliver, which core's
	// own tests replace to reach a panic in a delivery.
	deliver func(any)
	// fatal is the panic that stopped the model (see LoopPanic).
	fatal *LoopPanic
	// began is set by the first Begin, which arms the tick.
	began bool
	// armed counts the ticks armed (armTick), for tests.
	armed int
}

// call is one round trip: f runs on the loop, and its panic, if any, goes
// back on done.
type call struct {
	f    func(*Model)
	done chan *LoopPanic
}

// jobDone is a job's result, posted by the goroutine that ran it.
type jobDone struct{ result any }

// jobPanicked is a job's panic, posted by the goroutine that ran it.
type jobPanicked struct{ p *LoopPanic }

// tickDue is the health tick's timer firing.
type tickDue struct{}

// LoopPanic is a panic on the model's goroutine (in a call, a delivery or
// the tick) or in one of its jobs. The loop re-raises it on the goroutine
// of the call it happened in, or of the next call after it, so the
// caller's own recovery sees it: the TUI's Bubble Tea restores the
// terminal, which a panic on a goroutine of the loop's own would leave in
// raw mode. The model's state is unknown after one, so every later call
// re-raises it too, and nothing runs on the model again.
type LoopPanic struct {
	// Value is what was panicked with.
	Value any
	// Stack is the panicking goroutine's stack.
	Stack string
}

func (p *LoopPanic) Error() string {
	return fmt.Sprintf("core: panic on the model's goroutine: %v\n\n%s", p.Value, p.Stack)
}

// newLoopPanic records a recovered value with the stack of the goroutine
// that panicked; a LoopPanic passes through unchanged.
func newLoopPanic(v any) *LoopPanic {
	if p, ok := v.(*LoopPanic); ok {
		return p
	}
	return &LoopPanic{Value: v, Stack: string(debug.Stack())}
}

// Start runs m on a goroutine of its own and returns the loop serving it.
// From here on only the loop touches m. The health tick starts with Begin;
// Stop ends the loop.
func Start(m *Model) *Loop { return startLoop(m, false, tickInterval()) }

// startLoop starts a loop over m: hold keeps its jobs for the test, and
// interval is the tick's period (0: no tick of its own).
func startLoop(m *Model, hold bool, interval time.Duration) *Loop {
	l := &Loop{
		m:        m,
		calls:    make(chan call),
		inbox:    make(chan any),
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
		exited:   make(chan struct{}),
		hold:     hold,
		interval: interval,
	}
	l.deliver = m.Deliver
	go l.run()
	return l
}

// run is the loop's goroutine: one step at a time, a call or an inbox
// item (a job's result or panic, a tick), until Stop.
func (l *Loop) run() {
	defer close(l.exited)
	for {
		select {
		case <-l.done:
			return
		case c := <-l.calls:
			c.done <- l.step(func() { c.f(l.m) })
		case msg := <-l.inbox:
			l.step(func() { l.handle(msg) })
			l.signal()
		}
	}
}

// step runs f on the loop, then starts the jobs it queued. It returns the
// panic f raised, or one an earlier step raised (fatal): after a panic the
// model's state is unknown, so f does not run and no job starts.
func (l *Loop) step(f func()) (p *LoopPanic) {
	if l.fatal != nil {
		return l.fatal
	}
	defer func() {
		if r := recover(); r != nil {
			l.fatal = newLoopPanic(r)
			p = l.fatal
		}
	}()
	f()
	l.startJobs()
	return nil
}

// handle applies one inbox item.
func (l *Loop) handle(msg any) {
	switch msg := msg.(type) {
	case jobDone:
		l.deliverResult(msg.result)
	case jobPanicked:
		panic(msg.p)
	case tickDue:
		l.m.Tick(l.m.selected)
	}
}

// deliverResult applies a job's result. The health probe's result arms the
// next tick, so a tick never overlaps a probe still running.
func (l *Loop) deliverResult(result any) {
	l.deliver(result)
	if _, ok := result.(HealthResult); ok {
		l.armTick()
	}
}

// armTick fires the next tick one interval from now; a loop without an
// interval never ticks on its own.
func (l *Loop) armTick() {
	if l.interval <= 0 {
		return
	}
	l.armed++
	time.AfterFunc(l.interval, func() { l.post(tickDue{}) })
}

// startJobs starts every job the model queued: each on a goroutine of its
// own, or kept for the test (hold).
func (l *Loop) startJobs() {
	for _, j := range l.m.takeJobs() {
		if l.hold {
			l.heldMu.Lock()
			l.held = append(l.held, j)
			l.heldMu.Unlock()
			continue
		}
		go l.runJob(j)
	}
}

// runJob runs j off the loop and posts its result (none for nil), or its
// panic, to the loop.
func (l *Loop) runJob(j Job) {
	defer func() {
		if r := recover(); r != nil {
			l.post(jobPanicked{p: newLoopPanic(r)})
		}
	}()
	if result := j(); result != nil {
		l.post(jobDone{result: result})
	}
}

// post hands msg to the loop. After Stop it is dropped, so the goroutine
// posting it (a job's, the timer's) never blocks.
func (l *Loop) post(msg any) {
	select {
	case l.inbox <- msg:
	case <-l.done:
	}
}

// signal wakes the client: at most one wake waits, however many signals
// come before its next receive.
func (l *Loop) signal() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// Wakes says the loop did something unprompted (a job's result landed,
// the tick fired, a job panicked), so its client should drain (Sync).
// Wakes coalesce: one waits, however many happened since the last
// receive. Stop closes it.
func (l *Loop) Wakes() <-chan struct{} { return l.wake }

// Stop ends the loop. It waits for the step in progress, then drops every
// later result (as Bubble Tea dropped a Cmd's result after quit) and
// closes Wakes. A call after Stop runs nothing and returns zero values.
// Stop is idempotent; never call it from the loop's goroutine.
func (l *Loop) Stop() {
	l.stop.Do(func() {
		close(l.done)
		<-l.exited
		close(l.wake)
	})
}

// Begin starts the background jobs the client's first frame wants
// (Model.Begin) and, the first time, the health tick.
func (l *Loop) Begin() {
	l.do(func(m *Model) {
		m.Begin()
		if !l.began {
			l.began = true
			l.armTick()
		}
	})
}

// do runs f on the loop and waits for it. A panic in f, or one an earlier
// step raised, is re-raised here. After Stop, f does not run.
func (l *Loop) do(f func(*Model)) {
	c := call{f: f, done: make(chan *LoopPanic, 1)}
	select {
	case l.calls <- c:
	case <-l.done:
		log.For("core").Warn("loop.call_after_stop")
		return
	}
	if p := <-c.done; p != nil {
		panic(p)
	}
}

// get runs f on the loop and returns its result.
func get[T any](l *Loop, f func(*Model) T) T {
	var v T
	l.do(func(m *Model) { v = f(m) })
	return v
}

// get2 runs f on the loop and returns its two results.
func get2[A, B any](l *Loop, f func(*Model) (A, B)) (A, B) {
	var a A
	var b B
	l.do(func(m *Model) { a, b = f(m) })
	return a, b
}
```

- [ ] **Step 2: `core/loop_core.go`.** Write it in full. Each method runs its namesake and nothing else, except `Sync`, which runs `syncEvents`. `Tick` and `Deliver` have no forwarder: the loop ticks and delivers itself.

```go
package core

import (
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
)

// The Core methods of Loop, but Begin (loop.go): each runs its namesake on
// the model, on the loop's goroutine, and returns its results to the
// caller (TestLoopForwardsEachMethodToItsNamesake). Sync runs syncEvents,
// since the loop starts the jobs itself.

func (l *Loop) Sync() []Event { return get(l, (*Model).syncEvents) }

func (l *Loop) LoadClassic(sweepTmux bool) error {
	return get(l, func(m *Model) error { return m.LoadClassic(sweepTmux) })
}
func (l *Loop) InitAccounts()                         { l.do((*Model).InitAccounts) }
func (l *Loop) SetRCAuth(a session.RemoteControlAuth) { l.do(func(m *Model) { m.SetRCAuth(a) }) }

func (l *Loop) StayGlobal() { l.do((*Model).StayGlobal) }
func (l *Loop) RestoreSaved(saved []config.Workspace) int {
	return get(l, func(m *Model) int { return m.RestoreSaved(saved) })
}
func (l *Loop) RestoreFailed() []string { return get(l, (*Model).RestoreFailed) }
func (l *Loop) KeepRestoreFailed(desired map[string]bool) {
	l.do(func(m *Model) { m.KeepRestoreFailed(desired) })
}
func (l *Loop) OpenNames() []string { return get(l, (*Model).OpenNames) }
func (l *Loop) PersistOpenList()    { l.do((*Model).PersistOpenList) }
func (l *Loop) Register(name, dir string) (config.Workspace, error) {
	return get2(l, func(m *Model) (config.Workspace, error) { return m.Register(name, dir) })
}
func (l *Loop) SetLastUsed(name string) error {
	return get(l, func(m *Model) error { return m.SetLastUsed(name) })
}
func (l *Loop) SaveForQuit() error { return get(l, (*Model).SaveForQuit) }

func (l *Loop) Workspace(id WorkspaceID) (WorkspaceView, bool) {
	return get2(l, func(m *Model) (WorkspaceView, bool) { return m.Workspace(id) })
}
func (l *Loop) Classic() (WorkspaceView, bool) { return get2(l, (*Model).Classic) }
func (l *Loop) Tabs() []WorkspaceView          { return get(l, (*Model).Tabs) }
func (l *Loop) IsLoaded(id WorkspaceID) bool {
	return get(l, func(m *Model) bool { return m.IsLoaded(id) })
}
func (l *Loop) OpenTab(def config.Workspace) (WorkspaceView, error) {
	return get2(l, func(m *Model) (WorkspaceView, error) { return m.OpenTab(def) })
}
func (l *Loop) CloseTab(name string) error {
	return get(l, func(m *Model) error { return m.CloseTab(name) })
}
func (l *Loop) EnterGlobal(focused WorkspaceID) (WorkspaceView, error) {
	return get2(l, func(m *Model) (WorkspaceView, error) { return m.EnterGlobal(focused) })
}
func (l *Loop) Save(id WorkspaceID) error {
	return get(l, func(m *Model) error { return m.Save(id) })
}
func (l *Loop) Registry() RegistryView { return get(l, (*Model).Registry) }
func (l *Loop) ReloadRegistry() error  { return get(l, (*Model).ReloadRegistry) }
func (l *Loop) SaveSettings(id WorkspaceID, s config.Settings) error {
	return get(l, func(m *Model) error { return m.SaveSettings(id, s) })
}
func (l *Loop) SetUIPrefs(id WorkspaceID, p config.UIPrefs) error {
	return get(l, func(m *Model) error { return m.SetUIPrefs(id, p) })
}
func (l *Loop) SetHelpScreensSeen(id WorkspaceID, seen uint32) error {
	return get(l, func(m *Model) error { return m.SetHelpScreensSeen(id, seen) })
}

func (l *Loop) Views(id WorkspaceID) []InstanceView {
	return get(l, func(m *Model) []InstanceView { return m.Views(id) })
}
func (l *Loop) View(id InstanceID) (InstanceView, bool) {
	return get2(l, func(m *Model) (InstanceView, bool) { return m.View(id) })
}
func (l *Loop) Create(id WorkspaceID, spec NewInstance, req ReqID) {
	l.do(func(m *Model) { m.Create(id, spec, req) })
}
func (l *Loop) Kill(id InstanceID, req ReqID)   { l.do(func(m *Model) { m.Kill(id, req) }) }
func (l *Loop) Pause(id InstanceID, req ReqID)  { l.do(func(m *Model) { m.Pause(id, req) }) }
func (l *Loop) Resume(id InstanceID, req ReqID) { l.do(func(m *Model) { m.Resume(id, req) }) }
func (l *Loop) ResumeWith(id InstanceID, opts launch.Options, base string, req ReqID) {
	l.do(func(m *Model) { m.ResumeWith(id, opts, base, req) })
}
func (l *Loop) Recover(id InstanceID, req ReqID) { l.do(func(m *Model) { m.Recover(id, req) }) }
func (l *Loop) Merge(target, source InstanceID, req ReqID) {
	l.do(func(m *Model) { m.Merge(target, source, req) })
}
func (l *Loop) Push(id InstanceID, req ReqID) { l.do(func(m *Model) { m.Push(id, req) }) }
func (l *Loop) SendPrompt(id InstanceID, text string, req ReqID) {
	l.do(func(m *Model) { m.SendPrompt(id, text, req) })
}
func (l *Loop) FetchIssue(repo string, n int, req ReqID) {
	l.do(func(m *Model) { m.FetchIssue(repo, n, req) })
}

func (l *Loop) SetSelected(id InstanceID)     { l.do(func(m *Model) { m.SetSelected(id) }) }
func (l *Loop) MarkOutput(sessionName string) { l.do(func(m *Model) { m.MarkOutput(sessionName) }) }
func (l *Loop) PaneOutput(id InstanceID)      { l.do(func(m *Model) { m.PaneOutput(id) }) }
func (l *Loop) PaneQuiet(id InstanceID)       { l.do(func(m *Model) { m.PaneQuiet(id) }) }
func (l *Loop) VerifyDead(id InstanceID)      { l.do(func(m *Model) { m.VerifyDead(id) }) }

func (l *Loop) Program() string                   { return get(l, (*Model).Program) }
func (l *Loop) SetProgram(p string)               { l.do(func(m *Model) { m.SetProgram(p) }) }
func (l *Loop) RCAuth() session.RemoteControlAuth { return get(l, (*Model).RCAuth) }

func (l *Loop) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	return get2(l, func(m *Model) (github.Snapshot, bool) { return m.GitHubSnapshot(repo) })
}
func (l *Loop) GitHubErr(repo string) error {
	return get(l, func(m *Model) error { return m.GitHubErr(repo) })
}
func (l *Loop) GitHubUnavailable() bool         { return get(l, (*Model).GitHubUnavailable) }
func (l *Loop) GitHubUnavailableReason() string { return get(l, (*Model).GitHubUnavailableReason) }
func (l *Loop) ExpediteGitHub()                 { l.do((*Model).ExpediteGitHub) }

func (l *Loop) AccountNames() AccountNames { return get(l, (*Model).AccountNames) }
func (l *Loop) AccountsLoaded() bool       { return get(l, (*Model).AccountsLoaded) }
func (l *Loop) HasExtraAccounts() bool     { return get(l, (*Model).HasExtraAccounts) }
func (l *Loop) Account(name string) (account.Account, bool) {
	return get2(l, func(m *Model) (account.Account, bool) { return m.Account(name) })
}
func (l *Loop) RCAuthFor(acct string) session.RemoteControlAuth {
	return get(l, func(m *Model) session.RemoteControlAuth { return m.RCAuthFor(acct) })
}
func (l *Loop) AccountLoggedOut(acct string) bool {
	return get(l, func(m *Model) bool { return m.AccountLoggedOut(acct) })
}
func (l *Loop) AccountSync(name string) (account.SyncReport, bool) {
	return get2(l, func(m *Model) (account.SyncReport, bool) { return m.AccountSync(name) })
}
func (l *Loop) AccountUsage(name string) (account.Usage, error) {
	return get2(l, func(m *Model) (account.Usage, error) { return m.AccountUsage(name) })
}
func (l *Loop) AccountEnv(name string) ([]string, error) {
	return get2(l, func(m *Model) ([]string, error) { return m.AccountEnv(name) })
}
func (l *Loop) ClaudeProgram() string { return get(l, (*Model).ClaudeProgram) }
func (l *Loop) ReloadAccounts()       { l.do((*Model).ReloadAccounts) }
func (l *Loop) RequestAccountsRefresh(withDefault bool) {
	l.do(func(m *Model) { m.RequestAccountsRefresh(withDefault) })
}
func (l *Loop) RequestUsageProbe() { l.do((*Model).RequestUsageProbe) }
func (l *Loop) AddAccount(name string) (string, error) {
	return get2(l, func(m *Model) (string, error) { return m.AddAccount(name) })
}
func (l *Loop) RemoveAccount(name string) error {
	return get(l, func(m *Model) error { return m.RemoveAccount(name) })
}
func (l *Loop) SetDefaultAccount(name string) error {
	return get(l, func(m *Model) error { return m.SetDefaultAccount(name) })
}
```

If a method expression doesn't type-check (say a `Model` method returns something the `Core` signature doesn't), the `Core` signature wins: write the closure form and record the deviation.

- [ ] **Step 3: Seams.** Append to `core/seams.go`:

```go
// StartForTest runs m on a loop that keeps every job for the test to run
// (JobsForTest, then DeliverForTest) and never ticks on its own
// (TickForTest). Between calls its goroutine is idle, so the test may
// also reach m directly (ModelForTest): the loop's channel operations
// order those accesses for the race detector.
func StartForTest(m *Model) *Loop { return startLoop(m, true, 0) }

// ModelForTest returns the loop's model, for its seams. Only a loop that
// is idle between calls (StartForTest, or a production loop with nothing
// in flight) may be reached this way.
func (l *Loop) ModelForTest() *Model { return l.m }

// JobsForTest starts what the model has queued (a seam called on it
// directly may have queued jobs no step has taken yet), then takes the
// jobs a StartForTest loop kept, in the order queued.
func (l *Loop) JobsForTest() []Job {
	l.do(func(*Model) {})
	l.heldMu.Lock()
	defer l.heldMu.Unlock()
	jobs := l.held
	l.held = nil
	return jobs
}

// DeliverForTest delivers a job's result on the loop, as the goroutine
// that ran the job would (the health probe's result arming the next
// tick), and returns once it is applied. Its client is not woken.
func (l *Loop) DeliverForTest(result any) {
	l.do(func(*Model) { l.deliverResult(result) })
}

// TickForTest runs the model's health tick on the loop, as its timer
// would.
func (l *Loop) TickForTest() {
	l.do(func(m *Model) { m.Tick(m.selected) })
}

// SelectedForTest returns the instance the probe refreshes the full diff
// of (SetSelected).
func (m *Model) SelectedForTest() InstanceID { return m.selected }
```

`JobsForTest`'s empty call works because every step ends in `startJobs`.

### A3. Loop tests

**Files:** Create `core/loop_test.go`

- [ ] **Step 1: Write the tests.** The package is `core`, white-box.

```go
package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// catch runs f and returns the LoopPanic it panicked with, nil for none.
func catch(t *testing.T, f func()) (p *LoopPanic) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			lp, ok := r.(*LoopPanic)
			require.True(t, ok, "a call re-raises a *LoopPanic, got %T", r)
			p = lp
		}
	}()
	f()
	return nil
}

// waitWake waits for l's wake.
func waitWake(t *testing.T, l *Loop) {
	t.Helper()
	select {
	case <-l.Wakes():
	case <-time.After(5 * time.Second):
		t.Fatal("no wake")
	}
}

// recordDeliveries replaces l's delivery with a recorder and returns a
// reader of what it delivered, in order (each read is a call).
func recordDeliveries(l *Loop) func() []any {
	var got []any
	l.do(func(*Model) { l.deliver = func(r any) { got = append(got, r) } })
	return func() []any {
		return get(l, func(*Model) []any { return append([]any(nil), got...) })
	}
}

// TestLoop_SerializesCalls: calls from many goroutines run one at a time
// on the loop. The race detector is the check that matters here.
func TestLoop_SerializesCalls(t *testing.T) {
	l := StartForTest(NewForTest(Options{}))
	t.Cleanup(l.Stop)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.do(func(m *Model) { m.program += "x" })
		}()
	}
	wg.Wait()
	assert.Len(t, l.Program(), 50)
}

// TestLoop_RunsJobsAndDeliversTheirResults: a job runs off the loop (the
// call that queued it returns while it still runs), and its result is
// delivered on the loop, then a wake.
func TestLoop_RunsJobsAndDeliversTheirResults(t *testing.T) {
	l := startLoop(NewForTest(Options{}), false, 0)
	t.Cleanup(l.Stop)
	delivered := recordDeliveries(l)
	release := make(chan struct{})
	l.do(func(m *Model) { m.spawn(func() any { <-release; return "done" }) })
	assert.Empty(t, delivered(), "the call returned while its job still ran")
	close(release)
	waitWake(t, l)
	assert.Equal(t, []any{"done"}, delivered())
}

// TestLoop_WakesCoalesce: however many results land before the client
// drains, one wake waits.
func TestLoop_WakesCoalesce(t *testing.T) {
	l := startLoop(NewForTest(Options{}), false, 0)
	t.Cleanup(l.Stop)
	delivered := recordDeliveries(l)
	l.do(func(m *Model) {
		for i := 0; i < 3; i++ {
			m.spawn(func() any { return i })
		}
	})
	require.Eventually(t, func() bool { return len(delivered()) == 3 }, 5*time.Second, time.Millisecond)
	waitWake(t, l)
	select {
	case <-l.Wakes():
		t.Fatal("a second wake for one drain")
	default:
	}
}

// TestLoop_SyncReturnsEventsAndTheJobsRun: Sync returns the events, not
// the jobs, which went to the runner.
func TestLoop_SyncReturnsEventsAndTheJobsRun(t *testing.T) {
	l := StartForTest(NewForTest(Options{}))
	t.Cleanup(l.Stop)
	l.do(func(m *Model) {
		m.notifyInfo("hello")
		m.spawn(func() any { return nil })
	})
	assert.Contains(t, l.Sync(), Event(Notice{Info: "hello"}))
	assert.Len(t, l.JobsForTest(), 1, "the job went to the runner, not to Sync")
	assert.NotContains(t, l.Sync(), Event(Notice{Info: "hello"}), "Sync forgets what it returned")
}

// TestLoop_PanicInACallReachesItsCaller: the caller panics with the
// loop's stack, and every later call re-raises it.
func TestLoop_PanicInACallReachesItsCaller(t *testing.T) {
	l := StartForTest(NewForTest(Options{Program: "p"}))
	t.Cleanup(l.Stop)
	p := catch(t, func() { l.do(func(*Model) { panic("boom") }) })
	require.NotNil(t, p, "the caller panics")
	assert.Equal(t, "boom", p.Value)
	assert.Contains(t, p.Stack, "loop_test.go", "the stack is where it panicked")
	assert.Same(t, p, catch(t, func() { _ = l.Program() }), "every later call re-raises it")
}

// TestLoop_PanicInAJobReachesTheNextCall: a job's panic wakes the client,
// whose next call re-raises it.
func TestLoop_PanicInAJobReachesTheNextCall(t *testing.T) {
	l := startLoop(NewForTest(Options{}), false, 0)
	t.Cleanup(l.Stop)
	l.do(func(m *Model) { m.spawn(func() any { panic("job boom") }) })
	waitWake(t, l)
	p := catch(t, func() { _ = l.Sync() })
	require.NotNil(t, p)
	assert.Equal(t, "job boom", p.Value)
}

// TestLoop_PanicInADeliveryReachesTheNextCall: a delivery's panic wakes
// the client, whose next call re-raises it.
func TestLoop_PanicInADeliveryReachesTheNextCall(t *testing.T) {
	l := startLoop(NewForTest(Options{}), false, 0)
	t.Cleanup(l.Stop)
	l.do(func(m *Model) {
		l.deliver = func(any) { panic("deliver boom") }
		m.spawn(func() any { return "result" })
	})
	waitWake(t, l)
	p := catch(t, func() { _ = l.Sync() })
	require.NotNil(t, p)
	assert.Equal(t, "deliver boom", p.Value)
}

// TestLoop_StopEndsCallsAndDropsLateResults: after Stop a call runs
// nothing, a late result never blocks its goroutine, and Wakes is closed.
func TestLoop_StopEndsCallsAndDropsLateResults(t *testing.T) {
	l := startLoop(NewForTest(Options{Program: "p"}), false, 0)
	l.Stop()
	l.Stop() // idempotent
	assert.Equal(t, "", l.Program(), "a call after Stop runs nothing")
	returned := make(chan struct{})
	go func() {
		l.post(jobDone{result: "late"})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("a result posted after Stop blocked its goroutine")
	}
	for range l.Wakes() { // ends: Stop closed it
	}
}

// TestLoop_BeginArmsTheTickOnce: Begin starts the tick; a second Begin
// does not start another.
func TestLoop_BeginArmsTheTickOnce(t *testing.T) {
	l := startLoop(NewForTest(Options{}), true, time.Hour)
	t.Cleanup(l.Stop)
	l.Begin()
	l.Begin()
	assert.Equal(t, 1, get(l, func(*Model) int { return l.armed }))
}

// TestLoop_TickRearmsOnlyAfterItsProbeLands: the timer fires the model's
// tick, whose probe must land before the next tick is armed, so probes
// never overlap.
func TestLoop_TickRearmsOnlyAfterItsProbeLands(t *testing.T) {
	m := NewForTest(Options{})
	// An empty model still polls GitHub for the cwd's repository: hold that
	// poll in flight, so the probe is the tick's only job and nothing runs
	// git or gh.
	m.SetGateForTest("github", true, time.Now())
	l := startLoop(m, true, 5*time.Millisecond)
	t.Cleanup(l.Stop)
	l.do(func(*Model) { l.armTick() })
	var jobs []Job
	require.Eventually(t, func() bool {
		jobs = append(jobs, l.JobsForTest()...)
		return len(jobs) > 0
	}, 5*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond) // ten periods
	jobs = append(jobs, l.JobsForTest()...)
	require.Len(t, jobs, 1, "no second tick while the probe is out")
	probe := jobs[0]()
	require.IsType(t, HealthResult{}, probe)
	l.DeliverForTest(probe)
	require.Eventually(t, func() bool { return len(l.JobsForTest()) > 0 }, 5*time.Second, time.Millisecond,
		"the probe's landing armed the next tick")
}

// TestLoop_TickProbesTheSelection: the loop's tick passes the selection
// SetSelected stored.
func TestLoop_TickProbesTheSelection(t *testing.T) {
	l := StartForTest(NewForTest(Options{}))
	t.Cleanup(l.Stop)
	l.SetSelected(7)
	assert.Equal(t, InstanceID(7), get(l, (*Model).SelectedForTest))
}

// TestLoopForwardsEachMethodToItsNamesake: every method in loop_core.go
// runs its namesake on the model and nothing else (Sync runs syncEvents),
// so a copied forwarder can't call the wrong one.
func TestLoopForwardsEachMethodToItsNamesake(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "loop_core.go", nil, 0)
	require.NoError(t, err)
	pkgs := map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		pkgs[path[strings.LastIndex(path, "/")+1:]] = true
	}
	n := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		n++
		want := fn.Name.Name
		if want == "Sync" {
			want = "syncEvents"
		}
		called := map[string]bool{}
		ast.Inspect(fn.Body, func(x ast.Node) bool {
			sel, ok := x.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name == "do" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && pkgs[id.Name] {
				return true // a type: config.Workspace, session.RemoteControlAuth, …
			}
			called[sel.Sel.Name] = true
			return true
		})
		assert.Equal(t, map[string]bool{want: true}, called, "%s forwards to its namesake only", fn.Name.Name)
	}
	assert.Greater(t, n, 60, "loop_core.go holds the forwarders")
}
```

`TestLoop_TickProbesTheSelection` checks the stored value. The tick reading it is one line, in `handle` and in `TickForTest`. B's `TestSelection_ReachesTheModel` covers the TUI publishing it.

- [ ] **Step 2: Run.** `CGO_ENABLED=0 go test ./core/ -run 'TestLoop' -v` passes. Then run `CC=clang CGO_ENABLED=1 go test -race ./core/ -run 'TestLoop' -count=20`, which must be clean on every run. Check that each test bites:
  - drop `l.signal()` from `run`, and the wake tests fail;
  - drop the recover in `runJob`, and the job-panic test crashes the binary;
  - arm the tick in `handle`'s `tickDue` case, and the re-arm test fails;
  - swap `Kill`'s body to `m.Pause`, and the forwarding test fails.

  Revert each.

### A4. Verify and commit

- [ ] **Step 1:** Run `CGO_ENABLED=0 go vet ./...`, `CGO_ENABLED=0 go test ./...`, then `CC=clang CGO_ENABLED=1 go test -race ./core/`. All green. gofmt is clean.
- [ ] **Step 2: Commit.**

```bash
git add core/loop.go core/loop_core.go core/loop_test.go core/model.go core/views.go core/tick.go core/seams.go
git commit -m "feat(core): the model's own loop (core.Loop)" -m "core.Loop runs a Model on a goroutine of its own: every Core call is a round trip over it, the model's jobs run on goroutines of their own with their results delivered on the loop, the health tick fires from the loop's own timer and re-arms when its probe lands, a panic reaches the next caller, and a coalesced wake says when to drain. Nothing uses it yet: the TUI still drives the Model directly." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_016BYqFKpSvoVwusd7gJdYPr"
```

---

## Package B: the model runs on its own loop

The compiler drives most of this: deleting `coreResultMsg`/`coreCmd` and narrowing `Core` breaks every site that has to change. One commit at the end.

### B1. Narrow `Core`

**Files:** `core/iface.go`, `core/model.go`, `core/events.go`, `core/tick.go`, `core/tick_test.go`

- [ ] **Step 1: The interface.** In `core/iface.go`:
  - Replace the doc's last two sentences ("The exceptions are … *Model is the only implementation.") with: "*Loop, the model on its own goroutine, is the only implementation: every call is a round trip over that goroutine, the model runs its own jobs and tick, and Loop.Wakes says when to Sync."
  - Replace the loop group with:

```go
	// The loop: the TUI drains the model when the loop wakes it and after
	// every message (Sync), and starts its first background jobs and its
	// health tick (Begin).
	Sync() []Event
	Begin()
```

  - Replace `Tick(selected InstanceID)` and its group comment with:

```go
	// Claude status and the tick: what the TUI's pane events tell the
	// model, and the selected row the model's health tick favours.
	SetSelected(id InstanceID)
```

  - Keep `MarkOutput`, `PaneOutput`, `PaneQuiet` and `VerifyDead` after it.
  - Change the last line to `var _ Core = (*Loop)(nil)`.
- [ ] **Step 2: Docs.**
  - **`Model`'s doc in `core/model.go`:** replace "Methods must be called on one goroutine: in stage 1B, the TUI's Update goroutine." with "Methods must be called on one goroutine: its loop's (Loop), or a core test's."
  - **Field comments:** replace every "Update-goroutine only" with "Loop-goroutine only".
  - **`Job`'s doc:** "the caller hands back to Deliver" becomes "the loop (Loop) delivers back to Deliver".
  - **`HealthChecked`'s doc in `core/events.go`:** "reports that a health tick's probe landed and was applied. The loop arms the next tick then, so probes never overlap; the TUI refreshes the workbench's diff tab."
  - **`tickInst`'s doc in `core/tick.go`:** "selected is the TUI's selected instance" becomes "selected is the instance SetSelected named (the TUI's selected row)".
  - **`core/tick_test.go:71`'s comment:** "the event that re-arms the TUI's tick" becomes "the event that says the loop re-armed its tick".
- [ ] **Step 3:** Run `CGO_ENABLED=0 go build ./core/ && CGO_ENABLED=0 go test ./core/`. In `TestLoopForwardsEachMethodToItsNamesake`, replace `assert.Greater(t, n, 60, …)` with:

```go
	assert.Equal(t, reflect.TypeOf((*Core)(nil)).Elem().NumMethod()-1, n,
		"every Core method but Begin (loop.go) is a forwarder in loop_core.go")
```

(add `"reflect"` to the imports).

### B2. The TUI on the loop (production)

**Files:** `app/core_glue.go`, `app/app.go`, `app/app_init.go`

- [ ] **Step 1: `app/core_glue.go`.** Delete `coreResultMsg` and `coreCmd`, and add:

```go
// coreWakeMsg says the model did something unprompted (a job's result
// landed, its tick fired): the drain Update runs after every message
// (drainCore) applies what it produced. forwardWakes sends it.
type coreWakeMsg struct{}

// forwardWakes sends a coreWakeMsg for each of the model loop's wakes
// until they end (the loop stopped). It runs on a goroutine of its own:
// send blocks until Update takes the message, and the model's goroutine
// must never wait on the TUI.
func forwardWakes(wakes <-chan struct{}, send func(tea.Msg)) {
	for range wakes {
		send(coreWakeMsg{})
	}
}

// publishSelection tells the model which row is selected, when that
// changed since the last Update: its health probe refreshes that session's
// full diff (core.Core.SetSelected). None, or a draft row, is 0.
func (m *home) publishSelection() {
	if m.core == nil || m.workspaceSlot == nil || m.list == nil {
		return
	}
	var id core.InstanceID
	if sel := m.list.GetSelectedInstance(); sel != nil {
		id = sel.ID
	}
	if id != m.sentSelected {
		m.core.SetSelected(id)
		m.sentSelected = id
	}
}
```

  Rewrite `drainCore`, keeping its doc minus the jobs:

```go
// drainCore applies everything the model produced since the last drain:
// the views that changed first (core.ViewsChanged, from core.Core.Sync),
// then each event in order (applyCoreEvent). Applying an event can call
// the model again, so it drains until nothing is left. Update runs it
// after every message, a wake (coreWakeMsg) included. A caller whose later
// steps must see an event's effect (a workspace transition) runs it right
// after the model call. A bare test home without a model drains nothing.
func (m *home) drainCore() tea.Cmd {
	if m.core == nil {
		return nil
	}
	var cmds []tea.Cmd
	for events := m.core.Sync(); len(events) > 0; events = m.core.Sync() {
		for _, ev := range events {
			cmds = append(cmds, m.applyCoreEvent(ev))
		}
	}
	return tea.Batch(cmds...)
}
```

  In `applyCoreEvent`'s `core.HealthChecked` case, keep the workbench refresh, delete `return tickUpdateMetadataCmd`, and append to its comment: "The model's loop re-arms its own tick; the TUI's re-arms itself."
- [ ] **Step 2: `app/app.go`.**
  - **`home`'s fields.** Beside `core core.Core` (line ~133), add:

```go
	// wakes is the model loop's wake signal (core.Loop.Wakes), which Run
	// forwards into the program (forwardWakes), and stopCore stops the
	// loop. Both are nil in fixtures, whose loops run no job on their own.
	wakes    <-chan struct{}
	stopCore func()
	// sentSelected is the selection last published to the model
	// (publishSelection).
	sentSelected core.InstanceID
```

  - **`Update`:**

```go
// Update implements tea.Model: the message's handler (update), then
// whatever the model produced meanwhile (drainCore), then the selection,
// if it moved (publishSelection).
func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	cmd = tea.Batch(cmd, m.drainCore())
	m.publishSelection()
	return model, cmd
}
```

  - **The `coreResultMsg` case** in `update` becomes:

```go
	case coreWakeMsg:
		// The model's loop woke the TUI: Update's drain does the rest.
		return m, nil
```

  - **The `tickUpdateMetadataMessage` case.**
    - Delete the model half: the `// The model's half: …` comment, `selectedID` and `m.core.Tick(selectedID)`.
    - Start `cmds` with the re-arm: `cmds := []tea.Cmd{tickUpdateMetadataCmd, m.prunePanes()}`.
    - Above it, add: `// The TUI's half of the health tick re-arms itself. The model's half runs on its own loop, at the same cadence (core.Loop).`
    - Keep everything else: the toast expiry, the prune, the inline-attach backstop (it still reads `selected`), the snapshot scan and the workbench scan.
  - **`tickUpdateMetadataCmd`'s doc:** "drives the TUI's half of the health tick: toast expiry, pane-client prune and repair backstops, and the snapshot and workbench scans. It re-arms itself; the model's half (liveness, parity, diff stats, background jobs) runs on its loop's own timer at the same cadence (core.tickInterval). In event mode it is a slow belt-and-braces sweep; on the snapshot path it keeps the legacy 500ms cadence."
- [ ] **Step 3: `app/app_init.go`.** Add at package level:

```go
// startModel puts the model on its own loop: core.Start, which runs its
// jobs on goroutines of their own and ticks on its own timer. App's tests
// replace it with core.StartForTest, whose loop keeps every job for the
// test to run.
var startModel = core.Start
```

  In `newHome`, right after `core.New` succeeds:

```go
	loop := startModel(model)
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	classic, _ := loop.Classic()
	h := &home{
		ctx:        ctx,
		core:       loop,
		wakes:      loop.Wakes(),
		stopCore:   loop.Stop,
		…
```

  `model.Classic()` becomes `loop.Classic()`: after `startModel` only the loop touches the model. On the `LoadClassic` error path, call `h.stopCore()` before `return nil, fmt.Errorf("load instances: %w", err)`. Check `newHome` for any other `return nil, …` after the loop starts, and stop it on each.

  In `Run`, right after `newHome`'s error check:

```go
	// The model's loop stops when Run returns, after the program quit; a
	// result landing later is dropped, as a Cmd's was.
	defer h.stopCore()
```

  and right after `p := tea.NewProgram(h)`:

```go
	// The model's wakes (a job's result landed, its tick fired) reach the
	// program as coreWakeMsg; forwardWakes ends when the loop stops.
	go forwardWakes(h.wakes, p.Send)
```

- [ ] **Step 4:** `CGO_ENABLED=0 go build ./...` succeeds. Test compile errors are B3's.

### B3. Test plumbing and conversions

**Files:** `app/app_test.go`, `app/testcore_test.go`, the converted test files

- [ ] **Step 1: `TestMain`.** In `app/app_test.go`'s `runTests`, before the tests run: `startModel = core.StartForTest`, with the comment "every home's model runs on a loop that keeps its jobs for the test (newHome included)".
- [ ] **Step 2: Helpers in `app/testcore_test.go`.** Add:

```go
// testLoop runs model on a loop that keeps its jobs for the test
// (core.StartForTest), stopped when the test ends.
func testLoop(t *testing.T, model *core.Model) core.Core {
	t.Helper()
	l := core.StartForTest(model)
	t.Cleanup(l.Stop)
	return l
}

// loopOf returns the home's loop, for its seams.
func loopOf(m *home) *core.Loop { return m.core.(*core.Loop) }

// applyDrain drains m's model as Update's drain does (drainCore), applying
// its events but dropping their Cmds.
func applyDrain(m *home) {
	for events := m.core.Sync(); len(events) > 0; events = m.core.Sync() {
		for _, ev := range events {
			_ = m.applyCoreEvent(ev)
		}
	}
}

// tickModel runs the model's health tick as its loop's timer would
// (TickForTest), then drains as a wake's Update does.
func tickModel(t *testing.T, m *home) tea.Cmd {
	t.Helper()
	loopOf(m).TickForTest()
	_, cmd := m.Update(coreWakeMsg{})
	return cmd
}
```

  and replace these:

```go
// testModel returns the home's model, for its seams. Its loop is idle
// between calls (core.StartForTest), so the test may reach it directly.
func testModel(m *home) *core.Model { return loopOf(m).ModelForTest() }

// deliver hands m a core job's result as the runtime would (the loop
// delivers it, then wakes the TUI), returning the Cmd the wake's update
// produced (handler plus drained events).
func deliver(t *testing.T, m *home, result any) tea.Cmd {
	t.Helper()
	loopOf(m).DeliverForTest(result)
	_, cmd := m.Update(coreWakeMsg{})
	return cmd
}

// requestJob drains m's model as Update's drain does (applyDrain) and
// returns the one job the model kept: what a request made outside an
// Update (a handler called directly) queued. Calling it runs the job on
// the test's goroutine and returns its result, undelivered.
func requestJob(t *testing.T, m *home) core.Job {
	t.Helper()
	applyDrain(m)
	jobs := loopOf(m).JobsForTest()
	require.Len(t, jobs, 1, "the request queued one job")
	return jobs[0]
}

// requestResults drains m's model as Update's drain does (applyDrain), runs
// every job the model kept, and returns their results, each with a
// request's tracking removed (core.UntrackedForTest), undelivered: what a
// request made outside an Update (a handler called directly) queued.
func requestResults(t *testing.T, m *home) []any {
	t.Helper()
	applyDrain(m)
	var results []any
	for _, job := range loopOf(m).JobsForTest() {
		results = append(results, core.UntrackedForTest(job()))
	}
	return results
}

// pumpCore models the runtime's job loop: it runs cmd, then every Cmd it
// produces, serially in FIFO order, expanding tea.Batch and dropping every
// other message. Before each, it runs every job the model kept and
// delivers its result (deliver), whose Cmd joins the queue, so a job that
// follows another (a start's initial-prompt send) lands too. A
// tea.Sequence fails the test, since the order it promises is not
// modelled.
func pumpCore(t *testing.T, m *home, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; ; steps++ {
		require.Less(t, steps, 100, "core pump did not settle")
		if jobs := loopOf(m).JobsForTest(); len(jobs) > 0 {
			for _, job := range jobs {
				queue = append(queue, deliver(t, m, job()))
			}
			continue
		}
		if len(queue) == 0 {
			return
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if reflect.TypeOf(msg) == sequenceMsgType {
			t.Fatalf("pumpCore met a tea.Sequence, whose ordering it does not model")
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
		}
	}
}
```

  In `wireCore`, `m.core = core.NewForTest(core.Options{})` becomes `m.core = testLoop(t, core.NewForTest(core.Options{}))`, and its doc says a test that installs its own model wraps it in `testLoop`.
- [ ] **Step 3: Convert.** Build the tests with `CGO_ENABLED=0 go vet ./app/`, and convert each failure by this table. Then grep for the patterns the compiler can't see (the last three rows).

| Site | Before | After |
|---|---|---|
| Every `core: core.NewForTest(…)` (jump_waiting_fleet, workspace_toggle, overview_cursor, workspace_restore, remote_control ×2) | the bare model | `testLoop(t, core.NewForTest(…))` |
| `merge_test.go:132` | drives a bare `Model` (`m.Drain()`) | unchanged: core's own API on a model with no loop |
| `claude_status_test.go:17-23` (`deliverRoster`, its error twin) | `m.Update(coreResultMsg{msg: X})` | `loopOf(m).DeliverForTest(X); m.Update(coreWakeMsg{})` |
| `app_test.go:~553` | `msg := coreResultMsg{msg: core.StartResult{…}}; model, _ := h.Update(msg); homeModel := model.(*home)` | `deliver(t, h, core.StartResult{…}); homeModel := h` |
| `app_test.go:~628` | the task's `Async` returns a `coreResultMsg{KillResult}` | drop `Async`. The kill handler's task has had only `Sync` since 1C, and the test asserts the `Sync` step. |
| `events_test.go:~87-96`, `panes_test.go` `healThroughDeadEvent` | `_, cmd := m.Update(ptyDeadMsg…)`; `require.NotNil(t, cmd, …)`; unwrap `coreResultMsg` → `core.DeadVerified`; `m.Update(result)` | `_, _ = m.Update(ptyDeadMsg…)`; `verified, ok := requestJob(t, m)().(core.DeadVerified)`; `require.True(t, ok, "<the NotNil's message>")`; the `TmuxLive` assertion; `deliver(t, m, verified)`. `requestJob`'s `require.Len(jobs, 1)` now carries "the dead event scheduled the verification". |
| `panes_test.go:~450` (the retry loop) | `_, cmd := m.Update(ptyDeadMsg…)`; `require.NotNil(t, cmd)`; `if result, ok := cmd().(coreResultMsg); ok { m.Update(result) }` | `_, cmd := m.Update(ptyDeadMsg…)`; then `for _, job := range loopOf(m).JobsForTest() { deliver(t, m, job()) }`. Keep `require.NotNil(t, cmd)` if Update still returns a Cmd there. If not, assert instead that the first iteration's `JobsForTest` was non-empty, and record which you did. |
| `panes_test.go:~95-118` (kill/pause table) | `action` returns `tea.Cmd` (`requestJob(t, m)`); `done(t, msg tea.Msg)` unwraps `coreResultMsg` | `action` returns `core.Job`; `done(t, result any)` asserts `require.IsType(t, core.KillResult{}, core.UntrackedForTest(result))` (and `PauseResult`, with its `"%v"`); the call site passes `job()` |
| `panes_test.go:197`, `script_requests_test.go:255,338` | `coreResults(t, next)`; `results[i].(coreResultMsg).msg`; `m.Update(results[i])` | `coreResults(t, m, next)`; `results[i]`; `deliver(t, m, results[i])` |
| `requests_test.go:~189-195` | `res, ok := requestJob(t, m)().(coreResultMsg)`; `require.True(t, ok)`; `testModel(m).Deliver(res.msg)`; `m.core.Sync().Events` | `res := requestJob(t, m)()`; `require.NotNil(t, res, "the kill's job returned a result")`; `loopOf(m).DeliverForTest(res)`; `m.core.Sync()` |
| `health_tick_test.go:~30-80` | `m.core.Tick(…)`; `testModel(m).Drain()` for the job; `coreCmd`; `m.update(msg)`; then HealthChecked's re-arm | See Step 5. |
| `accounts_reload_test.go:29,57,61,62,67` | `m.Update(tickUpdateMetadataMessage{})` (meant for the model half) | `tickModel(t, m)`. Read each test: where it also relied on the TUI half, keep the `m.Update` too. |
| `workspace_restore_test.go:373`, `workspace_slot_test.go:175` | `newHome(…)` | add `t.Cleanup(m.stopCore)` after the error check |
| any `m.core.Tick(id)` / `.Deliver(x)` on a home's model / `.Sync().Events` | | `m.publishSelection(); loopOf(m).TickForTest()` / `loopOf(m).DeliverForTest(x)` / `.Sync()` |

  `script_requests_test.go` helpers, rewritten:

```go
// coreResults runs cmd (runCmds), then every job the model kept, and
// returns their results, undelivered and still tracked.
func coreResults(t *testing.T, m *home, cmd tea.Cmd) []any {
	t.Helper()
	runCmds(t, cmd)
	var out []any
	for _, job := range loopOf(m).JobsForTest() {
		out = append(out, job())
	}
	return out
}

// pumpRequests feeds cmd's script messages back through Update, and
// delivers every job the model kept (deliver), until none is left: a
// script's dispatch and resumes (scriptDoneMsg, scriptResumeMsg) and the
// model's job results, so a Lua call that waits on the model runs to its
// end. It returns the scriptDoneMsgs it delivered, in order. The error
// bar's hide timer waits on m.ctx, which it cancels for its run, so the
// timer returns at once.
func pumpRequests(t *testing.T, m *home, cmd tea.Cmd) []scriptDoneMsg {
	t.Helper()
	prev := m.ctx
	m.ctx = cancelledCtx()
	defer func() { m.ctx = prev }()
	var done []scriptDoneMsg
	queue := []tea.Cmd{cmd}
	for steps := 0; ; steps++ {
		require.Less(t, steps, 200, "request pump did not settle")
		if jobs := loopOf(m).JobsForTest(); len(jobs) > 0 {
			for _, job := range jobs {
				queue = append(queue, deliver(t, m, job()))
			}
			continue
		}
		if len(queue) == 0 {
			return done
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if reflect.TypeOf(msg) == sequenceMsgType {
			t.Fatalf("pumpRequests met a tea.Sequence, whose ordering it does not model")
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case scriptDoneMsg:
			done = append(done, msg)
			_, next := m.Update(msg)
			queue = append(queue, next)
		case scriptResumeMsg:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}
```

- [ ] **Step 4: Silent patterns.** Grep for the patterns that still compile but changed meaning:
  - `git grep -n 'tickUpdateMetadataMessage{}' -- 'app/*_test.go'`: each must want the TUI half only.
  - `git grep -n 'cmd()\|next()' -- 'app/*_test.go' | grep -i 'result\|core'`: a Cmd run to find a job result now finds none.
  - `git grep -n 'testModel(.*)\.\(Drain\|Sync\|Deliver\)' -- 'app/*_test.go'`: on a loop's model, `Drain` sees no jobs (`accounts_test.go:31` only discards output and may stay).

  Fix each that relied on the old plumbing.
- [ ] **Step 5: `health_tick_test.go`.**
  - **Rewrite the probe test** (lines ~30-80) to keep its first half's meaning: the tick's probe finds the session gone and pauses it. Replace `m.core.Tick(m.list.GetSelectedInstance().ID)` and the `Drain`/`coreCmd`/`update` dance with:

```go
	m.publishSelection()
	loopOf(m).TickForTest()
	jobs := loopOf(m).JobsForTest()
	require.Len(t, jobs, 1, "the probe is the tick's only job")
	result := jobs[0]()
	require.IsType(t, core.HealthResult{}, result)
	deliver(t, m, result)
	assert.Equal(t, session.Paused, inst.GetStatus(), "the probe found the session gone")
```

  - **Delete the re-arm half** (`rearm`, `checked`, the `HealthChecked` loop and the pointer comparison). Record that these assertions **moved**:
    - "every probe ends with HealthChecked" is already pinned by `core/tick_test.go`.
    - The re-arm is now the loop's: `TestLoop_TickRearmsOnlyAfterItsProbeLands`.
    - The TUI's own re-arm is `TestTUITick_RearmsItselfAndTicksNoModel` (B4).
  - **Update the test's doc comment.**

### B4. New app tests

**Files:** Create `app/core_glue_test.go`

- [ ] **Step 1: Write the tests.**

```go
package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForwardWakes_OneMessagePerWakeUntilTheLoopStops: each wake reaches
// the program as a coreWakeMsg, and the forwarder ends when its channel
// closes.
func TestForwardWakes_OneMessagePerWakeUntilTheLoopStops(t *testing.T) {
	wakes := make(chan struct{}, 1)
	var got []tea.Msg
	done := make(chan struct{})
	go func() {
		forwardWakes(wakes, func(msg tea.Msg) { got = append(got, msg) })
		close(done)
	}()
	wakes <- struct{}{}
	wakes <- struct{}{}
	close(wakes)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forwardWakes did not end when its channel closed")
	}
	assert.Equal(t, []tea.Msg{coreWakeMsg{}, coreWakeMsg{}}, got)
}

// tickRearmed stands in for the TUI tick's next firing.
type tickRearmed struct{}

// TestTUITick_RearmsItselfAndTicksNoModel: the TUI's tick re-arms itself
// and leaves the model's half to the model's loop. The tick's Cmd is
// swapped for one that answers at once: tea.Batch hands a lone Cmd back
// as itself, so running the real one would sleep a whole period.
func TestTUITick_RearmsItselfAndTicksNoModel(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m)
	m.syncViews()
	prev := tickUpdateMetadataCmd
	tickUpdateMetadataCmd = func() tea.Msg { return tickRearmed{} }
	t.Cleanup(func() { tickUpdateMetadataCmd = prev })

	_, cmd := m.Update(tickUpdateMetadataMessage{})
	assert.Contains(t, runCmds(t, cmd), tea.Msg(tickRearmed{}), "the TUI's tick re-arms itself")
	assert.Empty(t, loopOf(m).JobsForTest(), "the TUI's tick starts no probe: the model ticks on its own loop")
}

// TestHealthChecked_ArmsNoTUITick: the probe's landing re-arms only the
// model's tick.
func TestHealthChecked_ArmsNoTUITick(t *testing.T) {
	m := homeWithAppState(t)
	assert.Nil(t, m.applyCoreEvent(core.HealthChecked{}))
}

// TestSelection_ReachesTheModel: Update publishes the selected row to the
// model, whose probe refreshes its full diff.
func TestSelection_ReachesTheModel(t *testing.T) {
	m := homeWithAppState(t)
	inst := addReadyInstance(t, m)
	m.syncViews()
	m.Update(coreWakeMsg{})
	assert.Equal(t, idOf(m, inst), testModel(m).SelectedForTest())
}

// TestRealLoop_AJobsResultReachesTheTUIByWake: on a production loop, a
// request's job runs on a goroutine of its own, its result lands on the
// loop, and the loop's wake brings it to the TUI, with no test-driven
// delivery. Run it under -race.
func TestRealLoop_AJobsResultReachesTheTUIByWake(t *testing.T) {
	m := homeWithAppState(t)
	addReadyInstance(t, m) // never started: the kill's job fails (no worktree)
	m.errBox.SetSize(400, 1)
	m.syncViews()
	held := loopOf(m)
	model := held.ModelForTest()
	held.Stop()
	l := core.Start(model)
	t.Cleanup(l.Stop)
	m.core, m.wakes = l, l.Wakes()
	m.aliveProbe = func(string) bool { return true } // the TUI's probe must not read the model's instances while its loop runs

	_, _ = runKillSelectedNoConfirm(m)
	select {
	case <-m.wakes:
	case <-time.After(10 * time.Second):
		t.Fatal("the kill's result never woke the TUI")
	}
	_, _ = m.Update(coreWakeMsg{})
	assert.NotEmpty(t, m.errBox.String(), "the model's notice of the failed kill reached the error bar")
}
```

  Adaptations to record if you need them:
  - **The selection.** If `TestSelection_ReachesTheModel`'s fixture selects nothing, select the row first (`m.list.SetSelectedInstance(0)`, or the helper the other tests use).
  - **The error bar.** If `ErrBox.String()` renders empty for an error, assert on `ErrBox`'s error accessor instead.
  - **`runCmds`.** It lives in `script_requests_test.go` and expands batches. If its signature differs, use it as that file does.
- [ ] **Step 2: Run** `CGO_ENABLED=0 go test ./app/ -run 'TestForwardWakes|TestTUITick|TestHealthChecked_Arms|TestSelection_Reaches|TestRealLoop' -v`, then the same under `-race -count=10`. Check the tests bite:
  - delete `m.publishSelection()` from `Update`, and the selection test fails;
  - re-add `return tickUpdateMetadataCmd` to the `HealthChecked` applier, and its test fails;
  - drop the `go forwardWakes` line — no app test sees that, so the smoke run is its check (C2, check 1).

  Revert each.

### B5. Enforcement

**Files:** `core/value_boundary_test.go`, `internal/testenv/instance_enforce_test.go`

- [ ] **Step 1: `TestCoreIsValueTyped`.** Delete the `exempt` map, its `continue`, and the doc's last sentence ("Sync and Deliver carry …").
- [ ] **Step 2: `TestTUIHoldsNoModelObject`.** In `modelObjects`, the core set becomes `"Workspace": true, "WorkspaceParts": true, "NewWorkspace": true, "Model": true, "Loop": true, "Job": true, "Out": true`. In the comment above it, the core line becomes: "core: a loaded workspace, the model and its loop, and the jobs and output it keeps to itself (the TUI holds a core.Core, built with core.New and started in newHome, and handles no job)".
- [ ] **Step 3: Show each rule bites,** then revert:
  - a temporary `Deliver(any)` on `Core` and `Loop` fails `TestCoreIsValueTyped`;
  - a temporary `var _ core.Job` in an app file fails `TestTUIHoldsNoModelObject`;
  - a temporary `func f(l *core.Loop) {}` in an app file fails it too.

### B6. The multi-call audit (decision 3)

A job's result can now land between two calls the TUI makes in one Update. This table lists every app function with two or more model calls (from an AST scan at d071973) and why each is safe.

- [ ] **Step 1:** Re-run the scan: list `FuncDecl`s in `app/*.go`, tests excluded, with two or more `m.core.X(…)` calls. Confirm this list, and for any new function add a row with its verdict.

| Function | Calls | Verdict |
|---|---|---|
| `accounts.go:accountStatuses`, `accountRows` | names, usage, auth | Display. A refresh landing between calls shows its fresher value; the next `AccountsChanged` repaints. |
| `accounts.go:newLaunchOptionsOverlay` | `ReloadAccounts`, nested drain, account queries | It preselects and displays. The launch it leads to resolves the account at launch and fails closed (`MissingAccountError`). |
| `accounts.go:accountLoginCmd` | `Account`, `AccountEnv`, `ClaudeProgram` | An account removed in between makes `AccountEnv` fail, which is already handled. |
| `accounts.go:carryOutAccountRequest` | a write, then `ReloadAccounts` | A write followed by a reload. |
| `app.go:update` | one call per case | One call each. |
| `app_init.go:newHome` | startup | It runs before the program does. Startup jobs (sweeps) may land in between, which only freshens the next query. |
| `app_init.go:restoreSavedWorkspaces`, `views.go:seedViews`, `workspaces.go:checkSlotInvariant` | a transition or `IsLoaded`, then `Tabs`/`Views` | Only TUI requests change the set of loaded workspaces. No delivery opens or closes one. |
| `app_scripts.go:scriptInstanceOp`, `intents.go:runStashSelectedOpts` | one call per branch | One call. |
| `intents.go:runRestartWithOptionsSelected`, `runOpenSettings`, `state_issue_picker.go:openLaunchOptionsForNew`, `runNewFromIssue` | display queries, then a request or probe | The request re-validates (`admit`). |
| `intents.go:runOpenWorkspacePicker` | `ReloadRegistry`, `Registry`, `RestoreFailed` | `RestoreFailed` changes only on transitions. |
| `state_issue_picker.go:issuePickerStatus` | `GitHubErr`, `GitHubSnapshot` | A poll landing between them can pair one poll's error with the next one's snapshot for one frame. `GitHubChanged` repaints right after. Accepted. |
| `state_prompt.go:handleStatePromptKey` | `GitHubUnavailable`, `FetchIssue` | The request reports its own failure. |
| `state_workspace_picker.go:handleStateWorkspaceKey`, `workspaces.go:applyWorkspaceToggle` | writes | Writes that no delivery touches. |

  The general rule this rests on goes into CLAUDE.md (C1):
  - the TUI decides from its own cached stores (`slot.views`, `slot.info`), which change only at a drain;
  - live queries are display data;
  - a request re-validates.

  So nothing may combine two live queries into one decision.

### B7. Verify and commit

- [ ] **Step 1: Run the checks:**
  - `CGO_ENABLED=0 go vet ./...` and `CGO_ENABLED=0 go test ./...`;
  - `CC=clang CGO_ENABLED=1 go test -race ./...`, with every package green;
  - `CC=clang CGO_ENABLED=1 go test -race -count=5 ./core/ ./app/`;
  - `go test -tags e2e ./e2e/...`, which drives the real wake path (`TestE2E_FakeClaudeHooksDriveStatus`);
  - gofmt.
- [ ] **Step 2: Assertion count.** It must be no lower than 8586 plus the new tests, less the moved re-arm assertions. List each moved or reworded assertion.
- [ ] **Step 3: Commit** `refactor(core,app): the model runs on its own loop`, with a body naming the narrowed `Core`, the wake, the tick split and the test plumbing, ending with both trailers.

---

## Package C: documentation and verification

### C1. CLAUDE.md and the spec

- [ ] **Step 1: CLAUDE.md.**
  - **Core Flow (line ~175).**
    - "The TUI drives the model synchronously on its Update goroutine (daemon stages 1B and 1C; the model gets its own goroutine in 1D)" becomes: the model runs on its own goroutine (`core.Loop`, daemon stage 1E), and every `core.Core` call is a synchronous round trip over it.
    - Blocking work is a `core.Job` the loop runs on a goroutine of its own and delivers on the loop. After a result or a tick, the loop signals `Wakes`, which `Run`'s `forwardWakes` turns into a `coreWakeMsg`.
    - `home.Update` runs the handler, then `drainCore` (`Sync` returns events only), then `publishSelection`.
    - Delete `coreCmd`/`coreResultMsg`, and "wraps each job as a Cmd". `newHome` starts the loop (`startModel`), and `Init` calls `Begin`, which also arms the model's tick.
  - **`app/` bullet.** `app/core_glue.go` is the seam: `coreWakeMsg`/`forwardWakes`, `drainCore`/`applyCoreEvent`, `publishSelection`, `newSlotView`, `slotFor`.
  - **`core/` bullet.**
    - Replace "with `Sync` and `Deliver`, the job plumbing, exempt until stage 1E" with the loop.
    - Replace "Every method runs on one goroutine, in stages 1B to 1D the TUI's Update goroutine" with: `Model` is single-goroutine; `core.Loop` (`loop.go`, `loop_core.go`) owns it and implements `Core`.
    - Name the panic forwarding (`LoopPanic`), `Stop`, and the seams (`StartForTest`, `JobsForTest`, `DeliverForTest`, `TickForTest`, `ModelForTest`).
    - Note that the job outbox is now the loop's to run (`takeJobs`), while core's tests still drive `Model` with `Sync`/`Drain`/`Deliver`.
  - **Event-driven panes gotcha (line ~225).** The tick splits along the loop:
    - the model's half (`core.Model.Tick`) fires from the loop's own timer (`tickInterval`), armed by `Begin` and re-armed when the probe lands, favouring the row `SetSelected` named;
    - the TUI's half (the `tickUpdateMetadataMessage` case) re-arms itself;
    - `HealthChecked` re-arms nothing in the TUI.

    Also update "put it on the health tick: the model's half when it needs no pane client".
  - **No-model-mutation gotcha.**
    - The model is now a separate goroutine, and every call to it is a round trip, so a Cmd body that called `m.core` would no longer race. It would still read state the drain hasn't applied, so the rule stands.
    - Add the loop's rule: nothing on the loop waits on the TUI or calls the loop.
    - Add the per-call atomicity rule from B6.
    - Add the behaviour change: the model keeps running while the event loop is blocked (full-screen attach, `$EDITOR`).
  - **Testing Patterns.**
    - `wireCore`/`testLoop` install a `core.StartForTest` loop. App's `TestMain` sets `startModel`.
    - Jobs are held: `requestJob` returns a `core.Job`, `requestResults` runs them, `pumpCore` delivers them, `deliver` is `DeliverForTest` plus a wake, and `tickModel` runs the model's tick.
    - Seams on `testModel(m)` are safe because the loop is idle between calls.
  - **Stale names.** Find them with `git grep -n -e coreCmd -e coreResultMsg -e 'core.Model.Tick' -e 'Update goroutine' -e 'stage 1E' -e 'Out.Jobs' -- CLAUDE.md`, and fix each hit that describes the model's goroutine. "Update goroutine" stays where it means the TUI's.
- [ ] **Step 2: The spec.**
  - **Rollout stage 1:** link the 1E entry to this plan with a one-line summary. Amend it: the model also ticks on its own timer (the user's 2026-10-08 decision), and calls are serialized per call.
  - **Stage 2's line:** its transport keeps calls synchronous or revisits the read-after-write sites (decision 12).
  - **Assumption 4:** the model now ticks without a TUI. Hook scans still ride pane events plus that tick, so the launch watch and hook timer remain stage 3's.
- [ ] **Step 3: Commit** `docs: CLAUDE.md and spec for the model loop (daemon stage 1E)`, with both trailers.

### C2. Verification and smoke run

- [ ] **Step 1: The suite.** Run `go vet`, `go test ./...`, `-race ./...`, e2e and gofmt. All must be green.
- [ ] **Step 2: Sandbox smoke run.** Use the loom-dev skill, with the same safety rules as 1D's D2:
  - build once at a named SHA, then `start --no-build`;
  - build a baseline sandbox from d071973 (`git archive` into the scratchpad, no worktree);
  - point `CLAUDE_CONFIG_DIR` at a throwaway dir;
  - never touch `~/.loom`, `~/.claude` or the user's tmux server.

  Rerun 1D's smoke checks 1–7 and the lifecycle checks (create, pause, resume, recover, kill). Then run these new ones, which test the wake and the model's own tick. **Press nothing after the action** in each.
  1. **Kill.** Kill a session (`D`, `y`). The row disappears without another key.
  2. **Create.** Create a session with a prompt (`N`). It goes Loading → Running, and its prompt lands, without another key.
  3. **Diff stats.** Write a file in a session's worktree from a second shell. Its diff stats show in the pane title and the overview card within a few seconds.
  4. **Hook status.** A fake-claude session's status moves Running → Ready as its hooks fire.
  5. **Dead agent.** Kill an agent's process from a second shell. The row goes Paused within a tick.
  6. **Full-screen attach.** `alt+a` into a session; meanwhile kill another session's agent from a second shell. Detach: the other row is already Paused.
  7. **Idle.** Leave loom idle with three sessions for a minute. Its CPU stays near zero (`top -p <pid>`); nothing busy-loops on wakes.
  8. **Snapshot path.** Under `LOOM_PANE_RENDERER=snapshot`, checks 1 and 3 hold, and status still settles.
  9. **Quit.** `q` exits at once with a kill still in flight. The next start shows a consistent list, with the killed session gone or reconciled.

  Check every oddity against the baseline before calling it a regression.

- [ ] **Step 3: Report** the test totals, each smoke check's outcome, and every deviation from this plan.

### C3. Outcome (coordinator, after the final review)

- [ ] Append "Outcome and follow-ups" to this plan, and update the `loom-scrum-daemon-direction` memory: 1E is done, and the next step is the stage 2 plan (codec and transport).

---

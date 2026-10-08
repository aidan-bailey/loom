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

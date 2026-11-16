# Job controls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. The owner chose inline execution (superpowers:executing-plans) for this plan.

**Goal:** Let the owner start a queued job now, park/unpark a queued job, and pause/resume a running job, from the TUI's work view and the phone.

**Architecture:** The state lives on `sched.Record` (`Forced`, `Parked`, `Unparked`, `PausedAt`, `PausedSecs`, `PauseReclaimed`, `Control`, `ControlErr`); the pure `sched` functions read it (admission's forced starts and park skip, the paused job's counts, the ladder's order, grants, the forecast). One request, `Core.ControlJob(JobControl, ReqID)`, changes it; pause and resume go through a control step beside the ladder and the give-back, one confinement call at a time. The work view and the phone call the request.

**Tech Stack:** Go (Bubble Tea TUI, the daemon's model and generated RPC), systemd scopes over D-Bus (`sched.Confiner`), cgroup v2 files, Preact + htm (the phone app, no build step).

Spec: [`docs/superpowers/specs/2026-11-16-job-controls-design.md`](../specs/2026-11-16-job-controls-design.md).

**Spec amendments made while planning** (written into the spec in Task 1):

1. Pausing an exclusive job is refused (`job N is exclusive: it holds the host until it ends`): admission keeps an exclusive job's whole-host claim whatever its state, so a pause would free nothing.
2. The log events are `queue.paused` and `queue.unpaused`, not `queue.resumed`, which the give-back already logs with `lent_s`.
3. A forced job that a pass cannot start (admission's policy unusable, a lock taken since the request) stays queued and forced; the next pass starts it.

## Rules every task keeps

- Heavy commands go through the queue: `loom job run -d "<what>" -- <cmd>` (foreground, or `run_in_background` for the full suite). Never run `./loom`, `go run .` or `loom serve stop` in this pane.
- Stage and commit by path (`git add <paths>`, `git commit -- <paths>`), never `-a`, `git add -A` or `.`. Scratch files go in `/tmp`, not the worktree.
- `CGO_ENABLED=0` for every `go test`/`go vet`; race runs are `CC=clang CGO_ENABLED=1 go test -race`.
- Format with `gofmt -w` on the files touched; never `gofmt -w .`.
- Never edit `core/rpc/methods_gen.go` or `docs/specs/protocol.md` by hand: `go generate ./core/rpc` and `go test ./core/rpc -run TestProtocolReference -update`.
- `sched/CLAUDE.md` is at 19,874 of its 20,000 bytes: rules this plan adds go in `core/CLAUDE.md` (check `go run ./tools/claudemd -v` for headroom) or a guide, not there.

## File map

| File | Responsibility in this plan |
|---|---|
| `sched/job.go` | the record's control fields, `ControlKind`, `Paused`, `PausedFor`, `QueuedSince` |
| `sched/admit.go` | forced starts before the walk, the park skip, `WaitParked`, `LockBlock`, `runningNeed` for paused jobs |
| `sched/greedy.go` | a paused job counted at memory only; `PausedPrediction` |
| `sched/grant.go` | parked waits stop no grant; forced starts get none |
| `sched/forecast.go` | a paused job never ends in the simulation |
| `sched/ladder.go` | `LadderJob.Forced`, the victim order |
| `sched/control_test.go` (new) | the pure tests of this plan |
| `core/queue_control.go` (new) | `JobAction`, `JobControl`, `ControlJob`, the preconditions, the control step and its delivery |
| `core/queue_control_test.go` (new) | the model's tests through the production path |
| `core/iface.go`, `core/loop_core.go`, `core/model.go` | the method, its forwarder, the delivery case |
| `core/queue_watch.go` | the step after each sample; the rung reconcile |
| `core/queue_ladder.go`, `core/queue_borrow.go` | the ladder's and the give-back's skips; `Forced` to the ladder |
| `core/queue_greedy.go` | `predictRunning` for paused jobs |
| `core/queue_admit.go` | `forced` in `queue.job_started` |
| `core/queue_view.go` | the view's fields, `QueuedSince`, overdue |
| `core/rpc/methods_gen.go`, `docs/specs/protocol.md` | regenerated |
| `app/work.go`, `ui/work.go` | the keys, the confirmation, the hint, the row tags |
| `internal/cli/job_list.go` | the paused row's WAITS FOR |
| `internal/web/api.go`, `internal/web/jobs.go`, `internal/web/static/js/job.mjs` | the route, the JSON, the buttons |
| `USAGE.md`, `sched/README.md`, `core/README.md`, `internal/web/README.md`, `core/CLAUDE.md` | docs |

---

### Task 1: `sched`: the record and the pure rules

**Files:**
- Modify: `sched/job.go` (Record, after the `Locks`/`Key` fields; `QueuedSince` at the end of the record's methods)
- Modify: `sched/admit.go` (`runningNeed`, `admitWith`, a new `LockBlock` and `WaitParked`)
- Modify: `sched/greedy.go` (`greedyFit.add`; new `PausedPrediction`)
- Modify: `sched/grant.go` (`Grant`'s wait loop and start loop)
- Modify: `sched/forecast.go` (`forecastWith`'s running case)
- Modify: `sched/ladder.go` (`LadderJob`, `victim`)
- Create: `sched/control_test.go`
- Modify: `docs/superpowers/specs/2026-11-16-job-controls-design.md` (the three amendments)

- [ ] **Step 1: Write the failing tests** in `sched/control_test.go`:

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestQueuedSince_TakesTheLatestOfSubmitReadyAndUnpark(t *testing.T) {
	r := Record{Submitted: t0}
	assert.Equal(t, t0, r.QueuedSince())
	r.Ready = t0.Add(time.Minute)
	assert.Equal(t, t0.Add(time.Minute), r.QueuedSince())
	r.Unparked = t0.Add(30 * time.Second)
	assert.Equal(t, t0.Add(time.Minute), r.QueuedSince(), "an unpark before the release counts from the release")
	r.Unparked = t0.Add(2 * time.Minute)
	assert.Equal(t, t0.Add(2*time.Minute), r.QueuedSince())
	assert.Equal(t, t0.Add(2*time.Minute), Record{Submitted: t0, Unparked: t0.Add(2 * time.Minute)}.QueuedSince())
}

func TestPausedFor(t *testing.T) {
	r := Record{PausedSecs: 30}
	assert.Equal(t, 30*time.Second, r.PausedFor(t0))
	assert.False(t, r.Paused())
	r.PausedAt = t0.Add(-time.Minute)
	assert.True(t, r.Paused())
	assert.Equal(t, 90*time.Second, r.PausedFor(t0))
}

func TestAdmitGreedy_AForcedJobStartsPastEveryFit(t *testing.T) {
	p := greedyPolicy()
	g := Greedy{BudgetKB: gb(4), Running: map[JobID]Prediction{}, Predict: flatPredict(nil), Holdoff: ""}
	forced := queued(1, Claim{CPUs: 32, MemoryGB: 12}) // over the cores cap and the budget
	forced.Forced = true
	small := queued(2, Claim{CPUs: 1, MemoryGB: 1})
	d := AdmitGreedy([]Record{forced, small}, p, t0, g)
	assert.Equal(t, []JobID{1}, d.Start, "the forced job starts; the small one sees its 12 GB")
	assert.Contains(t, d.Waiting[2], "at its peak")

	g.Holdoff = "memory is short (job 9 squeezed)"
	d = AdmitGreedy([]Record{forced, small}, p, t0, g)
	assert.Equal(t, []JobID{1}, d.Start, "past the ladder's holdoff too")
	assert.Equal(t, g.Holdoff, d.Waiting[2])

	g.Holdoff = ""
	sizing := forced
	sizing.Sizing = SizingAsking
	assert.Equal(t, []JobID{1}, AdmitGreedy([]Record{sizing}, p, t0, g).Start, "while it is being sized")

	busy := running(5, Claim{Exclusive: true})
	g.Running = map[JobID]Prediction{5: Flat(0, t0, t0.Add(time.Hour))}
	assert.Equal(t, []JobID{1}, AdmitGreedy([]Record{busy, forced}, p, t0, g).Start, "beside an exclusive job")
}

func TestAdmit_AForcedJobWaitsOnARunningJobsLock(t *testing.T) {
	p := greedyPolicy()
	holder := running(5, Claim{CPUs: 1, MemoryGB: 1})
	holder.Locks = []string{"port:4300"}
	forced := queued(1, Claim{CPUs: 1, MemoryGB: 1})
	forced.Forced, forced.Locks = true, []string{"port:4300"}
	d := Admit([]Record{holder, forced}, p, t0, nil, nil)
	assert.Empty(t, d.Start)
	assert.Equal(t, "waits for lock port:4300 (job 5)", d.Waiting[1])
	assert.Equal(t, "waits for lock port:4300 (job 5)", LockBlock([]Record{holder, forced}, forced))

	// A queued job naming the lock a forced job takes waits for it, and
	// is never a starving head that the guard would start beside it.
	free := forced
	free.Locks = []string{"port:4301"}
	other := starvingFor(2, Claim{CPUs: 1, MemoryGB: 1}, 11*time.Minute)
	other.Locks = []string{"port:4301"}
	d = Admit([]Record{free, other}, p, t0, nil, nil)
	assert.Equal(t, []JobID{1}, d.Start)
	assert.Equal(t, "waits for lock port:4301 (job 1)", d.Waiting[2])

	assert.Empty(t, Admit([]Record{forced}, Policy{}, t0, nil, nil).Start, "a zero policy starts nothing, forced or not")
}

func TestAdmit_AParkedJobIsSkippedAndNeverTheHead(t *testing.T) {
	p := greedyPolicy()
	parked := starvingFor(1, Claim{CPUs: 1, MemoryGB: 1}, time.Hour)
	parked.Parked = true
	next := queued(2, Claim{CPUs: 1, MemoryGB: 1})
	d := Admit([]Record{parked, next}, p, t0, nil, nil)
	assert.Equal(t, []JobID{2}, d.Start)
	assert.Equal(t, WaitParked, d.Waiting[1])
	assert.Equal(t, JobID(0), d.Starved)

	parked.Parked = false
	d = Admit([]Record{parked, next}, p, t0, nil, nil)
	assert.Equal(t, JobID(1), d.Starved, "unparked, it is the starving head again")
}

func TestAdmit_APausedJobCountsMemoryOnly(t *testing.T) {
	p := greedyPolicy() // 14 cores, 14 GB, 2 I/O slots
	paused := running(5, Claim{CPUs: 14, MemoryGB: 6, IO: true})
	io := running(6, Claim{CPUs: 0, MemoryGB: 1, IO: true})
	next := queued(1, Claim{CPUs: 8, MemoryGB: 6, IO: true})
	assert.Empty(t, Admit([]Record{paused, io, next}, p, t0, nil, nil).Start, "running, its cores and slot count")
	paused.PausedAt = t0.Add(-time.Minute)
	assert.Equal(t, []JobID{1}, Admit([]Record{paused, io, next}, p, t0, nil, nil).Start, "paused, only its memory counts")
	next.Claim.MemoryGB = 8
	assert.Contains(t, Admit([]Record{paused, io, next}, p, t0, nil, nil).Waiting[1], "GB", "6 + 1 + 8 GB pass the 14 GB cap")

	g := Greedy{BudgetKB: gb(14), Running: map[JobID]Prediction{5: PausedPrediction(gb(2), t0), 6: Flat(gb(1), t0, t0.Add(time.Minute))}, Predict: flatPredict(nil)}
	next.Claim = Claim{CPUs: 14, MemoryGB: 11, IO: true}
	assert.Equal(t, []JobID{1}, AdmitGreedy([]Record{paused, io, next}, p, t0, g).Start, "its 2 GB used, no cores, no slot")
	next.Claim.MemoryGB = 12
	assert.Contains(t, AdmitGreedy([]Record{paused, io, next}, p, t0, g).Waiting[1], "at its peak")
}

func TestPausedPrediction_OutlastsAnyCandidate(t *testing.T) {
	pred := PausedPrediction(gb(3), t0)
	assert.Equal(t, gb(3), PeakSum([]Prediction{pred}, t0, t0.Add(24*time.Hour)))
}

func TestGrant_ParkedWaitsAndForcedStarts(t *testing.T) {
	p := greedyPolicy()
	start := queued(1, Claim{CPUs: 2, MemoryGB: 1})
	b := func(Record) *Belief { return nil }
	parked := queued(2, Claim{CPUs: 1, MemoryGB: 1})
	parked.Parked = true
	jobs := []Record{start, parked}
	dec := Decision{Start: []JobID{1}, Waiting: map[JobID]string{2: WaitParked}}
	g := Grant(dec, jobs, p, nil, b, HostIdle{})
	assert.Equal(t, Grant(Decision{Start: []JobID{1}, Waiting: map[JobID]string{}}, []Record{start}, p, nil, b, HostIdle{}), g,
		"a parked job's wait stops no grant")

	dec.Waiting[2] = "needs 9 GB (2 free)"
	assert.Empty(t, Grant(dec, jobs, p, nil, b, HostIdle{}).Spare, "any other wait does")

	start.Forced = true
	dec = Decision{Start: []JobID{1}, Waiting: map[JobID]string{}}
	assert.Empty(t, Grant(dec, []Record{start}, p, nil, b, HostIdle{}).Spare, "a forced start gets no spare cores")
}

func TestForecast_APausedJobNeverEnds(t *testing.T) {
	p := greedyPolicy()
	paused := running(5, Claim{CPUs: 1, MemoryGB: 10})
	paused.PausedAt = t0.Add(-time.Minute)
	next := queued(1, Claim{CPUs: 1, MemoryGB: 10})
	usual := func(Record) (time.Duration, bool) { return time.Minute, true }
	starts := Forecast([]Record{paused, next}, p, t0, nil, nil, usual, time.Minute)
	assert.NotContains(t, starts, JobID(1), "10 + 10 GB pass the cap until the owner resumes it")
	paused.PausedAt = time.Time{}
	assert.Contains(t, Forecast([]Record{paused, next}, p, t0, nil, nil, usual, time.Minute), JobID(1))
}

func TestLadder_ForcedJobsGoLast(t *testing.T) {
	in := LadderInput{Now: t0, HeadroomKB: 0, ReserveKB: gb(4), Jobs: []LadderJob{
		{ID: 1, Started: t0.Add(-time.Minute), WorkingKB: gb(1)},
		{ID: 2, Started: t0.Add(-time.Second), WorkingKB: gb(1), Forced: true},
	}}
	_, act := Ladder(LadderState{}, in)
	if assert.NotNil(t, act) {
		assert.Equal(t, JobID(1), act.Job, "the most recent start is forced: the older one goes first")
	}
	in.Jobs[1].Forced = false
	_, act = Ladder(LadderState{}, in)
	if assert.NotNil(t, act) {
		assert.Equal(t, JobID(2), act.Job)
	}
}
```

Add the helper beside the tests (the greedy tests' `starving` builds the same; this name keeps it apart):

```go
// starvingFor is queued job id with claim c, queued waited ago.
func starvingFor(id JobID, c Claim, waited time.Duration) Record {
	r := queued(id, c)
	r.Submitted, r.Ready = t0.Add(-waited), t0.Add(-waited)
	return r
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "sched: job controls red" -- sh -c 'CGO_ENABLED=0 go test -count=1 ./sched/ 2>&1 | tail -20'`
Expected: build failure naming `Unparked`, `PausedAt`, `Forced`, `Parked`, `PausedFor`, `Paused`, `WaitParked`, `LockBlock`, `PausedPrediction`, `LadderJob.Forced`.

- [ ] **Step 3: The record** (`sched/job.go`). After the `Key` field of `Record`:

```go
	// Job controls (spec 2026-11-16-job-controls §4): the owner started it
	// past the queue (start now), parked it, or paused it. Unparked is its
	// last unpark (QueuedSince); PausedAt is set while it is paused and
	// PausedSecs sums this attempt's earlier pauses; Control is a pause or
	// resume asked for and not yet carried out, ControlErr the last
	// failed one's error.
	Forced         bool        `json:"forced,omitempty"`
	Parked         bool        `json:"parked,omitempty"`
	Unparked       time.Time   `json:"unparked,omitzero"`
	PausedAt       time.Time   `json:"paused_at,omitzero"`
	PausedSecs     int64       `json:"paused_secs,omitempty"`
	PauseReclaimed bool        `json:"pause_reclaimed,omitempty"`
	Control        ControlKind `json:"control,omitempty"`
	ControlErr     string      `json:"control_err,omitempty"`
```

Before `type Record struct`:

```go
// ControlKind is a pause or resume the owner asked for (job controls spec
// §4), "" for none.
type ControlKind string

// The control step's actions.
const (
	ControlPause  ControlKind = "pause"
	ControlResume ControlKind = "resume"
)
```

Replace `QueuedSince` and add the two pause methods after it:

```go
// QueuedSince is when r joined the queue: the latest of Submitted, Ready (a
// dependent's release) and Unparked (job controls spec §5.2), so an
// unparked job's starvation wait starts again.
func (r Record) QueuedSince() time.Time {
	at := r.Submitted
	if r.Ready.After(at) {
		at = r.Ready
	}
	if r.Unparked.After(at) {
		at = r.Unparked
	}
	return at
}

// Paused reports whether the owner has r paused (job controls spec §5.3).
func (r Record) Paused() bool { return !r.PausedAt.IsZero() }

// PausedFor is how long r has been paused in this attempt as of now: its
// PausedSecs, plus the current pause while it is paused.
func (r Record) PausedFor(now time.Time) time.Duration {
	d := time.Duration(r.PausedSecs) * time.Second
	if r.Paused() {
		d += now.Sub(r.PausedAt)
	}
	return d
}
```

`QueuedSince` used to return `Ready` even when it was before `Submitted`; nothing sets one earlier (`core/queue_chain.go` releases at `now`), so the latest-of form changes no existing record's answer. Run the dependent-jobs tests in Step 9 to confirm.

- [ ] **Step 4: Admission** (`sched/admit.go`).

Beside `waitOver`:

```go
// WaitParked is why a parked job waits (job controls spec §5.2).
const WaitParked = "parked by you"
```

`runningNeed` becomes:

```go
// runningNeed is what running job r takes from p's caps: its hold, when
// holds has one and its claim is not exclusive (past-peak holds spec §6),
// else its claim; a paused job (job controls spec §7.2) takes its memory
// alone, using no cores and doing no I/O.
func runningNeed(r Record, p Policy, holds map[JobID]Need) Need {
	n := r.Claim.Need(p.Caps)
	if h, ok := holds[r.ID]; ok && !r.Claim.Exclusive {
		n = h
	}
	if r.Paused() {
		n.CPUs, n.IOSlots = 0, 0
	}
	return n
}
```

After `lockHolders`:

```go
// LockBlock is why r can't start for a lock a running job in jobs holds,
// "" when none of its locks is held: the walk's own text.
func LockBlock(jobs []Record, r Record) string { return lockHolders(jobs).blocks(r) }
```

In `admitWith`, replace the line `var line []Record // the jobs that can ever run, in queue order` and the loop after it up to (not including) `if len(line) == 0 {` with:

```go
	// Forced jobs (job controls spec §5.1) start first, in queue order,
	// whatever the fit says; f counts them and they take their locks, so
	// the walk and the starvation guard see them. Only a lock a running
	// job holds, or caps admission can't use, holds one back.
	var rest []Record
	for _, r := range queued {
		switch {
		case !r.Forced:
			rest = append(rest, r)
		case !p.usable():
			d.Waiting[r.ID] = waitOver
		case held.blocks(r) != "":
			d.Waiting[r.ID] = held.blocks(r)
		default:
			f.take(r)
			held.take(r)
			d.Start = append(d.Start, r.ID)
		}
	}

	var line []Record // the jobs that can ever run, in queue order
	for _, r := range rest {
		if r.Parked {
			d.Waiting[r.ID] = WaitParked
			continue
		}
		if r.Sizing != "" {
			d.Waiting[r.ID] = r.Sizing.Wait()
			continue
		}
		if !p.usable() || r.Claim.exceeds(p.Caps) {
			d.Waiting[r.ID] = waitOver
			continue
		}
		if why := held.blocks(r); why != "" {
			// A running job holds its lock: it can't start until that one
			// ends, so it is never the head either.
			d.Waiting[r.ID] = why
			continue
		}
		if why := f.alone(r); why != "" {
			// No running job's end makes room for it, so it can't start in
			// this pass and the guard can't help it: only memory outside
			// the queue coming free can.
			d.Waiting[r.ID] = why
			continue
		}
		line = append(line, r)
	}
```

Add to `admitWith`'s doc comment, after the named-locks paragraph:

```go
// A forced job (Record.Forced, job controls spec §5.1) starts before the
// walk, past every fit, the guard and the over-cap skip, but not past a
// lock a running job holds; a parked one (Record.Parked) waits WaitParked
// and is never the head.
```

`greedyFit.add` (`sched/greedy.go`) becomes:

```go
func (f *greedyFit) add(r Record, pred Prediction) {
	f.any = true
	switch {
	case r.Paused():
		// Memory only (job controls spec §7.2): pred is its use, flat.
		f.preds = append(f.preds, pred)
		return
	case r.Claim.Exclusive:
		f.exclusive = r.ID
		return
	}
	f.cores += r.Claim.CPUs
	if r.Claim.IO {
		f.slots++
	}
	f.preds = append(f.preds, pred)
}
```

At the end of `sched/greedy.go`:

```go
// pausedHorizon is how long a paused job's prediction lasts: longer than
// any candidate's, since only the owner ends a pause.
const pausedHorizon = 30 * 24 * time.Hour

// PausedPrediction is a paused job's prediction from now (job controls
// spec §7.2): flat at usedKB, its measured memory, until the owner resumes
// it.
func PausedPrediction(usedKB int64, now time.Time) Prediction {
	return Flat(usedKB, now, now.Add(pausedHorizon))
}
```

- [ ] **Step 5: Grants** (`sched/grant.go`). In `Grant`, the wait loop becomes:

```go
	for id, why := range dec.Waiting {
		// A parked job waits on the owner, not the caps (job controls
		// spec §7.2), so its wait stops no grant.
		if why != waitOver && why != WaitParked && !agent[id] {
			return g
		}
	}
```

and the start loop's first lines:

```go
	for _, id := range dec.Start {
		r := starting[id]
		if agent[id] || r.Forced {
			// A forced start (job controls spec §5.1) gets its claim and no
			// more.
			continue
		}
		b := belief(r)
```

(removing the old `r := starting[id]` line below it). Add "a forced start gets nothing, and a parked job's wait stops nothing" to `Grant`'s doc comment.

- [ ] **Step 6: The forecast** (`sched/forecast.go`). In `forecastWith`, the running case:

```go
		case r.State == Running && r.Paused():
			// A paused job never ends in the simulation (job controls spec
			// §7.2): only the owner ends a pause.
			sim = append(sim, r)
		case r.State == Running:
```

- [ ] **Step 7: The ladder** (`sched/ladder.go`). Add to `LadderJob` after `Starved`:

```go
	Forced    bool // the owner started it (start now): it goes last, with the starved
```

In `victim`, the comparator's first term becomes `compareBool(a.Starved || a.Forced, b.Starved || b.Forced)`, and its doc comment reads "…one neither the starvation guard nor the owner started before one either did, then the most recently started".

- [ ] **Step 8: The spec's amendments.** In the spec: §5.3's refusals gain "- an exclusive job: `job N is exclusive: it holds the host until it ends`;"; §9's `queue.resumed (job, paused seconds)` becomes `queue.unpaused (job, paused seconds)` with "(the give-back logs `queue.resumed`)"; §5.1 gains "A forced job a pass cannot start (caps admission can't use, a lock a running job took since) stays queued and forced, and the next pass starts it."

- [ ] **Step 9: Run the package's tests**

Run: `gofmt -l sched/; loom job run -d "sched: job controls green" -- sh -c 'CGO_ENABLED=0 go test -count=1 ./sched/... 2>&1 | tail -20'`
Expected: `ok` for `sched` and `sched/advisor`; `gofmt -l` prints nothing.

- [ ] **Step 10: Mutation-check the compound terms**, one at a time, restoring after each (copy the file to `/tmp` first): drop `|| a.Forced` in `victim` (red: `TestLadder_ForcedJobsGoLast`); drop `r.Forced` in Grant's start loop (red: `TestGrant_ParkedWaitsAndForcedStarts`); drop `why != WaitParked` (red: same); drop `n.CPUs, n.IOSlots = 0, 0` (red: `TestAdmit_APausedJobCountsMemoryOnly`); move the forced loop after the line loop (red: `TestAdmit_AForcedJobWaitsOnARunningJobsLock`'s second half).

- [ ] **Step 11: Docs and commit.** In `sched/README.md`'s admission paragraph (the one starting "`Admit(jobs, policy, now)` returns"), add after the over-cap sentence: "A forced job (`Record.Forced`, [job controls spec](../docs/superpowers/specs/2026-11-16-job-controls-design.md)) starts before the walk, past every fit, the guard and the over-cap skip, and waits only on a lock a running job holds (`LockBlock`) or unusable caps; a parked one waits `parked by you` (`WaitParked`) and is never the head. A paused job (`Record.Paused`) counts at its memory alone: its hold or claim's GB in the claim fit, its use (`PausedPrediction`) in the greedy fit, and it never ends in the forecast; `QueuedSince` is the latest of `Submitted`, `Ready` and `Unparked`." In the ladder paragraph: "one the starvation guard or the owner (`Forced`) did not start before one either did".

```bash
git add -- sched/job.go sched/admit.go sched/greedy.go sched/grant.go sched/forecast.go sched/ladder.go sched/control_test.go sched/README.md docs/superpowers/specs/2026-11-16-job-controls-design.md
git commit -m "feat(sched): forced starts, parked jobs and paused jobs in admission, grants, the forecast and the ladder" -- sched/job.go sched/admit.go sched/greedy.go sched/grant.go sched/forecast.go sched/ladder.go sched/control_test.go sched/README.md docs/superpowers/specs/2026-11-16-job-controls-design.md
```

(with the attribution line in the message body).

---

### Task 2: `core`: `ControlJob`, the control step and the model's other parts

**Files:**
- Create: `core/queue_control.go`, `core/queue_control_test.go`
- Modify: `core/iface.go` (after `MoveJobToFront`), `core/loop_core.go` (after `MoveJobToFront`'s forwarder), `core/model.go` (the delivery switch, after `giveBackDone`)
- Modify: `core/queue_watch.go` (the sample pass's tail; the rung reconcile)
- Modify: `core/queue_ladder.go` (`stepLadder`'s job loop), `core/queue_borrow.go` (`stepGiveBack`'s job loop)
- Modify: `core/queue_greedy.go` (`predictRunning`), `core/queue_admit.go` (`startJob`'s log), `core/queue_view.go` (`JobView`, `QueuedSince`, `queueJobView`)
- Regenerate: `core/rpc/methods_gen.go`, `docs/specs/protocol.md`
- Modify: `core/rpc/client_test.go` (the refusal table near line 841), `core/README.md`, `core/CLAUDE.md`

- [ ] **Step 1: Write the failing tests** in `core/queue_control_test.go`, on the ladder's fixture (a fake confiner, a temp cgroup root, real records):

```go
package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

```

The tests call `m.controlJob(c)` for its error: `ControlJob` with `ReqID` 0 only logs a refusal, and the Reply's path is `answerQueueRequest`'s, which `core/rpc/client_test.go`'s refusal table pins (Step 8).

```go
func TestControlJob_StartNowStartsAJobTheFitHoldsBack(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	wqConfig(t, l.m, `{"cpus": 8, "memory_gb": 14, "io_jobs": 2}`)
	l.rec(t, l.ids[0]).Claim = sched.Claim{CPUs: 2, MemoryGB: 12}
	big := wqSubmit(t, l.m, "sleep", "31")
	l.rec(t, big).Claim = sched.Claim{CPUs: 2, MemoryGB: 12}
	wqPass(t, l.m)
	require.Equal(t, sched.Queued, wqRecord(t, l.m, big).State, "12 + 12 GB of claims pass 14")

	require.NoError(t, l.m.controlJob(JobControl{Job: big, Action: JobStart}))
	rec := wqRecord(t, l.m, big)
	assert.Equal(t, sched.Running, rec.State)
	assert.True(t, rec.Forced)
}

func TestControlJob_Refusals(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	run := l.ids[0]
	l.rec(t, run).Locks = []string{"port:4300"}
	q := wqSubmit(t, l.m, "sleep", "31")
	l.rec(t, q).Locks = []string{"port:4300"}
	wqPass(t, l.m)
	cases := []struct {
		name string
		c    JobControl
		want string
	}{
		{"no such job", JobControl{Job: 99, Action: JobStart}, "there is no job 99"},
		{"another session's job", JobControl{Job: q, Owner: 12345, Action: JobPark}, "is not this session's"},
		{"start a running job", JobControl{Job: run, Action: JobStart}, "is running"},
		{"start past a lock", JobControl{Job: q, Action: JobStart}, "waits for lock port:4300"},
		{"park a running job", JobControl{Job: run, Action: JobPark}, "pause it instead"},
		{"unpark a job not parked", JobControl{Job: q, Action: JobUnpark}, "is not parked"},
		{"pause a queued job", JobControl{Job: q, Action: JobPause}, "is not running"},
		{"resume a job not paused", JobControl{Job: run, Action: JobResume}, "is not paused"},
		{"an unknown action", JobControl{Job: q, Action: "nap"}, "unknown action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := l.m.controlJob(tc.c)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	l.rec(t, run).Cgroup = ""
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: run, Action: JobPause}), "runs uncontained")
	l.rec(t, run).Cgroup, l.rec(t, run).Claim.Exclusive = "/t.slice/"+l.rec(t, run).Unit, true
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: run, Action: JobPause}), "is exclusive")
	l.rec(t, run).Claim.Exclusive, l.rec(t, run).Rung = false, sched.RungSqueezed
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: run, Action: JobPause}), "the ladder holds job")
}

func TestControlJob_ParkAndUnpark(t *testing.T) {
	l := newWQLadder(t, 20, 0)
	wqConfig(t, l.m, `{"cpus": 8, "memory_gb": 14, "io_jobs": 2}`)
	id := wqSubmit(t, l.m, "sleep", "31")
	// Over the cores cap, so no pass the requests run starts it.
	l.m.queue.store.Jobs[l.m.queue.index(id)].Claim = sched.Claim{CPUs: 64, MemoryGB: 1}
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPark}))
	assert.True(t, wqRecord(t, l.m, id).Parked)
	wqPass(t, l.m)
	assert.Equal(t, sched.WaitParked, l.m.queue.waiting[id])
	assert.ErrorContains(t, l.m.controlJob(JobControl{Job: id, Action: JobPark}), "parked already")

	before := time.Now()
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobUnpark}))
	rec := wqRecord(t, l.m, id)
	assert.False(t, rec.Parked)
	assert.False(t, rec.Unparked.Before(before), "the starvation wait starts again")
}
```

```go
func TestControlJob_PauseFreezesReclaimsAndResumeThaws(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	id := l.ids[0]
	prev := readSwapFree
	readSwapFree = func() (int64, error) { return 15 << 20, nil }
	t.Cleanup(func() { readSwapFree = prev })
	l.write(t, id, map[string]string{"memory.reclaim": ""})
	wqPass(t, l.m) // a sample, so its working set is known

	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Freeze "+l.rec(t, id).Unit)
	rec := l.rec(t, id)
	require.True(t, rec.Paused())
	assert.True(t, rec.PauseReclaimed)
	assert.True(t, rec.Laddered, "a paused run teaches floors only")
	assert.Empty(t, rec.Control)
	data, err := os.ReadFile(filepath.Join(l.root, rec.Cgroup, "memory.reclaim"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "swappiness=200")

	// The sampler sees it frozen: no rung, so the ladder never thaws it.
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	assert.Empty(t, l.rec(t, id).Rung)
	assert.True(t, l.rec(t, id).Paused())

	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobResume}))
	wqPump(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Thaw "+l.rec(t, id).Unit)
	assert.False(t, l.rec(t, id).Paused())
}

func TestControlJob_PauseWithSwapShortIsCPUOnly(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	id := l.ids[0]
	prev := readSwapFree
	readSwapFree = func() (int64, error) { return 1 << 10, nil } // 1 MB free
	t.Cleanup(func() { readSwapFree = prev })
	l.write(t, id, map[string]string{"memory.reclaim": ""})
	wqPass(t, l.m)
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	require.True(t, l.rec(t, id).Paused())
	assert.False(t, l.rec(t, id).PauseReclaimed)
	data, err := os.ReadFile(filepath.Join(l.root, l.rec(t, id).Cgroup, "memory.reclaim"))
	require.NoError(t, err)
	assert.Empty(t, string(data), "1 MB of swap can't hold its working set: nothing reclaimed")
}

func TestControlJob_AFailedFreezeChangesNothing(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	id := l.ids[0]
	l.fake.fail = map[string]error{"Freeze": errNoBusForTest}
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	rec := l.rec(t, id)
	assert.False(t, rec.Paused())
	assert.Empty(t, rec.Control)
	assert.Contains(t, rec.ControlErr, "pause failed")
}

func TestControlJob_ThawedByHandEndsThePause(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	id := l.ids[0]
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	l.rec(t, id).PausedAt = time.Now().Add(-time.Minute)
	wqPass(t, l.m) // the cgroup reads not frozen, sampled after the pause
	rec := l.rec(t, id)
	assert.False(t, rec.Paused())
	assert.Positive(t, rec.PausedSecs)
}

func TestControlJob_CancelThawsAPausedJob(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	id := l.ids[0]
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	require.NoError(t, l.m.cancelJob(id, "from the work view"))
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Thaw "+l.rec(t, id).Unit)
}

func TestControlJob_TheLadderAndTheGiveBackLeaveAPausedJobAlone(t *testing.T) {
	l := newWQLadder(t, 2, 1) // short
	id := l.ids[0]
	l.rec(t, id).PausedAt, l.rec(t, id).Borrower = time.Now().Add(-time.Minute), true
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	for _, call := range l.fake.Calls() {
		assert.NotContains(t, call, l.rec(t, id).Unit, "no squeeze, freeze, thaw or kill of a paused job")
	}
}

func TestControlJob_APauseOutlastsAnAdoption(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	id := l.ids[0]
	require.NoError(t, l.m.controlJob(JobControl{Job: id, Action: JobPause}))
	wqPump(t, l.m)
	l.m.adoptJobs() // what a restarted daemon's boot runs on a live job
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	assert.True(t, l.rec(t, id).Paused())
	assert.Empty(t, l.rec(t, id).Rung, "still the owner's pause, not the ladder's")
}

func TestControlJob_AParkedDependentStaysParkedWhenReleased(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	first := l.ids[0]
	dep := wqSubmitAfter(t, l.m, first, "sleep", "31")
	require.NoError(t, l.m.controlJob(JobControl{Job: dep.ID, Action: JobPark}))
	ids := l.m.releaseDependents(wqRecord(t, l.m, first), time.Now())
	require.Equal(t, []sched.JobID{dep.ID}, ids)
	rec := wqRecord(t, l.m, dep.ID)
	assert.Equal(t, sched.Queued, rec.State)
	assert.True(t, rec.Parked, "the release leaves the park alone")
	l.m.admitJobs()
	assert.Equal(t, sched.WaitParked, l.m.queue.waiting[dep.ID])
}

func TestControlJob_TheReplyCarriesTheRefusal(t *testing.T) {
	l := newWQLadder(t, 20, 0)
	l.m.ControlJob(JobControl{Job: 99, Action: JobPark}, 7)
	r := requestReply(t, l.m, 7)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.ErrorContains(t, r.Err, "there is no job 99")
}

func TestQueueView_APausedJobsOverdueClockStandsStill(t *testing.T) {
	l := newWQLadder(t, 20, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.PausedSecs = 600
	v := l.m.queueJobView(l.m.queue, *rec, nil)
	plain := v.OverdueAfter
	rec.PausedSecs = 0
	if base := l.m.queueJobView(l.m.queue, *rec, nil).OverdueAfter; base > 0 {
		assert.Equal(t, base+10*time.Minute, plain)
	}
}
```

The overdue test needs a belief with an overdue mark to bite; if `OverdueAfter` is 0 for the fixture's `sleep 30`, give its signature three runs through `wqCurved` (`core/queue_greedy_test.go`) before reading the view.

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "core: job controls red" -- sh -c 'CGO_ENABLED=0 go test -count=1 -run "TestControlJob_|TestQueueView_APaused" ./core/ 2>&1 | tail -20'`
Expected: build failure naming `JobControl`, `JobStart`, `controlJob`, `ControlJob`.

- [ ] **Step 3: The request** (`core/queue_control.go`):

```go
package core

import (
	"context"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// JobAction is what a ControlJob does to its job (job controls spec §5).
type JobAction string

// The job controls.
const (
	JobStart  JobAction = "start"  // start a queued job now, past the fit
	JobPark   JobAction = "park"   // keep a queued job from starting
	JobUnpark JobAction = "unpark" // let a parked job start again
	JobPause  JobAction = "pause"  // freeze a running job, its memory to swap when it fits
	JobResume JobAction = "resume" // thaw a paused job
)

// JobControl is one ControlJob: the job, the session that must own it (0
// for any: the work view covers the host), and the action.
type JobControl struct {
	Job    sched.JobID
	Owner  InstanceID
	Action JobAction
}

// ControlJob carries out a job control (job controls spec §5), answered at
// once; a pause or resume is accepted then, and the job shows how it went.
func (m *Model) ControlJob(c JobControl, req ReqID) {
	m.answerQueueRequest(req, m.controlJob(c))
}

// controlJob checks c against its job and changes the record: start, park
// and unpark at once, followed by an admission pass; pause and resume as
// an intent the control step carries out. A failed save puts the record
// back and refuses.
func (m *Model) controlJob(c JobControl) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	i := q.index(c.Job)
	if i < 0 {
		return fmt.Errorf("there is no job %d", c.Job)
	}
	rec := &q.store.Jobs[i]
	if c.Owner != 0 && InstanceID(rec.Owner) != c.Owner {
		return fmt.Errorf("job %d is not this session's", c.Job)
	}
	prev := *rec
	var err error
	switch c.Action {
	case JobStart:
		err = m.forceJob(rec)
	case JobPark:
		err = parkJob(rec)
	case JobUnpark:
		err = unparkJob(rec, time.Now())
	case JobPause:
		err = m.pauseJob(rec)
	case JobResume:
		err = m.resumeJob(rec)
	default:
		return fmt.Errorf("job %d: unknown action %q", c.Job, c.Action)
	}
	if err != nil {
		return err
	}
	if err := m.saveQueue(); err != nil {
		*rec = prev
		return err
	}
	q.moved()
	log.For("core").Info("queue.controlled", "job", int64(c.Job), "action", string(c.Action), "owner_given", c.Owner != 0)
	switch c.Action {
	case JobStart, JobUnpark:
		m.admitJobs()
	case JobPause, JobResume:
		m.stepJobControls(time.Now())
	}
	return nil
}

// unclaimedRefusal is why an agent or run job takes no control, "" for any
// other job.
func unclaimedRefusal(rec *sched.Record) string {
	switch rec.Kind {
	case sched.KindAgent:
		return fmt.Sprintf("job %d is an agent job: it starts by its account's agent slots", rec.ID)
	case sched.KindRun:
		return fmt.Sprintf("job %d is a flow run: it starts at once", rec.ID)
	}
	return ""
}

// forceJob marks queued job rec forced (job controls spec §5.1), clearing a
// park; the pass after it starts it.
func (m *Model) forceJob(rec *sched.Record) error {
	switch {
	case rec.State == sched.Running:
		return fmt.Errorf("job %d is running", rec.ID)
	case rec.State == sched.Held:
		return fmt.Errorf("job %d waits for job %d to end", rec.ID, rec.After)
	case rec.State != sched.Queued:
		return fmt.Errorf("job %d has ended", rec.ID)
	case unclaimedRefusal(rec) != "":
		return fmt.Errorf("%s", unclaimedRefusal(rec))
	}
	if _, paused := m.refreshPolicy(); paused != "" {
		return fmt.Errorf("admission paused: %s", paused)
	}
	if why := sched.LockBlock(m.queue.store.Jobs, *rec); why != "" {
		return fmt.Errorf("job %d %s", rec.ID, why)
	}
	rec.Forced, rec.Parked = true, false
	return nil
}

// parkJob parks queued or held job rec (job controls spec §5.2).
func parkJob(rec *sched.Record) error {
	switch {
	case rec.State == sched.Running:
		return fmt.Errorf("job %d is running: pause it instead", rec.ID)
	case !rec.State.Waiting():
		return fmt.Errorf("job %d has ended", rec.ID)
	case rec.Parked:
		return fmt.Errorf("job %d is parked already", rec.ID)
	}
	rec.Parked = true
	return nil
}

// unparkJob unparks rec at now, restarting its starvation wait.
func unparkJob(rec *sched.Record, now time.Time) error {
	if !rec.Parked {
		return fmt.Errorf("job %d is not parked", rec.ID)
	}
	rec.Parked, rec.Unparked = false, now
	return nil
}

// pauseJob asks the control step to pause running job rec (job controls
// spec §5.3), refusing what it can't freeze or what the ladder or the
// give-back holds.
func (m *Model) pauseJob(rec *sched.Record) error {
	q := m.queue
	switch {
	case rec.State != sched.Running:
		return fmt.Errorf("job %d is not running", rec.ID)
	case unclaimedRefusal(rec) != "":
		return fmt.Errorf("job %d is an agent job", rec.ID)
	case rec.Cgroup == "":
		return fmt.Errorf("job %d runs uncontained: there is no scope to freeze", rec.ID)
	case q.confiner == nil:
		return fmt.Errorf("job %d can't be paused: the queue has no systemd bus", rec.ID)
	case rec.Claim.Exclusive:
		return fmt.Errorf("job %d is exclusive: it holds the host until it ends", rec.ID)
	case rec.Rung != "":
		return fmt.Errorf("the ladder holds job %d (%s)", rec.ID, rec.Rung)
	case !rec.LentAt.IsZero():
		return fmt.Errorf("job %d is lent to give memory back", rec.ID)
	case rec.Requeue != "":
		return fmt.Errorf("job %d is being requeued", rec.ID)
	case rec.CancelReason != "":
		return fmt.Errorf("job %d is being cancelled", rec.ID)
	case rec.Control == sched.ControlPause:
		return fmt.Errorf("job %d is pausing", rec.ID)
	case rec.Control == sched.ControlResume:
		return fmt.Errorf("job %d is resuming", rec.ID)
	case rec.Paused():
		return fmt.Errorf("job %d is already paused", rec.ID)
	}
	rec.Control, rec.ControlErr = sched.ControlPause, ""
	return nil
}

// resumeJob asks the control step to resume paused job rec.
func (m *Model) resumeJob(rec *sched.Record) error {
	switch {
	case rec.Control == sched.ControlResume:
		return fmt.Errorf("job %d is resuming", rec.ID)
	case rec.State != sched.Running || !rec.Paused():
		return fmt.Errorf("job %d is not paused", rec.ID)
	case m.queue.confiner == nil:
		return fmt.Errorf("job %d can't be resumed: the queue has no systemd bus", rec.ID)
	}
	rec.Control, rec.ControlErr = sched.ControlResume, ""
	return nil
}

// jobControlDone is a control step's call that returned.
type jobControlDone struct {
	job        sched.JobID
	kind       sched.ControlKind
	at         time.Time
	err        error // the freeze's or thaw's
	reclaimed  bool
	reclaimKB  int64
	reclaimErr error
}

// stepJobControls carries out one pause or resume (job controls spec §6.2),
// one confinement call at a time with the ladder's and the give-back's: the
// first running job with a Control, or a cancelled paused job, which is
// thawed so it can act on its SIGTERM.
func (m *Model) stepJobControls(now time.Time) {
	q := m.queue
	if q.laddering || q.confiner == nil {
		return
	}
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State != sched.Running || rec.Cgroup == "" {
			continue
		}
		kind := rec.Control
		if kind == "" && rec.Paused() && rec.CancelReason != "" {
			kind = sched.ControlResume
		}
		if kind != "" {
			m.applyJobControl(i, kind, now)
			return
		}
	}
}

// applyJobControl spawns job i's call: a pause freezes the scope and, when
// SwapFree covers its working set, reclaims it; a resume thaws it.
func (m *Model) applyJobControl(i int, kind sched.ControlKind, now time.Time) {
	q := m.queue
	rec := q.store.Jobs[i]
	c, root, unit, cg, kb := q.confiner, q.cgroupRoot, rec.Unit, rec.Cgroup, q.usage[rec.ID].RSSKB
	swapKB, swapErr := readSwapFree()
	reclaim := kind == sched.ControlPause && swapErr == nil && kb > 0 && swapKB >= kb
	q.laddering = true
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		done := jobControlDone{job: rec.ID, kind: kind, at: now}
		switch kind {
		case sched.ControlPause:
			if done.err = c.Freeze(ctx, unit); done.err == nil && reclaim {
				done.reclaimErr = sched.CgroupReclaim(root, cg, kb)
				done.reclaimed, done.reclaimKB = done.reclaimErr == nil, kb
			}
		case sched.ControlResume:
			done.err = c.Thaw(ctx, unit)
		}
		return done
	})
}

// deliverJobControlDone records a control call that returned. A failure
// leaves the job as it was, with ControlErr on its row; a pause stamps
// PausedAt when the freeze returned (the reconcile's thawed-by-hand case
// trusts only samples after it).
func (m *Model) deliverJobControlDone(r jobControlDone) {
	q := m.queue
	q.laddering = false
	i := q.index(r.job)
	if i < 0 || q.store.Jobs[i].State != sched.Running {
		return
	}
	rec := &q.store.Jobs[i]
	rec.Control = ""
	if r.err != nil {
		rec.ControlErr = fmt.Sprintf("%s failed: %v", r.kind, r.err)
		log.For("core").Warn("queue.control_failed", "job", int64(rec.ID), "action", string(r.kind), "err", r.err)
		q.moved()
		m.saveQueueQuiet("control")
		return
	}
	rec.ControlErr = ""
	switch r.kind {
	case sched.ControlPause:
		rec.PausedAt, rec.PauseReclaimed, rec.Laddered = time.Now(), r.reclaimed, true
		log.For("core").Info("queue.paused", "job", int64(rec.ID), "reclaimed", r.reclaimed, "reclaimed_kb", r.reclaimKB)
		if r.reclaimErr != nil {
			log.For("core").Warn("queue.reclaim_failed", "job", int64(rec.ID), "err", r.reclaimErr)
		}
	case sched.ControlResume:
		paused := time.Since(rec.PausedAt)
		rec.PausedSecs += int64(paused.Seconds())
		rec.PausedAt, rec.PauseReclaimed = time.Time{}, false
		log.For("core").Info("queue.unpaused", "job", int64(rec.ID), "paused_s", int64(paused.Seconds()))
	}
	q.moved()
	m.saveQueueQuiet("control")
	if q.hasQueued() {
		m.admitJobs() // a pause makes room
	}
}
```

- [ ] **Step 4: Wire it.** `core/iface.go`, after `MoveJobToFront`:

```go
	ControlJob(c JobControl, req ReqID)
```

and in the comment block above the queue's methods add "ControlJob starts, parks, unparks, pauses or resumes a job (job controls spec)". `core/loop_core.go`, after `MoveJobToFront`'s forwarder:

```go
// ControlJob runs Model.ControlJob on the loop.
func (l *Loop) ControlJob(c JobControl, req ReqID) {
	l.do(func(m *Model) { m.ControlJob(c, req) })
}
```

`core/model.go`'s delivery switch, after `case giveBackDone:`:

```go
	case jobControlDone:
		m.deliverJobControlDone(msg)
```

`core/queue_watch.go`, after `m.stepGiveBack(time.Now())` at the sample pass's end:

```go
	m.stepJobControls(time.Now())
```

- [ ] **Step 5: The reconcile** (`core/queue_watch.go`, the block starting `if cg := ob.cg; cg != nil && !q.laddering {`). Replace from `lent := !rec.LentAt.IsZero()` to the `switch` with:

```go
			lent := !rec.LentAt.IsZero()
			if lent && !cg.Frozen && r.at.After(rec.LentAt) {
				// Thawed by hand: a running borrower again (memory
				// borrowing spec §10).
				rec.LentSecs += int64(r.at.Sub(rec.LentAt).Seconds())
				rec.LentAt, lent = time.Time{}, false
				q.moved()
			}
			paused := rec.Paused()
			if paused && !cg.Frozen && rec.Control == "" && r.at.After(rec.PausedAt) {
				// Thawed outside loom: the pause ends as a resume's would
				// (job controls spec §6.3).
				rec.PausedSecs += int64(r.at.Sub(rec.PausedAt).Seconds())
				rec.PausedAt, rec.PauseReclaimed, paused = time.Time{}, false, false
				log.For("core").Info("queue.unpaused_by_hand", "job", int64(rec.ID))
				q.moved()
			}
			// The cgroup decides the rung (cgroup spec §15, 17): frozen is
			// paused, unless the job is lent (memory borrowing spec §7.3)
			// or the owner paused it (job controls spec §6.3), a
			// memory.high is squeezed, neither is none.
			next := sched.Rung("")
			switch {
			case cg.Frozen && (lent || paused || rec.Control == sched.ControlPause):
```

(the remaining cases of the switch stay as they are).

- [ ] **Step 6: The ladder's and the give-back's skips.** `core/queue_ladder.go`, `stepLadder`'s loop condition becomes:

```go
		// A lent job is the give-back machine's (memory borrowing spec
		// §7.3), and a paused one, or one with a control in flight, the
		// owner's (job controls spec §7.1).
		if rec.State != sched.Running || rec.Cgroup == "" || rec.Requeue != "" || !rec.LentAt.IsZero() || rec.Paused() || rec.Control != "" {
			continue
		}
```

and the `sched.LadderJob{…}` literal gains `Forced: rec.Forced,`. `core/queue_borrow.go`, `stepGiveBack`'s loop condition:

```go
		if rec.State != sched.Running || !rec.Borrower || rec.Cgroup == "" || rec.Requeue != "" || rec.Paused() || rec.Control != "" {
			continue
		}
```

with the comment "a paused borrower is the owner's (job controls spec §7.2): a requeue would lose the work the pause keeps".

- [ ] **Step 7: Predictions, the start log and the view.** `core/queue_greedy.go`, at the top of `predictRunning`'s body:

```go
	q := m.queue
	if rec.Paused() {
		// Flat at its use until the owner resumes it (job controls spec
		// §7.2); its claim before a sample.
		used := q.usage[rec.ID].RSSKB
		if used <= 0 {
			used = int64(rec.Claim.MemoryGB) * kbPerGB
		}
		return sched.PausedPrediction(used, now)
	}
	// A lent job's progress stood still while it was frozen (memory
	// borrowing spec §6.4), and a paused one's (job controls spec §7.3).
	end := rec.Started.Add(expect(rec) + rec.LentFor(now) + rec.PausedFor(now))
```

(replacing the existing `q := m.queue`, comment and `end :=` lines). `core/queue_admit.go`, `startJob`'s log gains `"forced", rec.Forced`. `core/queue_view.go`: `JobView` gains, after `Locks`:

```go
	// Job controls (spec 2026-11-16-job-controls §8): started by the owner
	// past the queue, parked, the last unpark, paused since (zero unless
	// paused) and whether its memory went to swap, a pause or resume in
	// flight, and the last failed one's error.
	Forced         bool
	Parked         bool
	Unparked       time.Time
	PausedAt       time.Time
	PauseReclaimed bool
	Control        sched.ControlKind
	ControlErr     string
```

`JobView.QueuedSince` takes the same latest-of form as the record's (Submitted, Ready, Unparked). `queueJobView`'s literal gains `Forced: r.Forced, Parked: r.Parked, Unparked: r.Unparked, PausedAt: r.PausedAt, PauseReclaimed: r.PauseReclaimed, Control: r.Control, ControlErr: r.ControlErr,`, and after `jv.OverdueAfter = sched.OverdueAfter(b, r.Claim.CPUs)`:

```go
	if jv.OverdueAfter > 0 {
		// Paused time is no running time (job controls spec §7.3).
		jv.OverdueAfter += r.PausedFor(time.Now())
	}
```

If `queueJobView` takes a `now` already, use it instead of `time.Now()`.

- [ ] **Step 8: The wire.**

Run: `loom job run -d "core/rpc: generate ControlJob" -- sh -c 'go generate ./core/rpc && CGO_ENABLED=0 go test -count=1 ./core/rpc -run TestProtocolReference -update'`
Then add a row to the refusal table in `core/rpc/client_test.go` (near line 841): `{"a control of a job that doesn't exist", func(req core.ReqID) { c.ControlJob(core.JobControl{Job: 99, Action: core.JobPark}, req) }, "there is no job 99"},`.

- [ ] **Step 9: Run the package's tests**

Run: `gofmt -l core/; loom job run -d "core: job controls green" -- sh -c 'CGO_ENABLED=0 go test -count=1 ./core/... 2>&1 | grep -v "^ok\|no test files" | tail -40'`
Expected: no FAIL lines. Fix what fails before going on; the existing ladder and borrow tests must stay green.

- [ ] **Step 10: Docs and commit.** `core/README.md`: a sentence in the queue section naming `ControlJob` and `stepJobControls` (one confinement call at a time with the ladder and the give-back, `q.laddering`). `core/CLAUDE.md`, under "Requests, jobs and completions", a rule (check headroom with `go run ./tools/claudemd -v` first):

```markdown
- **Run every freeze, thaw, squeeze, reclaim and cgroup kill through `q.laddering`'s one-at-a-time slot (the ladder, the give-back and `stepJobControls`), and keep a paused job (`Record.Paused`, `Control`) out of the ladder's and the give-back's victims.** Two calls in flight race on one scope, and a ladder that took a paused job would thaw it when memory calms (the sampler reads frozen as the ladder's rung otherwise). **Enforced** by `TestControlJob_TheLadderAndTheGiveBackLeaveAPausedJobAlone` and `TestControlJob_PauseFreezesReclaimsAndResumeThaws`.
```

```bash
git add -- core/queue_control.go core/queue_control_test.go core/iface.go core/loop_core.go core/model.go core/queue_watch.go core/queue_ladder.go core/queue_borrow.go core/queue_greedy.go core/queue_admit.go core/queue_view.go core/rpc/methods_gen.go core/rpc/client_test.go docs/specs/protocol.md core/README.md core/CLAUDE.md
git commit -m "feat(core,rpc): ControlJob: start now, park, pause and resume a job" -- <the same paths>
```

---

### Task 3: The TUI work view and `loom job list`

**Files:**
- Modify: `app/work.go` (`handleWorkKey`, `workModelKeys`, `workHint`, three new key functions)
- Modify: `ui/work.go` (`workRowFacts`)
- Modify: `internal/cli/job_list.go` (the WAITS FOR cell)
- Test: `app/work_test.go`, `ui/work_test.go`, `internal/cli/job_list_test.go` (or the file `grep -ln "WAITS FOR" internal/cli/*_test.go` names)
- Modify: `USAGE.md` (the work view's key table; `loom job list`)

- [ ] **Step 1: Write the failing tests.** In `app/work_test.go`, beside the cancel and front tests, add to the `workWatch` fake (near line 636):

```go
// ControlJob records a control the work view sent.
func (w *workWatch) ControlJob(c core.JobControl, req core.ReqID) {
	w.controls = append(w.controls, c)
}
```

(with a `controls []core.JobControl` field on `workWatch`), and tests that, with the view open on a queued job, `s` opens a confirmation whose text contains the job's wait reason and, confirmed, sends `JobControl{Job: id, Action: core.JobStart}`; `h` on a queued job sends `JobPark`, on a parked one `JobUnpark`; `p` on a running job sends `JobPause`, on a paused one (`PausedAt` set) `JobResume`; `s` on a running job and `p` on a queued one send nothing and say why on the info line; offline, `s`, `h` and `p` are refused like `x`. Model them on the existing `x` and `t` tests in the same file (copy their fixture setup: `grep -n "func TestWork.*Cancel\|func TestWork.*Front" app/work_test.go`).

In `ui/work_test.go`, row tests: a running job with `PausedAt` 3 minutes ago shows `paused 3m`, with `PauseReclaimed` false `paused 3m, CPU only`; `Control` pause shows `pausing…`; `ControlErr` shows its text; `Forced` shows `forced`.

In the CLI list test: a running job with `PausedAt` set shows `paused by you` in WAITS FOR, with `, CPU only` when `PauseReclaimed` is false.

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "tui: job controls red" -- sh -c 'CGO_ENABLED=0 go test -count=1 ./app/ ./ui/ ./internal/cli/ 2>&1 | grep -E "^(--- FAIL|FAIL|ok)|undefined" | head -30'`
Expected: FAIL / undefined for the new keys and texts.

- [ ] **Step 3: The keys** (`app/work.go`). In `handleWorkKey`'s switch, after `case "t":`:

```go
	case "s":
		return m, m.askStartWorkJob()
	case "h":
		m.parkWorkJob()
	case "p":
		m.pauseWorkJob()
```

`workModelKeys` gains `"s": true, "h": true, "p": true`. `workHint`'s default line becomes `"w/esc back · enter log · b belief · x cancel · t to front · s start now · h park · p pause · o open session · e caps · q quit"`. The functions, after `frontWorkJob`:

```go
// askStartWorkJob is s: a confirm for starting the selected queued job now,
// past the fit (job controls spec §8). The prompt names why it waits and an
// exclusive job it would share the host with; the start goes to the job's
// ID whatever is selected when the answer comes, and the model
// re-validates.
func (m *home) askStartWorkJob() tea.Cmd {
	job, ok := m.workSelected()
	if !ok {
		return nil
	}
	if job.State != sched.Queued {
		m.errBox.SetInfo(fmt.Sprintf("job %d isn't queued", job.ID))
		return nil
	}
	id := job.ID
	return m.confirmTask(startPrompt(job, m.queue.Jobs), overlay.ConfirmationTask{Sync: func() {
		m.core.ControlJob(core.JobControl{Job: id, Action: core.JobStart}, m.newReq(pendingReq{work: &pendingWork{}}))
	}})
}

// startPrompt is s's question: the job, why it waits, and the exclusive job
// running beside it, if one is.
func startPrompt(job core.JobView, jobs []core.JobView) string {
	q := "Start " + ui.JobLabel(job) + " now?"
	if job.Waiting != "" {
		q += " It waits: " + job.Waiting + "."
	}
	for _, j := range jobs {
		if j.State == sched.Running && j.Claim.Exclusive {
			q += fmt.Sprintf(" Exclusive job %d is running.", j.ID)
			break
		}
	}
	return q
}

// parkWorkJob is h: park the selected waiting job, or unpark it.
func (m *home) parkWorkJob() {
	job, ok := m.workSelected()
	if !ok {
		return
	}
	if !job.State.Waiting() {
		m.errBox.SetInfo(fmt.Sprintf("job %d isn't waiting", job.ID))
		return
	}
	action := core.JobPark
	if job.Parked {
		action = core.JobUnpark
	}
	m.core.ControlJob(core.JobControl{Job: job.ID, Action: action}, m.newReq(pendingReq{work: &pendingWork{}}))
}

// pauseWorkJob is p: pause the selected running job, or resume it.
func (m *home) pauseWorkJob() {
	job, ok := m.workSelected()
	if !ok {
		return
	}
	if job.State != sched.Running {
		m.errBox.SetInfo(fmt.Sprintf("job %d isn't running", job.ID))
		return
	}
	action := core.JobPause
	if !job.PausedAt.IsZero() {
		action = core.JobResume
	}
	m.core.ControlJob(core.JobControl{Job: job.ID, Action: action}, m.newReq(pendingReq{work: &pendingWork{}}))
}
```

- [ ] **Step 4: The rows** (`ui/work.go`, `workRowFacts`'s running case), after the lent/borrowing switch:

```go
		switch {
		case j.Control == sched.ControlPause:
			extra += "  pausing…"
		case j.Control == sched.ControlResume:
			extra += "  resuming…"
		case !j.PausedAt.IsZero():
			extra += "  paused " + sched.RoughDuration(now.Sub(j.PausedAt))
			if !j.PauseReclaimed {
				extra += ", CPU only"
			}
		}
		if j.ControlErr != "" {
			extra += "  " + j.ControlErr
		}
		if j.Forced {
			extra += "  forced"
		}
```

A parked queued job's row needs nothing: its `Waiting` is `parked by you`.

- [ ] **Step 5: The CLI list** (`internal/cli/job_list.go`), after `if j.State == sched.Running && j.Explored {…}`:

```go
		if j.State == sched.Running && !j.PausedAt.IsZero() {
			waits = "paused by you"
			if !j.PauseReclaimed {
				waits += ", CPU only"
			}
		}
```

- [ ] **Step 6: Run the tests**

Run: `gofmt -l app/ ui/ internal/cli/; loom job run -d "tui: job controls green" -- sh -c 'CGO_ENABLED=0 go test -count=1 ./app/ ./ui/ ./internal/cli/ 2>&1 | grep -v "^ok\|no test files" | tail -30'`
Expected: no FAIL lines (`TestOfflineKeyGate` included).

- [ ] **Step 7: Docs and commit.** `USAGE.md`'s work view table gains, after `t`:

```markdown
| `s` | Start the selected queued job now, after a confirmation that says why it waits: past the memory and CPU fit, the starvation wait and the caps, but never past a named lock a running job holds, a paused admission or an unfinished `--after` job. The ladder takes a forced job last when memory runs short |
| `h` | Park the selected waiting job (it keeps its place but never starts, `parked by you`), or unpark it; an unparked job's starvation wait starts again |
| `p` | Pause the selected running job (its scope frozen, its memory pushed to swap when swap has room for it, else CPU only), or resume it. Only a contained job that the ladder isn't squeezing, pausing or lending can be paused; a paused job keeps its locks and the ladder leaves it alone |
```

and `loom job list`'s description notes `parked by you` and `paused by you`.

```bash
git add -- app/work.go app/work_test.go ui/work.go ui/work_test.go internal/cli/job_list.go <the CLI test file> USAGE.md
git commit -m "feat(app,ui,cli): the work view's start now, park and pause keys" -- <the same paths>
```

---

### Task 4: The phone

**Files:**
- Modify: `internal/web/api.go` (`routeAPI`; a `jobControl` handler)
- Modify: `internal/web/jobs.go` (`jobJSON`, `jobOf`)
- Modify: `internal/web/static/js/job.mjs` (buttons, the sheet)
- Test: `internal/web/api_test.go` or `internal/web/jobs_test.go` (whichever holds `readJobLog`'s tests: `grep -ln "jobs/.*log" internal/web/*_test.go`)
- Modify: `internal/web/README.md` (the API table; the job JSON), `e2e/web_test.go` only if it covers job routes already

- [ ] **Step 1: Write the failing tests**, beside the job log route's tests and on their fixture (a booted model, the real `rpc.Server`, `httptest`): `POST /api/v1/sessions/{id}/jobs/{job}/park` on the session's queued job answers 200 and the job reads `parked`; the same on another session's job answers 409 with `is not this session's`; `…/nap` answers 404; `…/pause` on a queued job answers 409 with `is not running`; the session's jobs in the snapshot carry `"parked": true`; an unpaired device gets 401.

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "web: job controls red" -- sh -c 'CGO_ENABLED=0 go test -count=1 ./internal/web/ 2>&1 | grep -E "^(--- FAIL|FAIL|ok)" | head -20'`
Expected: FAIL (404 for the route, no `parked` field).

- [ ] **Step 3: The route** (`internal/web/api.go`). In `routeAPI`, after the job log route:

```go
	m.Handle("POST /api/v1/sessions/{id}/jobs/{job}/{action}", l.authed(l.jobControl))
```

and the handler, beside `sessionOp`:

```go
// jobActions are the job controls the app may ask for (job controls spec
// §8); any other action is 404.
var jobActions = map[string]core.JobAction{
	"start": core.JobStart, "park": core.JobPark, "unpark": core.JobUnpark,
	"pause": core.JobPause, "resume": core.JobResume,
}

// jobControl is POST /api/v1/sessions/{id}/jobs/{job}/{action}: a control
// of one of the session's jobs. The model checks the job is the session's
// and refuses what the action can't do.
func (l *Layer) jobControl(w http.ResponseWriter, r *http.Request, device string) {
	action, ok := jobActions[r.PathValue("action")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	a := actionLog{device: device, name: "job " + string(action)}
	id, ok := l.sessionFromPath(w, r, a)
	if !ok {
		return
	}
	a.sessionID = id
	job, err := strconv.ParseInt(r.PathValue("job"), 10, 64)
	if err != nil || job < 1 {
		l.refuse(w, a, http.StatusBadRequest, "job: want a decimal job ID")
		return
	}
	l.doAction(w, r, a, func(req core.ReqID) {
		l.client.ControlJob(core.JobControl{Job: sched.JobID(job), Owner: id, Action: action}, req)
	})
}
```

Match `readJobLog`'s parsing of `{job}` if it already has a helper (`grep -n "PathValue(\"job\")" internal/web/*.go`), and its 404 for an unknown session. Check that the mux accepts this pattern beside `GET …/jobs/{job}/log` (the methods differ, so the patterns don't overlap; a panic at registration would say otherwise).

- [ ] **Step 4: The JSON** (`internal/web/jobs.go`). `jobJSON` gains (fields added, none renamed):

```go
	Parked         bool   `json:"parked"`
	Paused         bool   `json:"paused"`
	PauseReclaimed bool   `json:"pause_reclaimed"`
	Forced         bool   `json:"forced"`
	Control        string `json:"control"`
	ControlError   string `json:"control_error"`
```

and `jobOf` sets `Parked: j.Parked, Paused: !j.PausedAt.IsZero(), PauseReclaimed: j.PauseReclaimed, Forced: j.Forced, Control: string(j.Control), ControlError: j.ControlErr,`. None of them moves with the samples, so the projection's no-republish rule holds.

- [ ] **Step 5: The buttons** (`internal/web/static/js/job.mjs`). Import `useRun` from `./reply.mjs` and `ConfirmSheet` from `./sheets.mjs`; `Job` takes `{ snap, stale, route, act, toast }`. Inside `Job`, after the state hooks:

```js
  const { busy, run } = useRun(act, toast, route.id);
  const [asking, setAsking] = useState(false);
  const control = (action) => run("jobs/" + route.job + "/" + action);
```

and in the facts block, after the agent line:

```js
            ${controls(j, stale || busy, control, () => setAsking(true))}
```

with, at module level:

```js
// controls are the buttons job j's state allows (job controls spec §8):
// start now and park or unpark while it is queued, park or unpark while it
// is held, pause or resume while it runs. A control in flight shows as text.
function controls(j, off, control, askStart) {
  if (j.control) return html`<div class="sc-dim sc-small">${j.control === "pause" ? "Pausing…" : "Resuming…"}</div>`;
  const buttons = [];
  if (j.state === "queued") buttons.push(html`<button class="sc-btn sc-primary" type="button" disabled=${off} onClick=${askStart}>Start now</button>`);
  if (j.state === "queued" || j.state === "held") {
    buttons.push(j.parked
      ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => control("unpark")}>Unpark</button>`
      : html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => control("park")}>Park</button>`);
  }
  if (j.state === "running") {
    buttons.push(j.paused
      ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => control("resume")}>Resume</button>`
      : html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => control("pause")}>Pause</button>`);
  }
  if (!buttons.length) return null;
  return html`
    <div class="sc-job-controls">${buttons}</div>
    ${j.control_error ? html`<div class="sc-dim sc-small">${j.control_error}</div>` : null}
  `;
}
```

and the sheet, at the end of the screen's section:

```js
      ${asking && j
        ? html`<${ConfirmSheet}
            title=${"Start job " + j.id + " now?"}
            body=${j.waiting ? "It waits: " + j.waiting + ". It starts past the queue's fit; the ladder takes it last if memory runs short." : "It starts past the queue's fit."}
            confirm="Start now"
            onConfirm=${async () => { await control("start"); setAsking(false); }}
            onCancel=${() => setAsking(false)}
          />`
        : null}
```

`stateText` gains the pause: for a running job, `[ "running", elapsed(…), j.paused ? (j.pause_reclaimed ? "paused" : "paused, CPU only") : "", j.forced ? "forced" : "" ].filter(Boolean).join(" · ")`. In `app.css`, `.sc-job-controls { display: flex; gap: 8px; margin-top: 8px; }` (no `style` attribute: the CSP forbids it). Check that `screens.mjs` passes `act`, `toast` and `stale` to every screen (its header comment says it does).

- [ ] **Step 6: Check and run**

Run: `for f in internal/web/static/js/*.mjs; do node --check "$f" || echo "BAD $f"; done` (no output), then `loom job run -d "web: job controls green" -- sh -c 'CGO_ENABLED=0 go test -count=1 ./internal/web/ ./tools/webshot/... 2>&1 | grep -v "^ok\|no test files" | tail -20'` (no FAIL lines). Then `loom job run -d "webshot job screen" -- go run ./tools/webshot -out /tmp/webshot-jobs -screens job` if the fixture has a job screen (`grep -n "job" tools/webshot/canned.go`); look at the PNG.

- [ ] **Step 7: Docs and commit.** `internal/web/README.md`'s API table gains the route (`POST /api/v1/sessions/{id}/jobs/{job}/{action}`, `action` one of `start`, `park`, `unpark`, `pause`, `resume`, 404 otherwise; 409 with the model's text), and the job JSON's list its six fields.

```bash
git add -- internal/web/api.go internal/web/jobs.go internal/web/static/js/job.mjs internal/web/static/app.css <the web test file> internal/web/README.md
git commit -m "feat(web): start now, park and pause a job from the phone" -- <the same paths>
```

---

### Final gates (after Task 4)

- [ ] `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')` prints nothing.
- [ ] `loom job run -d "job controls: vet + full suite" -- sh -c 'CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test -count=1 ./... 2>&1 | grep -v "^ok\|no test files" | tail -60'` (background): no FAIL lines.
- [ ] `loom job run -d "job controls: race" -- sh -c 'CC=clang CGO_ENABLED=1 go test -race -count=1 ./sched/... ./core/... ./app/ ./internal/web/ 2>&1 | tail -20'`: all ok.
- [ ] `loom job run -d "job controls: e2e" -- go test -tags e2e ./e2e/...` (needs tmux; runs on a private server): ok.
- [ ] `GOOS=windows CGO_ENABLED=0 go build ./...` only if a task touched a syscall, process control or socket file (none is planned).
- [ ] golangci-lint: skipped locally (the dev shell's v2 rejects the repo's config); say so in the report.
- [ ] `go run ./tools/claudemd -v`: the budgets hold.

# Memory Borrowing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. The owner chose inline execution for this plan.

**Goal:** Let a job that greedy admission's firm fit holds back start as a borrower against what the firm jobs leave unused now, and give that memory back (requeue, or freeze plus `memory.reclaim` into zram) when memory gets tight, behind `scheduler.borrow`, off by default.

**Architecture:** Two new pure functions in `sched`: `Borrow` runs after `AdmitGreedy` in each admission pass, and `GiveBack` runs after each usage sample beside `Ladder`. `core` wires them in. Admission starts borrowers, and a give-back driver carries out the actions off the loop, sharing the ladder's one-action-in-flight flag. Records carry the state in new fields, never in `Rung`. Spec: `docs/superpowers/specs/2026-11-06-memory-borrowing-design.md`.

**Tech Stack:** Go 1.25, cgroup v2 files, go-systemd D-Bus (Freeze/Thaw), testify.

**Rules for every package:**
- Run every build and test through `loom job run -d "<what>" -- <cmd>`, using `CGO_ENABLED=0`.
- Commit by path (`git commit -- <paths>`), never with `-a`.
- Keep scratch files in the scratchpad.

---

## File structure

| file | responsibility |
|---|---|
| `sched/borrow.go` (new) | `Borrowing`, `Borrow`, `MayBorrow`, `BorrowFits`, the borrow load and the firm fit |
| `sched/giveback.go` (new) | `Tight`, `BorrowerJob`, `GiveBackInput`, `GiveBackState`, `GiveBack`, `ActLend`, `ActResume` |
| `sched/greedy.go` | `Greedy.Used`, `Greedy.Tight`; `greedyFit.gate` split out of `fits`; `onMemory`; `AdmitGreedy` sets `Decision.OnMemory` |
| `sched/admit.go` | `Decision.OnMemory` |
| `sched/cgroup.go` | `checkJobScope` (shared with `CgroupKill`), `CgroupReclaim`, `meminfoKB` |
| `sched/meminfo_linux.go`, `sched/meminfo_other.go` | `ReadSwapFreeKB` |
| `sched/job.go` | `Record` borrowing fields, `Record.LentFor`, `Policy.Borrow` |
| `sched/policy.go`, `config/settings.go` | `borrow` |
| `core/queue_borrow.go` (new) | the give-back driver: `stepGiveBack`, `applyGiveBack`, `deliverGiveBackDone`, `markBorrower`, `makeFirm` |
| `core/queue_admit.go` | `Borrow` after `AdmitGreedy`, borrowers out of `Grant`, starting borrowers, an uncontained borrower |
| `core/queue_greedy.go` | `Used` and `Tight` in `greedyInputs`; `predictRunning`'s end moved by time lent |
| `core/queue_ladder.go` | `stepLadder` skips lent jobs; `requeueJob` handles a give-back |
| `core/queue_watch.go` | `readSwapFree`; the rung reconcile's lent case; `stepGiveBack` after `stepLadder`; `settleGone` |
| `core/queue.go`, `core/model.go` | `giveBack`, `noLend` state; routing `giveBackDone` |
| `core/queue_ops.go`, `core/queue_view.go` | `setSchedulerCaps` keeps `borrow`; settings clone; view fields |
| `core/queue_notice.go` | the end message's clause |
| `internal/cli/job_sizing.go`, `internal/cli/job_follow.go` | list text, start line, lent/resumed/restarted lines |
| `ui/work.go` | row state text and header |
| docs | `USAGE.md`, `sched/README.md`, `sched/CLAUDE.md`, `docs/claude/the-work-queue.md`, `docs/specs/protocol.md` (generated) |

---

## Package 1: sched (pure logic, reclaim, config, live contract)

### Task 1.1: Record, Policy and config fields

**Files:**
- Modify: `sched/job.go` (the `Record` struct after `Starved`; the `Policy` struct)
- Modify: `config/settings.go` (`SchedulerSettings`, `clone`)
- Modify: `sched/policy.go` (`Resolve`'s returned `Policy`)
- Test: `sched/policy_test.go`

- [ ] **Step 1: Write the failing test** (append to `sched/policy_test.go`)

```go
func TestResolve_Borrow(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name string
		in   *bool
		want bool
	}{{"absent is off", nil, false}, {"false", &off, false}, {"true", &on, true}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(config.SchedulerSettings{Borrow: tc.in}, policyFresco)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Borrow)
		})
	}
}
```

- [ ] **Step 2: Add the fields.** In `sched/job.go`, after `Starved bool` inside `Record`:

```go
	// Memory borrowing (spec 2026-11-06-memory-borrowing §7.3): whether it
	// runs as a borrower, when it was lent (zero unless lent) and the
	// seconds it was lent in this attempt, its give-back requeues across
	// attempts, a requeue asked for as a give-back, and whether this attempt
	// runs firm only (the one after a give-back requeue).
	Borrower   bool      `json:"borrower,omitempty"`
	LentAt     time.Time `json:"lent_at,omitzero"`
	LentSecs   int64     `json:"lent_secs,omitempty"`
	GaveBack   int       `json:"gave_back,omitempty"`
	GivingBack bool      `json:"giving_back,omitempty"`
	NoBorrow   bool      `json:"no_borrow,omitempty"`
```

After `QueuedSince`:

```go
// LentFor is how long r has been lent in this attempt as of now: its
// LentSecs, plus the current lend's length while it is lent.
func (r Record) LentFor(now time.Time) time.Duration {
	d := time.Duration(r.LentSecs) * time.Second
	if !r.LentAt.IsZero() {
		d += now.Sub(r.LentAt)
	}
	return d
}
```

In `Policy`, after `CPUOvercommit`:

```go
	// Borrow lets a job the firm fit holds back start as a borrower, which
	// gives its memory back when memory is tight (memory borrowing spec §9).
	Borrow bool `json:"borrow"`
```

In `config/settings.go` `SchedulerSettings`, after `CPUOvercommit`:

```go
	// Borrow lets a job greedy admission holds back start on memory the
	// running jobs claim but don't use, giving it back when memory gets
	// tight (memory borrowing spec §9); absent means false.
	Borrow *bool `json:"borrow,omitempty"`
```

and in `SchedulerSettings.clone`, after `out.Contain = clonePtr(s.Contain)`: `out.Borrow = clonePtr(s.Borrow)`.

In `sched/policy.go` `Resolve`'s return, after `CPUOvercommit: overcommit,`: `Borrow: s.Borrow != nil && *s.Borrow,`.

- [ ] **Step 3: Run** `loom job run -d "P1 resolve" -- sh -c 'CGO_ENABLED=0 go test -count=1 -run "TestResolve|TestSettings|TestScheduler" ./sched/ ./config/'`. Expected: PASS.

### Task 1.2: `Decision.OnMemory` and the gate split

**Files:**
- Modify: `sched/admit.go` (`Decision`), `sched/greedy.go` (`Greedy`, `greedyFit`, `AdmitGreedy`)
- Test: `sched/borrow_test.go` (new; this task adds its first test)

- [ ] **Step 1: Write the failing test** (`sched/borrow_test.go`)

```go
package sched

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// borrowPolicy is greedyPolicy with borrowing on.
func borrowPolicy() Policy {
	p := greedyPolicy()
	p.Borrow = true
	return p
}

// borrowGreedy is a 14 GB budget whose running jobs are predicted flat at
// their claims for ten minutes and measured at used.
func borrowGreedy(jobs []Record, used map[JobID]int64) Greedy {
	g := Greedy{BudgetKB: gb(14), Running: map[JobID]Prediction{}, Used: used, Predict: flatPredict(nil)}
	for _, r := range jobs {
		if r.State == Running {
			g.Running[r.ID] = Flat(gb(int64(r.Claim.MemoryGB)), t0, t0.Add(10*time.Minute))
		}
	}
	return g
}

// borrowed is a running borrower.
func borrowed(id JobID, c Claim) Record {
	r := running(id, c)
	r.Borrower = true
	return r
}

func TestAdmitGreedy_OnMemoryNamesOnlyTheMemoryWaits(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 20, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 6}), queued(3, Claim{CPUs: 9, MemoryGB: 1})}
	d := AdmitGreedy(jobs, greedyPolicy(), t0, borrowGreedy(jobs, nil))
	assert.Equal(t, []JobID{2}, d.OnMemory)
	assert.Contains(t, d.Waiting[3], "CPU overcommitted")
}
```

- [ ] **Step 2: Run it to verify it fails.** `loom job run -d "P1 onmemory red" -- sh -c 'CGO_ENABLED=0 go test -count=1 -run TestAdmitGreedy_OnMemory ./sched/'`. Expected: a compile failure, since `Decision.OnMemory`, `Greedy.Used` and `Record.Borrower` are undefined (the last is added in 1.1).

- [ ] **Step 3: Implement.** In `sched/admit.go` `Decision`, add:

```go
	// OnMemory is the queued jobs AdmitGreedy left waiting on its memory fit
	// alone, in queue order: the jobs Borrow may start (memory borrowing
	// spec §5.2). Admit leaves it nil.
	OnMemory []JobID
```

In `sched/greedy.go` `Greedy`, after `Holdoff`:

```go
	// Borrowing (memory borrowing spec §5.2): each running job's measured
	// memory now, in KB (its working set, or its session's RSS when
	// uncontained; absent before its first sample), and whether memory is
	// tight (Tight).
	Used  map[JobID]int64
	Tight bool
```

Add `onMemory []JobID // the jobs fits failed on memory alone` to `greedyFit`. Replace `AdmitGreedy`'s body:

```go
	f := &greedyFit{p: p, g: g, now: now}
	d := admitWith(jobs, p, now, f)
	d.OnMemory = f.onMemory
	return d
```

Split `fits` into a `gate` and the memory check:

```go
// gate is why r can't start for anything but memory: the ladder's holdoff,
// an exclusive job, the CPU fit or the I/O slots (cgroup spec §7.3); ""
// when none holds it.
func (f *greedyFit) gate(r Record) string {
	limit := f.p.CPUOvercommit * f.p.Caps.CPUs
	switch {
	case f.g.Holdoff != "":
		return f.g.Holdoff
	case f.exclusive != 0:
		return fmt.Sprintf("waits for exclusive job %d", f.exclusive)
	case r.Claim.Exclusive && f.any:
		return "waits for the host to empty"
	case r.Claim.Exclusive:
		return ""
	case f.cores+r.Claim.CPUs > limit:
		return fmt.Sprintf("CPU overcommitted (%d/%d claimed)", f.cores+r.Claim.CPUs, limit)
	case f.any && f.g.CPUPressure >= float64(f.p.CPUPressurePct):
		return fmt.Sprintf("CPU busy (pressure %.0f%%)", f.g.CPUPressure)
	case r.Claim.IO && f.slots >= f.p.Caps.IOJobs:
		return "needs an I/O slot"
	}
	return ""
}

func (f *greedyFit) fits(r Record) string {
	if why := f.gate(r); why != "" || r.Claim.Exclusive {
		return why
	}
	cand := f.g.Predict(r, f.now)
	end := cand.End()
	if PeakSum(append(slices.Clone(f.preds), cand), f.now, end) <= f.g.BudgetKB {
		return ""
	}
	f.onMemory = append(f.onMemory, r.ID)
	need := (PeakSum([]Prediction{cand}, f.now, end) + kbPerGB - 1) / kbPerGB
	left := max(0, f.g.BudgetKB-PeakSum(f.preds, f.now, end)) / kbPerGB
	return fmt.Sprintf("needs ~%d GB at its peak (forecast leaves %d)", need, left)
}
```

- [ ] **Step 4: Run** `TestAdmitGreedy` and `TestForecast` (`-run "TestAdmitGreedy|TestForecast"`). Expected: PASS.

### Task 1.3: `Borrow`, `MayBorrow`, `BorrowFits`

**Files:**
- Create: `sched/borrow.go`
- Test: `sched/borrow_test.go`

- [ ] **Step 1: Write the failing tests** (append to `sched/borrow_test.go`)

```go
func TestBorrow_StartsAgainstWhatFirmJobsUseNow(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 6})}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(4)})
	dec := AdmitGreedy(jobs, borrowPolicy(), t0, g)
	require.Empty(t, dec.Start, "10 + 6 GB of forecasts pass the 14 GB budget")
	require.Equal(t, []JobID{2}, dec.OnMemory)
	assert.Equal(t, []JobID{2}, Borrow(jobs, dec, borrowPolicy(), t0, g).Start, "4 GB used × 1.15 + 6 GB fits")
	assert.Empty(t, Borrow(jobs, dec, greedyPolicy(), t0, g).Start, "borrow off")
}

func TestBorrow_BorrowersLendToNoOne(t *testing.T) {
	jobs := []Record{borrowed(1, Claim{CPUs: 4, MemoryGB: 9}), queued(2, Claim{CPUs: 4, MemoryGB: 6})}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(1)})
	assert.Empty(t, Borrow(jobs, Decision{OnMemory: []JobID{2}}, borrowPolicy(), t0, g).Start,
		"the borrower counts at its 9 GB prediction, not its 1 GB use")
}

func TestBorrow_AFirmJobNotYetMeasuredCountsAtItsPrediction(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 6})}
	g := borrowGreedy(jobs, map[JobID]int64{})
	assert.Empty(t, Borrow(jobs, Decision{OnMemory: []JobID{2}}, borrowPolicy(), t0, g).Start)
}

func TestBorrow_WhoMayBorrow(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Record)
		may  bool
	}{
		{"an ordinary job", func(*Record) {}, true},
		{"--no-requeue", func(r *Record) { r.NoRequeue = true }, false},
		{"requeued MaxRequeues times", func(r *Record) { r.Requeues = MaxRequeues }, false},
		{"one requeue below", func(r *Record) { r.Requeues = MaxRequeues - 1 }, true},
		{"the attempt after a give-back", func(r *Record) { r.NoBorrow = true }, false},
		{"exclusive", func(r *Record) { r.Claim.Exclusive = true }, false},
		{"an agent job", func(r *Record) { r.Kind = KindAgent }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := queued(2, Claim{CPUs: 4, MemoryGB: 6})
			tc.edit(&r)
			assert.Equal(t, tc.may, MayBorrow(r))
			jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), r}
			g := borrowGreedy(jobs, map[JobID]int64{1: gb(4)})
			assert.Equal(t, tc.may, len(Borrow(jobs, Decision{OnMemory: []JobID{2}}, borrowPolicy(), t0, g).Start) == 1)
		})
	}
}

func TestBorrow_HoldsBack(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 6}), borrowed(3, Claim{CPUs: 1, MemoryGB: 1})}
	used := map[JobID]int64{1: gb(4), 3: gb(1)}
	dec := Decision{OnMemory: []JobID{2}}
	g := borrowGreedy(jobs, used)
	require.Equal(t, []JobID{2}, Borrow(jobs, dec, borrowPolicy(), t0, g).Start, "4.6 + 1 + 6 GB fits: the case each guard spares")

	tight := g
	tight.Tight = true
	assert.Empty(t, Borrow(jobs, dec, borrowPolicy(), t0, tight).Start, "memory is tight")
	held := g
	held.Holdoff = "memory is short (job 1 paused)"
	assert.Empty(t, Borrow(jobs, dec, borrowPolicy(), t0, held).Start, "the ladder holds admission off")
	lent := slices.Clone(jobs)
	lent[2].LentAt = t0.Add(-time.Minute)
	assert.Empty(t, Borrow(lent, dec, borrowPolicy(), t0, g).Start, "a job is lent")
}

func TestBorrow_ThePassStartsCount(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 2, MemoryGB: 5}), queued(3, Claim{CPUs: 2, MemoryGB: 5})}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(4)})
	assert.Equal(t, []JobID{2}, Borrow(jobs, Decision{OnMemory: []JobID{2, 3}}, borrowPolicy(), t0, g).Start,
		"4.6 + 5 + 5 GB passes 14: the second borrower waits")

	jobs[2].Claim.CPUs = 4
	dec := Decision{Start: []JobID{3}, OnMemory: []JobID{2}}
	assert.Empty(t, Borrow(jobs, dec, borrowPolicy(), t0, g).Start, "AdmitGreedy's start counts at its 5 GB prediction")
}

func TestBorrow_TheCPUGateCountsThePassStarts(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 14, MemoryGB: 10}), queued(2, Claim{CPUs: 8, MemoryGB: 1}), queued(3, Claim{CPUs: 8, MemoryGB: 1})}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(4)})
	assert.Equal(t, []JobID{2}, Borrow(jobs, Decision{OnMemory: []JobID{2, 3}}, borrowPolicy(), t0, g).Start,
		"14 + 8 + 8 cores pass the 28 allowed")
}

func TestBorrow_ABorrowerBecomesFirmWhenTheFirmFitHolds(t *testing.T) {
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 6}), borrowed(2, Claim{CPUs: 4, MemoryGB: 8})}
	used := map[JobID]int64{1: gb(2), 2: gb(2)}
	assert.Equal(t, []JobID{2}, Borrow(jobs, Decision{}, greedyPolicy(), t0, borrowGreedy(jobs, used)).Firm,
		"6 + 8 GB of forecasts fit 14, with borrowing off too")
	jobs[1].Claim.MemoryGB = 9
	assert.Empty(t, Borrow(jobs, Decision{}, greedyPolicy(), t0, borrowGreedy(jobs, used)).Firm, "6 + 9 GB don't")
	jobs[1].Claim.MemoryGB = 8
	jobs[1].LentAt = t0
	assert.Empty(t, Borrow(jobs, Decision{}, greedyPolicy(), t0, borrowGreedy(jobs, used)).Firm, "a lent job resumes first")
}

func TestBorrowFits(t *testing.T) {
	lent := borrowed(2, Claim{CPUs: 4, MemoryGB: 8})
	lent.LentAt = t0.Add(-time.Minute)
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 12}), lent}
	g := borrowGreedy(jobs, map[JobID]int64{1: gb(5), 2: 0})
	assert.True(t, BorrowFits(jobs, g, 2, t0), "5 × 1.15 + 8 = 13.75 GB")
	g.Used[1] = gb(6)
	assert.False(t, BorrowFits(jobs, g, 2, t0), "6 × 1.15 + 8 = 14.9 GB")
	assert.False(t, BorrowFits(jobs, g, 9, t0), "no such job")
}
```

- [ ] **Step 2: Run them to verify they fail.** `-run 'TestBorrow'`. Expected: a compile failure, since `Borrow`, `MayBorrow` and `BorrowFits` are undefined.

- [ ] **Step 3: Implement** `sched/borrow.go`:

```go
package sched

import (
	"slices"
	"time"
)

// Borrowing is what Borrow decided in a pass (memory borrowing spec §5).
type Borrowing struct {
	Start []JobID // queued jobs to start as borrowers, in queue order
	Firm  []JobID // running borrowers that become firm
}

// MayBorrow reports whether queued job r may start as a borrower (memory
// borrowing spec §5.1): it is not --no-requeue, exclusive or an agent job,
// it has been requeued fewer than MaxRequeues times, and it is not the
// attempt after a give-back requeue.
func MayBorrow(r Record) bool {
	return !r.NoRequeue && !r.Claim.Exclusive && r.Kind != KindAgent && r.Requeues < MaxRequeues && !r.NoBorrow
}

// Borrow is the borrowing pass (memory borrowing spec §5.2–§5.3), a pure
// function run after AdmitGreedy, on its decision dec. Whatever p.Borrow
// says, it names the running unlent borrowers that become firm: those the
// firm fit holds for until their expected end. When p.Borrow is set, memory
// is not tight, the ladder holds nothing off and no job is lent, it also
// names the jobs dec left waiting on memory alone (dec.OnMemory) that start
// as borrowers. It walks them in queue order and starts each that may borrow
// (MayBorrow), passes the CPU and I/O gates and passes the borrow fit,
// counting the running jobs, dec's starts and the borrowers before it.
func Borrow(jobs []Record, dec Decision, p Policy, now time.Time, g Greedy) Borrowing {
	var out Borrowing
	all := runningPreds(jobs, g)
	lent := false
	for _, r := range jobs {
		switch {
		case r.State != Running || !r.Borrower:
		case !r.LentAt.IsZero():
			lent = true
		case PeakSum(all, now, g.Running[r.ID].End()) <= g.BudgetKB:
			out.Firm = append(out.Firm, r.ID)
		}
	}
	if !p.Borrow || g.Tight || g.Holdoff != "" || lent || len(dec.OnMemory) == 0 {
		return out
	}
	byID := map[JobID]Record{}
	f := &greedyFit{p: p, g: g, now: now}
	for _, r := range jobs {
		byID[r.ID] = r
		if r.State == Running && r.Kind != KindAgent {
			f.count(r)
		}
	}
	firmKB, preds := borrowLoad(jobs, g)
	for _, id := range dec.Start {
		if r, ok := byID[id]; ok && r.Kind != KindAgent {
			f.take(r)
			preds = append(preds, g.Predict(r, now))
		}
	}
	for _, id := range dec.OnMemory {
		r, ok := byID[id]
		if !ok || !MayBorrow(r) || f.gate(r) != "" {
			continue
		}
		cand := g.Predict(r, now)
		if firmKB+PeakSum(append(slices.Clone(preds), cand), now, cand.End()) > g.BudgetKB {
			continue
		}
		f.take(r)
		preds = append(preds, cand)
		out.Start = append(out.Start, id)
	}
	return out
}

// BorrowFits reports whether running borrower id fits the borrow fit from
// now to its prediction's end (memory borrowing spec §6.5): the firm jobs at
// their measured memory and every borrower, it included, at its prediction.
// The give-back machine resumes a lent job on it.
func BorrowFits(jobs []Record, g Greedy, id JobID, now time.Time) bool {
	pred, ok := g.Running[id]
	if !ok {
		return false
	}
	firmKB, preds := borrowLoad(jobs, g)
	return firmKB+PeakSum(preds, now, pred.End()) <= g.BudgetKB
}

// borrowLoad is what the borrow fit counts of the running jobs, agent jobs
// aside as AdmitGreedy leaves them (memory borrowing spec §5.2): a firm job
// at its measured memory plus the margin, or at its prediction before it is
// measured, and a borrower, lent or not, at its prediction.
func borrowLoad(jobs []Record, g Greedy) (firmKB int64, preds []Prediction) {
	for _, r := range jobs {
		if r.State != Running || r.Kind == KindAgent {
			continue
		}
		if used, ok := g.Used[r.ID]; ok && used > 0 && !r.Borrower {
			firmKB += WithMarginKB(used)
			continue
		}
		preds = append(preds, runningPred(r, g))
	}
	return firmKB, preds
}

// runningPreds is every running job's prediction but agent jobs', as
// AdmitGreedy counts them: the firm fit's terms (memory borrowing spec §5.3).
func runningPreds(jobs []Record, g Greedy) []Prediction {
	var preds []Prediction
	for _, r := range jobs {
		if r.State == Running && r.Kind != KindAgent {
			preds = append(preds, runningPred(r, g))
		}
	}
	return preds
}

// runningPred is running job r's prediction from now, or from its start
// when g has none (greedyFit.count's rule).
func runningPred(r Record, g Greedy) Prediction {
	if pred, ok := g.Running[r.ID]; ok {
		return pred
	}
	return g.Predict(r, r.Started)
}
```

- [ ] **Step 4: Run** `-run 'TestBorrow|TestAdmitGreedy'`. Expected: PASS.

### Task 1.4: `GiveBack`

**Files:**
- Create: `sched/giveback.go`
- Test: `sched/giveback_test.go` (new)

- [ ] **Step 1: Write the failing tests** (`sched/giveback_test.go`)

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gbIn is a reading at now with headroomGB of headroom, a 4 GB reserve,
// 15 GB of swap free and the bus up.
func gbIn(now time.Time, headroomGB int64, jobs ...BorrowerJob) GiveBackInput {
	return GiveBackInput{Now: now, HeadroomKB: gb(headroomGB), ReserveKB: gb(4), SwapFreeKB: gb(15), CanLend: true, Jobs: jobs}
}

func TestTight(t *testing.T) {
	assert.True(t, Tight(gb(6)-1, gb(4)), "below the reserve plus 2 GB")
	assert.False(t, Tight(gb(6), gb(4)), "at it")
}

func TestGiveBack_OnlyWhileTight(t *testing.T) {
	in := gbIn(t0, 6, BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(2)})
	_, act := GiveBack(GiveBackState{}, in)
	assert.Nil(t, act)
	in.HeadroomKB--
	_, act = GiveBack(GiveBackState{}, in)
	assert.Equal(t, &Action{Kind: ActLend, Job: 1}, act)
}

func TestGiveBack_TheYoungestUnlentBorrowerGoesFirst(t *testing.T) {
	old := BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(1)}
	young := BorrowerJob{ID: 2, Started: t0.Add(-10 * time.Minute), WorkingKB: gb(1)}
	lent := BorrowerJob{ID: 3, Started: t0.Add(-5 * time.Minute), LentAt: t0.Add(-time.Minute), WorkingKB: gb(1)}
	_, act := GiveBack(GiveBackState{}, gbIn(t0, 3, old, young, lent))
	require.NotNil(t, act)
	assert.Equal(t, JobID(2), act.Job)
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 3, lent))
	assert.Nil(t, act, "a lent job has given back already")
}

func TestGiveBack_TheStakeDecidesRequeueOrLend(t *testing.T) {
	long := BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(2)}
	for _, tc := range []struct {
		name string
		job  BorrowerJob
		edit func(*GiveBackInput)
		want ActionKind
	}{
		{"just under 2 minutes", BorrowerJob{ID: 1, Started: t0.Add(-2*time.Minute + time.Second), WorkingKB: gb(2)}, nil, ActRequeue},
		{"2 minutes", BorrowerJob{ID: 1, Started: t0.Add(-2 * time.Minute), WorkingKB: gb(2)}, nil, ActLend},
		{"time lent is no stake", BorrowerJob{ID: 1, Started: t0.Add(-3 * time.Minute), LentFor: 61 * time.Second, WorkingKB: gb(2)}, nil, ActRequeue},
		{"a freeze of it failed", BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(2), NoLend: true}, nil, ActRequeue},
		{"swap can't take it", long, func(in *GiveBackInput) { in.SwapFreeKB = gb(2) - 1 }, ActRequeue},
		{"swap takes it exactly", long, func(in *GiveBackInput) { in.SwapFreeKB = gb(2) }, ActLend},
		{"no bus", long, func(in *GiveBackInput) { in.CanLend = false }, ActRequeue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := gbIn(t0, 3, tc.job)
			if tc.edit != nil {
				tc.edit(&in)
			}
			_, act := GiveBack(GiveBackState{}, in)
			require.NotNil(t, act)
			assert.Equal(t, tc.want, act.Kind)
		})
	}
}

func TestGiveBack_TenSecondsBetweenGiveBacks(t *testing.T) {
	a := BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), WorkingKB: gb(1)}
	b := BorrowerJob{ID: 2, Started: t0.Add(-2 * time.Hour), WorkingKB: gb(1)}
	st, act := GiveBack(GiveBackState{}, gbIn(t0, 3, a, b))
	require.Equal(t, &Action{Kind: ActLend, Job: 1}, act)
	a.LentAt = t0
	_, act = GiveBack(st, gbIn(t0.Add(10*time.Second-time.Millisecond), 3, a, b))
	assert.Nil(t, act)
	_, act = GiveBack(st, gbIn(t0.Add(10*time.Second), 3, a, b))
	assert.Equal(t, &Action{Kind: ActLend, Job: 2}, act)
}

func TestGiveBack_Resume(t *testing.T) {
	first := BorrowerJob{ID: 1, Started: t0.Add(-time.Hour), LentAt: t0.Add(-31 * time.Second), Fits: true}
	second := BorrowerJob{ID: 2, Started: t0.Add(-2 * time.Hour), LentAt: t0.Add(-30 * time.Second), Fits: true}
	_, act := GiveBack(GiveBackState{}, gbIn(t0, 64, second, first))
	assert.Equal(t, &Action{Kind: ActResume, Job: 1}, act, "the earliest lent first, one per pass")

	soon := second
	soon.LentAt = t0.Add(-30*time.Second + time.Millisecond)
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 64, soon))
	assert.Nil(t, act, "lent under 30 s")
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 64, second))
	assert.Equal(t, &Action{Kind: ActResume, Job: 2}, act, "lent 30 s")

	unfit := second
	unfit.Fits = false
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 64, unfit))
	assert.Nil(t, act, "its fit doesn't hold")
	_, act = GiveBack(GiveBackState{}, gbIn(t0, 3, second))
	assert.Nil(t, act, "memory is tight")
	noBus := gbIn(t0, 64, second)
	noBus.CanLend = false
	_, act = GiveBack(GiveBackState{}, noBus)
	assert.Nil(t, act, "no bus to thaw it")
}
```

- [ ] **Step 2: Run them to verify they fail.** `-run 'TestTight|TestGiveBack'`. Expected: a compile failure.

- [ ] **Step 3: Implement** `sched/giveback.go`:

```go
package sched

import (
	"cmp"
	"slices"
	"time"
)

// The give-back machine's thresholds (memory borrowing spec §6, §9).
const (
	cheapStake    = 2 * time.Minute  // a borrower that has run less is requeued, not lent
	giveBackEvery = 10 * time.Second // the least time between give-backs
	lendToResume  = 30 * time.Second // the least time a job stays lent
)

// The give-back machine's actions; a give-back requeue is ActRequeue.
const (
	ActLend   ActionKind = "lend"   // freeze it, then push its memory to swap
	ActResume ActionKind = "resume" // thaw a lent job
)

// BorrowerJob is a running borrower as GiveBack sees it.
type BorrowerJob struct {
	ID        JobID
	Started   time.Time
	LentAt    time.Time     // zero unless lent
	LentFor   time.Duration // time lent in this attempt before LentAt
	WorkingKB int64
	NoLend    bool // a freeze of it failed: it is given back by requeue
	Fits      bool // lent: the borrow fit holds for it now (BorrowFits)
}

// GiveBackInput is one pass's reading (memory borrowing spec §6.1).
type GiveBackInput struct {
	Now        time.Time
	HeadroomKB int64 // the host's MemAvailable
	ReserveKB  int64 // interactive_reserve_gb
	SwapFreeKB int64 // the host's SwapFree
	CanLend    bool  // the user bus answers (greedy mode): a freeze or thaw can be made
	Jobs       []BorrowerJob
}

// GiveBackState is what the machine keeps between passes.
type GiveBackState struct {
	Last time.Time // the last give-back
}

// Tight reports whether memory is tight: headroom below the reserve plus
// the ladder's calm margin (memory borrowing spec §4).
func Tight(headroomKB, reserveKB int64) bool { return headroomKB < reserveKB+calmMarginKB }

// GiveBack is one pass of the give-back machine (memory borrowing spec §6),
// a pure function: at most one action. While memory is tight it gives back
// the most recently started unlent borrower, at most one every
// giveBackEvery. That is a requeue when the borrower has run under
// cheapStake (time lent aside), a freeze of it failed, there is no bus, or
// swap can't take its working set, and a lend otherwise. While memory is not
// tight, and the bus is up, it resumes the lent job lent earliest whose
// borrow fit holds, once it has been lent lendToResume.
func GiveBack(st GiveBackState, in GiveBackInput) (GiveBackState, *Action) {
	if Tight(in.HeadroomKB, in.ReserveKB) {
		if !st.Last.IsZero() && in.Now.Sub(st.Last) < giveBackEvery {
			return st, nil
		}
		var v *BorrowerJob
		for i := range in.Jobs {
			j := &in.Jobs[i]
			if j.LentAt.IsZero() && (v == nil || j.Started.After(v.Started) || (j.Started.Equal(v.Started) && j.ID > v.ID)) {
				v = j
			}
		}
		if v == nil {
			return st, nil
		}
		st.Last = in.Now
		if stake := in.Now.Sub(v.Started) - v.LentFor; stake < cheapStake || v.NoLend || !in.CanLend || in.SwapFreeKB < v.WorkingKB {
			return st, &Action{Kind: ActRequeue, Job: v.ID}
		}
		return st, &Action{Kind: ActLend, Job: v.ID}
	}
	if !in.CanLend {
		return st, nil
	}
	var ready []*BorrowerJob
	for i := range in.Jobs {
		if j := &in.Jobs[i]; !j.LentAt.IsZero() && j.Fits && in.Now.Sub(j.LentAt) >= lendToResume {
			ready = append(ready, j)
		}
	}
	if len(ready) == 0 {
		return st, nil
	}
	j := slices.MinFunc(ready, func(a, b *BorrowerJob) int { return cmp.Or(a.LentAt.Compare(b.LentAt), cmp.Compare(a.ID, b.ID)) })
	return st, &Action{Kind: ActResume, Job: j.ID}
}
```

- [ ] **Step 4: Run** `-run 'TestTight|TestGiveBack|TestLadder'`. Expected: PASS.

### Task 1.5: `CgroupReclaim` and `ReadSwapFreeKB`

**Files:**
- Modify: `sched/cgroup.go`, `sched/meminfo_linux.go`, `sched/meminfo_other.go`
- Test: `sched/cgroup_test.go`

- [ ] **Step 1: Write the failing tests** (append to `sched/cgroup_test.go`)

```go
func TestCgroupReclaim(t *testing.T) {
	root := t.TempDir()
	job := "/loom.slice/loom-ab-job-7-1.scope"
	writeCgroup(t, root, job, map[string]string{"memory.reclaim": ""})
	require.NoError(t, CgroupReclaim(root, job, 1<<20))
	data, err := os.ReadFile(filepath.Join(root, job, "memory.reclaim"))
	require.NoError(t, err)
	assert.Equal(t, "1073741824 swappiness=200", string(data), "a GB, anonymous pages first")

	writeCgroup(t, root, "/user.slice", map[string]string{"memory.reclaim": ""})
	assert.ErrorContains(t, CgroupReclaim(root, "/user.slice", 1), "not a loom job's scope")
	assert.ErrorContains(t, CgroupReclaim(root, "/loom.slice/loom-ab-job-8-1.scope", 1), "memory.reclaim", "no such scope")
}

func TestParseSwapFree(t *testing.T) {
	kb, err := meminfoKB([]byte("MemTotal: 1 kB\nSwapTotal:      15847932 kB\nSwapFree:       13065504 kB\n"), "SwapFree")
	require.NoError(t, err)
	assert.Equal(t, int64(13065504), kb)
	_, err = meminfoKB([]byte("MemTotal: 1 kB\n"), "SwapFree")
	assert.ErrorContains(t, err, "no SwapFree in /proc/meminfo")
}
```

- [ ] **Step 2: Run them to verify they fail.** `-run 'TestCgroupReclaim|TestParseSwapFree|TestCgroupKill|TestParseMemAvailable'`. Expected: a compile failure.

- [ ] **Step 3: Implement.** In `sched/cgroup.go`, add the `syscall` import and replace `CgroupKill`'s check with a shared one:

```go
// checkJobScope refuses path unless it is a clean absolute cgroup path whose
// base name is a loom job's scope (loom-…-job-….scope); verb names what was
// refused.
func checkJobScope(path, verb string) error {
	if err := checkCgroupPath(path); err != nil {
		return err
	}
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "loom-") || !strings.Contains(base, "-job-") || !strings.HasSuffix(base, ".scope") {
		return fmt.Errorf("refusing to %s cgroup %s: not a loom job's scope", verb, path)
	}
	return nil
}
```

`CgroupKill` then begins `if err := checkJobScope(path, "kill"); err != nil { return err }`, with the rest unchanged. Add:

```go
// CgroupReclaim asks the kernel to reclaim kb of path's memory under root
// through memory.reclaim (memory borrowing spec §7.1), anonymous pages
// first (swappiness=200), and again without that argument when the kernel
// refuses it as invalid. A write the kernel answers EAGAIN reclaimed less
// than asked, and is no error. It refuses anything but a loom job's scope,
// as CgroupKill does.
func CgroupReclaim(root, path string, kb int64) error {
	if err := checkJobScope(path, "reclaim from"); err != nil {
		return err
	}
	write := func(arg string) error {
		f, err := os.OpenFile(filepath.Join(root, path, "memory.reclaim"), os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		_, werr := f.Write([]byte(arg))
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return werr
	}
	amount := strconv.FormatInt(kb<<10, 10)
	err := write(amount + " swappiness=200")
	if errors.Is(err, syscall.EINVAL) {
		err = write(amount)
	}
	if err == nil || errors.Is(err, syscall.EAGAIN) {
		return nil
	}
	return fmt.Errorf("write memory.reclaim: %w", err)
}
```

Replace `parseMemAvailable` with a keyed reader, and keep the old name as its caller:

```go
// meminfoKB is /proc/meminfo's key, in KB.
func meminfoKB(data []byte, key string) (int64, error) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			if fields := strings.Fields(v); len(fields) >= 1 {
				return strconv.ParseInt(fields[0], 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("no %s in /proc/meminfo", key)
}

// parseMemAvailable is /proc/meminfo's MemAvailable, in KB.
func parseMemAvailable(data []byte) (int64, error) { return meminfoKB(data, "MemAvailable") }
```

In `sched/meminfo_linux.go`:

```go
// ReadSwapFreeKB is the host's SwapFree, in KB: what swap can still take,
// uncompressed (memory borrowing spec §7.2).
func ReadSwapFreeKB() (int64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	return meminfoKB(data, "SwapFree")
}
```

In `sched/meminfo_other.go`:

```go
// ReadSwapFreeKB answers ErrUnsupported: /proc/meminfo is Linux's.
func ReadSwapFreeKB() (int64, error) { return 0, ErrUnsupported }
```

- [ ] **Step 4: Run** `-run 'TestCgroup|TestParse'`. Expected: PASS.

### Task 1.6: The live lend contract

**Files:**
- Modify: `sched/confine_live_test.go`

- [ ] **Step 1: Add the helper child and the contract** (add `bufio` and `strconv` to its imports):

```go
// TestHelperHoldMemory is the child of TestConfiner_Live_ReclaimsAFrozenScope.
// It waits for a line on stdin, fills LOOM_TEST_HOLD_MB of memory with a
// known pattern and says "ready". It then waits for another line, and exits
// 0 only if the memory still holds the pattern. It does nothing in an
// ordinary run.
func TestHelperHoldMemory(t *testing.T) {
	mb, _ := strconv.Atoi(os.Getenv("LOOM_TEST_HOLD_MB"))
	if mb == 0 {
		t.Skip("only as the lend contract's child")
	}
	in := bufio.NewReader(os.Stdin)
	_, _ = in.ReadString('\n')
	buf := make([]byte, mb<<20)
	for i := range buf {
		buf[i] = byte(i ^ i>>9)
	}
	fmt.Println("ready")
	_, _ = in.ReadString('\n')
	for i := range buf {
		if buf[i] != byte(i^i>>9) {
			fmt.Println("corrupt")
			os.Exit(1)
		}
	}
	fmt.Println("intact")
	os.Exit(0)
}

// TestConfiner_Live_ReclaimsAFrozenScope is the lend's contract (memory
// borrowing spec §11.3): a frozen scope's memory goes to swap through
// CgroupReclaim, and after a thaw its process carries on, its memory
// intact. The child joins its scope before it allocates, since a process
// moved later leaves its memory charged where it was.
func TestConfiner_Live_ReclaimsAFrozenScope(t *testing.T) {
	addr := os.Getenv(testenv.EnvRealDBus)
	if addr == "" {
		t.Skip("no user bus")
	}
	t.Setenv(testenv.EnvDBus, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := NewConfiner(ctx)
	require.NoError(t, err)
	defer c.Close()
	slice := fmt.Sprintf("loom_test%d_lend.slice", os.Getpid())
	unit := fmt.Sprintf("loom-test%d-job-2-1.scope", os.Getpid())
	t.Cleanup(func() { removeTestSlice(t, slice) })
	require.NoError(t, c.EnsureSlice(ctx, slice, SliceProps{CPUWeight: 30, MemoryMaxBytes: 1 << 30}))

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldMemory$")
	cmd.Env = append(os.Environ(), "LOOM_TEST_HOLD_MB=200")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	require.NoError(t, c.JoinScope(ctx, ScopeSpec{Unit: unit, Slice: slice, CPUWeight: 100}, cmd.Process.Pid))
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", cmd.Process.Pid))
	require.NoError(t, err)
	path, err := parseSelfCgroup(data)
	require.NoError(t, err)

	out := bufio.NewReader(stdout)
	_, err = io.WriteString(stdin, "go\n")
	require.NoError(t, err)
	line, err := out.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ready\n", line)
	before, err := strconv.ParseInt(readKnob(t, path, "memory.current"), 10, 64)
	require.NoError(t, err)
	require.Greater(t, before, int64(190<<20), "the child's 200 MB is charged to its scope")

	require.NoError(t, c.Freeze(ctx, unit))
	require.NoError(t, CgroupReclaim(CgroupRoot, path, before>>10))
	after, err := strconv.ParseInt(readKnob(t, path, "memory.current"), 10, 64)
	require.NoError(t, err)
	assert.Less(t, after, before/2, "the frozen scope's memory went to swap")
	if swap, err := os.ReadFile(filepath.Join(CgroupRoot, path, "memory.swap.current")); err == nil {
		n, _ := strconv.ParseInt(strings.TrimSpace(string(swap)), 10, 64)
		assert.Greater(t, n, int64(50<<20), "and into swap, not only dropped cache")
	}

	require.NoError(t, c.Thaw(ctx, unit))
	_, err = io.WriteString(stdin, "check\n")
	require.NoError(t, err)
	line, err = out.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "intact\n", line)
}
```

(`io` joins the imports too.)

- [ ] **Step 2: Run** `loom job run -d "P1 live lend contract" -- sh -c 'CGO_ENABLED=0 go test -tags systemd_live -count=1 -run "TestConfiner_Live" -v ./sched/'`. Expected: PASS for all three live tests. If the reclaim on a frozen scope frees nothing, stop: the lend design depends on it, so take it to the owner.

### Task 1.7: Gates and commit

- [ ] **Step 1:** `loom job run -d "P1 gates" -- sh -c 'CGO_ENABLED=0 go test -count=1 ./sched/... ./config/... && CGO_ENABLED=0 go vet ./sched/... ./config/... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./... && gofmt -l sched config'`. Expected: every test passes, vet is clean, both cross builds succeed, and gofmt lists nothing. `core` still builds, since nothing it calls changed signature.
- [ ] **Step 2:** Commit:

```bash
git add sched/borrow.go sched/borrow_test.go sched/giveback.go sched/giveback_test.go
git commit -m "feat(sched,config): borrowing memory: Borrow, GiveBack, CgroupReclaim and the borrow switch" -- sched/ config/
```

---

## Package 2: core (admission, the give-back driver, the skip paths)

### Task 2.1: State, seams and routing

**Files:**
- Modify: `core/queue.go` (the `queueState` fields after `ladderErr`; the map literal beside `cpuMarks`)
- Modify: `core/queue_watch.go` (beside `var readMemAvailable`)
- Modify: `core/model.go` (the result switch after `case sliceHighSet:`)

- [ ] **Step 1:** Add to `queueState`:

```go
	// Memory borrowing (spec 2026-11-06 §6): the give-back machine's state,
	// and the borrowers whose freeze failed, which are given back by requeue.
	giveBack sched.GiveBackState
	noLend   map[sched.JobID]bool
```

Initialize `noLend: map[sched.JobID]bool{},` beside `cpuMarks`. In `core/queue_watch.go`: `var readSwapFree = sched.ReadSwapFreeKB`. In `core/model.go`: `case giveBackDone: m.deliverGiveBackDone(msg)`.

### Task 2.2: Greedy inputs and the lent job's prediction

**Files:** Modify `core/queue_greedy.go`.

- [ ] **Step 1:** In `greedyInputs`, build `g` with `Used: map[sched.JobID]int64{}`. In the running loop, after `used += ...`: `if u, ok := q.usage[rec.ID]; ok { g.Used[rec.ID] = u.RSSKB }`. After the budget: `g.Tight = sched.Tight(avail, int64(p.ReserveGB)*kbPerGB)`.
- [ ] **Step 2:** In `predictRunning`, change the expected end to `end := rec.Started.Add(expect(rec) + rec.LentFor(now))`.

### Task 2.3: Admission starts borrowers

**Files:** Modify `core/queue_admit.go`; create `core/queue_borrow.go`.

- [ ] **Step 1:** In `admitJobs`, replace the greedy/claim decision and the grant with:

```go
		var dec sched.Decision
		var bor sched.Borrowing
		g, greedy := m.greedyInputs(policy, now)
		if greedy {
			dec = sched.AdmitGreedy(jobs, policy, now, g)
			bor = sched.Borrow(jobs, dec, policy, now, g)
			m.makeFirm(bor.Firm)
		} else {
			dec = sched.Admit(jobs, policy, now, q.holding)
		}
		maps.Copy(waiting, dec.Waiting)
		granted := dec
		granted.Waiting = maps.Clone(dec.Waiting)
		for _, id := range bor.Start {
			delete(waiting, id)
			delete(granted.Waiting, id)
		}
		start, starved, borrow, pred = dec.Start, dec.Starved, bor.Start, g.Predict
		// Spare cores for the jobs it starts, when nothing else waits (job
		// scaling spec §5.3); a borrower gets none (memory borrowing spec
		// §5.2).
		spare = sched.Grant(granted, jobs, policy, q.holding, func(r sched.Record) *sched.Belief { return q.beliefOf(r.Signature) })
```

Declare `var borrow []sched.JobID` and `var pred func(sched.Record, time.Time) sched.Prediction` beside `start`. After the firm start loop, add:

```go
	for _, id := range borrow {
		if i := q.index(id); i >= 0 && q.store.Jobs[i].State == sched.Queued {
			m.startJob(i, policy, now, sched.Claim{}, id == starved)
			m.markBorrower(i, pred, now)
			started = true
		}
	}
```

- [ ] **Step 2:** In `deliverJobStarted`, inside `if r.uncontained != "" {`:

```go
		if rec.Borrower {
			// Nothing could give it back (memory borrowing spec §10).
			rec.Borrower = false
			log.For("core").Warn("queue.borrowed_uncontained", "job", int64(rec.ID), "why", r.uncontained)
		}
```

- [ ] **Step 3:** Create `core/queue_borrow.go` holding `markBorrower`, `makeFirm` and the driver of Task 2.4:

```go
package core

import (
	"context"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// markBorrower makes job i, just started, a borrower (memory borrowing spec
// §5.2), and logs its predicted peak against the budget.
func (m *Model) markBorrower(i int, pred func(sched.Record, time.Time) sched.Prediction, now time.Time) {
	q := m.queue
	rec := &q.store.Jobs[i]
	rec.Borrower = true
	p := pred(*rec, now)
	log.For("core").Info("queue.borrowed", "job", int64(rec.ID), "peak_kb", sched.PeakSum([]sched.Prediction{p}, now, p.End()))
}

// makeFirm makes the running borrowers ids firm (memory borrowing spec §5.3).
func (m *Model) makeFirm(ids []sched.JobID) {
	q := m.queue
	for _, id := range ids {
		if i := q.index(id); i >= 0 && q.store.Jobs[i].Borrower {
			q.store.Jobs[i].Borrower = false
			log.For("core").Info("queue.firm", "job", int64(id))
			q.moved()
		}
	}
}
```

### Task 2.4: The give-back driver

**Files:** `core/queue_borrow.go`, `core/queue_watch.go`.

- [ ] **Step 1:** Append to `core/queue_borrow.go`:

```go
// giveBackDone is a give-back action's result (memory borrowing spec §6):
// the kill's, freeze's or thaw's error, and after a lend the reclaim's.
type giveBackDone struct {
	act        sched.Action
	at         time.Time
	err        error
	reclaimErr error
}

// stepGiveBack runs one pass of the give-back machine after a sample while
// any borrower runs (memory borrowing spec §6.1), in fallback mode too,
// and one confinement action at a time with the ladder's. A cancelled lent
// job is thawed first, so it can act on its SIGTERM.
func (m *Model) stepGiveBack(now time.Time) {
	q := m.queue
	if q.laddering || q.availKB <= 0 {
		return
	}
	g, greedy := m.greedyInputs(q.policy, now)
	in := sched.GiveBackInput{Now: now, HeadroomKB: q.availKB, ReserveKB: int64(q.policy.ReserveGB) * kbPerGB, CanLend: greedy}
	if kb, err := readSwapFree(); err == nil {
		in.SwapFreeKB = kb
	}
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State != sched.Running || !rec.Borrower || rec.Cgroup == "" || rec.Requeue != "" {
			continue
		}
		if rec.CancelReason != "" {
			if !rec.LentAt.IsZero() && greedy {
				m.applyGiveBack(sched.Action{Kind: sched.ActResume, Job: rec.ID}, now)
				return
			}
			continue
		}
		j := sched.BorrowerJob{ID: rec.ID, Started: rec.Started, LentAt: rec.LentAt, LentFor: time.Duration(rec.LentSecs) * time.Second,
			WorkingKB: q.usage[rec.ID].RSSKB, NoLend: q.noLend[rec.ID]}
		if !rec.LentAt.IsZero() && greedy {
			j.Fits = sched.BorrowFits(q.store.Jobs, g, rec.ID, now)
		}
		in.Jobs = append(in.Jobs, j)
	}
	if len(in.Jobs) == 0 {
		return
	}
	var act *sched.Action
	q.giveBack, act = sched.GiveBack(q.giveBack, in)
	if act != nil {
		m.applyGiveBack(*act, now)
	}
}

// applyGiveBack carries out act on a borrower (memory borrowing spec
// §6.3–§6.5). A requeue writes the scope's cgroup.kill once the record asks
// for it, and settleGone then requeues the job as a give-back. A lend
// freezes the scope over D-Bus, then asks the kernel to reclaim its working
// set. A resume thaws it. The record changes only when the call returns.
func (m *Model) applyGiveBack(act sched.Action, now time.Time) {
	q := m.queue
	i := q.index(act.Job)
	if i < 0 {
		return
	}
	rec := &q.store.Jobs[i]
	c, root, unit, cg, kb := q.confiner, q.cgroupRoot, rec.Unit, rec.Cgroup, q.usage[rec.ID].RSSKB
	if act.Kind == sched.ActRequeue {
		rec.Requeue = fmt.Sprintf("it lent its memory back (%.1f GB free)", float64(q.availKB)/kbPerGB)
		rec.GivingBack = true
		q.moved()
		m.saveQueueQuiet("give back")
	}
	q.laddering = true
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		done := giveBackDone{act: act, at: now}
		switch act.Kind {
		case sched.ActRequeue:
			done.err = sched.CgroupKill(root, cg)
		case sched.ActLend:
			if done.err = c.Freeze(ctx, unit); done.err == nil {
				done.reclaimErr = sched.CgroupReclaim(root, cg, kb)
			}
		case sched.ActResume:
			done.err = c.Thaw(ctx, unit)
		}
		return done
	})
}

// deliverGiveBackDone records a give-back action that returned. A failed
// kill is retried; a failed freeze makes the next give-back of that job a
// requeue; a failed thaw is retried at the next pass.
func (m *Model) deliverGiveBackDone(r giveBackDone) {
	q := m.queue
	q.laddering = false
	i := q.index(r.act.Job)
	if i < 0 || q.store.Jobs[i].State != sched.Running {
		return
	}
	rec := &q.store.Jobs[i]
	if r.err != nil {
		log.For("core").Warn("queue.give_back_failed", "job", int64(rec.ID), "action", string(r.act.Kind), "err", r.err)
		q.ladderErr = fmt.Sprintf("%s job %d: %v", r.act.Kind, rec.ID, r.err)
		switch r.act.Kind {
		case sched.ActRequeue:
			rec.Requeue, rec.GivingBack = "", false // the kill did not go out
		case sched.ActLend:
			q.noLend[rec.ID] = true
		}
		q.moved()
		return
	}
	q.ladderErr = ""
	stake := int64(r.at.Sub(rec.Started).Seconds()) - rec.LentSecs
	switch r.act.Kind {
	case sched.ActRequeue:
		log.For("core").Info("queue.gave_back", "job", int64(rec.ID), "action", "requeue", "headroom_kb", q.availKB,
			"working_kb", q.usage[rec.ID].RSSKB, "stake_s", stake)
	case sched.ActLend:
		// Stamped when the freeze returned, so a sample pass that began
		// later read the frozen cgroup (the reconcile's thawed-by-hand case
		// trusts only those).
		rec.LentAt, rec.Laddered = time.Now(), true
		log.For("core").Info("queue.gave_back", "job", int64(rec.ID), "action", "lend", "headroom_kb", q.availKB,
			"working_kb", q.usage[rec.ID].RSSKB, "stake_s", stake)
		if r.reclaimErr != nil {
			log.For("core").Warn("queue.reclaim_failed", "job", int64(rec.ID), "err", r.reclaimErr)
		}
	case sched.ActResume:
		lent := time.Since(rec.LentAt)
		rec.LentSecs += int64(lent.Seconds())
		rec.LentAt = time.Time{}
		log.For("core").Info("queue.resumed", "job", int64(rec.ID), "lent_s", int64(lent.Seconds()))
	}
	q.moved()
	m.saveQueueQuiet("give back")
}
```

- [ ] **Step 2:** In `deliverQueuePass` (`core/queue_watch.go`), after `m.stepLadder(time.Now())`: `m.stepGiveBack(time.Now())`.

### Task 2.5: The skip paths, the reconcile and the requeue

**Files:** `core/queue_ladder.go`, `core/queue_watch.go`, `core/queue_ops.go`, `core/queue_view.go`.

- [ ] **Step 1:** In `stepLadder`'s loop condition, add `|| !rec.LentAt.IsZero()` (a lent job is the give-back's, §7.3).
- [ ] **Step 2:** In `applyUsage`'s reconcile, make the block:

```go
		if cg := ob.cg; cg != nil && !q.laddering {
			lent := !rec.LentAt.IsZero()
			if lent && !cg.Frozen && r.at.After(rec.LentAt) {
				// Thawed by hand: a running borrower again (memory
				// borrowing spec §10).
				rec.LentSecs += int64(r.at.Sub(rec.LentAt).Seconds())
				rec.LentAt, lent = time.Time{}, false
				q.moved()
			}
			// The cgroup decides the rung (cgroup spec §15, 17): frozen is
			// paused, unless the job is lent (memory borrowing spec §7.3),
			// a memory.high is squeezed, neither is none.
			next := sched.Rung("")
			switch {
			case cg.Frozen && lent:
			case cg.Frozen:
				next = sched.RungPaused
			case cg.HighKB > 0:
				next = sched.RungSqueezed
			}
```

(the rest of the block is unchanged).
- [ ] **Step 3:** In `settleGone`, make the requeue condition `if rec.GivingBack || (!rec.NoRequeue && rec.Requeues < sched.MaxRequeues) {`.
- [ ] **Step 4:** In `requeueJob`, before `*rec = sched.Record{`:

```go
	giveBack := rec.GivingBack
	requeues, gaveBack := rec.Requeues+1, rec.GaveBack
	if giveBack {
		requeues, gaveBack = rec.Requeues, rec.GaveBack+1
	}
```

In the literal, use `Requeues: requeues`, and add `GaveBack: gaveBack, NoBorrow: giveBack,`. Replace the last line with:

```go
	delete(q.noLend, rec.ID)
	if !giveBack {
		m.sendRequeueNotice(*rec, why)
	}
```

and add `"give_back", giveBack` to the `queue.requeued` log.
- [ ] **Step 5:** In `setSchedulerCaps`, add `s.Borrow = file.Borrow` and extend the comment ("nor borrow (memory borrowing spec §9)"). In `cloneSchedulerSettings`, add:

```go
	if s.Borrow != nil {
		on := *s.Borrow
		s.Borrow = &on
	}
```

### Task 2.6: Core tests

**Files:** Create `core/queue_borrow_test.go`; modify `core/queue_holds_test.go` (`TestJobQueue_ACapsEditKeepsTheReleaseSwitch`).

- [ ] **Step 1: Write the tests**

```go
package core

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWQBorrow is newWQLadder's contained queue with 20 GB available, 15 GB
// of swap free, an 8-core 14 GB cap and borrowing on, its jobs claiming
// 12 GB each and using wqCgroupFiles' 768 MB.
func newWQBorrow(t *testing.T, jobs int) *wqLadder {
	t.Helper()
	l := newWQLadder(t, 20, jobs)
	prev := readSwapFree
	readSwapFree = func() (int64, error) { return 15 << 20, nil }
	t.Cleanup(func() { readSwapFree = prev })
	wqConfig(t, l.m, `{"cpus": 8, "memory_gb": 14, "io_jobs": 2, "borrow": true}`)
	for _, id := range l.ids {
		l.rec(t, id).Claim = sched.Claim{CPUs: 2, MemoryGB: 12}
	}
	wqPass(t, l.m)
	return l
}

func TestQueueBorrow_AJobBorrowsWhatAFirmJobLeaves(t *testing.T) {
	l := newWQBorrow(t, 1)
	sub := wqJob(t, "sleep", "30")
	sub.Claim = sched.Claim{CPUs: 2, MemoryGB: 6}
	id, _, err := l.m.submitJob(sub)
	require.NoError(t, err)
	rec := wqRecord(t, l.m, id)
	assert.Equal(t, sched.Running, rec.State, "12 + 6 GB of claims pass 14, but 0.9 + 6 GB used fits: %q", l.m.queue.waiting[id])
	assert.True(t, rec.Borrower)
}

func TestQueueBorrow_OffByDefault(t *testing.T) {
	l := newWQBorrow(t, 1)
	wqConfig(t, l.m, `{"cpus": 8, "memory_gb": 14, "io_jobs": 2}`)
	sub := wqJob(t, "sleep", "30")
	sub.Claim = sched.Claim{CPUs: 2, MemoryGB: 6}
	id, _, err := l.m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.Queued, wqRecord(t, l.m, id).State)
	assert.Contains(t, l.m.queue.waiting[id], "at its peak")
}

func TestQueueBorrow_TightnessRequeuesAYoungBorrowerQuietly(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	l.rec(t, id).Borrower = true
	l.setAvail(5) // the 4 GB reserve plus 2 GB is tight
	wqPass(t, l.m)
	require.True(t, l.rec(t, id).GivingBack)
	kill, err := os.ReadFile(filepath.Join(l.root, l.rec(t, id).Cgroup, "cgroup.kill"))
	require.NoError(t, err)
	assert.Equal(t, "1", string(kill))

	require.NoError(t, sched.SignalGroup(l.rec(t, id).PID, syscall.SIGKILL))
	l.setAvail(64)
	wqWait(t, "the give-back requeue", func() bool { wqPass(t, l.m); return l.rec(t, id).Attempt == 1 })
	rec := l.rec(t, id)
	assert.Equal(t, 0, rec.Requeues, "a give-back is no requeue toward MaxRequeues")
	assert.Equal(t, 1, rec.GaveBack)
	assert.True(t, rec.NoBorrow, "its next attempt runs firm only")
	assert.False(t, rec.Borrower)
}

func TestQueueBorrow_TightnessLendsAnOlderBorrowerAndItResumes(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Borrower, rec.Started = true, time.Now().Add(-10*time.Minute)
	l.write(t, id, map[string]string{"memory.reclaim": ""})
	l.setAvail(5)
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Freeze "+l.rec(t, id).Unit)
	require.False(t, l.rec(t, id).LentAt.IsZero())
	data, err := os.ReadFile(filepath.Join(l.root, l.rec(t, id).Cgroup, "memory.reclaim"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "swappiness=200")

	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	assert.Empty(t, l.rec(t, id).Rung, "lent, not the ladder's pause")
	assert.Empty(t, l.m.ladderHoldoff(), "a lent job holds no admission off")

	l.setAvail(64)
	l.rec(t, id).LentAt = time.Now().Add(-31 * time.Second)
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Thaw "+l.rec(t, id).Unit)
	assert.True(t, l.rec(t, id).LentAt.IsZero())
	assert.Positive(t, l.rec(t, id).LentSecs)
}

func TestQueueBorrow_AFailedFreezeGivesBackByRequeue(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Borrower, rec.Started = true, time.Now().Add(-10*time.Minute)
	l.fake.fail = map[string]error{"Freeze": errNoBusForTest}
	l.setAvail(5)
	wqPass(t, l.m)
	require.True(t, l.rec(t, id).LentAt.IsZero())
	l.m.queue.giveBack.Last = time.Now().Add(-11 * time.Second)
	wqPass(t, l.m)
	assert.True(t, l.rec(t, id).GivingBack, "the next give-back is a requeue")
}

func TestQueueBorrow_ACancelThawsALentJob(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Borrower, rec.LentAt, rec.CancelReason = true, time.Now().Add(-time.Minute), "by the test"
	l.write(t, id, map[string]string{"cgroup.events": "populated 1\nfrozen 1\n"})
	wqPass(t, l.m)
	assert.Contains(t, l.fake.Calls(), "Thaw "+l.rec(t, id).Unit)
}

func TestQueueBorrow_ALentJobThawedByHandRunsAgain(t *testing.T) {
	l := newWQBorrow(t, 1)
	id := l.ids[0]
	rec := l.rec(t, id)
	rec.Borrower, rec.LentAt = true, time.Now().Add(-time.Minute)
	wqPass(t, l.m) // its cgroup.events says it is not frozen
	assert.True(t, l.rec(t, id).LentAt.IsZero())
	assert.Positive(t, l.rec(t, id).LentSecs)
}
```

In `TestJobQueue_ACapsEditKeepsTheReleaseSwitch`, add `"borrow": true` to the block, and add these assertions: `require.NotNil(t, got.Borrow)`, `assert.True(t, *got.Borrow)`.

- [ ] **Step 2: Run** `loom job run -d "P2 core borrow tests" -- sh -c 'CGO_ENABLED=0 go test -count=1 -run "TestQueueBorrow|TestQueueLadder|TestQueueGreedy|TestQueueContain|TestJobQueue_ACapsEdit" ./core/'`. Expected: PASS. Then run the whole `core` package with the `sched` suite, plus vet, as one job. If a fixture's numbers disagree with the real budget (the reserve, or `wqCgroupFiles`' working set), fix the test's numbers, not the code, and note it in the execution notes.

- [ ] **Step 3: Commit** `feat(core): start borrowers, give memory back by requeue or lend, resume lent jobs` with the files of Tasks 2.1–2.6.

---

## Package 3: surfaces and docs

### Task 3.1: View fields and the protocol

**Files:** `core/queue_view.go`, `docs/specs/protocol.md` (generated).

- [ ] **Step 1:** In `JobView`, after `NoRequeue bool`:

```go
	// Memory borrowing (spec 2026-11-06 §8): whether it runs as a borrower,
	// when it was lent (zero unless lent), and its give-back requeues.
	Borrower bool
	LentAt   time.Time
	GaveBack int
```

In the `JobView` literal: `Borrower: r.Borrower, LentAt: r.LentAt, GaveBack: r.GaveBack,`. In `QueueView`, after `LadderErr`: `BorrowedKB int64 // the running borrowers' predicted peaks, summed (memory borrowing spec §8)`. In the greedy branch of the view's builder, after `v.PeakKB = …`:

```go
		for _, r := range running {
			if p, ok := g.Running[r.ID]; ok && r.Borrower {
				v.BorrowedKB += sched.PeakSum([]sched.Prediction{p}, now, p.End())
			}
		}
```

- [ ] **Step 2:** `loom job run -d "P3 protocol" -- sh -c 'CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update && CGO_ENABLED=0 go test -count=1 ./core/rpc'`.

### Task 3.2: The CLI and the work view

**Files:** `internal/cli/job_sizing.go`, `internal/cli/job_follow.go`, `ui/work.go`, and their tests.

- [ ] **Step 1:** In `startedLine`, before `return line`: `if v.Borrower { line += " on borrowed memory" }`. In the list's claim text (after the rung): 

```go
	if v.State == sched.Running {
		switch {
		case !v.LentAt.IsZero():
			text += " lent"
		case v.Borrower:
			text += " borrowing"
		}
	}
```

- [ ] **Step 2:** In `jobFollower`, add `lent bool` and `gaveBack int`. Have `noteAttempt` record `f.gaveBack = v.GaveBack` with the first view. When the attempt changes, reset `f.lent = false`, and when `v.GaveBack > f.gaveBack` return `fmt.Sprintf("job %d restarted to lend memory back; following its next attempt", v.ID)`, updating `f.gaveBack`. Add the following, and call it beside `noteRung` in the running case:

```go
// noteLent is the line for a borrower lent or resumed (memory borrowing
// spec §8), "" for none.
func (f *jobFollower) noteLent(v core.JobView) string {
	lent := !v.LentAt.IsZero()
	if v.State != sched.Running || lent == f.lent {
		return ""
	}
	f.lent = lent
	if lent {
		return fmt.Sprintf("job %d lent its memory back: paused", v.ID)
	}
	return fmt.Sprintf("job %d resumed", v.ID)
}
```

- [ ] **Step 3:** In `ui/work.go` `workRowFacts`'s running case, after the rung:

```go
		switch {
		case !j.LentAt.IsZero():
			extra += "  lent (memory) " + sched.RoughDuration(now.Sub(j.LentAt))
		case j.Borrower:
			extra += "  borrowing"
		}
```

In the greedy header, after the `mem = fmt.Sprintf("budget …")` line: `if q.BorrowedKB > 0 { mem += " · borrowed " + workGB(q.BorrowedKB) }`.
- [ ] **Step 4:** Add tests beside the existing ones:
  - `TestJobFollow_BorrowLines`, which feeds a follower views: started as a borrower, then lent, then resumed, then the next attempt with `GaveBack` 1. It asserts each line.
  - `TestStartedLine_OnBorrowedMemory`.
  - A `ui/work` row test asserting `borrowing` and `lent (memory)`.

  Run `loom job run -d "P3 cli ui" -- sh -c 'CGO_ENABLED=0 go test -count=1 ./internal/cli/ ./ui/'`.

### Task 3.3: The end message

**Files:** `core/queue_notice.go`, `core/queue_notice_test.go`.

- [ ] **Step 1:** In `noticeHead`'s started case, after `if after != "" { … }`: `if note := borrowNote(rec); note != "" { wait += ", " + note }`, and add:

```go
// borrowNote is what a borrower's end message adds (memory borrowing spec
// §8): its give-back restarts and the time it was lent, "" for neither.
func borrowNote(rec sched.Record) string {
	var parts []string
	switch rec.GaveBack {
	case 0:
	case 1:
		parts = append(parts, "restarted once to lend memory back")
	default:
		parts = append(parts, fmt.Sprintf("restarted %d times to lend memory back", rec.GaveBack))
	}
	if rec.LentSecs > 0 {
		parts = append(parts, "lent memory back for "+roundedDur(time.Duration(rec.LentSecs)*time.Second))
	}
	return strings.Join(parts, ", ")
}
```

- [ ] **Step 2:** Add `TestNoticeHead_SaysWhatABorrowerGaveBack`, a table over (GaveBack 0/1/2, LentSecs 0/240) that pins the parenthesis.

### Task 3.4: Docs

- [ ] **USAGE.md:** add a `borrow` row to the scheduler table (`false`; "`true` or `false`: start a job greedy admission holds back on memory the running jobs claim but don't use, and give it back when memory gets tight"). Add a **Borrowing** paragraph after **Greedy admission** covering who may borrow, the tight state, requeue or lend, resume, what the work view and followers show, and that it is off until the owner turns it on.
- [ ] **sched/README.md:** add the `Borrow`/`GiveBack`/`CgroupReclaim` facts beside the ladder's.
- [ ] **sched/CLAUDE.md:** add a rule:

  > **Keep `Borrow`, `BorrowFits` and `GiveBack` pure, count borrowers at their predictions (they lend to no one), and let `CgroupReclaim` refuse anything but a job's scope.** The model calls them at every pass and sample. A borrower counted at its use would lend another borrower memory that both can claim back at once, and nothing gives that back but the emergency ladder.

  **Enforced** by `TestBorrow_BorrowersLendToNoOne`, `TestGiveBack_TheStakeDecidesRequeueOrLend` and `TestCgroupReclaim`. Check the budget with `go run ./tools/claudemd -v`.
- [ ] **docs/claude/the-work-queue.md:** add a "Borrowing" section with these rules:
  - give-back actions share `q.laddering` with the ladder;
  - lent state lives outside `Rung`, and `stepLadder`, `ladderHoldoff` and the rung reconcile must skip lent jobs;
  - `requeueJob` keys off `GivingBack`.
- [ ] Commit `feat(core,cli,ui,rpc): show borrowers, lent jobs and give-backs` and `docs(sched,core): borrowing memory`, or one commit for both.

### Task 3.5: Final gates

- [ ] `loom job submit -d "final: vet, cross, gofmt, full" -- sh -c 'CGO_ENABLED=0 go vet ./... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./... && test -z "$(gofmt -l $(git ls-files "*.go" | grep -v ^vendor/))" && CGO_ENABLED=0 go test -count=1 ./...'`
- [ ] Then `--after` it: `CC=clang CGO_ENABLED=1 go test -race ./...`.
- [ ] Then `go test -tags e2e ./e2e/...`, plus the live contract `-tags systemd_live -run TestConfiner_Live ./sched`.
- [ ] Expected: all pass. Record the job numbers in the execution notes.

---

## Execution notes

Executed inline on 2026-11-06 on `aidanb/changing-resource-alocation`: P1 e8d3e5c8, P2 727bc536, P3 in the commit that adds these notes. Gate jobs: 968 (P1 red), 969 (P1: sched and config suites, live contract, vet, cross builds), 971/973/974/975 (P2: borrow tests, three mutation checks, the core and sched suites), 976 and 980 (P3: cli, ui, rpc, the docs lint).

- **The live lend contract held at its first run.** A frozen 201 MB scope fell to 55 MB after `memory.reclaim` with 200 MB in swap, and its memory was intact after the thaw (systemd 261, kernel 7.2, zram).
- **The protocol reference was regenerated in P2 as well as P3:** `Policy.Borrow`, added in P1, is part of the queue view on the wire. Adding fields is compatible, so `Protocol` is unchanged.
- **Core tests check the temp cgroup root's `memory.reclaim` file** rather than a fake confiner's `Reclaim`: `CgroupReclaim` is a file write, not a D-Bus call, so the fake confiner is unchanged.
- **The work view's per-job detail line (spec §8) was dropped.** The view has no per-job detail line; the row's state text (`borrowing`, `lent (memory) 3m`), the follower's lines and the end message carry it.
- **`requeueJob`'s log names the new attempt.** It logged the attempt after the new one: the record was already replaced when it read `Attempt+1`.
- **`LentAt` is stamped when the freeze returns**, not at dispatch, so the reconcile's thawed-by-hand case trusts only passes that read the cgroup after the freeze.
- **Mutation checks** (job 973): dropping `stepLadder`'s lent skip, the reconcile's lent case, or `requeueJob`'s give-back counting each turned a `TestQueueBorrow` test red.
- **Merged with main's named job locks** (d5b044d9): a job left waiting on memory took no lock in `AdmitGreedy`'s walk, so `Borrow` now holds locks as `admitWith` does (running jobs, the decision's starts, earlier borrowers); `TestBorrow_KeepsNamedLocks` was red before the fix. The merge's two conflicts were fields added on both sides.
- **Not fixed:** `cloneSchedulerSettings` (core) clones no `Contain` pointer, a gap from before this work.

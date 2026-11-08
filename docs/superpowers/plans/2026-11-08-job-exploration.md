# Job exploration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On a quiet host, lend a job whose command has been measured at too few widths the cores to run wider, so the scaling fit gets the data points it needs (spec `docs/superpowers/specs/2026-11-08-job-exploration-design.md`).

**Architecture:** A pure `ExploreTarget`/`ExploreWidth` beside `SpareWidth` in `sched`, used by `Grant` when `Policy.Explore` is on and the host's idle cores (a new pure window over `/proc/stat` readings) leave room. `core` takes the readings on its tick, passes the idle cores to `Grant`, marks explored records and runs, and the CLI, TUI and belief text show it.

**Tech Stack:** Go, testify. Every build and test runs through `loom job run` (the work queue).

**Execution:** inline, one package at a time (owner's choice, 2026-11-08). Each package ends with its gate and one commit by path. Never `git add -A`/`.`/`-a`.

**Gate commands** (run each through the queue, e.g. `loom job run -d "exploration P1 gate" -- env CGO_ENABLED=0 go test ./sched/ ./config/ -count=1`):
- `env CGO_ENABLED=0 go test <packages> -count=1`
- `env CGO_ENABLED=0 go vet ./...`
- `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')` (no output), run directly: it compiles nothing.

---

## File map

| file | package | change |
|---|---|---|
| `sched/scaling.go` | P1 | `Scaling.Widest`, `Scaling.Top`, `exploreFactor`, `minExploreStep`, `ExploreTarget`, `MinExploreWidth` |
| `sched/grant.go` | P1 | `grantable`, `ExploreWidth`, `HostIdle`, `Grants`, `exploreStep`, `Grant`'s new input and result |
| `sched/job.go` | P1, P3 | `Policy.Explore` (P1); `Record.Explored` (P3) |
| `sched/policy.go` | P1 | `Resolve` reads `explore` |
| `config/settings.go` | P1 | `SchedulerSettings.Explore`, its clone |
| `core/queue_ops.go` | P1 | the caps edit keeps `explore` |
| `core/queue_admit.go` | P1, P3 | the `Grant` call (P1: no idle reading yet); readings, idle cores, explored starts, `queue.explored` (P3) |
| `sched/hostcpu.go`, `sched/hostcpu_linux.go`, `sched/hostcpu_other.go` | P2 | `CPUTimes`, `CPUReading`, `IdleWindow`, `parseHostStat`, `IdleCores`, `KeepReadings`, `ReadHostCPU` |
| `sched/belief.go` | P3 | `Run.Explored`, `RunOf` |
| `core/queue.go`, `core/queue_watch.go` | P3 | `queueState.hostCPU`, `hostCPUFailing`; the `readHostCPU` seam |
| `core/queue_view.go` | P3 | `JobView.Explored`, `JobView.Explore` |
| `core/testmain_test.go` | P3 | tests see no host CPU reading unless they set one |
| `internal/cli/job_list.go`, `internal/cli/job_sizing.go` | P4 | the wait column, the sized line |
| `ui/work.go` | P4 | the running row's `exploring N cores (was M)` |
| `sched/beliefs_view.go`, `sched/belief_text.go` | P4 | `BeliefView.Explore`, `ExploreFlat`, the scaling line's clause |
| `USAGE.md`, `sched/README.md`, `docs/claude/the-work-queue.md`, `sched/CLAUDE.md`, the spec | P4 | docs |

---

## Package 1: the explore rule in `sched`, and the switch

**Files:**
- Modify: `sched/scaling.go`, `sched/grant.go`, `sched/job.go:114-140` (`Policy`), `sched/policy.go:39-127`, `config/settings.go:173-229`, `core/queue_ops.go:330-339`, `core/queue_admit.go:70,126,135`
- Test: `sched/scaling_test.go`, `sched/grant_test.go`, `sched/agent_test.go:105`, `sched/run_test.go:75`, `sched/policy_test.go`, `config/settings_test.go:145`, `core/queue_holds_test.go:170`

### Task 1.1: `Scaling.Widest` and `Scaling.Top`

- [ ] **Step 1: Write the failing assertions** at the end of `TestScalingOf` (`sched/scaling_test.go`, before its closing brace):

```go
	assert.Equal(t, 4, race.Widest, "one width")
	assert.Zero(t, race.Top, "no pair")
	assert.Equal(t, 2, gotest.Widest)
	assert.Equal(t, 1.0, gotest.Top, "the two widest, 1 and 2: R = 2.03, held to 1")
	assert.Equal(t, 8, clippy.Widest)
	assert.Zero(t, clippy.Top, "4 cores 3.37 busy, 8 cores 1.02: R ≤ 1")
	assert.Equal(t, 8, ScalingOf(belief(at(4, 7.81), at(8, 12))).Widest, "an overrunner's widest is still known")
```

- [ ] **Step 2: Run it to see it fail**

Run: `loom job run -d "P1 scaling red" -- env CGO_ENABLED=0 go test ./sched/ -run TestScalingOf -count=1`
Expected: FAIL to compile: `race.Widest undefined (type Scaling has no field or method Widest)`.

- [ ] **Step 3: Implement.** In `sched/scaling.go`, add to `Scaling` after `Fit`:

```go
	// Widest is the most cores any of the runs read had (exploration spec
	// §3), the overrunner's too; 0 with none. Top is the pair's p of the
	// two widest of Widths, set only with 2 widths or more.
	Widest int     `json:"widest,omitempty"`
	Top    float64 `json:"top,omitempty"`
```

In `ScalingOf`, after `s.Busy, _ = median(busies)`:

```go
	s.Widest = most
```

and inside `if len(byWidth) >= 2 {`, after `s.Fit = fitParallel(s.Widths, byWidth)`:

```go
		n := len(s.Widths)
		a, b := s.Widths[n-2], s.Widths[n-1]
		busyA, _ := median(byWidth[a])
		busyB, _ := median(byWidth[b])
		s.Top = pairParallel(a, busyA, b, busyB)
```

- [ ] **Step 4: Run it to see it pass** (same command). Expected: PASS.

### Task 1.2: `ExploreTarget` and `MinExploreWidth`

- [ ] **Step 1: Write the failing tests** in `sched/scaling_test.go`:

```go
func TestExploreTarget(t *testing.T) {
	assert.Zero(t, ExploreTarget(nil, 14), "no belief")
	assert.Zero(t, ExploreTarget(belief(done(1<<20, 10, 10)), 14), "no width (an exclusive run)")
	assert.Equal(t, 4, ExploreTarget(belief(at(2, 0.97)), 14), "one width: twice it, whatever its p")
	assert.Equal(t, 2, ExploreTarget(belief(at(1, 1.0)), 14), "one core: 2")
	assert.Equal(t, 14, ExploreTarget(belief(at(8, 1.0)), 14), "held to the cap")
	assert.Zero(t, ExploreTarget(belief(at(14, 2.0)), 14), "the cap is no wider than its widest")
	assert.Zero(t, ExploreTarget(belief(at(4, 7.81)), 14), "an overrunner: the hints don't set its width")

	assert.Equal(t, 8, ExploreTarget(belief(at(2, 0.97), at(4, 2.0)), 14), "the two widest gain: p ≈ 1, 2× faster at 8")
	assert.Equal(t, 8, ExploreTarget(belief(at(2, 1.0), at(4, 1.6)), 14), "p ≈ 0.86: 1.43× faster at 8")
	assert.Zero(t, ExploreTarget(belief(at(2, 1.0), at(4, 1.3)), 14), "p ≈ 0.63: 1.18× at 8, under 1.25")
	assert.Zero(t, ExploreTarget(belief(at(2, 1.0), at(4, 1.0)), 14), "flat")
	assert.Equal(t, 8, ExploreTarget(belief(at(1, 1.0), at(2, 1.0), at(4, 2.0)), 14),
		"the two widest decide, not the fit over every pair")
}

func TestMinExploreWidth(t *testing.T) {
	for widest, want := range map[int]int{1: 2, 2: 3, 3: 5, 4: 6, 8: 12} {
		assert.Equal(t, want, MinExploreWidth(widest), "from %d", widest)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "P1 explore target red" -- env CGO_ENABLED=0 go test ./sched/ -run 'TestExploreTarget|TestMinExploreWidth' -count=1`
Expected: FAIL to compile: `undefined: ExploreTarget`.

- [ ] **Step 3: Implement** in `sched/scaling.go`. Add `"math"` to its imports, and after the scaling constants block:

```go
// The constants of exploring (exploration spec §3).
const (
	exploreFactor  = 2   // an explore's target, as a multiple of the widest width run
	minExploreStep = 1.5 // the narrowest explore, as a multiple of the widest width run
)

// ExploreTarget is the width a quiet host would run a job of belief b (nil
// for none) at to learn how its command scales (exploration spec §3): its
// widest width × exploreFactor, held to capCPUs. 0 when b explores
// nothing: it has no width, its command overruns, the held target is no
// wider than its widest width, or, with 2 widths or more, its two widest
// predict a speed at exploreFactor × the widest under floorGain × the
// speed at the widest. A pure function of its arguments.
func ExploreTarget(b *Belief, capCPUs int) int {
	s := ScalingOf(b)
	if s.Overruns || s.Widest < 1 {
		return 0
	}
	target := min(exploreFactor*s.Widest, capCPUs)
	if target <= s.Widest {
		return 0
	}
	if len(s.Widths) >= 2 {
		top := Scaling{Known: true, Parallel: s.Top}
		if top.factor(s.Widest)/top.factor(exploreFactor*s.Widest) < floorGain {
			return 0
		}
	}
	return target
}

// MinExploreWidth is the narrowest width an explore from widest takes
// (exploration spec §3): ⌈minExploreStep × widest⌉, and at least widest + 1.
// Two runs at one width differ in busy cores by a median 1.10×, so a
// narrower step mostly measures noise.
func MinExploreWidth(widest int) int {
	return max(int(math.Ceil(minExploreStep*float64(widest))), widest+1)
}
```

- [ ] **Step 4: Run them to see them pass** (same command). Expected: PASS.

### Task 1.3: `ExploreWidth`, `HostIdle`, `Grants` and the explore step in `Grant`

- [ ] **Step 1: Write the failing tests** in `sched/grant_test.go`:

```go
func TestExploreWidth(t *testing.T) {
	narrow := belief(at(2, 0.97))
	job := Record{Claim: Claim{CPUs: 2, MemoryGB: 4}, Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	assert.Equal(t, 4, ExploreWidth(job, narrow, 14))
	for name, edit := range map[string]func(*Record){
		"explicit cores":  func(r *Record) { r.Sources.CPUs = SourceExplicit },
		"family cores":    func(r *Record) { r.Sources.CPUs = SourceFamily },
		"the agent's":     func(r *Record) { r.Sources.CPUs = SourceAgent },
		"the default":     func(r *Record) { r.Sources.CPUs = SourceDefault },
		"explicit memory": func(r *Record) { r.Sources.Memory = SourceExplicit },
		"exclusive":       func(r *Record) { r.Claim = Claim{Exclusive: true} },
		"held to the cap": func(r *Record) { r.AboveCap = true },
	} {
		r := job
		edit(&r)
		assert.Zero(t, ExploreWidth(r, narrow, 14), name)
	}
}

func TestGrant_Explores(t *testing.T) {
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	p := szPolicy // 14 cores and 22 GB; the default claim, the reserve, 4 cores and 6 GB
	p.StarveAfter = 10 * time.Minute
	p.Explore = true
	narrow := belief(at(2, 0.97)) // one width, p reads 0: no ordinary grant widens it
	learned := func(id JobID, cores, mem int) Record {
		return Record{ID: id, Order: int64(id), State: Queued, Submitted: now, Claim: Claim{CPUs: cores, MemoryGB: mem},
			Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	}
	grant := func(b *Belief, idle HostIdle, jobs ...Record) Grants {
		return Grant(Admit(jobs, p, now, nil), jobs, p, nil, func(Record) *Belief { return b }, idle)
	}
	idle := func(cores float64) HostIdle { return HostIdle{Cores: cores, Known: true} }
	explored := func(spare map[JobID]Claim) Grants {
		g := Grants{Spare: spare, Explored: map[JobID]bool{}}
		for id := range spare {
			g.Explored[id] = true
		}
		return g
	}

	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 2, MemoryGB: 4}}), grant(narrow, idle(12), learned(1, 2, 4)),
		"12 idle cores less the reserve's 4 leave 8: 2 → 4, memory in proportion")
	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 1, MemoryGB: 2}}), grant(narrow, idle(5), learned(1, 2, 4)),
		"5 idle leave room for 1 more core: the smaller step, 3 = ⌈1.5 × 2⌉")
	assert.Equal(t, Grants{}, grant(narrow, idle(4), learned(1, 2, 4)), "no room for 3: no explore, and no ordinary grant either")
	wideOne := belief(at(4, 3.9)) // one width, p ≈ 0.99: an ordinary grant takes it to 8
	assert.Equal(t, Grants{Spare: map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}}, grant(wideOne, idle(5), learned(1, 4, 2)),
		"room for 5 cores, under the explore's 6: the ordinary grant, which idle cores don't bound")

	assert.Equal(t, Grants{}, grant(narrow, HostIdle{}, learned(1, 2, 4)), "idle cores unknown")
	off := p
	off.Explore = false
	assert.Equal(t, Grants{}, Grant(Admit([]Record{learned(1, 2, 4)}, off, now, nil), []Record{learned(1, 2, 4)}, off, nil,
		func(Record) *Belief { return narrow }, idle(12)), "explore off")

	waiter := Record{ID: 2, Order: 2, State: Queued, Submitted: now, Claim: Claim{CPUs: 13, MemoryGB: 1}, Sources: all(SourceExplicit)}
	assert.Equal(t, Grants{}, grant(narrow, idle(12), learned(1, 2, 4), waiter), "a job left waiting for cores")
	busy := Record{ID: 9, State: Running, Claim: Claim{CPUs: 8, MemoryGB: 2}, Started: now}
	assert.Equal(t, Grants{}, grant(narrow, idle(12), busy, learned(1, 2, 4)), "14 − 8 − 2 leaves only the reserve")

	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 1, MemoryGB: 5}}), grant(narrow, idle(12), learned(1, 2, 9)),
		"7 GB past the reserve: 4 cores would add 9, 3 cores add 5")
	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 2, MemoryGB: 2}, 2: {CPUs: 1, MemoryGB: 1}}),
		grant(narrow, idle(7), learned(1, 2, 2), learned(2, 2, 2)), "two explorers share 3 idle cores: 2, then 1")

	floored := belief(at(2, 0.97), at(4, 2.0)) // p ≈ 1 from two widths: its base is 4
	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 4, MemoryGB: 4}}), grant(floored, idle(12), learned(1, 4, 4)),
		"still gaining: 4 → 8")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "P1 grant red" -- env CGO_ENABLED=0 go test ./sched/ -run 'TestExploreWidth|TestGrant' -count=1`
Expected: FAIL to compile: `undefined: ExploreWidth`, `undefined: Grants`.

- [ ] **Step 3: Implement** in `sched/grant.go`. Replace `SpareWidth`'s first `if` with a shared check, and add the new types and functions:

```go
// grantable reports whether queued job r's claim may be widened past its
// base (job scaling spec §5.3): its cores learned from its own runs (not
// explicit, exclusive, the agent's, the family's or the default), and its
// memory neither explicit nor held to the cap.
func grantable(r Record) bool {
	return !r.Claim.Exclusive && r.Sources.CPUs == SourceLearned && r.Sources.Memory != SourceExplicit && !r.AboveCap
}
```

`SpareWidth` becomes:

```go
func SpareWidth(r Record, b *Belief) int {
	if !grantable(r) {
		return 0
	}
	s := ScalingOf(b)
	if !s.Known || s.Ideal <= r.Claim.CPUs {
		return 0
	}
	return s.Ideal
}
```

Add after it:

```go
// ExploreWidth is the width a quiet host could explore queued job r of
// belief b at (exploration spec §3): ExploreTarget(b, capCPUs) for a job a
// grant could widen, 0 for any other.
func ExploreWidth(r Record, b *Belief, capCPUs int) int {
	if !grantable(r) {
		return 0
	}
	return ExploreTarget(b, capCPUs)
}

// HostIdle is the host's mean idle logical CPUs over the last IdleWindow
// (exploration spec §4.3), and whether they are known.
type HostIdle struct {
	Cores float64
	Known bool
}

// Grants is what Grant lends: each widened job's spare claim, and the jobs
// among them whose width is an explore (exploration spec §4.2). Both are
// nil when nothing is lent.
type Grants struct {
	Spare    map[JobID]Claim
	Explored map[JobID]bool
}

// add lends c to job id, marked an explore when explored.
func (g *Grants) add(id JobID, c Claim, explored bool) {
	if g.Spare == nil {
		g.Spare = map[JobID]Claim{}
	}
	g.Spare[id] = c
	if explored {
		if g.Explored == nil {
			g.Explored = map[JobID]bool{}
		}
		g.Explored[id] = true
	}
}

// exploreStep is the width an explore takes queued job r of belief b to
// (exploration spec §4.2), and the memory it adds: from ExploreWidth down
// to the larger of MinExploreWidth of its widest width and its base cores
// + 1, at most limit, the first whose memory (spareMemory) adds at most
// memory; false when none does.
func exploreStep(r Record, b *Belief, p Policy, limit, memory int) (width, extra int, ok bool) {
	base := r.Claim.CPUs
	target := ExploreWidth(r, b, p.Caps.CPUs)
	if target <= base {
		return 0, 0, false
	}
	low := max(MinExploreWidth(ScalingOf(b).Widest), base+1)
	for t := min(target, limit); t >= low; t-- {
		if extra := spareMemory(r, b, t, p) - r.Claim.MemoryGB; extra <= memory {
			return t, extra, true
		}
	}
	return 0, 0, false
}
```

Replace `Grant` (doc comment and body) with:

```go
// Grant is what each job dec starts gets past its base claim (job scaling
// spec §5.3, exploration spec §4.2), a pure function of its arguments: dec
// is Admit's decision over jobs under p, belief gives a record's belief
// (nil for none), and idle is the host's idle cores. It grants nothing
// while a queued job waits for anything but the caps; agent and run jobs
// are passed over, their waits stopping nothing and their starts granted
// nothing (flow in loom parts 3a and 3b). The pool is the caps less the
// running jobs' holds (holds, as Admit counts them), the starting jobs'
// base claims and a reserve of the default claim. In dec's order, each
// job explores when p.Explore is on and idle is known: exploreStep, its
// extra cores within the pool and within the idle cores less the reserve's
// cores and less what earlier explores took. Otherwise each job SpareWidth
// can widen takes the most cores up to that width whose memory
// (spareMemory) fits what the pool has left. The pool shrinks by what each
// took. A job that gets nothing has no entry.
func Grant(dec Decision, jobs []Record, p Policy, holds map[JobID]Need, belief func(Record) *Belief, idle HostIdle) Grants {
	var g Grants
	if len(dec.Start) == 0 || !p.usable() {
		return g
	}
	agent := map[JobID]bool{}
	for _, r := range jobs {
		if Unclaimed(r.Kind) {
			agent[r.ID] = true
		}
	}
	for id, why := range dec.Waiting {
		if why != waitOver && !agent[id] {
			return g
		}
	}
	starting := make(map[JobID]Record, len(dec.Start))
	for _, id := range dec.Start {
		starting[id] = Record{}
	}
	var used Need
	for _, r := range jobs {
		if agent[r.ID] {
			continue
		}
		if _, ok := starting[r.ID]; ok {
			starting[r.ID] = r
			used = used.plus(r.Claim.Need(p.Caps))
		} else if r.State == Running {
			used = used.plus(runningNeed(r, p, holds))
		}
	}
	reserve := p.DefaultClaim.Need(p.Caps)
	cores := p.Caps.CPUs - used.CPUs - reserve.CPUs
	memory := p.Caps.MemoryGB - used.MemoryGB - reserve.MemoryGB
	// An explore's extra cores must also be idle now, past the reserve's
	// (exploration spec §3).
	explore, idleRoom := p.Explore && idle.Known, 0
	if explore {
		idleRoom = int(math.Floor(idle.Cores)) - reserve.CPUs
	}
	for _, id := range dec.Start {
		if agent[id] {
			continue
		}
		r := starting[id]
		b := belief(r)
		base := r.Claim.CPUs
		if explore {
			if t, extra, ok := exploreStep(r, b, p, base+min(cores, idleRoom), memory); ok {
				g.add(id, Claim{CPUs: t - base, MemoryGB: extra}, true)
				cores, idleRoom, memory = cores-(t-base), idleRoom-(t-base), memory-extra
				continue
			}
		}
		for t := min(SpareWidth(r, b), base+cores); t > base; t-- {
			extra := spareMemory(r, b, t, p) - r.Claim.MemoryGB
			if extra > memory {
				continue
			}
			g.add(id, Claim{CPUs: t - base, MemoryGB: extra}, false)
			cores -= t - base
			memory -= extra
			break
		}
	}
	return g
}
```

- [ ] **Step 4: Update the other callers to the new signature.**

`sched/grant_test.go`, in `TestGrant`:

```go
	grant := func(b *Belief, jobs ...Record) map[JobID]Claim {
		return Grant(Admit(jobs, p, now, nil), jobs, p, nil, func(Record) *Belief { return b }, HostIdle{}).Spare
	}
```

and its "no caps" line:

```go
	assert.Nil(t, Grant(Decision{Start: []JobID{1}}, []Record{learned(1, 4, 2)}, Policy{}, nil, func(Record) *Belief { return wide }, HostIdle{}).Spare, "no caps")
```

In `TestGrant_CountsARunningJobAtItsHold`, the two calls end `…, wide, HostIdle{}).Spare`.

`sched/agent_test.go:105` and `sched/run_test.go:75`: the closing `})` of each `Grant(dec, jobs, p, nil, func(r Record) *Belief { … })` becomes `}, HostIdle{}).Spare`.

`core/queue_admit.go:126` (the idle reading comes in Package 3):

```go
		spare = sched.Grant(granted, jobs, policy, q.holding, func(r sched.Record) *sched.Belief { return q.beliefOf(r.Signature) }, sched.HostIdle{}).Spare
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `loom job run -d "P1 grant green" -- env CGO_ENABLED=0 go test ./sched/ -count=1`
Expected: PASS (`TestGrant_Explores` needs `Policy.Explore`, so it compiles only after Task 1.4's Step 3; do 1.4 Step 3 first if the compiler says `p.Explore undefined`).

### Task 1.4: the `explore` switch

- [ ] **Step 1: Write the failing tests.** In `sched/policy_test.go`, add `Explore: true,` to `policyWant`'s literal (after `CPUOvercommit: 2,`) and to the two explicit `want` policies of `TestResolve_Explicit` (beside their `ReleasePastPeaks: true,` lines), and add:

```go
func TestResolve_Explore(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name string
		in   *bool
		want bool
	}{{"absent is on", nil, true}, {"false", &off, false}, {"true", &on, true}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(config.SchedulerSettings{Explore: tc.in}, policyFresco)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Explore)
		})
	}
}
```

In `config/settings_test.go`'s `TestSettings_CloneCopiesTheSchedulerBlock`: build `s` with `Borrow: &on, Explore: &on`, add `*c.Scheduler.Explore = false` beside the borrow flip, and assert `assert.True(t, *s.Scheduler.Explore, "the explore switch is its own (exploration spec §5)")`.

In `core/queue_holds_test.go`'s `TestJobQueue_ACapsEditKeepsTheReleaseSwitch`: add `, "explore": false` to the block's JSON, and at the end:

```go
	require.NotNil(t, got.Explore, "and explore (exploration spec §5)")
	assert.False(t, *got.Explore)
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "P1 switch red" -- env CGO_ENABLED=0 go test ./sched/ ./config/ -run 'TestResolve|TestSettings_Clone' -count=1`
Expected: FAIL to compile: `unknown field Explore in struct literal`.

- [ ] **Step 3: Implement.** `sched/job.go`, in `Policy` after `Borrow`:

```go
	// Explore lets Grant run a job wider than its claim on a quiet host to
	// learn how its command scales (exploration spec §5).
	Explore bool `json:"explore"`
```

`sched/policy.go`: in `Resolve`'s returned literal, after `Borrow: …`:

```go
		Explore:          s.Explore == nil || *s.Explore,
```

and in its doc comment, after `cpu_overcommit is\n// 2.`, the default list gains `explore is true;` (insert it after `contain is true;`).

`config/settings.go`, in `SchedulerSettings` after `Borrow`:

```go
	// Explore lets a quiet host run a job wider than its claim to learn how
	// its command scales (exploration spec §5); absent means true.
	Explore *bool `json:"explore,omitempty"`
```

and in `clone`, after `out.Borrow = clonePtr(s.Borrow)`:

```go
	out.Explore = clonePtr(s.Explore)
```

`core/queue_ops.go` (`setSchedulerCaps`): the comment's list ends `… nor borrow (memory borrowing spec §9) nor explore (exploration spec §5)`, and `s.Borrow = file.Borrow` becomes:

```go
	s.Borrow, s.Explore = file.Borrow, file.Explore
```

- [ ] **Step 4: Run the package gate**

Run: `loom job run -d "P1 gate" -- env CGO_ENABLED=0 go test ./sched/... ./config/... ./core/... -count=1`
Expected: PASS. A failure in a test that compares a whole `sched.Policy` literal means that literal needs `Explore: true`; add it.

- [ ] **Step 5: Format, vet and commit**

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')   # no output
loom job run -d "P1 vet" -- env CGO_ENABLED=0 go vet ./sched/... ./config/... ./core/...
git commit -m "feat(sched): explore a command's next width when the host is quiet

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- sched/scaling.go sched/scaling_test.go sched/grant.go sched/grant_test.go sched/agent_test.go sched/run_test.go sched/job.go sched/policy.go sched/policy_test.go config/settings.go config/settings_test.go core/queue_ops.go core/queue_holds_test.go core/queue_admit.go
```

---

## Package 2: the host's idle cores

**Files:**
- Create: `sched/hostcpu.go`, `sched/hostcpu_linux.go`, `sched/hostcpu_other.go`
- Test: `sched/hostcpu_test.go`, `sched/hostcpu_linux_test.go`

### Task 2.1: parse `/proc/stat`, the window, the readings kept

- [ ] **Step 1: Write the failing tests** in `sched/hostcpu_test.go`:

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHostStat(t *testing.T) {
	sample := "cpu  1000 50 300 8000 200 10 20 5 7 0\n" +
		"cpu0 500 25 150 4000 100 5 10 2 3 0\n" +
		"cpu1 500 25 150 4000 100 5 10 3 4 0\n" +
		"intr 12345\nctxt 999\n"
	got, err := parseHostStat([]byte(sample))
	require.NoError(t, err)
	assert.Equal(t, CPUTimes{Idle: 8200, Total: 9585, CPUs: 2}, got,
		"idle + iowait; the first 8 fields, guest left out since user already counts it")
	for name, bad := range map[string]string{
		"empty":                       "",
		"no aggregate line":           "cpu0 1 2 3 4 5 6 7 8\n",
		"too few fields":              "cpu 1 2 3 4 5 6 7\ncpu0 1 2 3 4 5 6 7\n",
		"a field that isn't a number": "cpu 1 2 x 4 5 6 7 8\ncpu0 1 2 3 4 5 6 7 8\n",
		"no cpuN line":                "cpu 1 2 3 4 5 6 7 8\nintr 1\n",
	} {
		_, err := parseHostStat([]byte(bad))
		assert.Error(t, err, name)
	}
}

func TestIdleCores(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	reading := func(s int, idle, total uint64) CPUReading {
		return CPUReading{At: t0.Add(time.Duration(s) * time.Second), Times: CPUTimes{Idle: idle, Total: total, CPUs: 16}}
	}
	// 35 s of 16 CPUs at 100 ticks a second is 56,000 ticks: 42,000 idle is 12 idle cores.
	got := IdleCores([]CPUReading{reading(0, 0, 0), reading(5, 1000, 10000), reading(40, 43000, 66000)})
	assert.True(t, got.Known)
	assert.InDelta(t, 12, got.Cores, 1e-9, "from the newest reading at least 30 s older (5 s), not the oldest")

	assert.Equal(t, HostIdle{}, IdleCores(nil), "no readings")
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(0, 0, 0)}), "one reading")
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(0, 0, 0), reading(20, 100, 1000)}), "none 30 s old")
	changed := reading(40, 43000, 66000)
	changed.Times.CPUs = 8
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(5, 1000, 10000), changed}), "the CPU count changed")
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(5, 1000, 10000), reading(40, 900, 66000)}), "idle went backwards")
	assert.Equal(t, HostIdle{}, IdleCores([]CPUReading{reading(5, 1000, 10000), reading(40, 1000, 10000)}), "no time counted")
}

func TestKeepReadings(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	var rs []CPUReading
	for s := 0; s <= 90; s += 3 {
		rs = KeepReadings(rs, CPUReading{At: t0.Add(time.Duration(s) * time.Second)})
	}
	assert.Equal(t, t0.Add(30*time.Second), rs[0].At, "2 × IdleWindow back from the newest")
	assert.Equal(t, t0.Add(90*time.Second), rs[len(rs)-1].At)
	assert.Len(t, rs, 21)
}
```

and `sched/hostcpu_linux_test.go`:

```go
package sched

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadHostCPU_ReadsThisHost(t *testing.T) {
	got, err := ReadHostCPU()
	require.NoError(t, err)
	assert.Positive(t, got.CPUs)
	assert.Positive(t, got.Total)
	assert.LessOrEqual(t, got.Idle, got.Total)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "P2 red" -- env CGO_ENABLED=0 go test ./sched/ -run 'TestParseHostStat|TestIdleCores|TestKeepReadings|TestReadHostCPU' -count=1`
Expected: FAIL to compile: `undefined: parseHostStat`.

- [ ] **Step 3: Implement** `sched/hostcpu.go`:

```go
package sched

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// IdleWindow is how far back the host's idle cores are measured
// (exploration spec §4.3).
const IdleWindow = 30 * time.Second

// CPUTimes is /proc/stat's aggregate cpu line: the host's idle and total
// CPU time in clock ticks since boot, and its logical CPUs (its cpuN
// lines).
type CPUTimes struct {
	Idle  uint64 // idle + iowait
	Total uint64 // user, nice, system, idle, iowait, irq, softirq and steal
	CPUs  int
}

// CPUReading is the host's CPU times, taken at At.
type CPUReading struct {
	At    time.Time
	Times CPUTimes
}

// errNoAggregate is a /proc/stat whose first line is not the aggregate
// cpu line with its 8 counted fields.
var errNoAggregate = errors.New("/proc/stat: no aggregate cpu line")

// parseHostStat reads data, a /proc/stat, into the host's CPU times: an
// error when its first line is not the aggregate cpu line with at least 8
// numeric fields, or no cpuN line follows. guest and guest_nice are left
// out of the total, since user and nice already count them.
func parseHostStat(data []byte) (CPUTimes, error) {
	lines := bytes.Split(data, []byte("\n"))
	fields := bytes.Fields(lines[0])
	if len(fields) < 9 || string(fields[0]) != "cpu" {
		return CPUTimes{}, errNoAggregate
	}
	var t CPUTimes
	for i, f := range fields[1:9] {
		v, err := strconv.ParseUint(string(f), 10, 64)
		if err != nil {
			return CPUTimes{}, fmt.Errorf("/proc/stat: cpu field %d: %w", i+1, err)
		}
		t.Total += v
		if i == 3 || i == 4 { // idle, iowait
			t.Idle += v
		}
	}
	for _, l := range lines[1:] {
		if f := bytes.Fields(l); len(f) > 0 && len(f[0]) > 3 && bytes.HasPrefix(f[0], []byte("cpu")) {
			t.CPUs++
		}
	}
	if t.CPUs == 0 {
		return CPUTimes{}, errors.New("/proc/stat: no cpuN line")
	}
	return t, nil
}

// IdleCores is the host's mean idle logical CPUs between the newest of
// readings (oldest first) and the newest reading at least IdleWindow older
// than it (exploration spec §4.3): the idle share of the CPU time counted
// between them, times the logical CPUs. Unknown when no reading is that
// old, the CPU count changed, a counter went backwards, or no time was
// counted. A pure function of its argument.
func IdleCores(readings []CPUReading) HostIdle {
	if len(readings) < 2 {
		return HostIdle{}
	}
	last := readings[len(readings)-1]
	for i := len(readings) - 2; i >= 0; i-- {
		first := readings[i]
		if last.At.Sub(first.At) < IdleWindow {
			continue
		}
		a, b := first.Times, last.Times
		if a.CPUs != b.CPUs || b.Idle < a.Idle || b.Total <= a.Total || b.Idle-a.Idle > b.Total-a.Total {
			return HostIdle{}
		}
		share := float64(b.Idle-a.Idle) / float64(b.Total-a.Total)
		return HostIdle{Cores: share * float64(b.CPUs), Known: true}
	}
	return HostIdle{}
}

// KeepReadings is readings (oldest first) with r added and every reading
// more than 2 × IdleWindow older than r dropped (exploration spec §4.3).
func KeepReadings(readings []CPUReading, r CPUReading) []CPUReading {
	cut := r.At.Add(-2 * IdleWindow)
	i := 0
	for i < len(readings) && readings[i].At.Before(cut) {
		i++
	}
	return append(readings[i:], r)
}
```

`sched/hostcpu_linux.go`:

```go
package sched

import (
	"fmt"
	"os"
)

// ReadHostCPU is the host's CPU times from /proc/stat (exploration spec
// §4.3).
func ReadHostCPU() (CPUTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return CPUTimes{}, fmt.Errorf("read /proc/stat: %w", err)
	}
	return parseHostStat(data)
}
```

`sched/hostcpu_other.go`:

```go
//go:build !linux

package sched

// ReadHostCPU answers ErrUnsupported: /proc/stat is Linux's.
func ReadHostCPU() (CPUTimes, error) { return CPUTimes{}, ErrUnsupported }
```

- [ ] **Step 4: Run the gate**

Run: `loom job run -d "P2 gate" -- sh -c 'CGO_ENABLED=0 go test ./sched/ -count=1 && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./...'`
Expected: PASS, both cross-builds succeed.

- [ ] **Step 5: Format and commit**

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')   # no output
git add sched/hostcpu.go sched/hostcpu_linux.go sched/hostcpu_other.go sched/hostcpu_test.go sched/hostcpu_linux_test.go
git commit -m "feat(sched): the host's idle cores from /proc/stat over 30 s

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- sched/hostcpu.go sched/hostcpu_linux.go sched/hostcpu_other.go sched/hostcpu_test.go sched/hostcpu_linux_test.go
```

---

## Package 3: the model explores

**Files:**
- Modify: `sched/job.go:202` (`Record`), `sched/belief.go:27-45,92-96` (`Run`, `RunOf`), `core/queue.go` (`queueState`), `core/queue_watch.go:378`, `core/queue_admit.go`, `core/queue_view.go:146-150,444,486-487`
- Test: `sched/belief_test.go`, `core/testmain_test.go`, `core/queue_beliefs_test.go`

### Task 3.1: `Record.Explored` and `Run.Explored`

- [ ] **Step 1: Write the failing test** in `sched/belief_test.go`:

```go
func TestRunOf_CarriesTheExploredMark(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	rec := Record{ID: 3, State: Done, Started: t0, Ended: t0.Add(time.Minute), Claim: Claim{CPUs: 4, MemoryGB: 4},
		Spare: Claim{CPUs: 2, MemoryGB: 2}, Explored: true, Exit: &Exit{Code: 0, Ended: t0.Add(time.Minute)}}
	run, ok := RunOf(rec)
	require.True(t, ok)
	assert.True(t, run.Explored)
	assert.Equal(t, 2, run.Spare)
	rec.Explored = false
	run, _ = RunOf(rec)
	assert.False(t, run.Explored, "an ordinary grant")
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `loom job run -d "P3 runof red" -- env CGO_ENABLED=0 go test ./sched/ -run TestRunOf -count=1`
Expected: FAIL to compile: `unknown field Explored in struct literal of type Record`.

- [ ] **Step 3: Implement.** `sched/job.go`, in `Record` after `Spare`:

```go
	Explored bool `json:"explored,omitempty"` // Spare is an explore's (exploration spec §4.4)
```

`sched/belief.go`, in `Run` after `Spare`:

```go
	Explored    bool      `json:"explored,omitempty"`   // its spare cores were an explore's (exploration spec §4.4)
```

and in `RunOf`'s `run := Run{…}` literal, after `Spare: rec.Spare.CPUs,`:

```go
		Explored: rec.Explored,
```

- [ ] **Step 4: Run it to see it pass** (same command). Expected: PASS.

### Task 3.2: readings on the tick, the idle cores into `Grant`, explored starts

- [ ] **Step 1: Write the failing tests.** In `core/testmain_test.go`, after the `readMemAvailable` line:

```go
	// Exploring reads the host's CPU times: tests see none, so nothing
	// explores, unless a test sets its own (wqIdle).
	readHostCPU = func() (sched.CPUTimes, error) { return sched.CPUTimes{}, sched.ErrUnsupported }
```

In `core/queue_beliefs_test.go`:

```go
// wqNarrowBelief gives sub's signature a done run at 2 cores that kept
// 0.97 busy (p reads 0): a job of it is sized 2c/2G, no ordinary grant
// widens it, and a quiet host explores it at 4.
func wqNarrowBelief(t *testing.T, m *Model, sub sched.Submit) sched.SignatureID {
	t.Helper()
	sig := wqSigOf(t, sub.Argv, sub.Cwd)
	m.queue.beliefs.Ensure(sig, time.Now()).AddRun(sched.Run{Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 2, MemoryGB: 2},
		PeakKB: 1 << 20, CPUSeconds: 97, WallSeconds: 100})
	return sig.ID()
}

// wqIdle gives the queue readings 31 s apart that show idle cores of 16
// idle, and has every later read repeat the newest.
func wqIdle(t *testing.T, m *Model, idle float64) {
	t.Helper()
	const total = 30 * 100 * 16 // 30 s of 16 CPUs at 100 ticks a second
	newest := sched.CPUTimes{Idle: uint64(idle * 30 * 100), Total: total, CPUs: 16}
	prev := readHostCPU
	readHostCPU = func() (sched.CPUTimes, error) { return newest, nil }
	t.Cleanup(func() { readHostCPU = prev })
	now := time.Now()
	m.queue.hostCPU = []sched.CPUReading{{At: now.Add(-31 * time.Second), Times: sched.CPUTimes{CPUs: 16}}, {At: now, Times: newest}}
}

func TestJobQueue_AQuietHostExploresAJobAtOneWidth(t *testing.T) {
	m := wqLiveModel(t)
	wqIdle(t, m, 12)
	sub := wqJob(t, "sleep", "1")
	sig := wqNarrowBelief(t, m, sub)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wqPump(t, m)

	rec := wqRecord(t, m, id)
	require.Equal(t, sched.Running, rec.State)
	assert.True(t, rec.Explored)
	// Caps of 14 cores and 24 GB, the reserve's 4c/6G and 12 idle cores:
	// 2 → 4, memory in proportion.
	assert.Equal(t, sched.Claim{CPUs: 2, MemoryGB: 2}, rec.Spare)
	assert.Equal(t, sched.Claim{CPUs: 4, MemoryGB: 4}, rec.Claim)
	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.Equal(t, 4, spec.HintCPUs, "the hint variables carry the explored width")

	wqSettle(t, m, id)
	b := m.queue.beliefs.Find(sig)
	require.NotNil(t, b)
	last := b.Runs[len(b.Runs)-1]
	assert.True(t, last.Explored, "the run records the explore")
	assert.Equal(t, 4, last.Claim.CPUs, "at its explored width")
}

func TestJobQueue_NoExploreWhileAnotherJobWaits(t *testing.T) {
	m := wqLiveModel(t)
	wqIdle(t, m, 12)
	blocker := wqJob(t, "sleep", "1")
	blocker.Claim = sched.Claim{CPUs: 14, MemoryGB: 1}
	bid, _, err := m.submitJob(blocker)
	require.NoError(t, err)
	require.Equal(t, sched.Running, wqRecord(t, m, bid).State)

	sub := wqJob(t, "sleep", "30")
	wqNarrowBelief(t, m, sub)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wide := wqJob(t, "sleep", "1")
	wide.Claim = sched.Claim{CPUs: 13, MemoryGB: 1}
	wid, _, err := m.submitJob(wide)
	require.NoError(t, err)

	wqWait(t, "the blocker to end and the next job to start", func() bool {
		if wqRecord(t, m, id).State == sched.Queued {
			wqPass(t, m)
		}
		return wqRecord(t, m, id).State != sched.Queued
	})
	rec := wqRecord(t, m, id)
	assert.False(t, rec.Explored, "job %d waits for 13 cores, so nothing explores", wid)
	assert.Zero(t, rec.Spare)
	assert.Equal(t, sched.Queued, wqRecord(t, m, wid).State)
}

func TestJobQueue_TheTickReadsTheHostsCPU(t *testing.T) {
	m := wqModel(t)
	calls := 0
	prev := readHostCPU
	readHostCPU = func() (sched.CPUTimes, error) {
		calls++
		return sched.CPUTimes{Idle: uint64(calls), Total: uint64(10 * calls), CPUs: 16}, nil
	}
	t.Cleanup(func() { readHostCPU = prev })
	m.tickQueue()
	require.Len(t, m.queue.hostCPU, 1, "the tick takes a reading, with or without jobs")
	first := m.queue.hostCPU[0].At
	m.sampleHostCPU(first.Add(time.Second))
	assert.Len(t, m.queue.hostCPU, 1, "at most once a queue interval")
	m.sampleHostCPU(first.Add(queueInterval))
	assert.Len(t, m.queue.hostCPU, 2)
	readHostCPU = func() (sched.CPUTimes, error) { return sched.CPUTimes{}, errors.New("no /proc") }
	m.sampleHostCPU(first.Add(2 * queueInterval))
	assert.Empty(t, m.queue.hostCPU, "a failed read drops the readings: the idle cores are unknown")
}
```

Add `"errors"` to `core/queue_beliefs_test.go`'s imports if it lacks it. In the existing `TestJobQueue_AnIdleHostLendsSpareCoresToAJobThatScales`, after the `rec.Claim` assertion, add:

```go
	assert.False(t, rec.Explored, "no idle reading: an ordinary grant")
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "P3 core red" -- env CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_AQuietHost|TestJobQueue_NoExplore|TestJobQueue_TheTickReads|TestJobQueue_AnIdleHostLends' -count=1`
Expected: FAIL to compile: `undefined: readHostCPU`, `m.queue.hostCPU undefined`.

- [ ] **Step 3: Implement.** `core/queue_watch.go`, beside `var readMemAvailable = sched.ReadMemAvailableKB`:

```go
// readHostCPU reads the host's CPU times for the idle cores exploring
// needs (exploration spec §4.3); tests replace it.
var readHostCPU = sched.ReadHostCPU
```

`core/queue.go`, at the end of `queueState` (after `noLend`):

```go
	// Exploring (spec 2026-11-08 §4.3): the host's CPU readings of the last
	// 2 × IdleWindow, oldest first, taken on the tick, and whether the last
	// read failed, so a run of failures is logged once. In memory only.
	hostCPU        []sched.CPUReading
	hostCPUFailing bool
```

`core/queue_admit.go`: in `tickQueue`, after the `if !q.ready() { return }` block:

```go
	m.sampleHostCPU(time.Now())
```

and add after `tickQueue`:

```go
// sampleHostCPU adds a reading of the host's CPU times to q.hostCPU, at
// most once a queueInterval (exploration spec §4.3). A failed read drops
// every reading, so the idle cores read unknown until the window fills
// again; a failure other than an unsupported host is logged once.
func (m *Model) sampleHostCPU(now time.Time) {
	q := m.queue
	if n := len(q.hostCPU); n > 0 && now.Sub(q.hostCPU[n-1].At) < queueInterval {
		return
	}
	t, err := readHostCPU()
	if err != nil {
		if !q.hostCPUFailing && !errors.Is(err, sched.ErrUnsupported) {
			log.For("core").Warn("queue.host_cpu_failed", "err", err)
		}
		q.hostCPU, q.hostCPUFailing = nil, true
		return
	}
	q.hostCPUFailing = false
	q.hostCPU = sched.KeepReadings(q.hostCPU, sched.CPUReading{At: now, Times: t})
}
```

In `admitJobs`, replace `var spare map[sched.JobID]sched.Claim` with:

```go
	var grants sched.Grants
	var idle sched.HostIdle
```

replace the `spare = sched.Grant(…)` line (and its comment's first line keeps "Spare cores for the jobs it starts") with:

```go
		idle = sched.IdleCores(q.hostCPU)
		grants = sched.Grant(granted, jobs, policy, q.holding, func(r sched.Record) *sched.Belief { return q.beliefOf(r.Signature) }, idle)
```

and the firm-start loop with:

```go
	for _, id := range start {
		if i := q.index(id); i >= 0 && q.store.Jobs[i].State == sched.Queued {
			if grants.Explored[id] {
				rec := q.store.Jobs[i]
				log.For("core").Info("queue.explored", "job", int64(id), "signature", string(rec.Signature),
					"from", rec.Claim.CPUs, "to", rec.Claim.CPUs+grants.Spare[id].CPUs, "idle_cores", idle.Cores)
			}
			m.startJob(i, policy, now, grants.Spare[id], grants.Explored[id], id == starved)
			started = true
		}
	}
```

The borrower loop's call becomes `m.startJob(i, policy, now, sched.Claim{}, false, id == starved)`.

`startJob`: its signature becomes

```go
func (m *Model) startJob(i int, policy sched.Policy, now time.Time, spare sched.Claim, explored, starved bool) {
```

its doc comment's first sentence ends "… with spare added to its claim (job scaling spec §5.3; zero for none), marked an explore when explored (exploration spec §4.4)", and after the `if spare != (sched.Claim{}) { … }` block:

```go
	rec.Explored = explored
```

`core/queue_view.go`: in `JobView`, after `Ideal int`:

```go
	// Exploration (spec 2026-11-08 §6): whether a started job's spare
	// cores are an explore, and the width a quiet host could explore a
	// queued job at (0 when none could, or exploring is off).
	Explored bool
	Explore  int
```

in `queueJobView`'s literal, after `Spare: r.Spare,`:

```go
		Explored:     r.Explored,
```

and in its `case sched.Queued:`, after `jv.Ideal = sched.SpareWidth(r, b)`:

```go
		if q.policy.Explore {
			jv.Explore = sched.ExploreWidth(r, b, q.policy.Caps.CPUs)
		}
```

- [ ] **Step 4: Run the package gate**

Run: `loom job run -d "P3 gate" -- env CGO_ENABLED=0 go test ./sched/... ./core/... ./internal/cli/... ./ui/... ./app/... -count=1`
Expected: PASS.

- [ ] **Step 5: Format, vet and commit**

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')   # no output
loom job run -d "P3 vet" -- env CGO_ENABLED=0 go vet ./...
git commit -m "feat(core): explore on a quiet host from the tick's idle cores

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- sched/job.go sched/belief.go sched/belief_test.go core/queue.go core/queue_watch.go core/queue_admit.go core/queue_view.go core/testmain_test.go core/queue_beliefs_test.go
```

---

## Package 4: what people see, and the docs

**Files:**
- Modify: `internal/cli/job_list.go:69-77`, `internal/cli/job_sizing.go:73-76`, `ui/work.go:566-585`, `sched/beliefs_view.go:25-66`, `sched/belief_text.go:110-150`, `USAGE.md`, `sched/README.md`, `docs/claude/the-work-queue.md`, `sched/CLAUDE.md`, `docs/superpowers/specs/2026-11-08-job-exploration-design.md`
- Test: `internal/cli/job_sizing_test.go`, `internal/cli/job_test.go`, `ui/work_test.go`, `sched/beliefs_view_test.go`

### Task 4.1: the CLI's list and sized line, and the work view

- [ ] **Step 1: Write the failing tests.** In `internal/cli/job_sizing_test.go`'s `TestSizingLine` cases, add:

```go
		{core.JobView{Claim: sched.Claim{CPUs: 2, MemoryGB: 2}, Sources: srcs(L, L, D), Learned: 1, Explore: 4},
			"sized 2c/2G (cores and memory learned from 1 run, I/O default); explores 4 cores when idle"},
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 2}, Sources: srcs(L, L, D), Learned: 1, Ideal: 8, Explore: 6},
			"sized 4c/2G (cores and memory learned from 1 run, I/O default); up to 8 cores when idle"},
```

In `internal/cli/job_test.go` (where the other `writeJobList` tests live):

```go
func TestJobList_SaysWhichJobsExplore(t *testing.T) {
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	q := core.QueueView{Jobs: []core.JobView{{ID: 7, State: sched.Running, Description: "nextest", Started: now.Add(-time.Minute),
		Claim: sched.Claim{CPUs: 4, MemoryGB: 4}, Spare: sched.Claim{CPUs: 2, MemoryGB: 2}, Explored: true}}}
	var out strings.Builder
	require.NoError(t, writeJobList(&out, q, false, now))
	assert.Contains(t, out.String(), "exploring 4 cores (was 2)")
}
```

(add any import the file lacks.) In `ui/work_test.go`:

```go
func TestRenderWork_ShowsExplorers(t *testing.T) {
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	q := core.QueueView{
		Host:   sched.Host{Name: "fresco"},
		Policy: sched.Policy{Caps: sched.Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}, CPUOvercommit: 2},
		Jobs: []core.JobView{{ID: 4, State: sched.Running, Description: "nextest", Started: now.Add(-time.Minute),
			Claim: sched.Claim{CPUs: 4, MemoryGB: 4}, Spare: sched.Claim{CPUs: 2, MemoryGB: 2}, Explored: true}},
	}
	out := ansi.Strip(RenderWork(160, 30, WorkData{Queue: q, Now: now}))
	assert.Contains(t, out, "exploring 4 cores (was 2)")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "P4 surfaces red" -- env CGO_ENABLED=0 go test ./internal/cli/ ./ui/ -run 'TestSizingLine|TestJobList_SaysWhichJobsExplore|TestRenderWork_ShowsExplorers' -count=1`
Expected: FAIL: the new strings are missing.

- [ ] **Step 3: Implement.** `internal/cli/job_sizing.go`, replace the `if v.Ideal > 0 { … }` block in `sizingLine` with:

```go
	switch {
	case v.Explore > v.Ideal:
		line += fmt.Sprintf("; explores %d cores when idle", v.Explore)
	case v.Ideal > 0:
		line += fmt.Sprintf("; up to %d cores when idle", v.Ideal)
	}
```

`internal/cli/job_list.go`, in `writeJobList`'s row loop, after `if j.State.Waiting() { waits = j.Waiting }`:

```go
		if j.State == sched.Running && j.Explored {
			waits = exploringText(j)
		}
```

and add to the file:

```go
// exploringText is a running explorer's note (exploration spec §6):
// "exploring 4 cores (was 2)".
func exploringText(j core.JobView) string {
	return fmt.Sprintf("exploring %d cores (was %d)", j.Claim.CPUs, j.Claim.CPUs-j.Spare.CPUs)
}
```

`ui/work.go`, in `workRowFacts`' `case sched.Running:`, after the borrower `switch`:

```go
		if j.Explored {
			extra += fmt.Sprintf("  exploring %d cores (was %d)", j.Claim.CPUs, j.Claim.CPUs-j.Spare.CPUs)
		}
```

- [ ] **Step 4: Run them to see them pass** (same command). Expected: PASS.

### Task 4.2: the belief text's explore clause

- [ ] **Step 1: Write the failing tests** at the end of `TestFormatBelief_Scaling` (`sched/beliefs_view_test.go`):

```go
	explore := szPolicy
	explore.Explore = true
	exploring := func(b *Belief) string {
		for _, l := range FormatBelief(ViewBelief(*b, nil, explore)) {
			if strings.HasPrefix(l, "scaling") {
				return l
			}
		}
		return ""
	}
	assert.Equal(t, "scaling   unknown until a run with 2 cores or more · next quiet run explores 2 cores", exploring(belief(at(1, 1.0))))
	assert.Equal(t, "scaling   0% parallel over 1 run · efficient up to 1 cores · ~2m at 2 · next quiet run explores 4 cores",
		exploring(belief(at(2, 0.97))))
	assert.Equal(t, "scaling   0% parallel from runs at 2 and 4 cores · efficient up to 1 cores · ~2m at 2 · no gain expected past 4 cores, checked again once its 4-core runs leave the window",
		exploring(belief(at(2, 1.0), at(4, 1.0))))
	assert.Equal(t, "scaling   ignores the hint variables · keeps 7.8 cores busy, claims 10", exploring(belief(at(4, 7.81))), "an overrunner explores nothing")
```

(`at` runs last 100 s, which `RoughDuration` prints as `2m`; the busy rule gives both beliefs a base of 2 cores, and a fit of 0 equals the busy p, so the second line names its widths.)

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "P4 belief red" -- env CGO_ENABLED=0 go test ./sched/ -run TestFormatBelief_Scaling -count=1`
Expected: FAIL: no explore clause.

- [ ] **Step 3: Implement.** `sched/beliefs_view.go`, in `BeliefView` after `Scaling`:

```go
	// Explore is the width a quiet host would explore its command at
	// (ExploreTarget), and ExploreFlat says its two widest widths show no
	// gain to look for below the cap (exploration spec §6); both zero with
	// exploring off.
	Explore     int
	ExploreFlat bool
```

in `ViewBelief`, inside `if p.usable() { … }` after `v.Sized = …`:

```go
		if p.Explore {
			v.Explore = ExploreTarget(&b, p.Caps.CPUs)
			s := v.Scaling
			v.ExploreFlat = v.Explore == 0 && !s.Overruns && len(s.Widths) >= 2 && s.Widest < p.Caps.CPUs
		}
```

`sched/belief_text.go`: `scalingLine`'s `case !s.Known:` returns `"scaling   unknown until a run with 2 cores or more" + exploreClause(v)`, and each of its last three returns appends `+ exploreClause(v)`:

```go
	if v.DurationRuns == 0 || base < 1 {
		return line + exploreClause(v)
	}
	…
	if s.Ideal > base {
		return line + " · " + at(base) + ", " + at(s.Ideal) + exploreClause(v)
	}
	return line + " · " + at(base) + exploreClause(v)
```

and add:

```go
// exploreClause is the scaling line's note on exploring (exploration spec
// §6): the width the next quiet run explores, or that the two widest
// widths show no gain past the widest; "" for neither.
func exploreClause(v BeliefView) string {
	switch {
	case v.Explore > 0:
		return fmt.Sprintf(" · next quiet run explores %d cores", v.Explore)
	case v.ExploreFlat:
		w := v.Scaling.Widest
		return fmt.Sprintf(" · no gain expected past %d cores, checked again once its %d-core runs leave the window", w, w)
	}
	return ""
}
```

Update `scalingLine`'s doc comment to end "…; or that it is unknown; then exploring's clause (exploreClause)".

- [ ] **Step 4: Run it to see it pass** (same command). Expected: PASS.

### Task 4.3: docs

- [ ] **Step 1: `USAGE.md`.** In the work-queue's **Spare cores** bullet (line ~951), after the sentence ending "with memory in proportion; …", add: "A command whose runs have only one width, or whose two widest widths still gain, *explores*: when no job waits and the host's idle cores (over the last 30 s, outside load included) cover the extra cores and the reserve, it starts at twice its widest width, or at least 1.5× when the room is less, so the next sizing has a second width to compare; `submit` says `explores 4 cores when idle`, `loom job list` and the work view `exploring 4 cores (was 2)`, and `loom job beliefs show` says what the next quiet run explores. Turn it off with `explore: false` in the global `~/.loom/config.json`." Add `"explore": true` to the example block near line 1472 and a table row after `borrow`'s:

```markdown
| `explore` | `true` | `true` or `false`: on a quiet host, run a job at a wider width than its claim to learn how its command scales ([Spare cores](#the-work-queue)) |
```

- [ ] **Step 2: `sched/README.md`.** Add `hostcpu.go` (`IdleCores`, `KeepReadings`, `ReadHostCPU`) to the file list beside `grant.go`, change `grant.go`'s entry to "`SpareWidth`, `ExploreWidth`, `Grant`", and extend **The grant** paragraph with: "With `Policy.Explore` on and the host's idle cores known (`HostIdle`, from `IdleCores` over 30 s of `/proc/stat`), a job `ExploreWidth` names explores first: `ExploreTarget` is twice its widest width (held to the cap) while it has one width or its two widest predict a 1.25× gain from doubling, and `exploreStep` takes the widest width down to `MinExploreWidth` (1.5×) that fits the pool, the idle cores less the reserve's and the memory; the grant is marked in `Grants.Explored`, `Record.Explored` and `Run.Explored`, and learning reads it as any grant."

- [ ] **Step 3: `docs/claude/the-work-queue.md`.** Append rule 18:

```markdown
18. **Explore only on a known idle reading, from the tick's readings alone** ([spec](../superpowers/specs/2026-11-08-job-exploration-design.md)). `tickQueue` calls `sampleHostCPU` (at most every `queueInterval`, whether or not jobs wait) and `admitJobs` hands `sched.IdleCores(q.hostCPU)` to `sched.Grant`; a failed read clears the readings, so nothing explores until 30 s of new ones exist. `startJob` sets `Record.Explored` on every start (false for an ordinary grant or none), and a requeue's rebuilt record drops it. Core tests see no reading (`TestMain` sets `readHostCPU` to `ErrUnsupported`); `wqIdle` gives one. *Silent:* a guessed or stale idle figure explores into load the queue can't see, and its contended run misleads the fit it exists to feed.
```

- [ ] **Step 4: `sched/CLAUDE.md`.** In the rule "**Keep `Size`, `Usual`, `OverdueAfter`, `RunOf`, `ScalingOf` and `Grant` pure…**", make the list `Size`, `Usual`, `OverdueAfter`, `RunOf`, `ScalingOf`, `ExploreTarget` and `Grant`. Check the budget: `loom job run -d "claudemd budget" -- go run ./tools/claudemd -v` must show `sched/CLAUDE.md` within 20,000 bytes.

- [ ] **Step 5: the spec.** In §6's **Log** bullet, drop `` and `room` `` (the log carries `job`, `signature`, `from`, `to` and `idle_cores`: the room is worked out inside `Grant`, which returns only what it lent), and add at the end of the spec:

```markdown
## 12. Execution notes

- The `queue.explored` log has no `room` field (§6): `Grant` returns what it lent, not the room it worked from.
```

- [ ] **Step 6: Final gates**

Run, each through the queue:
- `loom job run -d "exploration full" -- env CGO_ENABLED=0 go test ./... -count=1`
- `loom job run -d "exploration race" -- env CC=clang CGO_ENABLED=1 go test -race ./sched/... ./core/... ./internal/cli/... -count=1`
- `loom job run -d "exploration vet+cross" -- sh -c 'CGO_ENABLED=0 go vet ./... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./...'`
- `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')` (no output)

Expected: all PASS. golangci-lint is skipped locally (CI pins v1; say so in the report).

- [ ] **Step 7: Commit**

```bash
git commit -m "feat(sched,core,cli,ui): show explores, and document them

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- internal/cli/job_list.go internal/cli/job_test.go internal/cli/job_sizing.go internal/cli/job_sizing_test.go ui/work.go ui/work_test.go sched/beliefs_view.go sched/belief_text.go sched/beliefs_view_test.go USAGE.md sched/README.md docs/claude/the-work-queue.md sched/CLAUDE.md docs/superpowers/specs/2026-11-08-job-exploration-design.md
```

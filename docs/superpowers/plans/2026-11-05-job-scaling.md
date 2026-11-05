# Job scaling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The scheduler learns how each command scales with cores: on an idle host it lends spare cores to jobs that scale, it counts commands that ignore the hint variables at the cores they take, and it predicts durations at the cores a job gets.

**Architecture:** Pure `sched` code: `ScalingOf` reads a belief's done runs for Amdahl's parallel fraction, an ideal width and overruns; `Size` counts overrunners past the default claim; `Grant` lends spare cores after `Admit`; `Usual` scales wall times between core counts. `core` calls `Grant` in its admission pass and adds the grant to the record's claim before the hint is written. The CLI, the belief text and the work view show it.

**Tech Stack:** Go (module `github.com/aidan-bailey/loom`), testify, cobra, Bubble Tea v2 / lipgloss v2.

**Spec:** [`docs/superpowers/specs/2026-11-05-job-scaling-design.md`](../specs/2026-11-05-job-scaling-design.md). Execution: inline (superpowers:executing-plans), no checkpoint pauses; one commit per package.

---

## Ground rules for every package

- Scratch files go in the session scratchpad, never the worktree. Commit by path: `git add <paths>`, then `git commit -m … -- <paths>`; never `-a`, `git add -A` or `git add .`.
- Quick checks run directly: `CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 go vet ./<pkg>/...`, and one package's tests (`CGO_ENABLED=0 go test ./sched/ -run TestX`). The full suite and the race run go through loom's work queue, with Bash's `run_in_background`:
  - `loom job run -d "job scaling: full suite" -- env CGO_ENABLED=0 go test ./...`
  - `loom job run -d "job scaling: race" -- env CC=clang CGO_ENABLED=1 go test -race ./...`
- Format with `gofmt -w` on the files touched (never `gofmt -w .`), and check `gofmt -l sched core internal ui`.
- CLAUDE.md budgets: `go run ./tools/claudemd -v`. `core/CLAUDE.md` is full (19971/20000 bytes), so the model's rules go in `docs/claude/the-work-queue.md`; `sched/CLAUDE.md` has room (11612/20000).
- golangci-lint: the dev shell's v2 rejects the config; run `CGO_ENABLED=0 go vet ./...` and say lint was skipped.

## Spec amendments made while planning

Recorded in the spec in Step 1.1:

1. **A spare run's peak is a scaled floor only beside a full peak without spare cores** (§5.2). With floors alone, a command whose recent runs all had spare cores (an idle host, run after run) would lose its learned memory, fall back to the default claim and ask the advisor for a prior. So a run with spare cores counts as a floor of its peak × base ÷ its cores while the last 10 runs include a full peak of a run without spare cores; with none, its peak counts as it is, which is safe since fewer cores need no more memory.
2. **The forecast doesn't run `Grant`** (§5.4, §10, §12). A grant happens only when no queued job is left waiting except over the caps, and the forecast leaves out jobs being sized, so a simulated grant could never move a forecast start. The forecast's durations still come at each job's cores: a running job's total, a queued job's base.
3. **`JobView.Ideal`** is added (§9): the width a grant could widen a queued job to (`sched.SpareWidth`), 0 when none could. `submit`'s and `run`'s sized line read it.
4. **The scaling line uses the belief text's label column** (§6): `scaling   95% parallel over 1 run · …`. An overrunner's line ends `, claims N` only when there is a claim (admission isn't paused).

## Files

| File | Package | Responsibility |
|---|---|---|
| `sched/scaling.go` (new) | 1 | `Scaling`, `ScalingOf`, `runParallel`, `idealWidth`, `Scaling.factor`, `Scaling.scale` |
| `sched/scaling_test.go` (new) | 1 | `TestScalingOf`, `TestScaling_Scale`, the `at` run helper |
| `sched/grant.go` (new) | 1 | `SpareWidth`, `Grant`, `spareMemory` |
| `sched/grant_test.go` (new) | 1 | `TestGrant` |
| `sched/belief.go` | 1 | `Run.Spare`; `RunOf` copies it |
| `sched/job.go` | 1 | `Record.Spare`; `Claim.Spared` |
| `sched/size.go` | 1 | overrunners' cores; `largestPeak` takes the claim's cores |
| `sched/surprise.go` | 1 | `NeedsPrior`'s `largestPeak` call; the overrun surprise; the scaled duration check |
| `sched/durations.go` | 1 | `Usual(b, cores)`, `OverdueAfter(b, cores)` |
| `sched/beliefs_view.go` | 1 | `BeliefView.Scaling`; durations at the sized claim's cores |
| `sched/advisor/prompt.md` | 1 | the reconcile line on overruns |
| `core/queue_admit.go` | 1 | `Grant` after `Admit`; `startJob` applies the grant |
| `core/queue_view.go` | 1 | `JobView.Spare`, `JobView.Ideal`; durations at a job's cores |
| `core/queue_advisor.go` | 1 | `Usual(…, 0)` |
| `core/queue_beliefs_test.go` | 1 | the grant through the model |
| `docs/specs/protocol.md` | 1 | regenerated |
| `sched/belief_text.go` | 2 | the scaling line |
| `internal/cli/job_sizing.go`, `job_follow.go`, `job_beliefs.go` | 2 | the claim text, the sized and start lines, SCALES |
| `ui/work.go` | 2 | the claim cell and its width |
| `session/loom_context_shared.md` | 2 | the sizing sentence |
| `USAGE.md`, `sched/README.md`, `sched/CLAUDE.md`, `docs/claude/the-work-queue.md` | 1, 2 | docs |

## Package 1: The model

### Step 1.1: Amend the spec

- [ ] **Edit `docs/superpowers/specs/2026-11-05-job-scaling-design.md`:**
  - §5.2, the second bullet becomes: "**A run that had spare cores** teaches a base claim's memory as a floor, its peak scaled by the claim's cores over the run's, while the last 10 runs include a full peak of a run without spare cores: a 10-core run that peaked at 9 GB raises a 4-core claim to at least 5 GB (3.6 GB with the margin) and never sets it alone. With no such full peak, its peak counts as it is, since fewer cores need no more memory (amended while planning: with floors alone, a command whose recent runs all had spare cores would lose its learned memory and ask the advisor for a prior). Its cores and I/O teach as any done run's do."
  - §5.4, the second bullet becomes: "The view asks at a queued job's base cores and at a running job's total, and so does the forecast. The forecast runs no grant (amended while planning): a grant happens only when no queued job is left waiting except over the caps, and the forecast leaves out jobs being sized, so a simulated grant could never move a forecast start. The host's fallback duration and the advisor's request ask at 0."
  - §6, the `beliefs show` bullet: the examples become `scaling   95% parallel over 1 run · efficient up to 6 cores · ~34s at 4, ~24s at 6`, `scaling   ignores the hint variables · keeps 7.8 cores busy, claims 10` (", claims N" only when there is a claim) and `scaling   unknown until a run with 2 cores or more`.
  - §9 adds: "`JobView.Ideal`, the width a grant could widen a queued job to (`sched.SpareWidth`), 0 when none could, for the sized line (added while planning)."
  - §10: `sched/forecast.go` leaves the table; `sched/grant.go`'s row reads "`SpareWidth`, `Grant`, and a job's memory at t cores".
  - §12: drop "`TestForecast` (a grant)".

### Step 1.2: `Scaling` and `ScalingOf`

**Files:** Create `sched/scaling.go`, `sched/scaling_test.go`.

- [ ] **Write the test** (`sched/scaling_test.go`):

```go
package sched

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// at is a done run at cores that kept busy cores busy for 100 s and peaked
// at 1 GB.
func at(cores int, busy float64) Run {
	r := done(1<<20, busy*100, 100)
	r.Claim = Claim{CPUs: cores, MemoryGB: 2}
	return r
}

func TestScalingOf(t *testing.T) {
	assert.Equal(t, Scaling{}, ScalingOf(nil), "no belief")
	assert.Equal(t, Scaling{}, ScalingOf(belief(done(1<<20, 10, 10))), "a run whose claim had no cores (exclusive) teaches nothing")

	one := ScalingOf(belief(at(1, 1.0)))
	assert.False(t, one.Known, "a 1-core run can't show scaling")
	assert.Equal(t, 1.0, one.Busy)

	race := ScalingOf(belief(at(4, 2.17)))
	assert.True(t, race.Known)
	assert.InDelta(t, 0.719, race.Parallel, 0.001, "(1 − 1/2.17) ÷ (1 − 1/4)")
	assert.Equal(t, 1, race.Runs)
	assert.Equal(t, 4, race.Ideal, "efficiency 0.54 at 4 cores, 0.47 at 5")

	wasm := ScalingOf(belief(at(3, 2.74)))
	assert.InDelta(t, 0.952, wasm.Parallel, 0.001)
	assert.Equal(t, 6, wasm.Ideal, "22 at 50%, held to twice the 3 cores it had")

	nextest := ScalingOf(belief(at(2, 1.68), at(4, 1.18)))
	assert.InDelta(t, 0.507, nextest.Parallel, 0.001, "the median of 0.81 and 0.20")
	assert.Equal(t, 2, nextest.Runs)
	assert.Equal(t, 3, nextest.Ideal)

	e2e := ScalingOf(belief(at(3, 0.48)))
	assert.Zero(t, e2e.Parallel, "never busier than 1 core: held to 0")
	assert.Equal(t, 1, e2e.Ideal)

	full := ScalingOf(belief(at(4, 4.0)))
	assert.Equal(t, 1.0, full.Parallel, "a saturated run")
	assert.Equal(t, 8, full.Ideal, "p of 1: the bound alone")

	vitest := ScalingOf(belief(at(4, 7.81)))
	assert.True(t, vitest.Overruns)
	assert.InDelta(t, 7.81, vitest.Busy, 0.001)
	assert.False(t, vitest.Known, "an overrunner's p means nothing")

	assert.False(t, ScalingOf(belief(at(4, 5.0))).Overruns, "1.25 × its cores is not over")
	assert.True(t, ScalingOf(belief(at(4, 7.81), at(4, 7.0), at(4, 2.0))).Overruns, "by the median")

	failed := at(4, 7.81)
	failed.Outcome = OutcomeFailed
	assert.Equal(t, Scaling{}, ScalingOf(belief(failed)), "only done runs teach")
}

func TestScaling_Scale(t *testing.T) {
	s := ScalingOf(belief(at(3, 2.74))) // p ≈ 0.953
	assert.InDelta(t, 33.6, s.scale(43, 3, 4), 0.1)
	assert.InDelta(t, 24.3, s.scale(43, 3, 6), 0.1)
	assert.Equal(t, 43.0, s.scale(43, 0, 4), "a run with no cores")
	assert.Equal(t, 43.0, s.scale(43, 3, 0), "no cores asked")
	assert.Equal(t, 43.0, Scaling{}.scale(43, 3, 4), "p unknown")
	assert.Equal(t, 43.0, Scaling{Overruns: true, Known: true, Parallel: 1}.scale(43, 3, 4), "an overrunner")
}
```

- [ ] **Run it:** `CGO_ENABLED=0 go test ./sched/ -run 'TestScaling'`. Expected: FAIL to compile (`undefined: ScalingOf`).

- [ ] **Write `sched/scaling.go`:**

```go
package sched

// The constants of scaling (job scaling spec §5.1).
const (
	overrunRatio  = 1.25 // busy ÷ cores past which a command overruns
	minEfficiency = 0.5  // the efficiency the ideal width stays above
	growthBound   = 2    // the ideal width is at most this × the most cores a run had
)

// Scaling is how a belief's command scales with cores (job scaling spec
// §5.1). While Known is false (no run with 2 cores or more measured it),
// Parallel, Runs and Ideal hold nothing.
type Scaling struct {
	Overruns bool    `json:"overruns,omitempty"` // its runs keep more than overrunRatio × their cores busy
	Busy     float64 `json:"busy,omitempty"`     // the median busy cores of its runs
	Known    bool    `json:"known,omitempty"`
	Parallel float64 `json:"parallel,omitempty"` // p: the share of its work that runs in parallel, 0–1
	Runs     int     `json:"runs,omitempty"`     // the runs Parallel came from
	Ideal    int     `json:"ideal,omitempty"`    // the most cores at which its efficiency stays above minEfficiency
}

// ScalingOf is how b's command scales (job scaling spec §5.1), a pure
// function of b (nil for none). It reads the done runs with CPU time among
// b's last window runs, leaving out a run whose claim had no cores (an
// exclusive one). The command overruns when the median of their busy
// cores ÷ cores is above overrunRatio. Otherwise each of them with 2 cores
// or more gives Amdahl's parallel fraction, (1 − 1/busy) ÷ (1 − 1/cores)
// held to 0–1, and Parallel is their median; Ideal is the most cores at
// which the efficiency 1 ÷ (c(1 − p) + p) stays above minEfficiency, at
// least 1 and at most growthBound × the most cores any of these runs had.
func ScalingOf(b *Belief) Scaling {
	var ratios, busies, ps []float64
	most := 0
	for _, r := range b.recent() {
		c := r.Claim.CPUs
		if !r.teachesCores() || c < 1 {
			continue
		}
		busy := r.CPUSeconds / r.WallSeconds
		ratios = append(ratios, busy/float64(c))
		busies = append(busies, busy)
		most = max(most, c)
		if c >= 2 {
			ps = append(ps, runParallel(busy, c))
		}
	}
	var s Scaling
	s.Busy, _ = median(busies)
	if ratio, ok := median(ratios); ok && ratio > overrunRatio {
		s.Overruns = true
		return s
	}
	if p, ok := median(ps); ok {
		s.Known, s.Parallel, s.Runs = true, p, len(ps)
		s.Ideal = idealWidth(p, growthBound*most)
	}
	return s
}

// runParallel is the parallel fraction of a run at cores (2 or more) that
// kept busy cores busy, held to 0–1.
func runParallel(busy float64, cores int) float64 {
	p := (1 - 1/busy) / (1 - 1/float64(cores))
	return min(max(p, 0), 1)
}

// idealWidth is the most cores, from 1 to bound, at which a job of
// parallel fraction p keeps its efficiency above minEfficiency: the last c
// with c(1 − p) + p below 1 ÷ minEfficiency.
func idealWidth(p float64, bound int) int {
	ideal := 1
	for c := 2; c <= bound; c++ {
		if float64(c)*(1-p)+p >= 1/minEfficiency {
			break
		}
		ideal = c
	}
	return ideal
}

// factor is the speed factor at c cores, (1 − p) + p/c: a job's wall time
// at c cores is in proportion to it.
func (s Scaling) factor(c int) float64 { return 1 - s.Parallel + s.Parallel/float64(c) }

// scale is a wall time measured at from cores, at to cores (job scaling
// spec §5.4): wall × factor(to) ÷ factor(from), or wall as it is when
// either count is 0, Parallel is unknown or the command overruns.
func (s Scaling) scale(wall float64, from, to int) float64 {
	if from < 1 || to < 1 || !s.Known || s.Overruns {
		return wall
	}
	return wall * s.factor(to) / s.factor(from)
}
```

- [ ] **Run it:** `CGO_ENABLED=0 go test ./sched/ -run 'TestScaling'`. Expected: PASS.

### Step 1.3: The spare fields and the claim text

**Files:** Modify `sched/belief.go`, `sched/job.go`; tests in `sched/belief_test.go`, `sched/job_test.go`.

- [ ] **Write the tests.** Append to `sched/belief_test.go`:

```go
func TestRunOf_KeepsItsSpareCores(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	rec := Record{ID: 3, State: Done, Started: t0, Ended: t0.Add(time.Minute), Claim: Claim{CPUs: 8, MemoryGB: 4},
		Spare: Claim{CPUs: 4, MemoryGB: 2}, Exit: &Exit{Ended: t0.Add(time.Minute)}}
	run, ok := RunOf(rec)
	require.True(t, ok)
	assert.Equal(t, 4, run.Spare)
	assert.Equal(t, Claim{CPUs: 8, MemoryGB: 4}, run.Claim, "the claim it ran with: the total")
}
```

Append to `sched/job_test.go`:

```go
func TestClaim_Spared(t *testing.T) {
	assert.Equal(t, "4+2c/3G", Claim{CPUs: 6, MemoryGB: 3}.Spared(Claim{CPUs: 2, MemoryGB: 1}))
	assert.Equal(t, "4+2c/3G io", Claim{CPUs: 6, MemoryGB: 3, IO: true}.Spared(Claim{CPUs: 2, MemoryGB: 1}))
	assert.Equal(t, "4c/6G", Claim{CPUs: 4, MemoryGB: 6}.Spared(Claim{}))
	assert.Equal(t, "exclusive", Claim{Exclusive: true}.Spared(Claim{}))
}
```

(Check each file's imports: `belief_test.go` needs `require` and `time`, `job_test.go` `assert`.)

- [ ] **Run them:** `CGO_ENABLED=0 go test ./sched/ -run 'TestRunOf_KeepsItsSpareCores|TestClaim_Spared'`. Expected: FAIL to compile.

- [ ] **`sched/belief.go`:** add the field to `Run`, after `WriteBytes`:

```go
	Spare       int       `json:"spare,omitempty"` // the cores it had past its base claim (job scaling spec §5.3)
```

  and in `RunOf`'s literal, after `PeakKB: rec.PeakRSSKB, WallSeconds: …`, add `Spare: rec.Spare.CPUs,`.

- [ ] **`sched/job.go`:** add to `Record`, after `Adopted`:

```go

	// Spare is what a grant added to its base claim when it started (job
	// scaling spec §5.3); Claim is then the total.
	Spare Claim `json:"spare,omitzero"`
```

  and, after `Claim.String`:

```go
// Spared is c as a claim with spare cores reads (job scaling spec §6): the
// base cores, then spare's, "4+2c/3G"; c.String() when spare has no cores.
func (c Claim) Spared(spare Claim) string {
	if spare.CPUs == 0 || c.Exclusive {
		return c.String()
	}
	s := fmt.Sprintf("%d+%dc/%dG", c.CPUs-spare.CPUs, spare.CPUs, c.MemoryGB)
	if c.IO {
		s += " io"
	}
	return s
}
```

- [ ] **Run them:** Expected: PASS.

### Step 1.4: Sizing overrunners and runs with spare cores

**Files:** Modify `sched/size.go`, `sched/surprise.go`; test `sched/size_test.go`.

- [ ] **Write the tests.** In `TestSize`'s table, add:

```go
		{"an overrunner's cores pass the default", Claim{}, belief(at(4, 7.81)), szPolicy, Claim{CPUs: 10, MemoryGB: 2},
			ClaimSources{SourceLearned, SourceLearned, SourceDefault}, 1, false, 0},
		{"an overrunner's cores are held to the cap", Claim{}, belief(at(4, 15)), szPolicy, Claim{CPUs: 14, MemoryGB: 2},
			ClaimSources{SourceLearned, SourceLearned, SourceDefault}, 1, false, 0},
```

  and after the "only the last ten runs count" subtest:

```go
	t.Run("runs with spare cores", func(t *testing.T) {
		spare := at(10, 9.0)
		spare.Spare, spare.PeakKB = 6, 12<<20
		assert.Equal(t, Claim{CPUs: 4, MemoryGB: 6}, Size(Claim{}, belief(spare, at(4, 3.8)), szPolicy).Claim,
			"beside a full peak without spare cores, 12 GB at 10 cores is a floor of 4.8 GB at 4")
		assert.Equal(t, Claim{CPUs: 4, MemoryGB: 14}, Size(Claim{}, belief(spare), szPolicy).Claim,
			"alone, its peak counts as it is")
	})
```

- [ ] **Run them:** `CGO_ENABLED=0 go test ./sched/ -run TestSize`. Expected: FAIL (an overrunner gets 4 cores; the spare run sets 14 GB in the first case).

- [ ] **`sched/size.go`, `Size`:** compute the scaling before the cores, and add the overrunner case:

```go
	sc := ScalingOf(b)
	busy, haveBusy := medianBusy(runs)
	switch {
	case asked.CPUs > 0:
		s.Claim.CPUs, s.Sources.CPUs = asked.CPUs, SourceExplicit
	case sc.Overruns:
		// The hint variables don't reach it: count the cores it takes, up
		// to the cap (job scaling spec §5.2).
		s.Claim.CPUs, s.Sources.CPUs = clampInt(int(math.Ceil(coreHeadroom*sc.Busy)), 1, p.Caps.CPUs), SourceLearned
		learned = true
	case haveBusy:
```

  and the memory evidence takes the cores: `peakKB, full := largestPeak(runs, s.Claim.CPUs)`. In `Size`'s doc comment, "cores learn from busy cores, never past the default claim's" becomes "cores learn from busy cores, never past the default claim's unless the command overruns (then up to the cap)", and "A part that isn't explicit is held within the policy: cores at most the default claim's" becomes "A part that isn't explicit is held within the policy: cores at most the default claim's (an overrunner's at most the cap)".

- [ ] **Replace `largestPeak`:**

```go
// largestPeak is the memory evidence for a claim of cores (smart scheduler
// spec §5.4, job scaling spec §5.2): the largest peak among runs, floors
// included, and whether any of them is a full peak. A run that had spare
// cores counts as a floor of its peak scaled by cores over the cores it had,
// while runs include a full peak of a run without spare cores; with none,
// its peak counts as it is, since fewer cores need no more memory.
func largestPeak(runs []Run, cores int) (kb int64, full bool) {
	var plain, spared, scaled int64
	var plainFull, sparedFull bool
	for _, r := range runs {
		if r.Spare == 0 || r.Claim.CPUs < 1 {
			plain = max(plain, r.PeakKB)
			plainFull = plainFull || r.FullPeak()
			continue
		}
		spared = max(spared, r.PeakKB)
		sparedFull = sparedFull || r.FullPeak()
		scaled = max(scaled, int64(float64(r.PeakKB)*min(1, float64(cores)/float64(r.Claim.CPUs))))
	}
	if plainFull {
		return max(plain, scaled), true
	}
	return max(plain, spared), sparedFull
}
```

- [ ] **`sched/surprise.go`, `NeedsPrior`:** `_, full := largestPeak(runs, 0)` (whether a peak is full doesn't depend on the cores).

- [ ] **Run them:** `CGO_ENABLED=0 go test ./sched/ -run 'TestSize|TestNeedsPrior'`. Expected: PASS.

### Step 1.5: `SpareWidth` and `Grant`

**Files:** Create `sched/grant.go`, `sched/grant_test.go`.

- [ ] **Write the test** (`sched/grant_test.go`):

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGrant(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := szPolicy // 14 cores and 22 GB; the default claim, the reserve, 4 cores and 6 GB
	p.StarveAfter = 10 * time.Minute
	wide := belief(at(4, 3.9)) // p ≈ 0.99: efficient up to 8 cores, twice the 4 it had
	learned := func(id JobID, cores, mem int) Record {
		return Record{ID: id, Order: int64(id), State: Queued, Submitted: now, Claim: Claim{CPUs: cores, MemoryGB: mem},
			Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	}
	queued := func(id JobID, c Claim) Record {
		return Record{ID: id, Order: int64(id), State: Queued, Submitted: now, Claim: c, Sources: all(SourceExplicit)}
	}
	running := func(cores int) Record {
		return Record{ID: 9, State: Running, Claim: Claim{CPUs: cores, MemoryGB: 2}, Started: now}
	}
	grant := func(b *Belief, jobs ...Record) map[JobID]Claim {
		return Grant(Admit(jobs, p, now), jobs, p, func(Record) *Belief { return b })
	}

	assert.Equal(t, 8, SpareWidth(learned(1, 4, 2), wide))
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, grant(wide, learned(1, 4, 2)),
		"an idle host: 14 − 4 − the reserve's 4 leaves 6 cores; it takes 4, to its width of 8, and memory in proportion")
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 2, MemoryGB: 1}}, grant(wide, running(4), learned(1, 4, 2)),
		"what a running job leaves")
	assert.Nil(t, grant(wide, running(6), learned(1, 4, 2)), "the reserve stays free")

	assert.Nil(t, grant(wide, learned(1, 4, 2), queued(2, Claim{CPUs: 11, MemoryGB: 1})), "a job left waiting for cores")
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, grant(wide, learned(1, 4, 2), queued(2, Claim{CPUs: 20, MemoryGB: 1})),
		"a job over the caps waits for nothing a grant holds")
	sizing := queued(2, Claim{})
	sizing.Sizing = SizingAsking
	assert.Nil(t, grant(wide, learned(1, 4, 2), sizing), "a job being sized")
	head := learned(1, 4, 2)
	head.Submitted = now.Add(-time.Hour)
	assert.Nil(t, grant(wide, head, learned(2, 4, 2)), "a starved head starts alone")

	assert.Equal(t, map[JobID]Claim{1: {CPUs: 2, MemoryGB: 1}}, grant(wide, learned(1, 4, 2), learned(2, 4, 2)),
		"queue order: the first takes the 2 cores left, the second gets none")
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 2, MemoryGB: 5}}, grant(wide, learned(1, 4, 10)),
		"6 GB free past the reserve: 8 cores would need 20 GB and 7 cores 18, so 6 cores and 15 GB")

	r10 := at(10, 9.8)
	r10.Spare, r10.PeakKB = 6, 2<<20
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 6, MemoryGB: 1}}, grant(belief(at(4, 3.9), r10), learned(1, 4, 2)),
		"a run at 10 cores peaked at 2 GB: 3 GB with its margin, not 5 in proportion")

	explicitCores := learned(1, 4, 2)
	explicitCores.Sources.CPUs = SourceExplicit
	explicitMemory := learned(1, 4, 2)
	explicitMemory.Sources.Memory = SourceExplicit
	capped := learned(1, 4, 2)
	capped.AboveCap = true
	assert.Nil(t, grant(wide, explicitCores), "explicit cores")
	assert.Nil(t, grant(wide, explicitMemory), "explicit memory")
	assert.Nil(t, grant(wide, capped), "memory held to the cap")
	assert.Nil(t, grant(nil, learned(1, 4, 2)), "p unknown")
	assert.Nil(t, grant(belief(at(4, 7.81)), learned(1, 4, 2)), "an overrunner")
	assert.Nil(t, grant(belief(at(4, 2.17)), learned(1, 4, 2)), "a width of 4 is no wider")
	assert.Nil(t, Grant(Decision{Start: []JobID{1}}, []Record{learned(1, 4, 2)}, Policy{}, func(Record) *Belief { return wide }), "no caps")
}
```

- [ ] **Run it:** `CGO_ENABLED=0 go test ./sched/ -run TestGrant`. Expected: FAIL to compile (`undefined: Grant`).

- [ ] **Write `sched/grant.go`:**

```go
package sched

import "math"

// SpareWidth is the width a grant could widen queued job r of belief b
// (nil for none) to, or 0 when r can't be granted spare cores (job scaling
// spec §5.3): its cores aren't learned (they are explicit, exclusive, the
// agent's or the default), its memory is explicit or held to the cap, b's
// parallel fraction is unknown or it overruns, or b's ideal width is no
// wider than r's cores.
func SpareWidth(r Record, b *Belief) int {
	if r.Claim.Exclusive || r.Sources.CPUs != SourceLearned || r.Sources.Memory == SourceExplicit || r.AboveCap {
		return 0
	}
	s := ScalingOf(b)
	if !s.Known || s.Overruns || s.Ideal <= r.Claim.CPUs {
		return 0
	}
	return s.Ideal
}

// Grant is what each job dec starts gets past its base claim (job scaling
// spec §5.3), a pure function of its arguments: dec is Admit's decision
// over jobs under p, and belief gives a record's belief (nil for none). It
// grants nothing while a queued job waits for anything but the caps. The
// pool is the caps less the running jobs' claims, the starting jobs' base
// claims and a reserve of the default claim. In dec's order, each job
// SpareWidth can widen takes the most cores up to that width whose memory
// (spareMemory) fits what the pool has left, and the pool shrinks by what
// it took. A job that gets nothing has no entry; nil when none gets
// anything.
func Grant(dec Decision, jobs []Record, p Policy, belief func(Record) *Belief) map[JobID]Claim {
	if len(dec.Start) == 0 || !p.usable() {
		return nil
	}
	for _, why := range dec.Waiting {
		if why != waitOver {
			return nil
		}
	}
	starting := make(map[JobID]Record, len(dec.Start))
	for _, id := range dec.Start {
		starting[id] = Record{}
	}
	var used Need
	for _, r := range jobs {
		if _, ok := starting[r.ID]; ok {
			starting[r.ID] = r
			used = used.plus(r.Claim.Need(p.Caps))
		} else if r.State == Running {
			used = used.plus(r.Claim.Need(p.Caps))
		}
	}
	reserve := p.DefaultClaim.Need(p.Caps)
	cores := p.Caps.CPUs - used.CPUs - reserve.CPUs
	memory := p.Caps.MemoryGB - used.MemoryGB - reserve.MemoryGB
	var out map[JobID]Claim
	for _, id := range dec.Start {
		r := starting[id]
		b := belief(r)
		for t := min(SpareWidth(r, b), r.Claim.CPUs+cores); t > r.Claim.CPUs; t-- {
			extra := spareMemory(r, b, t, p) - r.Claim.MemoryGB
			if extra > memory {
				continue
			}
			if out == nil {
				out = map[JobID]Claim{}
			}
			out[id] = Claim{CPUs: t - r.Claim.CPUs, MemoryGB: extra}
			cores -= t - r.Claim.CPUs
			memory -= extra
			break
		}
	}
	return out
}

// spareMemory is job r's memory at t cores (job scaling spec §5.3): its
// claim's memory grown in proportion to t over its claim's cores, lowered
// to the largest full peak, with its margin, of b's last runs that had t
// cores or more, but never below its claim's memory nor above the cap.
func spareMemory(r Record, b *Belief, t int, p Policy) int {
	m := int(math.Ceil(float64(r.Claim.MemoryGB) * float64(t) / float64(r.Claim.CPUs)))
	var peak int64
	for _, run := range b.recent() {
		if run.FullPeak() && run.Claim.CPUs >= t {
			peak = max(peak, run.PeakKB)
		}
	}
	if peak > 0 {
		m = min(m, withMargin(peak))
	}
	return min(max(m, r.Claim.MemoryGB), p.Caps.MemoryGB)
}
```

- [ ] **Run it:** `CGO_ENABLED=0 go test ./sched/ -run TestGrant`. Expected: PASS.

### Step 1.6: Durations at a job's cores

**Files:** Modify `sched/durations.go`, `sched/beliefs_view.go`, `core/queue_view.go`, `core/queue_advisor.go`; test `sched/size_test.go` (`TestUsualAndOverdueAfter`).

- [ ] **Write the test.** In `TestUsualAndOverdueAfter`, every `Usual(x)` becomes `Usual(x, 0)` and `OverdueAfter(x)` becomes `OverdueAfter(x, 0)`, and add:

```go
	t.Run("at other cores", func(t *testing.T) {
		b := belief(at(3, 2.74), at(3, 2.74), at(3, 2.74)) // p ≈ 0.953; 100 s at 3 cores
		usual, p90, n, _ := Usual(b, 4)
		assert.Equal(t, 78*time.Second, usual, "100 s × f(4) ÷ f(3)")
		assert.Equal(t, 78*time.Second, p90)
		assert.Equal(t, 3, n)
		usual, _, _, _ = Usual(b, 0)
		assert.Equal(t, 100*time.Second, usual, "0 cores: as measured")
		assert.Equal(t, 378*time.Second, OverdueAfter(b, 4), "max(2 × 78 s, 78 s + 5m)")
	})
```

- [ ] **Run it:** Expected: FAIL to compile (`too many arguments`).

- [ ] **`sched/durations.go`:**

```go
// Usual is a belief's usual duration and its P90 at cores (smart scheduler
// spec §5.5, job scaling spec §5.4): the median and the 90th percentile
// (nearest rank) of the wall times of its duration runs among its last
// window, each scaled from the cores it had to cores (Scaling.scale: as
// measured when cores is 0, b's parallel fraction is unknown or it
// overruns); n is how many there were. With none, the prior's duration
// stands in for both, unscaled, and fromPrior is true; with no prior
// either, all are zero.
func Usual(b *Belief, cores int) (usual, p90 time.Duration, n int, fromPrior bool) {
	s := ScalingOf(b)
	var walls []float64
	for _, r := range b.recent() {
		if r.TeachesDuration() {
			walls = append(walls, s.scale(r.WallSeconds, r.Claim.CPUs, cores))
		}
	}
```

  (the rest unchanged), and:

```go
// OverdueAfter is how long a job of b at cores may run before it is
// overdue: max(2 × P90, P90 + 5 minutes) of Usual at cores once b has
// overdueRuns duration runs, else 0 (never).
func OverdueAfter(b *Belief, cores int) time.Duration {
	_, p90, n, _ := Usual(b, cores)
```

- [ ] **`sched/beliefs_view.go`:** `BeliefView` gains `Scaling Scaling` after `Sized`, its comment adds "how its command scales"; `ViewBelief` becomes:

```go
func ViewBelief(b Belief, p Policy) BeliefView {
	v := BeliefView{Belief: b, RepoName: RepoName(b.Repo), Scaling: ScalingOf(&b)}
	if p.usable() {
		v.Sized = Size(Claim{}, &b, p)
	}
	v.Usual, v.P90, v.DurationRuns, v.UsualFromPrior = Usual(&b, v.Sized.Claim.CPUs)
	return v
}
```

- [ ] **`core/queue_view.go`:** the forecast's `usualOf` calls `sched.Usual(q.beliefOf(r.Signature), r.Claim.CPUs)`; `hostUsual` calls `sched.Usual(&m.queue.beliefs.Beliefs[i], 0)`; `queueJobView` calls `sched.Usual(b, r.Claim.CPUs)` and `sched.OverdueAfter(b, r.Claim.CPUs)`.
- [ ] **`core/queue_advisor.go`:** both calls become `sched.Usual(&b, 0)` and `sched.Usual(like, 0)`.
- [ ] **Run:** `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./sched/`. Expected: PASS.

### Step 1.7: Surprises and the reconcile prompt

**Files:** Modify `sched/surprise.go`, `sched/advisor/prompt.md`; test `sched/surprise_test.go`.

- [ ] **Write the test.** Append to `TestSurprise`:

```go
	vitest := at(4, 7.81)
	assert.Equal(t, "it kept 7.8 cores busy on a 4-core claim: the hint variables don't reach it",
		Surprise(&Belief{}, vitest, SourceLearned), "on a first run too")
	assert.Equal(t, "", Surprise(belief(at(4, 7.81)), vitest, SourceLearned), "a belief that already overruns")
	wide := at(12, 10)
	wide.WallSeconds, wide.CPUSeconds = 30, 300
	assert.Equal(t, "", Surprise(belief(at(3, 2.74), at(3, 2.74), at(3, 2.74)), wide, SourceLearned),
		"30 s at 12 cores is about what 100 s at 3 scales to (35 s)")
```

- [ ] **Run it:** Expected: FAIL (no overrun surprise; the 12-core run reads as under a third of the usual).
- [ ] **`sched/surprise.go`, `Surprise`:**

```go
// Surprise says how run surprises b, which has not recorded it yet (smart
// scheduler spec §5.6, job scaling spec §5.5), or "" when it doesn't: a
// peak above its memory claim when memory (the claim's memory source) was
// learned, the agent's or the default; a SIGKILL nobody requested; more
// than overrunRatio × its claim's cores kept busy while b doesn't overrun;
// or, with three duration runs before it, a wall time over three times or
// under a third of their median, each scaled to the run's cores. All but
// the overrun need a run before it.
func Surprise(b *Belief, run Run, memory Source) string {
	if b == nil {
		return ""
	}
	before := len(b.Runs) > 0
	if before && (memory == SourceLearned || memory == SourceAgent || memory == SourceDefault) && run.PeakKB > int64(run.Claim.MemoryGB)*kbPerGB {
		return fmt.Sprintf("it peaked at %.1f GB, above its %d GB claim", float64(run.PeakKB)/kbPerGB, run.Claim.MemoryGB)
	}
	if before && run.Outcome == OutcomeSignal && run.Signal == "SIGKILL" {
		return "it was killed by SIGKILL, which nobody asked for (the out-of-memory killer?)"
	}
	s := ScalingOf(b)
	if c := run.Claim.CPUs; run.teachesCores() && c > 0 && !s.Overruns {
		if busy := run.CPUSeconds / run.WallSeconds; busy > overrunRatio*float64(c) {
			return fmt.Sprintf("it kept %.1f cores busy on a %d-core claim: the hint variables don't reach it", busy, c)
		}
	}
	if before && run.TeachesDuration() {
		var walls []float64
		for _, r := range b.recent() {
			if r.TeachesDuration() {
				walls = append(walls, s.scale(r.WallSeconds, r.Claim.CPUs, run.Claim.CPUs))
			}
		}
		if m, ok := median(walls); ok && len(walls) >= overdueRuns && (run.WallSeconds > 3*m || run.WallSeconds < m/3) {
			return fmt.Sprintf("it took %s, where it usually takes %s", seconds(run.WallSeconds), seconds(m))
		}
	}
	return ""
}
```

- [ ] **`sched/advisor/prompt.md`:** at the end of the reconcile paragraph, add: "When the surprise is that the run kept more cores busy than it claimed, the command doesn't read the hint variables: say which flag or environment variable sets its parallelism (a test runner's worker count, say), if you know one."
- [ ] **Run:** `CGO_ENABLED=0 go test ./sched/...`. Expected: PASS (if a test pins the prompt's text, update it to the new paragraph).

### Step 1.8: The grant in the model

**Files:** Modify `core/queue_admit.go`, `core/queue_view.go`; test `core/queue_beliefs_test.go`.

- [ ] **Write the tests.** Append to `core/queue_beliefs_test.go`:

```go
// wqWideBelief gives sub's signature a done run at 4 cores that kept 3.8
// busy (p ≈ 0.98): a job of it is sized 4c/2G and is efficient up to 8
// cores, twice the 4 it had.
func wqWideBelief(t *testing.T, m *Model, sub sched.Submit) sched.SignatureID {
	t.Helper()
	sig := wqSigOf(t, sub.Argv, sub.Cwd)
	m.queue.beliefs.Ensure(sig, time.Now()).AddRun(sched.Run{Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 4, MemoryGB: 2},
		PeakKB: 1 << 20, CPUSeconds: 380, WallSeconds: 100})
	return sig.ID()
}

func TestJobQueue_AnIdleHostLendsSpareCoresToAJobThatScales(t *testing.T) {
	m := wqLiveModel(t)
	sub := wqJob(t, "sh", "-c", "exit 0")
	sig := wqWideBelief(t, m, sub)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wqPump(t, m)

	rec := wqRecord(t, m, id)
	require.Equal(t, sched.Running, rec.State)
	// Caps of 14 cores and 24 GB (wqHost), the reserve's 4c/6G and the base
	// 4c/2G leave 6 spare cores: it takes 4, to its width of 8.
	assert.Equal(t, sched.Claim{CPUs: 4, MemoryGB: 2}, rec.Spare)
	assert.Equal(t, sched.Claim{CPUs: 8, MemoryGB: 4}, rec.Claim)
	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.Equal(t, 8, spec.HintCPUs, "the hint variables carry the total")

	m.admitJobs()
	assert.Equal(t, 8, wqRecord(t, m, id).Claim.CPUs, "a pass never re-sizes a running claim")

	wqSettle(t, m, id)
	b := m.queue.beliefs.Find(sig)
	require.NotNil(t, b)
	assert.Equal(t, 4, b.Runs[0].Spare, "the run records its spare cores")
}

func TestJobQueue_NoSpareCoresWhileAnotherJobWaits(t *testing.T) {
	m := wqLiveModel(t)
	blocker := wqJob(t, "sleep", "1")
	blocker.Claim = sched.Claim{CPUs: 14, MemoryGB: 1}
	bid, _, err := m.submitJob(blocker)
	require.NoError(t, err)
	require.Equal(t, sched.Running, wqRecord(t, m, bid).State)

	sub := wqJob(t, "sleep", "30")
	wqWideBelief(t, m, sub)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wide := wqJob(t, "sleep", "1")
	wide.Claim = sched.Claim{CPUs: 11, MemoryGB: 1}
	wid, _, err := m.submitJob(wide)
	require.NoError(t, err)

	wqWait(t, "the blocker to end and the next job to start", func() bool {
		if wqRecord(t, m, id).State == sched.Queued {
			wqPass(t, m)
		}
		return wqRecord(t, m, id).State != sched.Queued
	})
	rec := wqRecord(t, m, id)
	assert.Zero(t, rec.Spare, "job %d waits for 11 cores, so nothing is lent", wid)
	assert.Equal(t, 4, rec.Claim.CPUs)
	assert.Equal(t, sched.Queued, wqRecord(t, m, wid).State)
}
```

- [ ] **Run them:** `CGO_ENABLED=0 go test ./core/ -run 'SpareCores'`. Expected: FAIL (no spare on the record; the hint is 4).
- [ ] **`core/queue_admit.go`, `admitJobs`:** declare `var spare map[sched.JobID]sched.Claim` beside `start`; after `start = dec.Start`:

```go
		// Spare cores for the jobs it starts, when nothing else waits (job
		// scaling spec §5.3).
		spare = sched.Grant(dec, jobs, policy, func(r sched.Record) *sched.Belief { return q.beliefOf(r.Signature) })
```

  and the start loop calls `m.startJob(i, policy, now, spare[id])`.
- [ ] **`startJob`:**

```go
// startJob admits job i of the store with spare added to its claim (job
// scaling spec §5.3; zero for none): it is running from now (its claim is
// taken by the next pass), and a model job starts its shim (spec §5.3).
func (m *Model) startJob(i int, policy sched.Policy, now time.Time, spare sched.Claim) {
	q := m.queue
	rec := &q.store.Jobs[i]
	rec.State, rec.Started, rec.PID = sched.Running, now, 0
	if spare != (sched.Claim{}) {
		rec.Claim.CPUs += spare.CPUs
		rec.Claim.MemoryGB += spare.MemoryGB
		rec.Spare = spare
	}
```

  (the rest unchanged: the hint is `rec.Claim.Need(policy.Caps).CPUs`, now the total).
- [ ] **`core/queue_view.go`:** `JobView` gains, after `StartsAt`:

```go
	// Job scaling (spec 2026-11-05-job-scaling §5.3): what a grant added to
	// a started job's base claim, and the width a grant could widen a
	// queued job to (0 when none could).
	Spare sched.Claim
	Ideal int
```

  `queueJobView`'s literal sets `Spare: r.Spare,`, and its `case sched.Queued:` adds `jv.Ideal = sched.SpareWidth(r, b)`.
- [ ] **Run:** `CGO_ENABLED=0 go test ./core/ -run 'JobQueue'`. Expected: PASS.

### Step 1.9: The wire and the docs

- [ ] **Regenerate:** `CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update`, then `CGO_ENABLED=0 go test ./core/rpc`. Expected: PASS, with `docs/specs/protocol.md` gaining `Spare`, `Ideal` and `Scaling`.
- [ ] **`sched/README.md`:** in Files add "`scaling.go` — `Scaling`, `ScalingOf`." and "`grant.go` — `SpareWidth`, `Grant`."; in "Signatures, beliefs and sizing", **Size** adds "an overrunner's cores (its runs keep more than 1.25 × their cores busy, the hint variables not reaching it) are 1.25 × its median busy cores, up to the cap; a run that had spare cores counts toward memory as a floor scaled to the claim's cores beside a full peak without spare cores, and as it is without one"; **Durations** adds "each run's wall time scaled from its cores to the cores asked about"; and two bullets after **Durations**:
  - "**Scaling.** `ScalingOf` reads a belief's done runs for Amdahl's parallel fraction (each run with 2 cores or more gives (1 − 1/busy) ÷ (1 − 1/cores); the median), the ideal width (the most cores above 50% efficiency, at most twice the most cores a run had) and whether it overruns ([spec](../docs/superpowers/specs/2026-11-05-job-scaling-design.md))."
  - "**The grant.** `Grant` runs on what `Admit` starts: while no queued job waits but over the caps, each starting job `SpareWidth` can widen takes spare cores up to its ideal width, from the caps less the running claims, the starting base claims and a reserve of the default claim, with memory in proportion (lowered to what a run at that width measured)."
- [ ] **`sched/CLAUDE.md`:** the purity rule names `ScalingOf` and `Grant` too ("Keep `Size`, `Usual`, `OverdueAfter`, `RunOf`, `ScalingOf` and `Grant` pure functions of their arguments, …", **Enforced** adds `TestScalingOf` and `TestGrant`), and a new rule after it:
  - "**Let `Grant` lend nothing while a queued job waits for anything but the caps, never lend into the reserve (the default claim), and let `Size` count an overrunner's cores past the default claim only up to the cap.** A grant held while a job waits keeps that job waiting for the whole run, and one into the reserve leaves the next default job no room; nothing would say so. **Enforced** by `TestGrant` (its waiting, reserve and starved-head cases) and `TestSize` (its overrunner cases)."
- [ ] **`docs/claude/the-work-queue.md`:** rule 9 adds, after the `resizeQueued` sentence: "`admitJobs` then asks `sched.Grant` for spare cores (job scaling spec §5.3) and `startJob` adds them to the record's claim, recording them in `Record.Spare`, before the start job writes the hint: the claim a job starts with is the total, and `RunOf` records the spare cores in `Run.Spare`." The `queueView` paragraph adds that durations come at each job's cores (a queued job's base, a running job's total) and that a queued job's `Ideal` is `sched.SpareWidth`.
- [ ] **Run:** `go run ./tools/claudemd -v` (budgets) and `CGO_ENABLED=0 go test ./tools/... .` Expected: PASS.

### Step 1.10: Verify and commit Package 1

- [ ] `gofmt -l sched core internal ui` prints nothing; `CGO_ENABLED=0 go vet ./sched/... ./core/...` is clean.
- [ ] `loom job run -d "job scaling: full suite" -- env CGO_ENABLED=0 go test ./...` and `loom job run -d "job scaling: race" -- env CC=clang CGO_ENABLED=1 go test -race ./...` (background). Expected: both exit 0.
- [ ] Self-review the diff against the spec's §5 (every rule), then commit by path:

```bash
git add sched/scaling.go sched/scaling_test.go sched/grant.go sched/grant_test.go sched/belief.go sched/belief_test.go \
  sched/job.go sched/job_test.go sched/size.go sched/size_test.go sched/surprise.go sched/surprise_test.go \
  sched/durations.go sched/beliefs_view.go sched/advisor/prompt.md core/queue_admit.go core/queue_view.go \
  core/queue_advisor.go core/queue_beliefs_test.go docs/specs/protocol.md sched/README.md sched/CLAUDE.md \
  docs/claude/the-work-queue.md docs/superpowers/specs/2026-11-05-job-scaling-design.md docs/superpowers/plans/2026-11-05-job-scaling.md
git commit -m "feat(sched,core): learn how jobs scale, lend spare cores, count overrunners" -- <the same paths>
```

## Package 2: The surfaces

### Step 2.1: The belief text's scaling line

**Files:** Modify `sched/belief_text.go`; test `sched/beliefs_view_test.go`.

- [ ] **Write the test.** In `TestFormatBelief`'s list add `"94% parallel over 1 run · efficient up to 16 cores"` (its run: 8 cores, 5.5 busy). Append:

```go
func TestFormatBelief_Scaling(t *testing.T) {
	line := func(b *Belief) string {
		for _, l := range FormatBelief(ViewBelief(*b, szPolicy)) {
			if strings.HasPrefix(l, "scaling") {
				return l
			}
		}
		return ""
	}
	wasm := at(3, 2.74)
	wasm.WallSeconds, wasm.CPUSeconds = 43, 43*2.74
	assert.Equal(t, "scaling   95% parallel over 1 run · efficient up to 6 cores · ~34s at 4, ~24s at 6", line(belief(wasm)))
	assert.Equal(t, "scaling   ignores the hint variables · keeps 7.8 cores busy, claims 10", line(belief(at(4, 7.81))))
	assert.Equal(t, "scaling   unknown until a run with 2 cores or more", line(belief(at(1, 1.0))))
}
```

- [ ] **Run it:** Expected: FAIL (no scaling line).
- [ ] **`sched/belief_text.go`:** after the `usually`/`about` switch in `FormatBelief`, add `lines = append(lines, scalingLine(v))`, and:

```go
// scalingLine is the belief text's line on how its command scales (job
// scaling spec §6): an overrunner's busy cores and claim; or p, the runs it
// came from, the ideal width and the usual duration at the claim's cores
// and at the ideal width; or that it is unknown.
func scalingLine(v BeliefView) string {
	s, base := v.Scaling, v.Sized.Claim.CPUs
	switch {
	case s.Overruns:
		line := fmt.Sprintf("scaling   ignores the hint variables · keeps %.1f cores busy", s.Busy)
		if base > 0 {
			line += fmt.Sprintf(", claims %d", base)
		}
		return line
	case !s.Known:
		return "scaling   unknown until a run with 2 cores or more"
	}
	runs := "1 run"
	if s.Runs != 1 {
		runs = fmt.Sprintf("%d runs", s.Runs)
	}
	line := fmt.Sprintf("scaling   %.0f%% parallel over %s · efficient up to %d cores", 100*s.Parallel, runs, s.Ideal)
	if v.DurationRuns == 0 || base < 1 {
		return line
	}
	at := func(c int) string {
		u, _, _, _ := Usual(&v.Belief, c)
		return fmt.Sprintf("~%s at %d", RoughDuration(u), c)
	}
	if s.Ideal > base {
		return line + " · " + at(base) + ", " + at(s.Ideal)
	}
	return line + " · " + at(base)
}
```

- [ ] **Run:** `CGO_ENABLED=0 go test ./sched/ -run TestFormatBelief`. Expected: PASS.

### Step 2.2: The CLI

**Files:** Modify `internal/cli/job_sizing.go`, `internal/cli/job_follow.go`, `internal/cli/job_beliefs.go`; tests `internal/cli/job_sizing_test.go`, `internal/cli/job_beliefs_test.go`.

- [ ] **Write the tests.** In `TestSizingLine`'s table add:

```go
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 2}, Sources: srcs(L, L, D), Learned: 1, Usual: 34 * time.Second, Ideal: 6},
			"sized 4c/2G (cores and memory learned from 1 run, I/O default); usually 34s; up to 6 cores when idle"},
```

  In `TestClaimText` add:

```go
	assert.Equal(t, "4+2c/3G learned·1", claimText(core.JobView{Claim: sched.Claim{CPUs: 6, MemoryGB: 3}, Spare: sched.Claim{CPUs: 2, MemoryGB: 1},
		Sources: srcs(sched.SourceLearned, sched.SourceLearned, sched.SourceDefault), Learned: 1}))
```

  Append:

```go
func TestStartedLine(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	v := core.JobView{ID: 21, Submitted: t0, Started: t0.Add(6 * time.Second), Claim: sched.Claim{CPUs: 4, MemoryGB: 2}}
	assert.Equal(t, "job 21 started after 6s", startedLine(v))
	v.Claim, v.Spare = sched.Claim{CPUs: 6, MemoryGB: 3}, sched.Claim{CPUs: 2, MemoryGB: 1}
	assert.Equal(t, "job 21 started after 6s with 2 spare cores (4+2c/3G)", startedLine(v))
	v.Claim, v.Spare = sched.Claim{CPUs: 5, MemoryGB: 3}, sched.Claim{CPUs: 1, MemoryGB: 1}
	assert.Equal(t, "job 21 started after 6s with 1 spare core (4+1c/3G)", startedLine(v))
}
```

  In `TestWriteBeliefs` the row's regexp becomes `` `(?m)^3f9a1c2b7d10 +polyoxide +\. +bash scripts/web-smoke\.sh +0 +8c/16G io learned·5 +- +~2m +2h00m ago$` ``, and before the empty page's check add:

```go
	page.Beliefs[0].Scaling = sched.Scaling{Known: true, Parallel: 0.72, Runs: 1, Ideal: 4}
	out.Reset()
	writeBeliefs(&out, page, now)
	assert.Regexp(t, `learned·5 +72% +~2m`, out.String())
	page.Beliefs[0].Scaling = sched.Scaling{Overruns: true, Busy: 7.8}
	out.Reset()
	writeBeliefs(&out, page, now)
	assert.Regexp(t, `learned·5 +overruns +~2m`, out.String())
```

- [ ] **Run them:** `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestSizingLine|TestClaimText|TestStartedLine|TestWriteBeliefs'`. Expected: FAIL.
- [ ] **`internal/cli/job_sizing.go`:** `claimText` uses `text := v.Claim.Spared(v.Spare)`; `sizingLine` ends with

```go
	if v.Ideal > 0 {
		line += fmt.Sprintf("; up to %d cores when idle", v.Ideal)
	}
	return line
```

  and add:

```go
// startedLine is the follower's line when a job starts (job scaling spec
// §6): how long it waited, and the spare cores it was lent.
func startedLine(v core.JobView) string {
	line := fmt.Sprintf("job %d started after %s", v.ID, rounded(v.Started.Sub(v.Submitted)))
	switch n := v.Spare.CPUs; {
	case n == 1:
		line += fmt.Sprintf(" with 1 spare core (%s)", v.Claim.Spared(v.Spare))
	case n > 1:
		line += fmt.Sprintf(" with %d spare cores (%s)", n, v.Claim.Spared(v.Spare))
	}
	return line
}
```

- [ ] **`internal/cli/job_follow.go`:** the start line becomes `f.d.say("%s", startedLine(v))`.
- [ ] **`internal/cli/job_beliefs.go`:** the header becomes `"ID\tREPO\tFOLDER\tCOMMAND\tRUNS\tCLAIM\tSCALES\tUSUAL\tLAST SEEN"`, the row prints `scalesText(v.Scaling)` after the claim, and add:

```go
// scalesText is the SCALES column (job scaling spec §6): the parallel
// fraction as a percentage, "overruns", or "-" while it is unknown.
func scalesText(s sched.Scaling) string {
	switch {
	case s.Overruns:
		return "overruns"
	case s.Known:
		return fmt.Sprintf("%.0f%%", 100*s.Parallel)
	}
	return "-"
}
```

- [ ] **Run them:** Expected: PASS. Then `CGO_ENABLED=0 go test ./internal/cli/` (the run and follower goldens: a job sized from the default claim has no `Ideal`, so their lines stay as they are).

### Step 2.3: The work view's claim cell

**Files:** Modify `ui/work.go`; test `ui/work_test.go`.

- [ ] **Write the test.** Append to `ui/work_test.go`:

```go
func TestWorkClaim_ShowsSpareCores(t *testing.T) {
	j := core.JobView{Claim: sched.Claim{CPUs: 6, MemoryGB: 3}, Spare: sched.Claim{CPUs: 2, MemoryGB: 1}}
	assert.Equal(t, "4+2c/3G", workClaim(j))
	j.AboveCap = true
	assert.Equal(t, "4+2c/3G!", workClaim(j))
}
```

- [ ] **Run it:** Expected: FAIL (`6c/3G`).
- [ ] **`ui/work.go`:** `workClaim` uses `j.Claim.Spared(j.Spare)` in both branches; `workClaimW` becomes 12 (a `4+10c/16G io` fits). Run `CGO_ENABLED=0 go test ./ui/ ./app/`; a render test whose width now truncates the owner column gets one more column of width.
- [ ] **Run:** Expected: PASS.

### Step 2.4: The context sentence

- [ ] **`session/loom_context_shared.md`:** "The scheduler sizes each job from what the same command used before, so pass `--cpus`, `--mem` or `--io` only to override it; use `--exclusive` for benchmarks." becomes "The scheduler sizes each job from what the same command used before, and lends idle cores to jobs that scale, so pass `--cpus`, `--mem` or `--io` only to override it; use `--exclusive` for benchmarks."
- [ ] **Run:** `CGO_ENABLED=0 go test ./session/ ./internal/cli/ -run 'Context'`. Expected: PASS.

### Step 2.5: Docs

- [ ] **`USAGE.md`**, the work queue's sizing section: spare cores ("on an idle host, with nothing else waiting, a job that scales starts with spare cores up to its ideal width, always leaving one default claim free; its claim reads `4+2c/3G`; an explicit `--cpus` or `--mem` forgoes them"), overrunners ("a command that keeps over 1.25 × its cores busy, ignoring the hint variables, claims the cores it takes, up to the cap"), the sized line's `up to N cores when idle`, the follower's start line, `loom job beliefs`' SCALES column and `show`'s scaling line.
- [ ] **`sched/README.md`:** **Reads** adds that `FormatBelief` prints the scaling line.
- [ ] **Run:** `CGO_ENABLED=0 go test . ./tools/...` (the docs lint). Expected: PASS.

### Step 2.6: Verify and commit Package 2

- [ ] `gofmt -l sched core internal ui` prints nothing; `CGO_ENABLED=0 go vet ./...` is clean.
- [ ] `loom job run -d "job scaling: full suite" -- env CGO_ENABLED=0 go test ./...` and the race run (background). Expected: both exit 0.
- [ ] Self-review against the spec's §6–§8, then commit by path:

```bash
git add sched/belief_text.go sched/beliefs_view_test.go internal/cli/job_sizing.go internal/cli/job_sizing_test.go \
  internal/cli/job_follow.go internal/cli/job_beliefs.go internal/cli/job_beliefs_test.go ui/work.go ui/work_test.go \
  session/loom_context_shared.md USAGE.md sched/README.md
git commit -m "feat(sched,cli,ui): show spare cores, overrunners and how commands scale" -- <the same paths>
```

## Self-review

- **Spec coverage:** §5.1 → Step 1.2; §5.2 → 1.4 (amended, 1.1); §5.3 → 1.5, 1.8; §5.4 → 1.6 (the forecast amended, 1.1); §5.5 → 1.7; §6 → 2.1, 2.2; §7 → 2.3 (the pager is `FormatBelief`, 2.1); §8 → 2.4; §9 → 1.3, 1.6, 1.8, 1.9; §11 → `SpareWidth`'s and `Grant`'s guards (1.5) and `beliefOf`'s nil while learning is off; §12 → each step's tests; §13 → the two packages.
- **Names:** `Scaling{Overruns, Busy, Known, Parallel, Runs, Ideal}`, `ScalingOf`, `Scaling.scale`, `SpareWidth`, `Grant`, `spareMemory`, `largestPeak(runs, cores)`, `Usual(b, cores)`, `OverdueAfter(b, cores)`, `Record.Spare` (a `Claim`), `Run.Spare` (an `int`), `Claim.Spared`, `JobView.Spare`, `JobView.Ideal`, `BeliefView.Scaling`, `startedLine`, `scalesText`, `scalingLine` are used the same way in every step.
- **Fixtures:** `at(cores, busy)` lives in `sched/scaling_test.go` and is shared by the size, grant, surprise and belief text tests; `szPolicy`, `done`, `belief` and `all` are `sched/size_test.go`'s.

## Execution notes (2026-11-05)

- Step 1.5: a mutation check found `SpareWidth`'s `s.Overruns` term dead (`ScalingOf` never marks an overrunner `Known`), so it was dropped and `Scaling`'s doc states the invariant; every other guard in `Grant` and `ScalingOf` turns a test red when removed.
- Step 1.8: the first test's job is `sleep 1`, not `sh -c 'exit 0'`, which settled inside the pump before the test could see it running.
- Step 1.9: `docs/specs/protocol.md` lists methods and Reply fields, not view fields, so it needed no change.
- Step 2.2: the self-review found `sizingLine` printing a started job's total claim, so a job that starts at once with spare cores (the follower never sees it queued, and prints no start line) read `sized 8c/4G`; it now prints `sized 4+4c/4G` (`Claim.Spared`), with a `TestSizingLine` case. The full and race suites (jobs 81 and 82) ran before this one-line fix; the CLI package was rerun whole afterwards, and its job tests under `-race`.
- Package 1's full suite (job 78) failed once in `TestAccountList_ShowsAccountDirMissing`, an account test the change doesn't touch; it passed 8/8 alone and in the race run (job 79).

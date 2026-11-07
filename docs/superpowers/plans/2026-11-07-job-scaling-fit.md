# Job scaling fit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fit a command's parallel fraction from its busy cores at two widths or more, let that only raise p, and floor a job's learned base cores at the widest measured width the fit shows at least 1.25× faster.

**Architecture:** All in `sched`, as pure functions: `ScalingOf` gains the widths and the fit (`sched/scaling.go`), `Size` applies `Scaling.floor` where the cores come from the job's own runs (`sched/size.go`), and the belief text names the widths when the fit gave p (`sched/belief_text.go`). Grants, durations and surprises read `Parallel` and need no change. Spec: [`docs/superpowers/specs/2026-11-07-job-scaling-fit-design.md`](../specs/2026-11-07-job-scaling-fit-design.md).

**Tech Stack:** Go 1.25, testify. Heavy commands go through `loom job run` (the repo's rule for loom sessions).

---

## Package 1: the fit and the floor

**Files:**
- Modify: `sched/scaling.go` (the `Scaling` struct, `ScalingOf`, new `fitParallel`, `pairParallel`, `floor`, `floorGain`)
- Modify: `sched/size.go:174-176` (the `haveBusy` case)
- Test: `sched/scaling_test.go` (`TestScalingOf`), `sched/size_test.go` (`TestSize`, `TestSize_FromTheFamily`'s table)

- [ ] **Step 1: Write the failing `TestScalingOf` cases** — append to `TestScalingOf` in `sched/scaling_test.go`:

```go
	gotest := ScalingOf(belief(at(1, 0.31), at(2, 0.63)))
	assert.Equal(t, []int{1, 2}, gotest.Widths)
	assert.Equal(t, 1.0, gotest.Fit, "2.03× the busy cores at twice the width: held to 1")
	assert.Equal(t, 1.0, gotest.Parallel, "the 2-core run alone reads 0: the fit raises it")
	assert.Equal(t, 2, gotest.Runs, "every run the fit read")
	assert.Equal(t, 4, gotest.Ideal, "p of 1: twice the 2 cores it had")

	doc := ScalingOf(belief(at(1, 0.51), at(2, 0.70)))
	assert.InDelta(t, 0.543, doc.Fit, 0.001, "R = 1.37: 0.37 ÷ (1.37 × 0.5)")
	assert.InDelta(t, 0.543, doc.Parallel, 0.001)

	assert.Equal(t, []int{2, 4}, nextest.Widths)
	assert.Zero(t, nextest.Fit, "fewer busy cores at 4 than at 2: R ≤ 1 gives 0")
	assert.InDelta(t, 0.507, nextest.Parallel, 0.001, "the fit never lowers p")
	assert.Equal(t, 2, nextest.Runs, "the runs the busy p came from")

	clippy := ScalingOf(belief(at(2, 0.92), at(3, 0.93), at(4, 3.37), at(8, 1.02)))
	assert.InDelta(t, 0.281, clippy.Fit, 0.001, "the median over six pairs, the cold 4-core run among them")
	assert.Equal(t, 2, clippy.Ideal)

	assert.Nil(t, ScalingOf(belief(at(2, 1.0), at(2, 1.2))).Widths, "one width: no fit")
	laddered := at(2, 0.63)
	laddered.Laddered = true
	assert.Nil(t, ScalingOf(belief(at(1, 0.31), laddered)).Widths, "a laddered run leaves its width out")
	assert.Nil(t, ScalingOf(belief(at(1, 0.31), done(1<<20, 50, 100))).Widths, "an exclusive run (no cores) leaves its width out")
	assert.Nil(t, ScalingOf(belief(at(4, 7.81), at(8, 12))).Widths, "an overrunner has no fit")
```

- [ ] **Step 2: Run it to see it fail**

Run: `loom job run -d "sched scaling fit tests (red)" -- sh -c 'CGO_ENABLED=0 go test ./sched -run TestScalingOf'`
Expected: FAIL to build, `gotest.Widths undefined (type Scaling has no field or method Widths)`.

- [ ] **Step 3: Implement the fit** — in `sched/scaling.go`, add `"maps"` and `"slices"` imports, the constant and the fields, and replace `ScalingOf`:

```go
// The constants of scaling (job scaling spec §5.1, fit spec §3).
const (
	overrunRatio  = 1.25 // busy ÷ cores past which a command overruns
	minEfficiency = 0.5  // the efficiency the ideal width stays above
	growthBound   = 2    // the ideal width is at most this × the most cores a run had
	floorGain     = 1.25 // the speed over the busy rule's cores a width needs to floor a claim
)
```

Add to `Scaling`, after `Ideal`:

```go
	// Widths are the cores its runs had, ascending, when they had 2 widths
	// or more (fit spec §3); Fit is the parallel fraction fitted from their
	// median busy cores, which Parallel never falls below.
	Widths []int   `json:"widths,omitempty"`
	Fit    float64 `json:"fit,omitempty"`
```

```go
// ScalingOf is how b's command scales (job scaling spec §5.1, fit spec
// §4.1), a pure function of b (nil for none). It reads the done runs with
// CPU time among b's last window runs, leaving out a run whose claim had no
// cores (an exclusive one). The command overruns when the median of their
// busy cores ÷ cores is above overrunRatio. Otherwise each of them with 2
// cores or more gives Amdahl's parallel fraction, (1 − 1/busy) ÷ (1 −
// 1/cores) held to 0–1, and Parallel is their median; with 2 widths or more
// the fit (fitParallel) raises it when it is higher, since a run's own
// figure assumes 1 core would be kept busy. Ideal is the most cores at
// which the efficiency 1 ÷ (c(1 − p) + p) stays above minEfficiency, at
// least 1 and at most growthBound × the most cores any of these runs had.
func ScalingOf(b *Belief) Scaling {
	var ratios, busies, ps []float64
	byWidth := map[int][]float64{}
	most := 0
	for _, r := range b.recent() {
		c := r.Claim.CPUs
		if !r.teachesCores() || c < 1 {
			continue
		}
		busy := r.CPUSeconds / r.WallSeconds
		ratios = append(ratios, busy/float64(c))
		busies = append(busies, busy)
		byWidth[c] = append(byWidth[c], busy)
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
	}
	// Two widths include one of 2 cores or more, so Known is already true.
	if len(byWidth) >= 2 {
		s.Widths = slices.Sorted(maps.Keys(byWidth))
		s.Fit = fitParallel(s.Widths, byWidth)
		if s.Fit > s.Parallel {
			s.Parallel, s.Runs = s.Fit, len(busies)
		}
	}
	if s.Known {
		s.Ideal = idealWidth(s.Parallel, growthBound*most)
	}
	return s
}

// fitParallel is the parallel fraction fitted from the busy cores at each
// of widths (2 or more, ascending, each a key of busy): the median of
// pairParallel over every pair of widths, each at its median busy cores
// (fit spec §3).
func fitParallel(widths []int, busy map[int][]float64) float64 {
	at := make([]float64, len(widths))
	for i, w := range widths {
		at[i], _ = median(busy[w])
	}
	var ps []float64
	for i := range widths {
		for j := i + 1; j < len(widths); j++ {
			ps = append(ps, pairParallel(widths[i], at[i], widths[j], at[j]))
		}
	}
	p, _ := median(ps)
	return p
}

// pairParallel is the parallel fraction that takes a command keeping busyA
// cores busy at a cores to busyB at b (a < b, busyA above 0): with R =
// busyB ÷ busyA, 0 when R ≤ 1, else (R − 1) ÷ (R(1 − 1/b) − (1 − 1/a))
// held to 1. The work is the same at both widths, so R is their speedup.
func pairParallel(a int, busyA float64, b int, busyB float64) float64 {
	r := busyB / busyA
	if r <= 1 {
		return 0
	}
	return min((r-1)/(r*(1-1/float64(b))-(1-1/float64(a))), 1)
}
```

- [ ] **Step 4: Run `TestScalingOf` and the package**

Run: `loom job run -d "sched tests after the fit" -- sh -c 'CGO_ENABLED=0 go test ./sched'`
Expected: PASS. A failure elsewhere in `sched` (grants, durations, surprises over runs at two widths) is a fixture whose p the fit now raises: check the new p by hand with `pairParallel`, and fix the expectation only if the fitted value is the spec's.

- [ ] **Step 5: Write the failing `TestSize` cases** — append to `TestSize`'s `cases` in `sched/size_test.go`:

```go
		{"a command that waits keeps the width it gained at", Claim{}, belief(at(1, 0.31), at(2, 0.63)), szPolicy, Claim{CPUs: 2, MemoryGB: 2},
			ClaimSources{SourceLearned, SourceLearned, SourceDefault}, 2, false, 0},
		{"the floor is held to the default claim's cores", Claim{}, belief(at(1, 0.5), at(2, 0.95), at(8, 3.0)), szPolicy, Claim{CPUs: 2, MemoryGB: 2},
			ClaimSources{SourceLearned, SourceLearned, SourceDefault}, 3, false, 0},
		{"the floor is held to the fit's ideal width", Claim{}, belief(at(1, 0.5), at(4, 0.909)), szPolicy, Claim{CPUs: 1, MemoryGB: 2},
			ClaimSources{SourceLearned, SourceLearned, SourceDefault}, 2, false, 0},
		{"a width under the gain is no floor", Claim{}, belief(at(1, 0.5), at(2, 0.6)), szPolicy, Claim{CPUs: 1, MemoryGB: 2},
			ClaimSources{SourceLearned, SourceLearned, SourceDefault}, 2, false, 0},
		{"explicit cores take no floor", Claim{CPUs: 1}, belief(at(1, 0.31), at(2, 0.63)), szPolicy, Claim{CPUs: 1, MemoryGB: 2},
			ClaimSources{SourceExplicit, SourceLearned, SourceDefault}, 2, false, 0},
```

and to `TestSize_FromTheFamily`'s `cases`:

```go
		{"a family lends no floor", Claim{}, nil, belief(at(1, 0.31), at(2, 0.63)), szPolicy, Claim{CPUs: 1, MemoryGB: 2}, ClaimSources{F, F, D}, 0, 2, false},
```

Why each holds: the first has busy median 0.47 (1 core by the busy rule), fit 1, ideal 4, and 2 cores run twice as fast. The second fits 0.95 and would floor at 8 without the 4-core default. The third fits 0.60 (ideal 3), so its 4-core width, 1.8× faster, is past the ideal. The fourth fits 0.33, 1.2× faster at 2.

- [ ] **Step 6: Run them to see the floor cases fail**

Run: `loom job run -d "sched size floor tests (red)" -- sh -c 'CGO_ENABLED=0 go test ./sched -run TestSize'`
Expected: FAIL on "a command that waits keeps the width it gained at" (`CPUs: 1` where 2 was wanted) and on nothing else.

- [ ] **Step 7: Implement the floor** — in `sched/scaling.go`, after `scale`:

```go
// floor is the fewest cores the fit lets a job of this scaling claim when
// the busy rule gave cores (fit spec §3): the widest of Widths at most
// limit and at most the ideal width under Fit alone, at which Fit predicts
// a speed at least floorGain × the speed at cores. 0 when none is, and
// with no fit.
func (s Scaling) floor(cores, limit int) int {
	if len(s.Widths) < 2 || cores < 1 {
		return 0
	}
	fit := Scaling{Known: true, Parallel: s.Fit}
	ideal := idealWidth(s.Fit, growthBound*s.Widths[len(s.Widths)-1])
	for _, w := range slices.Backward(s.Widths) {
		if w <= limit && w <= ideal && fit.factor(cores)/fit.factor(w) >= floorGain {
			return w
		}
	}
	return 0
}
```

In `sched/size.go`, the `haveBusy` case becomes:

```go
	case haveBusy:
		// The busy rule, floored at the widest width the fit shows a job
		// that waits more than it computes runs faster at (fit spec §4.2).
		cores := clampInt(int(math.Ceil(coreHeadroom*busy)), 1, p.DefaultClaim.CPUs)
		s.Claim.CPUs, s.Sources.CPUs = max(cores, sc.floor(cores, p.DefaultClaim.CPUs)), SourceLearned
		learned = true
```

and `Size`'s doc comment's sentence "Cores learn from busy cores, never past the default claim's unless the command overruns (job scaling spec §5.2), then up to the cap;" becomes "Cores learn from busy cores, floored at the widest width the fit shows faster (fit spec §4.2), never past the default claim's unless the command overruns (job scaling spec §5.2), then up to the cap;".

- [ ] **Step 8: Run the package, vet and gofmt**

Run: `loom job run -d "sched tests, vet and gofmt after the floor" -- sh -c 'CGO_ENABLED=0 go test ./sched && CGO_ENABLED=0 go vet ./sched && test -z "$(gofmt -l sched)"'`
Expected: PASS, no output from gofmt.

- [ ] **Step 9: Commit**

```bash
git add sched/scaling.go sched/scaling_test.go sched/size.go sched/size_test.go
git commit -m "feat(sched): fit p from busy cores at two widths, and floor learned cores" -- sched/scaling.go sched/scaling_test.go sched/size.go sched/size_test.go
```

## Package 2: the scaling line and the docs

**Files:**
- Modify: `sched/belief_text.go:108-139` (`scalingLine`)
- Test: `sched/beliefs_view_test.go` (`TestFormatBelief_Scaling`)
- Modify: `sched/README.md` (the Size and Scaling bullets), `USAGE.md` (the Spare cores bullet)

- [ ] **Step 1: Write the failing line test** — append to `TestFormatBelief_Scaling`:

```go
	waits1, waits2 := at(1, 0.31), at(2, 0.63)
	waits1.WallSeconds, waits1.CPUSeconds = 80, 80*0.31
	waits2.WallSeconds, waits2.CPUSeconds = 40, 40*0.63
	assert.Equal(t, "scaling   100% parallel from runs at 1 and 2 cores · efficient up to 4 cores · ~40s at 2, ~20s at 4", line(belief(waits1, waits2)))
```

(At p = 1 the 80 s run at 1 core is 40 s at 2 and 20 s at 4, and the 40 s run at 2 is 20 s at 4; the floor makes the claim 2 cores.)

- [ ] **Step 2: Run it to see it fail**

Run: `loom job run -d "sched scaling line test (red)" -- sh -c 'CGO_ENABLED=0 go test ./sched -run TestFormatBelief_Scaling'`
Expected: FAIL, the line reading `100% parallel over 2 runs`.

- [ ] **Step 3: Implement** — in `scalingLine`, replace the `runs`/`line` lines:

```go
	from := "over 1 run"
	if s.Runs != 1 {
		from = fmt.Sprintf("over %d runs", s.Runs)
	}
	if s.fitted() {
		widths := make([]string, len(s.Widths))
		for i, w := range s.Widths {
			widths[i] = strconv.Itoa(w)
		}
		from = "from runs at " + JoinAnd(widths) + " cores"
	}
	line := fmt.Sprintf("scaling   %.0f%% parallel %s · efficient up to %d cores", 100*s.Parallel, from, s.Ideal)
```

with `"strconv"` imported, the doc comment's "or p, the runs it came from," becoming "or p, the runs or widths it came from,", and in `sched/scaling.go`:

```go
// fitted reports whether Parallel came from the fit (fit spec §5).
func (s Scaling) fitted() bool { return len(s.Widths) >= 2 && s.Parallel == s.Fit }
```

- [ ] **Step 4: Docs**
  - `sched/README.md`, the Size bullet: after "cores are 1.25 × the median busy cores, at most the default claim's" add "and at least the widest width the job ran at that the fit shows 1.25× faster, within the fit's ideal width".
  - `sched/README.md`, the Scaling bullet: after "the median)" add "; with runs at 2 widths or more, the fit (the median over pairs of widths of the p that takes one width's median busy cores to the other's) raises it when higher ([fit spec](../docs/superpowers/specs/2026-11-07-job-scaling-fit-design.md))".
  - `USAGE.md`, the Spare cores bullet: after "(at most twice the most cores it has had)." add "When a command has run at two widths, the scheduler compares its busy cores at each, so a command that waits more than it computes (a test suite) is seen to scale too, and its claim never shrinks below a width it ran at least 1.25× faster at."

- [ ] **Step 5: Run every gate**

Run: `loom job submit -d "full suite, vet, gofmt and the claudemd lint" -- sh -c 'CGO_ENABLED=0 go vet ./... && test -z "$(gofmt -l $(git ls-files "*.go" | grep -v ^vendor/))" && CGO_ENABLED=0 go test ./...'` then `loom job submit -d "race" --after N -- sh -c 'CC=clang CGO_ENABLED=1 go test -race ./sched ./core ./internal/cli'`
Expected: both PASS.

- [ ] **Step 6: Commit**

```bash
git commit -m "feat(sched): the scaling line names the widths the fit came from" -- sched/belief_text.go sched/beliefs_view_test.go sched/scaling.go sched/README.md USAGE.md
```

## Self-review notes

- Spec §4.1 → Package 1 steps 1–4; §4.2 → steps 5–8; §4.3 needs no code; §5 → Package 2; §7's tests are all present (the prior case is covered by "a prior", whose belief has no runs and so no widths).
- `slices.Backward` and `maps.Keys` need Go 1.23; `go.mod` says 1.25.8.

# Past-peak holds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A running job reserves less once its own past runs show its peaks are behind it, so the queue admits other jobs sooner without new memory risk.

**Architecture:** Pure `sched` code: a `Trace` of samples per running job, a `Profile` (two falling staircases over CPU-seconds) built from a done run's trace and kept on the run in `beliefs.json`, and `Hold`, which turns a job's trace, its belief and its last state into what it counts at now. `Admit`, `Grant` and `Forecast` take the holds as a map. `core` appends to the trace at each sample, refreshes the holds after each sample and before each admission pass, breaks a hold whose usage passes it, and builds the profile at settle. The work view and `loom job list` show a hold below its claim.

**Tech Stack:** Go (module `github.com/aidan-bailey/loom`), testify, cobra, Bubble Tea v2 / lipgloss v2.

**Spec:** [`docs/superpowers/specs/2026-11-05-past-peak-holds-design.md`](../specs/2026-11-05-past-peak-holds-design.md). Execution: inline (superpowers:executing-plans), no checkpoint pauses; one commit per package.

---

## Ground rules for every package

- Scratch files go in the session scratchpad, never the worktree. Commit by path: `git add <paths>`, then `git commit -m … -- <paths>`; never `-a`, `git add -A` or `git add .`.
- Quick checks run directly: `CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 go vet ./<pkg>/...`, one package's tests (`CGO_ENABLED=0 go test ./sched/ -run TestX`). The full suite, the race run and e2e go through loom's work queue (`loom job run -d "past-peak holds: …" -- env CGO_ENABLED=0 go test ./...`, `… -- env CC=clang CGO_ENABLED=1 go test -race ./...`, `… -- env CGO_ENABLED=0 go test -tags e2e ./e2e/...`), with Bash's `run_in_background`.
- Format with `gofmt -w` on the files touched, check `gofmt -l sched core internal ui config`.
- `core/CLAUDE.md` is full (19971/20000 bytes): the model's rules go in `docs/claude/the-work-queue.md`. `sched/CLAUDE.md` has room (13608/20000).
- golangci-lint: the dev shell's v2 rejects the config; run `CGO_ENABLED=0 go vet ./...` and say lint was skipped.

## Spec amendments made while planning

Recorded in the spec (new §13) in Step 1.1:

1. **`Hold` returns no reason.** It returns the state and the need; the tests pin each rule by its outcome on both sides. A reason no reader shows is dead code.
2. **Rule 5 compares the last 2 points with the envelope now, `E(x)`,** not with the envelope at each point's own `x`. Compared with their own, a point from before the drop passes trivially and the dwell is no dwell.
3. **Holds are refreshed after every sample (`deliverQueuePass`) and at every admission pass (`admitJobs`),** so the view follows a hold while nothing waits.
4. **A step is written as the pair `[until, level]`, `until` rounded up to 0.1 CPU-second** (later, so never lower), and so is a peak's and the end's progress.
5. **`loom job list` only:** there is no `loom job show`. The hold text is `Need.HoldText` (`holds 1c/2G`), shared by the work view and the CLI, as `Claim.Spared` is.
6. **The header shows `held` only while the held total is above 0 and below the claimed one,** so a view from a build without holds never reads `0/14 held`.
7. **An invalid profile is logged once at load (`loadBeliefs`) per signature.** Profiles made at run time are valid by construction.
8. **The core tests deliver synthetic samples through the pass** (`m.Deliver(queuePassResult{…})`, as `TestJobQueue_UsageFollowsTheSamplesAPassDelivers` does) to a real shim (`wqLaunch`) or a seeded record, so a trace is exact; the job a release admits is a real submit.

## Files

| File | Package | Responsibility |
|---|---|---|
| `sched/profile.go` (new) | 1 | `TracePoint`, `Trace` (`Add`, `Fresh`), `Step` (pair JSON), `Staircase` (`Peak`, `At`), `Profile`, `BuildProfile`, `Profile.Validate`, `ProfiledRuns` |
| `sched/profile_test.go` (new) | 1 | the trace, the build (exact and over random traces), validation, the JSON |
| `sched/hold.go` (new) | 1 | `HoldState` (`Exceeded`), `Hold`, the two resources, `profiledRuns` |
| `sched/hold_test.go` (new) | 1 | every rule on both sides |
| `sched/admit.go`, `grant.go`, `forecast.go` | 1 | the `holds` argument, `runningNeed` |
| `sched/belief.go` | 1 | `Run.Profile`, `Run.HoldBroke`; `AddRun` strips old profiles; `RunOf` copies `HoldBroke` |
| `sched/beliefs_view.go` | 1 | `Belief.Clone` copies profiles |
| `sched/job.go` | 1 | `Record.HoldBroke`, `Policy.ReleasePastPeaks`, `Need.HoldText` |
| `sched/policy.go`, `config/settings.go` | 1 | the switch |
| `sched/*_test.go` | 1 | callers of `Admit`, `Grant`, `Forecast`; the policy's defaults |
| `core/queue.go` | 2 | `traces`, `holds`, `holding` |
| `core/queue_watch.go` | 2 | the trace, the break, `refreshHolds`, the settle |
| `core/queue_admit.go` | 2 | holds into `Admit` and `Grant`; `queue.job_started` |
| `core/queue_beliefs.go` | 2 | the profile at `recordRun`; invalid profiles at load |
| `core/queue_holds.go` (new) | 2 | `refreshHolds`, `breakHold` |
| `core/queue_view.go` | 2 | `JobView.Holds`, `QueueView.Held`, the forecast's holds, the switch's clone |
| `core/queue_holds_test.go` (new) | 2 | the model's tests |
| `docs/specs/protocol.md` | 2 | regenerated |
| `ui/work.go`, `ui/work_test.go` | 3 | the row and the header |
| `internal/cli/job_sizing.go`, its test | 3 | `list`'s claim text |
| `sched/README.md`, `sched/CLAUDE.md`, `docs/claude/the-work-queue.md`, `USAGE.md` | 3 | docs |

## Package 1: `sched`

### Step 1.1: Amend the spec

- [ ] Add to `docs/superpowers/specs/2026-11-05-past-peak-holds-design.md` a §13 "Planning amendments" with the eight amendments above; in §5.3 rule 5 read "at or below `E(x)`, the envelope now"; drop "and why" from §5.3's first paragraph; §7's CLI bullet reads "`loom job list` adds…"; §11's core bullets say the samples are delivered through the pass.

### Step 1.2: The trace and the profile

**Files:** create `sched/profile.go`, `sched/profile_test.go`.

- [ ] **Write the tests** (`sched/profile_test.go`):

```go
package sched

import (
	"encoding/json"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tracePts is a complete trace of points one second apart from t0.
func tracePts(t0 time.Time, pts ...TracePoint) *Trace {
	tr := &Trace{}
	for i, p := range pts {
		tr.Add(t0.Add(time.Duration(i)*time.Second), p)
	}
	return tr
}

func TestTrace_Add(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	tr := tracePts(t0, TracePoint{X: 5, RSSKB: 10}, TracePoint{X: 3, RSSKB: 20})
	assert.Equal(t, 5.0, tr.Points[1].X, "progress never falls")
	assert.False(t, tr.Incomplete)
	assert.True(t, tr.Fresh(t0.Add(31*time.Second)), "30 s after its newest point it is fresh")
	assert.False(t, tr.Fresh(t0.Add(32*time.Second)))
	assert.False(t, (*Trace)(nil).Fresh(t0), "no trace is never fresh")

	tr.Add(t0.Add(32*time.Second), TracePoint{X: 6})
	assert.True(t, tr.Incomplete, "a gap of 31 s between points")

	full := &Trace{}
	for i := range tracePoints + 1 {
		full.Add(t0.Add(time.Duration(i)*time.Second), TracePoint{X: float64(i), RSSKB: int64(i % 7), Cores: float64(i % 5)})
	}
	require.Len(t, full.Points, (tracePoints+2)/2, "past 256 points, pairs merge")
	assert.Equal(t, TracePoint{X: 1, RSSKB: 1, Cores: 1}, full.Points[0], "the later progress, the higher usage")
	var rss int64
	var cores float64
	for _, p := range full.Points {
		rss, cores = max(rss, p.RSSKB), max(cores, p.Cores)
	}
	assert.Equal(t, int64(6), rss, "every peak stays")
	assert.Equal(t, 4.0, cores)
	assert.Equal(t, float64(tracePoints), full.Points[len(full.Points)-1].X, "the odd point out stays as it was")
}

func TestBuildProfile(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	tr := tracePts(t0,
		TracePoint{X: 1, RSSKB: 3000, Cores: 4},
		TracePoint{X: 10.04, RSSKB: 6000, Cores: 4},
		TracePoint{X: 10.04, RSSKB: 2000, Cores: 1},
		TracePoint{X: 20, RSSKB: 1000, Cores: 1},
		TracePoint{X: 30, RSSKB: 1000, Cores: 0.5},
	)
	p, ok := BuildProfile(tr)
	require.True(t, ok)
	assert.Equal(t, 30.0, p.End)
	assert.Equal(t, Staircase{PeakAt: 10.1, Steps: []Step{{Until: 10.1, Level: 6144}, {Until: 30, Level: 1024}}}, p.Mem,
		"suffix maxima, rounded up to whole MB, one step per progress and per level")
	assert.Equal(t, Staircase{PeakAt: 1, Steps: []Step{{Until: 10.1, Level: 4000}, {Until: 20, Level: 1000}, {Until: 30, Level: 500}}}, p.Cores)
	require.NoError(t, p.Validate())

	for name, tr := range map[string]*Trace{"none": nil, "empty": {}} {
		_, ok := BuildProfile(tr)
		assert.False(t, ok, name)
	}
	gap := tracePts(t0, TracePoint{X: 1})
	gap.Add(t0.Add(time.Minute), TracePoint{X: 2})
	_, ok = BuildProfile(gap)
	assert.False(t, ok, "an incomplete trace")
}

// A compressed staircase is never below the suffix maximum at any progress.
func TestBuildProfile_NeverBelowTheRun(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for range 200 {
		tr := &Trace{}
		x := 0.0
		for i := range 1 + r.IntN(300) {
			x += r.Float64() * 3
			tr.Add(t0.Add(time.Duration(i)*time.Second), TracePoint{X: x, RSSKB: r.Int64N(1 << 24), Cores: r.Float64() * 16})
		}
		p, ok := BuildProfile(tr)
		require.True(t, ok)
		require.NoError(t, p.Validate())
		require.LessOrEqual(t, len(p.Mem.Steps), profileSteps)
		require.LessOrEqual(t, len(p.Cores.Steps), profileSteps)
		for i, pt := range tr.Points {
			var rss int64
			var cores float64
			for _, later := range tr.Points[i:] {
				rss, cores = max(rss, later.RSSKB), max(cores, later.Cores)
			}
			// Just below the point's progress, its own usage is still to come.
			at := max(0, pt.X-1e-9)
			m, ok := p.Mem.At(at)
			require.True(t, ok)
			require.GreaterOrEqual(t, m, rss)
			c, ok := p.Cores.At(at)
			require.True(t, ok)
			require.GreaterOrEqual(t, float64(c), cores*1000)
		}
	}
}

func TestStaircase_At(t *testing.T) {
	s := Staircase{Steps: []Step{{Until: 10, Level: 5}, {Until: 20, Level: 2}}}
	for _, c := range []struct {
		x    float64
		want int64
		ok   bool
	}{{0, 5, true}, {9.9, 5, true}, {10, 2, true}, {19.9, 2, true}, {20, 0, false}} {
		got, ok := s.At(c.x)
		assert.Equal(t, c.want, got, "at %v", c.x)
		assert.Equal(t, c.ok, ok, "at %v", c.x)
	}
	assert.Equal(t, int64(5), s.Peak())
	assert.Zero(t, Staircase{}.Peak())
}

func TestProfile_Validate(t *testing.T) {
	good := func() *Profile {
		return &Profile{End: 20,
			Mem:   Staircase{PeakAt: 5, Steps: []Step{{Until: 10, Level: 5}, {Until: 20, Level: 2}}},
			Cores: Staircase{PeakAt: 0, Steps: []Step{{Until: 20, Level: 1000}}}}
	}
	require.NoError(t, good().Validate())
	for name, spoil := range map[string]func(p *Profile){
		"no steps":           func(p *Profile) { p.Cores.Steps = nil },
		"too many steps":     func(p *Profile) { p.Cores.Steps = make([]Step, profileSteps+1) },
		"a negative level":   func(p *Profile) { p.Mem.Steps[1].Level = -1 },
		"a negative until":   func(p *Profile) { p.Mem.Steps[0].Until = -1 },
		"steps out of order": func(p *Profile) { p.Mem.Steps[1].Until = 10 },
		"a rising level":     func(p *Profile) { p.Mem.Steps[1].Level = 6 },
		"a peak past step 1": func(p *Profile) { p.Mem.PeakAt = 11 },
		"a negative peak":    func(p *Profile) { p.Mem.PeakAt = -1 },
		"an end off the last": func(p *Profile) { p.End = 21 },
	} {
		p := good()
		spoil(p)
		assert.Error(t, p.Validate(), name)
	}
}

func TestProfile_JSON(t *testing.T) {
	p := Profile{End: 30, Mem: Staircase{PeakAt: 10.1, Steps: []Step{{Until: 10.1, Level: 6144}, {Until: 30, Level: 1024}}},
		Cores: Staircase{PeakAt: 1, Steps: []Step{{Until: 30, Level: 4000}}}}
	b, err := json.Marshal(p)
	require.NoError(t, err)
	assert.JSONEq(t, `{"end":30,"mem":{"peak_at":10.1,"steps":[[10.1,6144],[30,1024]]},"cores":{"peak_at":1,"steps":[[30,4000]]}}`, string(b))
	var back Profile
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, p, back)
	assert.Error(t, json.Unmarshal([]byte(`{"mem":{"steps":["x"]}}`), &back))
}
```

- [ ] **Run them:** `CGO_ENABLED=0 go test ./sched/ -run 'TestTrace_|TestBuildProfile|TestStaircase_|TestProfile_'` — FAIL (undefined names).

- [ ] **Write `sched/profile.go`:**

```go
package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// The trace's and the profile's bounds (past-peak holds spec §5.1, §5.2).
const (
	tracePoints  = 256              // a trace halves past this many points
	traceGap     = 30 * time.Second // a longer gap leaves a trace incomplete, and a newest point older than this is stale
	profileSteps = 8                // a staircase's most steps
	ProfiledRuns = 5                // the newest runs of a belief that keep their profiles
)

// TracePoint is one sample of a running job (past-peak holds spec §5.1):
// its progress (X, the CPU-seconds its session has used), its memory, and
// the cores it kept busy since the sample before.
type TracePoint struct {
	X     float64
	RSSKB int64
	Cores float64
}

// Trace is a running job's samples. The model keeps it in memory only.
type Trace struct {
	Points     []TracePoint
	Last       time.Time // when the newest point was taken
	Incomplete bool      // two points were more than traceGap apart
}

// Add appends p, sampled at at. Its progress never falls below the point
// before (a process that left the session took its CPU time with it); a gap
// over traceGap since the last point leaves the trace incomplete; past
// tracePoints points, adjacent pairs merge into one with the later progress
// and the higher memory and cores, so every peak stays.
func (t *Trace) Add(at time.Time, p TracePoint) {
	if n := len(t.Points); n > 0 {
		if at.Sub(t.Last) > traceGap {
			t.Incomplete = true
		}
		p.X = max(p.X, t.Points[n-1].X)
	}
	t.Points = append(t.Points, p)
	t.Last = at
	if len(t.Points) <= tracePoints {
		return
	}
	merged := t.Points[:0]
	for i := 0; i < len(t.Points); i += 2 {
		a := t.Points[i]
		if i+1 < len(t.Points) {
			b := t.Points[i+1]
			a = TracePoint{X: b.X, RSSKB: max(a.RSSKB, b.RSSKB), Cores: max(a.Cores, b.Cores)}
		}
		merged = append(merged, a)
	}
	t.Points = merged
}

// Fresh reports whether t has a point taken within traceGap of now.
func (t *Trace) Fresh(now time.Time) bool {
	return t != nil && len(t.Points) > 0 && now.Sub(t.Last) <= traceGap
}

// x is t's progress: its newest point's.
func (t *Trace) x() float64 { return t.Points[len(t.Points)-1].X }

// Step is one step of a staircase: Level holds for progress below Until. It
// is written as the pair [until, level].
type Step struct {
	Until float64
	Level int64
}

// MarshalJSON writes s as [until, level].
func (s Step) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]float64{s.Until, float64(s.Level)})
}

// UnmarshalJSON reads [until, level].
func (s *Step) UnmarshalJSON(b []byte) error {
	var pair [2]float64
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	s.Until, s.Level = pair[0], int64(pair[1])
	return nil
}

// Staircase is one resource of a profile (spec §5.2): the most a run still
// used from each point of its progress to its end. Its levels fall, so it is
// held as at most profileSteps steps. PeakAt is the progress at which the run
// first reached its peak.
type Staircase struct {
	PeakAt float64 `json:"peak_at"`
	Steps  []Step  `json:"steps"`
}

// Peak is the run's peak: the first step's level, 0 for no steps.
func (s Staircase) Peak() int64 {
	if len(s.Steps) == 0 {
		return 0
	}
	return s.Steps[0].Level
}

// At is the most the run used from progress x to its end, and false once x
// is at or past its end.
func (s Staircase) At(x float64) (int64, bool) {
	for _, st := range s.Steps {
		if x < st.Until {
			return st.Level, true
		}
	}
	return 0, false
}

// Profile is a done run's usage over its progress (spec §5.2): memory in KB,
// cores in milli-cores, and the progress at its end.
type Profile struct {
	End   float64   `json:"end"`
	Mem   Staircase `json:"mem"`
	Cores Staircase `json:"cores"`
}

// BuildProfile is t's profile, and false for no trace, an empty one or an
// incomplete one (spec §5.2).
func BuildProfile(t *Trace) (*Profile, bool) {
	if t == nil || len(t.Points) == 0 || t.Incomplete {
		return nil, false
	}
	return &Profile{
		End:   roundUpX(t.x()),
		Mem:   buildStaircase(t.Points, func(p TracePoint) int64 { return (p.RSSKB + 1023) / 1024 * 1024 }),
		Cores: buildStaircase(t.Points, func(p TracePoint) int64 { return milliCores(p.Cores) }),
	}, true
}

// milliCores is cores in whole milli-cores, rounded up.
func milliCores(cores float64) int64 { return int64(math.Ceil(cores * 1000)) }

// roundUpX is progress x rounded up to a tenth of a CPU-second.
func roundUpX(x float64) float64 { return math.Ceil(x*10) / 10 }

// buildStaircase is the suffix maximum of level over points, one step per
// progress (the first, highest, of equal ones) and per level (the last
// progress of equal ones), merged down to profileSteps by joining, each time,
// the adjacent pair whose merge adds the least area; a merge keeps the higher
// level and the later Until, so the staircase is never below the run.
func buildStaircase(points []TracePoint, level func(TracePoint) int64) Staircase {
	var s Staircase
	peak := int64(-1)
	for _, p := range points {
		if v := level(p); v > peak {
			peak, s.PeakAt = v, roundUpX(p.X)
		}
	}
	raw := make([]Step, len(points))
	most := int64(0)
	for i := len(points) - 1; i >= 0; i-- {
		most = max(most, level(points[i]))
		raw[i] = Step{Until: roundUpX(points[i].X), Level: most}
	}
	for _, st := range raw {
		k := len(s.Steps) - 1
		switch {
		case k >= 0 && s.Steps[k].Until == st.Until:
			// The same progress: the first, highest, level holds.
		case k >= 0 && s.Steps[k].Level == st.Level:
			s.Steps[k].Until = st.Until
		default:
			s.Steps = append(s.Steps, st)
		}
	}
	for len(s.Steps) > profileSteps {
		best, cost := 0, math.Inf(1)
		for i := 0; i+1 < len(s.Steps); i++ {
			c := float64(s.Steps[i].Level-s.Steps[i+1].Level) * (s.Steps[i+1].Until - s.Steps[i].Until)
			if c < cost {
				best, cost = i, c
			}
		}
		s.Steps[best].Until = s.Steps[best+1].Until
		s.Steps = slices.Delete(s.Steps, best+1, best+2)
	}
	return s
}

// Validate says why p can't be trusted, nil when it can: each staircase has
// 1 to profileSteps steps whose progress rises and whose levels fall, nothing
// negative, a peak within its first step, and a last step ending at End.
func (p *Profile) Validate() error {
	if err := p.Mem.validate(p.End); err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	if err := p.Cores.validate(p.End); err != nil {
		return fmt.Errorf("cores: %w", err)
	}
	return nil
}

func (s Staircase) validate(end float64) error {
	switch {
	case len(s.Steps) == 0:
		return errors.New("no steps")
	case len(s.Steps) > profileSteps:
		return fmt.Errorf("%d steps, over %d", len(s.Steps), profileSteps)
	}
	for i, st := range s.Steps {
		switch {
		case st.Level < 0 || !(st.Until >= 0):
			return fmt.Errorf("step %d is negative", i+1)
		case i > 0 && st.Until <= s.Steps[i-1].Until:
			return fmt.Errorf("step %d does not move on", i+1)
		case i > 0 && st.Level > s.Steps[i-1].Level:
			return fmt.Errorf("step %d rises", i+1)
		}
	}
	switch {
	case !(s.PeakAt >= 0) || s.PeakAt > s.Steps[0].Until:
		return errors.New("its peak is not in its first step")
	case s.Steps[len(s.Steps)-1].Until != end:
		return errors.New("its last step does not end at the run's end")
	}
	return nil
}
```

- [ ] **Run the tests:** PASS.

### Step 1.3: Runs, records, the policy and the switch

**Files:** modify `sched/belief.go`, `sched/beliefs_view.go`, `sched/job.go`, `sched/policy.go`, `config/settings.go`; tests in `sched/belief_test.go`, `sched/policy_test.go`.

- [ ] **Tests first.** In `sched/belief_test.go` add:

```go
func TestBelief_AddRunKeepsTheNewestProfiles(t *testing.T) {
	var b Belief
	for i := range ProfiledRuns + 2 {
		b.AddRun(Run{Job: JobID(i + 1), Profile: &Profile{End: float64(i)}})
	}
	for i, r := range b.Runs {
		assert.Equal(t, i < ProfiledRuns, r.Profile != nil, "run %d", r.Job)
	}
}

func TestRunOf_CopiesABrokenHold(t *testing.T) {
	rec := Record{ID: 1, State: Done, Started: time.Unix(10, 0), Ended: time.Unix(20, 0), HoldBroke: true}
	run, ok := RunOf(rec)
	require.True(t, ok)
	assert.True(t, run.HoldBroke)
}

func TestBelief_CloneCopiesProfiles(t *testing.T) {
	b := Belief{Runs: []Run{{Profile: &Profile{End: 1, Mem: Staircase{Steps: []Step{{Until: 1, Level: 2}}}}}}}
	c := b.Clone()
	c.Runs[0].Profile.Mem.Steps[0].Level = 9
	assert.Equal(t, int64(2), b.Runs[0].Profile.Mem.Steps[0].Level)
}
```

In `sched/policy_test.go` add:

```go
func TestResolve_ReleasePastPeaks(t *testing.T) {
	h := Host{Cores: 16, RAMBytes: 32 << 30}
	p, err := Resolve(config.SchedulerSettings{}, h)
	require.NoError(t, err)
	assert.True(t, p.ReleasePastPeaks, "on when absent")
	off := false
	p, err = Resolve(config.SchedulerSettings{ReleasePastPeaks: &off}, h)
	require.NoError(t, err)
	assert.False(t, p.ReleasePastPeaks)
}
```

- [ ] **Run:** FAIL (undefined fields).

- [ ] **`sched/belief.go`:** `Run` gains

```go
	Profile   *Profile `json:"profile,omitempty"`    // its usage over its progress (past-peak holds spec §5.2); only the newest ProfiledRuns keep one
	HoldBroke bool     `json:"hold_broke,omitempty"` // its usage passed its lowered hold (spec §5.4)
```

`RunOf` sets `HoldBroke: rec.HoldBroke` in the `Run` literal. `AddRun` becomes:

```go
// AddRun records r as b's newest run, keeping MaxRuns, and profiles on the
// newest ProfiledRuns only.
func (b *Belief) AddRun(r Run) {
	b.Runs = slices.Insert(b.Runs, 0, r)
	if len(b.Runs) > MaxRuns {
		b.Runs = b.Runs[:MaxRuns]
	}
	for i := ProfiledRuns; i < len(b.Runs); i++ {
		b.Runs[i].Profile = nil
	}
}
```

- [ ] **`sched/beliefs_view.go`:** `Belief.Clone`'s run loop also copies the profile:

```go
		if p := b.Runs[i].Profile; p != nil {
			c := *p
			c.Mem.Steps, c.Cores.Steps = slices.Clone(p.Mem.Steps), slices.Clone(p.Cores.Steps)
			b.Runs[i].Profile = &c
		}
```

- [ ] **`sched/job.go`:** `Record` gains, after `Ready`:

```go
	// Past-peak holds (spec 2026-11-05-past-peak-holds §5.4): its usage
	// passed its lowered hold, so it held its claim from then on.
	HoldBroke bool `json:"hold_broke,omitempty"`
```

`Policy` gains `ReleasePastPeaks bool `json:"release_past_peaks"`` (a running job's hold may fall below its claim, spec §8). Add after `Claim.Need`:

```go
// HoldText is n as the hold a running job counts at: "holds 1c/2G".
func (n Need) HoldText() string { return fmt.Sprintf("holds %dc/%dG", n.CPUs, n.MemoryGB) }
```

- [ ] **`config/settings.go`:** `SchedulerSettings` gains `ReleasePastPeaks *bool `json:"release_past_peaks,omitempty"`` with the comment "lets a running job's hold fall below its claim once it is past its peaks; absent means true".

- [ ] **`sched/policy.go`:** the returned `Policy` sets `ReleasePastPeaks: s.ReleasePastPeaks == nil || *s.ReleasePastPeaks`; the doc comment adds "release_past_peaks is true".

- [ ] **Run** `CGO_ENABLED=0 go test ./sched/ ./config/`: the new tests pass; fix any table that compares a whole `Policy` from `Resolve` (`TestResolve_Defaults`) by adding `ReleasePastPeaks: true`.

### Step 1.4: `Hold`

**Files:** create `sched/hold.go`, `sched/hold_test.go`.

- [ ] **Write the tests** (`sched/hold_test.go`):

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// holdProfile is a run that peaked at 6 GB and 4 cores before progress 10,
// then used 1 GB and 1 core to its end at 100.
func holdProfile() *Profile {
	return &Profile{End: 100,
		Mem:   Staircase{PeakAt: 10, Steps: []Step{{Until: 10, Level: 6 << 20}, {Until: 100, Level: 1 << 20}}},
		Cores: Staircase{PeakAt: 5, Steps: []Step{{Until: 10, Level: 4000}, {Until: 100, Level: 1000}}}}
}

// holdFixture is a job of claim 4c/8G io, its belief with three profiled
// runs, a trace that peaked and has tracked the profile since, and a policy
// that releases: every rule passes, and the hold is 2c/2G.
type holdFixture struct {
	rec   Record
	b     *Belief
	t     *Trace
	fresh bool
	prev  HoldState
	p     Policy
}

func newHoldFixture() *holdFixture {
	b := &Belief{}
	for range 3 {
		b.AddRun(Run{Outcome: OutcomeDone, Profile: holdProfile()})
	}
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	return &holdFixture{
		rec: Record{ID: 1, State: Running, Claim: Claim{CPUs: 4, MemoryGB: 8, IO: true},
			Sources: ClaimSources{CPUs: SourceLearned, Memory: SourceLearned}},
		b: b,
		t: tracePts(t0,
			TracePoint{X: 2, RSSKB: 5 << 20, Cores: 0},
			TracePoint{X: 8, RSSKB: 6 << 20, Cores: 3.6},
			TracePoint{X: 11, RSSKB: 900 << 10, Cores: 0.9},
			TracePoint{X: 13, RSSKB: 800 << 10, Cores: 0.8}),
		fresh: true,
		p:     Policy{Caps: Caps{CPUs: 14, MemoryGB: 24, IOJobs: 2}, ReleasePastPeaks: true},
	}
}

func (f *holdFixture) hold() (HoldState, Need) { return Hold(f.rec, f.b, f.t, f.fresh, f.prev, f.p) }

var (
	claimNeed    = Need{CPUs: 4, MemoryGB: 8, IOSlots: 1}
	releasedNeed = Need{CPUs: 2, MemoryGB: 2, IOSlots: 1}
)

func TestHold_ReleasesAJobPastItsPeaks(t *testing.T) {
	state, need := newHoldFixture().hold()
	assert.Equal(t, HoldState{CPUs: 2, MemoryGB: 2}, state, "1 GB + 15% is 2 GB, 1 core + 25% is 2")
	assert.Equal(t, releasedNeed, need, "the I/O slot is held to the end")
}

// Each rule, just failing: the job holds its claim.
func TestHold_EachRuleHoldsTheClaim(t *testing.T) {
	for name, spoil := range map[string]func(f *holdFixture){
		"1: two profiled runs":        func(f *holdFixture) { f.b.Runs = f.b.Runs[:2] },
		"1: an invalid profile":       func(f *holdFixture) { f.b.Runs[0].Profile = &Profile{End: 1} },
		"1: no belief":                func(f *holdFixture) { f.b = nil },
		"1: a recent broken hold":     func(f *holdFixture) { f.b.Runs[2].HoldBroke = true },
		"2: adopted":                  func(f *holdFixture) { f.rec.Adopted = true },
		"2: no trace":                 func(f *holdFixture) { f.t = nil },
		"2: an incomplete trace":      func(f *holdFixture) { f.t.Incomplete = true },
		"2: a stale sample":           func(f *holdFixture) { f.fresh = false },
		"2: switched off":             func(f *holdFixture) { f.p.ReleasePastPeaks = false },
		"2: exclusive":                func(f *holdFixture) { f.rec.Claim.Exclusive = true },
		"2: an explicit claim":        func(f *holdFixture) { f.rec.Sources = ClaimSources{CPUs: SourceExplicit, Memory: SourceExplicit} },
		"2: this job broke its hold":  func(f *holdFixture) { f.rec.HoldBroke = true },
		"4: outlasting every run":     func(f *holdFixture) { f.t.Points[3].X = 100 },
	} {
		f := newHoldFixture()
		spoil(f)
		_, need := f.hold()
		want := claimNeed
		if f.rec.Claim.Exclusive {
			want = f.rec.Claim.Need(f.p.Caps)
		}
		assert.Equal(t, want, need, name)
	}
}

// Rules 2's explicit claim and 1's history count only in full.
func TestHold_TheOtherSideOfEachRule(t *testing.T) {
	for name, adjust := range map[string]func(f *holdFixture){
		"explicit cores alone": func(f *holdFixture) { f.rec.Sources.CPUs = SourceExplicit },
		"explicit memory alone": func(f *holdFixture) { f.rec.Sources.Memory = SourceExplicit },
		"a broken hold 11 runs back": func(f *holdFixture) {
			f.b.Runs[2].HoldBroke = true
			for range window - 2 {
				f.b.AddRun(Run{Outcome: OutcomeDone, Profile: holdProfile()})
			}
		},
		"an older run with no profile": func(f *holdFixture) { f.b.Runs = append(f.b.Runs, Run{Outcome: OutcomeDone}) },
		"a shorter run drops out": func(f *holdFixture) {
			short := holdProfile()
			short.End, short.Mem.Steps[1].Until, short.Cores.Steps[1].Until = 12, 12, 12
			f.b.AddRun(Run{Outcome: OutcomeDone, Profile: short})
		},
	} {
		f := newHoldFixture()
		adjust(f)
		_, need := f.hold()
		assert.Equal(t, releasedNeed, need, name)
	}
}

// Rules 3 and 5 decide each resource on its own.
func TestHold_PerResourceRules(t *testing.T) {
	cases := map[string]struct {
		spoil func(f *holdFixture)
		want  Need
	}{
		"3: memory never reached 90% of the lowest peak": {func(f *holdFixture) { f.t.Points[1].RSSKB = 5 << 20 }, Need{CPUs: 2, MemoryGB: 8, IOSlots: 1}},
		"3: memory at 90% is enough":                     {func(f *holdFixture) { f.t.Points[1].RSSKB = 6<<20*9/10 + 1 }, releasedNeed},
		"3: not past a run's memory peak": {func(f *holdFixture) {
			late := holdProfile()
			late.Mem.PeakAt, late.Mem.Steps[0].Until = 13, 14
			f.b.AddRun(Run{Outcome: OutcomeDone, Profile: late})
		}, Need{CPUs: 2, MemoryGB: 8, IOSlots: 1}},
		"5: memory above the envelope one point back": {func(f *holdFixture) { f.t.Points[2].RSSKB = 1<<20 + 1 }, Need{CPUs: 2, MemoryGB: 8, IOSlots: 1}},
		"5: memory at the envelope tracks":            {func(f *holdFixture) { f.t.Points[2].RSSKB = 1 << 20 }, releasedNeed},
		"5: cores above the envelope now":             {func(f *holdFixture) { f.t.Points[3].Cores = 1.01 }, Need{CPUs: 4, MemoryGB: 2, IOSlots: 1}},
		"a hold that allows no less than the claim": {func(f *holdFixture) { f.rec.Claim = Claim{CPUs: 2, MemoryGB: 2} }, Need{CPUs: 2, MemoryGB: 2}},
	}
	for name, c := range cases {
		f := newHoldFixture()
		c.spoil(f)
		state, need := f.hold()
		assert.Equal(t, c.want, need, name)
		assert.Equal(t, need.CPUs < f.rec.Claim.CPUs, state.CPUs > 0, name)
	}
}

// A released hold only falls; after release rules 3 and 5 stop counting,
// and a failing rule 1, 2 or 4 counts the claim but keeps the state.
func TestHold_AfterRelease(t *testing.T) {
	f := newHoldFixture()
	f.prev = HoldState{CPUs: 3, MemoryGB: 1}
	f.t.Points[3].RSSKB = 2 << 20 // above the envelope: rule 5 would fail
	state, need := f.hold()
	assert.Equal(t, HoldState{CPUs: 2, MemoryGB: 1}, state, "the lower of the stored hold and the candidate")
	assert.Equal(t, Need{CPUs: 2, MemoryGB: 1, IOSlots: 1}, need)

	f.fresh = false
	state, need = f.hold()
	assert.Equal(t, f.prev, state, "a failed sample keeps the state")
	assert.Equal(t, claimNeed, need, "and counts the claim")
}

func TestHoldState_Exceeded(t *testing.T) {
	h := HoldState{CPUs: 2, MemoryGB: 2}
	assert.Equal(t, "", h.Exceeded(2<<20, 2))
	assert.Equal(t, "memory", h.Exceeded(2<<20+1, 0))
	assert.Equal(t, "cores", h.Exceeded(0, 2.01))
	assert.Equal(t, "", HoldState{}.Exceeded(1<<30, 64), "nothing released, nothing exceeded")
	assert.Equal(t, "", HoldState{MemoryGB: 2}.Exceeded(0, 64), "cores not released")
}
```

- [ ] **Run:** FAIL (undefined).

- [ ] **Write `sched/hold.go`:**

```go
package sched

import "math"

// The hold's rules (past-peak holds spec §5.3).
const (
	holdRuns  = 3   // the profiled runs a belief needs
	holdSeen  = 0.9 // the share of the lowest profiled peak a run must reach
	holdDwell = 2   // the newest points that must be within the envelope now
)

// HoldState is where a running job's hold stands between passes (spec
// §5.3): for each resource, the hold it was released to, 0 while it holds
// its claim. A released hold only falls.
type HoldState struct {
	CPUs     int
	MemoryGB int
}

// Exceeded names the released resource whose usage, rssKB of memory and
// cores busy, is above its hold, "" for none (spec §5.4).
func (h HoldState) Exceeded(rssKB int64, cores float64) string {
	switch {
	case h.MemoryGB > 0 && rssKB > int64(h.MemoryGB)*kbPerGB:
		return "memory"
	case h.CPUs > 0 && cores > float64(h.CPUs):
		return "cores"
	}
	return ""
}

// holdResource is a resource a hold can release: its staircase in a
// profile, a trace point's usage in the staircase's unit, and a level, with
// its margin, in the claim's unit (at least 1).
type holdResource struct {
	stair func(*Profile) Staircase
	usage func(TracePoint) int64
	hold  func(level int64) int
}

var (
	holdMemory = holdResource{
		stair: func(p *Profile) Staircase { return p.Mem },
		usage: func(pt TracePoint) int64 { return pt.RSSKB },
		hold:  withMargin,
	}
	holdCores = holdResource{
		stair: func(p *Profile) Staircase { return p.Cores },
		usage: func(pt TracePoint) int64 { return milliCores(pt.Cores) },
		hold:  func(milli int64) int { return max(1, int(math.Ceil(float64(milli)*coreHeadroom/1000))) },
	}
)

// Hold is what running job rec takes from p's caps now, and where its hold
// stands after this pass (past-peak holds spec §5.3), a pure function of its
// arguments: b is rec's belief (nil for none, or while learning is off), t
// its trace (nil for none), fresh whether the latest sample succeeded and t
// is fresh, and prev where the last pass left it. The need is the claim's
// but for a released resource; the I/O slot is always the claim's. When
// rule 1, 2 or 4 fails the need is the claim's and prev is kept.
func Hold(rec Record, b *Belief, t *Trace, fresh bool, prev HoldState, p Policy) (HoldState, Need) {
	claim := rec.Claim.Need(p.Caps)
	runs := profiledRuns(b)
	if rec.HoldBroke || !holdable(rec, t, fresh, p) || len(runs) < holdRuns || brokeRecently(b) {
		return prev, claim
	}
	x := t.x()
	if len(aliveAt(runs, x)) == 0 {
		return prev, claim
	}
	next := HoldState{
		CPUs:     holdCores.released(prev.CPUs, claim.CPUs, t, runs, x),
		MemoryGB: holdMemory.released(prev.MemoryGB, claim.MemoryGB, t, runs, x),
	}
	need := claim
	if next.CPUs > 0 {
		need.CPUs = next.CPUs
	}
	if next.MemoryGB > 0 {
		need.MemoryGB = next.MemoryGB
	}
	return next, need
}

// holdable is rule 2: a complete, fresh trace of a job that was not
// adopted, with the switch on and a claim neither exclusive nor fully
// explicit.
func holdable(rec Record, t *Trace, fresh bool, p Policy) bool {
	return p.ReleasePastPeaks && fresh && t != nil && len(t.Points) > 0 && !t.Incomplete && !rec.Adopted &&
		!rec.Claim.Exclusive && !(rec.Sources.CPUs == SourceExplicit && rec.Sources.Memory == SourceExplicit)
}

// profiledRuns is the valid profiles among b's newest ProfiledRuns runs.
func profiledRuns(b *Belief) []*Profile {
	if b == nil {
		return nil
	}
	var out []*Profile
	for _, r := range b.Runs[:min(len(b.Runs), ProfiledRuns)] {
		if r.Profile != nil && r.Profile.Validate() == nil {
			out = append(out, r.Profile)
		}
	}
	return out
}

// brokeRecently reports whether one of b's last window runs broke its hold.
func brokeRecently(b *Belief) bool {
	for _, r := range b.recent() {
		if r.HoldBroke {
			return true
		}
	}
	return false
}

// aliveAt is the runs whose end is past progress x.
func aliveAt(runs []*Profile, x float64) []*Profile {
	var out []*Profile
	for _, p := range runs {
		if p.End > x {
			out = append(out, p)
		}
	}
	return out
}

// envelope is the most any of runs still used from progress x (rule 4).
func (r holdResource) envelope(runs []*Profile, x float64) int64 {
	var most int64
	for _, p := range runs {
		if v, ok := r.stair(p).At(x); ok {
			most = max(most, v)
		}
	}
	return most
}

// released is the resource's hold after this pass, 0 while it holds its
// claim: a released one (prev > 0) takes the lower of prev and the
// candidate; an unreleased one is released to the candidate when it has
// seen its peak (rule 3), tracks the envelope (rule 5) and the candidate is
// below the claim.
func (r holdResource) released(prev, claim int, t *Trace, runs []*Profile, x float64) int {
	alive := aliveAt(runs, x)
	now := r.envelope(alive, x)
	cand := min(r.hold(now), claim)
	if prev > 0 {
		return min(prev, cand)
	}
	lowest := int64(math.MaxInt64)
	for _, p := range runs {
		lowest = min(lowest, r.stair(p).Peak())
	}
	var seen int64
	for _, pt := range t.Points {
		seen = max(seen, r.usage(pt))
	}
	if float64(seen) < holdSeen*float64(lowest) {
		return 0
	}
	for _, p := range alive {
		if x <= r.stair(p).PeakAt {
			return 0
		}
	}
	if len(t.Points) < holdDwell {
		return 0
	}
	for _, pt := range t.Points[len(t.Points)-holdDwell:] {
		if r.usage(pt) > now {
			return 0
		}
	}
	if cand >= claim {
		return 0
	}
	return cand
}
```

- [ ] **Run:** PASS. The fixture's numbers: at x = 13 every run is alive; memory `E` = 1 GB, candidate `withMargin(1<<20)` = 2; cores `E` = 1000, candidate ⌈1.25⌉ = 2; the last two points (900 MB, 0.9c; 800 MB, 0.8c) are within; the seen peaks are 6 GB (≥ 5.4) and 3.6 cores (≥ 0.9 × 4).

### Step 1.5: Holds in `Admit`, `Grant` and `Forecast`

**Files:** `sched/admit.go`, `sched/grant.go`, `sched/forecast.go` and their tests.

- [ ] **Tests.** Every existing call gains `nil` for the holds (`Admit(jobs, p, now, nil)`, `Grant(dec, jobs, p, nil, belief)`, `Forecast(jobs, p, now, nil, usual, fallback)`). Add to `sched/admit_test.go`:

```go
func TestAdmit_CountsARunningJobAtItsHold(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	p := Policy{Caps: Caps{CPUs: 4, MemoryGB: 8, IOJobs: 2}, StarveAfter: time.Hour}
	running := Record{ID: 1, Order: 1, State: Running, Claim: Claim{CPUs: 4, MemoryGB: 8}}
	queued := Record{ID: 2, Order: 2, State: Queued, Submitted: now, Claim: Claim{CPUs: 2, MemoryGB: 4}}
	jobs := []Record{running, queued}

	assert.Equal(t, "needs 2 cores (0 free)", Admit(jobs, p, now, nil).Waiting[2], "a job with no hold counts at its claim")
	assert.Equal(t, []JobID{2}, Admit(jobs, p, now, map[JobID]Need{1: {CPUs: 2, MemoryGB: 2}}).Start)
	assert.Equal(t, "needs 2 cores (0 free)", Admit(jobs, p, now, map[JobID]Need{1: {CPUs: 3, MemoryGB: 2}}).Waiting[2])
	assert.Equal(t, "needs 4 GB (0 free)", Admit(jobs, p, now, map[JobID]Need{1: {CPUs: 2, MemoryGB: 9}}).Waiting[2], "a hold above what is free starts nothing")

	running.Claim.Exclusive = true
	jobs[0] = running
	assert.Empty(t, Admit(jobs, p, now, map[JobID]Need{1: {CPUs: 1, MemoryGB: 1}}).Start, "an exclusive claim ignores a hold")
}
```

Add to `sched/grant_test.go`:

```go
func TestGrant_CountsARunningJobAtItsHold(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := szPolicy // 14 cores and 22 GB; the reserve 4 cores and 6 GB
	p.StarveAfter = 10 * time.Minute
	wide := func(Record) *Belief { return belief(at(4, 3.9)) }
	job := Record{ID: 1, Order: 1, State: Queued, Submitted: now, Claim: Claim{CPUs: 4, MemoryGB: 2},
		Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	jobs := []Record{{ID: 9, State: Running, Claim: Claim{CPUs: 6, MemoryGB: 2}, Started: now}, job}
	assert.Nil(t, Grant(Admit(jobs, p, now, nil), jobs, p, nil, wide), "at its claim the reserve is all that is left")
	holds := map[JobID]Need{9: {CPUs: 2, MemoryGB: 1}}
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, Grant(Admit(jobs, p, now, holds), jobs, p, holds, wide),
		"at its hold, 14 − 2 − 4 − the reserve's 4 leaves 4 cores")
}
```

Add to `sched/forecast_test.go`:

```go
func TestForecast_CountsRunningJobsAtTheirHolds(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	p := Policy{Caps: Caps{CPUs: 4, MemoryGB: 8, IOJobs: 2}, StarveAfter: time.Hour}
	jobs := []Record{
		{ID: 1, Order: 1, State: Running, Started: now, Claim: Claim{CPUs: 4, MemoryGB: 8}},
		{ID: 2, Order: 2, State: Queued, Submitted: now, Claim: Claim{CPUs: 2, MemoryGB: 4}},
	}
	usual := func(Record) (time.Duration, bool) { return 10 * time.Minute, true }
	assert.Equal(t, now.Add(10*time.Minute), Forecast(jobs, p, now, nil, usual, time.Minute)[2])
	assert.Equal(t, now, Forecast(jobs, p, now, map[JobID]Need{1: {CPUs: 2, MemoryGB: 2}}, usual, time.Minute)[2])
}
```

- [ ] **Run:** FAIL (wrong arity).

- [ ] **`sched/admit.go`:** add

```go
// runningNeed is what running job r takes from p's caps: its hold, when
// holds has one and its claim is not exclusive (past-peak holds spec §6),
// else its claim.
func runningNeed(r Record, p Policy, holds map[JobID]Need) Need {
	if h, ok := holds[r.ID]; ok && !r.Claim.Exclusive {
		return h
	}
	return r.Claim.Need(p.Caps)
}
```

`Admit(jobs []Record, p Policy, now time.Time, holds map[JobID]Need)` uses `used = used.plus(runningNeed(r, p, holds))` for a running record; its doc comment adds "A running job counts at its hold in holds (past-peak holds spec §6), at its claim with none."

- [ ] **`sched/grant.go`:** `Grant(dec Decision, jobs []Record, p Policy, holds map[JobID]Need, belief func(Record) *Belief)`; the running branch is `used = used.plus(runningNeed(r, p, holds))`; the comment's pool reads "the caps less the running jobs' holds".

- [ ] **`sched/forecast.go`:** `Forecast(jobs []Record, p Policy, now time.Time, holds map[JobID]Need, usual …, fallback …)` passes `holds` to `Admit(sim, p, clock, holds)`; the comment adds "A running job counts at its hold in holds until its expected end; a job the simulation starts, at its claim."

- [ ] **Fix `core`'s three calls** so the build passes (`sched.Admit(jobs, policy, now, nil)`, `sched.Grant(dec, jobs, policy, nil, …)`, `sched.Forecast(…, time.Now(), nil, usualOf, …)`): package 2 replaces the `nil`s.

- [ ] **Run** `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./sched/... ./config/...`: PASS. `CGO_ENABLED=0 go vet ./sched/ ./config/`.

### Step 1.6: Commit

- [ ] `gofmt -l sched config core`, then commit by path the spec amendment, the plan, `sched/`, `config/settings.go` and the three core call sites: `feat(sched,config): profiles of done runs, and holds that fall once a job is past its peaks`.

## Package 2: the model

### Step 2.1: State, the trace, the break and the refresh

**Files:** `core/queue.go`, `core/queue_watch.go`, `core/queue_admit.go`, `core/queue_beliefs.go`, create `core/queue_holds.go`, `core/queue_view.go`; test `core/queue_holds_test.go`.

- [ ] **Write the tests** (`core/queue_holds_test.go`):

```go
package core

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqHoldProfile is a run that peaked at 6 GB and 4 cores before progress
// 10, then used 1 GB and 1 core to its end at 100.
func wqHoldProfile() *sched.Profile {
	return &sched.Profile{End: 100,
		Mem:   sched.Staircase{PeakAt: 10, Steps: []sched.Step{{Until: 10, Level: 6 << 20}, {Until: 100, Level: 1 << 20}}},
		Cores: sched.Staircase{PeakAt: 5, Steps: []sched.Step{{Until: 10, Level: 4000}, {Until: 100, Level: 1000}}}}
}

// wqHoldJob launches a real shim (sleep 30) on caps of 4 cores and 8 GB,
// gives it a 4c/8G learned claim and a belief with three profiled runs, and
// returns it with the model.
func wqHoldJob(t *testing.T) (*Model, sched.JobID) {
	t.Helper()
	m := wqLiveModel(t)
	wqConfig(t, m, `{"cpus": 4, "memory_gb": 8}`)
	id := wqLaunch(t, m, "sleep", "30")
	rec := &m.queue.store.Jobs[m.queue.index(id)]
	sig := wqSigOf(t, rec.Argv, rec.Cwd)
	b := m.queue.beliefs.Ensure(sig, time.Now())
	for range 3 {
		b.AddRun(sched.Run{Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 4, MemoryGB: 8}, PeakKB: 6 << 20,
			CPUSeconds: 100, WallSeconds: 50, Profile: wqHoldProfile()})
	}
	rec.Signature, rec.Claim = sig.ID(), sched.Claim{CPUs: 4, MemoryGB: 8}
	rec.Sources = sched.ClaimSources{CPUs: sched.SourceLearned, Memory: sched.SourceLearned, IO: sched.SourceLearned}
	return m, id
}

// wqSample delivers a pass whose sample shows job id's session at rss KB and
// cpu CPU-seconds, taken at.
func wqSample(m *Model, id sched.JobID, at time.Time, rss int64, cpu float64) {
	pid := m.queue.store.Jobs[m.queue.index(id)].PID
	snap := wqSnap(at, pid, rss, time.Duration(cpu*float64(time.Second)))
	m.Deliver(queuePassResult{at: at, obs: []jobObs{{id: id, pid: pid}}, snap: snap})
}

// wqPastPeak samples job id through its peak and two samples past it: at
// progress 13 its hold is 2c/2G.
func wqPastPeak(m *Model, id sched.JobID) {
	t0 := time.Now().Add(-12 * time.Second)
	wqSample(m, id, t0, 5<<20, 2)
	wqSample(m, id, t0.Add(3*time.Second), 6<<20, 12.8) // 3.6 cores: the peak
	wqSample(m, id, t0.Add(6*time.Second), 900<<10, 15.5)
	wqSample(m, id, t0.Add(9*time.Second), 800<<10, 18)
}

func TestJobQueue_AJobPastItsPeaksLetsTheNextOneIn(t *testing.T) {
	m, id := wqHoldJob(t)
	sub := wqJob(t, "sleep", "1")
	sub.Claim = sched.Claim{CPUs: 2, MemoryGB: 4}
	next, _, err := m.submitJob(sub)
	require.NoError(t, err)
	require.Equal(t, sched.Queued, wqRecord(t, m, next).State, "the claim leaves no room")

	wqPastPeak(m, id)
	assert.Equal(t, sched.HoldState{CPUs: 2, MemoryGB: 2}, m.queue.holds[id])
	v := m.queueView()
	require.Equal(t, id, v.Jobs[0].ID)
	assert.Equal(t, sched.Need{CPUs: 2, MemoryGB: 2}, v.Jobs[0].Holds)
	assert.Equal(t, sched.Need{CPUs: 2, MemoryGB: 2}, v.Held)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, v.Claimed)

	m.admitJobs()
	assert.Equal(t, sched.Running, wqRecord(t, m, next).State, "the hold leaves 2c/6G")
}

func TestJobQueue_AReleasedJobThatGrowsBreaksItsHold(t *testing.T) {
	m, id := wqHoldJob(t)
	wqPastPeak(m, id)
	require.NotZero(t, m.queue.holds[id])

	wqSample(m, id, time.Now(), 3<<20, 19)
	assert.True(t, wqRecord(t, m, id).HoldBroke)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, m.queue.holding[id], "back to its claim")
	assert.Zero(t, m.queueView().Jobs[0].Holds)

	wqSample(m, id, time.Now(), 800<<10, 19.5)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, m.queue.holding[id], "for the rest of its run")
}

func TestJobQueue_AnAdoptedJobHoldsItsClaim(t *testing.T) {
	m, id := wqHoldJob(t)
	m.queue.store.Jobs[m.queue.index(id)].Adopted = true
	wqPastPeak(m, id)
	assert.Nil(t, m.queue.traces[id], "no trace for an adopted job")
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, m.queue.holding[id])
}

func TestJobQueue_TheSwitchOffHoldsEveryClaim(t *testing.T) {
	m, id := wqHoldJob(t)
	wqConfig(t, m, `{"cpus": 4, "memory_gb": 8, "release_past_peaks": false}`)
	m.admitJobs() // rereads the block
	wqPastPeak(m, id)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 8}, m.queue.holding[id])
}

func TestJobQueue_ADoneRunsTraceBecomesItsProfile(t *testing.T) {
	const pid = 4242
	m := wqModel(t)
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Ended, rec.PID, rec.Notify = sched.Running, nil, time.Time{}, pid, false
	sig := sched.NewSignature("/repo", ".", rec.Argv)
	rec.Signature = sig.ID()
	m.queue.beliefs.Ensure(sig, time.Now())
	wqSeed(t, m, rec)
	t0 := time.Now().Add(-6 * time.Second)
	for i, s := range []struct {
		rss int64
		cpu float64
	}{{4 << 20, 1}, {6 << 20, 7}, {1 << 20, 8}} {
		at := t0.Add(time.Duration(i) * 3 * time.Second)
		m.Deliver(queuePassResult{at: at, obs: []jobObs{{id: 1, pid: pid}}, snap: wqSnap(at, pid, s.rss, time.Duration(s.cpu*float64(time.Second)))})
	}
	end := time.Now()
	m.Deliver(queuePassResult{at: end, obs: []jobObs{{id: 1, pid: pid, gone: true, exit: &sched.Exit{Code: 0, Ended: end}}}})

	b := m.queue.beliefs.Find(sig.ID())
	require.Len(t, b.Runs, 1)
	p := b.Runs[0].Profile
	require.NotNil(t, p, "a done run's trace is its profile")
	assert.Equal(t, 8.0, p.End)
	assert.Equal(t, int64(6<<20), p.Mem.Peak())
	assert.Equal(t, 7.0, p.Mem.PeakAt)
	assert.Nil(t, m.queue.traces[1], "the settle drops the trace")
}
```

Pin the other side of the profile rule too: in the same file a `TestJobQueue_AnAdoptedRunGetsNoProfile` that seeds the record with `Adopted: true` and the same samples, and asserts the run's `Profile` is nil.

- [ ] **Run** `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_(AJobPast|AReleased|AnAdopted|TheSwitch|ADoneRuns)'`: FAIL (undefined).

- [ ] **`core/queue.go`:** `queueState` gains, after `cpuMarks`:

```go
	// traces is each running job's samples since its start (past-peak holds
	// spec §5.1), holds where each one's hold stands (sched.HoldState), and
	// holding what each counts at for admission, from the last refreshHolds.
	// In memory only: settledJob drops a job's entries.
	traces  map[sched.JobID]*sched.Trace
	holds   map[sched.JobID]sched.HoldState
	holding map[sched.JobID]sched.Need
```

and `newQueueState` makes the three maps.

- [ ] **Create `core/queue_holds.go`:**

```go
package core

import (
	"maps"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// refreshHolds works out each running job's hold (sched.Hold, past-peak
// holds spec §5.3) under policy: where it stands, kept for the next pass,
// and what admission counts it at. A release is logged once per job and
// resource, and a change is a change a client sees.
func (m *Model) refreshHolds(policy sched.Policy, now time.Time) {
	q := m.queue
	holding := map[sched.JobID]sched.Need{}
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State != sched.Running {
			continue
		}
		tr := q.traces[rec.ID]
		prev := q.holds[rec.ID]
		next, need := sched.Hold(*rec, q.beliefOf(rec.Signature), tr, !q.sampleFailing && tr.Fresh(now), prev, policy)
		if prev.MemoryGB == 0 && next.MemoryGB > 0 {
			log.For("core").Info("queue.hold_released", "job", int64(rec.ID), "resource", "memory", "hold_gb", next.MemoryGB, "claim_gb", rec.Claim.MemoryGB)
		}
		if prev.CPUs == 0 && next.CPUs > 0 {
			log.For("core").Info("queue.hold_released", "job", int64(rec.ID), "resource", "cores", "hold_cpus", next.CPUs, "claim_cpus", rec.Claim.CPUs)
		}
		if next == (sched.HoldState{}) {
			delete(q.holds, rec.ID)
		} else {
			q.holds[rec.ID] = next
		}
		holding[rec.ID] = need
	}
	if !maps.Equal(holding, q.holding) {
		q.holding = holding
		q.moved()
	}
}

// breakHold returns job rec's hold to its claim for the rest of its run
// (past-peak holds spec §5.4): the jobs admitted on what it released keep
// running, the record says HoldBroke, and its run will.
func (m *Model) breakHold(rec *sched.Record, what string, rssKB int64, cores float64) {
	q := m.queue
	h := q.holds[rec.ID]
	log.For("core").Warn("queue.hold_broken", "job", int64(rec.ID), "signature", string(rec.Signature), "resource", what,
		"hold_cpus", h.CPUs, "hold_gb", h.MemoryGB, "rss_kb", rssKB, "cores", cores)
	rec.HoldBroke = true
	delete(q.holds, rec.ID)
	q.moved()
}
```

- [ ] **`core/queue_watch.go`:**
  - In `applyUsage`, after `q.cpuMarks[ob.id] = …` and before the usage comparison:

```go
		if !rec.Adopted {
			tr := q.traces[ob.id]
			if tr == nil {
				tr = &sched.Trace{}
				q.traces[ob.id] = tr
			}
			tr.Add(r.snap.Taken, sched.TracePoint{X: cpu.Seconds(), RSSKB: rss, Cores: cores})
		}
		if what := q.holds[ob.id].Exceeded(rss, cores); what != "" {
			m.breakHold(rec, what, rss, cores)
		}
```

  - In `deliverQueuePass`, after `m.applyUsage(r)`: `m.refreshHolds(q.policy, time.Now())`.
  - In `settledJob`, before the deletes: `trace := q.traces[rec.ID]`; delete `q.traces`, `q.holds`, `q.holding` entries with the others; call `m.recordRun(rec, trace)`.

- [ ] **`core/queue_beliefs.go`:** `recordRun(rec sched.Record, trace *sched.Trace)`; after `RunOf`:

```go
	if run.FullPeak() {
		if p, ok := sched.BuildProfile(trace); ok {
			run.Profile = p
		}
	}
```

and its comment adds "a done run that was not adopted keeps its trace as its profile (past-peak holds spec §5.2)". In the beliefs load (line 18's function), after a successful load, log each signature with an invalid profile once:

```go
	for _, b := range q.beliefs.Beliefs {
		for _, r := range b.Runs {
			if r.Profile == nil {
				continue
			}
			if err := r.Profile.Validate(); err != nil {
				log.For("core").Warn("queue.profile_invalid", "signature", string(b.ID), "job", int64(r.Job), "err", err)
				break
			}
		}
	}
```

- [ ] **`core/queue_admit.go`:** in `admitJobs`, after `m.resizeQueued(policy)`: `m.refreshHolds(policy, now)`; pass `q.holding` to `sched.Admit(jobs, policy, now, q.holding)` and `sched.Grant(dec, jobs, policy, q.holding, …)`. In `startJob`, first line after `rec :=`:

```go
	log.For("core").Info("queue.job_started", "job", int64(rec.ID), "signature", string(rec.Signature),
		"waited_s", int64(now.Sub(rec.QueuedSince()).Seconds()))
```

- [ ] **`core/queue_view.go`:** `QueueView` gains `Held sched.Need // what the running jobs' holds take (past-peak holds spec §7)` after `Claimed`; `JobView` gains, after `Ideal`:

```go
	// Past-peak holds (spec 2026-11-05-past-peak-holds §7): what a running
	// job counts at when that is below its claim; zero while it holds its
	// claim.
	Holds sched.Need
```

  `queueView` passes `q.holding` to `sched.Forecast`, and sums held beside claimed:

```go
			held := jv.Need
			if jv.Holds != (sched.Need{}) {
				held = jv.Holds
			}
			v.Held.CPUs += held.CPUs
			v.Held.MemoryGB += held.MemoryGB
			v.Held.IOSlots += held.IOSlots
```

  `queueJobView` sets, for a running record, `if h, ok := q.holding[r.ID]; ok && h != jv.Need { jv.Holds = h }`. `cloneSchedulerSettings` copies `ReleasePastPeaks` (`if s.ReleasePastPeaks != nil { on := *s.ReleasePastPeaks; s.ReleasePastPeaks = &on }`).

- [ ] **Run** the five tests: PASS. Then `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue|TestQueue|TestReadBeliefs|TestModelView|TestStateViews'`.

### Step 2.2: The wire

- [ ] `go test ./core/rpc -run TestProtocolReference -update`, then `CGO_ENABLED=0 go test ./core/rpc/` passes; `git diff --stat docs/specs/protocol.md` shows the new fields only.

### Step 2.3: Gates and commit

- [ ] `CGO_ENABLED=0 go vet ./core/ ./sched/`, `gofmt -l core sched`.
- [ ] Full suite through the queue: `loom job run -d "past-peak holds P2: full suite" -- env CGO_ENABLED=0 go test ./...`.
- [ ] Commit by path: `feat(core,rpc): traces, holds and broken holds in the model; profiles at settle`.

## Package 3: clients and docs

### Step 3.1: The work view and `list`

**Files:** `ui/work.go`, `ui/work_test.go`, `internal/cli/job_sizing.go`, its test.

- [ ] **Tests.** In `ui/work_test.go`, beside the existing row and bar tests: a running `JobView` with `Holds: sched.Need{CPUs: 1, MemoryGB: 2}` renders `holds 1c/2G` in its row, and one without doesn't; `workBars` of a view with `Claimed {12, 20, 0}`, `Held {8, 10, 0}` reads `8/14 held · 12 claimed · 5.1 in use` and `10/24 GB held · 20 claimed · …`, and with `Held` zero reads as today. In the CLI's sizing test: `claimText` of a running view with holds ends ` holds 1c/2G`, and of a queued view with `Holds` set does not.

- [ ] **`ui/work.go`:** in `workRowFacts`' running case, after the `extra` line (and not when cancelling):

```go
		if j.Holds != (sched.Need{}) {
			extra += "  " + j.Holds.HoldText()
		}
```

  `workBars` becomes:

```go
func workBars(q core.QueueView) []string {
	caps := q.Policy.Caps
	cpuFill, memFill := q.Claimed.CPUs, q.Claimed.MemoryGB
	cpu := fmt.Sprintf("%d/%d claimed · %.1f in use", q.Claimed.CPUs, caps.CPUs, q.UsedCPU)
	mem := fmt.Sprintf("%d/%d GB claimed · %s in use", q.Claimed.MemoryGB, caps.MemoryGB, workGB(q.UsedRSSKB))
	if q.Held.CPUs > 0 && q.Held.CPUs < q.Claimed.CPUs {
		cpuFill = q.Held.CPUs
		cpu = fmt.Sprintf("%d/%d held · %d claimed · %.1f in use", q.Held.CPUs, caps.CPUs, q.Claimed.CPUs, q.UsedCPU)
	}
	if q.Held.MemoryGB > 0 && q.Held.MemoryGB < q.Claimed.MemoryGB {
		memFill = q.Held.MemoryGB
		mem = fmt.Sprintf("%d/%d GB held · %d claimed · %s in use", q.Held.MemoryGB, caps.MemoryGB, q.Claimed.MemoryGB, workGB(q.UsedRSSKB))
	}
	io := fmt.Sprintf("%d/%d", q.Claimed.IOSlots, caps.IOJobs)
	return []string{
		workBarLine("CPU", cpuFill, caps.CPUs, workBarCells, cpu),
		workBarLine("MEM", memFill, caps.MemoryGB, workBarCells, mem),
		workBarLine("I/O", q.Claimed.IOSlots, caps.IOJobs, min(workBarCells, caps.IOJobs*2), io),
	}
}
```

  and its comment reads "the claimed totals, or the held ones while holds are below claims (past-peak holds spec §7), against the caps".

- [ ] **`internal/cli/job_sizing.go`:** `claimText` appends, before the tag, `if v.State == sched.Running && v.Holds != (sched.Need{}) { text += " " + v.Holds.HoldText() }`; its comment says so.

- [ ] **Run** `CGO_ENABLED=0 go test ./ui/ ./internal/cli/ -run 'Work|Claim|Sizing|JobList'`: PASS.

### Step 3.2: Docs

- [ ] **`sched/README.md`:** the Files list gains `profile.go` (`TracePoint`, `Trace`, `Step`, `Staircase`, `Profile`, `BuildProfile`, `ProfiledRuns`) and `hold.go` (`HoldState`, `Hold`); "Signatures, beliefs and sizing" gains a **Holds** bullet: what a trace and a profile are, the five rules in one sentence each, the margins, that a hold only falls, the break, and that `Admit`, `Grant` and `Forecast` count a running job at its hold; the `beliefs.json` row says the newest 5 runs carry `profile`.
- [ ] **`sched/CLAUDE.md`:** add a rule: "**Keep `Hold` and `BuildProfile` pure, never let a released hold rise, and never let a compressed staircase fall below the run it came from.** The model calls them on its loop at every sample and pass; a staircase below its run, or a hold that rose to a level history doesn't show, hands out memory a job will use, and nothing says so until the host swaps. **Enforced** by `TestBuildProfile_NeverBelowTheRun`, `TestHold_AfterRelease` and `TestHold_EachRuleHoldsTheClaim`."
- [ ] **`docs/claude/the-work-queue.md`:** a rule 12: holds are refreshed on the loop after each sample (`deliverQueuePass`) and before each pass (`admitJobs`), from `queueState.traces`; `applyUsage` appends only for a job not adopted and breaks a hold whose usage passes it (`breakHold`, which sets `Record.HoldBroke`); `settledJob` takes the trace for `recordRun`'s profile and drops the three maps' entries; nothing about holds is persisted but `HoldBroke` and the profile. *Silent:* a trace kept past settle grows a stranger's profile; a hold computed off the loop races the sample.
- [ ] **`USAGE.md`:** the Scheduler Caps table gains `release_past_peaks` (`true`; `true`/`false`): "Lets a running job reserve less than its claim once its own past runs show its peaks are behind it, so other jobs start sooner"; the work queue's sizing paragraph gains a **Holds** bullet saying what `holds 1c/2G` and the header's `held` mean, that a job using more than its hold returns to its claim and its command stops releasing for its next 10 runs.

### Step 3.3: Gates and commit

- [ ] `go run ./tools/claudemd -v` (budgets), `CGO_ENABLED=0 go vet ./...` (lint skipped: v2 rejects the config), `gofmt -l` on the touched packages, `GOOS=windows CGO_ENABLED=0 go build ./...`.
- [ ] Full, race and e2e through the queue, one at a time.
- [ ] Commit by path: `feat(ui,cli): show a hold below its claim; docs`.
- [ ] Update the memory note and say what's done.

## Execution notes (2026-11-05, inline)

- **P1** 36f7273e. Deviations from the steps above: `TestAdmit_CountsARunningJobAtItsHold`'s 3-core hold leaves "(1 free)", not 0; `policyOff` is a package variable (`new(false)` needs Go 1.26, `go.mod` is 1.25); the `Resolve` tables expect `ReleasePastPeaks: true` except "every field set", which sets it false. Mutation runs: every term of `Hold`'s guards killed after one added case (a released job outlasting every run, in `TestHold_AfterRelease`, which killed the two survivors: `len(aliveAt) == 0` and `p.End > x`); the trace merge's mutant (keep the later point) survived a fixture whose pairs peaked on their later point, fixed by giving point 0 the peak.
- **P2**: the model's six tests passed first time; mutants killed: the `breakHold` call, the adopted guard on the trace, the settle's trace drop, `Admit`'s holds, the view's `h != jv.Need`, the profile at `recordRun`. `TestJobQueue_AnAdoptedRunGetsNoProfile` passes on the trace guard alone (an adopted job has no trace), so `recordRun`'s `FullPeak` check is a second line, not separately pinned. Adding view fields needs no `rpc.Protocol` bump (`core/rpc/CLAUDE.md`).
- **P3**: `claimText` puts the hold after the source tag (`4c/8G learned·3 holds 1c/2G`); the work view's bars are 14 cells.
- **Gates**: the first full suite (job 552, on the P2+P3 tree, run late behind a starved 9 GB head) failed in `TestQueueView_OrdersJobsAndTotals`, whose `qvPolicy` predates the switch; fixing it showed a real gap: `setSchedulerCaps` kept only the file's `agent` block, so a caps edit from the work view's form (`app/work_caps.go`, its own fields only) would turn `release_past_peaks: false` back to the default. Fixed in P2 with `TestJobQueue_ACapsEditKeepsTheReleaseSwitch` (red first), spec amendment 9. The view test also pins `Held` equal to `Claimed` with no profiled runs.

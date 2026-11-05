package sched

import (
	"cmp"
	"slices"
	"time"
)

// Rung is where the ladder has a running job (cgroup spec §8.2): "" for
// none.
type Rung string

// The ladder's rungs on a running job; a requeue ends the attempt.
const (
	RungSqueezed Rung = "squeezed" // its MemoryHigh is its working set
	RungPaused   Rung = "paused"   // frozen
)

// Text is r as the work view and loom job list show it.
func (r Rung) Text() string {
	if r == RungPaused {
		return "paused (memory)"
	}
	return string(r)
}

// The ladder's thresholds and timings (cgroup spec §8).
const (
	shortPressure  = 10.0        // the slice's memory pressure (some avg10, %) that makes memory short
	calmPressure   = 2.0         // pressure under which, with headroom, memory is calm
	calmMarginKB   = 2 * kbPerGB // headroom above the reserve that is calm
	minHighKB      = 64 << 10    // a squeeze's least MemoryHigh: 0 would be no limit
	squeezeToPause = 15 * time.Second
	pauseToRequeue = 30 * time.Second
	calmFor        = 30 * time.Second
	restoreEvery   = 15 * time.Second
	// MaxRequeues is how many times a job is requeued before it runs at its
	// full claim and is never laddered again.
	MaxRequeues = 2
)

// LadderJob is a running contained job as the ladder sees it.
type LadderJob struct {
	ID        JobID
	Started   time.Time
	WorkingKB int64
	Rung      Rung
	RungAt    time.Time
	NoRequeue bool
	Exclusive bool
	Starved   bool // the starvation guard started it
	Requeues  int
}

// LadderInput is one pass's reading (cgroup spec §8.1).
type LadderInput struct {
	Now         time.Time
	HeadroomKB  int64   // the host's MemAvailable
	ReserveKB   int64   // interactive_reserve_gb
	MemPressure float64 // the jobs slice's memory.pressure "some" avg10
	Jobs        []LadderJob
}

// LadderState is what the ladder keeps between passes.
type LadderState struct {
	CalmSince   time.Time // zero unless memory is calm
	LastRestore time.Time
}

// ActionKind is what an Action does.
type ActionKind string

// The ladder's actions.
const (
	ActSqueeze ActionKind = "squeeze"
	ActPause   ActionKind = "pause"
	ActRequeue ActionKind = "requeue"
	ActRestore ActionKind = "restore" // thaw if paused, MemoryHigh back to no limit
)

// Action is one step the ladder asks for.
type Action struct {
	Kind         ActionKind
	Job          JobID
	MemoryHighKB int64 // ActSqueeze's
}

// Short reports whether memory is short: headroom below the reserve, or the
// slice's memory pressure at 10% or more.
func (in LadderInput) Short() bool {
	return in.HeadroomKB < in.ReserveKB || in.MemPressure >= shortPressure
}

// calm reports whether memory is calm: headroom at least 2 GB above the
// reserve and pressure under 2%.
func (in LadderInput) calm() bool {
	return in.HeadroomKB >= in.ReserveKB+calmMarginKB && in.MemPressure < calmPressure
}

// Ladder is one pass of the ladder (cgroup spec §8.2–§8.3), a pure function:
// at most one action. While memory is short it climbs a rung on the victim;
// once it has been calm for calmFor it restores the oldest laddered job, one
// every restoreEvery; in between it holds.
func Ladder(st LadderState, in LadderInput) (LadderState, *Action) {
	switch {
	case in.Short():
		st.CalmSince = time.Time{}
		return st, climb(in)
	case in.calm():
		if st.CalmSince.IsZero() {
			st.CalmSince = in.Now
		}
		if in.Now.Sub(st.CalmSince) < calmFor || in.Now.Sub(st.LastRestore) < restoreEvery {
			return st, nil
		}
		if j := oldestLaddered(in.Jobs); j != nil {
			st.LastRestore = in.Now
			return st, &Action{Kind: ActRestore, Job: j.ID}
		}
		return st, nil
	}
	st.CalmSince = time.Time{}
	return st, nil
}

// climb is the next rung on the victim, nil while its rung's wait runs.
func climb(in LadderInput) *Action {
	v := victim(in.Jobs)
	switch {
	case v == nil:
		return nil
	case v.Rung == "":
		return &Action{Kind: ActSqueeze, Job: v.ID, MemoryHighKB: max(v.WorkingKB, minHighKB)}
	case v.Rung == RungSqueezed && in.Now.Sub(v.RungAt) >= squeezeToPause:
		return &Action{Kind: ActPause, Job: v.ID}
	case v.Rung == RungPaused && in.Now.Sub(v.RungAt) >= pauseToRequeue:
		return &Action{Kind: ActRequeue, Job: v.ID}
	}
	return nil
}

// victim is the job the ladder acts on: of the jobs with something left to
// give (not exclusive, not requeued MaxRequeues times, not paused with
// NoRequeue), one the starvation guard didn't start before one it did, then
// the most recently started; nil for none.
func victim(jobs []LadderJob) *LadderJob {
	var cands []*LadderJob
	for i := range jobs {
		j := &jobs[i]
		if j.Exclusive || j.Requeues >= MaxRequeues || (j.Rung == RungPaused && j.NoRequeue) {
			continue
		}
		cands = append(cands, j)
	}
	if len(cands) == 0 {
		return nil
	}
	return slices.MinFunc(cands, func(a, b *LadderJob) int {
		return cmp.Or(compareBool(a.Starved, b.Starved), b.Started.Compare(a.Started), cmp.Compare(b.ID, a.ID))
	})
}

// oldestLaddered is the laddered job that started first, nil for none.
func oldestLaddered(jobs []LadderJob) *LadderJob {
	var out *LadderJob
	for i := range jobs {
		j := &jobs[i]
		if j.Rung != "" && (out == nil || j.Started.Before(out.Started) || (j.Started.Equal(out.Started) && j.ID < out.ID)) {
			out = j
		}
	}
	return out
}

// compareBool orders false before true.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}

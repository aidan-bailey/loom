package sched

import (
	"slices"
	"time"
)

// The registry's limits (smart scheduler spec §5.3).
const (
	MaxBeliefs = 1000
	MaxRuns    = 20
	MaxNotes   = 5
)

// Outcome is how a run ended, which decides what it teaches (spec §5.2).
type Outcome string

// The outcomes of a run.
const (
	OutcomeDone      Outcome = "done"
	OutcomeFailed    Outcome = "failed"    // a non-zero exit code
	OutcomeSignal    Outcome = "signal"    // a signal nobody requested
	OutcomeCancelled Outcome = "cancelled" // whatever its exit
)

// Run is one settled job as its signature's belief records it.
type Run struct {
	Job         JobID     `json:"job"`
	Ended       time.Time `json:"ended"`
	Outcome     Outcome   `json:"outcome"`
	Signal      string    `json:"signal,omitempty"`
	Adopted     bool      `json:"adopted,omitempty"`
	Claim       Claim     `json:"claim"`
	PeakKB      int64     `json:"peak_kb"`
	CPUSeconds  float64   `json:"cpu_seconds,omitempty"`
	WallSeconds float64   `json:"wall_seconds"`
	ReadBytes   *int64    `json:"read_bytes,omitempty"`
	WriteBytes  *int64    `json:"write_bytes,omitempty"`
	Spare       int       `json:"spare,omitempty"`      // the cores it had past its base claim (job scaling spec §5.3)
	Checkout    string    `json:"checkout,omitempty"`   // the working tree's top it ran in (Record.Checkout); "" for a run recorded before 2026-11-05
	Profile     *Profile  `json:"profile,omitempty"`    // its usage over its progress (past-peak holds spec §5.2); only the newest ProfiledRuns keep one
	HoldBroke   bool      `json:"hold_broke,omitempty"` // its usage passed its lowered hold (past-peak holds spec §5.4)
	Curve       *Curve    `json:"curve,omitempty"`      // its usage over its progress, rising and falling (cgroup spec §6); only the newest ProfiledRuns keep one
}

// FullPeak reports whether r's peak is the job's whole peak: a done run's,
// sampled from its start. Every other peak is a floor, which can raise a
// claim but never set one alone.
func (r Run) FullPeak() bool { return r.Outcome == OutcomeDone && !r.Adopted }

// teachesCores reports whether r teaches cores: a done run with CPU time.
func (r Run) teachesCores() bool {
	return r.Outcome == OutcomeDone && r.CPUSeconds > 0 && r.WallSeconds > 0
}

// teachesIO reports whether r teaches I/O: a done run that measured it.
func (r Run) teachesIO() bool { return r.Outcome == OutcomeDone && r.WriteBytes != nil }

// TeachesDuration reports whether r teaches a duration: a run that exited,
// done or with an exit code.
func (r Run) TeachesDuration() bool {
	return (r.Outcome == OutcomeDone || r.Outcome == OutcomeFailed) && r.WallSeconds > 0
}

// RunOf is what finished record rec teaches its belief (spec §5.2), and
// false for a job that taught nothing: one that never started, failed to
// start, or was lost.
func RunOf(rec Record) (Run, bool) {
	if rec.Started.IsZero() || rec.Ended.IsZero() || rec.Failure != "" {
		return Run{}, false
	}
	var out Outcome
	switch rec.State {
	case Done:
		out = OutcomeDone
	case Failed:
		switch {
		case rec.Exit == nil || rec.Exit.Error != "":
			return Run{}, false
		case rec.Exit.Signal != "":
			out = OutcomeSignal
		default:
			out = OutcomeFailed
		}
	case Cancelled:
		out = OutcomeCancelled
	default:
		return Run{}, false
	}
	run := Run{
		Job: rec.ID, Ended: rec.Ended, Outcome: out, Adopted: rec.Adopted, Claim: rec.Claim,
		PeakKB: rec.PeakRSSKB, WallSeconds: rec.Ended.Sub(rec.Started).Seconds(), Spare: rec.Spare.CPUs,
		Checkout: rec.Checkout, HoldBroke: rec.HoldBroke,
	}
	if e := rec.Exit; e != nil {
		run.Signal = e.Signal
		run.PeakKB = max(run.PeakKB, e.MaxRSSKB)
		run.CPUSeconds = e.CPUSeconds
		run.ReadBytes, run.WriteBytes = clonePtr(e.ReadBytes), clonePtr(e.WriteBytes)
	}
	return run, true
}

// clonePtr returns a pointer to a copy of *p, or nil.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Prior is the advisor's estimate for a signature no run has measured.
type Prior struct {
	CPUs            int         `json:"cpus"`
	MemoryGB        int         `json:"memory_gb"`
	IO              bool        `json:"io"`
	DurationSeconds int         `json:"duration_seconds"`
	Like            SignatureID `json:"like,omitempty"`
	Note            string      `json:"note,omitempty"`
	Model           string      `json:"model,omitempty"`
	Effort          string      `json:"effort,omitempty"`
	At              time.Time   `json:"at"`
	Score           PriorScore  `json:"score,omitzero"`
}

// PriorScore compares a prior with the first runs that measured it: the
// first full peak over the prior's memory, the first duration run's wall
// time over the prior's duration. 0 until measured.
type PriorScore struct {
	Memory   float64 `json:"memory,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// Note is the advisor's note on a surprise.
type Note struct {
	At   time.Time `json:"at"`
	Job  JobID     `json:"job"`
	Text string    `json:"text"`
}

// AdvisorMarks are the advisor's dealings with one belief: its last failed
// prior, and its last reconcile.
type AdvisorMarks struct {
	FailedAt     time.Time `json:"failed_at,omitzero"`
	FailedWhy    string    `json:"failed_why,omitempty"`
	ReconciledAt time.Time `json:"reconciled_at,omitzero"`
}

// Belief is what the scheduler knows of one signature (spec §5.3).
type Belief struct {
	ID SignatureID `json:"id"`
	Signature
	FirstSeen time.Time    `json:"first_seen"`
	LastSeen  time.Time    `json:"last_seen"`
	Runs      []Run        `json:"runs,omitempty"` // newest first, at most MaxRuns
	Prior     *Prior       `json:"prior,omitempty"`
	Notes     []Note       `json:"notes,omitempty"` // newest first, at most MaxNotes
	Advisor   AdvisorMarks `json:"advisor,omitzero"`
}

// AddRun records r as b's newest run, keeping MaxRuns, and profiles and
// curves on the newest ProfiledRuns only.
func (b *Belief) AddRun(r Run) {
	b.Runs = slices.Insert(b.Runs, 0, r)
	if len(b.Runs) > MaxRuns {
		b.Runs = b.Runs[:MaxRuns]
	}
	for i := ProfiledRuns; i < len(b.Runs); i++ {
		b.Runs[i].Profile, b.Runs[i].Curve = nil, nil
	}
}

// AddNote records n as b's newest note, keeping MaxNotes.
func (b *Belief) AddNote(n Note) {
	b.Notes = slices.Insert(b.Notes, 0, n)
	if len(b.Notes) > MaxNotes {
		b.Notes = b.Notes[:MaxNotes]
	}
}

// recent is b's last window runs, newest first; none for a nil belief.
func (b *Belief) recent() []Run {
	if b == nil {
		return nil
	}
	return b.Runs[:min(len(b.Runs), window)]
}

// Beliefs is beliefs.json: every belief.
type Beliefs struct {
	Version int      `json:"version"`
	Beliefs []Belief `json:"beliefs"`
}

// Find is id's belief, nil when there is none. The pointer is good until
// the registry next gains or loses a belief.
func (bs *Beliefs) Find(id SignatureID) *Belief {
	for i := range bs.Beliefs {
		if bs.Beliefs[i].ID == id {
			return &bs.Beliefs[i]
		}
	}
	return nil
}

// Ensure finds sig's belief, or makes it, and marks it seen at now. Making
// one past MaxBeliefs drops the belief least recently seen.
func (bs *Beliefs) Ensure(sig Signature, now time.Time) *Belief {
	id := sig.ID()
	if b := bs.Find(id); b != nil {
		b.LastSeen = now
		return b
	}
	if len(bs.Beliefs) >= MaxBeliefs {
		oldest := 0
		for i := range bs.Beliefs {
			if bs.Beliefs[i].LastSeen.Before(bs.Beliefs[oldest].LastSeen) {
				oldest = i
			}
		}
		bs.Beliefs = slices.Delete(bs.Beliefs, oldest, oldest+1)
	}
	sig.Command, sig.Env = slices.Clone(sig.Command), slices.Clone(sig.Env)
	bs.Beliefs = append(bs.Beliefs, Belief{ID: id, Signature: sig, FirstSeen: now, LastSeen: now})
	return &bs.Beliefs[len(bs.Beliefs)-1]
}

// Forget drops id's belief, and reports whether there was one.
func (bs *Beliefs) Forget(id SignatureID) bool {
	n := len(bs.Beliefs)
	bs.Beliefs = slices.DeleteFunc(bs.Beliefs, func(b Belief) bool { return b.ID == id })
	return len(bs.Beliefs) < n
}

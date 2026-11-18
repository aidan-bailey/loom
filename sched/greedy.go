package sched

import (
	"fmt"
	"slices"
	"time"
)

// Greedy is what greedy admission needs beside the records (cgroup spec §7).
type Greedy struct {
	BudgetKB int64
	// Running is each running job's prediction from now. A running job
	// without one (a start the forecast simulates) is predicted from its
	// start through Predict.
	Running map[JobID]Prediction
	// Predict is a job's prediction from start: a candidate's, or a
	// simulated start's.
	Predict     func(r Record, start time.Time) Prediction
	CPUPressure float64 // the jobs slice's cpu.pressure "some" avg10
	Holdoff     string  // set: nothing starts, and this is why (the ladder)
	// Borrowing (memory borrowing spec §5.2): each running job's measured
	// memory now, in KB (its working set, or its session's RSS when
	// uncontained; absent before its first sample), and whether memory is
	// tight (Tight).
	Used  map[JobID]int64
	Tight bool
	// Limits is the accounts whose agents a usage limit holds (usage-limit
	// hold spec §2), as Admit's limits.
	Limits AccountLimits
}

// Budget is the memory greedy admission packs into, in KB (cgroup spec
// §7.1): what the running jobs use now plus the host's available memory,
// less the reserve, held between 0 and the memory cap.
func Budget(usedKB, availKB int64, p Policy) int64 {
	b := usedKB + availKB - int64(p.ReserveGB)*kbPerGB
	return min(max(b, 0), int64(p.Caps.MemoryGB)*kbPerGB)
}

// AdmitGreedy is Admit with the greedy fit (cgroup spec §7.2–§7.3): the
// same queue walk, over-cap skip and starvation guard, a pure function of
// its arguments.
func AdmitGreedy(jobs []Record, p Policy, now time.Time, g Greedy) Decision {
	f := &greedyFit{p: p, g: g, now: now}
	d := admitWith(jobs, p, now, f, g.Limits)
	d.OnMemory = f.onMemory
	return d
}

// greedyFit counts running jobs by their predictions and claimed cores.
type greedyFit struct {
	p         Policy
	g         Greedy
	now       time.Time
	preds     []Prediction
	cores     int
	slots     int
	exclusive JobID   // a running or started exclusive job
	any       bool    // a job runs or has started
	onMemory  []JobID // the jobs fits failed on memory alone
	paused    JobID   // a paused job, 0 for none
	pausedUse []Prediction
}

func (f *greedyFit) count(r Record) {
	pred, ok := f.g.Running[r.ID]
	if !ok {
		pred = f.g.Predict(r, r.Started)
	}
	f.add(r, pred)
}

func (f *greedyFit) take(r Record) { f.add(r, f.g.Predict(r, f.now)) }

func (f *greedyFit) add(r Record, pred Prediction) {
	f.any = true
	switch {
	case r.Paused():
		// Memory only (job controls spec §7.2): pred is its use, flat.
		f.preds = append(f.preds, pred)
		f.pausedUse = append(f.pausedUse, pred)
		if f.paused == 0 {
			f.paused = r.ID
		}
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

// alone is why r's predicted peak passes the budget by itself, or beside
// the paused jobs' use, which only the owner's resume ends (job controls
// spec §7.2). The budget counts what the running jobs use as free (Budget),
// so no job's end makes room for r; nor can a borrow, whose fit adds to the
// same peak. An exclusive claim waits only for the host to empty, never on
// memory, so only while no job is paused; and under the ladder's holdoff,
// which starts nothing, the holdoff says why.
func (f *greedyFit) alone(r Record) string {
	switch {
	case f.g.Holdoff != "":
		return ""
	case r.Claim.Exclusive && f.paused != 0:
		return fmt.Sprintf("waits for job %d, which you paused", f.paused)
	case r.Claim.Exclusive:
		return ""
	}
	cand := f.g.Predict(r, f.now)
	end := cand.End()
	peak := PeakSum([]Prediction{cand}, f.now, end)
	need := (peak + kbPerGB - 1) / kbPerGB
	switch {
	case peak > f.g.BudgetKB:
		return fmt.Sprintf("needs ~%d GB at its peak (the host leaves %d with no job running)", need, f.g.BudgetKB/kbPerGB)
	case len(f.pausedUse) > 0 && PeakSum(append(slices.Clone(f.pausedUse), cand), f.now, end) > f.g.BudgetKB:
		left := max(0, f.g.BudgetKB-PeakSum(f.pausedUse, f.now, end)) / kbPerGB
		return fmt.Sprintf("needs ~%d GB at its peak (the host leaves %d beside the jobs you paused)", need, left)
	}
	return ""
}

// pausedHorizon is how long a paused job's prediction lasts: longer than any
// candidate's, since only the owner ends a pause.
const pausedHorizon = 30 * 24 * time.Hour

// PausedPrediction is a paused job's prediction from now (job controls spec
// §7.2): flat at usedKB, its measured memory, until the owner resumes it.
func PausedPrediction(usedKB int64, now time.Time) Prediction {
	return Flat(usedKB, now, now.Add(pausedHorizon))
}

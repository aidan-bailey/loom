package sched

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// waitOver is why a job whose claim exceeds a cap waits: it never will start
// at these caps.
const waitOver = "over this host's caps"

// Decision is the outcome of one admission pass.
type Decision struct {
	Start   []JobID          // in the order to start them; nil when none
	Waiting map[JobID]string // why each queued job left waiting waits; never nil
	Starved JobID            // the head the starvation guard started; 0 for none
	// OnMemory is the queued jobs AdmitGreedy left waiting on its memory fit
	// alone, in queue order: the jobs Borrow may start (memory borrowing
	// spec §5.2). Admit leaves it nil.
	OnMemory []JobID
}

// plus is the sum of two needs.
func (n Need) plus(o Need) Need {
	return Need{CPUs: n.CPUs + o.CPUs, MemoryGB: n.MemoryGB + o.MemoryGB, IOSlots: n.IOSlots + o.IOSlots}
}

// exceeds reports whether c is over a cap of caps, so that it can never fit
// however empty the host is. An exclusive claim is never over.
func (c Claim) exceeds(caps Caps) bool {
	if c.Exclusive {
		return false
	}
	return c.CPUs > caps.CPUs || c.MemoryGB > caps.MemoryGB
}

// usable reports whether p has caps to admit against. The zero Policy, which
// the model holds while admission is paused, has none.
func (p Policy) usable() bool {
	return p.Caps.CPUs >= 1 && p.Caps.MemoryGB >= 1 && p.Caps.IOJobs >= 1
}

// shortfall says why need does not fit what used leaves of caps, checking
// the cores, then the GB, then the I/O slots, or returns "" when it fits.
// What is free is never shown below 0: lowering a cap leaves the running
// jobs over it.
func shortfall(used, need Need, caps Caps) string {
	switch {
	case used.CPUs+need.CPUs > caps.CPUs:
		return fmt.Sprintf("needs %d cores (%d free)", need.CPUs, max(0, caps.CPUs-used.CPUs))
	case used.MemoryGB+need.MemoryGB > caps.MemoryGB:
		return fmt.Sprintf("needs %d GB (%d free)", need.MemoryGB, max(0, caps.MemoryGB-used.MemoryGB))
	case used.IOSlots+need.IOSlots > caps.IOJobs:
		if need.IOSlots == 1 {
			return "needs an I/O slot"
		}
		return fmt.Sprintf("needs %d I/O slots (%d free)", need.IOSlots, max(0, caps.IOJobs-used.IOSlots))
	}
	return ""
}

// runningNeed is what running job r takes from p's caps: its hold, when
// holds has one and its claim is not exclusive (past-peak holds spec §6),
// else its claim.
func runningNeed(r Record, p Policy, holds map[JobID]Need) Need {
	if h, ok := holds[r.ID]; ok && !r.Claim.Exclusive {
		return h
	}
	return r.Claim.Need(p.Caps)
}

// fitter is how admission counts what runs and decides what fits: claims
// (claimFit, spec §5.2) or the greedy forecast (greedyFit, cgroup spec §7).
type fitter interface {
	count(r Record)       // a running job
	fits(r Record) string // why r doesn't fit what is counted; "" when it does
	take(r Record)        // r starts: count it
}

// claimFit counts running jobs at their holds or claims.
type claimFit struct {
	p     Policy
	holds map[JobID]Need
	used  Need
}

func (f *claimFit) count(r Record)       { f.used = f.used.plus(runningNeed(r, f.p, f.holds)) }
func (f *claimFit) fits(r Record) string { return shortfall(f.used, r.Claim.Need(f.p.Caps), f.p.Caps) }
func (f *claimFit) take(r Record)        { f.used = f.used.plus(r.Claim.Need(f.p.Caps)) }

// Admit decides which queued jobs start now (spec §5.2) by their claims: a
// Running exclusive claim takes every cap as it stands, and any other
// running job counts at its hold in holds (past-peak holds spec §6), at its
// claim with none. The walk is admitWith's. It is a pure function of its
// arguments.
func Admit(jobs []Record, p Policy, now time.Time, holds map[JobID]Need, limits AccountLimits) Decision {
	return admitWith(jobs, p, now, &claimFit{p: p, holds: holds}, limits)
}

// admitWith walks the queued jobs in queue order (Order, then ID), counting
// the running ones with f:
//
//  1. A job whose claim exceeds a cap is skipped and waits "over this host's
//     caps"; it is never the head. A job being sized (Record.Sizing) waits
//     with Sizing.Wait and is never the head either.
//  2. The head is the first job that is not skipped. Once it has waited
//     p.StarveAfter since it joined the queue (Record.QueuedSince: its
//     release, for a dependent), it starts if it fits (Decision.Starved
//     names it) and nothing else starts, so smaller jobs cannot pass a large
//     one forever.
//  3. Otherwise (first fit) it starts every job, in order, that fits what
//     the running jobs and the jobs started before it leave.
//
// Agent jobs (Kind KindAgent) are apart: each starts, in queue order, while
// fewer than p.AgentJobs of its account run, unless limits holds the
// account (usage-limit hold spec §2); they take nothing from the caps and
// are never the head. Run jobs (Kind KindRun) start at once, take
// nothing from the caps and are never the head.
//
// Named locks (Record.Locks) come before the fit: a job naming a lock that
// a running job holds waits "waits for lock NAME (job N)" and is never the
// head; one naming a lock a job started earlier in the pass waits the same.
//
// Each job left waiting gets its reason in Decision.Waiting. Only Queued and
// Running records count, so a held job is never seen. A policy without caps
// (the zero Policy) starts nothing. The jobs slice is left as it is.
func admitWith(jobs []Record, p Policy, now time.Time, f fitter, limits AccountLimits) Decision {
	d := Decision{Waiting: map[JobID]string{}}
	var queued, agents, runs []Record
	running := map[string]int{} // running agent jobs per account key
	for _, r := range jobs {
		switch {
		case r.Kind == KindRun && r.State == Queued:
			runs = append(runs, r)
		case r.Kind == KindRun:
			// A running run job takes nothing from the caps.
		case r.Kind == KindAgent && r.State == Running:
			running[r.AccountKey]++
		case r.Kind == KindAgent && r.State == Queued:
			agents = append(agents, r)
		case r.State == Running:
			f.count(r)
		case r.State == Queued:
			queued = append(queued, r)
		}
	}
	byOrder := func(a, b Record) int { return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID)) }
	// Run jobs (flow in loom part 3b, spec §5.2) start at once while
	// admission has a usable policy, and are never a head.
	slices.SortFunc(runs, byOrder)
	for _, r := range runs {
		if p.usable() {
			d.Start = append(d.Start, r.ID)
		}
	}
	// Agent jobs (flow in loom part 3a, spec §5.2) start by their account's
	// limit, never by the caps, and are never a head; while admission has
	// no usable policy they wait with no reason, so the view shows its.
	slices.SortFunc(agents, byOrder)
	for _, r := range agents {
		switch {
		case !p.usable():
		case limits.holding(r.AccountKey, now) != "":
			d.Waiting[r.ID] = limits.holding(r.AccountKey, now)
		case running[r.AccountKey] >= p.AgentJobs:
			d.Waiting[r.ID] = fmt.Sprintf("waits for an agent slot (%d of %d running on this account)", running[r.AccountKey], p.AgentJobs)
		default:
			running[r.AccountKey]++
			d.Start = append(d.Start, r.ID)
		}
	}
	slices.SortFunc(queued, byOrder)
	held := lockHolders(jobs)

	var line []Record // the jobs that can ever run, in queue order
	for _, r := range queued {
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
		line = append(line, r)
	}
	if len(line) == 0 {
		return d
	}

	if head := line[0]; now.Sub(head.QueuedSince()) >= p.StarveAfter {
		if why := f.fits(head); why == "" {
			d.Start = append(d.Start, head.ID)
			d.Starved = head.ID
		} else {
			d.Waiting[head.ID] = why
		}
		for _, r := range line[1:] {
			d.Waiting[r.ID] = fmt.Sprintf("waits for job %d, which has waited past %dm", head.ID, int(p.StarveAfter/time.Minute))
		}
		return d
	}

	for _, r := range line {
		why := held.blocks(r)
		if why == "" {
			why = f.fits(r)
		}
		if why != "" {
			d.Waiting[r.ID] = why
			continue
		}
		f.take(r)
		held.take(r)
		d.Start = append(d.Start, r.ID)
	}
	return d
}

// locks maps each lock a running job, or one a pass starts, holds to that
// job.
type locks map[string]JobID

// lockHolders is the locks the running jobs among jobs hold.
func lockHolders(jobs []Record) locks {
	held := locks{}
	for _, r := range jobs {
		if r.State == Running {
			held.take(r)
		}
	}
	return held
}

// take records r's locks as held by r.
func (l locks) take(r Record) {
	for _, name := range r.Locks {
		if _, ok := l[name]; !ok {
			l[name] = r.ID
		}
	}
}

// blocks says why r can't start for a lock another job holds, naming the
// first in r's order, or "" when none of its locks is held.
func (l locks) blocks(r Record) string {
	for _, name := range r.Locks {
		if id, ok := l[name]; ok && id != r.ID {
			return fmt.Sprintf("waits for lock %s (job %d)", name, id)
		}
	}
	return ""
}

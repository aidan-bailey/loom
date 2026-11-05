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

// Admit decides which queued jobs start now (spec §5.2). It is a pure
// function of its arguments. It walks the queued jobs in queue order (Order,
// then ID):
//
//  1. A job whose claim exceeds a cap is skipped and waits "over this host's
//     caps"; it is never the head. A job being sized (Record.Sizing) waits
//     with Sizing.Wait and is never the head either.
//  2. The head is the first job that is not skipped. Once it has waited
//     p.StarveAfter since it joined the queue (Record.QueuedSince: its
//     release, for a dependent), Admit starts it if it fits and starts
//     nothing else, so smaller jobs cannot pass a large one forever.
//  3. Otherwise (first fit) it starts every job, in order, whose claim fits
//     what the running jobs and the jobs started before it leave.
//
// Agent jobs (Kind KindAgent) are apart: each starts, in queue order, while
// fewer than p.AgentJobs of its account run; they take nothing from the caps
// and are never the head.
//
// Each job left waiting gets its reason in Decision.Waiting. Only Queued and
// Running records count, so a held job is never seen; a Running exclusive
// claim takes every cap as it stands, and any other running job counts at
// its hold in holds (past-peak holds spec §6), at its claim with none. A
// policy without caps (the zero Policy) starts nothing. The jobs slice is
// left as it is.
func Admit(jobs []Record, p Policy, now time.Time, holds map[JobID]Need) Decision {
	d := Decision{Waiting: map[JobID]string{}}
	var used Need
	var queued, agents []Record
	running := map[string]int{} // running agent jobs per account key
	for _, r := range jobs {
		switch {
		case r.Kind == KindAgent && r.State == Running:
			running[r.AccountKey]++
		case r.Kind == KindAgent && r.State == Queued:
			agents = append(agents, r)
		case r.State == Running:
			used = used.plus(runningNeed(r, p, holds))
		case r.State == Queued:
			queued = append(queued, r)
		}
	}
	byOrder := func(a, b Record) int { return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID)) }
	// Agent jobs (flow in loom part 3a, spec §5.2) start by their account's
	// limit, never by the caps, and are never a head; while admission has
	// no usable policy they wait with no reason, so the view shows its.
	slices.SortFunc(agents, byOrder)
	for _, r := range agents {
		switch {
		case !p.usable():
		case running[r.AccountKey] >= p.AgentJobs:
			d.Waiting[r.ID] = fmt.Sprintf("waits for an agent slot (%d of %d running on this account)", running[r.AccountKey], p.AgentJobs)
		default:
			running[r.AccountKey]++
			d.Start = append(d.Start, r.ID)
		}
	}
	slices.SortFunc(queued, byOrder)

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
		line = append(line, r)
	}
	if len(line) == 0 {
		return d
	}

	if head := line[0]; now.Sub(head.QueuedSince()) >= p.StarveAfter {
		if why := shortfall(used, head.Claim.Need(p.Caps), p.Caps); why == "" {
			d.Start = append(d.Start, head.ID)
		} else {
			d.Waiting[head.ID] = why
		}
		for _, r := range line[1:] {
			d.Waiting[r.ID] = fmt.Sprintf("waits for job %d, which has waited past %dm", head.ID, int(p.StarveAfter/time.Minute))
		}
		return d
	}

	for _, r := range line {
		need := r.Claim.Need(p.Caps)
		if why := shortfall(used, need, p.Caps); why != "" {
			d.Waiting[r.ID] = why
			continue
		}
		used = used.plus(need)
		d.Start = append(d.Start, r.ID)
	}
	return d
}

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

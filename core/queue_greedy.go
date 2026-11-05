package core

import (
	"time"

	"github.com/aidan-bailey/loom/sched"
)

// kbPerGB is a GB in KB.
const kbPerGB = 1 << 20

// usualOf is a job's usual duration at its claimed cores, and whether it has
// one (the view's forecast and greedy admission share it).
func (m *Model) usualOf(r sched.Record) (time.Duration, bool) {
	u, _, n, fromPrior := sched.Usual(m.queue.beliefOf(r.Signature), r.Claim.CPUs)
	return u, n > 0 || fromPrior
}

// greedyInputs is what greedy admission needs now (cgroup spec §7), and
// false in fallback mode or when the host's memory can't be read. It reads
// MemAvailable fresh (a /proc file, no process), since an idle queue has no
// pass to read it.
func (m *Model) greedyInputs(p sched.Policy, now time.Time) (sched.Greedy, bool) {
	q := m.queue
	if q.containWhy(p) != "" {
		return sched.Greedy{}, false
	}
	avail, err := readMemAvailable()
	if err != nil || avail <= 0 {
		return sched.Greedy{}, false
	}
	fallback := m.hostUsual()
	expect := func(r sched.Record) time.Duration {
		if d, ok := m.usualOf(r); ok && d > 0 {
			return d
		}
		return fallback
	}
	g := sched.Greedy{Running: map[sched.JobID]sched.Prediction{}}
	var used int64
	for i := range q.store.Jobs {
		rec := q.store.Jobs[i]
		if rec.State != sched.Running {
			continue
		}
		used += q.usage[rec.ID].RSSKB
		g.Running[rec.ID] = m.predictRunning(rec, now, expect)
	}
	g.BudgetKB = sched.Budget(used, avail, p)
	g.Predict = func(r sched.Record, start time.Time) sched.Prediction { return m.predictNew(r, start, expect) }
	if q.slice != nil {
		g.CPUPressure = q.slice.CPUPressure
	}
	return g, true
}

// predictRunning is a running job's prediction from now (cgroup spec §7.2):
// a contained job whose command has curves follows them from its progress;
// any other counts flat at its hold or claim until its expected end; and a
// job using more than predicted counts flat at its use plus the margin.
func (m *Model) predictRunning(rec sched.Record, now time.Time, expect func(sched.Record) time.Duration) sched.Prediction {
	q := m.queue
	end := rec.Started.Add(expect(rec))
	if !end.After(now) {
		end = now.Add(time.Minute)
	}
	var pred sched.Prediction
	ok := false
	if tr := q.traces[rec.ID]; rec.Cgroup != "" && !rec.Adopted && tr != nil && len(tr.Points) > 0 {
		pred, ok = sched.Predict(sched.CurvedRuns(q.beliefOf(rec.Signature)), tr.Points[len(tr.Points)-1].X, now)
	}
	if !ok {
		gb := rec.Claim.MemoryGB
		if h, held := q.holding[rec.ID]; held {
			gb = h.MemoryGB
		}
		pred = sched.Flat(int64(gb)*kbPerGB, now, end)
	}
	if use := sched.WithMarginKB(q.usage[rec.ID].RSSKB); use > pred.At(now) {
		pred = sched.Flat(use, now, later(end, pred.End()))
	}
	return pred
}

// predictNew is a job's prediction from start: its command's curves, or flat
// at its claim for its expected duration.
func (m *Model) predictNew(r sched.Record, start time.Time, expect func(sched.Record) time.Duration) sched.Prediction {
	if pred, ok := sched.Predict(sched.CurvedRuns(m.queue.beliefOf(r.Signature)), 0, start); ok {
		return pred
	}
	return sched.Flat(int64(r.Claim.MemoryGB)*kbPerGB, start, start.Add(expect(r)))
}

// later is the later of a and b.
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

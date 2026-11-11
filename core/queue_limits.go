package core

import (
	"path/filepath"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// usageLimitRead is what the log of an agent job that failed says of a
// usage limit, back from the job that read it off the loop.
type usageLimitRead struct {
	id      sched.JobID
	account string
	limit   sched.UsageLimit
	limited bool
	err     error
}

// readUsageLimit reads, off the loop, whether rec stopped on a usage limit
// (usage-limit hold spec §2): only an agent job that failed is read, and
// only its log's tail. It returns no job for any other.
func (m *Model) readUsageLimit(rec sched.Record) Job {
	if rec.Kind != sched.KindAgent || rec.State != sched.Failed {
		return nil
	}
	id, account := rec.ID, rec.AccountKey
	logPath := filepath.Join(m.queue.jobDir(id), sched.LogFile)
	return func() any {
		data, _, err := sched.ReadLogEnd(logPath, sched.UsageLimitTail)
		if err != nil {
			return usageLimitRead{id: id, account: account, err: err}
		}
		limit, limited := sched.UsageLimitOf(data)
		return usageLimitRead{id: id, account: account, limit: limit, limited: limited}
	}
}

// deliverUsageLimit records an account's usage limit when the read found
// one whose reset is still ahead, keeping the later reset of two, and runs
// an admission pass so the account's queued agents say why they wait. A
// limit with no reset holds nothing here: the engine asks its manager.
func (m *Model) deliverUsageLimit(r usageLimitRead) {
	q := m.queue
	if r.err != nil {
		log.For("core").Warn("queue.usage_limit_unread", "job", int64(r.id), "err", r.err)
		return
	}
	if !r.limited {
		return
	}
	now := time.Now()
	log.For("core").Info("queue.usage_limit", "job", int64(r.id), "account", r.account,
		"window", r.limit.Window, "resets", r.limit.Resets)
	if r.limit.Resets.IsZero() || !now.Before(r.limit.Resets.Add(sched.LimitMargin)) {
		return
	}
	if old, ok := q.limits[r.account]; ok && !old.Resets.Before(r.limit.Resets) {
		return
	}
	q.limits[r.account] = r.limit
	q.moved()
	if q.hasQueued() {
		m.admitJobs()
	}
}

// pruneLimits drops the account limits whose hold has ended.
func (q *queueState) pruneLimits(now time.Time) {
	for k, u := range q.limits {
		if !now.Before(u.Resets.Add(sched.LimitMargin)) {
			delete(q.limits, k)
		}
	}
}

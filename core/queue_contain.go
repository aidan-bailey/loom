package core

import (
	"context"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

const (
	probeEvery     = 60 * time.Second // a probe's retry while jobs run uncontained
	confineTimeout = 5 * time.Second  // one D-Bus call's bound
	bootProbeLimit = 2 * time.Second  // the boot's probe, which delays the daemon's start
)

// newConfiner reaches the user's service manager; nil makes no probe (core's
// tests, which install a fake through SetConfinerForTest instead).
var newConfiner = sched.NewConfiner

// containWhy is why jobs started now run uncontained (fallback mode, cgroup
// spec §5.5), "" when they are contained.
func (q *queueState) containWhy(p sched.Policy) string {
	switch {
	case queueGOOS != "linux":
		return "cgroups are Linux only"
	case !p.Contain:
		return "scheduler.contain is off"
	case q.confiner != nil:
		return ""
	case q.confineErr != nil:
		return q.confineErr.Error()
	}
	return "not connected to the user's systemd yet"
}

// confinerProbed is a probe's result.
type confinerProbed struct {
	c   sched.Confiner
	err error
}

// probeConfinerAtBoot reaches for the user's service manager before the
// queue's first admission pass, so the jobs it starts are contained. It runs
// in Boot, before the loop serves any client, bounded by bootProbeLimit; a
// failure leaves maybeProbeConfiner to retry.
func (m *Model) probeConfinerAtBoot() {
	q := m.queue
	policy, paused := m.refreshPolicy()
	if newConfiner == nil || paused != "" || queueGOOS != "linux" || !policy.Contain || q.confiner != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), bootProbeLimit)
	defer cancel()
	q.lastProbe = time.Now()
	c, err := newConfiner(ctx)
	if err != nil {
		m.confinerFailed(err)
		return
	}
	q.confiner, q.confineErr = c, nil
	log.For("core").Info("queue.contained")
}

// maybeProbeConfiner reaches for the user's service manager again from a
// job, every probeEvery while jobs wait and start uncontained with contain
// on; an idle queue makes no call.
func (m *Model) maybeProbeConfiner(p sched.Policy) {
	q := m.queue
	if newConfiner == nil || queueGOOS != "linux" || !p.Contain || q.confiner != nil || q.probing || !q.hasQueued() ||
		(!q.lastProbe.IsZero() && time.Since(q.lastProbe) < probeEvery) {
		return
	}
	q.probing, q.lastProbe = true, time.Now()
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		c, err := newConfiner(ctx)
		return confinerProbed{c: c, err: err}
	})
}

// deliverConfinerProbed keeps a probe's confiner, or why there is none, and
// runs an admission pass with what it found.
func (m *Model) deliverConfinerProbed(r confinerProbed) {
	q := m.queue
	q.probing = false
	switch {
	case q.confiner != nil:
		// A confiner arrived meanwhile (a test's): it stands.
		if r.c != nil {
			r.c.Close()
		}
	case r.err != nil:
		m.confinerFailed(r.err)
	default:
		q.confiner, q.confineErr = r.c, nil
		log.For("core").Info("queue.contained")
		q.moved()
	}
	m.admitJobs()
}

// confinerFailed drops the confiner after a D-Bus call failed: jobs start
// uncontained until the next probe reaches systemd again.
func (m *Model) confinerFailed(err error) {
	q := m.queue
	if q.confineErr == nil || q.confineErr.Error() != err.Error() {
		log.For("core").Warn("queue.uncontained", "err", err)
	}
	if q.confiner != nil {
		q.confiner.Close()
	}
	q.confiner, q.confineErr = nil, err
	q.moved()
}

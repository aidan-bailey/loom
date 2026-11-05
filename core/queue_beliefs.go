package core

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// loadBeliefs loads beliefs.json beside jobs.json (smart scheduler spec
// §5.3). A file that fails to load is left untouched and never written:
// learning is off, and a notice tells the next TUI.
func (m *Model) loadBeliefs() {
	q := m.queue
	b, err := sched.LoadBeliefs(q.dir)
	if err != nil {
		q.beliefsErr = err
		log.For("core").Error("queue.beliefs_load_failed", "dir", q.dir, "err", err)
		m.notifyErr(fmt.Errorf("work queue: learning is off: %s; beliefs.json is left as it is", learningWhy(err)))
		return
	}
	q.beliefs = b
	// A profile or curve that fails validation is ignored (past-peak holds
	// spec §5.2, cgroup spec §6.2): say so once per signature and kind, and
	// leave the file as it is.
	for _, b := range q.beliefs.Beliefs {
		profileSaid, curveSaid := false, false
		for _, r := range b.Runs {
			if err := validProfile(r.Profile); err != nil && !profileSaid {
				log.For("core").Warn("queue.profile_invalid", "signature", string(b.ID), "job", int64(r.Job), "err", err)
				profileSaid = true
			}
			if err := validCurve(r.Curve); err != nil && !curveSaid {
				log.For("core").Warn("queue.curve_invalid", "signature", string(b.ID), "job", int64(r.Job), "err", err)
				curveSaid = true
			}
		}
	}
}

// validProfile is why p can't be trusted, nil for none or a sound one.
func validProfile(p *sched.Profile) error {
	if p == nil {
		return nil
	}
	return p.Validate()
}

// validCurve is why c can't be trusted, nil for none or a sound one.
func validCurve(c *sched.Curve) error {
	if c == nil {
		return nil
	}
	return c.Validate()
}

// learningWhy is why a beliefs.json load error turns learning off.
func learningWhy(err error) string {
	if errors.Is(err, sched.ErrNewerBeliefs) {
		return err.Error()
	}
	return "beliefs.json could not be loaded: " + err.Error()
}

// learningOff is why learning is off, "" while it is on.
func (q *queueState) learningOff() string {
	if q.beliefsErr == nil {
		return ""
	}
	return learningWhy(q.beliefsErr)
}

// saveBeliefsQuiet writes beliefs.json after a change the model made: when
// the write fails the change stays in memory, a Notice says so once for every
// client, and the next save writes it. A registry that failed to load is
// never written.
func (m *Model) saveBeliefsQuiet(after string) {
	q := m.queue
	if !q.ready() || q.beliefsErr != nil {
		return
	}
	if err := sched.SaveBeliefs(q.dir, q.beliefs); err != nil {
		log.For("core").Warn("queue.beliefs_save_failed", "after", after, "err", err)
		if !q.beliefsSaveFailed {
			q.beliefsSaveFailed = true
			m.causedBy(0, func() {
				m.notifyErr(fmt.Errorf("work queue: write beliefs.json: %w; the change is kept in memory and written with the next save", err))
			})
		}
		return
	}
	q.beliefsSaveFailed = false
}

// jobSignature is the signature of argv run in cwd with the checkout it
// runs in, or why there is none (spec §5.1). It reads git's files, never
// runs git, so the loop can call it.
func jobSignature(argv []string, cwd string) (sig *sched.Signature, checkout, unsized string) {
	repo, folder, checkout, err := sched.FindRepo(cwd)
	if err != nil {
		return nil, "", err.Error()
	}
	s := sched.NewSignature(repo, folder, argv)
	return &s, checkout, ""
}

// beliefOf is the belief of signature id, nil for none: no signature,
// learning off, or a belief forgotten.
func (q *queueState) beliefOf(id sched.SignatureID) *sched.Belief {
	if id == "" || q.beliefsErr != nil {
		return nil
	}
	return q.beliefs.Find(id)
}

// sizeRecord sets rec's claim from what it asked for, its belief and p
// (spec §5.4), and reports whether anything changed.
func (q *queueState) sizeRecord(rec *sched.Record, p sched.Policy) bool {
	s := sched.Size(rec.Asked, q.beliefOf(rec.Signature), p)
	if rec.Claim == s.Claim && rec.Sources == s.Sources && rec.Learned == s.Learned &&
		rec.AboveCap == s.AboveCap && rec.BelowKB == s.BelowKB {
		return false
	}
	rec.Claim, rec.Sources, rec.Learned, rec.AboveCap, rec.BelowKB = s.Claim, s.Sources, s.Learned, s.AboveCap, s.BelowKB
	return true
}

// resizeQueued recomputes the claim of every queued or held job this build sized,
// from its belief and p: a claim stays live until the job starts (spec
// §5.4). A record queued by a build before sizing (no Sources) keeps its
// claim.
func (m *Model) resizeQueued(p sched.Policy) {
	q := m.queue
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if !rec.State.Waiting() || rec.Sources == (sched.ClaimSources{}) {
			continue
		}
		if q.sizeRecord(rec, p) {
			q.moved()
		}
	}
}

// readBeliefs is the page query selects: copies, most recently seen first.
// It refuses while learning is off, and an ID it doesn't hold.
func (m *Model) readBeliefs(query sched.BeliefQuery) (sched.BeliefsPage, error) {
	q := m.queue
	switch {
	case !q.ready():
		return sched.BeliefsPage{}, errQueueNotLoaded
	case q.beliefsErr != nil:
		return sched.BeliefsPage{}, fmt.Errorf("learning is off: %s", q.learningOff())
	}
	page := sched.BeliefsPage{Track: sched.TrackRecord(q.beliefs.Beliefs)}
	for _, b := range q.beliefs.Beliefs {
		if query.Matches(b) {
			v := sched.ViewBelief(b.Clone(), q.policy)
			if b.After != "" && b.After != sched.AfterNone {
				if p := q.beliefs.Find(b.After); p != nil {
					v.After = slices.Clone(p.Command)
				}
			}
			page.Beliefs = append(page.Beliefs, v)
		}
	}
	if query.ID != "" && len(page.Beliefs) == 0 {
		return sched.BeliefsPage{}, fmt.Errorf("no belief %s", query.ID)
	}
	slices.SortStableFunc(page.Beliefs, func(a, b sched.BeliefView) int { return b.Belief.LastSeen.Compare(a.Belief.LastSeen) })
	return page, nil
}

// forgetBelief drops belief id and writes beliefs.json; a failed write
// refuses the request and keeps the belief. Queued jobs of its signature
// get a new, empty belief and are re-sized by the pass that follows.
func (m *Model) forgetBelief(id sched.SignatureID) error {
	q := m.queue
	switch {
	case !q.ready():
		return errQueueNotLoaded
	case q.beliefsErr != nil:
		return fmt.Errorf("learning is off: %s", q.learningOff())
	}
	b := q.beliefs.Find(id)
	if b == nil {
		return fmt.Errorf("no belief %s", id)
	}
	kept := b.Clone()
	q.beliefs.Forget(id)
	if err := sched.SaveBeliefs(q.dir, q.beliefs); err != nil {
		q.beliefs.Beliefs = append(q.beliefs.Beliefs, kept)
		return fmt.Errorf("write beliefs.json: %w", err)
	}
	if slices.ContainsFunc(q.store.Jobs, func(r sched.Record) bool { return r.State.Waiting() && r.Signature == id }) {
		q.beliefs.Ensure(kept.Signature, time.Now())
		m.saveBeliefsQuiet("forget")
	}
	q.moved()
	m.admitJobs()
	return nil
}

// recordRun records what settled job rec teaches its belief (spec §5.2):
// nothing for a job without a signature, while learning is off, or for a
// run that taught nothing.
func (m *Model) recordRun(rec sched.Record, trace *sched.Trace) {
	q := m.queue
	b := q.beliefOf(rec.Signature)
	if b == nil {
		return
	}
	run, ok := sched.RunOf(rec)
	if !ok {
		return
	}
	if run.FullPeak() {
		if p, ok := sched.BuildProfile(trace); ok {
			run.Profile = p
		}
		if c, ok := sched.BuildCurve(trace, rec.Started); ok {
			run.Curve = c
		}
	}
	now := time.Now()
	sched.ScorePrior(b, run)
	surprise := sched.Surprise(b, run, rec.Sources.Memory)
	b.AddRun(run)
	if surprise != "" && now.Sub(b.Advisor.ReconciledAt) >= reconcileEvery && m.advisorOff(now) == "" {
		b.Advisor.ReconciledAt = now
		m.queueAdvice(advisorCall{kind: adviseReconcile, sig: b.ID, job: rec.ID, queued: now, surprise: surprise, run: run})
	}
	m.saveBeliefsQuiet("settle")
}

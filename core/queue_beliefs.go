package core

import (
	"errors"
	"fmt"

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

// jobSignature is the signature of argv run in cwd, or why there is none
// (spec §5.1). It reads git's files, never runs git, so the loop can call it.
func jobSignature(argv []string, cwd string) (*sched.Signature, string) {
	repo, folder, err := sched.FindRepo(cwd)
	if err != nil {
		return nil, err.Error()
	}
	sig := sched.NewSignature(repo, folder, argv)
	return &sig, ""
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

// resizeQueued recomputes the claim of every queued job this build sized,
// from its belief and p: a claim stays live until the job starts (spec
// §5.4). A record queued by a build before sizing (no Sources) keeps its
// claim.
func (m *Model) resizeQueued(p sched.Policy) {
	q := m.queue
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State != sched.Queued || rec.Sources == (sched.ClaimSources{}) {
			continue
		}
		if q.sizeRecord(rec, p) {
			q.moved()
		}
	}
}

// recordRun records what settled job rec teaches its belief (spec §5.2):
// nothing for a job without a signature, while learning is off, or for a
// run that taught nothing.
func (m *Model) recordRun(rec sched.Record) {
	q := m.queue
	b := q.beliefOf(rec.Signature)
	if b == nil {
		return
	}
	run, ok := sched.RunOf(rec)
	if !ok {
		return
	}
	b.AddRun(run)
	m.saveBeliefsQuiet("settle")
}

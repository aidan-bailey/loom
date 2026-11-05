package core

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/sched/advisor"
)

// Dependent jobs (docs/superpowers/specs/2026-11-05-dependent-jobs-design.md):
// a job submitted --after another is held until that one ends. The end
// releases the jobs held after it when it is done and cancels them, down the
// chain, when it ended any other way; both happen inside its settle
// (settledJob, settleChain), before any admission pass.

// chainLink is one job a cascade cancelled, with the job it waited on.
type chainLink struct {
	id, after sched.JobID
}

// heldAfter is the IDs of the held jobs that wait on job id, in ID order.
func (q *queueState) heldAfter(id sched.JobID) []sched.JobID {
	var ids []sched.JobID
	for _, r := range q.store.Jobs {
		if r.State == sched.Held && r.After == id {
			ids = append(ids, r.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

// heldChain is the IDs of every job held down job id's chain: those that
// wait on it, those that wait on them, and so on, in ID order.
func (q *queueState) heldChain(id sched.JobID) []sched.JobID {
	var out []sched.JobID
	for next := []sched.JobID{id}; len(next) > 0; {
		var more []sched.JobID
		for _, n := range next {
			more = append(more, q.heldAfter(n)...)
		}
		out = append(out, more...)
		next = more
	}
	slices.Sort(out)
	return out
}

// prerequisiteOf is the record a submission names with --after (spec §5.1),
// a copy, or nil for none (after 0). It refuses a job it has no record of, a
// job with another owner than the submitter's (owner 0, a plain shell, is
// an owner too), and a job that ended other than done.
func (q *queueState) prerequisiteOf(after sched.JobID, owner InstanceID) (*sched.Record, error) {
	if after == 0 {
		return nil, nil
	}
	i := q.index(after)
	if i < 0 {
		return nil, fmt.Errorf("there is no job %d", after)
	}
	p := q.store.Jobs[i]
	switch {
	case InstanceID(p.Owner) != owner:
		return nil, fmt.Errorf("job %d has another owner: --after takes a job with the same owner as the new one", after)
	case p.State.Finished() && p.State != sched.Done:
		return nil, fmt.Errorf("job %d %s, so a job after it would never run", after, endedHow(p))
	}
	return &p, nil
}

// afterPart is the prerequisite part of a dependent's signature (spec
// §5.6): its prerequisite's signature, or sched.AfterNone when it has none.
func afterPart(prereq sched.Record) sched.SignatureID {
	if prereq.Signature == "" {
		return sched.AfterNone
	}
	return prereq.Signature
}

// endedHow is how a job that did not succeed ended, as a refusal and a
// cascade's reason say it: "failed (exit 101)", "was killed by SIGSEGV",
// "could not start", "was cancelled" or "was lost".
func endedHow(r sched.Record) string {
	switch {
	case r.State == sched.Cancelled:
		return "was cancelled"
	case r.State == sched.Lost:
		return "was lost"
	case startFailed(r.State, r.Failure, r.Exit):
		return "could not start"
	case r.Exit != nil && r.Exit.Signal != "":
		return "was killed by " + r.Exit.Signal
	case r.Exit != nil:
		return fmt.Sprintf("failed (exit %d)", r.Exit.Code)
	}
	return "failed"
}

// cascadeReason is why root's end cancels the jobs held after it (spec
// §5.4): "because job 14 failed (exit 101)", and so on (endedHow).
func cascadeReason(root sched.Record) string {
	return fmt.Sprintf("because job %d %s", root.ID, endedHow(root))
}

// settleChain does what rec's end does to the jobs held after it, and sends
// rec's message (spec §5.3–§5.5). Done, it releases them, and its message
// goes with theirs (carried); any other end cancels them down the chain, and
// one message per branch says so. With none held after it, rec's message
// goes as it always did.
func (m *Model) settleChain(rec sched.Record) {
	now := time.Now()
	if rec.State == sched.Done {
		if released := m.releaseDependents(rec, now); len(released) > 0 {
			m.carry(rec.ID, released)
			return
		}
		m.sendJobNotice(rec)
		return
	}
	if links := m.cascade(rec, now); len(links) > 0 {
		m.sendCascadeNotices(rec, links)
		return
	}
	m.sendJobNotice(rec)
}

// releaseDependents releases the held jobs that wait on rec, which ended
// done (spec §5.3): each joins the queue now, at the lower of its order and
// rec's, so it keeps its chain's place in line. It returns their IDs in ID
// order.
func (m *Model) releaseDependents(rec sched.Record, now time.Time) []sched.JobID {
	q := m.queue
	ids := q.heldAfter(rec.ID)
	for _, id := range ids {
		d := &q.store.Jobs[q.index(id)]
		d.State, d.Ready, d.Order = sched.Queued, now, min(d.Order, rec.Order)
	}
	if len(ids) > 0 {
		q.moved()
	}
	return ids
}

// cascade cancels the held jobs that wait on root, which ended other than
// done (spec §5.4), and the jobs held after those, depth first in ID order.
// Each keeps a reason it already has (cancelOwnerJobs marks them first) and
// otherwise gets the one its prerequisite's end gives. It sends no message
// (sendCascadeNotices does) and returns what it cancelled, each with the job
// it waited on, in the order it cancelled them: a job always after the job
// it waited on.
func (m *Model) cascade(root sched.Record, now time.Time) []chainLink {
	q := m.queue
	var links []chainLink
	var walk func(parent sched.JobID, reason string)
	walk = func(parent sched.JobID, reason string) {
		for _, id := range q.heldAfter(parent) {
			d := &q.store.Jobs[q.index(id)]
			if d.CancelReason == "" {
				d.CancelReason = reason
			}
			endJob(d, sched.Cancelled, now)
			links = append(links, chainLink{id: id, after: parent})
			walk(id, fmt.Sprintf("because job %d was cancelled", id))
		}
	}
	walk(root.ID, cascadeReason(root))
	if len(links) > 0 {
		q.moved()
	}
	return links
}

// wantsNotice reports whether rec would send its owner a message now: it
// asked for one, has an owner, and its notice has no fate yet.
func wantsNotice(rec sched.Record) bool {
	return rec.Notify && rec.Owner != 0 && rec.Notice == sched.NoticeNone
}

// carry records that job id's message goes with the messages of the jobs by
// (spec §5.5), when it would send one. The settle's save writes it.
func (m *Model) carry(id sched.JobID, by []sched.JobID) {
	q := m.queue
	i := q.index(id)
	if i < 0 || !wantsNotice(q.store.Jobs[i]) {
		return
	}
	q.store.Jobs[i].Notice, q.store.Jobs[i].NoticeWhy = sched.NoticeCarried, "its result goes with "+jobsPossessive(by)
	q.moved()
}

// jobsPossessive is "job 15's", or "jobs 15 and 16's".
func jobsPossessive(ids []sched.JobID) string {
	words := make([]string, len(ids))
	for i, id := range ids {
		words[i] = strconv.FormatInt(int64(id), 10)
	}
	if len(words) == 1 {
		return "job " + words[0] + "'s"
	}
	return "jobs " + sched.JoinAnd(words) + "'s"
}

// sendCascadeNotices sends the messages of the cascade root's end set off
// (spec §5.5), one per branch, each carrying root's log. root sends its own
// when it notifies, with a line naming every job the cascade cancelled, each
// of which it then carries. Otherwise each job the cascade cancelled that
// notifies sends one, naming root's end and the jobs below it, unless a job
// above it already does, which then carries it.
func (m *Model) sendCascadeNotices(root sched.Record, links []chainLink) {
	q := m.queue
	parent := make(map[sched.JobID]sched.JobID, len(links))
	for _, l := range links {
		parent[l.id] = l.after
	}
	// below is the cascade's jobs under top, in ID order.
	below := func(top sched.JobID) []sched.JobID {
		var out []sched.JobID
		for _, l := range links {
			for p, ok := l.after, true; ok; p, ok = parent[p] {
				if p == top {
					out = append(out, l.id)
					break
				}
			}
		}
		slices.Sort(out)
		return out
	}
	reporter := map[sched.JobID]sched.JobID{}
	if wantsNotice(root) {
		reporter[root.ID] = root.ID
	}
	for _, l := range links {
		if rep := reporter[l.after]; rep != 0 {
			reporter[l.id] = rep
			m.carry(l.id, []sched.JobID{rep})
			continue
		}
		if wantsNotice(q.store.Jobs[q.index(l.id)]) {
			reporter[l.id] = l.id
		}
	}
	if reporter[root.ID] == root.ID {
		m.sendNotice(root, m.jobHead(root)+"\n"+m.cancelledLine(below(root.ID)), root.ID)
	}
	for _, l := range links {
		if reporter[l.id] != l.id {
			continue
		}
		rec := q.store.Jobs[q.index(l.id)]
		head := fmt.Sprintf("[loom] job %d %q was cancelled because job %d %q %s.", rec.ID, rec.Description, root.ID, root.Description, endedHow(root))
		if under := below(l.id); len(under) > 0 {
			head += "\n" + m.cancelledLine(under)
		}
		m.sendNotice(rec, head, root.ID)
	}
}

// cancelledLine names the jobs a cascade cancelled below a message's job
// (spec §5.5): `Job 15 "run tests", which waited on it, was cancelled.`, or
// `Jobs 15 "run tests" and 16 "bench", which waited on it, were cancelled.`
func (m *Model) cancelledLine(ids []sched.JobID) string {
	q := m.queue
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = fmt.Sprintf("%d %q", id, q.store.Jobs[q.index(id)].Description)
	}
	if len(names) == 1 {
		return "Job " + names[0] + ", which waited on it, was cancelled."
	}
	return "Jobs " + sched.JoinAnd(names) + ", which waited on it, were cancelled."
}

// resolveHeld settles, at boot, every held job whose prerequisite a previous
// daemon left settled or never recorded (spec §5.7), in ID order, so a chain
// resolves from its first job down. First it cancels, down their chains, the
// held jobs a cancel was asked for (an older loom's pause can ask without
// ending one), so no release below takes one of them back into the queue.
// Then a held job is released when its prerequisite is done, and cancelled,
// down its chain, when that ended any other way or when it names no record
// (an older loom's save drops `after`). One whose prerequisite is queued,
// held or running stays held. loadQueue runs it before adoptJobs, whose
// settles release and cancel through settledJob, and whose afterSettle runs
// an admission pass.
func (m *Model) resolveHeld() {
	q := m.queue
	now := time.Now()
	changed := false
	// held is the held jobs match selects, in ID order.
	held := func(match func(sched.Record) bool) []sched.JobID {
		var ids []sched.JobID
		for _, r := range q.store.Jobs {
			if r.State == sched.Held && match(r) {
				ids = append(ids, r.ID)
			}
		}
		slices.Sort(ids)
		return ids
	}
	// end cancels held job id with reason (a reason it has stands), unless
	// a cascade above it has ended it.
	end := func(id sched.JobID, reason string) {
		i := q.index(id)
		if i < 0 || q.store.Jobs[i].State != sched.Held {
			return
		}
		if q.store.Jobs[i].CancelReason == "" {
			q.store.Jobs[i].CancelReason = reason
		}
		endJob(&q.store.Jobs[i], sched.Cancelled, now)
		m.settledJob(i)
		changed = true
	}
	for _, id := range held(func(r sched.Record) bool { return r.CancelReason != "" }) {
		end(id, "")
	}
	for _, id := range held(func(sched.Record) bool { return true }) {
		i := q.index(id)
		if q.store.Jobs[i].State != sched.Held {
			continue // released or cancelled above
		}
		p := -1
		if after := q.store.Jobs[i].After; after != 0 {
			p = q.index(after)
		}
		switch {
		case p < 0:
			end(id, "because its prerequisite is unknown")
		case q.store.Jobs[p].State == sched.Done:
			m.releaseDependents(q.store.Jobs[p], now)
			changed = true
		case q.store.Jobs[p].State.Finished():
			end(id, cascadeReason(q.store.Jobs[p]))
		}
	}
	if changed {
		m.saveQueueQuiet("boot")
	}
}

// heldWaiting is why a held job waits (spec §6): "after job 14 (running)",
// with the prerequisite's state, or "(gone)" when its record is.
func (q *queueState) heldWaiting(r sched.Record) string {
	state := "gone"
	if i := q.index(r.After); i >= 0 {
		state = string(q.store.Jobs[i].State)
	}
	return fmt.Sprintf("after job %d (%s)", r.After, state)
}

// advisorAfter is what the advisor is told of rec's prerequisite (spec
// §5.6): its command as its belief normalizes it (its argv when it has no
// belief) and its description; nil for a job with no prerequisite, or one
// whose prerequisite's record is gone.
func (m *Model) advisorAfter(rec sched.Record) *advisor.After {
	if rec.After == 0 {
		return nil
	}
	i := m.queue.index(rec.After)
	if i < 0 {
		return nil
	}
	p := m.queue.store.Jobs[i]
	command := slices.Clone(p.Argv)
	if b := m.queue.beliefOf(p.Signature); b != nil {
		command = slices.Clone(b.Command)
	}
	return &advisor.After{Command: command, Description: p.Description}
}

package core

import (
	"errors"
	"fmt"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
)

// CancelFrom says where a CancelJob came from, for the wording of the
// cancel in the job's notice and the follower's line.
type CancelFrom string

const (
	// CancelFromCLI is a cancel made with loom job cancel.
	CancelFromCLI CancelFrom = "cli"
	// CancelFromView is a cancel made in the TUI's work view.
	CancelFromView CancelFrom = "view"
)

// cancelReasons is what follows "was cancelled" for each source.
var cancelReasons = map[CancelFrom]string{
	CancelFromCLI:  "with loom job cancel",
	CancelFromView: "from the work view",
}

// The work queue's requests: each runs one of the queue's operations
// (queue_ops.go) on the loop and answers at once, with the refusal when the
// operation refused. Every refusal matches ErrRefused. A request with
// ReqID 0 asks for no Reply and a refusal is only logged.

// SubmitJob adds a job to the queue. The Reply's Job is the new job's ID,
// and its Notice the operation's warning, if any: the submitter's launch is
// one no served session holds, so the job has no owner.
func (m *Model) SubmitJob(spec sched.Submit, req ReqID) {
	id, warning, err := m.submitJob(spec)
	if err != nil {
		m.refuse(req, 0, err)
		return
	}
	r := Reply{Job: id}
	if warning != "" {
		r.Notice = errors.New(warning)
	}
	m.reply(req, r)
}

// CancelJob cancels a queued or running job. from names the source, which
// words the cancel for the notice (cancelReasons); any other value is
// refused before the job is looked at.
func (m *Model) CancelJob(id sched.JobID, from CancelFrom, req ReqID) {
	reason, ok := cancelReasons[from]
	if !ok {
		m.refuse(req, 0, fmt.Errorf("cancel job %d: unknown source %q", id, from))
		return
	}
	m.answerQueueRequest(req, m.cancelJob(id, reason))
}

// MoveJobToFront puts a queued job first in the queue.
func (m *Model) MoveJobToFront(id sched.JobID, req ReqID) {
	m.answerQueueRequest(req, m.moveJobToFront(id))
}

// NotifyJob asks for a message to the job's owner when it ends.
func (m *Model) NotifyJob(id sched.JobID, req ReqID) {
	m.answerQueueRequest(req, m.notifyJob(id))
}

// SetSchedulerCaps validates s, writes it as the global config's scheduler
// block and runs an admission pass.
func (m *Model) SetSchedulerCaps(s config.SchedulerSettings, req ReqID) {
	m.answerQueueRequest(req, m.setSchedulerCaps(s))
}

// ReadBeliefs answers with the beliefs q selects, most recently seen first,
// and the advisor's track record (smart scheduler spec §7).
func (m *Model) ReadBeliefs(q sched.BeliefQuery, req ReqID) {
	page, err := m.readBeliefs(q)
	if err != nil {
		m.refuse(req, 0, err)
		return
	}
	m.reply(req, Reply{Beliefs: page})
}

// ForgetBelief drops a belief.
func (m *Model) ForgetBelief(id sched.SignatureID, req ReqID) {
	m.answerQueueRequest(req, m.forgetBelief(id))
}

// answerQueueRequest replies to a request the model answers at once: with
// the refusal when err is set, else with a success.
func (m *Model) answerQueueRequest(req ReqID, err error) {
	if err != nil {
		m.refuse(req, 0, err)
		return
	}
	m.reply(req, Reply{})
}

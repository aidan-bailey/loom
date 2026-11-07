package core

import (
	"fmt"
	"path/filepath"

	"github.com/aidan-bailey/loom/sched"
)

// jobLogLimit is the most of a job's log one ReadJobLog answers with.
const jobLogLimit = 64 << 10

// jobLogResult is ReadJobLog's job result.
type jobLogResult struct {
	read sched.LogRead
	err  error
}

// ReadJobLog reads job's output.log for session id in a job (sched.ReadLog:
// its tail when after < 0, else the bytes from after, at most jobLogLimit)
// and answers with the Reply's JobLog. The job must be in the queue and
// owned by id: a session reads its own jobs only. A job outlives every
// status of its session, so only a workspace terminal is refused.
func (m *Model) ReadJobLog(id InstanceID, job sched.JobID, after int64, req ReqID) {
	inst, _, ok := m.admit(opReadJobLog, id, req)
	if !ok {
		return
	}
	q := m.queue
	i := -1
	if q.ready() {
		i = q.index(job)
	}
	if i < 0 {
		m.refuse(req, id, fmt.Errorf("%s %s: job %d is no longer in the queue", opReadJobLog, inst.Title, job))
		return
	}
	if InstanceID(q.store.Jobs[i].Owner) != id {
		m.refuse(req, id, fmt.Errorf("%s %s: job %d is not this session's", opReadJobLog, inst.Title, job))
		return
	}
	m.spawn(m.track(req, id, readJobLogJob(filepath.Join(q.jobDir(job), sched.LogFile), after)))
}

// readJobLogJob is ReadJobLog's job: it reads only the file.
func readJobLogJob(path string, after int64) Job {
	return func() any {
		r, err := sched.ReadLog(path, after, jobLogLimit)
		return jobLogResult{read: r, err: err}
	}
}

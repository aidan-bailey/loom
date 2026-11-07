package web

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
)

// A session's work-queue jobs: the projection puts each owner's jobs on its
// session (jobs, jobs_label, jobs_focus), and GET …/jobs/{job}/log reads a
// job's output through Core.ReadJobLog, which reads only the session's own
// jobs.

// maxFinishedJobs is how many finished jobs a session carries; the queue
// itself keeps keep_finished (50 by default) across the host.
const maxFinishedJobs = 10

// exitJSON is a finished job's exit: its code (-1 for a signal) and the
// signal's name.
type exitJSON struct {
	Code   int    `json:"code"`
	Signal string `json:"signal,omitempty"`
}

// jobJSON is one of a session's jobs as the app reads it. It holds no
// sampled usage: the queue publishes at every sample, and a field that moved
// with the samples would republish the fleet each time.
type jobJSON struct {
	ID           sched.JobID `json:"id"`
	State        string      `json:"state"`
	Description  string      `json:"description"`
	Command      string      `json:"command"`
	Agent        string      `json:"agent"`
	Submitted    string      `json:"submitted"`
	Started      string      `json:"started"`
	Ended        string      `json:"ended"`
	Exit         *exitJSON   `json:"exit"`
	Failure      string      `json:"failure"`
	CancelReason string      `json:"cancel_reason"`
	Waiting      string      `json:"waiting"`
}

// sessionJobs is one session's jobs and its badge (core.JobBadges).
type sessionJobs struct {
	jobs  []jobJSON
	label string
	focus sched.JobID
}

// live reports whether a job in state s has yet to end.
func live(s sched.State) bool {
	return s == sched.Running || s == sched.Queued || s == sched.Held
}

// jobOf is a job as the app reads it.
func jobOf(j core.JobView) jobJSON {
	out := jobJSON{
		ID:           j.ID,
		State:        string(j.State),
		Description:  j.Description,
		Command:      strings.Join(j.Argv, " "),
		Agent:        j.Agent,
		Submitted:    stamp(j.Submitted),
		Started:      stamp(j.Started),
		Ended:        stamp(j.Ended),
		Failure:      j.Failure,
		CancelReason: j.CancelReason,
		Waiting:      j.Waiting,
	}
	if j.Exit != nil {
		out.Exit = &exitJSON{Code: j.Exit.Code, Signal: j.Exit.Signal}
	}
	return out
}

// jobsOf groups the queue's jobs by owner in the view's order (running,
// waiting, then finished newest first), keeping at most maxFinishedJobs
// finished ones each. A job with no owner is left out.
func jobsOf(q core.QueueView) map[core.InstanceID]sessionJobs {
	out := map[core.InstanceID]sessionJobs{}
	finished := map[core.InstanceID]int{}
	for _, j := range q.Jobs {
		if j.Owner == 0 {
			continue
		}
		if !live(j.State) {
			if finished[j.Owner] >= maxFinishedJobs {
				continue
			}
			finished[j.Owner]++
		}
		sj := out[j.Owner]
		sj.jobs = append(sj.jobs, jobOf(j))
		out[j.Owner] = sj
	}
	for id, b := range core.JobBadges(q) {
		sj := out[id]
		sj.label, sj.focus = b.Label, b.Focus
		out[id] = sj
	}
	return out
}

// withJobs is s with its jobs from p.jobs: an empty list and no badge for
// a session that owns none.
func (p *projection) withJobs(s sessionJSON) sessionJSON {
	sj := p.jobs[s.ID]
	s.Jobs = sj.jobs
	if s.Jobs == nil {
		s.Jobs = []jobJSON{}
	}
	s.JobsLabel, s.JobsFocus = sj.label, sj.focus
	return s
}

// setQueue takes the queue's view and reports whether the jobs the app
// reads changed; a view that moved only in its samples changes nothing.
func (p *projection) setQueue(q core.QueueView) bool {
	next := jobsOf(q)
	if reflect.DeepEqual(next, p.jobs) {
		return false
	}
	p.jobs = next
	for ws, list := range p.sessions {
		out := make([]sessionJSON, len(list))
		for i, s := range list {
			out[i] = p.withJobs(s)
		}
		p.sessions[ws] = out
	}
	return true
}

// jobLogJSON is a read of a job's log as the app reads it: text from the
// byte offset start to end (the next read's after), cut when the read began
// past the file's start, reset when it is a fresh tail that replaces what
// the app holds.
type jobLogJSON struct {
	Text  string `json:"text"`
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Cut   bool   `json:"cut"`
	Reset bool   `json:"reset"`
}

func jobLogOf(r sched.LogRead) jobLogJSON {
	return jobLogJSON{Text: cleanLog(r.Data), Start: r.Start, End: r.End, Cut: r.Cut, Reset: r.Reset}
}

// readJobLog answers GET /api/v1/sessions/{id}/jobs/{job}/log?after=: the
// job's output from the byte offset after, or its tail when after is
// absent, cleaned for display. The model refuses a job that is not the
// session's.
func (l *Layer) readJobLog(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "job_log"}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	job, after, err := jobLogQuery(r)
	if err != nil {
		l.refuse(w, a, http.StatusBadRequest, err.Error())
		return
	}
	reply, ok := l.read(w, r, a, func(req core.ReqID) { l.client.ReadJobLog(id, job, after, req) })
	if !ok {
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, jobLogOf(reply.JobLog))
}

// jobLogQuery reads the log route's job (a decimal ID above 0) and after (a
// byte offset; -1, the tail, when absent).
func jobLogQuery(r *http.Request) (sched.JobID, int64, error) {
	s := r.PathValue("job")
	job, err := strconv.ParseUint(s, 10, 64)
	if err != nil || job == 0 {
		return 0, 0, fmt.Errorf("job %q: want a job id", s)
	}
	after := int64(-1)
	if s := r.URL.Query().Get("after"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, fmt.Errorf("after %q: want a byte offset", s)
		}
		after = n
	}
	return sched.JobID(job), after, nil
}

// cleanLog is a log's bytes as text the app draws: terminal escapes dropped
// (CSI, OSC, and any other ESC with its intermediates and final byte),
// invalid UTF-8
// replaced, and of a line holding carriage returns only what follows the
// last (a progress bar's last state); a "\r\n" is a line end.
func cleanLog(b []byte) string {
	var out strings.Builder
	out.Grow(len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c != 0x1b {
			out.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(b) {
			break
		}
		switch b[i+1] {
		case '[':
			// CSI: parameters and intermediates up to a final byte 0x40–0x7E.
			j := i + 2
			for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
				j++
			}
			i = j + 1
		case ']':
			// OSC: up to BEL or ST (ESC \).
			j := i + 2
			for j < len(b) && b[j] != 0x07 && (b[j] != 0x1b || j+1 >= len(b) || b[j+1] != '\\') {
				j++
			}
			if j < len(b) && b[j] == 0x1b {
				j++
			}
			i = j + 1
		default:
			// nF and Fp/Fe/Fs: intermediates 0x20–0x2F, then one final byte
			// ("ESC ( B", "ESC =").
			j := i + 1
			for j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f {
				j++
			}
			i = j + 1
		}
	}
	text := out.String()
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	if !strings.Contains(text, "\r") {
		return text
	}
	lines := strings.Split(text, "\n")
	for k, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if i := strings.LastIndexByte(line, '\r'); i >= 0 {
			line = line[i+1:]
		}
		lines[k] = line
	}
	return strings.Join(lines, "\n")
}

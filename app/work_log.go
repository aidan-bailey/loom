package app

import (
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// workTailLines is how many of a log's last lines the tail keeps.
const workTailLines = 16

// workTailBytes is how much of a log's end the tail reads.
const workTailBytes = 64 << 10

// workPagerBytes is how much of a log's end the pager reads: a longer log
// shows its last part, and says so. The queue keeps no cap on a log's size,
// and the TUI must not read a gigabyte into memory.
const workPagerBytes = 4 << 20

// logStat is a log file's size and mtime (UnixNano) at a read. A zero one
// (known false) matches nothing: the file was absent or unreadable.
type logStat struct {
	known bool
	size  int64
	mtime int64
}

// statLog is path's logStat; zero when it cannot be stat'd.
func statLog(path string) logStat {
	st, err := os.Stat(path)
	if err != nil {
		return logStat{}
	}
	return logStat{known: true, size: st.Size(), mtime: st.ModTime().UnixNano()}
}

// workLogMsg is a log read's result: a job's tail, or all the pager can
// show of it.
type workLogMsg struct {
	id   sched.JobID
	full bool
	// seq orders the reads: see workState.seq.
	seq uint64
	// stat is the log's size and mtime when the read began, and unchanged
	// says they matched the ones the caller already holds, so nothing was
	// read and lines is empty.
	stat      logStat
	unchanged bool
	lines     []string
	omitted   bool
	err       string
}

// workLogRead issues the read of job's log for the tail or the pager,
// numbering it (workState.seq) and handing the Cmd the stat of what the
// view already holds for that log, if any.
func (m *home) workLogRead(job core.JobView, full bool) tea.Cmd {
	m.work.seq++
	var prev logStat
	if full {
		if p := m.work.pager; p != nil && p.id == job.ID && p.loaded && p.err == "" {
			prev = p.stat
		}
	} else if m.work.tail.ID == job.ID && m.work.tail.Err == "" {
		prev = m.work.tailStat
	}
	return workLogCmd(job.ID, job.LogPath, full, m.work.seq, prev)
}

// workLogCmd reads the end of the log at path for job id, unless the log's
// size and mtime are prev's: then the view already has what a read would
// find. Everything it reads is passed in: the Cmd runs off the Update
// goroutine. The stat comes before the read, so a log that grows between
// them is read again next time.
func workLogCmd(id sched.JobID, path string, full bool, seq uint64, prev logStat) tea.Cmd {
	limit := int64(workTailBytes)
	if full {
		limit = workPagerBytes
	}
	return func() tea.Msg {
		msg := workLogMsg{id: id, full: full, seq: seq, stat: statLog(path)}
		if prev.known && msg.stat == prev {
			msg.unchanged = true
			return msg
		}
		data, partial, err := readLogEnd(path, limit)
		if err != nil {
			msg.err = err.Error()
			return msg
		}
		lines := ui.LogLines(data, partial)
		if !full && len(lines) > workTailLines {
			lines = lines[len(lines)-workTailLines:]
		}
		msg.lines, msg.omitted = lines, partial
		return msg
	}
}

// readLogEnd reads the last limit bytes of the file at path; partial says
// the file's start was left out. A file that does not exist is an empty log
// (a queued job has none yet), not an error.
func readLogEnd(path string, limit int64) (data []byte, partial bool, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	start := int64(0)
	if st.Size() > limit {
		start, partial = st.Size()-limit, true
	}
	data = make([]byte, st.Size()-start)
	n, err := f.ReadAt(data, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	return data[:n], partial, nil
}

// applyWorkLog takes a log read's result. One older than the last read
// issued is dropped: a newer read is in flight or landed. A tail read lands
// only for the job selected now, and a pager read only for the job the pager
// shows, so a read that was overtaken by a key press shows under no other
// job. The pager keeps its place unless it was at the end, where it follows.
func (m *home) applyWorkLog(msg workLogMsg) {
	if msg.seq < m.work.seq {
		return
	}
	if !msg.full {
		sel, ok := m.workSelected()
		if m.work.pager != nil || !ok || sel.ID != msg.id {
			return
		}
		if msg.unchanged && m.work.tail.ID == msg.id {
			return
		}
		m.work.tail = ui.WorkTail{ID: msg.id, Lines: msg.lines, Err: msg.err}
		m.work.tailStat = logStat{}
		if msg.err == "" {
			m.work.tailStat = msg.stat
		}
		return
	}
	p := m.work.pager
	if p == nil || p.id != msg.id {
		return
	}
	if msg.unchanged && p.loaded {
		return
	}
	p.err = msg.err
	p.stat = logStat{}
	if msg.err != "" {
		return
	}
	body := ui.WorkPagerBody(m.workHeight())
	atEnd := !p.loaded || p.top >= max(0, len(p.lines)-body)
	oldTop := p.top
	p.lines, p.omitted, p.loaded, p.stat = msg.lines, msg.omitted, true, msg.stat
	maxTop := max(0, len(p.lines)-body)
	if atEnd {
		p.top = maxTop
	} else {
		p.top = min(oldTop, maxTop)
	}
}

// workDrained is Update's step after the model's events were applied
// (drainCore): it reads the log of the job the view shows when that job, its
// state or its sampled usage differ from what the last read was for. The
// queue changes only through QueueChanged, so this is the read on
// QueueChanged; a key that moves the selection or opens the pager changes
// the signature too.
func (m *home) workDrained() tea.Cmd {
	if m.viewMode != viewWork {
		return nil
	}
	job, ok := m.workShown()
	if !ok {
		m.work.sig = workSig{}
		return nil
	}
	sig := workSig{id: job.ID, state: job.State, rss: job.RSSKB, cpu: job.CPU, pager: m.work.pager != nil}
	if sig == m.work.sig {
		return nil
	}
	m.work.sig = sig
	return m.workLogRead(job, sig.pager)
}

// workTick is the health tick's read: a running job's log grows between
// samples, so it is read again. A queued job has no log and a finished
// job's has stopped growing.
func (m *home) workTick() tea.Cmd {
	job, ok := m.workShown()
	if !ok || job.State != sched.Running {
		return nil
	}
	return m.workLogRead(job, m.work.pager != nil)
}

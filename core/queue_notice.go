package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
)

const (
	// lostWhy is why a job is lost: its runner vanished without writing its
	// result (spec §4).
	lostWhy = "its runner died without a result (the host may have rebooted)"
	// noticeTailBytes caps the log tail a notice carries (spec §5.8).
	noticeTailBytes = 4000
)

// jobNoticeBuilt is a job's notice, back from the job that read its log: the
// text for the owner's mod.
type jobNoticeBuilt struct {
	id    sched.JobID
	owner InstanceID
	agent string // the subagent that submitted the job (sched.Record.Agent)
	text  string
}

// sendJobNotice tells rec's owner how the job ended (spec §5.8), through the
// loom mod's outbox with the origin OriginJob. Nothing is sent for a job
// that asked for no notice, has no owner, or already has a fate. When the
// owner is gone or can't take it the notice is recorded undelivered at once;
// otherwise a job reads the log's tail off the loop (a log has no size cap)
// and deliverJobNotice queues the text. rec is a copy: the caller may be
// iterating the store.
func (m *Model) sendJobNotice(rec sched.Record) {
	if !rec.Notify || rec.Owner == 0 || rec.Notice != sched.NoticeNone {
		return
	}
	owner := InstanceID(rec.Owner)
	if _, why := m.noticeTarget(owner); why != "" {
		m.noteNotice(rec.ID, sched.NoticeUndelivered, why)
		return
	}
	logPath := filepath.Join(m.queue.jobDir(rec.ID), sched.LogFile)
	m.spawnBackground(buildNoticeJob(rec.ID, owner, rec.Agent, noticeHead(rec), logPath, rec.Tail))
}

// noticeTarget is the session a notice for owner would go to, and why it
// can't go now ("" when it can): the owner is no longer served, or its
// session doesn't run (the checks Send makes of a CLI message).
func (m *Model) noticeTarget(owner InstanceID) (*session.Instance, string) {
	inst := m.ownerInst(owner)
	if inst == nil {
		return nil, "the session that submitted it is no longer served"
	}
	return inst, jobOwnerWhy(inst)
}

// ownerInst is the served instance with ID id. It computes the ID of every
// instance when the cache (lookup) has not met it yet: at boot nothing has
// published the instances' IDs, and an ID is derived from the disk, so a job's
// owner is found again after a restart.
func (m *Model) ownerInst(id InstanceID) *session.Instance {
	if id == 0 {
		return nil
	}
	if inst, _ := m.lookup(id); inst != nil {
		return inst
	}
	for _, ws := range m.workspaces {
		for _, inst := range ws.insts {
			if m.idOf(inst) == id {
				return inst
			}
		}
	}
	return nil
}

// jobOwnerWhy is why a session can't take a job's notice now, "" when it can:
// it has not started, is not running, or is busy starting, resuming or
// pausing (its link would be the old launch's).
func jobOwnerWhy(inst *session.Instance) string {
	switch st := inst.GetStatus(); {
	case st == session.Loading || st == session.Deleting:
		return fmt.Sprintf("the session is busy (%s)", st)
	case st == session.Paused || st == session.Recoverable:
		return fmt.Sprintf("the session is not running (%s)", st)
	case !inst.Started():
		return "the session has not started"
	}
	return ""
}

// jobNoModReason says why inst's launch has no live mod, for a notice
// recorded undelivered: noModReason's causes in its order, in words that
// name no `loom` command.
func jobNoModReason(inst *session.Instance) string {
	launch, hooked := inst.HookLaunch()
	switch {
	case !session.IsClaudeProgram(inst.Program()):
		return fmt.Sprintf("it runs %s, not Claude", programName(inst.Program()))
	case session.ModSocket() == "":
		return "this loom daemon serves no loom mod"
	case inst.Launches() == 0:
		return "it was launched before this daemon started and its loom mod has not said hello: pause and resume it to relaunch it with the mod"
	case launch != "" && !hooked:
		return "it runs without loom's hooks, so it has no loom mod"
	default:
		return "its Claude runs without the loom mod (an older Claude, or the mod did not load)"
	}
}

// noteNotice records a notice's fate on job id and saves: sent (the mod
// queued it) or undelivered, with why. A fate once recorded stays.
func (m *Model) noteNotice(id sched.JobID, fate sched.Notice, why string) {
	q := m.queue
	i := q.index(id)
	if i < 0 || q.store.Jobs[i].Notice != sched.NoticeNone {
		return
	}
	q.store.Jobs[i].Notice, q.store.Jobs[i].NoticeWhy = fate, why
	q.moved()
	m.saveQueueQuiet("notice")
}

// buildNoticeJob reads the log's tail off the loop and returns the notice's
// text. It reads no model state: head, logPath and tail are copies.
func buildNoticeJob(id sched.JobID, owner InstanceID, agent, head, logPath string, tail int) Job {
	return func() any {
		return jobNoticeBuilt{id: id, owner: owner, agent: agent, text: composeNotice(head, logPath, tail)}
	}
}

// deliverJobNotice hands a built notice to its owner's mod. The owner is
// looked up again: it may have been killed or paused while the log was read.
func (m *Model) deliverJobNotice(r jobNoticeBuilt) {
	inst, why := m.noticeTarget(r.owner)
	if why != "" {
		m.noteNotice(r.id, sched.NoticeUndelivered, why)
		return
	}
	m.enqueue(inst, &modMessage{text: r.text, origin: OriginJob, job: r.id, agent: r.agent})
}

// noticeHead is the notice's first line (spec §5.8): the job, then its outcome
// and how long it ran and waited. A lost job or one that failed to start has
// no run to time; a job cancelled while queued only waited.
func noticeHead(rec sched.Record) string {
	var what string
	switch {
	case rec.State == sched.Cancelled:
		what = "was cancelled " + rec.CancelReason
	case rec.State == sched.Lost:
		what = "was lost: " + lostWhy
	case startFailed(rec.State, rec.Failure, rec.Exit):
		what = "could not start: " + startFailReason(rec)
	case rec.Exit != nil && rec.Exit.Signal != "":
		what = "was killed by " + rec.Exit.Signal
	case rec.Exit != nil:
		what = fmt.Sprintf("finished: exit %d", rec.Exit.Code)
	default:
		what = "finished"
	}
	head := fmt.Sprintf("[loom] job %d %q %s", rec.ID, rec.Description, what)
	switch {
	case rec.State == sched.Lost, startFailed(rec.State, rec.Failure, rec.Exit):
		return head + "."
	case !rec.Started.IsZero() && !rec.Ended.Before(rec.Started):
		return fmt.Sprintf("%s after %s (%s in the queue).", head, roundedDur(rec.Ended.Sub(rec.Started)), roundedDur(rec.Started.Sub(rec.Submitted)))
	default:
		return fmt.Sprintf("%s after %s in the queue.", head, roundedDur(rec.Ended.Sub(rec.Submitted)))
	}
}

// startFailReason is why a job that never ran failed: the daemon's reason,
// else the exec error the runner recorded.
func startFailReason(rec sched.Record) string {
	switch {
	case rec.Failure != "":
		return rec.Failure
	case rec.Exit != nil && rec.Exit.Error != "":
		return rec.Exit.Error
	}
	return "no reason was recorded"
}

// roundedDur is a duration to the second, as the CLI shows durations.
func roundedDur(d time.Duration) string { return d.Round(time.Second).String() }

// composeNotice is head, then the log's line: its path and line count, and the
// last tail lines within the last 4,000 bytes. A job that never ran has no log
// and gets the head alone.
func composeNotice(head, logPath string, tail int) string {
	total, lines, err := readLogTail(logPath, tail)
	var b strings.Builder
	b.WriteString(head)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		fmt.Fprintf(&b, "\nLog: %s (could not be read: %v)", logPath, err)
	default:
		fmt.Fprintf(&b, "\nLog: %s (%s).", logPath, plural(total, "line"))
		if len(lines) > 0 {
			fmt.Fprintf(&b, " Last %s:\n\n%s", plural(len(lines), "line"), strings.Join(lines, "\n"))
		}
	}
	return b.String()
}

// withSubmitter is a notice's text with line after its head line: the
// session's copy of a notice a subagent submitted (subagent job results spec
// §5.4).
func withSubmitter(text, line string) string {
	head, rest, ok := strings.Cut(text, "\n")
	if !ok {
		return head + "\n" + line
	}
	return head + "\n" + line + "\n" + rest
}

// unreachableLine names the subagent that submitted a job, with why loom
// could not reach it, for the session's copy. The full ID: SendMessage takes
// it.
func unreachableLine(agent, why string) string {
	why = strings.TrimSuffix(strings.TrimSpace(why), ".")
	if why == "" {
		why = "no reason was given"
	}
	return fmt.Sprintf("(Submitted by your subagent %s; loom could not reach it: %s.)", agent, why)
}

// oldModLine names the subagent that submitted a job, for a session whose
// loom mod can't message it.
func oldModLine(agent string) string {
	return fmt.Sprintf("(Submitted by your subagent %s; this session's loom mod is too old to message it, so pass this on with SendMessage if it still needs it.)", agent)
}

// plural is "1 line" or "N lines".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// readLogTail counts the lines of the file at path and returns the last want
// of them, within its last noticeTailBytes bytes. The window may open
// mid-line, so its first line is dropped when there is another.
func readLogTail(path string, want int) (total int, lines []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	var size int64
	last := byte('\n')
	buf := make([]byte, 64<<10)
	for {
		n, rerr := f.Read(buf)
		total += bytes.Count(buf[:n], []byte{'\n'})
		if n > 0 {
			size += int64(n)
			last = buf[n-1]
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, nil, rerr
		}
	}
	if size > 0 && last != '\n' {
		total++
	}
	if want <= 0 || size == 0 {
		return total, nil, nil
	}
	off := max(0, size-noticeTailBytes)
	window := make([]byte, size-off)
	if _, err := f.ReadAt(window, off); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, err
	}
	lines = strings.Split(strings.TrimRight(string(window), "\n"), "\n")
	if off > 0 && len(lines) > 1 {
		lines = lines[1:]
	}
	if len(lines) > want {
		lines = lines[len(lines)-want:]
	}
	for i := range lines {
		lines[i] = strings.ToValidUTF8(lines[i], "")
	}
	return total, lines, nil
}

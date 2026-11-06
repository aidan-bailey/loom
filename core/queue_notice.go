package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
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
// loom mod's outbox with the origin OriginJob: its head (jobHead) and its
// log's tail.
func (m *Model) sendJobNotice(rec sched.Record) {
	m.sendNotice(rec, m.jobHead(rec), rec.ID)
}

// sendNotice sends head, then the tail of job logOf's log at rec's --tail,
// to rec's owner (or the subagent that submitted rec, rec.Agent), and records
// the notice's fate on rec. Nothing is sent for a job that asked for no
// notice, has no owner, or already has a fate (wantsNotice). When the owner
// is gone or can't take it the notice is recorded undelivered at once;
// otherwise a job reads the log's tail off the loop (a log has no size cap)
// and deliverJobNotice queues the text. rec is a copy: the caller may be
// iterating the store.
func (m *Model) sendNotice(rec sched.Record, head string, logOf sched.JobID) {
	if !wantsNotice(rec) {
		return
	}
	owner := InstanceID(rec.Owner)
	if _, why := m.noticeTarget(owner); why != "" {
		m.noteNotice(rec.ID, sched.NoticeUndelivered, why)
		return
	}
	logPath := filepath.Join(m.queue.jobDir(logOf), sched.LogFile)
	m.spawnBackground(buildNoticeJob(rec.ID, owner, rec.Agent, head, logPath, rec.Tail, wantsLockHint(rec)))
}

// wantsLockHint reports whether rec's notice looks in its log for a port
// already in use (lockHint): it ran and exited non-zero, and holds no lock.
func wantsLockHint(rec sched.Record) bool {
	return rec.State == sched.Failed && len(rec.Locks) == 0 && rec.Exit != nil && rec.Exit.Code > 0
}

// jobHead is rec's message before its log: its first line, which names the
// job it was released after (afterText), and the over-claim line of a
// dependent that needs one (overClaimLine).
func (m *Model) jobHead(rec sched.Record) string {
	head := noticeHead(rec, m.afterText(rec))
	if line := overClaimLine(rec); line != "" {
		head += "\n" + line
	}
	return head
}

// afterText is what a released dependent's first line adds in its
// parenthesis (dependent jobs spec §5.5): `after job 14 "build tests", done
// in 3m10s`. It is "" for a job with no prerequisite, one queued at once
// after a job that had already ended (its Ready is its submit), and one
// whose prerequisite's record is gone or never ran.
func (m *Model) afterText(rec sched.Record) string {
	if rec.After == 0 || !rec.Ready.After(rec.Submitted) {
		return ""
	}
	i := m.queue.index(rec.After)
	if i < 0 {
		return ""
	}
	p := m.queue.store.Jobs[i]
	if p.State != sched.Done || p.Started.IsZero() {
		return ""
	}
	return fmt.Sprintf("after job %d %q, done in %s", p.ID, p.Description, roundedDur(p.Ended.Sub(p.Started)))
}

// overClaimLine is the line a dependent's message gets when it peaked at
// over twice its memory claim (dependent jobs spec §5.5): it may have rebuilt
// what its prerequisite built. The peak is the larger of the sampled one and
// the command's max RSS, as sched.RunOf takes it. "" for a job with no
// prerequisite, an exclusive claim, or a peak within twice the claim.
func overClaimLine(rec sched.Record) string {
	if rec.After == 0 || rec.Claim.Exclusive || rec.Claim.MemoryGB < 1 {
		return ""
	}
	peak := rec.PeakRSSKB
	if rec.Exit != nil {
		peak = max(peak, rec.Exit.MaxRSSKB)
	}
	if peak <= 2*(int64(rec.Claim.MemoryGB)<<20) {
		return ""
	}
	return fmt.Sprintf("It peaked at %.1f GB on a %d GB claim: if it rebuilt what job %d built, run job %d's output instead.",
		float64(peak)/(1<<20), rec.Claim.MemoryGB, rec.After, rec.After)
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
func buildNoticeJob(id sched.JobID, owner InstanceID, agent, head, logPath string, tail int, hint bool) Job {
	return func() any {
		return jobNoticeBuilt{id: id, owner: owner, agent: agent, text: composeNotice(head, logPath, tail, hint)}
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

// sendRequeueNotice tells rec's owner (or the subagent that submitted it)
// that the job was killed for memory and requeued (cgroup spec §8.4). It
// records no fate on the job (the mod message names job 0), so the end
// message still comes (§15, 10); a job that asked for no message, or whose
// owner can't take one, sends none.
func (m *Model) sendRequeueNotice(rec sched.Record, why string) {
	if !rec.Notify || rec.Owner == 0 {
		return
	}
	inst, reason := m.noticeTarget(InstanceID(rec.Owner))
	if reason != "" {
		log.For("core").Info("queue.requeue_notice_skipped", "job", int64(rec.ID), "why", reason)
		return
	}
	text := fmt.Sprintf("[loom] job %d %q was stopped and requeued: %s; it keeps its place in line", rec.ID, rec.Description, why)
	m.enqueue(inst, &modMessage{text: text, origin: OriginJob, agent: rec.Agent})
}

// noticeHead is the notice's first line (spec §5.8): the job, then its
// outcome and how long it ran and waited in the queue (from when it joined
// it, Record.QueuedSince), with after, unless it is "", added in the
// parenthesis (dependent jobs spec §5.5). A lost job or one that failed to
// start has no run to time; a job cancelled while it waited only waited.
func noticeHead(rec sched.Record, after string) string {
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
		wait := roundedDur(rec.Started.Sub(rec.QueuedSince())) + " in the queue"
		if after != "" {
			wait += ", " + after
		}
		if note := borrowNote(rec); note != "" {
			wait += ", " + note
		}
		return fmt.Sprintf("%s after %s (%s).", head, roundedDur(rec.Ended.Sub(rec.Started)), wait)
	default:
		return fmt.Sprintf("%s after %s in the queue.", head, roundedDur(rec.Ended.Sub(rec.QueuedSince())))
	}
}

// borrowNote is what a borrower's end message adds (memory borrowing spec
// §8): its give-back restarts and the time it was lent, "" for neither.
func borrowNote(rec sched.Record) string {
	var parts []string
	switch rec.GaveBack {
	case 0:
	case 1:
		parts = append(parts, "restarted once to lend memory back")
	default:
		parts = append(parts, fmt.Sprintf("restarted %d times to lend memory back", rec.GaveBack))
	}
	if rec.LentSecs > 0 {
		parts = append(parts, "lent memory back for "+roundedDur(time.Duration(rec.LentSecs)*time.Second))
	}
	return strings.Join(parts, ", ")
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
// and gets the head alone. With hint, the last hintLines lines (at least) are
// searched for a port already in use, and lockHint's line follows the head.
func composeNotice(head, logPath string, tail int, hint bool) string {
	want := tail
	if hint {
		want = max(tail, hintLines)
	}
	total, lines, err := readLogTail(logPath, want)
	var b strings.Builder
	b.WriteString(head)
	if hint {
		if line := lockHint(lines); line != "" {
			b.WriteString("\n" + line)
		}
	}
	lines = lines[max(0, len(lines)-tail):]
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

// hintLines is how many of a failed job's last lines lockHint reads, whatever
// its --tail.
const hintLines = 20

var (
	// inUse matches a log line saying an address or port is taken: Node's
	// EADDRINUSE, the OS's "address already in use", Playwright's "is
	// already used" and the dev servers' "port 4300 is already in use".
	inUse = regexp.MustCompile(`(?i)eaddrinuse|address already in use|already (?:in )?use[d]?\b`)
	// portIn finds the port such a line names: host:port, or "port N".
	// aboutAnAddress tells such a line without a port from one about
	// something else that is already used.
	aboutAnAddress = regexp.MustCompile(`(?i)eaddrinuse|address|\bport\b`)
	portIn         = regexp.MustCompile(`(?i)(?:[a-z0-9\]\.:]:|port\s+)(\d{2,5})\b`)
)

// lockHint is the line a failed job's notice gets when one of its log lines
// says a port was already in use: two jobs binding one port should take turns
// under a lock. It names port:N when the line gives the port; "" when no line
// says so.
func lockHint(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if !inUse.MatchString(lines[i]) {
			continue
		}
		m := portIn.FindStringSubmatch(lines[i])
		if m == nil && !aboutAnAddress.MatchString(lines[i]) {
			continue // "the name is already used": no port in sight
		}
		lock := "port:<N>"
		if m != nil {
			lock = "port:" + m[1]
		}
		return fmt.Sprintf("Its log says a port was already in use. If another job binds it too, submit both with --lock %s so they take turns.", lock)
	}
	return ""
}

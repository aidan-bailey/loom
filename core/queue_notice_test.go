package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqSeed adds rec to the queue and makes its directory, as a submit does.
func wqSeed(t *testing.T, m *Model, rec sched.Record) {
	t.Helper()
	m.queue.store.Jobs = append(m.queue.store.Jobs, rec)
	if rec.ID >= m.queue.store.NextID {
		m.queue.store.NextID = rec.ID + 1
	}
	require.NoError(t, sched.WriteSpec(m.queue.jobDir(rec.ID), sched.Spec{ID: rec.ID, Argv: rec.Argv, Cwd: rec.Cwd}))
}

// wqRecord is a copy of job id's record.
func wqRecord(t *testing.T, m *Model, id sched.JobID) sched.Record {
	t.Helper()
	i := m.queue.index(id)
	require.GreaterOrEqual(t, i, 0, "job %d", id)
	return m.queue.store.Jobs[i]
}

// wqPump runs the jobs the model queued and delivers their results, until it
// queues no more, and returns the events it produced meanwhile.
func wqPump(t *testing.T, m *Model) []Event {
	t.Helper()
	var events []Event
	for range 50 {
		out := m.Drain()
		events = append(events, out.Events...)
		if len(out.Jobs) == 0 {
			return events
		}
		for _, j := range out.Jobs {
			m.Deliver(j())
		}
	}
	t.Fatal("the model kept queuing jobs")
	return events
}

func TestJobQueue_ANoticeNamesItsJobsOutcome(t *testing.T) {
	with := func(edit func(*sched.Record)) sched.Record {
		r := wqEnded(14, 0)
		edit(&r)
		return r
	}
	const prefix = `[loom] job 14 "polyoxide tests" `
	cases := []struct {
		name string
		rec  sched.Record
		want string
	}{
		{"exit 0", with(func(*sched.Record) {}),
			prefix + `finished: exit 0 after 3m10s (1m42s in the queue).`},
		{"a non-zero exit", with(func(r *sched.Record) { r.State = sched.Failed; r.Exit = &sched.Exit{Code: 2} }),
			prefix + `finished: exit 2 after 3m10s (1m42s in the queue).`},
		{"a signal", with(func(r *sched.Record) { r.State = sched.Failed; r.Exit = &sched.Exit{Code: -1, Signal: "SIGKILL"} }),
			prefix + `was killed by SIGKILL after 3m10s (1m42s in the queue).`},
		{"cancelled while running", with(func(r *sched.Record) {
			r.State, r.CancelReason, r.Exit = sched.Cancelled, "from the work view", nil
		}),
			prefix + `was cancelled from the work view after 3m10s (1m42s in the queue).`},
		{"cancelled while queued", with(func(r *sched.Record) {
			r.State, r.CancelReason, r.Exit = sched.Cancelled, "with loom job cancel", nil
			r.Started, r.Ended = time.Time{}, r.Submitted.Add(2*time.Minute)
		}),
			prefix + `was cancelled with loom job cancel after 2m0s in the queue.`},
		{"lost", with(func(r *sched.Record) { r.State, r.Exit = sched.Lost, nil }),
			prefix + `was lost: its runner died without a result (the host may have rebooted).`},
		{"could not start", with(func(r *sched.Record) {
			r.State, r.Exit, r.Started, r.Failure = sched.Failed, nil, time.Time{}, "its folder is gone"
		}),
			prefix + `could not start: its folder is gone.`},
		{"an exec that failed", with(func(r *sched.Record) {
			r.State, r.Exit = sched.Failed, &sched.Exit{Code: 127, Error: "exec: no such file"}
		}),
			prefix + `could not start: exec: no such file.`},
		{"failed with no exit and no reason", with(func(r *sched.Record) {
			r.State, r.Exit, r.Started = sched.Failed, nil, time.Time{}
		}),
			prefix + `could not start: no reason was recorded.`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, noticeHead(c.rec))
		})
	}
}

func TestJobQueue_ANoticeCarriesTheLogsTail(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	var thirty strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&thirty, "line %d\n", i)
	}
	full := write("thirty.log", thirty.String())

	assert.Equal(t, "HEAD\nLog: "+full+" (30 lines). Last 5 lines:\n\nline 26\nline 27\nline 28\nline 29\nline 30",
		composeNotice("HEAD", full, 5))
	assert.Equal(t, "HEAD\nLog: "+full+" (30 lines).", composeNotice("HEAD", full, 0),
		"--tail 0 names the log and sends none of it")

	short := write("short.log", "a\nb")
	assert.Equal(t, "HEAD\nLog: "+short+" (2 lines). Last 2 lines:\n\na\nb", composeNotice("HEAD", short, 20),
		"a last line without its newline counts")
	one := write("one.log", "only\n")
	assert.Equal(t, "HEAD\nLog: "+one+" (1 line). Last 1 line:\n\nonly", composeNotice("HEAD", one, 20))
	empty := write("empty.log", "")
	assert.Equal(t, "HEAD\nLog: "+empty+" (0 lines).", composeNotice("HEAD", empty, 20))
	assert.Equal(t, "HEAD", composeNotice("HEAD", filepath.Join(dir, "never.log"), 20),
		"a job that never ran has no log to name")

	line := strings.Repeat("x", 99)
	wide := write("wide.log", strings.Repeat(line+"\n", 100))
	capped := composeNotice("HEAD", wide, 200)
	_, tail, found := strings.Cut(capped, ":\n\n")
	require.True(t, found)
	assert.LessOrEqual(t, len(tail), 4000, "at most the last 4,000 bytes")
	assert.True(t, strings.HasSuffix(tail, line), "and they end with the log's last line")
	assert.Contains(t, capped, "(100 lines). Last 39 lines:", "the window's first line may be cut, so it goes")
}

func TestJobQueue_ANoticeReachesTheOwnersModAndIsRecordedSent(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	rec := wqEnded(5, m.idOf(inst))
	wqSeed(t, m, rec)
	logPath := filepath.Join(m.queue.jobDir(5), sched.LogFile)
	require.NoError(t, os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0o600))
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))

	m.sendJobNotice(rec)
	wqPump(t, m)

	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.False(t, msgs[0].AsUser, "a job's notice is nobody's own words")
	assert.Equal(t, noticeHead(rec)+"\nLog: "+logPath+" (3 lines). Last 2 lines:\n\ntwo\nthree", msgs[0].Text)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 5).Notice, "not sent until the mod has queued it")

	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))

	assert.Equal(t, sched.NoticeSent, wqRecord(t, m, 5).Notice)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	require.Len(t, saved.Jobs, 1)
	assert.Equal(t, sched.NoticeSent, saved.Jobs[0].Notice, "the fate is on disk")
}

// A launch with no live mod is never pasted into: a paste could answer an
// open dialog. The notice is recorded on the job with the reason (spec §5.8).
func TestJobQueue_ANoticeWithNoLiveModIsRecordedUndeliveredAndNeverPasted(t *testing.T) {
	cases := []struct{ name, program, why string }{
		{"no mod served", "claude", "this loom daemon serves no loom mod"},
		{"another program", "aider", "it runs aider, not Claude"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqModel(t)
			inst := startedInst(t, "owner", c.program)
			hold(m, inst)
			wqSeed(t, m, wqEnded(5, m.idOf(inst)))

			m.sendJobNotice(wqRecord(t, m, 5))
			wqPump(t, m)

			got := wqRecord(t, m, 5)
			assert.Equal(t, sched.NoticeUndelivered, got.Notice)
			assert.Equal(t, c.why, got.NoticeWhy)
			assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
		})
	}
}

// A message the mod never took is settled as undelivered when the daemon
// stops, and one it took (an inbox poll handed it over) too, since it may
// have reached Claude; neither is pasted (core/CLAUDE.md, loom mod links).
func TestJobQueue_ANoticeTheModNeverQueuedIsUndeliveredWhenTheDaemonStops(t *testing.T) {
	for _, offered := range []bool{false, true} {
		t.Run(fmt.Sprintf("offered=%v", offered), func(t *testing.T) {
			withMod(t)
			m := wqModel(t)
			inst := modInst(t, m, "owner")
			wqSeed(t, m, wqEnded(5, m.idOf(inst)))
			m.sendJobNotice(wqRecord(t, m, 5))
			wqPump(t, m)
			if offered {
				require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
				msgs, _, err := m.ModInbox("L1", "")
				require.NoError(t, err)
				require.Len(t, msgs, 1)
			}

			m.ModsStop()

			got := wqRecord(t, m, 5)
			assert.Equal(t, sched.NoticeUndelivered, got.Notice)
			if offered {
				assert.Contains(t, got.NoticeWhy, "may or may not have reached Claude")
			} else {
				assert.Equal(t, modsStoppedWhy, got.NoticeWhy)
			}
			assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
		})
	}
}

func TestJobQueue_ANoticeNeedsAnOwnerThatCanTakeIt(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	paused := pausedInst(t, "paused")
	hold(m, paused)
	cases := []struct {
		name string
		rec  sched.Record
		why  string
	}{
		{"not served", wqEnded(1, 4242), "the session that submitted it is no longer served"},
		{"paused", wqEnded(2, m.idOf(paused)), "the session is not running (Paused)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wqSeed(t, m, c.rec)

			m.sendJobNotice(c.rec)

			got := wqRecord(t, m, c.rec.ID)
			assert.Equal(t, sched.NoticeUndelivered, got.Notice)
			assert.Equal(t, c.why, got.NoticeWhy)
			assert.Empty(t, m.Drain().Jobs, "no notice is built for it")
		})
	}
}

func TestJobQueue_ANoticeIsSentOnlyForAJobThatAsksForOneAndHasNoFateYet(t *testing.T) {
	m := wqModel(t)
	ownerless := wqEnded(1, 0)
	quiet := wqEnded(2, 77)
	quiet.Notify = false
	decided := wqEnded(3, 77)
	decided.Notice = sched.NoticeSent
	for _, rec := range []sched.Record{ownerless, quiet, decided} {
		wqSeed(t, m, rec)

		m.sendJobNotice(rec)

		assert.Equal(t, rec.Notice, wqRecord(t, m, rec.ID).Notice, "job %d", rec.ID)
		assert.Empty(t, m.Drain().Jobs, "job %d", rec.ID)
	}
}

func TestJobQueue_AnOwnerGoneWhileTheNoticeWasBeingBuiltIsRecordedUndelivered(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	wqSeed(t, m, wqEnded(5, m.idOf(inst)))
	m.sendJobNotice(wqRecord(t, m, 5))
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	built := jobs[0]()
	require.True(t, m.workspaces[0].RemoveForTest(inst), "the session is killed meanwhile")

	m.Deliver(built)

	got := wqRecord(t, m, 5)
	assert.Equal(t, sched.NoticeUndelivered, got.Notice)
	assert.Equal(t, "the session that submitted it is no longer served", got.NoticeWhy)
}

func TestJobQueue_NoClientCanSendAJobsOrigin(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	m.Send(m.idOf(inst), Message{Text: "hi", Origin: OriginJob}, 9)

	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	require.Error(t, rs[0].Err)
	assert.Contains(t, rs[0].Err.Error(), "is no sender's origin")
}

// agentNotice seeds job 5, ended and owned by inst, as submitted by subagent
// a1, with a three-line log, and returns its record and log path.
func agentNotice(t *testing.T, m *Model, inst InstanceID) (sched.Record, string) {
	t.Helper()
	rec := wqEnded(5, inst)
	rec.Agent = "a1"
	wqSeed(t, m, rec)
	logPath := filepath.Join(m.queue.jobDir(5), sched.LogFile)
	require.NoError(t, os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0o600))
	return rec, logPath
}

// A subagent's notice goes to it through a mod that can send to subagents,
// as today's text, and a mod that can't gets the session's copy naming the
// subagent. Which one is decided as the inbox hands it over, so a notice
// queued before the hello follows the hello (subagent job results spec
// §5.3).
func TestJobQueue_ASubagentsNoticeGoesToItWhenTheModCan(t *testing.T) {
	for _, agents := range []bool{true, false} {
		for _, helloFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("agents=%v helloFirst=%v", agents, helloFirst), func(t *testing.T) {
				withMod(t)
				m := wqModel(t)
				inst := modInst(t, m, "owner")
				rec, logPath := agentNotice(t, m, m.idOf(inst))
				hello := func() { require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Agents: agents})) }
				if helloFirst {
					hello()
				}
				m.sendJobNotice(rec)
				wqPump(t, m)
				if !helloFirst {
					hello()
				}

				msgs, _, err := m.ModInbox("L1", "")
				require.NoError(t, err)
				require.Len(t, msgs, 1)
				tail := "\nLog: " + logPath + " (3 lines). Last 2 lines:\n\ntwo\nthree"
				if agents {
					assert.Equal(t, "a1", msgs[0].Agent)
					assert.Equal(t, noticeHead(rec)+tail, msgs[0].Text, "the subagent reads today's notice")
				} else {
					assert.Empty(t, msgs[0].Agent, "the session's copy")
					assert.Equal(t, noticeHead(rec)+"\n"+oldModLine("a1")+tail, msgs[0].Text)
				}
				require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
				assert.Equal(t, sched.NoticeSent, wqRecord(t, m, 5).Notice)
			})
		}
	}
}

// A subagent that can't take its notice leaves it to the session, which gets
// it with the subagent named and why; its fate is recorded when the session's
// copy is queued.
func TestJobQueue_ASubagentThatCantTakeItsNoticeLeavesItToTheSession(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	rec, logPath := agentNotice(t, m, m.idOf(inst))
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Agents: true}))
	m.sendJobNotice(rec)
	wqPump(t, m)
	first, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, first, 1)

	why := "Agent 'a1' could not be resumed: No transcript found for agent ID: a1."
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "failed", ID: first[0].ID, Error: why}}))

	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 5).Notice, "not sent until the session's copy is queued")
	again, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.NotEqual(t, first[0].ID, again[0].ID)
	assert.Empty(t, again[0].Agent)
	assert.Equal(t, noticeHead(rec)+"\n"+unreachableLine("a1", why)+"\nLog: "+logPath+" (3 lines). Last 2 lines:\n\ntwo\nthree", again[0].Text)
	assert.Contains(t, again[0].Text, "(Submitted by your subagent a1; loom could not reach it: Agent 'a1' could not be resumed")
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: again[0].ID}}))
	assert.Equal(t, sched.NoticeSent, wqRecord(t, m, 5).Notice)
}

// A failed report for any other message is unchanged: a CLI message the mod
// could not submit is refused, not handed to the session again.
func TestJobQueue_OnlyASubagentsNoticeFallsBackToTheSession(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Agents: true}))
	m.enqueue(inst, cliMessage(7, "hi", false))
	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "failed", ID: msgs[0].ID, Error: "boom"}}))
	again, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	assert.Empty(t, again, "nothing is queued again")
}

// A subagent's notice a stopping daemon holds is undelivered, as any notice
// the mod took is: it is never rerouted to the session (core/CLAUDE.md, loom
// mod links).
func TestJobQueue_ASubagentsNoticeHeldAtTheStopIsUndelivered(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	rec, _ := agentNotice(t, m, m.idOf(inst))
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Agents: true}))
	m.sendJobNotice(rec)
	wqPump(t, m)
	_, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)

	m.ModsStop()

	assert.Equal(t, sched.NoticeUndelivered, wqRecord(t, m, 5).Notice)
}

func TestWithSubmitter_PutsItsLineAfterTheHead(t *testing.T) {
	assert.Equal(t, "head\nline\nrest\nmore", withSubmitter("head\nrest\nmore", "line"))
	assert.Equal(t, "head\nline", withSubmitter("head", "line"), "a job that never ran has a head alone")
	assert.Equal(t, "(Submitted by your subagent a1; loom could not reach it: no reason was given.)", unreachableLine("a1", ""))
}

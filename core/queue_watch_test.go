package core

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqBeatScript is a command that writes a rising number to its first argument
// every 50 ms until it dies: a process whose life a test can watch.
const wqBeatScript = `n=0; while :; do n=$((n+1)); echo $n > "$1"; sleep 0.05; done`

// wqWait polls cond on the test's goroutine until it holds.
func wqWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// wqConfig writes the scheduler block block into the global config.json the
// queue reads.
func wqConfig(t *testing.T, m *Model, block string) {
	t.Helper()
	body := `{"scheduler": ` + block + `}`
	require.NoError(t, os.WriteFile(filepath.Join(m.queue.cfgDir, config.ConfigFileName), []byte(body), 0o644))
}

// wqLiveModel is wqModel for a test that runs real shims: when the test ends
// every job still unfinished is cancelled and settled, so no process group
// outlives it.
func wqLiveModel(t *testing.T) *Model {
	t.Helper()
	m := wqModel(t)
	t.Cleanup(func() { wqStop(t, m) })
	return m
}

func wqStop(t *testing.T, m *Model) {
	t.Helper()
	q := m.queue
	for i := range q.store.Jobs {
		switch rec := &q.store.Jobs[i]; rec.State {
		case sched.Queued, sched.Held:
			rec.State = sched.Cancelled
		case sched.Running:
			if rec.CancelReason == "" {
				rec.CancelReason = "when the test ended"
			}
		}
	}
	q.escalation = 200 * time.Millisecond
	wqWait(t, "the jobs to stop", func() bool {
		wqPass(t, m)
		for _, rec := range q.store.Jobs {
			if rec.State == sched.Running {
				return false
			}
		}
		return true
	})
}

// wqPass runs one pass of gateQueue to completion, as the tick would: it
// makes the gate due, dispatches, runs every job the model queued and
// delivers the results.
func wqPass(t *testing.T, m *Model) []Event {
	t.Helper()
	m.gate(gateQueue).expedite()
	m.maybeQueue()
	return wqPump(t, m)
}

// wqSettle passes until job id has finished, and returns its record.
func wqSettle(t *testing.T, m *Model, id sched.JobID) sched.Record {
	t.Helper()
	wqWait(t, fmt.Sprintf("job %d to settle", id), func() bool {
		wqPass(t, m)
		return wqRecord(t, m, id).State.Finished()
	})
	return wqRecord(t, m, id)
}

// wqLaunch starts a real shim for a new running record, as the model's own
// start does (Task 3.4 drives that), and waits for its started.json.
func wqLaunch(t *testing.T, m *Model, argv ...string) sched.JobID {
	t.Helper()
	q := m.queue
	id := q.store.NextID
	q.store.NextID++
	dir := q.jobDir(id)
	cwd := t.TempDir()
	require.NoError(t, sched.WriteSpec(dir, sched.Spec{ID: id, Argv: argv, Cwd: cwd, Env: []string{"PATH=" + os.Getenv("PATH")}}))
	_, err := q.runner.Start(dir)
	require.NoError(t, err)
	var st sched.Started
	wqWait(t, "the shim's started.json", func() bool {
		var err error
		st, err = sched.ReadStarted(dir)
		return err == nil
	})
	q.store.Jobs = append(q.store.Jobs, sched.Record{
		ID: id, Order: int64(id), Description: "launched", Argv: argv, Cwd: cwd,
		Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Tail: 20,
		State: sched.Running, Submitted: st.Started, Started: st.Started, PID: st.PID,
	})
	return id
}

// wqBeats reports whether the number in file rises within a second.
func wqBeats(file string) bool {
	first := ""
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(file)
		if v := strings.TrimSpace(string(b)); err == nil && v != "" {
			if first == "" {
				first = v
			} else if v != first {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// wqBystander starts a process of its own, in a group of its own: the stranger
// a reused pid names. It returns its pid and its heartbeat file, and is killed
// when the test ends.
func wqBystander(t *testing.T) (pid int, beat string) {
	t.Helper()
	beat = filepath.Join(t.TempDir(), "beat")
	r := sched.Runner{Shim: []string{"sh", "-c", wqBeatScript, "sh", beat}}
	pid, err := r.Start(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	wqWait(t, "the bystander's first beat", func() bool { _, err := os.Stat(beat); return err == nil })
	return pid, beat
}

// wqBlockJobsJSON makes jobs.json unwritable: a directory takes its place, so
// the atomic rename fails. It returns the undo.
func wqBlockJobsJSON(t *testing.T, m *Model) (unblock func()) {
	t.Helper()
	path := filepath.Join(m.queue.dir, sched.StoreFile)
	if err := os.Remove(path); !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(path, "x"), 0o700))
	return func() {
		require.NoError(t, os.Remove(filepath.Join(path, "x")))
		require.NoError(t, os.Remove(path))
	}
}

func TestJobQueue_AJobThatExitsZeroSettlesDone(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "echo hi")

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Done, rec.State)
	require.NotNil(t, rec.Exit)
	assert.Equal(t, 0, rec.Exit.Code)
	assert.Equal(t, rec.Exit.Ended, rec.Ended)
	assert.Empty(t, rec.Failure)
	out, err := os.ReadFile(filepath.Join(m.queue.jobDir(id), sched.LogFile))
	require.NoError(t, err)
	assert.Equal(t, "hi\n", string(out))
}

func TestJobQueue_ANonZeroExitOrASignalSettlesFailed(t *testing.T) {
	t.Run("a non-zero exit", func(t *testing.T) {
		m := wqLiveModel(t)
		rec := wqSettle(t, m, wqLaunch(t, m, "sh", "-c", "exit 3"))

		assert.Equal(t, sched.Failed, rec.State)
		require.NotNil(t, rec.Exit)
		assert.Equal(t, 3, rec.Exit.Code)
		assert.Empty(t, rec.Failure, "it ran: nothing failed to start")
	})
	t.Run("a signal", func(t *testing.T) {
		m := wqLiveModel(t)
		rec := wqSettle(t, m, wqLaunch(t, m, "sh", "-c", "kill -9 $$"))

		assert.Equal(t, sched.Failed, rec.State)
		require.NotNil(t, rec.Exit)
		assert.Equal(t, "SIGKILL", rec.Exit.Signal)
	})
}

func TestJobQueue_ACommandThatCannotBeExecutedFailsWithItsError(t *testing.T) {
	m := wqLiveModel(t)

	rec := wqSettle(t, m, wqLaunch(t, m, "/no/such/binary"))

	assert.Equal(t, sched.Failed, rec.State)
	require.NotNil(t, rec.Exit)
	assert.NotEmpty(t, rec.Exit.Error)
	assert.Equal(t, rec.Exit.Error, rec.Failure, "the notice says it could not start")
}

func TestJobQueue_ACancelledRunningJobGetsSIGTERMToItsGroupAndSettlesCancelled(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "sleep 30")
	m.queue.store.Jobs[m.queue.index(id)].CancelReason = "from the work view"

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Cancelled, rec.State, "whatever its exit")
	assert.Equal(t, "from the work view", rec.CancelReason)
	assert.False(t, rec.SignalledAt.IsZero(), "SIGTERM went to its group")
	require.NotNil(t, rec.Exit, "the shim stayed up to report")
	assert.Equal(t, "SIGTERM", rec.Exit.Signal)
}

func TestJobQueue_ACancelledJobThatIgnoresSIGTERMGetsSIGKILLAfterTheEscalation(t *testing.T) {
	m := wqLiveModel(t)
	m.queue.escalation = 300 * time.Millisecond
	ready := filepath.Join(t.TempDir(), "ready")
	id := wqLaunch(t, m, "sh", "-c", `trap "" TERM; echo ok > "$1"; while :; do sleep 0.1; done`, "sh", ready)
	wqWait(t, "the command to ignore SIGTERM", func() bool { _, err := os.Stat(ready); return err == nil })
	m.queue.store.Jobs[m.queue.index(id)].CancelReason = "with loom job cancel"

	wqPass(t, m)
	first := wqRecord(t, m, id)
	require.Equal(t, sched.Running, first.State, "SIGTERM alone does not end it")
	require.False(t, first.SignalledAt.IsZero())

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Cancelled, rec.State)
	assert.Nil(t, rec.Exit, "SIGKILL took the shim with it, so nothing reported")
}

// A shim that vanished without writing its result leaves a lost job, and its
// group, which nothing now watches, is sent SIGKILL: the sample shows
// processes in the group and none with the shim's pid (spec §5.4).
func TestJobQueue_AShimThatVanishedLeavesALostJobAndItsOrphanedGroupIsKilled(t *testing.T) {
	m := wqLiveModel(t)
	beat := filepath.Join(t.TempDir(), "beat")
	id := wqLaunch(t, m, "sh", "-c", wqBeatScript, "sh", beat)
	wqWait(t, "the first beat", func() bool { _, err := os.Stat(beat); return err == nil })
	p, err := os.FindProcess(wqRecord(t, m, id).PID)
	require.NoError(t, err)
	require.NoError(t, p.Kill(), "the shim alone: its command and the command's group live on")
	dir := m.queue.jobDir(id)
	wqWait(t, "the shim's lock to be released", func() bool { alive, _ := sched.Probe(dir); return !alive })
	require.True(t, wqBeats(beat), "the orphaned command still runs")

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Lost, rec.State)
	assert.Equal(t, lostWhy, rec.Failure)
	assert.Nil(t, rec.Exit)
	wqWait(t, "the orphaned group to die", func() bool { return !wqBeats(beat) })
}

// Only a lost job's group is killed: a job that wrote its result has nothing
// the queue lost, whatever it left running.
func TestJobQueue_OnlyALostJobsGroupIsKilledNotADoneJobsLeftovers(t *testing.T) {
	m := wqLiveModel(t)
	beat := filepath.Join(t.TempDir(), "beat")
	script := `sh -c '` + wqBeatScript + `' sh "$1" >/dev/null 2>&1 & exit 0`
	id := wqLaunch(t, m, "sh", "-c", script, "sh", beat)
	shim := wqRecord(t, m, id).PID
	// The leftover keeps the shim's pid in use as its group, so no other
	// process can have taken it by the time this signal is sent.
	t.Cleanup(func() { _ = sched.SignalGroup(shim, syscall.SIGKILL) })

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Done, rec.State)
	assert.True(t, wqBeats(beat), "the job wrote its result: nothing is lost, so nothing is killed")
}

// A process that holds the shim's pid is a stranger (a reused pid) or the shim
// itself: its group is never signalled, lost job or not (spec §5.4).
func TestJobQueue_ALostJobsGroupIsLeftAloneWhenAProcessHoldsTheShimsPid(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%v", cancelled), func(t *testing.T) {
			m := wqLiveModel(t)
			stranger, beat := wqBystander(t)
			rec := wqEnded(1, 0)
			rec.State, rec.Exit, rec.Ended, rec.PID = sched.Running, nil, time.Time{}, stranger
			if cancelled {
				rec.CancelReason = "from the work view"
			}
			wqSeed(t, m, rec)

			got := wqSettle(t, m, 1)

			if cancelled {
				assert.Equal(t, sched.Cancelled, got.State)
			} else {
				assert.Equal(t, sched.Lost, got.State)
			}
			assert.True(t, wqBeats(beat), "the stranger carries on")
		})
	}
}

func TestJobQueue_AGroupIsOrphanedOnlyWhenItHasMembersAndNoProcessHasTheShimsPid(t *testing.T) {
	snap := func(procs ...sched.Proc) sched.Snapshot { return sched.Snapshot{Taken: time.Now(), Procs: procs} }
	cases := []struct {
		name string
		snap sched.Snapshot
		pgid int
		want bool
	}{
		{"members, no leader", snap(sched.Proc{PID: 20, PGID: 10}, sched.Proc{PID: 21, PGID: 10}), 10, true},
		{"the shim itself lives", snap(sched.Proc{PID: 10, PGID: 10}, sched.Proc{PID: 20, PGID: 10}), 10, false},
		{"a stranger holds the pid", snap(sched.Proc{PID: 10, PGID: 3}, sched.Proc{PID: 20, PGID: 10}), 10, false},
		{"nobody in the group", snap(sched.Proc{PID: 30, PGID: 30}), 10, false},
		{"pid 0 names the caller's own group", snap(sched.Proc{PID: 20, PGID: 0}), 0, false},
		{"pid 1 names everyone", snap(sched.Proc{PID: 20, PGID: 1}), 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, orphanedGroup(c.snap, c.pgid))
		})
	}
}

// A signal goes to a job's group only after its lock test proves the shim
// alive (spec §5.5): a stranger's group, a free lock and pid 0 are refused.
func TestJobQueue_ASignalGoesOnlyToAGroupWhoseShimHoldsItsLock(t *testing.T) {
	stranger, beat := wqBystander(t)

	t.Run("pid 0 names the caller's own group", func(t *testing.T) {
		sent, err := signalAlive(t.TempDir(), 0, syscall.Signal(0))
		assert.False(t, sent)
		assert.Error(t, err)
	})
	t.Run("a free lock", func(t *testing.T) {
		sent, err := signalAlive(t.TempDir(), stranger, syscall.SIGTERM)
		assert.NoError(t, err)
		assert.False(t, sent)
		assert.True(t, wqBeats(beat), "the stranger was not signalled")
	})
	t.Run("a held lock", func(t *testing.T) {
		m := wqLiveModel(t)
		id := wqLaunch(t, m, "sh", "-c", "sleep 30")

		sent, err := signalAlive(m.queue.jobDir(id), wqRecord(t, m, id).PID, syscall.SIGTERM)

		require.NoError(t, err)
		assert.True(t, sent)
		rec := wqSettle(t, m, id)
		assert.Equal(t, sched.Failed, rec.State, "nobody asked for a cancel")
		require.NotNil(t, rec.Exit)
		assert.Equal(t, "SIGTERM", rec.Exit.Signal)
	})
}

// wqSilent seeds what a start that timed out leaves: a failed record for a
// command that beats into beat, and an entry owing its shim a SIGKILL. start
// starts the shim (its dir exists, with the spec written) and returns its pid.
func wqSilent(t *testing.T, m *Model, beat string, start func(dir string) int) (id sched.JobID, pid int) {
	t.Helper()
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Failure = sched.Failed, nil, errRunnerSilent.Error()
	rec.Argv, rec.Cwd = []string{"sh", "-c", wqBeatScript, "sh", beat}, t.TempDir()
	wqSeed(t, m, rec)
	dir := m.queue.jobDir(rec.ID)
	require.NoError(t, sched.WriteSpec(dir, sched.Spec{ID: rec.ID, Argv: rec.Argv, Cwd: rec.Cwd, Env: []string{"PATH=" + os.Getenv("PATH")}}))
	pid = start(dir)
	t.Cleanup(func() { _ = sched.SignalGroup(pid, syscall.SIGKILL) })
	m.queue.silent[rec.ID] = silentShim{pid: pid, since: time.Now()}
	return rec.ID, pid
}

// A shim slowed by memory pressure can take its lock after the 5 s wait, and
// would run the command while its job reads failed (spec §5.3 step 4). The
// entry a failed start leaves keeps the group due a SIGKILL: a free lock with
// the shim still there proves nothing, and the kill goes once a lock test
// finds the shim alive.
func TestJobQueue_AFailedStartsSlowShimIsKilledOnceItsLockProvesItAlive(t *testing.T) {
	m := wqLiveModel(t)
	beat := filepath.Join(t.TempDir(), "beat")
	slow := sched.TestRunner()
	slow.Shim = append([]string{"sh", "-c", `sleep 1.5; exec "$@"`, "sh"}, slow.Shim...)
	id, _ := wqSilent(t, m, beat, func(dir string) int {
		pid, err := slow.Start(dir)
		require.NoError(t, err)
		return pid
	})

	wqPass(t, m)
	require.Contains(t, m.queue.silent, id, "the shim is not up yet: a free lock proves nothing")

	wqWait(t, "the shim to take its lock and be killed", func() bool {
		wqPass(t, m)
		_, owed := m.queue.silent[id]
		return !owed
	})

	assert.Equal(t, sched.Failed, wqRecord(t, m, id).State, "the job stays failed")
	assert.False(t, wqBeats(beat), "the command does not run on, unaccounted")
}

// Once no process has the shim's pid it cannot take its lock any more: the
// entry goes, with no signal.
func TestJobQueue_AFailedStartsShimThatIsGoneIsDropped(t *testing.T) {
	m := wqLiveModel(t)
	id, _ := wqSilent(t, m, filepath.Join(t.TempDir(), "beat"), func(dir string) int {
		pid, err := sched.Runner{Shim: []string{"true"}}.Start(dir)
		require.NoError(t, err)
		return pid
	})

	wqWait(t, "the entry to go", func() bool {
		wqPass(t, m)
		_, owed := m.queue.silent[id]
		return !owed
	})
}

// A shim that took its lock, started the command and died leaves a group
// nothing watches: a gone shim's orphaned group is sent SIGKILL under the
// lost-job rule.
func TestJobQueue_AFailedStartsOrphanedGroupIsKilledOnceItsShimIsGone(t *testing.T) {
	m := wqLiveModel(t)
	beat := filepath.Join(t.TempDir(), "beat")
	var shim int
	id, _ := wqSilent(t, m, beat, func(dir string) int {
		pid, err := m.queue.runner.Start(dir)
		require.NoError(t, err)
		shim = pid
		return pid
	})
	wqWait(t, "the first beat", func() bool { _, err := os.Stat(beat); return err == nil })
	p, err := os.FindProcess(shim)
	require.NoError(t, err)
	require.NoError(t, p.Kill(), "the shim alone: its command lives on")
	dir := m.queue.jobDir(id)
	wqWait(t, "the shim's lock to be released", func() bool { alive, _ := sched.Probe(dir); return !alive })
	require.True(t, wqBeats(beat), "the orphaned command still runs")

	wqWait(t, "the entry to go", func() bool {
		wqPass(t, m)
		_, owed := m.queue.silent[id]
		return !owed
	})

	wqWait(t, "the orphaned group to die", func() bool { return !wqBeats(beat) })
}

// A process that has the shim's pid may be a stranger that reused it: its
// group is never signalled, and the entry waits until silentLimit gives it up.
func TestJobQueue_AFailedStartsEntryNeverSignalsAStrangerAndIsGivenUpAfterTheLimit(t *testing.T) {
	m := wqLiveModel(t)
	stranger, beat := wqBystander(t)
	id, _ := wqSilent(t, m, filepath.Join(t.TempDir(), "unused"), func(string) int { return stranger })

	wqPass(t, m)
	wqPass(t, m)

	assert.Contains(t, m.queue.silent, id, "the pid is in use: not gone, and nothing proves the shim alive")
	assert.True(t, wqBeats(beat), "the stranger was not signalled")

	m.queue.silent[id] = silentShim{pid: stranger, since: time.Now().Add(-silentLimit)}
	assert.Empty(t, m.queuePassFor(false).jobs, "an entry owed for silentLimit is given up")
	assert.NotContains(t, m.queue.silent, id)
	assert.True(t, wqBeats(beat), "and still nothing was signalled")
}

func TestJobQueue_AJobsUsageIsSampledFromItsProcessGroup(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "sleep 30")

	wqPass(t, m)
	wqPass(t, m)

	u, ok := m.queue.usage[id]
	require.True(t, ok)
	assert.Greater(t, u.RSSKB, int64(0))
	assert.GreaterOrEqual(t, wqRecord(t, m, id).PeakRSSKB, u.RSSKB)
	rev := m.queue.rev

	m.queue.store.Jobs[m.queue.index(id)].CancelReason = "when the test ends"
	wqSettle(t, m, id)

	assert.NotContains(t, m.queue.usage, id, "a settled job's usage is dropped")
	assert.NotContains(t, m.queue.cpuMarks, id)
	assert.Greater(t, m.queue.rev, rev, "a client is told")
}

// A job whose start job is in flight may not hold its lock yet: the watch
// must not take it for lost.
func TestJobQueue_AJobStillStartingIsLeftToItsStartJob(t *testing.T) {
	m := wqLiveModel(t)
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Ended = sched.Running, nil, time.Time{}
	wqSeed(t, m, rec)
	m.queue.starting[1] = true

	assert.False(t, m.maybeQueue(), "nothing to watch yet")
	assert.Empty(t, m.Drain().Jobs)
	assert.Equal(t, sched.Running, wqRecord(t, m, 1).State)

	delete(m.queue.starting, 1)
	wqPass(t, m)
	assert.Equal(t, sched.Lost, wqRecord(t, m, 1).State, "once started, an absent shim is a lost job")
}

func TestJobQueue_AnObservationOfAJobThatMovedOnChangesNothing(t *testing.T) {
	m := wqModel(t)
	done := wqEnded(1, 0)
	done.Notify = false
	wqSeed(t, m, done)
	rev := m.queue.rev

	m.Deliver(queuePassResult{obs: []jobObs{{id: 1, gone: true}, {id: 99, gone: true}}})

	assert.Equal(t, sched.Done, wqRecord(t, m, 1).State, "a finished job is not settled again")
	assert.Equal(t, rev, m.queue.rev)
}

// A change the model makes on its own stays in memory when its write fails,
// raises one notice, and is written with the next save (spec §5.9).
func TestJobQueue_ASettleWhoseWriteFailsStaysInMemoryAndIsWrittenWithTheNextSave(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "exit 0")
	unblock := wqBlockJobsJSON(t, m)
	var events []Event
	wqWait(t, "the job to settle", func() bool {
		events = append(events, wqPass(t, m)...)
		return wqRecord(t, m, id).State.Finished()
	})

	assert.Equal(t, sched.Done, wqRecord(t, m, id).State, "the change stays in memory")
	m.saveQueueQuiet("another change")
	events = append(events, wqPump(t, m)...)
	var notices []string
	for _, ev := range events {
		if n, ok := ev.(Notice); ok && n.Err != nil {
			notices = append(notices, n.Err.Error())
		}
	}
	require.Len(t, notices, 1, "one notice for a run of failures")
	assert.Contains(t, notices[0], "kept in memory")

	unblock()
	require.True(t, m.saveQueueQuiet("the next save"))

	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	require.Len(t, saved.Jobs, 1)
	assert.Equal(t, sched.Done, saved.Jobs[0].State)
	assert.False(t, m.queue.saveFailed)
}

// A failed quiet save raised while the model works on a request's behalf is
// for every client: its Notice carries no request, while the request's own
// Reply keeps it.
func TestJobQueue_AQuietSaveFailureIsNoticedForEveryClient(t *testing.T) {
	m := wqLiveModel(t)
	wqSeed(t, m, wqQueuedRecord(t, 1))
	unblock := wqBlockJobsJSON(t, m)
	t.Cleanup(unblock)

	m.causedBy(7, func() {
		m.admitJobs() // starts job 1 and saves quietly
		m.reply(7, Reply{})
	})

	require.Equal(t, sched.Running, wqRecord(t, m, 1).State, "fixture: the pass started the job")
	var notices []Notice
	events := wqPump(t, m)
	for _, ev := range events {
		if n, ok := ev.(Notice); ok && n.Err != nil {
			notices = append(notices, n)
		}
	}
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0].Err.Error(), "kept in memory")
	assert.Zero(t, notices[0].Req, "the notice is for every client")
	r := replyTo(events, 7)
	require.NotNil(t, r, "the request's own reply")
	assert.Equal(t, ReqID(7), r.Req)
	assert.Equal(t, ReqID(0), m.cause, "fixture: the cause is back to none")
	m.causedBy(8, func() { m.notifyErr(errors.New("another")) })
	n, ok := wqPump(t, m)[0].(Notice)
	require.True(t, ok)
	assert.Equal(t, ReqID(8), n.Req, "an ordinary notice inside a request keeps its cause")
}

func TestJobQueue_AWatchRequestedMidFlightRunsAgainWhenItLands(t *testing.T) {
	m := wqLiveModel(t)
	wqLaunch(t, m, "sh", "-c", "sleep 30")
	require.True(t, m.maybeQueue())
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	m.gate(gateQueue).request() // a cancel arrives while the pass runs
	result := jobs[0]()

	m.Deliver(result)

	again := m.Drain().Jobs
	require.Len(t, again, 1, "the pass runs again as soon as the first one lands")
	m.Deliver(again[0]())
	assert.False(t, m.gate(gateQueue).inFlight)
}

func TestJobQueue_TheTickWatchesRunningJobs(t *testing.T) {
	m := wqLiveModel(t)
	wqLaunch(t, m, "sh", "-c", "sleep 30")

	m.Tick()

	assert.True(t, m.gate(gateQueue).inFlight, "the health tick dispatches the watch")
}

func TestJobQueue_RetentionDropsTheOldestFinishedJobsAndTheirDirectories(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"keep_finished": 2}`)
	base := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	for id := sched.JobID(1); id <= 4; id++ {
		rec := wqEnded(id, 0)
		rec.Ended = base.Add(time.Duration(id) * time.Minute)
		wqSeed(t, m, rec)
	}
	running := wqEnded(5, 0)
	running.State, running.Exit, running.Ended = sched.Running, nil, time.Time{}
	wqSeed(t, m, running)
	m.queue.store.NextID = 9
	m.refreshPolicy()

	m.afterSettle()
	wqPump(t, m)

	var ids []sched.JobID
	for _, rec := range m.queue.store.Jobs {
		ids = append(ids, rec.ID)
	}
	assert.Equal(t, []sched.JobID{3, 4, 5}, ids, "the two newest finished jobs stay, and the running one is no finished job")
	assert.NoDirExists(t, m.queue.jobDir(1))
	assert.NoDirExists(t, m.queue.jobDir(2))
	for id := sched.JobID(3); id <= 5; id++ {
		assert.DirExists(t, m.queue.jobDir(id))
	}
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Len(t, saved.Jobs, 3)
	assert.Equal(t, sched.JobID(9), saved.NextID, "the counter never goes back")
}

// A directory goes only once the shorter jobs.json that no longer lists its
// job is on disk.
func TestJobQueue_RetentionKeepsTheDirectoriesUntilTheShorterJobsFileIsWritten(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"keep_finished": 1}`)
	for id := sched.JobID(1); id <= 3; id++ {
		wqSeed(t, m, wqEnded(id, 0))
	}
	m.refreshPolicy()
	unblock := wqBlockJobsJSON(t, m)

	m.afterSettle()
	wqPump(t, m)

	assert.Len(t, m.queue.store.Jobs, 1)
	assert.DirExists(t, m.queue.jobDir(1), "jobs.json still lists job 1")
	assert.DirExists(t, m.queue.jobDir(2))

	unblock()
	require.True(t, m.saveQueueQuiet("the next save"))
	wqPump(t, m)

	assert.NoDirExists(t, m.queue.jobDir(1))
	assert.NoDirExists(t, m.queue.jobDir(2))
	assert.DirExists(t, m.queue.jobDir(3))
}

func TestJobQueue_RetentionDropsNothingWhileAdmissionIsPaused(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 5000, "keep_finished": 1}`)
	for id := sched.JobID(1); id <= 3; id++ {
		wqSeed(t, m, wqEnded(id, 0))
	}
	m.refreshPolicy()
	require.NotEmpty(t, m.queue.paused)

	m.afterSettle()
	wqPump(t, m)

	assert.Len(t, m.queue.store.Jobs, 3, "the limit is unknown while the block is invalid")
}

// The jobs run off the loop on snapshots: while one runs, the loop edits the
// queue in every way a request can. Under -race a job that read model state
// shows up here.
func TestJobQueue_AGatePassOverlapsTheLoopsEdits(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "sleep 30")
	m.gate(gateQueue).expedite()
	require.True(t, m.maybeQueue())
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)

	done := make(chan any, 1)
	go func() { done <- jobs[0]() }()
	for i := range 200 {
		extra := sched.JobID(100 + i)
		m.queue.store.Jobs[m.queue.index(id)].CancelReason = fmt.Sprintf("edit %d", i)
		m.queue.store.Jobs = append(m.queue.store.Jobs, wqEnded(extra, 0))
		m.queue.waiting[extra] = "edit"
		m.queue.usage[id] = jobUsage{RSSKB: int64(i)}
		m.queue.starting[extra] = true
		m.queue.cpuMarks[id] = cpuMark{cpu: time.Duration(i)}
		m.queue.rev++
	}
	m.Deliver(<-done)

	assert.False(t, m.gate(gateQueue).inFlight)
}

func TestJobQueue_AStartJobOverlapsTheLoopsEdits(t *testing.T) {
	m := wqLiveModel(t)
	id := wqSubmit(t, m, "sh", "-c", "exit 0")
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)

	done := make(chan any, 1)
	go func() { done <- jobs[0]() }()
	for i := range 200 {
		rec := &m.queue.store.Jobs[m.queue.index(id)]
		rec.Cwd, rec.Description = fmt.Sprintf("/elsewhere/%d", i), "edit"
		m.queue.store.Jobs = append(m.queue.store.Jobs, wqEnded(sched.JobID(100+i), 0))
		m.queue.rev++
	}
	m.Deliver(<-done)

	assert.Greater(t, wqRecord(t, m, id).PID, 1, "it started on the snapshot it was given")
}

// wqSnap is a process sample taken at at: the job's shim (pid pgid, leading
// the group and the session) and one child in both, together using rss KB
// and cpu of CPU time, and a bystander of another group and session that no
// job counts.
func wqSnap(at time.Time, pgid int, rss int64, cpu time.Duration) *sched.Snapshot {
	return &sched.Snapshot{Taken: at, Procs: []sched.Proc{
		{PID: pgid, PGID: pgid, SID: pgid, RSSKB: rss / 2, CPU: cpu / 2},
		{PID: pgid + 1, PGID: pgid, SID: pgid, RSSKB: rss - rss/2, CPU: cpu - cpu/2},
		{PID: 7, PGID: 7, SID: 7, RSSKB: 1 << 30, CPU: time.Hour},
	}}
}

// A job's usage sums its session, not its process group: a child that moved
// to a group of its own (coreutils timeout) still counts (sccache measurement
// spec §5.2).
func TestJobQueue_UsageSumsTheJobsSession(t *testing.T) {
	const pid = 4242
	m := wqModel(t)
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Ended, rec.PID, rec.Notify = sched.Running, nil, time.Time{}, pid, false
	wqSeed(t, m, rec)
	snap := &sched.Snapshot{Taken: time.Now(), Procs: []sched.Proc{
		{PID: pid, PGID: pid, SID: pid, RSSKB: 100},
		{PID: pid + 1, PGID: pid + 1, SID: pid, RSSKB: 900},
		{PID: 7, PGID: 7, SID: 7, RSSKB: 1 << 30},
	}}
	m.Deliver(queuePassResult{at: snap.Taken, obs: []jobObs{{id: 1, pid: pid}}, snap: snap})
	assert.Equal(t, int64(1000), m.queue.usage[1].RSSKB)
	assert.Equal(t, int64(1000), wqRecord(t, m, 1).PeakRSSKB)
}

// Usage comes from the samples a pass delivers (spec §5.4): cores are the
// change in the group's summed CPU time over the time between two samples, a
// sum that drops reads 0, and a failed sample (a nil snapshot) leaves the
// last values, with the pass's lock tests still going ahead.
func TestJobQueue_UsageFollowsTheSamplesAPassDelivers(t *testing.T) {
	const pid = 4242
	m := wqModel(t)
	for id, p := range map[sched.JobID]int{1: pid, 2: pid + 100} {
		rec := wqEnded(id, 0)
		rec.State, rec.Exit, rec.Ended, rec.PID, rec.Notify = sched.Running, nil, time.Time{}, p, false
		wqSeed(t, m, rec)
	}
	t0 := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	alive := []jobObs{{id: 1, pid: pid}}
	steps := []struct {
		name     string
		snap     *sched.Snapshot
		wantUse  jobUsage
		wantPeak int64
	}{
		{"the first sample has nothing to compare with", wqSnap(t0, pid, 1000, 10*time.Second), jobUsage{RSSKB: 1000}, 1000},
		{"3 s of CPU time over 2 s is 1.5 cores", wqSnap(t0.Add(2*time.Second), pid, 800, 13*time.Second), jobUsage{RSSKB: 800, CPU: 1.5}, 1000},
		{"a CPU sum that drops reads 0", wqSnap(t0.Add(4*time.Second), pid, 2000, 11*time.Second), jobUsage{RSSKB: 2000}, 2000},
		{"and the next sample compares with the dropped one", wqSnap(t0.Add(5*time.Second), pid, 1500, 12*time.Second), jobUsage{RSSKB: 1500, CPU: 1}, 2000},
		{"a failed sample leaves the last values", nil, jobUsage{RSSKB: 1500, CPU: 1}, 2000},
	}
	for _, s := range steps {
		m.Deliver(queuePassResult{at: t0, obs: alive, snap: s.snap})
		assert.Equal(t, s.wantUse, m.queue.usage[1], s.name)
		assert.Equal(t, s.wantPeak, wqRecord(t, m, 1).PeakRSSKB, s.name)
	}

	// A pass whose sample failed still settles a job whose shim is gone, and
	// leaves the other job's usage as it was.
	m.Deliver(queuePassResult{at: t0, obs: []jobObs{{id: 1, pid: pid}, {id: 2, pid: pid + 100, gone: true}}, sampleErr: errors.New("ps timed out")})
	assert.Equal(t, sched.Lost, wqRecord(t, m, 2).State, "settled without a sample")
	assert.Equal(t, jobUsage{RSSKB: 1500, CPU: 1}, m.queue.usage[1])
	assert.Equal(t, int64(2000), wqRecord(t, m, 1).PeakRSSKB)
	assert.Equal(t, sched.Running, wqRecord(t, m, 1).State)
}

// A failing ps is logged once per run of failures, and again after a sample
// that worked.
func TestJobQueue_AFailingSampleIsLoggedOncePerRun(t *testing.T) {
	m := wqModel(t)
	var buf bytes.Buffer
	prev := log.Structured
	log.Structured = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { log.Structured = prev })
	fail := queuePassResult{sampleErr: errors.New("ps timed out")}
	ok := queuePassResult{snap: &sched.Snapshot{Taken: time.Now()}}
	logged := func() int { return strings.Count(buf.String(), "queue.sample_failed") }

	m.Deliver(ok)
	assert.Equal(t, 0, logged(), "a sample that worked says nothing")
	m.Deliver(fail)
	assert.Equal(t, 1, logged(), "the first failure is logged")
	assert.Contains(t, buf.String(), "ps timed out")
	m.Deliver(fail)
	m.Deliver(fail)
	assert.Equal(t, 1, logged(), "a repeat stays quiet")
	m.Deliver(queuePassResult{})
	m.Deliver(fail)
	assert.Equal(t, 1, logged(), "a pass that took no sample ends no run")
	m.Deliver(ok)
	m.Deliver(fail)
	assert.Equal(t, 2, logged(), "the first failure after a success is logged again")
}

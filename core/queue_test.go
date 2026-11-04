package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqHost is the host the queue's tests run on: 16 cores and 32 GB of RAM, so
// the default caps are 14 cores, 24 GB and 2 I/O jobs, and a default claim is
// 4 cores and 6 GB. Three default jobs fit; the fourth waits on cores.
var wqHost = sched.Host{Name: "testhost", Cores: 16, RAMBytes: 32 << 30}

// wqModel is a model whose queue is loaded over a throwaway global dir, its
// shims the test binary itself (sched.TestRunner; TestMain hands over to the
// shim first).
func wqModel(t *testing.T) *Model {
	t.Helper()
	return wqModelAt(t, t.TempDir())
}

// wqModelAt is wqModel over the global dir dir. A second model over the same
// dir is a restarted daemon.
func wqModelAt(t *testing.T, dir string) *Model {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, dir)
	m := NewForTest(Options{})
	m.SetJobRunnerForTest(sched.TestRunner())
	m.loadQueue(dir, wqHost)
	return m
}

// wqEnded is a finished job's record: job id "polyoxide tests", submitted at
// 10:00:00, started 1m42s later and ended 3m10s after that, with exit 0.
func wqEnded(id sched.JobID, owner InstanceID) sched.Record {
	t0 := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	ended := t0.Add(292 * time.Second)
	return sched.Record{
		ID: id, Order: int64(id), Owner: uint64(owner), Description: "polyoxide tests",
		Argv: []string{"cargo", "test"}, Cwd: "/work", Notify: true, Tail: 2,
		State: sched.Done, Submitted: t0, Started: t0.Add(102 * time.Second), Ended: ended,
		Exit: &sched.Exit{Code: 0, Ended: ended},
	}
}

func TestJobQueue_AnEmptyQueueLoadsWithItsNextIDAtOne(t *testing.T) {
	m := wqModel(t)
	q := m.queue

	assert.True(t, q.ready())
	require.NoError(t, q.loadErr)
	assert.Empty(t, q.store.Jobs)
	assert.Equal(t, sched.JobID(1), q.store.NextID)
	assert.Equal(t, filepath.Join(q.cfgDir, "jobs"), q.dir)
	assert.Equal(t, wqHost, q.host)
	assert.Empty(t, q.paused)
	assert.Equal(t, sched.Caps{CPUs: 14, MemoryGB: 24, IOJobs: 2}, q.policy.Caps, "the host's defaults")
	assert.Equal(t, sched.Claim{CPUs: 4, MemoryGB: 6}, q.policy.DefaultClaim)
}

func TestJobQueue_ASavedQueueLoadsAsItWasSaved(t *testing.T) {
	dir := t.TempDir()
	saved := sched.Store{Version: sched.StoreVersion, NextID: 8, Jobs: []sched.Record{wqEnded(7, 0)}}
	require.NoError(t, sched.SaveStore(filepath.Join(dir, "jobs"), saved))

	m := wqModelAt(t, dir)

	assert.Equal(t, sched.JobID(8), m.queue.store.NextID)
	require.Len(t, m.queue.store.Jobs, 1)
	got := m.queue.store.Jobs[0]
	assert.Equal(t, sched.JobID(7), got.ID)
	assert.Equal(t, sched.Done, got.State)
	assert.Equal(t, "polyoxide tests", got.Description)
}

// A jobs.json that fails to load (a newer loom's, or one that is garbled) is
// left as it is: submissions are refused, admission pauses, the next TUI is
// told, and no save ever rewrites the file (spec §5.9).
func TestJobQueue_AJobsFileThatFailsToLoadIsLeftUntouched(t *testing.T) {
	cases := []struct {
		name, body, paused string
		newer              bool
	}{
		{"a newer loom's", `{"version": 99, "next_id": 5, "jobs": []}`, "jobs.json is from a newer loom", true},
		{"garbled", `{not json`, "jobs.json could not be loaded: ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "jobs", sched.StoreFile)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, []byte(c.body), 0o600))

			m := wqModelAt(t, dir)

			require.Error(t, m.queue.loadErr)
			assert.Equal(t, c.newer, errors.Is(m.queue.loadErr, sched.ErrNewerStore))
			assert.Contains(t, m.queue.paused, c.paused)
			assert.Equal(t, sched.Policy{}, m.queue.policy, "admission is paused")
			require.Error(t, m.saveQueue(), "a queue that failed to load never writes")
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, c.body, string(got))
			var notices []Notice
			for _, ev := range m.Drain().Events {
				if n, ok := ev.(Notice); ok {
					notices = append(notices, n)
				}
			}
			require.Len(t, notices, 1, "the next TUI is told")
			assert.Contains(t, notices[0].Err.Error(), "left as it is")
		})
	}
}

func TestJobQueue_AnInvalidSchedulerBlockPausesAdmissionAtLoad(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(`{"scheduler": {"cpus": 5000}}`), 0o644))

	m := wqModelAt(t, dir)

	assert.Equal(t, "scheduler.cpus is 5000: it must be 1–1024", m.queue.paused)
	assert.Equal(t, sched.Policy{}, m.queue.policy, "no cap is guessed")
	assert.Equal(t, 5000, m.queue.settings.CPUs, "the block as it was read")
}

func TestJobQueue_AdmissionPausesForTheMostBasicReasonFirst(t *testing.T) {
	resolve := errors.New("scheduler.cpus is 0: bad")
	q := newQueueState()

	assert.Equal(t, "", q.pauseReason(nil))
	assert.Equal(t, resolve.Error(), q.pauseReason(resolve))
	q.runnerErr = errors.New("no executable")
	assert.Equal(t, "no executable", q.pauseReason(resolve))
	q.loadErr = sched.ErrNewerStore
	assert.Equal(t, "jobs.json is from a newer loom", q.pauseReason(resolve))
	q.loadErr = errors.New("disk on fire")
	assert.Equal(t, "jobs.json could not be loaded: disk on fire", q.pauseReason(nil))
	q.runnerErr = sched.ErrUnsupported
	assert.Equal(t, sched.ErrUnsupported.Error(), q.pauseReason(resolve), "an unavailable queue hides every other reason")
}

// On Windows no shim runs: the queue is paused from Boot on with
// sched.ErrUnsupported's text (spec §10), whatever runner a test set.
func TestJobQueue_OnWindowsTheQueueIsPausedFromBootOn(t *testing.T) {
	queueGOOS = "windows"
	t.Cleanup(func() { queueGOOS = runtime.GOOS })

	m := wqModel(t)

	assert.ErrorIs(t, m.queue.runnerErr, sched.ErrUnsupported)
	assert.Equal(t, sched.ErrUnsupported.Error(), m.queue.paused)
	assert.Equal(t, sched.Policy{}, m.queue.policy, "no cap is guessed")
}

func TestJobQueue_ARunnerSetBeforeBootSurvivesIt(t *testing.T) {
	m := NewForTest(Options{})
	m.SetJobRunnerForTest(sched.Runner{Shim: []string{"true"}})
	m.queue.runnerErr = errors.New("left by NewRunner")
	m.SetJobRunnerForTest(sched.Runner{Shim: []string{"false"}})

	m.loadQueue(t.TempDir(), wqHost)

	assert.Equal(t, []string{"false"}, m.queue.runner.Shim, "loading the queue does not replace it")
	assert.NoError(t, m.queue.runnerErr, "setting it cleared the error NewRunner left")
}

func TestJobQueue_SaveQueueWritesTheStore(t *testing.T) {
	m := wqModel(t)
	m.queue.store.NextID = 9

	require.NoError(t, m.saveQueue())

	got, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.JobID(9), got.NextID)
}

// Boot loads the queue from the global config dir, after the workspaces.
func TestJobQueue_BootLoadsTheQueueFromTheGlobalDir(t *testing.T) {
	m := bootModel(t)
	global, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	saved := sched.Store{Version: sched.StoreVersion, NextID: 4, Jobs: []sched.Record{wqEnded(3, 0)}}
	require.NoError(t, sched.SaveStore(filepath.Join(global, "jobs"), saved))

	m.Boot()

	assert.Equal(t, filepath.Join(global, "jobs"), m.queue.dir)
	require.Len(t, m.queue.store.Jobs, 1)
	assert.Equal(t, sched.JobID(3), m.queue.store.Jobs[0].ID)
}

// wqJob is a valid submission of argv from a fresh folder: the default claim,
// no owner, no notice.
func wqJob(t *testing.T, argv ...string) sched.Submit {
	t.Helper()
	return sched.Submit{
		Argv: argv, Cwd: t.TempDir(), Env: []string{"PATH=" + os.Getenv("PATH")},
		Description: "test job", Tail: 20,
	}
}

// wqSubmit submits argv and returns the job's ID.
func wqSubmit(t *testing.T, m *Model, argv ...string) sched.JobID {
	t.Helper()
	id, warning, err := m.submitJob(wqJob(t, argv...))
	require.NoError(t, err)
	require.Empty(t, warning)
	return id
}

func TestJobQueue_ASubmittedJobIsQueuedPersistedAndAdmitted(t *testing.T) {
	m := wqModel(t)
	sub := wqJob(t, "sh", "-c", "exit 0")
	sub.Env = []string{"SECRET=hunter2"}
	sub.Description = "  a build  "
	sub.Notify, sub.Tail = true, 7

	id, warning, err := m.submitJob(sub)

	require.NoError(t, err)
	assert.Empty(t, warning)
	assert.Equal(t, sched.JobID(1), id)
	rec := wqRecord(t, m, id)
	assert.Equal(t, "a build", rec.Description, "trimmed")
	assert.Equal(t, sched.Running, rec.State, "it fits, so the pass that follows the submit admits it")
	assert.Equal(t, sched.Claim{CPUs: 4, MemoryGB: 6}, rec.Claim, "the default claim, applied")
	assert.Equal(t, int64(1), rec.Order)
	assert.True(t, rec.Notify)
	assert.Equal(t, 7, rec.Tail)
	assert.Zero(t, rec.Owner)
	assert.False(t, rec.Submitted.IsZero())
	assert.True(t, m.queue.starting[id], "its start job is in flight")
	assert.Len(t, m.Drain().Jobs, 1, "and queued, to be run by the loop")

	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.Equal(t, sched.Spec{ID: id, Argv: sub.Argv, Cwd: sub.Cwd, Env: sub.Env}, spec,
		"the environment is in spec.json, and the CPU hint is written when the job starts")
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	require.Len(t, saved.Jobs, 1)
	assert.Equal(t, sched.Running, saved.Jobs[0].State)
	assert.Equal(t, sched.JobID(2), saved.NextID)
	raw, err := os.ReadFile(filepath.Join(m.queue.dir, sched.StoreFile))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "hunter2", "jobs.json holds no environment")
}

func TestJobQueue_AClaimTakesTheDefaultsExceptWhereItIsExclusive(t *testing.T) {
	cases := []struct {
		name        string
		claim, want sched.Claim
	}{
		{"empty", sched.Claim{}, sched.Claim{CPUs: 4, MemoryGB: 6}},
		{"cores only", sched.Claim{CPUs: 2}, sched.Claim{CPUs: 2, MemoryGB: 6}},
		{"memory and io", sched.Claim{MemoryGB: 3, IO: true}, sched.Claim{CPUs: 4, MemoryGB: 3, IO: true}},
		{"exclusive", sched.Claim{Exclusive: true}, sched.Claim{Exclusive: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqModel(t)
			sub := wqJob(t, "true")
			sub.Claim = c.claim

			id, _, err := m.submitJob(sub)

			require.NoError(t, err)
			assert.Equal(t, c.want, wqRecord(t, m, id).Claim)
		})
	}
}

func TestJobQueue_ASubmitOutsideTheBoundsIsRefusedAndChangesNothing(t *testing.T) {
	cases := []struct {
		name string
		edit func(*sched.Submit)
		want string
	}{
		{"no description", func(s *sched.Submit) { s.Description = "  " }, "the description is empty"},
		{"no command", func(s *sched.Submit) { s.Argv = nil }, "no command to run"},
		{"a relative folder", func(s *sched.Submit) { s.Cwd = "work" }, "the folder work is not an absolute path"},
		{"a folder that is gone", func(s *sched.Submit) { s.Cwd = filepath.Join(s.Cwd, "gone") }, "the folder %CWD% does not exist"},
		{"a negative tail", func(s *sched.Submit) { s.Tail = -1 }, "--tail is -1: it must be 0–200"},
		{"too many cores", func(s *sched.Submit) { s.Claim.CPUs = 99 }, "claims 99 cores; this host's cap is 14"},
		{"too much memory", func(s *sched.Submit) { s.Claim.MemoryGB = 99 }, "claims 99 GB; this host's cap is 24 GB"},
		{"exclusive with cores", func(s *sched.Submit) { s.Claim = sched.Claim{Exclusive: true, CPUs: 2} },
			"--exclusive claims everything: drop --cpus, --mem and --io"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqModel(t)
			sub := wqJob(t, "true")
			c.edit(&sub)

			id, _, err := m.submitJob(sub)

			assert.EqualError(t, err, strings.ReplaceAll(c.want, "%CWD%", sub.Cwd))
			assert.Zero(t, id)
			assert.Empty(t, m.queue.store.Jobs)
			assert.Equal(t, sched.JobID(1), m.queue.store.NextID)
			assert.NoDirExists(t, m.queue.dir, "nothing was written")
			assert.Empty(t, m.Drain().Jobs)
		})
	}
}

func TestJobQueue_AJobsOwnerIsTheSessionHoldingItsLaunch(t *testing.T) {
	m := wqModel(t)
	inst := modInst(t, m, "owner") // launch L1
	cases := []struct {
		name, launch, warning string
		owner                 uint64
	}{
		{"a served launch", "L1", "", uint64(m.idOf(inst))},
		{"a plain shell", "", "", 0},
		{"a launch nobody holds", "L9",
			"this shell's loom session (launch L9) isn't one the daemon serves: job 3 has no owner, so no message will come", 0},
	}
	for i, c := range cases {
		sub := wqJob(t, "true")
		sub.Launch = c.launch

		id, warning, err := m.submitJob(sub)

		require.NoError(t, err, c.name)
		assert.Equal(t, sched.JobID(i+1), id)
		assert.Equal(t, c.warning, warning, c.name)
		assert.Equal(t, c.owner, wqRecord(t, m, id).Owner, c.name)
	}
}

func TestJobQueue_AdmissionFillsTheCapsAndTheRestWait(t *testing.T) {
	m := wqModel(t)
	var ids []sched.JobID
	for range 4 {
		ids = append(ids, wqSubmit(t, m, "sleep", "30"))
	}
	for _, id := range ids[:3] {
		assert.Equal(t, sched.Running, wqRecord(t, m, id).State)
	}
	assert.Equal(t, sched.Queued, wqRecord(t, m, ids[3]).State)
	assert.Equal(t, "needs 4 cores (2 free)", m.queue.waiting[ids[3]])
	assert.Len(t, m.Drain().Jobs, 3, "three starts, which this test never runs")

	// A job ends: the pass that follows starts the fourth.
	m.finishJob(m.queue.index(ids[0]), sched.Done, time.Now())
	m.afterSettle()

	assert.Equal(t, sched.Running, wqRecord(t, m, ids[3]).State)
	assert.NotContains(t, m.queue.waiting, ids[3])
	assert.Len(t, m.Drain().Jobs, 1)
}

// The tick runs an admission pass while jobs wait, which picks up a hand edit
// of the scheduler block, at most once a queue interval.
func TestJobQueue_TheTickRereadsTheCapsWhileJobsWait(t *testing.T) {
	m := wqModel(t)
	loaded := m.queue.lastPass
	m.queue.lastPass = loaded.Add(-time.Hour)
	m.tickQueue()
	assert.Equal(t, loaded.Add(-time.Hour), m.queue.lastPass, "nothing waits, so no pass")
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	wqSubmit(t, m, "true")
	waiting := wqSubmit(t, m, "true")
	require.Equal(t, sched.Queued, wqRecord(t, m, waiting).State)
	assert.Equal(t, "needs 4 cores (0 free)", m.queue.waiting[waiting])

	wqConfig(t, m, `{"cpus": 8}`) // edited by hand
	m.tickQueue()
	assert.Equal(t, sched.Queued, wqRecord(t, m, waiting).State, "a pass ran a moment ago")

	m.queue.lastPass = time.Now().Add(-queueInterval)
	m.tickQueue()
	assert.Equal(t, sched.Running, wqRecord(t, m, waiting).State)
}

// An idle queue rereads the scheduler block from the tick too, so a hand edit
// reaches the published view (Settings, Policy, Paused) within a queue
// interval, and a reread that finds the file unchanged publishes nothing.
func TestJobQueue_AnIdleQueuesTickRereadsAndRepublishesTheBlock(t *testing.T) {
	m := wqModel(t)
	require.Len(t, queueEvents(m.Sync().Events), 1, "published the first time")
	require.False(t, m.queue.hasQueued())

	m.queue.lastPolicy = time.Now().Add(-queueInterval)
	m.tickQueue()
	assert.Empty(t, queueEvents(m.Sync().Events), "an unchanged file republishes nothing")

	wqConfig(t, m, `{"cpus": 6}`) // edited by hand
	m.tickQueue()
	assert.Empty(t, queueEvents(m.Sync().Events), "a read a moment ago: the edit waits for the interval")

	m.queue.lastPolicy = time.Now().Add(-queueInterval)
	m.tickQueue()
	got := queueEvents(m.Sync().Events)
	require.Len(t, got, 1, "the edit is published")
	assert.Equal(t, 6, got[0].View.Settings.CPUs)
	assert.Equal(t, 6, got[0].View.Policy.Caps.CPUs)
	assert.Empty(t, got[0].View.Paused)

	m.queue.lastPolicy = time.Now().Add(-queueInterval)
	m.tickQueue()
	assert.Empty(t, queueEvents(m.Sync().Events), "read again, unchanged: nothing")
}

// A garbled config.json read by an idle queue's tick pauses admission with the
// reason, publishes it, and is left where it is.
func TestJobQueue_AnIdleQueuesTickPausesOnAGarbledConfig(t *testing.T) {
	dir := t.TempDir()
	m := wqModelAt(t, dir)
	queueEvents(m.Sync().Events)
	garbled := []byte(`{"scheduler": [`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), garbled, 0o644))

	m.queue.lastPolicy = time.Now().Add(-queueInterval)
	m.tickQueue()

	got := queueEvents(m.Sync().Events)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].View.Paused, "config.json could not be read: ")
	assert.Equal(t, sched.Policy{}, got[0].View.Policy)
	data, err := os.ReadFile(filepath.Join(dir, config.ConfigFileName))
	require.NoError(t, err)
	assert.Equal(t, string(garbled), string(data), "left as it is")
	wqNoSiblings(t, dir)
}

func TestJobQueue_AnInvalidSchedulerBlockPausesAdmissionAndSaysWhy(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`)
	running := wqSubmit(t, m, "true")
	waiting := wqSubmit(t, m, "true")
	reason := "scheduler.cpus is 5000: it must be 1–1024"

	wqConfig(t, m, `{"cpus": 5000}`)
	m.admitJobs()

	assert.Equal(t, reason, m.queue.paused)
	assert.Equal(t, sched.Policy{}, m.queue.policy)
	assert.Equal(t, "admission paused: "+reason, m.queue.waiting[waiting])
	assert.Equal(t, sched.Running, wqRecord(t, m, running).State, "running jobs are left alone")
	_, _, err := m.submitJob(wqJob(t, "true"))
	assert.EqualError(t, err, "admission paused: "+reason)

	wqConfig(t, m, `{"cpus": 8}`)
	m.admitJobs()

	assert.Empty(t, m.queue.paused)
	assert.Equal(t, sched.Running, wqRecord(t, m, waiting).State, "the fixed block admits what waited")
}

// On Windows the refusal comes before every other check: a submission that is
// also invalid still hears that the queue isn't available, and so does one made
// before the queue loaded.
func TestJobQueue_AWindowsDaemonRefusesEverySubmission(t *testing.T) {
	was := queueGOOS
	queueGOOS = "windows"
	t.Cleanup(func() { queueGOOS = was })

	t.Run("a queue booted there", func(t *testing.T) {
		m := wqModel(t)

		_, _, err := m.submitJob(wqJob(t, "true"))
		assert.ErrorIs(t, err, sched.ErrUnsupported)
		assert.EqualError(t, err, "the work queue isn't available on Windows")

		_, _, err = m.submitJob(sched.Submit{})
		assert.ErrorIs(t, err, sched.ErrUnsupported, "before the submission is even checked")
		assert.Empty(t, m.queue.store.Jobs)
		assert.NoDirExists(t, m.queue.jobDir(1))
	})
	t.Run("a queue not loaded", func(t *testing.T) {
		m := NewForTest(Options{})

		_, _, err := m.submitJob(wqJob(t, "true"))

		assert.ErrorIs(t, err, sched.ErrUnsupported)
	})
	t.Run("an unsupported runner elsewhere refuses the same way", func(t *testing.T) {
		queueGOOS = was
		m := wqModel(t)
		m.queue.runnerErr = sched.ErrUnsupported

		_, _, err := m.submitJob(wqJob(t, "true"))

		assert.ErrorIs(t, err, sched.ErrUnsupported)
	})
	t.Run("another failure of NewRunner pauses admission and refuses with its reason", func(t *testing.T) {
		queueGOOS = was
		m := wqModel(t)
		m.queue.runnerErr = errors.New("no executable")

		_, _, err := m.submitJob(wqJob(t, "true"))

		assert.EqualError(t, err, "admission paused: no executable")
	})
}

// A failed write refuses a request and changes nothing (spec §5.9).
func TestJobQueue_ASubmitWhoseWriteFailsIsRefusedAndTakenBack(t *testing.T) {
	m := wqModel(t)
	unblock := wqBlockJobsJSON(t, m)

	_, _, err := m.submitJob(wqJob(t, "true"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "write jobs.json")
	assert.Empty(t, m.queue.store.Jobs)
	assert.Equal(t, sched.JobID(1), m.queue.store.NextID)
	assert.NoDirExists(t, m.queue.jobDir(1), "its spec was taken back")
	assert.Empty(t, m.Drain().Jobs, "and nothing was started")

	unblock()
	assert.Equal(t, sched.JobID(1), wqSubmit(t, m, "true"), "the ID was not spent")
}

// The job's directory is made exclusively (spec §5.9): one already there,
// from a counter that fell behind the disk, is refused, left untouched, and
// takes neither the spec nor a removal. The queue's jobs are unchanged and
// NextID moves past the directory, so the refusal is of one submit, not of
// every later one.
func TestJobQueue_ASubmitWhoseDirectoryAlreadyExistsIsRefusedAndLeavesItAlone(t *testing.T) {
	m := wqModel(t)
	planted := filepath.Join(m.queue.jobDir(1), "output.log")
	require.NoError(t, os.MkdirAll(m.queue.jobDir(1), 0o700))
	require.NoError(t, os.WriteFile(planted, []byte("an old job's output"), 0o600))
	before := m.queue.store

	_, _, err := m.submitJob(wqJob(t, "true"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Equal(t, before.Jobs, m.queue.store.Jobs, "the queue's jobs are unchanged")
	assert.Equal(t, sched.JobID(2), m.queue.store.NextID, "NextID moved past the directory")
	got, rerr := os.ReadFile(planted)
	require.NoError(t, rerr, "the planted directory survives")
	assert.Equal(t, "an old job's output", string(got))
	entries, derr := os.ReadDir(m.queue.jobDir(1))
	require.NoError(t, derr)
	assert.Len(t, entries, 1, "and no spec.json was written into it")
	assert.NoFileExists(t, filepath.Join(m.queue.dir, sched.StoreFile), "jobs.json was not written")
	assert.Empty(t, m.Drain().Jobs, "nothing was started")

	// The refusal does not wedge the queue: the next submit takes the next
	// ID, and the directory of job 1 is still not touched.
	assert.Equal(t, sched.JobID(2), wqSubmit(t, m, "true"))
	assert.FileExists(t, planted)
}

func TestJobQueue_ARequestWhoseWriteFailsChangesNothing(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	running := wqSubmit(t, m, "true")
	waiting := wqSubmit(t, m, "true")
	last := wqSubmit(t, m, "true")
	m.queue.store.Jobs[m.queue.index(waiting)].Owner = 77
	m.Drain()
	unblock := wqBlockJobsJSON(t, m)
	defer unblock()
	before := slices.Clone(m.queue.store.Jobs)

	assert.Error(t, m.cancelJob(waiting, "from the work view"))
	assert.Error(t, m.cancelJob(running, "from the work view"))
	assert.Error(t, m.moveJobToFront(last))
	assert.Error(t, m.notifyJob(waiting))

	assert.Equal(t, before, m.queue.store.Jobs, "nothing changed")
}

func TestJobQueue_CancelEndsAQueuedJobAtOnceAndRecordsARunningJobsRequest(t *testing.T) {
	m := wqModel(t)
	var ids []sched.JobID
	for range 4 {
		ids = append(ids, wqSubmit(t, m, "sleep", "30"))
	}
	m.Drain() // the three start jobs are never run here
	running, queued := ids[0], ids[3]

	require.NoError(t, m.cancelJob(queued, "from the work view"))

	rec := wqRecord(t, m, queued)
	assert.Equal(t, sched.Cancelled, rec.State)
	assert.Equal(t, "from the work view", rec.CancelReason)
	assert.False(t, rec.Ended.IsZero())
	assert.NotContains(t, m.queue.waiting, queued)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.Cancelled, saved.Jobs[3].State, "written")

	require.NoError(t, m.cancelJob(running, "with loom job cancel"))
	assert.Equal(t, sched.Running, wqRecord(t, m, running).State, "gateQueue ends a running job")
	assert.Equal(t, "with loom job cancel", wqRecord(t, m, running).CancelReason)
	require.NoError(t, m.cancelJob(running, "from the work view"), "asked again")
	assert.Equal(t, "with loom job cancel", wqRecord(t, m, running).CancelReason, "the first request stands")

	assert.EqualError(t, m.cancelJob(queued, "x"), "job 4 has finished")
	assert.EqualError(t, m.cancelJob(99, "x"), "there is no job 99")
}

// The whole path: a running job is cancelled, its group gets SIGTERM, it
// settles, and the pass that follows starts the job that waited on its claim.
func TestJobQueue_CancellingARunningJobFreesItsClaimForTheNextWaitingOne(t *testing.T) {
	m := wqLiveModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	long := wqSubmit(t, m, "sleep", "30")
	next := wqSubmit(t, m, "sh", "-c", "echo next")
	wqPump(t, m) // the first one's start: a real shim
	require.Equal(t, sched.Queued, wqRecord(t, m, next).State)
	assert.Equal(t, "needs 4 cores (0 free)", m.queue.waiting[next])

	require.NoError(t, m.cancelJob(long, "from the work view"))
	rec := wqSettle(t, m, long)

	assert.Equal(t, sched.Cancelled, rec.State)
	require.NotNil(t, rec.Exit)
	assert.Equal(t, "SIGTERM", rec.Exit.Signal)
	assert.Equal(t, sched.Done, wqSettle(t, m, next).State, "its claim freed, the waiting job ran")
}

func TestJobQueue_MoveToFrontPutsAJobAheadOfTheQueue(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	first := wqSubmit(t, m, "true")
	second := wqSubmit(t, m, "true")
	third := wqSubmit(t, m, "true")
	require.Equal(t, sched.Queued, wqRecord(t, m, third).State)

	require.NoError(t, m.moveJobToFront(third))

	assert.Equal(t, int64(1), wqRecord(t, m, third).Order, "one below the lowest queued order, the second job's 2")
	m.finishJob(m.queue.index(first), sched.Done, time.Now())
	m.afterSettle()
	assert.Equal(t, sched.Running, wqRecord(t, m, third).State, "the pass that follows starts the moved job first")
	assert.Equal(t, sched.Queued, wqRecord(t, m, second).State)

	assert.EqualError(t, m.moveJobToFront(third), "job 3 isn't waiting", "it runs now")
	assert.EqualError(t, m.moveJobToFront(first), "job 1 isn't waiting", "it has finished")
	assert.EqualError(t, m.moveJobToFront(99), "there is no job 99")
}

func TestJobQueue_NotifyJobSetsTheFlagWhereAMessageCanComeBack(t *testing.T) {
	m := wqModel(t)
	queued := func(id sched.JobID, owner InstanceID) sched.Record {
		r := wqEnded(id, owner)
		r.State, r.Exit, r.Ended, r.Started, r.Notify = sched.Queued, nil, time.Time{}, time.Time{}, false
		return r
	}
	wqSeed(t, m, queued(1, 77))
	wqSeed(t, m, queued(2, 0))
	wqSeed(t, m, wqEnded(3, 77))

	require.NoError(t, m.notifyJob(1))

	assert.True(t, wqRecord(t, m, 1).Notify)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.True(t, saved.Jobs[0].Notify, "written")
	assert.EqualError(t, m.notifyJob(2), "job 2 has no owner")
	assert.EqualError(t, m.notifyJob(3), "job 3 has finished")
	assert.EqualError(t, m.notifyJob(99), "there is no job 99")
}

// A job being cancelled (its running process not gone yet) takes no notify flag
// back: cancelOwnerJobs cleared it so no notice would come, and a later
// notifyJob must not undo that.
func TestJobQueue_NotifyJobRefusesAJobBeingCancelled(t *testing.T) {
	m := wqModel(t)
	running := wqEnded(1, 77)
	running.State, running.Exit, running.Ended, running.Notify = sched.Running, nil, time.Time{}, false
	wqSeed(t, m, running)
	running.ID, running.CancelReason = 2, "because the session was paused"
	wqSeed(t, m, running)

	assert.EqualError(t, m.notifyJob(2), "job 2 is being cancelled")
	assert.False(t, wqRecord(t, m, 2).Notify, "the flag stays cleared")
	require.NoError(t, m.notifyJob(1), "a job not being cancelled still takes it")
	assert.True(t, wqRecord(t, m, 1).Notify)
}

func TestJobQueue_SetSchedulerCapsWritesTheBlockAndAppliesItAtOnce(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, config.ConfigFileName)
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"default_program": "my-claude", "scheduler": {"cpus": 4}}`), 0o644))
	m := wqModelAt(t, dir)
	global := NewWorkspace(WorkspaceParts{Ctx: &config.WorkspaceContext{ConfigDir: dir}, Config: config.LoadConfigFrom(dir)})
	m.SetWorkspacesForTest(global)
	wqSubmit(t, m, "true")
	second := wqSubmit(t, m, "true")
	require.Equal(t, sched.Queued, wqRecord(t, m, second).State, "four cores hold one default job")

	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 8, MemoryGB: 12}))

	assert.Equal(t, sched.Running, wqRecord(t, m, second).State, "a caps edit applies at once")
	written := config.LoadConfigFrom(dir)
	assert.Equal(t, config.SchedulerSettings{CPUs: 8, MemoryGB: 12}, written.GetScheduler())
	assert.Equal(t, "my-claude", written.DefaultProgram, "the rest of config.json is kept")
	assert.Equal(t, 8, global.cfg.GetScheduler().CPUs, "and so is the global workspace's own copy")

	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	err = m.setSchedulerCaps(config.SchedulerSettings{CPUs: 5000})
	assert.EqualError(t, err, "scheduler.cpus is 5000: it must be 1–1024")
	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a refused edit writes nothing")
}

// The whole path for one job: submit, start (a real shim and its handshake),
// the CPU claim in spec.json, watch and settle.
func TestJobQueue_AStartedJobRecordsItsShimAndGetsItsCPUHint(t *testing.T) {
	cases := []struct {
		name  string
		claim sched.Claim
		hint  int
	}{
		{"the default claim", sched.Claim{}, 4},
		{"its own cores", sched.Claim{CPUs: 3}, 3},
		{"exclusive: the cpu cap", sched.Claim{Exclusive: true}, 14},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqLiveModel(t)
			sub := wqJob(t, "sh", "-c", "echo built")
			sub.Claim = c.claim
			id, _, err := m.submitJob(sub)
			require.NoError(t, err)

			wqPump(t, m)

			rec := wqRecord(t, m, id)
			assert.Greater(t, rec.PID, 1)
			assert.False(t, rec.Started.IsZero())
			assert.NotContains(t, m.queue.starting, id)
			spec, err := sched.ReadSpec(m.queue.jobDir(id))
			require.NoError(t, err)
			assert.Equal(t, c.hint, spec.HintCPUs)

			assert.Equal(t, sched.Done, wqSettle(t, m, id).State)
			out, err := os.ReadFile(filepath.Join(m.queue.jobDir(id), sched.LogFile))
			require.NoError(t, err)
			assert.Equal(t, "built\n", string(out))
		})
	}
}

func TestJobQueue_AStartThatCannotWorkFailsTheJob(t *testing.T) {
	t.Run("its folder is gone", func(t *testing.T) {
		m := wqLiveModel(t)
		sub := wqJob(t, "true")
		sub.Cwd = filepath.Join(t.TempDir(), "work")
		require.NoError(t, os.Mkdir(sub.Cwd, 0o755))
		id, _, err := m.submitJob(sub)
		require.NoError(t, err)
		require.NoError(t, os.Remove(sub.Cwd))

		wqPump(t, m)

		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.Failed, rec.State)
		assert.Equal(t, "its folder is gone", rec.Failure)
		assert.False(t, rec.Ended.IsZero())
		assert.Zero(t, rec.PID, "nothing started")
	})
	t.Run("the runner writes no started.json", func(t *testing.T) {
		m := wqLiveModel(t)
		m.SetJobRunnerForTest(sched.Runner{Shim: []string{"true"}})
		m.queue.handshake = 200 * time.Millisecond
		id := wqSubmit(t, m, "true")

		wqPump(t, m)

		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.Failed, rec.State)
		assert.Equal(t, "the job runner did not start", rec.Failure)
		assert.NotContains(t, m.queue.starting, id)
		assert.Contains(t, m.queue.silent, id, "a free lock does not prove the shim gone: its group stays owed a SIGKILL")
	})
	t.Run("a cancel asked for meanwhile wins", func(t *testing.T) {
		m := wqLiveModel(t)
		m.SetJobRunnerForTest(sched.Runner{Shim: []string{"true"}})
		m.queue.handshake = 200 * time.Millisecond
		id := wqSubmit(t, m, "true")
		require.NoError(t, m.cancelJob(id, "with loom job cancel"))

		wqPump(t, m)

		assert.Equal(t, sched.Cancelled, wqRecord(t, m, id).State)
	})
}

// wqRestart is a daemon restarted over dir: a second model over the same
// global dir, which settles every job it still finds running when the test
// ends.
func wqRestart(t *testing.T, dir string) *Model {
	t.Helper()
	m := wqModelAt(t, dir)
	t.Cleanup(func() { wqStop(t, m) })
	return m
}

// wqShimDone waits until job id's shim has written its result and let go of
// its lock.
func wqShimDone(t *testing.T, m *Model, id sched.JobID) {
	t.Helper()
	dir := m.queue.jobDir(id)
	wqWait(t, "the shim to finish", func() bool {
		if _, err := sched.ReadExit(dir); err != nil {
			return false
		}
		alive, _ := sched.Probe(dir)
		return !alive
	})
}

// wqGated is a command that runs until the file gate exists, then exits with
// code: a job a test can keep running across a restart and end when it likes.
func wqGated(gate string, code int) []string {
	return []string{"sh", "-c", fmt.Sprintf(`while [ ! -e "$1" ]; do sleep 0.05; done; exit %d`, code), "sh", gate}
}

func TestJobQueue_ABootAdoptsARunningJobAndSettlesItLater(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(t.TempDir(), "go")
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, wqGated(gate, 7)...)
	old.queue.store.Jobs[0].PeakRSSKB = 1 << 40
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	rec := wqRecord(t, m, id)
	assert.Equal(t, sched.Running, rec.State)
	assert.Equal(t, wqRecord(t, old, id).PID, rec.PID)
	assert.Less(t, rec.PeakRSSKB, int64(1<<40), "the peak restarts from zero when a daemon adopts a job")
	assert.NotContains(t, m.queue.starting, id)

	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	done := wqSettle(t, m, id)
	assert.Equal(t, sched.Failed, done.State)
	require.NotNil(t, done.Exit)
	assert.Equal(t, 7, done.Exit.Code)
}

func TestJobQueue_ABootSettlesWhatEndedWhileTheDaemonWasDown(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, "sh", "-c", "exit 0")
	wqShimDone(t, old, id)
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	rec := wqRecord(t, m, id)
	assert.Equal(t, sched.Done, rec.State)
	require.NotNil(t, rec.Exit)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.Done, saved.Jobs[0].State, "and written")
}

func TestJobQueue_ABootCallsAShimThatVanishedLost(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Ended, rec.PID = sched.Running, nil, time.Time{}, 999999
	wqSeed(t, old, rec)
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	got := wqRecord(t, m, 1)
	assert.Equal(t, sched.Lost, got.State)
	assert.Equal(t, lostWhy, got.Failure)
}

// A start whose save failed leaves a running shim behind a queued record: it
// is adopted as running, and never started twice (spec §5.6).
func TestJobQueue_ABootAdoptsAShimBehindAQueuedRecord(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(t.TempDir(), "go")
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, wqGated(gate, 0)...)
	old.queue.store.Jobs[0].State, old.queue.store.Jobs[0].PID, old.queue.store.Jobs[0].Started = sched.Queued, 0, time.Time{}
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	got := wqRecord(t, m, id)
	assert.Equal(t, sched.Running, got.State, "adopted")
	assert.Greater(t, got.PID, 1)
	assert.Empty(t, m.queue.starting)
	assert.Empty(t, m.Drain().Jobs, "no start job: it is never started twice")
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	assert.Equal(t, sched.Done, wqSettle(t, m, id).State)
}

func TestJobQueue_ABootSettlesAQueuedRecordFromItsExit(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, "sh", "-c", "exit 3")
	wqShimDone(t, old, id)
	old.queue.store.Jobs[0].State, old.queue.store.Jobs[0].PID, old.queue.store.Jobs[0].Started = sched.Queued, 0, time.Time{}
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	got := wqRecord(t, m, id)
	assert.Equal(t, sched.Failed, got.State)
	require.NotNil(t, got.Exit)
	assert.Equal(t, 3, got.Exit.Code)
	assert.Empty(t, m.Drain().Jobs, "it is never started twice")
}

// A queued record whose shim never ran stays queued through adoption, and the
// first admission pass, which comes after it, starts it.
func TestJobQueue_ABootLeavesAnUnstartedQueuedJobToTheFirstPass(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	queued := wqEnded(1, 0)
	queued.State, queued.Exit, queued.Started, queued.Ended = sched.Queued, nil, time.Time{}, time.Time{}
	wqSeed(t, old, queued)
	require.NoError(t, old.saveQueue())

	m := wqModelAt(t, dir)

	assert.Equal(t, sched.Running, wqRecord(t, m, 1).State, "admitted")
	assert.True(t, m.queue.starting[1])
	assert.Len(t, m.Drain().Jobs, 1, "with one start job")
}

// The first admission pass counts the jobs boot adopted: with three default
// jobs running, the fourth still waits for cores.
func TestJobQueue_ABootCountsAdoptedJobsBeforeItAdmits(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	t.Cleanup(func() { wqStop(t, old) })
	var ids []sched.JobID
	for range 3 {
		ids = append(ids, wqSubmit(t, old, "sleep", "30"))
	}
	wqPump(t, old)
	fourth := wqSubmit(t, old, "sleep", "30")
	require.Equal(t, sched.Queued, wqRecord(t, old, fourth).State)

	m := wqRestart(t, dir)

	for _, id := range ids {
		assert.Equal(t, sched.Running, wqRecord(t, m, id).State)
	}
	assert.Equal(t, sched.Queued, wqRecord(t, m, fourth).State, "adoption came before admission")
	assert.Equal(t, "needs 4 cores (2 free)", m.queue.waiting[fourth])
	assert.Empty(t, m.queue.starting)
}

func TestJobQueue_ABootMarksNoticesStillOnTheirWayUndelivered(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	pending := wqEnded(1, 77)
	sent := wqEnded(2, 77)
	sent.Notice = sched.NoticeSent
	quiet := wqEnded(3, 77)
	quiet.Notify = false
	ownerless := wqEnded(4, 0)
	for _, rec := range []sched.Record{pending, sent, quiet, ownerless} {
		wqSeed(t, old, rec)
	}
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	got := wqRecord(t, m, 1)
	assert.Equal(t, sched.NoticeUndelivered, got.Notice)
	assert.Equal(t, "the daemon stopped before the message was delivered", got.NoticeWhy)
	assert.Equal(t, sched.NoticeSent, wqRecord(t, m, 2).Notice)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 3).Notice)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 4).Notice)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.NoticeUndelivered, saved.Jobs[0].Notice, "and written")
}

// A job that ended while no daemon ran is settled at boot, and its owner, a
// session this daemon serves, is told: its notice is not taken for one that
// was lost with the old daemon.
func TestJobQueue_ABootSendsTheNoticeOfAJobThatEndedWhileTheDaemonWasDown(t *testing.T) {
	withMod(t)
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, "sh", "-c", "echo done")
	wqShimDone(t, old, id)
	m := NewForTest(Options{})
	m.SetJobRunnerForTest(sched.TestRunner())
	inst := modInst(t, m, "owner")
	old.queue.store.Jobs[0].Owner, old.queue.store.Jobs[0].Notify = uint64(m.idOf(inst)), true
	require.NoError(t, old.saveQueue())

	m.loadQueue(dir, wqHost)
	wqPump(t, m)

	assert.Equal(t, sched.Done, wqRecord(t, m, id).State)
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1, "the owner is told")
	assert.Contains(t, msgs[0].Text, "finished: exit 0")
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, id).Notice, "still on its way")
}

// wqOwnerSession serves a started Claude session on launch L1, in a workspace
// with storage (the pause and kill requests need one), and returns its ID. A
// job submitted with Launch "L1" is that session's.
func wqOwnerSession(t *testing.T, m *Model) InstanceID {
	t.Helper()
	inst := startedInst(t, "owner", "claude")
	ws := storedWorkspace(t, "owners")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)
	adoptLaunch(t, m, inst, "L1", time.Now())
	m.Drain()
	return m.idOf(inst)
}

// Pausing or killing a job's owner cancels its queued and running jobs first,
// with no notice, and leaves the jobs of other owners and of none alone (spec
// §5.7).
func TestJobQueue_PauseOrKillCancelsTheOwnersJobsAndOnlyTheirs(t *testing.T) {
	cases := []struct {
		name   string
		act    func(*Model, InstanceID)
		reason string
	}{
		{"pause", func(m *Model, id InstanceID) { m.Pause(id, 0) }, "because the session was paused"},
		{"kill", func(m *Model, id InstanceID) { m.Kill(id, 0) }, "because the session was killed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqLiveModel(t)
			wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
			owner := wqOwnerSession(t, m)
			submit := func(launch string, argv ...string) sched.JobID {
				sub := wqJob(t, argv...)
				sub.Launch, sub.Notify = launch, true
				id, _, err := m.submitJob(sub)
				require.NoError(t, err)
				return id
			}
			running := submit("L1", "sleep", "30")
			queued := submit("L1", "sleep", "30")
			ownerless := submit("", "sleep", "30")
			stranger := wqEnded(9, 77)
			stranger.State, stranger.Exit, stranger.Started, stranger.Ended = sched.Queued, nil, time.Time{}, time.Time{}
			stranger.Argv, stranger.Cwd, stranger.Claim = []string{"sleep", "30"}, t.TempDir(), sched.Claim{CPUs: 4, MemoryGB: 6}
			wqSeed(t, m, stranger)
			wqPump(t, m) // the running job's start: a real shim
			require.Equal(t, owner, InstanceID(wqRecord(t, m, running).Owner))

			c.act(m, owner)
			m.Drain() // the session's own job, which this test does not run, and the pass that went with it
			m.gate(gateQueue).inFlight = false

			got := wqRecord(t, m, queued)
			require.Equal(t, sched.Cancelled, got.State, "queued: cancelled at once")
			assert.Equal(t, c.reason, got.CancelReason)
			assert.False(t, got.Notify, "and no notice is sent for it")
			got = wqRecord(t, m, running)
			assert.Equal(t, sched.Running, got.State, "running: gateQueue ends it, and the request does not wait for that")
			assert.Equal(t, c.reason, got.CancelReason)
			assert.False(t, got.Notify)
			assert.Empty(t, wqRecord(t, m, ownerless).CancelReason, "a job with no owner is not touched")
			assert.Empty(t, wqRecord(t, m, 9).CancelReason, "nor is another owner's")

			rec := wqSettle(t, m, running)
			assert.Equal(t, sched.Cancelled, rec.State)
			require.NotNil(t, rec.Exit)
			assert.Equal(t, "SIGTERM", rec.Exit.Signal)
			assert.Equal(t, sched.NoticeNone, rec.Notice, "no notice, not even an undelivered one")
		})
	}
}

// The cancel comes after the request is admitted: a request the model refuses
// (the session is busy) changes nothing.
func TestJobQueue_ARefusedPauseOrKillCancelsNothing(t *testing.T) {
	m := wqModel(t)
	owner := wqOwnerSession(t, m)
	m.Pause(owner, 0) // admitted: it moves the session to Loading
	m.Drain()
	sub := wqJob(t, "sleep", "30")
	sub.Launch = "L1"
	later, _, err := m.submitJob(sub)
	require.NoError(t, err)
	m.Drain()

	m.Pause(owner, 0) // refused: the session is busy
	m.Kill(owner, 0)  // refused too

	rec := wqRecord(t, m, later)
	assert.Empty(t, rec.CancelReason)
	assert.NotEqual(t, sched.Cancelled, rec.State)
}

// A reset counts as a requested kill of every session in its workspace: it
// cancels their queued jobs first, with no notice, and leaves the jobs of no
// owner and of a session in another workspace alone (spec §5.7).
func TestJobQueue_ResetCancelsTheWorkspacesOwnersJobsAndOnlyTheirs(t *testing.T) {
	f := newResetFixture(t)
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	f.m.SetJobRunnerForTest(sched.TestRunner())
	other := storedWorkspace(t, "elsewhere")
	stranger := pausedInst(t, "stranger")
	other.add(stranger)
	f.m.SetWorkspacesForTest(f.ws, other)
	f.m.loadQueue(dir, wqHost)
	owner := f.m.idOf(f.ws.insts[0])
	require.NotZero(t, owner)

	// Claims over the caps, so every job stays queued until a test cancels it.
	seed := func(id sched.JobID, owner InstanceID) {
		rec := wqQueuedRecord(t, id)
		rec.Owner, rec.Notify = uint64(owner), owner != 0
		rec.Claim = sched.Claim{CPUs: 64, MemoryGB: 6}
		wqSeed(t, f.m, rec)
	}
	seed(1, owner)
	seed(2, 0)
	seed(3, f.m.idOf(stranger))

	require.NoError(t, f.m.Reset(f.m.wsIDOf(f.ws), 7))

	got := wqRecord(t, f.m, 1)
	assert.Equal(t, sched.Cancelled, got.State)
	assert.Equal(t, "because the session was reset", got.CancelReason)
	assert.False(t, got.Notify, "and no notice is sent for it")
	assert.Equal(t, sched.NoticeNone, got.Notice)
	for _, id := range []sched.JobID{2, 3} {
		rec := wqRecord(t, f.m, id)
		assert.Equal(t, sched.Queued, rec.State, "job %d: not the reset workspace's", id)
		assert.Empty(t, rec.CancelReason)
	}
}

// The reset's jobs of other owners and of none keep running: only the
// sessions' own are cancelled (spec §5.7), the running one by SIGTERM.
func TestJobQueue_ResetLeavesTheOtherJobsRunning(t *testing.T) {
	f := newResetFixture(t)
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	f.m.SetJobRunnerForTest(sched.TestRunner())
	other := storedWorkspace(t, "elsewhere")
	stranger := pausedInst(t, "stranger")
	other.add(stranger)
	f.m.SetWorkspacesForTest(f.ws, other)
	f.m.loadQueue(dir, wqHost)
	t.Cleanup(func() { wqStop(t, f.m) })
	owner := f.m.idOf(f.ws.insts[0])
	require.NotZero(t, owner)

	seed := func(id sched.JobID, owner InstanceID) {
		rec := wqQueuedRecord(t, id)
		rec.Owner, rec.Notify = uint64(owner), owner != 0
		rec.Argv, rec.Claim = []string{"sleep", "30"}, sched.Claim{CPUs: 4, MemoryGB: 6}
		wqSeed(t, f.m, rec)
		require.NoError(t, sched.WriteSpec(f.m.queue.jobDir(id), sched.Spec{ID: id, Argv: rec.Argv, Cwd: rec.Cwd, Env: []string{"PATH=" + os.Getenv("PATH")}}))
	}
	seed(1, owner)
	seed(2, 0)
	seed(3, f.m.idOf(stranger))
	f.m.admitJobs() // the host's caps have room for all three
	wqPump(t, f.m)  // real shims
	for _, id := range []sched.JobID{1, 2, 3} {
		require.Equal(t, sched.Running, wqRecord(t, f.m, id).State, "the setup: job %d runs", id)
	}

	require.NoError(t, f.m.Reset(f.m.wsIDOf(f.ws), 7))
	f.m.Drain() // the reset job itself is not run; nor is the gate pass that went with it
	f.m.gate(gateQueue).inFlight = false
	wqPass(t, f.m) // carries out the running job's cancel

	for _, id := range []sched.JobID{2, 3} {
		rec := wqRecord(t, f.m, id)
		assert.Equal(t, sched.Running, rec.State, "job %d: not the reset workspace's, so it keeps running", id)
		assert.Empty(t, rec.CancelReason)
	}
	got := wqSettle(t, f.m, 1)
	assert.Equal(t, sched.Cancelled, got.State)
	assert.Equal(t, "because the session was reset", got.CancelReason)
	require.NotNil(t, got.Exit)
	assert.Equal(t, "SIGTERM", got.Exit.Signal)
	assert.Equal(t, sched.NoticeNone, got.Notice)
}

// A reset cancels all its sessions' queued jobs before any admission pass.
// Session A's job is a starved head that cannot start yet, so it holds session
// B's fitting job behind it; ending A's alone would let the pass that follows
// start B's job, only for the reset to kill it.
func TestJobQueue_ResetCancelsAllItsQueuedJobsBeforeAdmitting(t *testing.T) {
	f := newResetFixture(t)
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	f.m.SetJobRunnerForTest(sched.TestRunner())
	second := pausedInst(t, "second")
	f.ws.add(second)
	f.m.SetWorkspacesForTest(f.ws)
	f.m.loadQueue(dir, wqHost)
	t.Cleanup(func() { wqStop(t, f.m) })
	a, b := f.m.idOf(f.ws.insts[0]), f.m.idOf(second)
	require.NotZero(t, a)
	require.NotZero(t, b)

	wqLaunch(t, f.m, "sleep", "30") // takes 4 of the 14 cores and 6 of the 24 GB
	seed := func(id sched.JobID, owner InstanceID, claim sched.Claim, submitted time.Time) {
		rec := wqQueuedRecord(t, id)
		rec.Owner, rec.Claim, rec.Submitted = uint64(owner), claim, submitted
		wqSeed(t, f.m, rec)
	}
	seed(2, a, sched.Claim{CPUs: 11, MemoryGB: 1}, time.Now().Add(-2*time.Hour)) // starved, and 4 + 11 > 14
	seed(3, b, sched.Claim{CPUs: 1, MemoryGB: 1}, time.Now())                    // fits, but waits for job 2
	f.m.admitJobs()
	require.Equal(t, sched.Queued, wqRecord(t, f.m, 3).State, "the setup: job 2 holds job 3 back")

	require.NoError(t, f.m.Reset(f.m.wsIDOf(f.ws), 7))
	f.m.Drain() // the reset job itself is not run, nor the gate pass that went with it
	f.m.gate(gateQueue).inFlight = false

	for _, id := range []sched.JobID{2, 3} {
		rec := wqRecord(t, f.m, id)
		assert.Equal(t, sched.Cancelled, rec.State, "job %d", id)
		assert.Equal(t, "because the session was reset", rec.CancelReason)
	}
}

// A reset the model refuses cancels nothing.
func TestJobQueue_ARefusedResetCancelsNothing(t *testing.T) {
	f := newResetFixture(t)
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	f.m.SetJobRunnerForTest(sched.TestRunner())
	f.m.loadQueue(dir, wqHost)
	rec := wqQueuedRecord(t, 1)
	rec.Owner, rec.Claim = uint64(f.m.idOf(f.ws.insts[0])), sched.Claim{CPUs: 64, MemoryGB: 6}
	wqSeed(t, f.m, rec)
	f.ws.resetting = true // a reset already running: refused

	assert.ErrorIs(t, f.m.Reset(f.m.wsIDOf(f.ws), 7), ErrRefused)

	assert.Equal(t, sched.Queued, wqRecord(t, f.m, 1).State)
	assert.Empty(t, wqRecord(t, f.m, 1).CancelReason)
}

// wqQueuedRecord is a queued job that fits any cap: one core, 1 GB, a real
// folder, a command that exits at once.
func wqQueuedRecord(t *testing.T, id sched.JobID) sched.Record {
	t.Helper()
	return sched.Record{
		ID: id, Order: int64(id), Description: "queued", Argv: []string{"true"}, Cwd: t.TempDir(),
		Claim: sched.Claim{CPUs: 1, MemoryGB: 1}, Tail: 2, State: sched.Queued, Submitted: time.Now(),
	}
}

// wqNoSiblings asserts that dir holds nothing but config.json and the jobs
// directory: nothing was renamed aside (config.LoadConfigFrom's quarantine).
func wqNoSiblings(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Contains(t, []string{config.ConfigFileName, jobsDirName}, e.Name(), "nothing is moved aside")
	}
}

// A global config.json that fails to parse pauses admission and is left
// exactly where it is, whatever the pass reads it for: LoadConfigFrom would
// have renamed it aside and run the queue on the host's defaults.
func TestJobQueue_AGarbledGlobalConfigPausesAdmissionAndIsLeftAsItIs(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, config.ConfigFileName)
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"default_program": "my-claude", "scheduler": {"cpus": 4}}`), 0o644))
	m := wqModelAt(t, dir)
	t.Cleanup(func() { wqStop(t, m) })
	wqSeed(t, m, wqQueuedRecord(t, 1))
	require.Empty(t, m.queue.paused)

	garbled := []byte(`{"default_program": "my-claude", "scheduler": {"cpus": 4,}`)
	require.NoError(t, os.WriteFile(cfgPath, garbled, 0o644))
	m.admitJobs()

	assert.True(t, strings.HasPrefix(m.queue.paused, "config.json could not be read: "), m.queue.paused)
	assert.NotContains(t, m.queue.paused, "\n")
	assert.Equal(t, sched.Policy{}, m.queue.policy, "no cap is guessed")
	assert.Equal(t, "admission paused: "+m.queue.paused, m.queue.waiting[1])
	assert.Equal(t, sched.Queued, wqRecord(t, m, 1).State, "the queued job does not start")
	assert.Empty(t, m.Drain().Jobs)
	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(garbled), string(got), "the file is byte-identical")
	wqNoSiblings(t, dir)

	// The tick runs a pass every few seconds while jobs wait: still nothing moves.
	m.queue.lastPass = time.Now().Add(-queueInterval)
	m.tickQueue()
	got, err = os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(garbled), string(got))
	wqNoSiblings(t, dir)
	_, _, err = m.submitJob(wqJob(t, "true"))
	assert.ErrorContains(t, err, "admission paused: config.json could not be read: ")

	// The other side: the file mended, the next pass admits what waited.
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"default_program": "my-claude", "scheduler": {"cpus": 4}}`), 0o644))
	m.admitJobs()
	assert.Empty(t, m.queue.paused)
	assert.Equal(t, 4, m.queue.policy.Caps.CPUs)
	assert.Equal(t, sched.Running, wqRecord(t, m, 1).State)
}

// A daemon booted over a garbled global config.json pauses at load and leaves
// the file alone.
func TestJobQueue_ABootOverAGarbledGlobalConfigPausesAndLeavesIt(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, config.ConfigFileName)
	garbled := []byte(`{not json`)
	require.NoError(t, os.WriteFile(cfgPath, garbled, 0o644))

	m := wqModelAt(t, dir)

	assert.True(t, strings.HasPrefix(m.queue.paused, "config.json could not be read: "), m.queue.paused)
	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(garbled), string(got))
	wqNoSiblings(t, dir)
}

// A file without a scheduler block, or no file at all, is the host's defaults,
// not a fault.
func TestJobQueue_AConfigWithoutASchedulerBlockOrNoConfigRunsOnTheHostsDefaults(t *testing.T) {
	for name, body := range map[string]string{"no file": "", "no block": `{"default_program": "x"}`} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if body != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(body), 0o644))
			}
			m := wqModelAt(t, dir)
			policy, paused := m.refreshPolicy()
			assert.Empty(t, paused)
			assert.Equal(t, 14, policy.Caps.CPUs)
		})
	}
}

// The caps form on a file that can't be read refuses and writes nothing, where
// LoadConfigFrom would have written the defaults over every other setting.
func TestJobQueue_SetSchedulerCapsOnAGarbledConfigRefusesAndLeavesIt(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, config.ConfigFileName)
	m := wqModelAt(t, dir)
	garbled := []byte(`{"default_program": "my-claude", "scheduler": [`)
	require.NoError(t, os.WriteFile(cfgPath, garbled, 0o644))

	err := m.setSchedulerCaps(config.SchedulerSettings{CPUs: 8})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "config.json could not be read, so the caps were not saved")
	got, rerr := os.ReadFile(cfgPath)
	require.NoError(t, rerr)
	assert.Equal(t, string(garbled), string(got), "byte-identical")
	wqNoSiblings(t, dir)

	// Valid JSON with a mistyped field elsewhere fails config.json's full
	// decode: LoadConfigFrom would quarantine it and the save would write the
	// defaults over every other setting.
	mistyped := []byte(`{"default_program": 5, "branch_prefix": "x/"}`)
	require.NoError(t, os.WriteFile(cfgPath, mistyped, 0o644))
	err = m.setSchedulerCaps(config.SchedulerSettings{CPUs: 8})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config.json could not be read, so the caps were not saved")
	got, rerr = os.ReadFile(cfgPath)
	require.NoError(t, rerr)
	assert.Equal(t, string(mistyped), string(got), "byte-identical")
	wqNoSiblings(t, dir)

	// The other side: a readable file takes the caps and keeps its settings.
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"default_program": "my-claude"}`), 0o644))
	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 8}))
	saved := config.LoadConfigFrom(dir)
	assert.Equal(t, 8, saved.GetScheduler().CPUs)
	assert.Equal(t, "my-claude", saved.DefaultProgram)
}

// wqUnreadableLock makes job id's lock file fail the lock test (an open that
// is denied) and returns the undo. The test is skipped as root, which reads
// everything.
func wqUnreadableLock(t *testing.T, m *Model, id sched.JobID) (restore func()) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root opens an unreadable file")
	}
	path := filepath.Join(m.queue.jobDir(id), sched.LockFile)
	if _, err := os.Stat(path); err != nil {
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}
	require.NoError(t, os.Chmod(path, 0))
	_, err := sched.Probe(m.queue.jobDir(id))
	require.Error(t, err, "the lock test must fail")
	return func() { require.NoError(t, os.Chmod(path, 0o600)) }
}

// wqLockFailuresLogged counts the queue.lock_test_failed records whose line
// holds marker. The log file is shared by the package's tests, so a marker (a
// temp dir in a path, or a unique error text) picks out one test's records.
func wqLockFailuresLogged(t *testing.T, marker string) int {
	t.Helper()
	data, err := os.ReadFile(log.LogFilePath())
	require.NoError(t, err)
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "queue.lock_test_failed") && strings.Contains(line, marker) {
			n++
		}
	}
	return n
}

// A queued record whose lock test keeps failing is logged when the failure is
// new, not at every pass (3 s): the same text is not logged again, a different
// one is.
func TestJobQueue_AQueuedRecordWhoseLockTestKeepsFailingIsLoggedOnce(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	wqSeed(t, old, wqQueuedRecord(t, 1))
	require.NoError(t, old.saveQueue())
	restore := wqUnreadableLock(t, old, 1)
	logged := func() int { return wqLockFailuresLogged(t, dir) }

	m := wqRestart(t, dir)
	require.Contains(t, m.queue.unprobed, sched.JobID(1))
	assert.Equal(t, 1, logged(), "the first failure is logged")

	m.reprobeHeld()
	m.reprobeHeld()

	require.Contains(t, m.queue.unprobed, sched.JobID(1), "the record is still held")
	assert.Equal(t, 1, logged(), "the same failure at the next passes is not")

	// The other side: the lock now fails in another way (a symlink loop where
	// the file was), and that is news.
	restore()
	lock := filepath.Join(m.queue.jobDir(1), sched.LockFile)
	require.NoError(t, os.Remove(lock))
	require.NoError(t, os.Symlink(lock, lock))
	m.reprobeHeld()

	require.Contains(t, m.queue.unprobed, sched.JobID(1), "still held")
	assert.Equal(t, 2, logged(), "a failure with other text is logged")
	m.reprobeHeld()
	assert.Equal(t, 2, logged(), "and once")
}

// A running record whose lock test keeps failing is logged when the failure is
// new for that job, not at every pass; an answer in between makes the same
// failure news again.
func TestJobQueue_ARunningRecordWhoseLockTestKeepsFailingIsLoggedOnce(t *testing.T) {
	m := wqModel(t)
	running := wqEnded(1, 0)
	running.State, running.Exit, running.Ended = sched.Running, nil, time.Time{}
	wqSeed(t, m, running)
	other := running
	other.ID = 2
	wqSeed(t, m, other)
	marker := t.TempDir() // unique per run: the log file outlives runs
	fail := func(id sched.JobID, why string) {
		m.deliverQueuePass(queuePassResult{at: time.Now(), obs: []jobObs{{id: id, err: errors.New(marker + " " + why)}}})
	}
	answer := func(id sched.JobID) {
		m.deliverQueuePass(queuePassResult{at: time.Now(), obs: []jobObs{{id: id, pid: 1 << 22}}})
	}
	logged := func() int { return wqLockFailuresLogged(t, marker) }

	fail(1, "denied")
	assert.Equal(t, 1, logged(), "the first failure is logged")
	fail(1, "denied")
	fail(1, "denied")
	assert.Equal(t, 1, logged(), "the same failure at the next passes is not")

	fail(2, "denied")
	assert.Equal(t, 2, logged(), "another job's identical failure is its own")

	fail(1, "loop")
	assert.Equal(t, 3, logged(), "a failure with other text is logged")
	fail(1, "loop")
	assert.Equal(t, 3, logged())

	answer(1)
	fail(1, "loop")
	assert.Equal(t, 4, logged(), "after an answer the same failure is news again")
}

// A failed start's shim whose lock test keeps failing is logged when the text
// is new, and again after the test answered.
func TestJobQueue_ASilentShimsLockTestFailureIsLoggedWhenItsTextIsNew(t *testing.T) {
	m := wqModel(t)
	const id = sched.JobID(5)
	m.queue.silent[id] = silentShim{pid: 1 << 22, since: time.Now()}
	marker := t.TempDir() // unique per run: the log file outlives runs
	obs := func(err error) {
		m.noteSilent(jobObs{id: id, silent: true, err: err})
	}
	logged := func() int { return wqLockFailuresLogged(t, marker) }

	obs(errors.New(marker + " denied"))
	obs(errors.New(marker + " denied"))
	assert.Equal(t, 1, logged(), "once for a run of the same failure")
	obs(errors.New(marker + " loop"))
	assert.Equal(t, 2, logged(), "a failure with other text is logged")

	obs(nil) // the test answered, the shim is still owed its kill
	obs(errors.New(marker + " loop"))
	assert.Equal(t, 3, logged(), "after an answer the same failure is news again")
	assert.Contains(t, m.queue.silent, id)

	obs(nil)
	m.noteSilent(jobObs{id: 6, silent: true, err: errors.New(marker + " unknown")})
	assert.Equal(t, 3, logged(), "an entry that is gone logs nothing")
}

// A queued record whose boot probe failed is not admitted: a shim may be
// behind it, and a second would run the command again after it (spec §5.6).
// It waits with the reason; once the probe answers, it is treated as boot
// adoption would have treated it.
func TestJobQueue_AQueuedRecordWhoseLockCouldNotBeTestedIsNotStartedUntilItCan(t *testing.T) {
	t.Run("a held lock is adopted as running", func(t *testing.T) {
		dir := t.TempDir()
		gate := filepath.Join(t.TempDir(), "go")
		old := wqModelAt(t, dir)
		id := wqLaunch(t, old, wqGated(gate, 0)...)
		old.queue.store.Jobs[0].State, old.queue.store.Jobs[0].PID, old.queue.store.Jobs[0].Started = sched.Queued, 0, time.Time{}
		require.NoError(t, old.saveQueue())
		restore := wqUnreadableLock(t, old, id)

		m := wqRestart(t, dir)

		assert.Equal(t, sched.Queued, wqRecord(t, m, id).State, "not adopted, not started")
		assert.Contains(t, m.queue.waiting[id], "its lock could not be tested: ")
		assert.Empty(t, m.queue.starting)
		assert.Empty(t, m.Drain().Jobs, "no start job: a second shim would run the command again")
		m.queue.lastPass = time.Now().Add(-queueInterval)
		m.tickQueue()
		assert.Empty(t, m.Drain().Jobs, "and the tick starts none either")

		restore()
		m.queue.lastPass = time.Now().Add(-queueInterval)
		m.tickQueue()

		got := wqRecord(t, m, id)
		assert.Equal(t, sched.Running, got.State, "adopted once the probe answers")
		assert.Greater(t, got.PID, 1)
		assert.NotContains(t, m.queue.waiting, id)
		assert.Empty(t, m.Drain().Jobs, "and never started twice")
		require.NoError(t, os.WriteFile(gate, nil, 0o600))
		assert.Equal(t, sched.Done, wqSettle(t, m, id).State)
	})
	t.Run("a free lock with an exit.json is settled from it", func(t *testing.T) {
		dir := t.TempDir()
		old := wqModelAt(t, dir)
		id := wqLaunch(t, old, "sh", "-c", "exit 3")
		wqShimDone(t, old, id)
		old.queue.store.Jobs[0].State, old.queue.store.Jobs[0].PID, old.queue.store.Jobs[0].Started = sched.Queued, 0, time.Time{}
		require.NoError(t, old.saveQueue())
		restore := wqUnreadableLock(t, old, id)

		m := wqRestart(t, dir)

		assert.Equal(t, sched.Queued, wqRecord(t, m, id).State)
		assert.Contains(t, m.queue.waiting[id], "its lock could not be tested: ")
		assert.Empty(t, m.Drain().Jobs)

		restore()
		m.admitJobs()

		got := wqRecord(t, m, id)
		assert.Equal(t, sched.Failed, got.State)
		require.NotNil(t, got.Exit)
		assert.Equal(t, 3, got.Exit.Code)
		assert.Empty(t, m.Drain().Jobs, "it is never started")
	})
	t.Run("a free lock without a result is released to the queue and admitted", func(t *testing.T) {
		dir := t.TempDir()
		old := wqModelAt(t, dir)
		wqSeed(t, old, wqQueuedRecord(t, 1))
		require.NoError(t, old.saveQueue())
		restore := wqUnreadableLock(t, old, 1)

		m := wqRestart(t, dir)

		assert.Equal(t, sched.Queued, wqRecord(t, m, 1).State, "not started on an unanswered probe")
		assert.Contains(t, m.queue.waiting[1], "its lock could not be tested: ")
		assert.Empty(t, m.Drain().Jobs)

		restore()
		m.admitJobs()

		assert.Equal(t, sched.Running, wqRecord(t, m, 1).State, "an ordinary queued job again")
		assert.True(t, m.queue.starting[1])
		assert.Empty(t, m.queue.unprobed)
		jobs := m.Drain().Jobs
		require.Len(t, jobs, 1, "with one start job")
		m.Deliver(jobs[0]())
	})
	t.Run("a record that is cancelled meanwhile leaves the set", func(t *testing.T) {
		dir := t.TempDir()
		old := wqModelAt(t, dir)
		wqSeed(t, old, wqQueuedRecord(t, 1))
		require.NoError(t, old.saveQueue())
		restore := wqUnreadableLock(t, old, 1)
		defer restore()

		m := wqRestart(t, dir)
		require.Contains(t, m.queue.unprobed, sched.JobID(1))
		require.NoError(t, m.cancelJob(1, "by the test"))
		m.admitJobs()

		assert.Empty(t, m.queue.unprobed)
		assert.Equal(t, sched.Cancelled, wqRecord(t, m, 1).State)
	})
}

// The held record does not stand in the way of the others: they are admitted
// as usual, and it counts against nothing.
func TestJobQueue_ARecordWhoseLockCouldNotBeTestedDoesNotBlockTheOthers(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	wqSeed(t, old, wqQueuedRecord(t, 1))
	wqSeed(t, old, wqQueuedRecord(t, 2))
	require.NoError(t, old.saveQueue())
	restore := wqUnreadableLock(t, old, 1)
	defer restore()

	m := wqRestart(t, dir)

	assert.Equal(t, sched.Queued, wqRecord(t, m, 1).State)
	assert.Equal(t, sched.Running, wqRecord(t, m, 2).State)
}

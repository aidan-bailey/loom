package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
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

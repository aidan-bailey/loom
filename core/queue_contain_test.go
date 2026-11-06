package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConfiner records calls and fails those named in fail, by method.
type fakeConfiner struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
}

func (f *fakeConfiner) note(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.fail[strings.Fields(call)[0]]
}

func (f *fakeConfiner) JoinScope(_ context.Context, s sched.ScopeSpec, _ int) error {
	return f.note("JoinScope " + s.Unit)
}

func (f *fakeConfiner) EnsureSlice(_ context.Context, name string, p sched.SliceProps) error {
	return f.note(fmt.Sprintf("EnsureSlice %s %d %d", name, p.CPUWeight, p.MemoryMaxBytes))
}

func (f *fakeConfiner) SetMemoryHigh(_ context.Context, unit string, b uint64) error {
	return f.note(fmt.Sprintf("SetMemoryHigh %s %d", unit, b))
}

func (f *fakeConfiner) Freeze(_ context.Context, unit string) error { return f.note("Freeze " + unit) }
func (f *fakeConfiner) Thaw(_ context.Context, unit string) error   { return f.note("Thaw " + unit) }
func (f *fakeConfiner) Close()                                      {}

// Calls is every call so far, in order.
func (f *fakeConfiner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// wqCgroupFiles is a cgroup's files: 1 GiB in use, 256 MiB of it inactive
// file cache, 2.5 CPU-seconds, quiet pressure, not frozen.
func wqCgroupFiles() map[string]string {
	return map[string]string{
		"memory.current":  "1073741824\n",
		"memory.stat":     "anon 1\nfile 2\ninactive_file 268435456\n",
		"memory.high":     "max\n",
		"cpu.stat":        "usage_usec 2500000\n",
		"memory.pressure": "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
		"cpu.pressure":    "some avg10=7.00 avg60=0.00 avg300=0.00 total=0\n",
		"memory.events":   "oom_kill 0\n",
		"cgroup.events":   "populated 1\nfrozen 0\n",
		"cgroup.kill":     "",
	}
}

// wqWriteCgroup writes files into root/path, over what is there.
func wqWriteCgroup(t *testing.T, root, path string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, path)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
}

// wqStarted waits for job id's start job to land and returns its record.
func wqStarted(t *testing.T, m *Model, id sched.JobID) sched.Record {
	t.Helper()
	wqWait(t, fmt.Sprintf("job %d to start", id), func() bool {
		wqPump(t, m)
		rec := wqRecord(t, m, id)
		return rec.State != sched.Queued && !m.queue.starting[id]
	})
	return wqRecord(t, m, id)
}

func TestQueueContain_FallbackSaysWhy(t *testing.T) {
	if queueGOOS != "linux" {
		t.Skip("cgroups are Linux only")
	}
	m := wqLiveModel(t)
	require.NotEmpty(t, m.queueView().Uncontained, "no confiner yet")
	id := wqSubmit(t, m, "true")
	rec := wqStarted(t, m, id)
	assert.NotEmpty(t, rec.Uncontained)
	assert.Empty(t, rec.Unit)
	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.Nil(t, spec.Scope, "an uncontained start asks the shim for no scope")

	wqConfig(t, m, `{"contain": false}`)
	m.admitJobs()
	assert.Empty(t, m.queueView().Uncontained, "containment switched off is not expected")
	assert.Equal(t, "scheduler.contain is off", m.queue.containWhy(m.queue.policy))
}

func TestQueueContain_AStartEnsuresTheSliceAndWritesTheScope(t *testing.T) {
	if queueGOOS != "linux" {
		t.Skip("cgroups are Linux only")
	}
	m := wqLiveModel(t)
	fake := &fakeConfiner{}
	m.SetConfinerForTest(fake)
	wqPump(t, m)
	require.Empty(t, m.queueView().Uncontained)
	id := wqSubmit(t, m, "true")
	rec := wqStarted(t, m, id)

	q := m.queue
	spec, err := sched.ReadSpec(q.jobDir(id))
	require.NoError(t, err)
	require.NotNil(t, spec.Scope)
	assert.Equal(t, sched.ScopeSpec{Unit: sched.ScopeName(q.dir, id, 0), Slice: sched.SliceName(q.dir), CPUWeight: sched.CPUWeight(rec.Claim.CPUs)}, *spec.Scope)
	assert.Contains(t, fake.Calls(), fmt.Sprintf("EnsureSlice %s %d %d", sched.SliceName(q.dir), q.policy.JobsCPUWeight, uint64(q.policy.Caps.MemoryGB)<<30))
	assert.Empty(t, rec.Unit, "the test shim can't reach the bus to join")
	assert.NotEmpty(t, rec.Uncontained, "and says so")
	assert.NotNil(t, q.confiner, "a shim's failure keeps the daemon's confiner")
}

func TestQueueContain_ASliceThatFailsStartsTheJobUncontained(t *testing.T) {
	if queueGOOS != "linux" {
		t.Skip("cgroups are Linux only")
	}
	m := wqLiveModel(t)
	fake := &fakeConfiner{fail: map[string]error{"EnsureSlice": errors.New("no slice")}}
	m.SetConfinerForTest(fake)
	id := wqSubmit(t, m, "true")
	rec := wqStarted(t, m, id)
	assert.Equal(t, "its slice could not be set: no slice", rec.Uncontained)
	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.Nil(t, spec.Scope)
	assert.Nil(t, m.queue.confiner, "a failed call drops the confiner until a probe")
	assert.Equal(t, "no slice", m.queue.containWhy(m.queue.policy))
}

func TestQueueContain_UsageComesFromTheCgroup(t *testing.T) {
	m := wqLiveModel(t)
	root := t.TempDir()
	m.SetCgroupRootForTest(root)
	prev := readMemAvailable
	readMemAvailable = func() (int64, error) { return 8 << 20, nil }
	t.Cleanup(func() { readMemAvailable = prev })
	id := wqLaunch(t, m, "sleep", "30")
	const cg = "/t.slice/loom-ab-job-1-1.scope"
	wqWriteCgroup(t, root, cg, wqCgroupFiles())
	wqWriteCgroup(t, root, "/t.slice", wqCgroupFiles())
	m.queue.store.Jobs[m.queue.index(id)].Cgroup = cg

	wqPass(t, m)

	assert.Equal(t, int64((1073741824-268435456)/1024), m.queue.usage[id].RSSKB, "the working set, not the session's RSS")
	assert.Equal(t, int64(8<<20), m.queue.availKB)
	require.NotNil(t, m.queue.slice)
	assert.Equal(t, 7.0, m.queue.slice.CPUPressure)
}

func TestQueueContain_TheSIGKILLAlsoKillsTheCgroup(t *testing.T) {
	m := wqLiveModel(t)
	m.queue.escalation = 300 * time.Millisecond
	root := t.TempDir()
	m.SetCgroupRootForTest(root)
	ready := filepath.Join(t.TempDir(), "ready")
	id := wqLaunch(t, m, "sh", "-c", `trap "" TERM; echo ok > "$1"; while :; do sleep 0.1; done`, "sh", ready)
	wqWait(t, "the command to ignore SIGTERM", func() bool { _, err := os.Stat(ready); return err == nil })
	const cg = "/t.slice/loom-ab-job-1-1.scope"
	wqWriteCgroup(t, root, cg, wqCgroupFiles())
	rec := &m.queue.store.Jobs[m.queue.index(id)]
	rec.Cgroup, rec.CancelReason = cg, "with loom job cancel"

	wqSettle(t, m, id)

	data, err := os.ReadFile(filepath.Join(root, cg, "cgroup.kill"))
	require.NoError(t, err)
	assert.Equal(t, "1", string(data), "the escalation's SIGKILL reaches the whole scope")
}

func TestQueueContain_TheBootProbesBeforeItsFirstAdmission(t *testing.T) {
	if queueGOOS != "linux" {
		t.Skip("cgroups are Linux only")
	}
	fake := &fakeConfiner{}
	prev := newConfiner
	newConfiner = func(context.Context) (sched.Confiner, error) { return fake, nil }
	t.Cleanup(func() { newConfiner = prev })
	m := wqLiveModel(t)
	assert.Same(t, fake, m.queue.confiner, "found before the first pass")
	id := wqSubmit(t, m, "true")
	assert.Equal(t, sched.Running, wqRecord(t, m, id).State, "a submit still starts at once")
	wqStarted(t, m, id)
	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.NotNil(t, spec.Scope, "and contained")

	newConfiner = func(context.Context) (sched.Confiner, error) { return nil, errors.New("no bus here") }
	m2 := wqLiveModel(t)
	assert.Nil(t, m2.queue.confiner)
	assert.Equal(t, "no bus here", m2.queue.containWhy(m2.queue.policy), "the boot's failure says why")
	assert.False(t, m2.queue.probing, "an idle queue probes no more")
}

// An agent job claims nothing, so a scope would give it the least CPU
// weight, and a contained job is the ladder's to squeeze, freeze or requeue:
// it runs outside the jobs' slice, while a build beside it is contained.
func TestQueueContain_AnAgentJobRunsUncontained(t *testing.T) {
	if queueGOOS != "linux" {
		t.Skip("cgroups are Linux only")
	}
	m := wqLiveModel(t)
	fake := &fakeConfiner{}
	m.SetConfinerForTest(fake)
	wqPump(t, m)
	sub := wqJob(t, "true")
	sub.Kind = sched.KindAgent
	agent, _, err := m.submitJob(sub)
	require.NoError(t, err)
	rec := wqStarted(t, m, agent)
	assert.Equal(t, agentUncontained, rec.Uncontained)
	spec, err := sched.ReadSpec(m.queue.jobDir(agent))
	require.NoError(t, err)
	assert.Nil(t, spec.Scope, "the shim is asked for no scope")
	for _, c := range fake.Calls() {
		assert.False(t, strings.HasPrefix(c, "EnsureSlice"), "nor is the slice ensured for it: %s", c)
	}

	build := wqSubmit(t, m, "true")
	wqStarted(t, m, build)
	spec, err = sched.ReadSpec(m.queue.jobDir(build))
	require.NoError(t, err)
	assert.NotNil(t, spec.Scope, "a build job is still contained")
}

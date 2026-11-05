package sched

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeCgroup writes files into root/path, making the directories.
func writeCgroup(t *testing.T, root, path string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, path)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
}

func fullCgroup() map[string]string {
	return map[string]string{
		"memory.current":  "1073741824\n",
		"memory.stat":     "anon 1\nfile 2\ninactive_file 268435456\nactive_file 5\n",
		"memory.high":     "max\n",
		"cpu.stat":        "usage_usec 2500000\nuser_usec 1\nsystem_usec 1\n",
		"memory.pressure": "some avg10=12.50 avg60=1.00 avg300=0.00 total=1\nfull avg10=3.00 avg60=0.00 avg300=0.00 total=1\n",
		"cpu.pressure":    "some avg10=34.00 avg60=0.00 avg300=0.00 total=9\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
		"memory.events":   "low 0\nhigh 3\nmax 0\noom 1\noom_kill 1\n",
		"cgroup.events":   "populated 1\nfrozen 0\n",
	}
}

func TestReadCgroup(t *testing.T) {
	root := t.TempDir()
	path := "/user.slice/loom-ab-job-1-1.scope"
	writeCgroup(t, root, path, fullCgroup())
	s, err := ReadCgroup(root, path)
	require.NoError(t, err)
	assert.Equal(t, CgroupSample{
		WorkingKB: (1073741824 - 268435456) / 1024, CPU: 2500 * time.Millisecond,
		MemPressure: 12.5, CPUPressure: 34, OOMKills: 1, Populated: true,
	}, s)

	files := fullCgroup()
	files["memory.high"] = "536870912\n"
	files["cgroup.events"] = "populated 1\nfrozen 1\n"
	writeCgroup(t, root, path, files)
	s, err = ReadCgroup(root, path)
	require.NoError(t, err)
	assert.Equal(t, int64(512*1024), s.HighKB)
	assert.True(t, s.Frozen)
}

func TestReadCgroup_OptionalAndRequiredFiles(t *testing.T) {
	root := t.TempDir()
	files := fullCgroup()
	delete(files, "memory.pressure")
	delete(files, "cpu.pressure")
	delete(files, "memory.events")
	delete(files, "memory.high")
	writeCgroup(t, root, "/a.scope", files)
	s, err := ReadCgroup(root, "/a.scope")
	require.NoError(t, err, "pressure, events and memory.high are optional")
	assert.Zero(t, s.MemPressure)
	assert.Zero(t, s.HighKB)

	for _, need := range []string{"memory.current", "memory.stat", "cpu.stat", "cgroup.events"} {
		files := fullCgroup()
		delete(files, need)
		dir := "/" + need + ".scope"
		writeCgroup(t, root, dir, files)
		_, err := ReadCgroup(root, dir)
		assert.Error(t, err, need)
	}
}

func TestReadCgroup_RefusesAPathItCouldNotMean(t *testing.T) {
	for _, p := range []string{"", "/", "relative/x.scope", "/a/../b.scope", "/a//b.scope"} {
		_, err := ReadCgroup(t.TempDir(), p)
		assert.Error(t, err, p)
	}
}

func TestCgroupKill_RefusesWhatIsNotAJobScope(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"/user.slice", "/loom.slice/loom-ab.slice", "/x/other-job-1-1.scope", "/x/loom-ab-1.scope"} {
		writeCgroup(t, root, p, map[string]string{"cgroup.kill": ""})
		assert.Error(t, CgroupKill(root, p), p)
		data, _ := os.ReadFile(filepath.Join(root, p, "cgroup.kill"))
		assert.Empty(t, data, "nothing written for %s", p)
	}
	job := "/loom.slice/loom-ab.slice/loom-ab-job-4-2.scope"
	writeCgroup(t, root, job, map[string]string{"cgroup.kill": ""})
	require.NoError(t, CgroupKill(root, job))
	data, err := os.ReadFile(filepath.Join(root, job, "cgroup.kill"))
	require.NoError(t, err)
	assert.Equal(t, "1", string(data))
}

func TestParseSelfCgroup(t *testing.T) {
	p, err := parseSelfCgroup([]byte("0::/user.slice/loom-ab-job-1-1.scope\n"))
	require.NoError(t, err)
	assert.Equal(t, "/user.slice/loom-ab-job-1-1.scope", p)
	_, err = parseSelfCgroup([]byte("12:cpu:/x\n"))
	assert.Error(t, err, "cgroup v1 only")
}

func TestParseMemAvailable(t *testing.T) {
	kb, err := parseMemAvailable([]byte("MemTotal:       31696820 kB\nMemFree: 1 kB\nMemAvailable:   17896096 kB\n"))
	require.NoError(t, err)
	assert.Equal(t, int64(17896096), kb)
	_, err = parseMemAvailable([]byte("MemTotal: 1 kB\n"))
	assert.Error(t, err)
}

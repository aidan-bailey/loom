//go:build e2e && systemd_live && linux

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A job runs confined: in a scope of its own inside its loom's slice, as
// started.json records. Every test binary points D-Bus at a dead socket
// (testenv.IsolateLoomDirs), so this test, opt-in like sched's live test
// (go test -tags 'e2e systemd_live' ./e2e/...), gives the sandbox's daemon
// the real user bus back. The slice is the sandbox's own, named from its
// jobs dir, never a real loom's.
func TestE2E_Queue_AJobRunsInItsOwnScope(t *testing.T) {
	addr := os.Getenv(testenv.EnvRealDBus)
	if addr == "" {
		t.Skip("no user bus: DBUS_SESSION_BUS_ADDRESS was unset when the suite started")
	}
	t.Setenv(testenv.EnvDBus, addr) // the daemon inherits it through the join that starts it
	sb := newSandbox(t, "")
	dir := t.TempDir()
	id := submitJob(t, sb, dir, `cat /proc/self/cgroup > "$1/cgroup"`)
	out, code := waitJob(t, sb, id)
	require.Equal(t, 0, code, out)

	started, err := sched.ReadStarted(sched.JobDir(filepath.Join(sb.GlobalDir(), "jobs"), sched.JobID(id)))
	require.NoError(t, err)
	require.Empty(t, started.Uncontained, "the job asked for a scope and ran without one")
	require.NotEmpty(t, started.Unit, "the job ran uncontained (the daemon's serve.log says why: queue.uncontained)")
	assert.True(t, strings.HasSuffix(started.Unit, ".scope"), started.Unit)
	b, err := os.ReadFile(filepath.Join(dir, "cgroup"))
	require.NoError(t, err)
	assert.Contains(t, string(b), started.Unit, "the job's own process sits in the scope it recorded")
	assert.Contains(t, string(b), started.Cgroup, "and in the cgroup it recorded")
	assert.Regexp(t, `/loom_[0-9a-f]{8}\.slice/`, string(b), "inside its loom's slice")
}

//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobCLI runs `loom job <args>` in the sandbox. An empty LOOM_LAUNCH makes
// a job's command a plain shell's, whatever shell runs the suite.
func jobCLI(t *testing.T, sb *devsandbox.Sandbox, args ...string) (string, error) {
	t.Helper()
	return runLoom(t, sb, append([]string{"env", "LOOM_LAUNCH=", sb.LoomBin(), "job"}, args...)...)
}

// submitJob submits script as `sh -c script sh dir` with a fixed small
// claim (so no job waits to be sized) plus flags, and returns its ID.
func submitJob(t *testing.T, sb *devsandbox.Sandbox, dir, script string, flags ...string) int {
	t.Helper()
	args := append([]string{"submit", "--no-notify", "--cpus", "1", "--mem", "1"}, flags...)
	out, err := jobCLI(t, sb, append(args, "--", "sh", "-c", script, "sh", dir)...)
	require.NoError(t, err, out)
	m := regexp.MustCompile(`job (\d+) `).FindStringSubmatch(out)
	require.NotNil(t, m, "no job ID in %q", out)
	id, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	return id
}

// jobState is id's state in `loom job list --all`, "" when it isn't listed.
func jobState(t *testing.T, sb *devsandbox.Sandbox, id int) string {
	t.Helper()
	out, err := jobCLI(t, sb, "list", "--all")
	require.NoError(t, err, out)
	m := regexp.MustCompile(`(?m)^` + strconv.Itoa(id) + ` +(\S+)`).FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1]
}

// waitJob runs `loom job wait id` and returns its output and exit code.
func waitJob(t *testing.T, sb *devsandbox.Sandbox, id int) (string, int) {
	t.Helper()
	out, err := jobCLI(t, sb, "wait", strconv.Itoa(id))
	if err == nil {
		return out, 0
	}
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, out)
	return out, exit.ExitCode()
}

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A dependent job is held while its prerequisite runs and starts only once
// it ends done; a dependent of one that fails is cancelled without its
// command ever running, and `loom job wait` exits 125 for it.
func TestE2E_Queue_DependentsFollowTheirPrerequisite(t *testing.T) {
	sb := newSandbox(t, "")
	dir := t.TempDir()

	first := submitJob(t, sb, dir, `touch "$1/first.began"; sleep 3; touch "$1/first.ended"`)
	second := submitJob(t, sb, dir, `test -e "$1/first.ended" && touch "$1/second.ran"`, "--after", strconv.Itoa(first))
	require.Eventually(t, func() bool { return fileExists(filepath.Join(dir, "first.began")) },
		uiTimeout, 100*time.Millisecond, "the prerequisite never began")
	assert.Equal(t, "held", jobState(t, sb, second), "the dependent waits while its prerequisite runs")
	out, code := waitJob(t, sb, second)
	assert.Equal(t, 0, code, out)
	assert.True(t, fileExists(filepath.Join(dir, "second.ran")), "the dependent ran, after its prerequisite had ended")

	failing := submitJob(t, sb, dir, `sleep 1; exit 3`)
	orphan := submitJob(t, sb, dir, `touch "$1/orphan.ran"`, "--after", strconv.Itoa(failing))
	out, code = waitJob(t, sb, failing)
	assert.Equal(t, 3, code, out)
	out, code = waitJob(t, sb, orphan)
	assert.Equal(t, 125, code, "a cancelled job exits 125: %s", out)
	assert.Equal(t, "cancelled", jobState(t, sb, orphan))
	assert.False(t, fileExists(filepath.Join(dir, "orphan.ran")), "a dependent of a failed job never runs")
}

// span is when a job's command ran, from the marks it wrote.
type span struct{ start, end int64 }

// spanOf reads the start and end marks (nanoseconds) a job wrote under
// dir as <name>.start and <name>.end.
func spanOf(t *testing.T, dir, name string) span {
	t.Helper()
	read := func(suffix string) int64 {
		b, err := os.ReadFile(filepath.Join(dir, name+suffix))
		require.NoError(t, err)
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		require.NoError(t, err, "%q", b)
		return n
	}
	return span{read(".start"), read(".end")}
}

// Jobs naming the same lock take turns even when the host could run them
// together, as two jobs naming none show it can.
func TestE2E_Queue_JobsSharingALockTakeTurns(t *testing.T) {
	sb := newSandbox(t, "")
	dir := t.TempDir()
	// $2 names the job's marks.
	script := `date +%s%N > "$1/$2.start"; sleep 2; date +%s%N > "$1/$2.end"`
	run := func(name string, flags ...string) int {
		args := append([]string{"submit", "--no-notify", "--cpus", "1", "--mem", "1"}, flags...)
		out, err := jobCLI(t, sb, append(args, "--", "sh", "-c", script, "sh", dir, name)...)
		require.NoError(t, err, out)
		id, err := strconv.Atoi(regexp.MustCompile(`job (\d+) `).FindStringSubmatch(out)[1])
		require.NoError(t, err)
		return id
	}
	overlap := func(a, b span) bool { return a.start < b.end && b.start < a.end }

	free1, free2 := run("free1"), run("free2")
	for _, id := range []int{free1, free2} {
		out, code := waitJob(t, sb, id)
		require.Equal(t, 0, code, out)
	}
	require.True(t, overlap(spanOf(t, dir, "free1"), spanOf(t, dir, "free2")),
		"two unlocked jobs ran one after the other, so the lock test below would prove nothing")

	lock1, lock2 := run("lock1", "--lock", "port:e2e"), run("lock2", "--lock", "port:e2e")
	for _, id := range []int{lock1, lock2} {
		out, code := waitJob(t, sb, id)
		require.Equal(t, 0, code, out)
	}
	a, b := spanOf(t, dir, "lock1"), spanOf(t, dir, "lock2")
	assert.False(t, overlap(a, b), "jobs sharing a lock ran together: %+v %+v", a, b)
}

// Cancelling a running job ends its whole process group, a background
// child included, and the job ends cancelled.
func TestE2E_Queue_CancelEndsTheJobsWholeGroup(t *testing.T) {
	sb := newSandbox(t, "")
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	id := submitJob(t, sb, dir, `sleep 300 & echo $! > "$1/child.pid"; wait`)
	var child int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		child, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && child > 0
	}, uiTimeout, 100*time.Millisecond, "the job never started its child")
	require.True(t, alive(child))

	out, err := jobCLI(t, sb, "cancel", strconv.Itoa(id))
	require.NoError(t, err, out)
	out, code := waitJob(t, sb, id)
	assert.Equal(t, 125, code, out)
	assert.Equal(t, "cancelled", jobState(t, sb, id))
	assert.True(t, waitExit(child, 15*time.Second), "the job's background child outlived the cancel")
}

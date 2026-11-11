package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bounded waits stop at the deadline a re-exec carried across
// (waitDeadline), so a call that used most of its bound before the daemon
// was upgraded still ends within waitCap: the new process, given SECONDS
// again, stops at the old process's deadline, not SECONDS from its start.

// stopsAtTheInheritedDeadline runs wait, as the new process after a
// re-exec whose deadline is a second and a half away, with a bound of a
// minute, and asserts it ends with the ordinary timeout (75) at that
// deadline.
func stopsAtTheInheritedDeadline(t *testing.T, wait func() (string, error)) string {
	t.Helper()
	deadline := time.Now().Add(1500 * time.Millisecond)
	inheritDeadline(t, deadline)
	out, err := wait()
	ended := time.Now()
	assert.Equal(t, exitStillRunning, flowExit(err), "the ordinary timeout: call it again\n%s", out)
	assert.False(t, ended.Before(deadline), "it waited until the inherited deadline")
	assert.Less(t, ended.Sub(deadline), 10*time.Second, "it stopped at the inherited deadline, not a minute after its start")
	return out
}

func TestFlowWaitWait_StopsAtTheDeadlineAReexecCarried(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "slow", "--", "sh", "-c", "echo up; sleep 30")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	out := stopsAtTheInheritedDeadline(t, func() (string, error) {
		out, _, err := runFlow(t, "wait", "wait", "slow", "60")
		return out, err
	})
	assert.Regexp(t, `^running slow: job 1 running \d+s\n$`, out)
}

func TestFlowGatesWait_StopsAtTheDeadlineAReexecCarried(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo up; sleep 30"}
	_, _, err := runFlow(t, "gates", "start", "--story", "abc")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	stopsAtTheInheritedDeadline(t, func() (string, error) {
		out, _, err := runFlow(t, "gates", "wait", "60")
		return out, err
	})
}

func TestFlowAgentWait_StopsAtTheDeadlineAReexecCarried(t *testing.T) {
	agentFixture(t, "intermediate-dev")
	_, _, err := runAgent(t, "SLOW", "run", "intermediate-dev")
	require.NoError(t, err)
	out := stopsAtTheInheritedDeadline(t, func() (string, error) {
		out, _, err := runAgent(t, "", "wait", "60")
		return out, err
	})
	assert.Contains(t, out, "running intermediate-dev-1")
}

// TestFlowWaitWait_APastDeadlineEndsAtOnce: a deadline the re-exec carried
// that has passed by the time the new process waits ends the wait at once,
// with its ordinary timeout.
func TestFlowWaitWait_APastDeadlineEndsAtOnce(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "slow", "--", "sh", "-c", "echo up; sleep 30")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	inheritDeadline(t, time.Now().Add(-time.Second))
	start := time.Now()
	out, _, err := runFlow(t, "wait", "wait", "slow", "60")
	assert.Equal(t, exitStillRunning, flowExit(err), out)
	assert.Regexp(t, `^running slow: job 1 running \d+s\n$`, out)
	assert.Less(t, time.Since(start), 5*time.Second, "at once")
}

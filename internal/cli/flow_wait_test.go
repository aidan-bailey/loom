package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlowWait_StartQueuesTheStepAndWaitReportsItsExit(t *testing.T) {
	d, conf := flowJobsDaemon(t)

	out, _, err := runFlow(t, "wait", "start", "build", "--", "sh", "-c", "echo out; exit 3")
	require.NoError(t, err)
	logPath := filepath.Join(conf.state, "agent-wait", "build.log")
	assert.Equal(t, "started build: job 1; log "+logPath+"\n", out)
	j := d.job(1)
	assert.Equal(t, []string{"sh", "-c", "echo out; exit 3"}, j.Argv)
	assert.Equal(t, "build: sh -c 'echo out; exit 3'", j.Description)

	out, _, err = runFlow(t, "wait", "wait", "build", "60")
	assert.Equal(t, 3, flowExit(err), "the job's own exit code")
	assert.Equal(t, "done build exit 3\n", out)
	b, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "out\n", string(b))
}

func TestFlowWait_ABoundedWaitSaysWhereTheJobStandsAndTheLogGrows(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "slow", "--", "sh", "-c", "echo one; sleep 4; echo two")
	require.NoError(t, err)
	d.waitLog(t, 1, "one")
	logPath := filepath.Join(conf.state, "agent-wait", "slow.log")

	out, _, err := runFlow(t, "wait", "wait", "slow", "0")
	assert.Equal(t, exitStillRunning, flowExit(err))
	assert.Regexp(t, `^running slow: job 1 running \d+s\n$`, out)
	b, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "one\n", string(b))

	out, _, err = runFlow(t, "wait", "wait", "slow", "60")
	require.NoError(t, err)
	assert.Equal(t, "done slow exit 0\n", out)
	b, err = os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", string(b), "each call adds only what the job's output gained")
}

func TestFlowWait_ACancelledJobIsLost(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "slow", "--", "sh", "-c", "echo up; sleep 30")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	_, _, err = runJob(t, "cancel", "1")
	require.NoError(t, err)

	out, _, err := runFlow(t, "wait", "wait", "slow", "60")
	assert.Equal(t, exitLost, flowExit(err))
	assert.Regexp(t, `^lost slow: job 1 was cancelled`, out)
}

func TestFlowWait_RefusesANameWhoseJobHasntEnded(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "slow", "--", "sh", "-c", "echo up; sleep 30")
	require.NoError(t, err)

	_, _, err = runFlow(t, "wait", "start", "slow", "--", "true")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "slow is still running as job 1")

	_, _, err = runJob(t, "cancel", "1")
	require.NoError(t, err)
	d.waitFinished(t, 1)
	out, _, err := runFlow(t, "wait", "start", "slow", "--", "true")
	require.NoError(t, err, "a name whose job has ended starts again")
	assert.Contains(t, out, "started slow: job 2;")
}

func TestFlowWait_FollowsAJobByIDAndSaysWhenTheQueueDoesntKnowIt(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	_, _, err := runJob(t, "submit", "--no-notify", "--", "sh", "-c", "echo x")
	require.NoError(t, err)

	out, _, err := runFlow(t, "wait", "wait", "--job", "1", "60")
	require.NoError(t, err)
	assert.Equal(t, "done job-1 exit 0\n", out)
	b, err := os.ReadFile(filepath.Join(conf.state, "agent-wait", "job-1.log"))
	require.NoError(t, err)
	assert.Equal(t, "x\n", string(b))

	out, _, err = runFlow(t, "wait", "wait", "--job", "99")
	assert.Equal(t, exitLost, flowExit(err))
	assert.Equal(t, "lost job-99: the queue no longer knows job 99\n", out)
}

func TestFlowWait_RefusesPollsAndLoomCommands(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "poll", "--", "bash", "-c", "until grep -q x f; do sleep 10; done")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "a poll isn't a step")
	_, _, err = runFlow(t, "wait", "start", "nested", "--", "loom", "job", "run", "--", "make")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "already a job")
	assert.Empty(t, d.jobs(), "a refused step queues nothing")
}

func TestFlowWait_IsPoll(t *testing.T) {
	for _, c := range []struct {
		argv []string
		poll bool
	}{
		{[]string{"sleep", "5"}, true},
		{[]string{"/run/current-system/sw/bin/sleep", "5"}, true},
		{[]string{"bash", "-c", "until grep -q x f; do sleep 10; done"}, true},
		{[]string{"sh", "-c", "while :; do sleep 1; done"}, true},
		{[]string{"zsh", "-lc", "while true\ndo\n  sleep 2\ndone"}, true},
		{[]string{"bash", "--norc", "-c", "until [ -s f ]; do sleep 10; done"}, true},
		{[]string{"cargo", "test", "sleep_tests"}, false},
		{[]string{"bash", "-c", "sleep 2 && make"}, false},
		{[]string{"bash", "-c", "until make; do :; done"}, false},
		{[]string{"bash", "-c", "while read f; do make $f; done < list; sleep 1"}, false},
		{[]string{"grep", "-c", "until x; do sleep 1; done", "file"}, false},
		{[]string{"bash", "script.sh", "-c", "while x; do sleep 1; done"}, false},
	} {
		assert.Equal(t, c.poll, isPoll(c.argv), "%q", c.argv)
	}
}

func TestFlowWait_Usage(t *testing.T) {
	useFakeConf(t)
	for _, args := range [][]string{
		{"wait", "start", "build"},
		{"wait", "start", "build", "true"},
		{"wait", "start", "bad name", "--", "true"},
		{"wait", "wait"},
		{"wait", "wait", "--job", "1", "x", "60"},
		{"wait", "wait", "--job", "0"},
		{"wait", "wait", "build", "171"},
	} {
		_, _, err := runFlow(t, args...)
		assert.Equal(t, exitUsage, flowExit(err), "%q", args)
	}
	_, _, err := runFlow(t, "wait", "wait", "never")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "nothing started as never")
}

func TestFlowStanding(t *testing.T) {
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 1, State: sched.Running, Started: time.Now().Add(-70 * time.Second)},
		{ID: 2, State: sched.Queued},
		{ID: 3, State: sched.Queued},
		{ID: 4, State: sched.Held, Waiting: "after job 1 (running)"},
		{ID: 5, State: sched.Done},
	}}
	assert.Equal(t, "running 1m10s", standing(q, q.Jobs[0]))
	assert.Equal(t, "queued (2nd in line)", standing(q, q.Jobs[2]))
	assert.Equal(t, "held: runs after job 1 (running)", standing(q, q.Jobs[3]))
	assert.Equal(t, "ended done", standing(q, q.Jobs[4]))
}

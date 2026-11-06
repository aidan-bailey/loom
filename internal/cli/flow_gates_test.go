package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readTestRun is run name's record in the fake state dir.
func readTestRun(t *testing.T, conf *fakeConf, name string) *gateRun {
	t.Helper()
	r, err := readRun(runPath(filepath.Join(conf.state, "gates"), name))
	require.NoError(t, err)
	return r
}

func TestFlowGates_AStoryRunChainsItsGatesAndPasses(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo one", "SKIPPED cargo doc {packages} (the story changed no Cargo package)", "RUN echo two"}

	out, _, err := runFlow(t, "gates", "start", "--story", "abc1234", "def5678")
	require.NoError(t, err)
	assert.Equal(t, "started story-abc1234: 3 steps, jobs 1, 2\n2 SKIPPED: cargo doc {packages} (the story changed no Cargo package)\n", out)
	assert.Equal(t, [][]string{{"--story", "abc1234", "def5678"}}, conf.gateArgs)
	assert.Equal(t, []string{"bash", "-c", "echo one"}, d.job(1).Argv)
	assert.Equal(t, "gate 1/3: echo one", d.job(1).Description)
	assert.Equal(t, sched.JobID(1), d.job(2).After, "each gate runs after the one before")

	_, _, err = runFlow(t, "gates", "start", "--final")
	assert.Equal(t, exitUsage, flowExit(err), "one run at a time in a worktree")
	assert.ErrorContains(t, err, "run story-abc1234 hasn't ended here")

	out, _, err = runFlow(t, "gates", "wait", "60")
	require.NoError(t, err)
	assert.Equal(t, "story-abc1234: 3 steps\n1 PASS: echo one\n2 SKIPPED: cargo doc {packages} (the story changed no Cargo package)\n3 PASS: echo two\nended: 2 PASS, 1 SKIPPED\n", out)
	b, err := os.ReadFile(filepath.Join(conf.state, "gates", "story-abc1234", "1.log"))
	require.NoError(t, err)
	assert.Equal(t, "one\n", string(b))

	out, _, err = runFlow(t, "gates", "cancel", "story-abc1234")
	require.NoError(t, err)
	assert.Equal(t, "nothing to cancel: story-abc1234 has ended\n", out)
	_, _, err = runFlow(t, "gates", "start", "--final")
	require.NoError(t, err, "a run that has ended no longer holds the worktree")
}

func TestFlowGates_AStoryStopsAtItsFirstFail(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo bad; exit 3", "RUN echo never", "FAIL cargo test {packages} (cannot expand {packages}: no metadata)", "RUN echo after"}

	out, _, err := runFlow(t, "gates", "start", "--story", "abc")
	require.NoError(t, err)
	assert.Contains(t, out, "started story-abc: 4 steps, jobs 1, 2\n")
	assert.Contains(t, out, "4 SKIPPED: echo after (not run: gate 3 failed)\n", "no RUN line after the expansion's first FAIL is chained")

	out, _, err = runFlow(t, "gates", "wait", "60")
	assert.Equal(t, 1, flowExit(err))
	assert.Regexp(t, `1 FAIL: echo bad; exit 3 \(job 1 failed in \S+ \(exit 3\); log \S+/gates/story-abc/1\.log\)\n`, out)
	assert.Contains(t, out, "2 SKIPPED: echo never (not run: gate 1 failed)\n")
	assert.Contains(t, out, "3 FAIL: cargo test {packages} (cannot expand {packages}: no metadata)\n")
	assert.Contains(t, out, "ended: 2 FAIL, 2 SKIPPED\n")
}

func TestFlowGates_AFinalFailQueuesTheRestAgain(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN exit 3", "RUN echo two", "RUN echo three"}
	_, _, err := runFlow(t, "gates", "start", "--final")
	require.NoError(t, err)

	out, _, err := runFlow(t, "gates", "wait", "final", "0")
	assert.Equal(t, exitStillRunning, flowExit(err), "a bounded wait returns before the run ends")
	assert.Contains(t, out, "not ended: call loom flow gates wait final again\n")

	out, _, err = runFlow(t, "gates", "wait", "final", "60")
	assert.Equal(t, 1, flowExit(err), "a later call resumes the run from its record")
	assert.Contains(t, out, "1 FAIL: exit 3")
	assert.Contains(t, out, "2 PASS: echo two\n")
	assert.Contains(t, out, "3 PASS: echo three\n")
	run := readTestRun(t, conf, "final")
	require.Len(t, run.Steps[1].Jobs, 2, "gate 2 ran in a second generation")
	again := run.Steps[1].Jobs[1]
	assert.Equal(t, sched.Cancelled, d.job(run.Steps[1].Jobs[0]).State)
	assert.Equal(t, sched.JobID(0), d.job(again).After, "the new chain's first gate runs after nothing")
	assert.Equal(t, again, d.job(run.Steps[2].job()).After)
}

func TestFlowGates_EachVerdictComesFromTheJobsEnd(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo 0 tests ran", "RUN echo checker breaks", "RUN kill -KILL $$", "RUN echo fine"}
	conf.vacuous = func(file string) (string, bool, error) {
		b, err := os.ReadFile(file)
		require.NoError(t, err)
		switch {
		case strings.Contains(string(b), "0 tests ran"):
			return "0 tests ran", true, nil
		case strings.Contains(string(b), "checker breaks"):
			return "", false, errors.New("bad pattern")
		}
		return "", false, nil
	}
	_, _, err := runFlow(t, "gates", "start", "--final")
	require.NoError(t, err)

	out, _, err := runFlow(t, "gates", "wait", "60")
	assert.Equal(t, 1, flowExit(err))
	assert.Regexp(t, `1 VACUOUS: echo 0 tests ran \(0 tests ran; log \S+\)\n`, out)
	assert.Regexp(t, `2 FAIL \(infra\): echo checker breaks \(the vacuous check failed: bad pattern; log \S+\)\n`, out)
	assert.Regexp(t, `3 FAIL \(killed\): kill -KILL \$\$ \(job 3 was killed by SIGKILL after \S+; log \S+\)\n`, out)
	assert.Contains(t, out, "4 PASS: echo fine\n", "the gate after a killed one runs again")
}

func TestFlowGates_CancelEndsARunAndFreesTheWorktree(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo up; sleep 30", "RUN echo two"}
	_, _, err := runFlow(t, "gates", "start", "--story", "abc")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")

	out, _, err := runFlow(t, "gates", "wait", "1")
	assert.Equal(t, exitStillRunning, flowExit(err))
	assert.Regexp(t, `1 running \d+s: echo up; sleep 30\n`, out)
	assert.Contains(t, out, "2 held after gate 1: echo two\n")

	out, _, err = runFlow(t, "gates", "cancel")
	require.NoError(t, err)
	assert.Equal(t, "cancelled story-abc: jobs 1, 2\n", out)
	assert.Equal(t, sched.Cancelled, d.waitFinished(t, 1).State)
	assert.Equal(t, sched.Cancelled, d.waitFinished(t, 2).State)

	out, _, err = runFlow(t, "gates", "wait")
	assert.Equal(t, 1, flowExit(err))
	assert.Contains(t, out, "1 cancelled: echo up; sleep 30\n")
	assert.Contains(t, out, "\ncancelled\n")
	_, _, err = runFlow(t, "gates", "start", "--final")
	require.NoError(t, err, "a cancelled run no longer holds the worktree")
}

// A gate cancelled by someone else, while the one before it still ran, is
// an infra failure, not a gate skipped after a failure.
func TestFlowGates_AGateCancelledFromOutsideIsAnInfraFail(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo up; sleep 2", "RUN echo two"}
	_, _, err := runFlow(t, "gates", "start", "--story", "abc")
	require.NoError(t, err)
	_, _, err = runJob(t, "cancel", "2")
	require.NoError(t, err)
	d.waitFinished(t, 1)

	out, _, err := runFlow(t, "gates", "wait", "60")
	assert.Equal(t, 1, flowExit(err))
	assert.Contains(t, out, "1 PASS: echo up; sleep 2\n")
	assert.Regexp(t, `2 FAIL \(infra\): echo two \(job 2 was cancelled`, out)
}

func TestFlowGates_TwoWaitsQueueTheRestOnce(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN exit 3", "RUN echo two"}
	_, _, err := runFlow(t, "gates", "start", "--final")
	require.NoError(t, err)

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := runFlow(t, "gates", "wait", "final", "60")
			codes[i] = flowExit(err)
		}()
	}
	wg.Wait()
	assert.Equal(t, []int{1, 1}, codes)
	assert.Len(t, d.jobs(), 3, "the gate after the failure was queued again once")
}

func TestFlowGates_AChainItCantQueueWholeIsCancelledAndLeavesNoRecord(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo up; sleep 30", "RUN echo two"}
	calls := 0
	old := submitFlowJob
	submitFlowJob = func(c *client, sub sched.Submit) (sched.JobID, error) {
		if calls++; calls == 2 {
			return 0, errors.New("refused for the test")
		}
		return old(c, sub)
	}
	t.Cleanup(func() { submitFlowJob = old })

	_, _, err := runFlow(t, "gates", "start", "--story", "abc")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "gate 2 was not queued (refused for the test), so the 1 queued before it were cancelled")
	assert.Equal(t, sched.Cancelled, d.waitFinished(t, 1).State)
	records, err := filepath.Glob(filepath.Join(conf.state, "gates", "*.json"))
	require.NoError(t, err)
	assert.Empty(t, records)
}

func TestFlowGates_ARecordItCantReadIsLeftAsItIs(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	dir := filepath.Join(conf.state, "gates")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	bad := filepath.Join(dir, "final.json")
	require.NoError(t, os.WriteFile(bad, []byte("{not json"), 0o600))

	_, _, err := runFlow(t, "gates", "wait", "final")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "the record is left as it is")
	_, _, err = runFlow(t, "gates", "start", "--story", "abc")
	assert.Equal(t, exitNotRun, flowExit(err), "nor is it guessed past")
	b, err := os.ReadFile(bad)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(b))
}

func TestFlowGates_AJobTheQueueNoLongerKnowsIsAnInfraFail(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	dir := filepath.Join(conf.state, "gates")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, saveRun(dir, &gateRun{Schema: gateRunSchema, Name: "final", Kind: "final",
		Steps: []gateStep{{Line: "RUN echo x", Run: true, Jobs: []sched.JobID{99}}}}))

	out, _, err := runFlow(t, "gates", "wait", "final")
	assert.Equal(t, 1, flowExit(err))
	assert.Contains(t, out, "1 FAIL (infra): echo x (the queue no longer knows job 99)\n")
}

func TestFlowGates_StartRefusesAnExpansionItCantRead(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo one", "MAYBE something"}
	_, _, err := runFlow(t, "gates", "start", "--final")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, `"MAYBE something"`)

	conf.gates, conf.gatesErr = nil, errors.New("loom flow conf gates exited 1: no usable profile")
	_, _, err = runFlow(t, "gates", "start", "--final")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "no usable profile")
	assert.Empty(t, d.jobs())
}

func TestFlowGates_Usage(t *testing.T) {
	useFakeConf(t)
	for _, args := range [][]string{
		{"gates", "start"},
		{"gates", "start", "--story"},
		{"gates", "start", "--final", "abc"},
		{"gates", "start", "--story", "--final", "abc"},
		{"gates", "wait", "final", "999"},
		{"gates", "wait", "bad name"},
	} {
		_, _, err := runFlow(t, args...)
		assert.Equal(t, exitUsage, flowExit(err), "%q", args)
	}
	_, _, err := runFlow(t, "gates", "wait")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "no gate run here")
}

func TestFlowGates_RunArgsAndSlug(t *testing.T) {
	for _, c := range []struct {
		args       []string
		name, secs string
	}{
		{nil, "", ""},
		{[]string{"60"}, "", "60"},
		{[]string{"final"}, "final", ""},
		{[]string{"story-abc", "5"}, "story-abc", "5"},
	} {
		name, secs := runArgs(c.args)
		assert.Equal(t, c.name, name, "%q", c.args)
		assert.Equal(t, c.secs, secs, "%q", c.args)
	}
	assert.Equal(t, "abc1234def56", runSlug("abc1234def5678"))
	assert.Equal(t, "HEAD-2", runSlug("HEAD~2"))
}

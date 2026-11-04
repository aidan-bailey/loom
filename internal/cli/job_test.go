package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runJob runs `loom job args…` on a fresh command tree (newJobCmd), so no
// flag carries over from another test.
func runJob(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newJobCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// jobs is the daemon's jobs as the test's own client has them now.
func (d *testDaemon) jobs() []core.JobView {
	if d.client.FlushForTest() != nil {
		return nil
	}
	d.client.Sync()
	return d.client.Queue().Jobs
}

// job is the daemon's view of job id, or the zero view when it serves none.
func (d *testDaemon) job(id sched.JobID) core.JobView {
	for _, j := range d.jobs() {
		if j.ID == id {
			return j
		}
	}
	return core.JobView{}
}

// waitFinished waits until job id has ended, which the daemon notices when
// its queue gate next runs (every 3 s), and returns its view.
func (d *testDaemon) waitFinished(t *testing.T, id sched.JobID) core.JobView {
	t.Helper()
	var v core.JobView
	require.Eventually(t, func() bool {
		v = d.job(id)
		return v.State.Finished()
	}, 30*time.Second, 100*time.Millisecond, "job %d never ended", id)
	return v
}

// waitLog waits until job id's output holds text: the command ran, so its
// runner holds the lock a cancel needs.
func (d *testDaemon) waitLog(t *testing.T, id sched.JobID, text string) {
	t.Helper()
	require.Eventually(t, func() bool {
		path := d.job(id).LogPath
		if path == "" {
			return false
		}
		b, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(b), text)
	}, 30*time.Second, 50*time.Millisecond, "job %d never printed %q", id, text)
}

// parsedSubmit parses a submit command line's flags the way cobra does and
// returns them with the command and the arguments left over.
func parsedSubmit(t *testing.T, line ...string) (*claimFlags, *cobra.Command, []string) {
	t.Helper()
	f := &claimFlags{}
	c := &cobra.Command{Use: "submit"}
	f.add(c)
	require.NoError(t, c.Flags().Parse(line))
	return f, c, c.Flags().Args()
}

func TestJobShellWord(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"cargo", "cargo"},
		{"--release", "--release"},
		{"a/b-c.d=1,2:3@x%y+z_w", "a/b-c.d=1,2:3@x%y+z_w"},
		{"", "''"},
		{"two words", "'two words'"},
		{"it's", `'it'\''s'`},
		{"$HOME", "'$HOME'"},
		{"a;b", "'a;b'"},
		{"é", "'é'"},
	} {
		assert.Equal(t, tc.want, shellWord(tc.in), "%q", tc.in)
	}
}

func TestJobDefaultDescription(t *testing.T) {
	assert.Equal(t, "cargo test -p sched 'two words'", defaultDescription([]string{"cargo", "test", "-p", "sched", "two words"}))

	long := defaultDescription([]string{"echo", strings.Repeat("é", 100)})
	assert.Equal(t, 60, len([]rune(long)), "cut to 60 characters, not 60 bytes")
	assert.True(t, strings.HasPrefix(long, "echo 'éé"), "the cut keeps whole characters: %q", long)
}

func TestJobOrdinal(t *testing.T) {
	for n, want := range map[int]string{
		1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 10: "10th", 11: "11th", 12: "12th", 13: "13th",
		21: "21st", 22: "22nd", 23: "23rd", 101: "101st", 111: "111th", 112: "112th",
	} {
		assert.Equal(t, want, ordinal(n), "%d", n)
	}
}

func TestJobParseID(t *testing.T) {
	id, err := parseJobID("14")
	require.NoError(t, err)
	assert.Equal(t, sched.JobID(14), id)

	for _, bad := range []string{"", "0", "-3", "x", "1.5", "12abc"} {
		_, err := parseJobID(bad)
		require.Error(t, err, "%q", bad)
		assert.Contains(t, err.Error(), "is not a job ID", "%q", bad)
	}
}

func TestJobCheckPlatform(t *testing.T) {
	err := checkPlatform("windows")
	require.ErrorIs(t, err, sched.ErrUnsupported)
	assert.Equal(t, "the work queue isn't available on Windows", err.Error())
	assert.NoError(t, checkPlatform("linux"))
	assert.NoError(t, checkPlatform("darwin"))
}

func TestJobQueuePlace(t *testing.T) {
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 1, State: sched.Running},
		{ID: 2, State: sched.Running},
		{ID: 3, State: sched.Queued},
		{ID: 4, State: sched.Queued},
		{ID: 5, State: sched.Done},
	}}

	n, running := queuePlace(q, 4)
	assert.Equal(t, 2, n, "the second queued job")
	assert.Equal(t, 2, running)

	n, _ = queuePlace(q, 3)
	assert.Equal(t, 1, n)

	n, running = queuePlace(q, 1)
	assert.Zero(t, n, "a running job is in no line")
	assert.Equal(t, 2, running)
}

func TestJobSubmission_CapturesTheShellItRunsIn(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("LOOMJOB_TEST", "bar")
	t.Setenv(claudemod.EnvLaunch, "L1")
	f, c, args := parsedSubmit(t, "--cpus", "8", "--mem", "12", "--io", "--", "cargo", "test", "--release")

	sub, err := f.submission(c, args, true, 20)

	require.NoError(t, err)
	assert.Equal(t, []string{"cargo", "test", "--release"}, sub.Argv)
	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, wd, sub.Cwd)
	assert.Contains(t, sub.Env, "LOOMJOB_TEST=bar", "the shell's environment travels with the job")
	assert.Equal(t, "L1", sub.Launch)
	assert.Equal(t, "cargo test --release", sub.Description, "the command line, when -d is not given")
	assert.Equal(t, sched.Claim{CPUs: 8, MemoryGB: 12, IO: true}, sub.Claim)
	assert.True(t, sub.Notify)
	assert.Equal(t, 20, sub.Tail)
}

func TestJobSubmission_TheDescription(t *testing.T) {
	f, c, args := parsedSubmit(t, "-d", "polyoxide tests", "--", "cargo", "test")
	sub, err := f.submission(c, args, true, 20)
	require.NoError(t, err)
	assert.Equal(t, "polyoxide tests", sub.Description)

	// An empty -d is sent as it is: the daemon refuses it, saying so.
	f, c, args = parsedSubmit(t, "-d", "", "--", "cargo", "test")
	sub, err = f.submission(c, args, true, 20)
	require.NoError(t, err)
	assert.Equal(t, "", sub.Description)
}

func TestJobSubmission_TheClaim(t *testing.T) {
	f, c, args := parsedSubmit(t, "--exclusive", "--", "bench")
	sub, err := f.submission(c, args, true, 20)
	require.NoError(t, err)
	assert.Equal(t, sched.Claim{Exclusive: true}, sub.Claim)

	f, c, args = parsedSubmit(t, "--", "true")
	sub, err = f.submission(c, args, false, 20)
	require.NoError(t, err)
	assert.Equal(t, sched.Claim{}, sub.Claim, "no flags: the daemon applies the default claim")
	assert.False(t, sub.Notify)

	// On the wire a 0 means the default claim, so an explicit 0 cannot be
	// sent: it is refused here, with the daemon's wording.
	for _, tc := range []struct {
		flag, value, want string
	}{
		{"--cpus", "0", "--cpus is 0: it must be at least 1"},
		{"--cpus", "-4", "--cpus is -4: it must be at least 1"},
		{"--mem", "0", "--mem is 0: it must be at least 1"},
	} {
		f, c, args = parsedSubmit(t, tc.flag, tc.value, "--", "true")
		_, err = f.submission(c, args, true, 20)
		require.Error(t, err, tc.flag+" "+tc.value)
		assert.Equal(t, tc.want, err.Error())
	}
}

func TestJobSubmission_TheCommandComesAfterTheDash(t *testing.T) {
	for _, line := range [][]string{
		{"cargo", "test"},
		{"-d", "x"},
		{"--"},
		{"cargo", "--", "test"},
	} {
		f, c, args := parsedSubmit(t, line...)
		_, err := f.submission(c, args, true, 20)
		require.Error(t, err, "%q", line)
		assert.Contains(t, err.Error(), "put the command after --", "%q", line)
	}
}

func TestJobList_ShowsEachJobsStateOwnerClaimAndWait(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 3, State: sched.Running, Description: "tests", Owner: 7, OwnerTitle: "alpha", Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Submitted: now.Add(-5 * time.Minute), Started: now.Add(-3 * time.Minute)},
		{ID: 4, State: sched.Queued, Description: "bench", Claim: sched.Claim{Exclusive: true}, Submitted: now.Add(-90 * time.Second), Waiting: "needs 14 cores (2 free)"},
		{ID: 2, State: sched.Failed, Description: "build", Owner: 9, Claim: sched.Claim{CPUs: 2, MemoryGB: 2, IO: true}, Submitted: now.Add(-time.Hour), Started: now.Add(-50 * time.Minute), Ended: now.Add(-40 * time.Minute), Exit: &sched.Exit{Code: 3}},
		{ID: 1, State: sched.Failed, Description: "link", Claim: sched.Claim{CPUs: 1, MemoryGB: 1}, Submitted: now.Add(-2 * time.Hour), Started: now.Add(-110 * time.Minute), Ended: now.Add(-100 * time.Minute), Exit: &sched.Exit{Code: -1, Signal: "SIGKILL"}},
	}}

	var all bytes.Buffer
	require.NoError(t, writeJobList(&all, q, true, now))

	assert.Regexp(t, `(?m)^ID +STATE +DESCRIPTION +OWNER +CLAIM +TIME +WAITS FOR$`, all.String())
	assert.Regexp(t, `(?m)^3 +running +tests +alpha +4c/6G +3m0s *$`, all.String())
	assert.Regexp(t, `(?m)^4 +queued +bench +- +exclusive +1m30s +needs 14 cores \(2 free\)$`, all.String())
	assert.Regexp(t, `(?m)^2 +failed \(exit 3\) +build +#9 +2c/2G io +10m0s *$`, all.String())
	assert.Regexp(t, `(?m)^1 +failed \(SIGKILL\) +link +- +1c/1G +10m0s *$`, all.String())

	var live bytes.Buffer
	require.NoError(t, writeJobList(&live, q, false, now))
	assert.Contains(t, live.String(), "tests")
	assert.Contains(t, live.String(), "bench")
	assert.NotContains(t, live.String(), "build", "finished jobs need --all")
	assert.NotContains(t, live.String(), "link", "finished jobs need --all")
}

func TestJobList_SaysWhenThereIsNothingToList(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer

	require.NoError(t, writeJobList(&out, core.QueueView{}, false, now))
	assert.Equal(t, "No running or queued jobs.\n", out.String())

	out.Reset()
	require.NoError(t, writeJobList(&out, core.QueueView{}, true, now))
	assert.Equal(t, "No jobs.\n", out.String())

	out.Reset()
	done := core.QueueView{Jobs: []core.JobView{{ID: 1, State: sched.Done, Started: now, Ended: now}}}
	require.NoError(t, writeJobList(&out, done, false, now))
	assert.Equal(t, "No running or queued jobs. 1 finished (loom job list --all shows them).\n", out.String())

	out.Reset()
	paused := core.QueueView{Paused: "scheduler.cpus is 5000: it must be 1–1024"}
	require.NoError(t, writeJobList(&out, paused, false, now))
	assert.Equal(t, "admission paused: scheduler.cpus is 5000: it must be 1–1024\nNo running or queued jobs.\n", out.String())
}

// A job claiming every cap holds the host: the next one queues behind it,
// `list` says why it waits, and a cancel ends each (a queued job at once, a
// running one when the daemon next settles it).
func TestJobSubmit_QueuesBehindAnExclusiveJobAndListShowsWhy(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")

	out, _, err := runJob(t, "submit", "-d", "hog", "--exclusive", "--", "sh", "-c", "echo up; exec sleep 60")
	require.NoError(t, err)
	assert.Equal(t, "job 1 started\nno session to notify: follow it with loom job wait 1\n", out)
	d.waitLog(t, 1, "up")

	t.Setenv(claudemod.EnvLaunch, "no-such-launch")
	out, errOut, err := runJob(t, "submit", "-d", "next", "--", "true")
	require.NoError(t, err)
	assert.Equal(t, "job 2 queued (1st in line, 1 running)\n", out)
	assert.Contains(t, errOut, "this shell's loom session (launch no-such-launch) isn't one the daemon serves: job 2 has no owner, so no message will come")

	out, _, err = runJob(t, "list")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^1 +running +hog +- +exclusive +\S+ *$`, out)
	assert.Regexp(t, `(?m)^2 +queued +next +- +\d+c/\d+G +\S+ +needs \d+ cores \(0 free\)$`, out)

	out, _, err = runJob(t, "cancel", "2")
	require.NoError(t, err)
	assert.Equal(t, "cancel requested for job 2\n", out)
	require.Eventually(t, func() bool { return d.job(2).State == sched.Cancelled }, 10*time.Second, 50*time.Millisecond,
		"a queued job is cancelled at once")
	assert.Equal(t, "with loom job cancel", d.job(2).CancelReason)

	out, _, err = runJob(t, "list", "--all")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^2 +cancelled +next `, out)

	_, _, err = runJob(t, "cancel", "2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "job 2 has finished")
	_, _, err = runJob(t, "cancel", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "there is no job 99")
	_, _, err = runJob(t, "cancel", "soon")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"soon" is not a job ID`)

	_, _, err = runJob(t, "cancel", "1")
	require.NoError(t, err)
	hog := d.waitFinished(t, 1)
	assert.Equal(t, sched.Cancelled, hog.State)
	assert.Equal(t, "with loom job cancel", hog.CancelReason)
}

// The daemon holds every bound, so a refused submission is the daemon's text,
// printed as the command's error, and queues nothing.
func TestJobSubmit_RefusalsArePrintedAndQueueNothing(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"exclusive with cpus", []string{"submit", "-d", "x", "--exclusive", "--cpus", "2", "--", "true"}, "--exclusive claims everything: drop --cpus, --mem and --io"},
		{"zero cpus", []string{"submit", "-d", "x", "--cpus", "0", "--", "true"}, "--cpus is 0: it must be at least 1"},
		{"zero memory", []string{"submit", "-d", "x", "--mem", "0", "--", "true"}, "--mem is 0: it must be at least 1"},
		{"over the cpu cap", []string{"submit", "-d", "x", "--cpus", "5000", "--", "true"}, "claims 5000 cores; this host's cap is"},
		{"over the memory cap", []string{"submit", "-d", "x", "--mem", "500000", "--", "true"}, "claims 500000 GB; this host's cap is"},
		{"empty description", []string{"submit", "-d", "", "--", "true"}, "the description is empty"},
		{"tail out of range", []string{"submit", "-d", "x", "--tail", "201", "--", "true"}, "--tail is 201: it must be 0–200"},
		{"no command", []string{"submit", "-d", "x"}, "put the command after --"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := runJob(t, tc.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, out)
		})
	}
	assert.Empty(t, d.jobs(), "no refusal queued a job")
}

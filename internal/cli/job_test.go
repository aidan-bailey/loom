package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
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

// A multi-line script gives a one-line description, and a description (or an
// owner) with control characters keeps list's rows whole.
func TestJobDescription_StaysOnOneLine(t *testing.T) {
	desc := defaultDescription([]string{"sh", "-c", "echo one\n\techo two"})
	assert.NotContains(t, desc, "\n")
	assert.NotContains(t, desc, "\t")
	assert.Equal(t, "sh -c 'echo one  echo two'", desc)

	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 1, State: sched.Running, Description: "line one\nline two\tend", OwnerTitle: "al\rpha", Owner: 7, Claim: sched.Claim{CPUs: 1, MemoryGB: 1}, Started: now},
		{ID: 2, State: sched.Running, Description: "plain", Claim: sched.Claim{CPUs: 1, MemoryGB: 1}, Started: now},
	}}
	var out bytes.Buffer
	require.NoError(t, writeJobList(&out, q, true, now))

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	require.Len(t, lines, 3, "a header and one row per job: %q", out.String())
	assert.Regexp(t, `^1 +running +line one line two end +al pha +1c/1G`, lines[1])
	assert.Regexp(t, `^2 +running +plain +- +1c/1G`, lines[2])
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

	assert.Regexp(t, `(?m)^ID +STATE +DESCRIPTION +OWNER +CLAIM +TIME +USUAL +WAITS FOR$`, all.String())
	assert.Regexp(t, `(?m)^3 +running +tests +alpha +4c/6G +3m0s +- *$`, all.String())
	assert.Regexp(t, `(?m)^4 +queued +bench +- +exclusive +1m30s +- +needs 14 cores \(2 free\)$`, all.String())
	assert.Regexp(t, `(?m)^2 +failed \(exit 3\) +build +#9 +2c/2G io +10m0s +- *$`, all.String())
	assert.Regexp(t, `(?m)^1 +failed \(SIGKILL\) +link +- +1c/1G +10m0s +- *$`, all.String())

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
	assert.Equal(t, "job 2 queued (1st in line, 1 running)\nsized 4c/6G (default)\n", out)
	assert.Contains(t, errOut, "this shell's loom session (launch no-such-launch) isn't one the daemon serves: job 2 has no owner, so no message will come")

	out, _, err = runJob(t, "list")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^1 +running +hog +- +exclusive +\S+ +- *$`, out)
	assert.Regexp(t, `(?m)^2 +queued +next +- +\d+c/\d+G default +\S+ +- +(starts \S+ · )?needs \d+ cores \(0 free\)$`, out)

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

// exitCode is the exit code a command's error asks main to end with.
func exitCode(t *testing.T, err error) int {
	t.Helper()
	var exit *ExitError
	require.ErrorAs(t, err, &exit)
	return exit.Code
}

// interruptWhenAJobExists replaces the follower's signal source with one that
// delivers sig once d serves a job, and restores it when the test ends.
func interruptWhenAJobExists(t *testing.T, d *testDaemon, sig os.Signal) {
	t.Helper()
	sigs := make(chan os.Signal, 1)
	orig := notifySignals
	notifySignals = func() (<-chan os.Signal, func()) { return sigs, func() {} }
	t.Cleanup(func() { notifySignals = orig })
	go func() {
		for {
			if len(d.jobs()) > 0 {
				sigs <- sig
				return
			}
			select {
			case <-t.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()
}

func TestJobOutcome(t *testing.T) {
	t0 := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	ran := func(state sched.State, exit *sched.Exit) core.JobView {
		return core.JobView{ID: 5, State: state, Submitted: t0.Add(-time.Minute), Started: t0, Ended: t0.Add(190 * time.Second), Exit: exit}
	}
	cancelled := ran(sched.Cancelled, &sched.Exit{Code: -1, Signal: "SIGTERM"})
	cancelled.CancelReason = "from the work view"
	cancelledBare := ran(sched.Cancelled, nil)
	lost := ran(sched.Lost, nil)
	lost.Failure = "its runner died without a result (the host may have rebooted)"
	noStart := ran(sched.Failed, nil)
	noStart.Failure = "its folder is gone"
	execFailed := ran(sched.Failed, &sched.Exit{Code: 127, Error: `exec: "nope": executable file not found in $PATH`})
	// The daemon never writes this pair; core.JobView.StartFailed counts it as
	// a start that failed, as the notice and the TUI do, and so does the CLI.
	reasonedWithExit := ran(sched.Failed, &sched.Exit{Code: 3})
	reasonedWithExit.Failure = "its folder is gone"

	for _, tc := range []struct {
		name string
		job  core.JobView
		line string
		code int
	}{
		{"done", ran(sched.Done, &sched.Exit{Code: 0}), "job 5 done in 3m10s (exit 0)", 0},
		{"failed", ran(sched.Failed, &sched.Exit{Code: 3}), "job 5 failed in 3m10s (exit 3)", 3},
		{"killed", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIGKILL"}), "job 5 was killed by SIGKILL after 3m10s", 137},
		{"terminated", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIGTERM"}), "job 5 was killed by SIGTERM after 3m10s", 143},
		{"a signal beyond the usual few", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIGXCPU"}), "job 5 was killed by SIGXCPU after 3m10s", 152},
		{"a real-time signal the platform has no name for", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIG63"}), "job 5 was killed by SIG63 after 3m10s", 191},
		{"a name no signal has", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIGBOGUS"}), "job 5 was killed by SIGBOGUS after 3m10s", exitNotRun},
		{"cancelled", cancelled, "job 5 was cancelled from the work view", exitNotRun},
		{"cancelled, no reason", cancelledBare, "job 5 was cancelled", exitNotRun},
		{"lost", lost, "job 5 was lost: its runner died without a result (the host may have rebooted)", exitNotRun},
		{"its start failed", noStart, "job 5 could not start: its folder is gone", exitNotRun},
		{"a reason beside an exit record without an error", reasonedWithExit, "job 5 could not start: its folder is gone", exitNotRun},
		{"its exec failed", execFailed, `job 5 could not start: exec: "nope": executable file not found in $PATH`, exitNotRun},
	} {
		line, code := jobOutcome(tc.job)
		assert.Equal(t, tc.line, line, tc.name)
		assert.Equal(t, tc.code, code, tc.name)
	}
}

func TestJobInterruptCode(t *testing.T) {
	assert.Equal(t, 130, interruptCode(syscall.SIGINT))
	assert.Equal(t, 143, interruptCode(syscall.SIGTERM))
	assert.Equal(t, 129, interruptCode(syscall.SIGHUP))
	assert.Equal(t, 130, interruptCode(nil), "an interrupt that no signal brought (a cancelled context)")
}

func TestJobTailOffset(t *testing.T) {
	tail := func(content string, n int) string {
		off, err := tailOffset(strings.NewReader(content), int64(len(content)), n)
		require.NoError(t, err)
		return content[off:]
	}

	assert.Equal(t, "a\nb\nc\n", tail("a\nb\nc\n", -1), "a negative count is all of it")
	assert.Equal(t, "", tail("a\nb\nc\n", 0))
	assert.Equal(t, "c\n", tail("a\nb\nc\n", 1))
	assert.Equal(t, "b\nc\n", tail("a\nb\nc\n", 2))
	assert.Equal(t, "a\nb\nc\n", tail("a\nb\nc\n", 3))
	assert.Equal(t, "a\nb\nc\n", tail("a\nb\nc\n", 10))
	assert.Equal(t, "b", tail("a\nb", 1), "a last line with no newline is a line")
	assert.Equal(t, "", tail("", 5))

	var long strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&long, "line %05d\n", i)
	}
	assert.Equal(t, "line 04998\nline 04999\nline 05000\n", tail(long.String(), 3), "found across several read blocks")
	assert.Equal(t, long.String(), tail(long.String(), 5000))
	assert.Equal(t, long.String()[len("line 00001\n"):], tail(long.String(), 4999))
}

func TestJobCopyLog_CopiesOnlyWhatIsNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	var out bytes.Buffer
	f := &jobFollower{out: &out}

	require.NoError(t, f.copyLog(path), "a job that has not started has no log yet")
	assert.Empty(t, out.String())

	require.NoError(t, os.WriteFile(path, []byte("ab"), 0o600))
	require.NoError(t, f.copyLog(path))
	assert.Equal(t, "ab", out.String())

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = file.WriteString("cd")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.NoError(t, f.copyLog(path))
	assert.Equal(t, "abcd", out.String(), "the second read starts where the first stopped")

	require.NoError(t, f.copyLog(path))
	assert.Equal(t, "abcd", out.String(), "nothing new, nothing copied")
}

// `run` follows the job to its end: the job's output goes to stdout, its
// progress to stderr, and the command exits with the job's exit code. `logs`
// and `wait` then read the finished job back.
func TestJobRun_FollowsAJobToItsExitCodeAndLogsReadItBack(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	serveDaemon(t, "true")

	out, errOut, err := runJob(t, "run", "-d", "fails", "--", "sh", "-c", "echo out; echo err >&2; exit 3")
	assert.Equal(t, 3, exitCode(t, err))
	assert.Equal(t, "out\nerr\n", out, "stdout and stderr of the job, in order")
	assert.Contains(t, errOut, "loom: job 1 started after ")
	assert.Regexp(t, `loom: job 1 failed in \S+ \(exit 3\)\n`, errOut)

	out, errOut, err = runJob(t, "run", "-d", "fine", "--", "sh", "-c", "echo fine")
	require.NoError(t, err)
	assert.Equal(t, "fine\n", out)
	assert.Regexp(t, `loom: job 2 done in \S+ \(exit 0\)\n`, errOut)

	out, _, err = runJob(t, "logs", "1")
	require.NoError(t, err)
	assert.Equal(t, "out\nerr\n", out)
	out, _, err = runJob(t, "logs", "1", "--tail", "1")
	require.NoError(t, err)
	assert.Equal(t, "err\n", out)
	out, _, err = runJob(t, "logs", "1", "--tail", "0")
	require.NoError(t, err)
	assert.Equal(t, "", out)
	out, _, err = runJob(t, "logs", "1", "-f")
	require.NoError(t, err, "following a finished job prints its output and returns")
	assert.Equal(t, "out\nerr\n", out)
	_, _, err = runJob(t, "logs", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "there is no job 99")

	out, errOut, err = runJob(t, "wait", "1")
	assert.Equal(t, 3, exitCode(t, err), "wait follows a finished job too")
	assert.Equal(t, "out\nerr\n", out)
	assert.Regexp(t, `loom: job 1 failed in \S+ \(exit 3\)\n`, errOut)
}

// A job that queues behind another says where it stands, and when it starts
// how long it waited.
func TestJobRun_ShowsItsPlaceInLineThenItsStart(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "hog", "--exclusive", "--no-notify", "--", "sh", "-c", "echo up; sleep 2")
	require.NoError(t, err)

	out, errOut, err := runJob(t, "run", "-d", "next", "--", "sh", "-c", "echo next")

	require.NoError(t, err)
	assert.Equal(t, "next\n", out)
	assert.Regexp(t, `(?s)loom: job 2 queued \(1st in line, 1 running\)\n.*loom: job 2 started after \d+s\n.*loom: job 2 done in \S+ \(exit 0\)\n`, errOut)
	assert.Equal(t, 1, strings.Count(errOut, "job 2 queued (1st in line, 1 running)"), "the place is said once while it holds, not on every poll: %q", errOut)
	d.waitFinished(t, 1)
}

// A job cancelled while waited on ends the wait with 125: it did not run to
// an exit.
func TestJobWait_ACancelledJobExits125(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "nap", "--no-notify", "--", "sh", "-c", "echo up; exec sleep 60")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	_, _, err = runJob(t, "cancel", "1")
	require.NoError(t, err)

	_, errOut, err := runJob(t, "wait", "1")

	assert.Equal(t, 125, exitCode(t, err))
	assert.Contains(t, errOut, "loom: job 1 was cancelled with loom job cancel\n")

	_, errOut, err = runJob(t, "wait", "99")
	assert.Equal(t, 125, exitCode(t, err), "a job the daemon does not know is not the job's own exit")
	assert.Equal(t, "loom: there is no job 99\n", errOut)
}

// exitOf is the *ExitError a command ended with.
func exitOf(t *testing.T, err error) *ExitError {
	t.Helper()
	var exit *ExitError
	require.ErrorAs(t, err, &exit)
	return exit
}

// `run` that submits nothing exits 125, whatever stopped it, and says so; the
// job's own code and `submit`'s 1 stay apart from it. Cobra's usage errors
// keep exit 1.
func TestJobRun_NothingSubmittedExits125(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	dial := Join
	t.Cleanup(func() { Join = dial })

	t.Run("a refused submission", func(t *testing.T) {
		_, _, err := runJob(t, "run", "-d", "x", "--cpus", "5000", "--", "true")
		exit := exitOf(t, err)
		assert.Equal(t, 125, exit.Code)
		assert.Contains(t, exit.Msg, "loom: nothing was submitted: ")
		assert.Contains(t, exit.Msg, "claims 5000 cores; this host's cap is")
		assert.Empty(t, d.jobs())

		_, _, err = runJob(t, "submit", "-d", "x", "--cpus", "5000", "--", "true")
		require.Error(t, err)
		var asExit *ExitError
		assert.False(t, errors.As(err, &asExit), "submit keeps exit 1 for a refusal")
	})
	t.Run("a bad command line", func(t *testing.T) {
		for _, args := range [][]string{
			{"run", "-d", "x"},
			{"run", "-d", "x", "--cpus", "0", "--", "true"},
		} {
			_, _, err := runJob(t, args...)
			exit := exitOf(t, err)
			assert.Equal(t, 125, exit.Code, "%v", args)
			assert.Contains(t, exit.Msg, "loom: nothing was submitted: ")
		}
		assert.Empty(t, d.jobs())
	})
	t.Run("no daemon", func(t *testing.T) {
		Join = func() (*rpc.Client, error) { return nil, errors.New("no daemon to join") }
		_, _, err := runJob(t, "run", "-d", "x", "--", "true")
		exit := exitOf(t, err)
		assert.Equal(t, 125, exit.Code)
		assert.Equal(t, "loom: nothing was submitted: no daemon to join", exit.Msg)
	})
	t.Run("the daemon lost before the reply", func(t *testing.T) {
		Join = func() (*rpc.Client, error) {
			c, err := dial()
			d.srv.Close() // before the submit is sent
			return c, err
		}
		_, _, err := runJob(t, "run", "-d", "x", "--", "true")
		exit := exitOf(t, err)
		assert.Equal(t, 125, exit.Code)
		assert.Contains(t, exit.Msg, "loom: nothing was submitted: lost the loom daemon")
	})
	t.Run("a usage error", func(t *testing.T) {
		Join = dial
		_, _, err := runJob(t, "run", "--no-such-flag", "--", "true")
		require.Error(t, err)
		var asExit *ExitError
		assert.False(t, errors.As(err, &asExit), "cobra's usage errors exit 1 as for every command")
	})
}

// The Windows refusal comes before anything is submitted, so `run` ends it
// with the same 125 and line.
func TestJobRun_TheWindowsRefusalIsNothingSubmitted(t *testing.T) {
	exit := exitOf(t, notSubmitted(checkPlatform("windows")))
	assert.Equal(t, 125, exit.Code)
	assert.Contains(t, exit.Msg, "loom: nothing was submitted: ")
}

// A join that fails is a *joinError, which reads as its cause, and the
// command's work does not run: `run` reports it as nothing submitted. An error
// of the work's own is not one.
func TestWithDaemon_AFailedJoinIsAJoinError(t *testing.T) {
	isolateWorkspaces(t)
	serveDaemon(t, "true")
	dial := Join
	t.Cleanup(func() { Join = dial })
	c := &cobra.Command{}
	c.SetErr(io.Discard)
	var join *joinError

	cause := errors.New("no daemon to join")
	Join = func() (*rpc.Client, error) { return nil, cause }
	ran := false
	err := withDaemon(c, func(*client) error { ran = true; return nil })
	require.ErrorAs(t, err, &join)
	assert.ErrorIs(t, err, cause)
	assert.Equal(t, "no daemon to join", err.Error())
	assert.False(t, ran, "the work does not run without a daemon")

	Join = nil
	err = withDaemon(c, func(*client) error { ran = true; return nil })
	assert.ErrorAs(t, err, &join)
	assert.False(t, ran)

	Join = dial
	own := errors.New("the work's own")
	err = withDaemon(c, func(*client) error { ran = true; return own })
	assert.True(t, ran)
	assert.ErrorIs(t, err, own)
	assert.False(t, errors.As(err, &join), "an error of the work's own is not the join's")
}

// A follower that can't read the job's output.log can't go on, and exits 125:
// for a job it saw ended, saying how the job ended; for one that may still be
// running, saying so and how to follow it again.
func TestJobWait_AnUnreadableLogExits125(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	// A directory in the log's place opens, then fails to read: whoever runs
	// the test, root included.
	unreadable := func(t *testing.T, id sched.JobID) {
		t.Helper()
		log := d.job(id).LogPath
		require.NotEmpty(t, log)
		require.NoError(t, os.Remove(log))
		require.NoError(t, os.Mkdir(log, 0o755))
	}

	t.Run("a job seen ended", func(t *testing.T) {
		_, _, err := runJob(t, "run", "-d", "quick", "--", "sh", "-c", "echo hi")
		require.NoError(t, err)
		unreadable(t, 1)

		_, errOut, err := runJob(t, "wait", "1")

		assert.Equal(t, 125, exitCode(t, err))
		assert.Regexp(t, `^loom: copy the job's output: .*; job 1 done in \S+ \(exit 0\)\n$`, errOut)
	})
	t.Run("a job still running", func(t *testing.T) {
		_, _, err := runJob(t, "submit", "-d", "nap", "--no-notify", "--", "sh", "-c", "echo up; exec sleep 60")
		require.NoError(t, err)
		d.waitLog(t, 2, "up")
		unreadable(t, 2)

		_, errOut, err := runJob(t, "wait", "2")

		assert.Equal(t, 125, exitCode(t, err))
		assert.Regexp(t, `^loom: copy the job's output: .*; job 2 may still be running \(loom job wait 2 follows it again\)\n$`, errOut)
		_, _, err = runJob(t, "cancel", "2")
		require.NoError(t, err)
		d.waitFinished(t, 2)
	})
}

// queueBlind is a connection to the daemon that drops the queue's views
// (QueueChanged), so its client never sees a job, and passes every other frame
// whole. With cutAtReply it delivers the frames up to the first Reply event,
// then reads as closed: the daemon is lost the moment the submit's answer
// lands, before the job's view has (the Reply can come first, as awaitJob
// allows). The wire is one JSON frame per line.
type queueBlind struct {
	io.ReadWriteCloser
	cutAtReply bool
	in, out    []byte // what was read short of a whole frame; frames to deliver
	cut        bool
}

func (c *queueBlind) Read(p []byte) (int, error) {
	for len(c.out) == 0 {
		if c.cut {
			c.ReadWriteCloser.Close()
			return 0, io.EOF
		}
		chunk := make([]byte, 4096)
		n, err := c.ReadWriteCloser.Read(chunk)
		c.in = append(c.in, chunk[:n]...)
		for !c.cut {
			end := bytes.IndexByte(c.in, '\n')
			if end < 0 {
				break
			}
			frame := c.in[:end+1]
			c.in = c.in[end+1:]
			if bytes.Contains(frame, []byte(`"event":"QueueChanged"`)) {
				continue
			}
			c.out = append(c.out, frame...)
			c.cut = c.cutAtReply && bytes.Contains(frame, []byte(`"event":"Reply"`))
		}
		if err != nil && len(c.out) == 0 {
			return 0, err
		}
	}
	n := copy(p, c.out)
	c.out = c.out[n:]
	return n, nil
}

// `run` whose job the daemon accepted but never reported (the queue's views
// never reach it, though the daemon stays up) can't go on: awaitJob gives up,
// and the command says the job may still be running and how to follow it
// again, and exits 125.
func TestJobRun_AJobTheDaemonNeverReportsExits125(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	t.Cleanup(func(j func() (*rpc.Client, error)) func() { return func() { Join = j } }(Join))
	Join = func() (*rpc.Client, error) {
		a, b := net.Pipe()
		d.srv.Serve(a)
		return rpc.Dial(&queueBlind{ReadWriteCloser: b})
	}
	defer func(w time.Duration) { jobWait = w }(jobWait)
	jobWait = 50 * time.Millisecond

	_, errOut, err := runJob(t, "run", "-d", "quick", "--", "true")

	assert.Equal(t, 125, exitCode(t, err))
	assert.Equal(t, "loom: job 1 was submitted, but the loom daemon did not report it; job 1 may still be running (loom job wait 1 follows it again)\n", errOut)
	d.waitFinished(t, 1) // the job went on without the follower
}

// `run` that loses the daemon after it accepted the job, before the job's
// view arrives (awaitJob), says the job may still be running and exits 125.
func TestJobRun_LosingTheDaemonBeforeTheJobsViewSaysTheJobContinues(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	t.Cleanup(func(j func() (*rpc.Client, error)) func() { return func() { Join = j } }(Join))
	Join = func() (*rpc.Client, error) {
		a, b := net.Pipe()
		d.srv.Serve(a)
		return rpc.Dial(&queueBlind{ReadWriteCloser: b, cutAtReply: true})
	}

	_, errOut, err := runJob(t, "run", "-d", "quick", "--", "true")

	assert.Equal(t, 125, exitCode(t, err))
	assert.Equal(t, "loom: lost the daemon; job 1 may still be running (loom job wait 1 follows it again)\n", errOut)
	d.waitFinished(t, 1) // the job went on without the follower
}

// An interrupted follower of a job with no owner has no message to ask for;
// it says the job goes on and how to follow it again, and exits with the
// signal's code. The job is never cancelled by the follower's end.
func TestJobRun_AnInterruptWithNoOwnerSaysSo(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	interruptWhenAJobExists(t, d, syscall.SIGTERM)

	_, errOut, err := runJob(t, "run", "-d", "nap", "--", "sleep", "1")

	assert.Equal(t, 143, exitCode(t, err))
	assert.Contains(t, errOut, "loom: job 1 continues (loom job wait 1 follows it again)\n")
	assert.NotContains(t, errOut, "a message will bring")
	assert.NotContains(t, errOut, "no message was asked for", "a job with no owner is not asked about")
	v := d.waitFinished(t, 1)
	assert.Equal(t, sched.Done, v.State, "the job ran on and finished")
}

// Losing the daemon under a follower says the job may still be running and
// how to follow it again, and ends it with 125: the job's own exit code is
// not known.
func TestJobWait_LosingTheDaemonSaysTheJobContinues(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "nap", "--no-notify", "--", "sh", "-c", "echo up; sleep 2")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	// Close the server only once the follower has connected (and so holds the
	// running job in its replica), not after a guessed delay.
	dial := Join
	connected := make(chan struct{})
	Join = func() (*rpc.Client, error) {
		c, err := dial()
		if err == nil {
			close(connected)
		}
		return c, err
	}
	t.Cleanup(func() { Join = dial })
	go func() {
		<-connected
		d.srv.Close()
	}()

	_, errOut, err := runJob(t, "wait", "1")

	assert.Equal(t, 125, exitCode(t, err))
	assert.Equal(t, "loom: sized 4c/6G (default)\nloom: lost the daemon; job 1 may still be running (loom job wait 1 follows it again)\n", errOut)
}

// An interrupt that lands after the job ended reports its outcome, not that
// it continues, and asks for no message; the exit code is the signal's.
func TestJobInterrupted_AJobThatEndedReportsItsOutcome(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "run", "-d", "quick", "--", "true")
	require.NoError(t, err)
	// run followed the job on its own connection: the test's client sees
	// the end only once synced (d.job flushes and syncs it).
	require.Equal(t, sched.Done, d.job(1).State)
	var errOut bytes.Buffer
	c := &client{Client: d.client, errOut: &errOut, ctx: context.Background()}

	err = c.interrupted(1, syscall.SIGTERM)

	assert.Equal(t, 143, exitCode(t, err))
	assert.Regexp(t, `loom: job 1 done in \S+ \(exit 0\)\n`, errOut.String())
	assert.NotContains(t, errOut.String(), "continues")
	assert.NotContains(t, errOut.String(), "no message was asked for")
}

// The follower's request for a message is refused when the daemon refuses it
// (here the job has no owner), and the refusal is written, not swallowed.
// interrupted itself asks only for an owned job that has not ended, so a test
// reaches the refusal through askForMessage: through interrupted it would
// need the job to end between the follower's look and its request.
func TestJobAskForMessage_ADaemonRefusalIsWritten(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "nap", "--no-notify", "--", "sleep", "2")
	require.NoError(t, err)
	var errOut bytes.Buffer
	c := &client{Client: d.client, errOut: &errOut, ctx: context.Background()}

	assert.False(t, c.askForMessage(1))
	assert.Contains(t, errOut.String(), "loom: no message was asked for: job 1 has no owner\n")
	d.waitFinished(t, 1)
}

// An interrupted follower of a session's job asks the daemon for a message
// (`run` submitted it without one): the job's result then reaches the
// session's Claude through the mod when it ends.
func TestJobRun_AnInterruptAsksForAMessageWhenTheJobHasAnOwner(t *testing.T) {
	isolateWorkspaces(t)
	withModSocket(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	launch := launchOf(t, d, "ws-a", "alpha")
	require.NoError(t, d.loop.ModHello(launch, core.ModHello{Mod: 1}))
	carried := playMod(t, d.loop, launch, "", "")
	t.Setenv(claudemod.EnvLaunch, launch)
	interruptWhenAJobExists(t, d, syscall.SIGTERM)

	_, errOut, err := runJob(t, "run", "-d", "nap", "--", "sleep", "2")

	assert.Equal(t, 143, exitCode(t, err))
	assert.Contains(t, errOut, "loom: job 1 continues; a message will bring its result (loom job wait 1 follows it again)\n")
	select {
	case text := <-carried:
		assert.Contains(t, text, `[loom] job 1 "nap" finished: exit 0`, "the notice the interrupt asked for")
	case <-time.After(30 * time.Second):
		t.Fatal("the job's result never reached the session's Claude")
	}
}

// The paragraph both context files give an agent (in the tail they share)
// tells it how to submit work and which flags claim resources. A flag renamed
// on the commands but not there would send agents to a command that refuses
// it, so every flag the paragraph names must exist on both `submit` and
// `run`. Neither file promises that builds and tests run directly, which the
// paragraph contradicts (spec §8). The paragraph also tells the agent that a
// job's message goes back to the subagent that submitted it, even after it
// has finished (subagent job results spec §9).
func TestJobContext_NamesOnlyFlagsTheCommandsHave(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, session.WriteLoomContextFiles(dir, false))
	flagName := regexp.MustCompile(`--[a-z]+`)

	for _, name := range []string{"claude-loom-context.md", "claude-loom-context-workspace.md"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		text := string(b)
		start := strings.Index(text, "**Heavy work goes through loom's work queue.**")
		require.GreaterOrEqual(t, start, 0, "%s carries the paragraph", name)
		para, _, _ := strings.Cut(text[start:], "\n")
		assert.NotContains(t, text, "running builds and tests", name)

		assert.Contains(t, para, `loom job submit -d "<what>" -- <cmd>`, name)
		assert.Contains(t, para, `loom job run -d "<what>" -- <cmd>`, name)
		assert.Contains(t, para, "subagent", name)
		assert.Contains(t, para, "even after it has finished", name)
		flags := flagName.FindAllString(para, -1)
		assert.ElementsMatch(t, []string{"--cpus", "--mem", "--io", "--exclusive", "--after"}, flags, name)
		for _, flag := range flags {
			for _, command := range []*cobra.Command{newJobSubmitCmd(), newJobRunCmd()} {
				assert.NotNil(t, command.Flags().Lookup(strings.TrimPrefix(flag, "--")), "%s: loom job %s has no %s", name, command.Name(), flag)
			}
		}
	}
}

// submit's last line says a job that has already ended ended, not that it
// started: a start that failed or a cancel can land before the command
// returns.
func TestJobPrintPlacement_SaysHowAnEndedJobEnded(t *testing.T) {
	d := &client{}
	for _, tc := range []struct {
		name string
		view core.JobView
		want string
	}{
		{"running", core.JobView{ID: 4, State: sched.Running}, "job 4 started\n"},
		{"done", core.JobView{ID: 4, State: sched.Done, Exit: &sched.Exit{}}, "job 4 done\n"},
		{"cancelled", core.JobView{ID: 4, State: sched.Cancelled}, "job 4 cancelled\n"},
		{"lost", core.JobView{ID: 4, State: sched.Lost}, "job 4 lost\n"},
		{"failed", core.JobView{ID: 4, State: sched.Failed, Exit: &sched.Exit{Code: 3}}, "job 4 failed (exit 3)\n"},
		{"failed to start", core.JobView{ID: 4, State: sched.Failed, Failure: "its folder is gone"}, "job 4 failed (not started)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			d.printPlacement(&out, tc.view)
			assert.Equal(t, tc.want, out.String())
		})
	}
}

func TestJobList_ShowsUsualDurationsOverdueMarksAndForecasts(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 1, State: sched.Running, Description: "tests", Claim: sched.Claim{CPUs: 4, MemoryGB: 2}, Started: now.Add(-9 * time.Minute), Usual: 2 * time.Minute, OverdueAfter: 7 * time.Minute},
		{ID: 2, State: sched.Queued, Description: "build", Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Submitted: now, StartsAt: now.Add(3 * time.Minute), Waiting: "needs 4 cores (0 free)"},
	}}
	var out bytes.Buffer
	require.NoError(t, writeJobList(&out, q, false, now))
	assert.Regexp(t, `(?m)^1 +running +tests +- +4c/2G +9m0s +~2m overdue *$`, out.String())
	assert.Regexp(t, `(?m)^2 +queued +build +- +4c/6G +0s +- +starts ~3m · needs 4 cores \(0 free\)$`, out.String())
}

func TestOwnerText_NamesTheSubagent(t *testing.T) {
	assert.Equal(t, "-", ownerText(core.JobView{}))
	assert.Equal(t, "lto build", ownerText(core.JobView{Owner: 3, OwnerTitle: "lto build"}))
	assert.Equal(t, "lto build › ab91bd4", ownerText(core.JobView{Owner: 3, OwnerTitle: "lto build", Agent: "ab91bd4b710780552"}))
	assert.Equal(t, "#3 › ab91bd4", ownerText(core.JobView{Owner: 3, Agent: "ab91bd4b710780552"}))
}

// submit says where a job's result will go, so a subagent knows whether it
// may end its turn (subagent job results spec §5.5).
func TestResultRoute(t *testing.T) {
	served := sched.Submit{Launch: "L1", Notify: true}
	assert.Equal(t, "its result comes back to this subagent (ab91bd4)", resultRoute(served, core.JobView{ID: 1, Owner: 3, Agent: "ab91bd4b710780552"}))
	assert.Equal(t, "its result comes to this session", resultRoute(served, core.JobView{ID: 1, Owner: 3}))
	assert.Equal(t, "", resultRoute(served, core.JobView{ID: 1}), "an unknown launch: the daemon's warning says so")
	assert.Equal(t, "", resultRoute(sched.Submit{Launch: "L1"}, core.JobView{ID: 1, Owner: 3}), "--no-notify")
	assert.Equal(t, "no session to notify: follow it with loom job wait 1", resultRoute(sched.Submit{Notify: true}, core.JobView{ID: 1}))
	assert.Equal(t, "no session to notify: follow it with loom job wait 1", resultRoute(sched.Submit{}, core.JobView{ID: 1}), "a plain shell, as before")
}

func TestContinuesLine_NamesTheSubagent(t *testing.T) {
	assert.Equal(t, "job 4 continues; a message will bring its result (loom job wait 4 follows it again)", continuesLine(core.JobView{ID: 4}))
	assert.Equal(t, "job 4 continues; a message will bring its result to this subagent (ab91bd4) (loom job wait 4 follows it again)", continuesLine(core.JobView{ID: 4, Agent: "ab91bd4b710780552"}))
}

func TestJobSubmission_TheAfterFlag(t *testing.T) {
	f, c, args := parsedSubmit(t, "--after", "14", "--", "true")
	sub, err := f.submission(c, args, true, defaultTail)
	require.NoError(t, err)
	assert.Equal(t, sched.JobID(14), sub.After)

	f, c, args = parsedSubmit(t, "--", "true")
	sub, err = f.submission(c, args, true, defaultTail)
	require.NoError(t, err)
	assert.Zero(t, sub.After, "none")

	f, c, args = parsedSubmit(t, "--after", "0", "--", "true")
	_, err = f.submission(c, args, true, defaultTail)
	assert.EqualError(t, err, "--after is 0: it must be a job ID")
}

func TestJobStartedLine_AReleasedJobCountsFromItsPrerequisitesEnd(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	v := core.JobView{ID: 15, After: 14, Submitted: t0, Ready: t0.Add(3 * time.Minute), Started: t0.Add(3*time.Minute + 2*time.Second)}
	assert.Equal(t, "job 15 started 2s after job 14 ended", startedLine(v))
	v.Ready = t0 // queued at once after a job already done
	assert.Equal(t, "job 15 started after 3m2s", startedLine(v))
	v.After, v.Ready = 0, time.Time{} // a record from an earlier build
	assert.Equal(t, "job 15 started after 3m2s", startedLine(v))
}

func TestJobList_ShowsAHeldJobAndWhatItRunsAfter(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 4, State: sched.Running, Description: "build", Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Submitted: now.Add(-5 * time.Minute), Started: now.Add(-3 * time.Minute)},
		{ID: 5, State: sched.Held, Description: "tests", After: 4, Claim: sched.Claim{CPUs: 2, MemoryGB: 2}, Submitted: now.Add(-2 * time.Minute), Waiting: "after job 4 (running)"},
	}}
	var out bytes.Buffer
	require.NoError(t, writeJobList(&out, q, false, now))
	assert.Regexp(t, `(?m)^5 +held +tests +- +2c/2G +2m0s +- +after job 4 \(running\)$`, out.String())
}

func TestJobBeliefCommand_NamesWhatItRunsAfter(t *testing.T) {
	v := sched.BeliefView{Belief: sched.Belief{Signature: sched.Signature{Command: []string{"cargo", "test"}}}}
	assert.Equal(t, "cargo test", beliefCommand(v))
	v.After = []string{"cargo", "test", "--no-run"}
	assert.Equal(t, "cargo test (after cargo test --no-run)", beliefCommand(v))
}

// The whole path through a daemon: a job submitted --after a running one is
// held, and its follower says so, then counts its start from the end of the
// job it waited on.
func TestJobSubmit_AfterHoldsTheJobUntilItsPrerequisiteIsDone(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "build", "--no-notify", "--", "sh", "-c", "echo up; sleep 3")
	require.NoError(t, err)

	out, _, err := runJob(t, "submit", "-d", "tests", "--after", "1", "--", "sh", "-c", "echo ran")
	require.NoError(t, err)
	assert.Regexp(t, `^job 2 held: runs after job 1 \((queued|running)\)\n`, out)

	out, errOut, err := runJob(t, "wait", "2")
	require.NoError(t, err)
	assert.Equal(t, "ran\n", out)
	assert.Regexp(t, `(?s)loom: job 2 held: runs after job 1 \(running\)\n.*loom: job 2 started \d+s after job 1 ended\n.*loom: job 2 done in \S+ \(exit 0\)\n`, errOut)
	d.waitFinished(t, 1)
}

func TestJobRun_AJobWhosePrerequisiteFailsExits125(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "build", "--no-notify", "--", "sh", "-c", "sleep 1; exit 3")
	require.NoError(t, err)

	_, errOut, err := runJob(t, "run", "-d", "tests", "--after", "1", "--", "true")

	assert.Equal(t, 125, exitCode(t, err))
	assert.Contains(t, errOut, "loom: job 2 was cancelled because job 1 failed (exit 3)\n")
}

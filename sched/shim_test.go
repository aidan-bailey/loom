//go:build unix

package sched

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// shimLimit bounds every wait on a real process.
const shimLimit = 10 * time.Second

// shimWait polls cond until it holds, and fails the test after limit.
func shimWait(t *testing.T, what string, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", limit, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// shimSpec is a spec for argv, run from a fresh folder in the environment of
// a shell that has only PATH; later env entries override it. The CPU hint is
// 3 and the job's ID 1.
func shimSpec(t *testing.T, argv []string, env ...string) Spec {
	t.Helper()
	return Spec{
		ID:       1,
		Argv:     argv,
		Cwd:      t.TempDir(),
		Env:      append([]string{"PATH=" + os.Getenv("PATH")}, env...),
		HintCPUs: 3,
	}
}

// writeShimJob writes spec under a fresh jobs dir and returns the job's dir.
func writeShimJob(t *testing.T, spec Spec) string {
	t.Helper()
	dir := JobDir(t.TempDir(), spec.ID)
	require.NoError(t, WriteSpec(dir, spec))
	return dir
}

// shimJob writes the job shimSpec describes and returns its dir.
func shimJob(t *testing.T, argv []string, env ...string) string {
	t.Helper()
	return writeShimJob(t, shimSpec(t, argv, env...))
}

// startShim starts the job's shim the way the daemon does, and returns its
// pid, which is also the job's process group. When the test ends it kills
// what the job left behind, but only a group it can prove is the job's: the
// shim's lock held, or the shim gone with members left in the group and
// nothing holding the shim's pid.
func startShim(t *testing.T, dir string) int {
	t.Helper()
	pid, err := TestRunner().Start(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if alive, err := Probe(dir); err == nil && alive {
			_ = SignalGroup(pid, syscall.SIGKILL)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		snap, err := Sample(ctx)
		if err != nil {
			return
		}
		if _, _, members, leaderAlive := snap.Group(pid); members > 0 && !leaderAlive {
			_ = SignalGroup(pid, syscall.SIGKILL)
		}
	})
	return pid
}

func shimFileExists(path string) func() bool {
	return func() bool {
		_, err := os.Stat(path)
		return err == nil
	}
}

// shimLockFree reports whether nothing holds the job's run.lock. It also holds
// before the shim has created the file.
func shimLockFree(dir string) func() bool {
	return func() bool {
		alive, err := Probe(dir)
		return err == nil && !alive
	}
}

// shimReadyThen is a command that creates the file ready, then runs script. A
// test that signals the job waits for ready first: the shim passes on no
// signal that arrives before it has started the command.
func shimReadyThen(ready, script string) []string {
	return []string{"sh", "-c", `: > "$1"; ` + script, "sh", ready}
}

// awaitShimExit waits for the shim to write exit.json and release its lock, and
// returns what it wrote.
func awaitShimExit(t *testing.T, dir string) Exit {
	t.Helper()
	var exit Exit
	shimWait(t, "exit.json", shimLimit, func() bool {
		var err error
		exit, err = ReadExit(dir)
		return err == nil
	})
	shimWait(t, "the shim to release run.lock", shimLimit, shimLockFree(dir))
	return exit
}

// shimLog is everything the shim and the command wrote to output.log.
func shimLog(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, LogFile))
	require.NoError(t, err)
	return string(data)
}

// shimOutput is the last line in output.log. A shim that could not lower its
// priority writes a warning before the command's lines.
func shimOutput(t *testing.T, dir string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(shimLog(t, dir), "\n"), "\n")
	return lines[len(lines)-1]
}

func TestRunShim_ReportsTheCommandsExit(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantCode   int
		wantSignal string
	}{
		{"success", "exit 0", 0, ""},
		{"a failing exit code", "exit 7", 7, ""},
		{"an exit code that looks like a signal's", "exit 137", 137, ""},
		{"killed by a signal", "kill -KILL $$", -1, "SIGKILL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now()
			dir := shimJob(t, []string{"sh", "-c", tc.script})
			startShim(t, dir)

			exit := awaitShimExit(t, dir)

			assert.Equal(t, tc.wantCode, exit.Code)
			assert.Equal(t, tc.wantSignal, exit.Signal)
			assert.Empty(t, exit.Error)
			assert.False(t, exit.Ended.Before(before), "exit.json is stamped when the command ended")
			assert.Greater(t, exit.MaxRSSKB, int64(0), "the command's peak memory comes from rusage")
		})
	}
}

func TestRunShim_HoldsTheLockWhileTheCommandRuns(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	dir := shimJob(t, shimReadyThen(ready, "exec sleep 30"))
	pid := startShim(t, dir)
	shimWait(t, "the command to run", shimLimit, shimFileExists(ready))

	started, err := ReadStarted(dir)
	require.NoError(t, err)
	assert.Equal(t, pid, started.PID, "started.json names the shim, which leads the job's group")
	assert.False(t, started.Started.IsZero())
	alive, err := Probe(dir)
	require.NoError(t, err)
	assert.True(t, alive, "the shim holds run.lock while the command runs")
	_, err = ReadExit(dir)
	assert.ErrorIs(t, err, fs.ErrNotExist, "there is no result until the command ends")

	require.NoError(t, SignalGroup(pid, syscall.SIGTERM))
	exit := awaitShimExit(t, dir)

	assert.Equal(t, -1, exit.Code)
	assert.Equal(t, "SIGTERM", exit.Signal)
	alive, err = Probe(dir)
	require.NoError(t, err)
	assert.False(t, alive, "the lock is free once the shim has reported")
}

// grandchildScript starts a child that traps SIGTERM, noting it in marker,
// and waits for it. Its loop ends by itself, so a test that fails leaves no
// process behind for long.
const grandchildScript = `marker=$1
ready=$2
(
	trap 'echo term > "$marker"; exit 0' TERM
	: > "$ready"
	i=0
	while [ "$i" -lt 400 ]; do sleep 0.05; i=$((i + 1)); done
) &
wait
`

func TestRunShim_ACancelReachesTheCommandAndItsChildrenThroughTheGroup(t *testing.T) {
	scratch := t.TempDir()
	marker, ready := filepath.Join(scratch, "marker"), filepath.Join(scratch, "ready")
	dir := shimJob(t, []string{"sh", "-c", grandchildScript, "sh", marker, ready})
	pid := startShim(t, dir)
	shimWait(t, "the grandchild to run", shimLimit, shimFileExists(ready))

	require.NoError(t, SignalGroup(pid, syscall.SIGTERM))
	exit := awaitShimExit(t, dir)

	assert.Equal(t, -1, exit.Code, "the command died of the signal, and the shim stayed up to say so")
	assert.Equal(t, "SIGTERM", exit.Signal)
	shimWait(t, "the grandchild to see the signal", shimLimit, shimFileExists(marker))
}

func TestRunShim_AKilledGroupLeavesNoResultAndFreesTheLock(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	dir := shimJob(t, shimReadyThen(ready, "exec sleep 30"))
	pid := startShim(t, dir)
	shimWait(t, "the command to run", shimLimit, shimFileExists(ready))

	require.NoError(t, SignalGroup(pid, syscall.SIGKILL))
	shimWait(t, "the lock to be free", shimLimit, shimLockFree(dir))

	_, err := ReadExit(dir)
	assert.ErrorIs(t, err, fs.ErrNotExist, "a shim that was killed reports nothing")
}

func TestRunShim_ALeftoverChildDoesNotKeepTheLock(t *testing.T) {
	dir := shimJob(t, []string{"sh", "-c", "sleep 6 & exit 0"})
	startShim(t, dir)
	shimWait(t, "exit.json", shimLimit, func() bool {
		_, err := ReadExit(dir)
		return err == nil
	})

	// The command is gone and its child sleeps on. Had the child inherited
	// run.lock, the lock would stay held until the child ended.
	shimWait(t, "the lock to be free while the child sleeps", 3*time.Second, shimLockFree(dir))
}

func TestRunShim_SetsTheHintsOnlyWhereTheSubmitterDidNot(t *testing.T) {
	script := `echo "$CARGO_BUILD_JOBS $RUST_TEST_THREADS $MAKEFLAGS $LOOM_JOB"`
	dir := shimJob(t, []string{"sh", "-c", script}, "CARGO_BUILD_JOBS=99")
	startShim(t, dir)

	awaitShimExit(t, dir)

	assert.Equal(t, "99 3 -j3 1", shimOutput(t, dir), "the submitter's 99 stays; the others are the spec's 3 cores")
}

func TestRunShim_TheJobSeesOnlyTheSubmittersEnvironment(t *testing.T) {
	t.Setenv("LOOM_SCHED_TEST_LEAK", "from the daemon")
	shimEnv := TestRunner().shimEnv()
	require.Contains(t, shimEnv, "LOOM_SCHED_TEST_LEAK=from the daemon", "the shim itself has the daemon's environment")
	require.Contains(t, shimEnv, ShimTestEnv+"=1", "and the runner's additions")
	script := `echo "${LOOM_SCHED_TEST_LEAK-unset} ${LOOM_SCHED_TEST_SHIM-unset} ${FROM_SPEC-unset}"`
	dir := shimJob(t, []string{"sh", "-c", script}, "FROM_SPEC=yes")
	startShim(t, dir)

	awaitShimExit(t, dir)

	assert.Equal(t, "unset unset yes", shimOutput(t, dir))
}

func TestRunShim_FindsTheCommandOnTheSubmittersPath(t *testing.T) {
	bin := t.TempDir()
	tool := filepath.Join(bin, "loom-sched-test-tool")
	require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\necho ran from the job PATH\n"), 0o700))
	_, err := exec.LookPath("loom-sched-test-tool")
	require.Error(t, err, "the shim's own PATH must not find it")
	dir := shimJob(t, []string{"loom-sched-test-tool"}, "PATH="+bin)
	startShim(t, dir)

	exit := awaitShimExit(t, dir)

	assert.Equal(t, 0, exit.Code, "exit.Error is %q", exit.Error)
	assert.Equal(t, "ran from the job PATH", shimOutput(t, dir))
}

func TestRunShim_ReportsACommandThatCannotRun(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		cwd  string
		want string
	}{
		{"the command is on no PATH", []string{"loom-sched-no-such-command"}, "", "not found"},
		{"the folder is gone", []string{"true"}, filepath.Join(t.TempDir(), "gone"), "no such file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := shimSpec(t, tc.argv)
			if tc.cwd != "" {
				spec.Cwd = tc.cwd
			}
			dir := writeShimJob(t, spec)
			startShim(t, dir)

			exit := awaitShimExit(t, dir)

			assert.Equal(t, 127, exit.Code)
			assert.Empty(t, exit.Signal)
			assert.Contains(t, exit.Error, tc.want)
			_, err := ReadStarted(dir)
			assert.NoError(t, err, "the shim was up, so it says it started")
		})
	}
}

func TestRunShim_LowersTheCommandsPriority(t *testing.T) {
	requirePS(t)
	dir := shimJob(t, []string{"sh", "-c", "ps -o ni= -p $$"})
	startShim(t, dir)

	awaitShimExit(t, dir)

	nice, err := strconv.Atoi(strings.TrimSpace(shimOutput(t, dir)))
	require.NoError(t, err, "ps printed %q", shimOutput(t, dir))
	assert.GreaterOrEqual(t, nice, niceValue, "the command inherits the shim's nice value")
}

func TestRunShim_SetsTheCommandsIOClass(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ioprio_set is Linux's")
	}
	if _, err := exec.LookPath("ionice"); err != nil {
		t.Skip("ionice is not installed")
	}
	dir := shimJob(t, []string{"sh", "-c", "ionice -p $$"})
	startShim(t, dir)

	awaitShimExit(t, dir)

	assert.Contains(t, shimLog(t, dir), "best-effort: prio 7")
}

func TestRunShim_RefusesToRunWhenItLeadsNoGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	dir := shimJob(t, []string{"sh", "-c", `: > "$1"`, "sh", marker})
	exe, err := os.Executable()
	require.NoError(t, err)

	// Started without Setsid, the shim belongs to this test's process group.
	cmd := exec.Command(exe, "job-shim", dir)
	cmd.Env = TestRunner().shimEnv()
	err = cmd.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.NoFileExists(t, marker, "the command must not run")
	_, err = ReadStarted(dir)
	assert.ErrorIs(t, err, fs.ErrNotExist)
	exit, err := ReadExit(dir)
	require.NoError(t, err)
	assert.Equal(t, 127, exit.Code)
	assert.Contains(t, exit.Error, "process group")
}

func TestResolveCommand(t *testing.T) {
	cwd := t.TempDir()
	first, second, rel := filepath.Join(cwd, "first"), filepath.Join(cwd, "second"), filepath.Join(cwd, "rel")
	for _, d := range []string{first, second, rel, filepath.Join(first, "dirtool")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	write := func(path string, mode os.FileMode) {
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700))
		require.NoError(t, os.Chmod(path, mode))
	}
	write(filepath.Join(first, "tool"), 0o755)
	write(filepath.Join(second, "tool"), 0o755)
	write(filepath.Join(first, "nonexec"), 0o644)
	write(filepath.Join(second, "nonexec"), 0o755)
	write(filepath.Join(second, "dirtool"), 0o755)
	write(filepath.Join(rel, "reltool"), 0o755)
	write(filepath.Join(cwd, "cwdtool"), 0o755)
	both := "PATH=" + first + ":" + second

	tests := []struct {
		name  string
		env   []string
		argv0 string
		want  string // "" means not found
	}{
		{"the first directory with an executable wins", []string{both}, "tool", filepath.Join(first, "tool")},
		{"a file without an exec bit is skipped", []string{both}, "nonexec", filepath.Join(second, "nonexec")},
		{"a directory of that name is skipped", []string{both}, "dirtool", filepath.Join(second, "dirtool")},
		{"a relative PATH entry is the folder's", []string{"PATH=rel"}, "reltool", filepath.Join(rel, "reltool")},
		{"an empty PATH entry is the folder itself", []string{"PATH=:"}, "cwdtool", filepath.Join(cwd, "cwdtool")},
		{"the last PATH of the environment wins", []string{"PATH=" + first, "PATH=" + second}, "nonexec", filepath.Join(second, "nonexec")},
		{"a name with a slash is taken as it is", []string{"PATH="}, "./cwdtool", "./cwdtool"},
		{"a name on no PATH is not found", []string{both}, "nothing-like-it", ""},
		{"an environment without PATH finds nothing, even what the shim's PATH holds", nil, "sh", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCommand(tc.argv0, tc.env, cwd)
			if tc.want == "" {
				require.ErrorIs(t, err, exec.ErrNotFound)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestProbe_ReadsTheLockAndLeavesItFree(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, LockFile)

	alive, err := Probe(dir)
	require.NoError(t, err)
	assert.False(t, alive, "no run.lock: no shim has taken it")

	held, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	require.NoError(t, syscall.Flock(int(held.Fd()), syscall.LOCK_EX))
	alive, err = Probe(dir)
	require.NoError(t, err)
	assert.True(t, alive, "a lock held elsewhere reads as a living shim")

	require.NoError(t, held.Close())
	alive, err = Probe(dir)
	require.NoError(t, err)
	assert.False(t, alive, "a released lock reads as a shim that is gone")

	again, err := os.Open(lockPath)
	require.NoError(t, err)
	defer again.Close()
	assert.NoError(t, syscall.Flock(int(again.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "Probe must not keep the lock it took")
}

func TestSignalGroup_RefusesAGroupItCouldNotMean(t *testing.T) {
	for _, pgid := range []int{-5, 0, 1, syscall.Getpgrp()} {
		err := SignalGroup(pgid, 0)
		assert.Error(t, err, "pgid %d", pgid)
	}
}

func TestSignalGroup_ReportsAGroupThatIsGone(t *testing.T) {
	dir := shimJob(t, []string{"true"})
	pid := startShim(t, dir)
	awaitShimExit(t, dir)

	// Until Start's goroutine has reaped the shim, its zombie still belongs
	// to the group.
	shimWait(t, "the group to be gone", shimLimit, func() bool {
		return errors.Is(SignalGroup(pid, 0), syscall.ESRCH)
	})
}

func TestNewRunner_UsesThisExecutable(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)

	r, err := NewRunner()

	require.NoError(t, err)
	assert.Equal(t, []string{exe, "job-shim"}, r.Shim)
	assert.Empty(t, r.Env)
}

func TestRunnerStart_AppendsToTheLog(t *testing.T) {
	dir := shimJob(t, []string{"sh", "-c", "echo out; echo err >&2"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogFile), []byte("earlier\n"), 0o600))
	startShim(t, dir)

	awaitShimExit(t, dir)

	log := shimLog(t, dir)
	assert.True(t, strings.HasPrefix(log, "earlier\n"), "the log is appended to, not truncated: %q", log)
	assert.True(t, strings.HasSuffix(log, "out\nerr\n"), "stdout and stderr both land in it: %q", log)
}

func TestRunnerStart_GivesTheShimNoStdinAndTheLogForOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/<pid>/fd")
	}
	ready := filepath.Join(t.TempDir(), "ready")
	dir := shimJob(t, shimReadyThen(ready, "exec sleep 30"))
	pid := startShim(t, dir)
	shimWait(t, "the command to run", shimLimit, shimFileExists(ready))
	logPath, err := filepath.EvalSymlinks(filepath.Join(dir, LogFile))
	require.NoError(t, err)

	for fd, want := range map[int]string{0: os.DevNull, 1: logPath, 2: logPath} {
		got, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%d", pid, fd))
		require.NoError(t, err)
		assert.Equal(t, want, got, "fd %d of the shim", fd)
	}

	require.NoError(t, SignalGroup(pid, syscall.SIGTERM))
	awaitShimExit(t, dir)
}

func TestRunnerStart_RefusesWhatItCannotStart(t *testing.T) {
	dir := shimJob(t, []string{"true"})
	tests := []struct {
		name   string
		runner Runner
		dir    string
	}{
		{"no shim command", Runner{}, dir},
		{"a shim that does not exist", Runner{Shim: []string{filepath.Join(t.TempDir(), "no-such-loom")}}, dir},
		{"a job dir that does not exist", TestRunner(), filepath.Join(t.TempDir(), "gone")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pid, err := tc.runner.Start(tc.dir)
			assert.Error(t, err)
			assert.Zero(t, pid)
		})
	}
}

func TestSignalName(t *testing.T) {
	tests := []struct {
		sig  syscall.Signal
		want string
	}{
		{syscall.SIGKILL, "SIGKILL"},
		{syscall.SIGTERM, "SIGTERM"},
		{syscall.SIGSEGV, "SIGSEGV"},
		{syscall.SIGHUP, "SIGHUP"},
		{syscall.Signal(63), "SIG63"},
	}
	for _, tc := range tests {
		got := signalName(tc.sig)
		assert.Equal(t, tc.want, got)
		if unix.SignalName(tc.sig) != "" {
			assert.Equal(t, tc.sig, unix.SignalNum(got), "the follower maps the name back to the signal")
		}
	}
}

func TestSample_SeesAJobsWholeGroup(t *testing.T) {
	requirePS(t)
	ready := filepath.Join(t.TempDir(), "ready")
	dir := shimJob(t, []string{"sh", "-c", `sleep 30 & : > "$1"; wait`, "sh", ready})
	pid := startShim(t, dir)
	shimWait(t, "the command to run", shimLimit, shimFileExists(ready))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	snap, err := Sample(ctx)
	require.NoError(t, err)

	rss, cpu, members, leaderAlive := snap.Group(pid)
	assert.GreaterOrEqual(t, members, 3, "the shim, the command's shell and its sleep")
	assert.True(t, leaderAlive, "the shim leads the group")
	assert.Greater(t, rss, int64(0))
	assert.GreaterOrEqual(t, cpu, time.Duration(0))

	require.NoError(t, SignalGroup(pid, syscall.SIGTERM))
	awaitShimExit(t, dir)
}

//go:build unix

package sched

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// niceValue is the nice value the shim, and everything it starts, runs at.
const niceValue = 10

// Start starts a shim for the job in jobDir: this program's "job-shim", as a
// child in a session of its own (so it leads a process group whose id is its
// pid, the job's), with stdin on /dev/null and stdout and stderr appended to
// output.log. A goroutine reaps it. The shim's environment is this
// process's plus r.Env. Start returns once the child exists: the shim says
// it is up by writing started.json.
func (r Runner) Start(jobDir string) (pid int, err error) {
	if len(r.Shim) == 0 {
		return 0, errors.New("no job-shim command to start")
	}
	logFile, err := os.OpenFile(filepath.Join(jobDir, LogFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return 0, fmt.Errorf("opening the job's log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(r.Shim[0], append(slices.Clone(r.Shim[1:]), jobDir)...)
	cmd.Env = r.shimEnv()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting the job runner: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

// Probe tests whether the job's shim lives, by trying to take run.lock
// without waiting and letting go of it at once. The shim holds the lock from
// before it writes started.json until after it writes exit.json, and the
// kernel drops it if the shim dies, so a held lock means a living shim and a
// free one a shim that is gone. A run.lock that does not exist yet reads as
// free: no shim has taken it. Probe does not prove that a pid recorded for
// the job is still the shim's; the lock does, which is why callers signal a
// job's group only after finding it held.
func Probe(jobDir string) (alive bool, err error) {
	f, err := os.Open(filepath.Join(jobDir, LockFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("opening the job's lock: %w", err)
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	switch {
	case err == nil:
		return false, nil // closing f releases the lock again
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true, nil
	default:
		return false, fmt.Errorf("testing the job's lock: %w", err)
	}
}

// SignalGroup sends sig to every process of process group pgid. It refuses a
// pgid of 1 or below and this process's own group, which kill(2) would read
// as "my group" (0) or "every process" (-1) or which is the daemon's own. An
// ESRCH error means the group is gone. SignalGroup cannot tell the job's
// group from one that a reused pid now leads: the caller proves the group is
// the job's first (see Probe and Snapshot.Group).
func SignalGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 1 {
		return fmt.Errorf("refusing to signal process group %d: it is no job's", pgid)
	}
	if pgid == syscall.Getpgrp() {
		return fmt.Errorf("refusing to signal process group %d: it is this process's own", pgid)
	}
	return syscall.Kill(-pgid, sig)
}

// RunShim is the shim's whole life (spec §5.3). It runs the job in jobDir and
// writes how it ended, and returns the shim's own exit code: 0 once exit.json
// is written, including for a command that could not be executed (code 127 in
// it), and 1 when the shim gave up before running anything or could not write
// exit.json. A shim that finds started.json already there is a duplicate for
// a job that has run: it returns 1 and writes nothing. It prints to its
// stderr, which is output.log, and never to the job's.
//
// The shim must lead its own process group, which Runner.Start arranges: the
// daemon signals the group -pid as the job's. It holds an exclusive flock on
// run.lock from before it writes started.json to after it writes exit.json.
func RunShim(jobDir string) int {
	// Linux keeps the nice value and the I/O class per thread, and a forked
	// child inherits its forking thread's. Pin this goroutine to its thread,
	// set both on it, and start the command from it.
	runtime.LockOSThread()

	// Catching these keeps the shim up when a signal sent to the group
	// reaches it along with the command, which that same send signals; the
	// shim stays to report. A caught signal is back at its default in the
	// command, an ignored one would stay ignored across exec.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if syscall.Getpgrp() != os.Getpid() {
		return shimGiveUp(jobDir, errors.New("the shim must lead its own process group: the daemon starts it with Setsid"))
	}
	spec, err := ReadSpec(jobDir)
	if err != nil {
		return shimGiveUp(jobDir, fmt.Errorf("reading spec.json: %w", err))
	}
	if len(spec.Argv) == 0 {
		return shimGiveUp(jobDir, errors.New("spec.json has no command"))
	}
	if err := lowerPriority(); err != nil {
		// Not fatal: a job at the default priority is still a job.
		fmt.Fprintf(os.Stderr, "loom job-shim: lowering the priority: %v\n", err)
	}

	lock, err := os.OpenFile(filepath.Join(jobDir, LockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return shimGiveUp(jobDir, fmt.Errorf("opening run.lock: %w", err))
	}
	// Go opens the file close-on-exec, so the command never holds the lock.
	// The deferred Close lets it go after exit.json is written, and keeps
	// the file reachable until then, since a finalizer would drop the lock.
	defer lock.Close()
	if err := lockExclusive(lock); err != nil {
		return shimGiveUp(jobDir, fmt.Errorf("locking run.lock: %w", err))
	}
	if _, err := os.Stat(filepath.Join(jobDir, StartedFile)); err == nil {
		// Another shim has been through here for this job (it waited for the
		// lock behind it). Running the command again would run the job twice.
		return 1
	}
	if err := writeStarted(jobDir, Started{PID: os.Getpid(), Started: time.Now()}); err != nil {
		return shimGiveUp(jobDir, fmt.Errorf("writing started.json: %w", err))
	}

	// The job's environment is the spec's and the hints: nothing of this
	// process's own, which is the daemon's. Hints returns a new slice of
	// spec.Env, the hint variables it lacks and LOOM_JOB, and the command
	// runs with exactly that.
	env := Hints(spec.Env, max(spec.HintCPUs, 1), spec.ID)
	// A SIGTERM that reached this shim before the command starts is not
	// checked for: Go delivers signals asynchronously, so no read of sigs
	// here is reliable. The command starts and the daemon's SIGKILL, 10 s
	// after the cancel, ends it, and the job settles as cancelled (spec §5.5).
	before, measured := readIOCounters()
	cmd, err := startCommand(spec, env)
	if err != nil {
		return shimReport(jobDir, Exit{Code: 127, Ended: time.Now(), Error: err.Error()})
	}
	waitErr := cmd.Wait()
	exit := exitOf(cmd.ProcessState, waitErr)
	if measured {
		// The command and the descendants it reaped are in the shim's own
		// counters now (smart scheduler spec §5.2).
		if after, ok := readIOCounters(); ok {
			exit.ReadBytes, exit.WriteBytes = ioDelta(before, after)
		}
	}
	return shimReport(jobDir, exit)
}

// lockExclusive takes an exclusive flock on f, waiting for it: the daemon's
// Probe holds the lock for an instant, and the shim must outlast that.
func lockExclusive(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// shimReport writes the job's result and returns the shim's exit code.
func shimReport(jobDir string, e Exit) int {
	if err := writeExit(jobDir, e); err != nil {
		fmt.Fprintf(os.Stderr, "loom job-shim: writing exit.json: %v\n", err)
		return 1
	}
	return 0
}

// shimGiveUp reports a job the shim could not run, as the command that
// could not be executed (code 127) with cause as the reason, and returns 1.
func shimGiveUp(jobDir string, cause error) int {
	fmt.Fprintf(os.Stderr, "loom job-shim: %v\n", cause)
	if err := writeExit(jobDir, Exit{Code: 127, Ended: time.Now(), Error: cause.Error()}); err != nil {
		fmt.Fprintf(os.Stderr, "loom job-shim: writing exit.json: %v\n", err)
	}
	return 1
}

// startCommand starts spec's argv in its folder with env as its whole
// environment, no shell and stdin on /dev/null. The shim's stdout and
// stderr, which are output.log, are the command's.
func startCommand(spec Spec, env []string) (*exec.Cmd, error) {
	path, err := resolveCommand(spec.Argv[0], env, spec.Cwd)
	if err != nil {
		return nil, err
	}
	cmd := &exec.Cmd{
		Path:   path,
		Args:   spec.Argv,
		Dir:    spec.Cwd,
		Env:    env,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// exitOf describes how the command ended. Code is the exit code, or -1 and
// the signal's name when a signal ended it; MaxRSSKB is the command's, and
// its waited-for descendants', peak memory from rusage, and CPUSeconds their
// user and system time.
func exitOf(ps *os.ProcessState, waitErr error) Exit {
	if ps == nil {
		return Exit{Code: 127, Ended: time.Now(), Error: fmt.Sprintf("waiting for the command: %v", waitErr)}
	}
	exit := Exit{Code: ps.ExitCode(), Ended: time.Now()} // -1 when a signal ended it
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		exit.Signal = signalName(ws.Signal())
	}
	if ru, ok := ps.SysUsage().(*syscall.Rusage); ok && ru != nil {
		exit.MaxRSSKB = maxRSSKB(ru)
	}
	exit.CPUSeconds = (ps.UserTime() + ps.SystemTime()).Seconds()
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		exit.Error = waitErr.Error()
	}
	return exit
}

// maxRSSKB is rusage's peak resident size in kilobytes: Linux reports
// kilobytes, macOS bytes.
func maxRSSKB(ru *syscall.Rusage) int64 {
	n := int64(ru.Maxrss)
	if runtime.GOOS == "darwin" {
		return n / 1024
	}
	return n
}

// signalName is unix.SignalName's name for sig ("SIGKILL"), or "SIG<n>" for
// one it has no name for, so that a signal's end is never recorded as none.
// syscall.Signal.String gives prose ("killed").
func signalName(sig syscall.Signal) string {
	if name := unix.SignalName(sig); name != "" {
		return name
	}
	return fmt.Sprintf("SIG%d", int(sig))
}

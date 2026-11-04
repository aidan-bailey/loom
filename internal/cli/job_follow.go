package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/spf13/cobra"
)

// logPoll is how often a follower reads output.log for new output.
var logPoll = 200 * time.Millisecond

// notifyReq names the interrupted follower's NotifyJob request, apart from
// the SubmitJob request (sessionReq) the same command made before it.
const notifyReq core.ReqID = 2

// notifyWait bounds an interrupted follower's wait for the daemon to answer
// its request for a message.
var notifyWait = 2 * time.Second

// notifySignals delivers the signals that interrupt a follower (SIGINT,
// SIGTERM and SIGHUP, as a Bash timeout sends them) on the channel it
// returns, until the stop function it returns is called. It registers with
// signal.Notify and releases with signal.Stop, never signal.Ignore: an
// ignored signal would survive into a process started later. Tests replace
// it with a channel of their own.
var notifySignals = func() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}

// interruptible returns a context that the first interrupt cancels, the
// function that names that signal, and the stop that releases the handler.
// The context ends the follower's waits (client.ctx); the daemon's work is
// never touched by it.
func interruptible(parent context.Context) (ctx context.Context, signalled func() os.Signal, stop func()) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	sigs, release := notifySignals()
	var (
		mu  sync.Mutex
		got os.Signal
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case s := <-sigs:
			mu.Lock()
			got = s
			mu.Unlock()
			cancel()
		case <-ctx.Done():
		}
	}()
	signalled = func() os.Signal {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
	stop = func() {
		release()
		cancel()
		<-done
	}
	return ctx, signalled, stop
}

// interruptCode is the exit code of a command ended by sig, as a shell
// reports it: 128 plus the signal's number (130 for SIGINT, 143 for SIGTERM,
// 129 for SIGHUP). An interrupt no signal brought counts as SIGINT.
func interruptCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 128 + int(syscall.SIGINT)
}

// jobOutcome is the line a follower writes when job v has ended, and the
// exit code that follows (spec §6.3): the job's own exit code, 128 plus the
// signal that killed it, or exitNotRun for a job that was lost, could not
// start or was cancelled.
func jobOutcome(v core.JobView) (line string, code int) {
	took := rounded(v.Ended.Sub(v.Started))
	switch v.State {
	case sched.Done:
		return fmt.Sprintf("job %d done in %s (exit 0)", v.ID, took), 0
	case sched.Cancelled:
		return strings.TrimSpace(fmt.Sprintf("job %d was cancelled %s", v.ID, v.CancelReason)), exitNotRun
	case sched.Lost:
		return fmt.Sprintf("job %d was lost: %s", v.ID, v.Failure), exitNotRun
	case sched.Failed:
		sig, exit, ran := ranAs(v)
		switch {
		case ran && sig != "":
			return fmt.Sprintf("job %d was killed by %s after %s", v.ID, sig, took), signalExit(sig)
		case ran:
			return fmt.Sprintf("job %d failed in %s (exit %d)", v.ID, took, exit), exit
		}
		return fmt.Sprintf("job %d could not start: %s", v.ID, startFailure(v)), exitNotRun
	}
	return fmt.Sprintf("job %d is %s", v.ID, v.State), 1
}

// startFailure is why a job that never ran failed: the daemon's reason, else
// the exec error the runner recorded.
func startFailure(v core.JobView) string {
	switch {
	case v.Failure != "":
		return v.Failure
	case v.Exit != nil && v.Exit.Error != "":
		return v.Exit.Error
	}
	return "no reason was recorded"
}

// say writes one of a command's progress lines to stderr.
func (d *client) say(format string, a ...any) {
	fmt.Fprintf(d.errOut, "loom: "+format+"\n", a...)
}

// jobFollower follows one job: it copies the job's output to out as the log
// grows and, unless quiet, says on stderr where the job stands in the queue
// and when it starts.
type jobFollower struct {
	d       *client
	id      sched.JobID
	out     io.Writer
	off     int64
	quiet   bool
	queued  bool
	started bool
	last    string
}

// noJobError is the error of a follower whose job the daemon does not hold.
type noJobError struct{ id sched.JobID }

func (e *noJobError) Error() string { return fmt.Sprintf("there is no job %d", e.id) }

// lostDaemonError is the error of a follower that lost the daemon: the job
// goes on without it.
type lostDaemonError struct {
	id  sched.JobID
	err error
}

func (e *lostDaemonError) Error() string {
	return fmt.Sprintf("job %d continues (loom job wait %d follows it again): lost the loom daemon: %v", e.id, e.id, e.err)
}

func (e *lostDaemonError) Unwrap() error { return e.err }

// run follows the job until it has ended (its last view, with the log copied
// whole) or the command's context ends (that context's error, with the last
// view). A job the daemon does not hold is a *noJobError, and a lost daemon a
// *lostDaemonError: the job goes on without it.
func (f *jobFollower) run() (core.JobView, error) {
	tick := time.NewTicker(logPoll)
	defer tick.Stop()
	for {
		f.d.events()
		v, ok := jobOf(f.d.Queue(), f.id)
		if !ok {
			return v, &noJobError{id: f.id}
		}
		// The view comes first, so a job seen ended has a complete log.
		if err := f.copyLog(v.LogPath); err != nil {
			return v, err
		}
		f.report(v)
		if v.State.Finished() {
			return v, nil
		}
		if err := f.d.Err(); err != nil {
			return v, &lostDaemonError{id: f.id, err: err}
		}
		if err := f.d.ctx.Err(); err != nil {
			return v, err
		}
		select {
		case <-f.d.Wakes():
		case <-tick.C:
		case <-f.d.ctx.Done():
		}
	}
}

// report writes the queue line when the job's place changes, and the start
// line when a job seen waiting starts.
func (f *jobFollower) report(v core.JobView) {
	if f.quiet {
		return
	}
	switch v.State {
	case sched.Queued:
		f.queued = true
		n, running := queuePlace(f.d.Queue(), v.ID)
		if line := fmt.Sprintf("job %d queued (%s in line, %d running)", v.ID, ordinal(n), running); line != f.last {
			f.d.say("%s", line)
			f.last = line
		}
	case sched.Running:
		if f.queued && !f.started {
			f.d.say("job %d started after %s", v.ID, rounded(v.Started.Sub(v.Submitted)))
		}
		f.started = true
	}
}

// copyLog copies what the log at path has gained since the last call to out.
// A log that does not exist yet (a job still queued) has gained nothing.
func (f *jobFollower) copyLog(path string) error {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("read the job's output: %w", err)
	}
	defer file.Close()
	if _, err := file.Seek(f.off, io.SeekStart); err != nil {
		return fmt.Errorf("read the job's output: %w", err)
	}
	n, err := io.Copy(f.out, file)
	f.off += n
	if err != nil {
		return fmt.Errorf("copy the job's output: %w", err)
	}
	return nil
}

// follow follows job id to its end, with its output on out, then writes the
// outcome and returns the exit code it asks for (an *ExitError, or nil for
// exit 0). A job the daemon does not know, or a lost daemon, ends the command
// with exitNotRun and a line on stderr that says which (lostDaemon). An
// interrupt (signalled names it) leaves the job running and ends the command
// (interrupted). queuedHere is true for a job this command
// queued, so its start is reported even when it never waited.
func (d *client) follow(out io.Writer, id sched.JobID, signalled func() os.Signal, queuedHere bool) error {
	f := &jobFollower{d: d, id: id, out: out, queued: queuedHere}
	v, err := f.run()
	var (
		noJob *noJobError
		lost  *lostDaemonError
	)
	switch {
	case errors.As(err, &noJob):
		d.say("%v", noJob)
		return &ExitError{Code: exitNotRun}
	case errors.As(err, &lost):
		return d.lostDaemon(id)
	case err == nil:
		line, code := jobOutcome(v)
		d.say("%s", line)
		if code == 0 {
			return nil
		}
		return &ExitError{Code: code}
	case errors.Is(err, context.Canceled):
		return d.interrupted(id, signalled())
	}
	return err
}

// lostDaemon ends a command that lost the daemon while job id was submitted:
// it says so on stderr, with how to follow the job again, and exits with
// exitNotRun. The job goes on without the daemon.
func (d *client) lostDaemon(id sched.JobID) error {
	d.say("lost the daemon; job %d may still be running (loom job wait %d follows it again)", id, id)
	return &ExitError{Code: exitNotRun}
}

// interrupted ends a follower that sig interrupted. The job is never
// cancelled by it. A job that has an owner and has not ended gets a message
// asked for (the follower's job was submitted without one), and the line says
// so only when the daemon took the request; otherwise it says how to follow
// the job again. The exit code is the signal's.
func (d *client) interrupted(id sched.JobID, sig os.Signal) error {
	d.events() // writes the daemon's pending notices before the outcome line
	v, _ := jobOf(d.Queue(), id)
	switch {
	case v.State.Finished():
		line, _ := jobOutcome(v)
		d.say("%s", line)
	case v.Owner != 0 && d.askForMessage(id):
		d.say("job %d continues; a message will bring its result (loom job wait %d follows it again)", id, id)
	default:
		d.say("job %d continues (loom job wait %d follows it again)", id, id)
	}
	return &ExitError{Code: interruptCode(sig)}
}

// askForMessage sends NotifyJob on a copy of the client whose context is not
// the interrupted one (client.await would give up at once), and reports
// whether the daemon took it. A refusal or no answer is written to errOut.
func (d *client) askForMessage(id sched.JobID) bool {
	n := *d
	n.ctx = context.Background()
	n.NotifyJob(id, notifyReq)
	var reply core.Reply
	got, err := n.await(notifyWait, func(events []core.Event) bool {
		for _, ev := range events {
			if r, ok := ev.(core.Reply); ok && r.Req == notifyReq {
				reply = r
				return true
			}
		}
		return false
	})
	switch {
	case err != nil:
		d.say("no message was asked for: %v", err)
		return false
	case !got:
		d.say("no message was asked for: the daemon did not answer")
		return false
	case reply.Err != nil:
		d.say("no message was asked for: %v", reply.Err)
		return false
	}
	return true
}

func newJobRunCmd() *cobra.Command {
	var f claimFlags
	cmd := &cobra.Command{
		Use:   "run [flags] -- CMD [ARGS…]",
		Short: "Queue a command, follow it, and exit with its exit code",
		Long: "Queues the command and follows it: its output goes to stdout, where it stands in the queue to stderr, " +
			"and the command exits with the job's exit code (128 plus N for a job killed by signal N; 125 for a job that was lost, " +
			"could not start or was cancelled, and when the daemon is lost or does not know the job). No message is sent when it ends. An interrupt (Ctrl-C, a Bash timeout) leaves the job running: " +
			"when it belongs to a session, that session is sent a message with its result.",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			sub, err := f.submission(c, args, false, defaultTail)
			if err != nil {
				return err
			}
			ctx, signalled, stop := interruptible(c.Context())
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				id, err := d.submitJob(sub)
				switch {
				case errors.Is(err, context.Canceled):
					return &ExitError{
						Code: interruptCode(signalled()),
						Msg:  "interrupted before the daemon answered: the job may still be queued (loom job list shows it)",
					}
				case err != nil:
					return err
				}
				if _, err := d.awaitJob(id); err != nil {
					if errors.Is(err, context.Canceled) {
						return d.interrupted(id, signalled())
					}
					if d.Err() != nil {
						return d.lostDaemon(id)
					}
					return fmt.Errorf("job %d was submitted, but %w", id, err)
				}
				return d.follow(c.OutOrStdout(), id, signalled, true)
			})
		},
	}
	f.add(cmd)
	return cmd
}

func newJobWaitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wait ID",
		Short: "Follow a job to its end, and exit with its exit code",
		Long: "Follows a queued, running or finished job as `run` does: its output goes to stdout, its progress to stderr, " +
			"and the exit code is the job's. An interrupt leaves the job running.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			id, err := parseJobID(args[0])
			if err != nil {
				return err
			}
			ctx, signalled, stop := interruptible(c.Context())
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				return d.follow(c.OutOrStdout(), id, signalled, false)
			})
		},
	}
}

func newJobLogsCmd() *cobra.Command {
	var (
		tail   uint
		follow bool
	)
	cmd := &cobra.Command{
		Use:   "logs ID",
		Short: "Print a job's output",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			id, err := parseJobID(args[0])
			if err != nil {
				return err
			}
			lines := -1
			if c.Flags().Changed("tail") {
				lines = int(min(tail, 1<<31-1))
			}
			ctx, signalled, stop := interruptible(c.Context())
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				err := d.logs(c.OutOrStdout(), id, lines, follow)
				if errors.Is(err, context.Canceled) {
					return &ExitError{Code: interruptCode(signalled())}
				}
				return err
			})
		},
	}
	cmd.Flags().UintVar(&tail, "tail", 0, "Print only the last N lines")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep printing until the job ends")
	return cmd
}

// logs prints job id's output (all of it with tail below 0, else the last
// tail lines) and, with follow, goes on printing until the job ends.
func (d *client) logs(out io.Writer, id sched.JobID, tail int, follow bool) error {
	v, ok := jobOf(d.Queue(), id)
	if !ok {
		return fmt.Errorf("there is no job %d", id)
	}
	off, err := tailOffsetOf(v.LogPath, tail)
	if err != nil {
		return err
	}
	f := &jobFollower{d: d, id: id, out: out, off: off, quiet: true}
	if !follow {
		return f.copyLog(v.LogPath)
	}
	_, err = f.run()
	return err
}

// tailOffsetOf is where the last n lines of the file at path begin (see
// tailOffset); a log that does not exist yet begins at 0.
func tailOffsetOf(path string, n int) (int64, error) {
	if n < 0 || path == "" {
		return 0, nil
	}
	file, err := os.Open(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("read the job's output: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("read the job's output: %w", err)
	}
	return tailOffset(file, info.Size(), n)
}

// tailOffset is the offset in r (size bytes long) where its last n lines
// begin: 0 when it has no more than n lines, or when n is negative (all of
// it); size when n is 0. A final newline ends the last line rather than
// starting another. It reads backwards in blocks, so a large log costs only
// its tail.
func tailOffset(r io.ReaderAt, size int64, n int) (int64, error) {
	if n < 0 {
		return 0, nil
	}
	if n == 0 {
		return size, nil
	}
	end := size
	if size > 0 {
		last := make([]byte, 1)
		if _, err := r.ReadAt(last, size-1); err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("read the job's output: %w", err)
		}
		if last[0] == '\n' {
			end--
		}
	}
	buf := make([]byte, 32<<10)
	seen := 0
	for end > 0 {
		step := min(int64(len(buf)), end)
		start := end - step
		if _, err := r.ReadAt(buf[:step], start); err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("read the job's output: %w", err)
		}
		for i := step - 1; i >= 0; i-- {
			if buf[i] != '\n' {
				continue
			}
			seen++
			if seen == n {
				return start + i + 1, nil
			}
		}
		end = start
	}
	return 0, nil
}

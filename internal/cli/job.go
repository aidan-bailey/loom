package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/spf13/cobra"
)

// JobCmd is `loom job`: queue work (a build, a test run, a benchmark) with
// the daemon, follow it, read its output and cancel it. The daemon starts
// each job when its claim fits the host's caps and tells the submitting
// session's Claude when it ends. main attaches it.
var JobCmd = newJobCmd()

// defaultTail is how many lines of output.log a job's result message
// carries unless --tail says otherwise.
const defaultTail = 20

// descriptionLimit is how many characters of the command line a default
// description keeps.
const descriptionLimit = 60

// jobWait bounds the wait for the daemon to report a job it has just
// accepted: the Reply comes first, and the view that holds the job follows.
var jobWait = 5 * time.Second

// newJobCmd builds the `loom job` command tree, with flags of its own: each
// call is independent (tests build one per run).
func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Queue builds, tests and benchmarks against this host's caps",
		Long: "Queue heavy work with the loom daemon, which starts each job when its claim fits what the host's caps leave " +
			"(cores, memory and I/O-heavy jobs) and keeps it running across daemon restarts.\n\n" +
			"Put the command after --. No shell is involved: a pipeline is `-- sh -c '…'`.",
	}
	cmd.AddCommand(newJobSubmitCmd(), newJobRunCmd(), newJobWaitCmd(), newJobLogsCmd(), newJobListCmd(), newJobCancelCmd())
	return cmd
}

// beginJob readies a `loom job` command's run: its errors are loom's, not
// usage mistakes, and the platform has a work queue.
func beginJob(c *cobra.Command) error {
	loomErrors(c)
	return checkPlatform(runtime.GOOS)
}

// checkPlatform refuses goos "windows", which has no scheduler, with the
// error the daemon's own refusal is too (sched.ErrUnsupported).
func checkPlatform(goos string) error {
	if goos == "windows" {
		return sched.ErrUnsupported
	}
	return nil
}

// claimFlags are the flags `submit` and `run` share: what the job is and what
// it claims.
type claimFlags struct {
	desc          string
	cpus, mem     int
	io, exclusive bool
}

// add registers the flags on c.
func (f *claimFlags) add(c *cobra.Command) {
	fl := c.Flags()
	fl.StringVarP(&f.desc, "desc", "d", "", "What the job is (default: the command line, cut to 60 characters)")
	fl.IntVar(&f.cpus, "cpus", 0, "Cores to claim, at least 1 (default: the host's default claim)")
	fl.IntVar(&f.mem, "mem", 0, "GB of memory to claim, at least 1 (default: the host's default claim)")
	fl.BoolVar(&f.io, "io", false, "I/O-heavy: takes one of the host's I/O slots")
	fl.BoolVar(&f.exclusive, "exclusive", false, "Claim every cap, for a benchmark (not with --cpus, --mem or --io)")
}

// claim is the claim the flags ask for. The daemon applies the default claim
// to a 0 and holds every other bound, so an explicit 0 is the one value the
// wire cannot carry: it is refused here, with the daemon's wording.
func (f *claimFlags) claim(c *cobra.Command) (sched.Claim, error) {
	for _, o := range []struct {
		flag string
		n    int
	}{
		{"cpus", f.cpus},
		{"mem", f.mem},
	} {
		if c.Flags().Changed(o.flag) && o.n < 1 {
			return sched.Claim{}, fmt.Errorf("--%s is %d: it must be at least 1", o.flag, o.n)
		}
	}
	return sched.Claim{CPUs: f.cpus, MemoryGB: f.mem, IO: f.io, Exclusive: f.exclusive}, nil
}

// submission is the job the command line asks for: args (everything after
// --) as the argv, with the shell it runs in: its working directory, its
// whole environment and the launch it belongs to ($LOOM_LAUNCH, which the
// daemon resolves to the owning session). The daemon checks every bound.
func (f *claimFlags) submission(c *cobra.Command, args []string, notify bool, tail int) (sched.Submit, error) {
	if len(args) == 0 || c.ArgsLenAtDash() != 0 {
		return sched.Submit{}, fmt.Errorf("put the command after --, as in: loom job %s -d \"<what>\" -- <cmd> [args…]", c.Name())
	}
	claim, err := f.claim(c)
	if err != nil {
		return sched.Submit{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return sched.Submit{}, fmt.Errorf("the working directory: %w", err)
	}
	desc := f.desc
	if !c.Flags().Changed("desc") {
		desc = defaultDescription(args)
	}
	return sched.Submit{
		Argv: slices.Clone(args), Cwd: cwd, Env: os.Environ(), Description: desc, Claim: claim,
		Notify: notify, Tail: tail, Launch: os.Getenv(claudemod.EnvLaunch),
	}, nil
}

// oneLine maps every control character (a newline, a tab, an escape) to a
// space, so a text stays on one line of a list's table.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// defaultDescription is the command line, each word shell-quoted and on one
// line, cut to descriptionLimit characters.
func defaultDescription(argv []string) string {
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = shellWord(a)
	}
	r := []rune(oneLine(strings.Join(words, " ")))
	if len(r) > descriptionLimit {
		r = r[:descriptionLimit]
	}
	return string(r)
}

// shellWord quotes s for a POSIX shell: as it is when it holds only
// characters a shell never treats specially, else in single quotes.
func shellWord(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !plainWordRune(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func plainWordRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("_@%+=:,./-", r)
}

func newJobSubmitCmd() *cobra.Command {
	var (
		f        claimFlags
		tail     int
		noNotify bool
	)
	cmd := &cobra.Command{
		Use:   "submit [flags] -- CMD [ARGS…]",
		Short: "Queue a command and return at once; a message brings the result",
		Long: "Queues the command with the daemon and returns as soon as it is queued or started. When it ends, " +
			"the daemon sends the result to the session this command runs in (unless --no-notify).",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			sub, err := f.submission(c, args, !noNotify, tail)
			if err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				id, err := d.submitJob(sub)
				if err != nil {
					return err
				}
				v, err := d.awaitJob(id)
				if err != nil {
					return fmt.Errorf("job %d was submitted, but %w", id, err)
				}
				out := c.OutOrStdout()
				d.printPlacement(out, v)
				if sub.Launch == "" {
					fmt.Fprintf(out, "no session to notify: follow it with loom job wait %d\n", id)
				}
				return nil
			})
		},
	}
	f.add(cmd)
	cmd.Flags().IntVar(&tail, "tail", defaultTail, "Lines of output the result message carries, 0–200")
	cmd.Flags().BoolVar(&noNotify, "no-notify", false, "Send no message when the job ends")
	return cmd
}

// submitJob makes the SubmitJob request and waits for its Reply: the new
// job's ID, or the daemon's refusal. The warning an unknown launch earns
// rides the Reply and goes to errOut.
func (d *client) submitJob(sub sched.Submit) (sched.JobID, error) {
	d.SubmitJob(sub, sessionReq)
	reply, err := d.awaitReply(sessionReq)
	switch {
	case err != nil:
		return 0, err
	case reply.Err != nil:
		return 0, reply.Err
	}
	if reply.Notice != nil {
		fmt.Fprintf(d.errOut, "loom: %v\n", reply.Notice)
	}
	return reply.Job, nil
}

// awaitJob waits, for up to jobWait, until the replica holds job id: the
// Reply to a submission can arrive before the view that shows the job.
func (d *client) awaitJob(id sched.JobID) (core.JobView, error) {
	var v core.JobView
	ok, err := d.await(jobWait, func([]core.Event) bool {
		var found bool
		v, found = jobOf(d.Queue(), id)
		return found
	})
	switch {
	case err != nil:
		return v, err
	case !ok:
		return v, errors.New("the loom daemon did not report it (loom job list shows where it is)")
	}
	return v, nil
}

// printPlacement writes the line `submit` ends with: where a queued job
// stands, or that it started.
func (d *client) printPlacement(out io.Writer, v core.JobView) {
	if v.State != sched.Queued {
		fmt.Fprintf(out, "job %d started\n", v.ID)
		return
	}
	n, running := queuePlace(d.Queue(), v.ID)
	fmt.Fprintf(out, "job %d queued (%s in line, %d running)\n", v.ID, ordinal(n), running)
}

// jobOf is job id among q's jobs.
func jobOf(q core.QueueView, id sched.JobID) (core.JobView, bool) {
	for _, j := range q.Jobs {
		if j.ID == id {
			return j, true
		}
	}
	return core.JobView{}, false
}

// queuePlace is job id's place among the queued jobs (1 for the head, 0 when
// it is not queued) and how many jobs run.
func queuePlace(q core.QueueView, id sched.JobID) (place, running int) {
	queued := 0
	for _, j := range q.Jobs {
		switch j.State {
		case sched.Running:
			running++
		case sched.Queued:
			queued++
			if j.ID == id {
				place = queued
			}
		}
	}
	return place, running
}

// ordinal is n as "1st", "2nd", "3rd", "4th", "11th", "21st"…
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// parseJobID is a job ID typed on the command line.
func parseJobID(s string) (sched.JobID, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q is not a job ID", s)
	}
	return sched.JobID(n), nil
}

func newJobListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the running and queued jobs, and why each queued one waits",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				return writeJobList(c.OutOrStdout(), d.Queue(), all, time.Now())
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include the finished jobs the daemon still keeps")
	return cmd
}

// writeJobList prints q's jobs as a table (the running, the queued, and with
// all the finished ones kept), led by the reason when admission is paused.
func writeJobList(w io.Writer, q core.QueueView, all bool, now time.Time) error {
	if q.Paused != "" {
		fmt.Fprintf(w, "admission paused: %s\n", q.Paused)
	}
	var rows []core.JobView
	finished := 0
	for _, j := range q.Jobs {
		switch {
		case !j.State.Finished() || all:
			rows = append(rows, j)
		default:
			finished++
		}
	}
	if len(rows) == 0 {
		msg := "No jobs."
		if !all {
			msg = "No running or queued jobs."
			if finished > 0 {
				msg += fmt.Sprintf(" %d finished (loom job list --all shows them).", finished)
			}
		}
		_, err := fmt.Fprintln(w, msg)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tDESCRIPTION\tOWNER\tCLAIM\tTIME\tWAITS FOR")
	for _, j := range rows {
		waits := ""
		if j.State == sched.Queued {
			waits = j.Waiting
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, stateText(j), oneLine(j.Description), oneLine(ownerText(j)), claimText(j.Claim), rounded(elapsed(j, now)), oneLine(waits))
	}
	return tw.Flush()
}

// stateText is a job's state, with how a failed one failed.
func stateText(j core.JobView) string {
	if j.State != sched.Failed {
		return string(j.State)
	}
	sig, code, ran := ranAs(j)
	switch {
	case !ran:
		return "failed (not started)"
	case sig != "":
		return "failed (" + sig + ")"
	}
	return fmt.Sprintf("failed (exit %d)", code)
}

// ranAs says how a failed job ran: ran is false when it never did (its start
// failed, or it has no exit record), else it was killed by the signal sig (a
// name) or exited with code.
func ranAs(j core.JobView) (sig string, code int, ran bool) {
	e := j.Exit
	switch {
	case e == nil || e.Error != "":
		return "", 0, false
	case e.Signal != "":
		return e.Signal, 0, true
	case e.Code > 0:
		return "", e.Code, true
	}
	return "", 0, false
}

func ownerText(j core.JobView) string {
	switch {
	case j.Owner == 0:
		return "-"
	case j.OwnerTitle == "":
		return fmt.Sprintf("#%d", j.Owner)
	}
	return j.OwnerTitle
}

func claimText(c sched.Claim) string {
	if c.Exclusive {
		return "exclusive"
	}
	s := fmt.Sprintf("%dc/%dG", c.CPUs, c.MemoryGB)
	if c.IO {
		s += " io"
	}
	return s
}

// elapsed is how long a job has waited (queued), has run (running), or ran
// (finished; a job that never started, how long it lived).
func elapsed(j core.JobView, now time.Time) time.Duration {
	switch {
	case j.State == sched.Queued:
		return now.Sub(j.Submitted)
	case j.State == sched.Running:
		return now.Sub(j.Started)
	case !j.Started.IsZero():
		return j.Ended.Sub(j.Started)
	}
	return j.Ended.Sub(j.Submitted)
}

// rounded is d to the second, as "3m10s".
func rounded(d time.Duration) string {
	return d.Round(time.Second).String()
}

func newJobCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel ID",
		Short: "Cancel a queued or running job",
		Long: "A queued job is cancelled at once. A running job's processes get SIGTERM, and SIGKILL ten seconds later " +
			"if they are still there; the job ends as cancelled either way.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			id, err := parseJobID(args[0])
			if err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				if err := d.request(func(req core.ReqID) { d.CancelJob(id, core.CancelFromCLI, req) }); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "cancel requested for job %d\n", id)
				return nil
			})
		},
	}
}

// ExitError ends a command with Code as the process's exit code. main prints
// Msg first when it has one: a follower says what happened on stderr as it
// goes, so most carry none.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// exitNotRun is the exit code of a follower whose job was lost, could not
// start or was cancelled: it did not run to an exit of its own.
const exitNotRun = 125

// logPoll is how often a follower reads output.log for new output.
var logPoll = 200 * time.Millisecond

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

// run follows the job until it has ended (its last view, with the log copied
// whole) or the command's context ends (that context's error, with the last
// view). A job the daemon does not hold is an error, and so is a lost
// daemon: the job goes on without it.
func (f *jobFollower) run() (core.JobView, error) {
	tick := time.NewTicker(logPoll)
	defer tick.Stop()
	for {
		f.d.events()
		v, ok := jobOf(f.d.Queue(), f.id)
		if !ok {
			return v, fmt.Errorf("there is no job %d", f.id)
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
			return v, fmt.Errorf("lost the loom daemon: %w", err)
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
// exit 0). An interrupt (signalled names it) leaves the job running and ends
// the command (interrupted). queuedHere is true for a job this command
// queued, so its start is reported even when it never waited.
func (d *client) follow(out io.Writer, id sched.JobID, signalled func() os.Signal, queuedHere bool) error {
	f := &jobFollower{d: d, id: id, out: out, queued: queuedHere}
	v, err := f.run()
	switch {
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

// interrupted ends a follower that sig interrupted. The job is never
// cancelled by it. A job that has an owner and has not ended gets a message
// asked for (the follower's job was submitted without one), and the line says
// so only when the daemon took the request; otherwise it says how to follow
// the job again. The exit code is the signal's.
func (d *client) interrupted(id sched.JobID, sig os.Signal) error {
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
	n.NotifyJob(id, sessionReq)
	var reply core.Reply
	got, err := n.await(notifyWait, func(events []core.Event) bool {
		for _, ev := range events {
			if r, ok := ev.(core.Reply); ok && r.Req == sessionReq {
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
			"could not start or was cancelled). No message is sent when it ends. An interrupt (Ctrl-C, a Bash timeout) leaves the job running: " +
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

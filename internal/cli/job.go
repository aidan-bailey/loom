package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
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
	cmd.AddCommand(newJobSubmitCmd(), newJobRunCmd(), newJobWaitCmd(), newJobLogsCmd(), newJobListCmd(), newJobCancelCmd(), newJobBeliefsCmd(), newJobAgentCmd())
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

// claimFlags are the flags `submit` and `run` share: what the job is, what it
// claims, and the job it runs after.
type claimFlags struct {
	desc          string
	cpus, mem     int
	io, exclusive bool
	after         int64
	noRequeue     bool
}

// add registers the flags on c.
func (f *claimFlags) add(c *cobra.Command) {
	fl := c.Flags()
	fl.StringVarP(&f.desc, "desc", "d", "", "What the job is (default: the command line, cut to 60 characters)")
	fl.IntVar(&f.cpus, "cpus", 0, "Cores to claim, at least 1 (default: the host's default claim)")
	fl.IntVar(&f.mem, "mem", 0, "GB of memory to claim, at least 1 (default: the host's default claim)")
	fl.BoolVar(&f.io, "io", false, "I/O-heavy: takes one of the host's I/O slots")
	fl.BoolVar(&f.exclusive, "exclusive", false, "Claim every cap, for a benchmark (not with --cpus, --mem or --io)")
	fl.Int64Var(&f.after, "after", 0, "Run after job N ends done: held until then, and cancelled if it ends any other way")
	fl.BoolVar(&f.noRequeue, "no-requeue", false, "Never requeue the job when memory runs short; pause it instead")
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
	if c.Flags().Changed("after") && f.after < 1 {
		return sched.Submit{}, fmt.Errorf("--after is %d: it must be a job ID", f.after)
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
		Notify: notify, Tail: tail, Launch: os.Getenv(claudemod.EnvLaunch), After: sched.JobID(f.after),
		NoRequeue: f.noRequeue,
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
				if errors.Is(err, errNotReported) {
					return fmt.Errorf("job %d was submitted, but %w (loom job list shows where it is)", id, err)
				}
				if err != nil {
					return fmt.Errorf("job %d was submitted, but %w", id, err)
				}
				out := c.OutOrStdout()
				d.printPlacement(out, v)
				d.printSizing(out, id)
				if line := resultRoute(sub, v); line != "" {
					fmt.Fprintln(out, line)
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

// errNotReported is awaitJob's error when the daemon accepted a job but its
// view never arrived.
var errNotReported = errors.New("the loom daemon did not report it")

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
		return v, errNotReported
	}
	return v, nil
}

// printPlacement writes the line `submit` ends with: where a queued job
// stands, what a held job runs after, how a job that has already ended
// ended, or that it started.
func (d *client) printPlacement(out io.Writer, v core.JobView) {
	switch {
	case v.State.Finished():
		fmt.Fprintf(out, "job %d %s\n", v.ID, stateText(v))
		return
	case v.State == sched.Held:
		fmt.Fprintf(out, "job %d held: runs %s\n", v.ID, v.Waiting)
		return
	case v.State != sched.Queued:
		fmt.Fprintf(out, "job %d started\n", v.ID)
		return
	}
	n, running := queuePlace(d.Queue(), v.ID)
	fmt.Fprintf(out, "job %d queued (%s in line, %d running)\n", v.ID, ordinal(n), running)
}

// resultRoute says where a submitted job's result will go (subagent job
// results spec §5.5): to the subagent that submitted it, to the session, or,
// from a plain shell, nowhere. "" when the submit asked for no message, or
// its launch is unknown (the daemon's warning says so).
func resultRoute(sub sched.Submit, v core.JobView) string {
	switch {
	case sub.Launch == "":
		return fmt.Sprintf("no session to notify: follow it with loom job wait %d", v.ID)
	case !sub.Notify || v.Owner == 0:
		return ""
	case v.Agent != "":
		return fmt.Sprintf("its result comes back to this subagent (%s)", core.ShortAgent(v.Agent))
	}
	return "its result comes to this session"
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
// failed, as core.JobView.StartFailed says, which the notice and the TUI use
// too), else it was killed by the signal sig (a name) or exited with code.
func ranAs(j core.JobView) (sig string, code int, ran bool) {
	e := j.Exit
	switch {
	case j.StartFailed() || e == nil:
		return "", 0, false
	case e.Signal != "":
		return e.Signal, 0, true
	case e.Code > 0:
		return "", e.Code, true
	}
	return "", 0, false
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
// start or was cancelled, or that can't go on (it lost the daemon, found it did
// not know the job or never heard of it, or could not read the output), and of
// `run` when it submitted nothing: it saw no exit of the job's own.
const exitNotRun = 125

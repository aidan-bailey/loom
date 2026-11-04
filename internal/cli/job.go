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
	"text/tabwriter"
	"time"

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
	cmd.AddCommand(newJobSubmitCmd(), newJobListCmd(), newJobCancelCmd())
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

// defaultDescription is the command line, each word shell-quoted, cut to
// descriptionLimit characters.
func defaultDescription(argv []string) string {
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = shellWord(a)
	}
	r := []rune(strings.Join(words, " "))
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
			j.ID, stateText(j), j.Description, ownerText(j), claimText(j.Claim), rounded(elapsed(j, now)), waits)
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

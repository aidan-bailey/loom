package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/spf13/cobra"
)

// waitCap bounds one wait call (flow in loom part 2, spec §5.2, §5.6):
// Claude Code moves a background subagent's foreground Bash call to the
// background once it has run 180 s, and a moved wait wakes its agent later.
const waitCap = 170 * time.Second

// The exit codes of a wait for a step that hasn't ended (call it again) and
// for one that ended without an exit of its own: those of the script the
// Go wait replaced, so agents' handling of them doesn't change.
const (
	exitStillRunning = 75
	exitLost         = 70
)

// exitUsage is a flow command's exit code for arguments it can't take.
const exitUsage = 2

// flowName is a step's or a run's name: it names files in the state dir.
var flowName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// pollLoop is a shell loop whose body runs sleep.
var pollLoop = regexp.MustCompile(`(?s)\b(until|while)\b.*?\bdo\b.*?\bsleep\b.*?\bdone\b`)

func newFlowWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Start a long step as a job, or wait for one inside a turn",
	}
	cmd.AddCommand(newFlowWaitStartCmd(), newFlowWaitWaitCmd())
	return cmd
}

func newFlowWaitStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start NAME -- CMD [ARGS…]",
		Short: "Queue CMD as step NAME's job, and print its job ID and log",
		Long: "Queues CMD in loom's work queue as step NAME's job, with no message when it ends, and prints " +
			"`started NAME: job N; log <state>/agent-wait/NAME.log`. Refuses (exit 2) a NAME whose job hasn't " +
			"ended, a poll (`sleep`, or a sh, bash or zsh -c loop around `sleep`) and a `loom` command. Exits 125 " +
			"when it queued nothing.",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			if c.ArgsLenAtDash() != 1 || len(args) < 2 {
				return &ExitError{Code: exitUsage, Msg: "usage: loom flow wait start NAME -- CMD [ARGS…]"}
			}
			name, argv := args[0], args[1:]
			if !flowName.MatchString(name) {
				return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow wait: bad name %q", name)}
			}
			if why := refusedStep(argv); why != "" {
				return &ExitError{Code: exitUsage, Msg: "loom flow wait: " + why}
			}
			cwd, err := os.Getwd()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow wait: the working directory: %v", err)}
			}
			conf, cleanup, err := newFlowConf()
			if err != nil {
				return err
			}
			defer cleanup()
			dir, err := flowStateDir(conf, "agent-wait")
			if err != nil {
				return err
			}
			sub := flowJob(argv, name+": "+defaultDescription(argv), cwd, 0)
			return flowDaemon(c, func(d *client) error { return d.startStep(c.OutOrStdout(), dir, name, sub) })
		},
	}
}

func newFlowWaitWaitCmd() *cobra.Command {
	var job int64
	cmd := &cobra.Command{
		Use:   "wait (NAME | --job N) [SECONDS]",
		Short: "Wait up to SECONDS (170, at most) for a step's job to end, then say how it stands",
		Long: "Prints `done NAME exit S` and exits with the job's exit code (128 + N for a job killed by signal N); " +
			"`running NAME: job N …` and exits 75 while it hasn't ended; `lost NAME: …` and exits 70 for a job " +
			"lost, cancelled, never started or no longer known to the queue; exits 125 when it can't follow the " +
			"job. Each call copies what the job's output gained to the step's log. A daemon lost mid-wait (a " +
			"restart) is joined again within SECONDS, saying so on stderr; still away then, it exits 75.",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			byJob := c.Flags().Changed("job")
			var name, secs string
			switch {
			case byJob && len(args) <= 1 && job > 0:
				name = fmt.Sprintf("job-%d", job)
				if len(args) == 1 {
					secs = args[0]
				}
			case !byJob && len(args) >= 1:
				name = args[0]
				if len(args) == 2 {
					secs = args[1]
				}
			default:
				return &ExitError{Code: exitUsage, Msg: "usage: loom flow wait wait (NAME | --job N) [SECONDS]"}
			}
			if !flowName.MatchString(name) {
				return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow wait: bad name %q", name)}
			}
			bound, err := waitSeconds(secs)
			if err != nil {
				return &ExitError{Code: exitUsage, Msg: "loom flow wait: " + err.Error()}
			}
			conf, cleanup, err := newFlowConf()
			if err != nil {
				return err
			}
			defer cleanup()
			dir, err := flowStateDir(conf, "agent-wait")
			if err != nil {
				return err
			}
			id := sched.JobID(job)
			if !byJob {
				id, err = readStepJob(filepath.Join(dir, name+".job"))
				switch {
				case errors.Is(err, fs.ErrNotExist):
					return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow wait: nothing started as %s", name)}
				case err != nil:
					return &ExitError{Code: exitNotRun, Msg: "loom flow wait: " + err.Error()}
				}
			}
			logPath := filepath.Join(dir, name+".log")
			deadline := waitDeadline(bound)
			err = rejoin(c, deadline, 0, sayOnStderr(c), func(d *client) error { return d.waitStep(c.OutOrStdout(), name, id, logPath, deadline) })
			var away *outOfReachError
			if errors.As(err, &away) {
				fmt.Fprintf(c.OutOrStdout(), "running %s: job %d goes on, but %v (loom flow wait wait %s follows it again)\n", name, id, away, name)
				return &ExitError{Code: exitStillRunning}
			}
			if up := (*daemonUpgradedError)(nil); errors.As(err, &up) {
				return up.exit("loom flow wait", "loom flow wait wait "+name)
			}
			return err
		},
	}
	cmd.Flags().Int64Var(&job, "job", 0, "Wait for job N, queued any way, in place of a named step")
	return cmd
}

// startStep queues sub as step name's job and records it in dir (spec
// §5.5), refusing a name whose job hasn't ended.
func (d *client) startStep(out io.Writer, dir, name string, sub sched.Submit) error {
	jobFile := filepath.Join(dir, name+".job")
	d.events()
	switch id, err := readStepJob(jobFile); {
	case err == nil:
		if v, ok := jobOf(d.Queue(), id); ok && !v.State.Finished() {
			return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow wait: %s is still running as job %d (loom flow wait wait %s follows it)", name, id, name)}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return &ExitError{Code: exitNotRun, Msg: "loom flow wait: " + err.Error()}
	}
	id, err := submitFlowJob(d, sub)
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: "loom flow wait: nothing was queued: " + err.Error()}
	}
	logPath := filepath.Join(dir, name+".log")
	if err := os.Remove(logPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow wait: job %d was queued, but the old log stays: %v (loom flow wait wait --job %d follows it)", id, err, id)}
	}
	if err := config.AtomicWriteFile(jobFile, []byte(fmt.Sprintf("%d\n", id)), 0o600); err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow wait: job %d was queued, but %s could not be written: %v (loom flow wait wait --job %d follows it)", id, jobFile, err, id)}
	}
	fmt.Fprintf(out, "started %s: job %d; log %s\n", name, id, logPath)
	return nil
}

// readStepJob is the job ID a step's .job file holds.
func readStepJob(path string) (sched.JobID, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	id, err := parseJobID(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return id, nil
}

// waitStep waits until deadline for job id to end, copies what its output
// gained to logPath, and says how it stands as label (spec §5.6). It is
// safe to run again on a daemon joined again (rejoin): the copy adds only
// what the step's log lacks.
func (d *client) waitStep(out io.Writer, label string, id sched.JobID, logPath string, deadline time.Time) error {
	v, known, err := d.jobWithin(id, time.Until(deadline))
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow wait: job %d goes on, but %v (loom flow wait wait %s follows it again)", id, err, label)}
	}
	if !known {
		fmt.Fprintf(out, "lost %s: the queue no longer knows job %d\n", label, id)
		return &ExitError{Code: exitLost}
	}
	// The view came first, so a job seen ended has a complete log.
	if err := appendNew(v.LogPath, logPath); err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow wait: job %d: %v", id, err)}
	}
	if !v.State.Finished() {
		fmt.Fprintf(out, "running %s: job %d %s\n", label, id, standing(d.Queue(), v))
		return &ExitError{Code: exitStillRunning}
	}
	line, code := jobOutcome(v)
	if !ranToAnEnd(v) {
		fmt.Fprintf(out, "lost %s: %s\n", label, line)
		return &ExitError{Code: exitLost}
	}
	fmt.Fprintf(out, "done %s exit %d\n", label, code)
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

// jobWithin waits up to bound for job id to end, and returns its last view;
// known is false when the daemon doesn't hold the job. An error is a lost
// daemon or an ended context.
func (d *client) jobWithin(id sched.JobID, bound time.Duration) (v core.JobView, known bool, err error) {
	_, err = d.await(max(bound, time.Millisecond), func([]core.Event) bool {
		v, known = jobOf(d.Queue(), id)
		return !known || v.State.Finished()
	})
	return v, known, err
}

// standing is where job v stands: "queued (3rd in line)", "held: runs after
// job 4 (running)", "running 2m10s", or "ended <state>".
func standing(q core.QueueView, v core.JobView) string {
	switch {
	case v.State == sched.Queued:
		n, _ := queuePlace(q, v.ID)
		return fmt.Sprintf("queued (%s in line)", ordinal(n))
	case v.State == sched.Held:
		return "held: runs " + v.Waiting
	case v.State.Finished():
		return "ended " + string(v.State)
	}
	return "running " + rounded(time.Since(v.Started))
}

// ranToAnEnd reports whether job v ran its command to an end of its own:
// done, or failed with an exit code or a signal.
func ranToAnEnd(v core.JobView) bool {
	_, _, ran := ranAs(v)
	return v.State == sched.Done || (v.State == sched.Failed && ran)
}

// appendNew appends to dst what src holds past dst's own length: dst holds
// the start of src, as every earlier call copied it. A src not written yet
// (a job still queued) adds nothing.
func appendNew(src, dst string) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("write the step's log: %w", err)
	}
	defer out.Close()
	if src == "" {
		return out.Close()
	}
	in, err := os.Open(src)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return out.Close()
	case err != nil:
		return fmt.Errorf("read the job's output: %w", err)
	}
	defer in.Close()
	info, err := out.Stat()
	if err != nil {
		return fmt.Errorf("write the step's log: %w", err)
	}
	if _, err := in.Seek(info.Size(), io.SeekStart); err != nil {
		return fmt.Errorf("read the job's output: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy the job's output: %w", err)
	}
	return out.Close()
}

// waitSeconds is a wait's bound from its argument: waitCap when there is
// none, refused above it.
func waitSeconds(arg string) (time.Duration, error) {
	if arg == "" {
		return waitCap, nil
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 0 || time.Duration(n)*time.Second > waitCap {
		return 0, fmt.Errorf("%q is not a number of seconds from 0 to %d", arg, int(waitCap/time.Second))
	}
	return time.Duration(n) * time.Second, nil
}

// refusedStep is why argv can't be a step, or "" (spec §5.5).
func refusedStep(argv []string) string {
	switch {
	case filepath.Base(argv[0]) == "loom":
		return "already a job: follow it with `loom flow wait wait --job <N>`"
	case isPoll(argv):
		return "a poll isn't a step, and it would hold a queue claim doing nothing: wait on what it watches " +
			"with `loom flow wait wait <name>` or `--job <N>`; a step's output is already in its log"
	}
	return ""
}

// isPoll reports whether argv is `sleep`, or a sh, bash or zsh -c script
// holding a loop whose body runs sleep.
func isPoll(argv []string) bool {
	if filepath.Base(argv[0]) == "sleep" {
		return true
	}
	script, ok := shellScript(argv)
	return ok && pollLoop.MatchString(script)
}

// shellScript is the script of `sh|bash|zsh [OPTION…] -c SCRIPT …`: the
// argument after the first short option cluster holding c.
func shellScript(argv []string) (string, bool) {
	switch filepath.Base(argv[0]) {
	case "sh", "bash", "zsh":
	default:
		return "", false
	}
	for i := 1; i+1 < len(argv); i++ {
		a := argv[i]
		switch {
		case strings.HasPrefix(a, "--"):
			continue
		case !strings.HasPrefix(a, "-"):
			return "", false
		case strings.Contains(a[1:], "c"):
			return argv[i+1], true
		}
	}
	return "", false
}

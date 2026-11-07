package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
)

// gateRunSchema is the run record's version (flow in loom part 2, spec §5.4).
const gateRunSchema = 1

// The verdicts a step can have (spec §5.2).
const (
	verdictPass    = "PASS"
	verdictFail    = "FAIL"
	verdictKilled  = "FAIL (killed)"
	verdictInfra   = "FAIL (infra)"
	verdictVacuous = "VACUOUS"
	verdictSkipped = "SKIPPED"
)

// failing is the verdicts that fail a run, in the order a tally lists them
// after PASS.
var failing = []string{verdictFail, verdictKilled, verdictInfra, verdictVacuous}

// runLockWait bounds how long a gates command waits for another to release
// the run lock: each holds it only to judge, queue and save.
var runLockWait = 30 * time.Second

// notName is a character a run's name can't hold.
var notName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// gateRun is one `loom flow gates start` and what became of it: the run
// record, <state>/gates/<name>.json.
type gateRun struct {
	Schema    int        `json:"schema"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"` // "story" or "final"
	Commits   []string   `json:"commits,omitempty"`
	Cwd       string     `json:"cwd"` // where its jobs run, queued-again ones too
	Steps     []gateStep `json:"steps"`
	Cancelled bool       `json:"cancelled,omitempty"`
}

// gateStep is one line of the expansion and what became of it.
type gateStep struct {
	Line    string        `json:"line"`              // as the expansion printed it, a LOCKED line as RUN <command>
	Run     bool          `json:"run,omitempty"`     // a RUN or LOCKED line, which a job runs
	Locks   []string      `json:"locks,omitempty"`   // a LOCKED line's locks, which each of its jobs takes
	Jobs    []sched.JobID `json:"jobs,omitempty"`    // its job in each generation, the latest last
	Verdict string        `json:"verdict,omitempty"` // "" until judged
	Why     string        `json:"why,omitempty"`
	Log     string        `json:"log,omitempty"` // its output, copied when its job ended
}

// text is the line past its first word: a RUN line's command, or a SKIPPED
// or FAIL line's gate and why.
func (s gateStep) text() string {
	_, rest, _ := strings.Cut(s.Line, " ")
	return rest
}

// job is the step's latest job, 0 for none.
func (s gateStep) job() sched.JobID {
	if len(s.Jobs) == 0 {
		return 0
	}
	return s.Jobs[len(s.Jobs)-1]
}

// ended reports whether every step has a verdict, or the run was cancelled.
func (r *gateRun) ended() bool {
	return r.Cancelled || !slices.ContainsFunc(r.Steps, func(s gateStep) bool { return s.Verdict == "" })
}

// failed reports whether a step's verdict fails the run.
func (r *gateRun) failed() bool {
	return slices.ContainsFunc(r.Steps, func(s gateStep) bool { return slices.Contains(failing, s.Verdict) })
}

// gateOf is the table number of the step one of whose jobs is id, 0 for none.
func (r *gateRun) gateOf(id sched.JobID) int {
	for i, s := range r.Steps {
		if id != 0 && slices.Contains(s.Jobs, id) {
			return i + 1
		}
	}
	return 0
}

// openJobs is the latest job of each RUN step not yet judged.
func (r *gateRun) openJobs() []sched.JobID {
	var ids []sched.JobID
	for _, s := range r.Steps {
		if s.Run && s.Verdict == "" && s.job() != 0 {
			ids = append(ids, s.job())
		}
	}
	return ids
}

// plan fills r's steps from the expansion's lines (spec §5.1): a RUN line a
// job will run, a LOCKED line one whose job takes the locks it names first
// (LOCKED <lock>[,<lock>…] <command>), SKIPPED and FAIL lines judged as
// printed, and in a story run every RUN line after the first FAIL SKIPPED.
func (r *gateRun) plan(lines []string) error {
	failedAt := 0
	for _, line := range lines {
		st := gateStep{Line: line}
		word, rest, _ := strings.Cut(line, " ")
		switch word {
		case "none:":
			continue
		case "LOCKED":
			locks, command, _ := strings.Cut(rest, " ")
			st.Locks = strings.Split(locks, ",")
			if command == "" || slices.ContainsFunc(st.Locks, func(l string) bool { return !sched.ValidLockName(l) }) ||
				len(slices.Compact(slices.Sorted(slices.Values(st.Locks)))) != len(st.Locks) {
				return fmt.Errorf("loom flow gates: the expansion printed %q, which is no LOCKED <lock>[,<lock>…] <command> line", line)
			}
			st.Line = "RUN " + command
			fallthrough
		case "RUN":
			st.Run = true
			if r.Kind == "story" && failedAt > 0 {
				st.Verdict, st.Why = verdictSkipped, fmt.Sprintf("not run: gate %d failed", failedAt)
			}
		case "SKIPPED":
			st.Verdict = verdictSkipped
		case "FAIL":
			st.Verdict = verdictFail
			if failedAt == 0 {
				failedAt = len(r.Steps) + 1
			}
		default:
			return fmt.Errorf("loom flow gates: the expansion printed %q, which is no RUN, LOCKED, SKIPPED or FAIL line", line)
		}
		r.Steps = append(r.Steps, st)
	}
	return nil
}

func newFlowGatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gates",
		Short: "Run a story's gates or the final gates as a chain of jobs, and report each step's verdict",
	}
	cmd.AddCommand(newFlowGatesStartCmd(), newFlowGatesWaitCmd(), newFlowGatesCancelCmd())
	return cmd
}

func newFlowGatesStartCmd() *cobra.Command {
	var story, final bool
	cmd := &cobra.Command{
		Use:   "start (--story COMMIT… | --final)",
		Short: "Queue a story's gates or the final gates as a chain of jobs",
		Long: "Expands the gates (loom flow conf gates) and queues each RUN line as a job, each after the one " +
			"before, and each LOCKED line as one taking its locks; a story's chain stops at the expansion's first " +
			"FAIL line. Prints the run's name and jobs. " +
			"Refuses (exit 2) while a run in this worktree hasn't ended; exits 125 when it queued nothing.",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			var run *gateRun
			switch {
			case story && !final && len(args) > 0:
				run = &gateRun{Name: "story-" + runSlug(args[0]), Kind: "story", Commits: args}
			case final && !story && len(args) == 0:
				run = &gateRun{Name: "final", Kind: "final"}
			default:
				return &ExitError{Code: exitUsage, Msg: "usage: loom flow gates start (--story COMMIT… | --final)"}
			}
			cwd, err := os.Getwd()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: the working directory: %v", err)}
			}
			run.Schema, run.Cwd = gateRunSchema, cwd
			conf, cleanup, err := newFlowConf()
			if err != nil {
				return err
			}
			defer cleanup()
			dir, err := flowStateDir(conf, "gates")
			if err != nil {
				return err
			}
			gateArgs := []string{"--final"}
			if run.Kind == "story" {
				gateArgs = append([]string{"--story"}, args...)
			}
			lines, err := conf.Gates(gateArgs...)
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: err.Error()}
			}
			if err := run.plan(lines); err != nil {
				return &ExitError{Code: exitNotRun, Msg: err.Error()}
			}
			return flowDaemon(c, func(d *client) error { return d.startRun(c.OutOrStdout(), dir, run) })
		},
	}
	cmd.Flags().BoolVar(&story, "story", false, "A story's gates; the arguments are the story's commits")
	cmd.Flags().BoolVar(&final, "final", false, "The final gates")
	return cmd
}

func newFlowGatesWaitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wait [RUN] [SECONDS]",
		Short: "Wait up to SECONDS (170, at most) for a gate run, then print each step's verdict or standing",
		Long: "RUN is the worktree's latest run unless named, and a lone number is SECONDS. Judges each step as its " +
			"job ends, and after a final gate fails queues the gates after it again. Exits 0 when the run has " +
			"ended with every step PASS or SKIPPED, 1 when it has ended with any FAIL or VACUOUS, 75 when it " +
			"hasn't ended (call it again), 125 when it can't follow the run.",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			name, secs := runArgs(args)
			bound, err := waitSeconds(secs)
			if err != nil {
				return &ExitError{Code: exitUsage, Msg: "loom flow gates: " + err.Error()}
			}
			conf, cleanup, err := newFlowConf()
			if err != nil {
				return err
			}
			defer cleanup()
			dir, err := flowStateDir(conf, "gates")
			if err != nil {
				return err
			}
			if name, err = runName(dir, name); err != nil {
				return err
			}
			return flowDaemon(c, func(d *client) error { return d.waitRun(c.OutOrStdout(), conf, dir, name, bound) })
		},
	}
}

func newFlowGatesCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel [RUN]",
		Short: "Cancel a gate run's jobs that haven't ended, and mark the run cancelled",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			conf, cleanup, err := newFlowConf()
			if err != nil {
				return err
			}
			defer cleanup()
			dir, err := flowStateDir(conf, "gates")
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name, err = runName(dir, name); err != nil {
				return err
			}
			return flowDaemon(c, func(d *client) error { return d.cancelRun(c.OutOrStdout(), dir, name) })
		},
	}
}

// runArgs splits wait's arguments into a run's name and seconds: a lone
// argument of digits is seconds (planning amendment 3).
func runArgs(args []string) (name, secs string) {
	switch {
	case len(args) == 2:
		return args[0], args[1]
	case len(args) == 1 && strings.Trim(args[0], "0123456789") == "":
		return "", args[0]
	case len(args) == 1:
		return args[0], ""
	}
	return "", ""
}

// runSlug is a commit as part of a run's name: at most 12 characters, each
// one a name can hold.
func runSlug(commit string) string {
	s := notName.ReplaceAllString(commit, "-")
	if len(s) > 12 {
		s = s[:12]
	}
	return s
}

// runName is name checked, or dir's latest run when name is "".
func runName(dir, name string) (string, error) {
	if name != "" {
		if !flowName.MatchString(name) {
			return "", &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow gates: bad run name %q", name)}
		}
		return name, nil
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return "", &ExitError{Code: exitNotRun, Msg: "loom flow gates: " + err.Error()}
	}
	var latest string
	var at time.Time
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(at) {
			latest, at = strings.TrimSuffix(filepath.Base(p), ".json"), info.ModTime()
		}
	}
	if latest == "" {
		return "", &ExitError{Code: exitUsage, Msg: "loom flow gates: no gate run here; loom flow gates start starts one"}
	}
	return latest, nil
}

// startRun queues run's RUN steps as a chain and saves its record (spec
// §5.1), refusing while another run here hasn't ended. A chain it can't
// queue whole, or whose record it can't save, is cancelled.
func (d *client) startRun(out io.Writer, dir string, run *gateRun) error {
	unlock, err := lockRuns(dir)
	if err != nil {
		return err
	}
	defer unlock()
	active, err := activeRun(dir)
	switch {
	case err != nil:
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: %v; the record is left as it is", err)}
	case active != "":
		return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow gates: run %s hasn't ended here: loom flow gates wait %s finishes it, loom flow gates cancel %s ends it", active, active, active)}
	}
	var steps []int
	for i, st := range run.Steps {
		if st.Run && st.Verdict == "" {
			steps = append(steps, i)
		}
	}
	ids, err := d.submitChain(run, steps)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(dir, run.Name)); err != nil {
		return d.abandon(ids, fmt.Errorf("the old logs: %w", err))
	}
	if err := saveRun(dir, run); err != nil {
		return d.abandon(ids, err)
	}
	fmt.Fprintf(out, "started %s: %d steps, %s\n", run.Name, len(run.Steps), jobsText(ids))
	for i, st := range run.Steps {
		if st.Verdict != "" {
			fmt.Fprintf(out, "%d %s: %s%s\n", i+1, st.Verdict, st.text(), detail(st))
		}
	}
	return nil
}

// submitChain queues run's steps as a chain, each after the one before, in
// run's directory, each with its step's locks, and adds each job to its
// step. A submit refused partway, or a job whose locks the daemon dropped
// (keptLocks), cancels the jobs already queued and adds none.
func (d *client) submitChain(run *gateRun, steps []int) ([]sched.JobID, error) {
	var ids []sched.JobID
	var after sched.JobID
	for _, i := range steps {
		cmd := run.Steps[i].text()
		sub := flowJob([]string{"bash", "-c", cmd}, fmt.Sprintf("gate %d/%d: %s", i+1, len(run.Steps), cmd), run.Cwd, after)
		sub.Locks = slices.Clone(run.Steps[i].Locks)
		id, err := submitFlowJob(d, sub)
		if err == nil {
			err = d.keptLocks(sub, id)
		}
		if err != nil {
			_ = d.cancelJobs(ids)
			return nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: gate %d was not queued (%v), so the %d queued before it were cancelled", i+1, err, len(ids))}
		}
		ids = append(ids, id)
		after = id
	}
	for k, i := range steps {
		run.Steps[i].Jobs = append(run.Steps[i].Jobs, ids[k])
	}
	return ids, nil
}

// keptLocks checks that the daemon kept the locks sub, queued as job id,
// asked for (refuseIgnoredLocks, which cancels a job whose locks a daemon
// from before them dropped), and cancels id when it can't tell.
func (d *client) keptLocks(sub sched.Submit, id sched.JobID) error {
	if len(sub.Locks) == 0 {
		return nil
	}
	v, err := d.awaitJob(id)
	if err != nil {
		_ = d.cancelJobs([]sched.JobID{id})
		return fmt.Errorf("job %d's locks can't be checked, so it was cancelled: %w", id, err)
	}
	return d.refuseIgnoredLocks(sub, v)
}

// abandon cancels ids, a chain whose record could not be saved, and ends
// the command with why.
func (d *client) abandon(ids []sched.JobID, why error) error {
	_ = d.cancelJobs(ids)
	return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: %v, so the run's jobs were cancelled", why)}
}

// waitRun advances run name until it has ended or bound has passed, then
// prints its table and exits as the run stands (spec §5.2).
func (d *client) waitRun(out io.Writer, conf flowConf, dir, name string, bound time.Duration) error {
	deadline := time.Now().Add(bound)
	for {
		run, err := d.advance(conf, dir, name)
		if err != nil {
			return err
		}
		if run.ended() || !time.Now().Before(deadline) {
			d.events()
			printRun(out, d.Queue(), run)
			return runExit(run)
		}
		open := run.openJobs()
		_, err = d.await(max(time.Until(deadline), time.Millisecond), func([]core.Event) bool {
			return slices.ContainsFunc(open, func(id sched.JobID) bool {
				v, ok := jobOf(d.Queue(), id)
				return !ok || v.State.Finished()
			})
		})
		if err != nil {
			return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: the run's jobs go on, but %v (loom flow gates wait %s follows them again)", err, name)}
		}
	}
}

// advance judges each step whose job has ended and, in a final run, queues
// again the steps a failure cancelled, under the run lock, saving the
// record when anything changed (spec §5.2, §5.4).
func (d *client) advance(conf flowConf, dir, name string) (*gateRun, error) {
	unlock, err := lockRuns(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	run, err := readRun(runPath(dir, name))
	if err != nil {
		return nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: %v; the record is left as it is", err)}
	}
	if run.ended() {
		return run, nil
	}
	d.events()
	q := d.Queue()
	changed := false
	var again []int
	for i := range run.Steps {
		st := &run.Steps[i]
		if !st.Run || st.Verdict != "" {
			continue
		}
		v, ok := jobOf(q, st.job())
		switch {
		case !ok:
			st.Verdict, st.Why = verdictInfra, fmt.Sprintf("the queue no longer knows job %d", st.job())
		case !v.State.Finished():
			continue
		default:
			gate, cascaded := cascadedFrom(q, run, v)
			switch {
			case cascaded && run.Kind == "final":
				again = append(again, i)
				continue
			case cascaded:
				st.Verdict, st.Why = verdictSkipped, fmt.Sprintf("not run: gate %d failed", gate)
			default:
				judgeStep(conf, filepath.Join(dir, run.Name, strconv.Itoa(i+1)+".log"), st, v)
			}
		}
		changed = true
	}
	var ids []sched.JobID
	var queueErr error
	if len(again) > 0 {
		if ids, queueErr = d.submitChain(run, again); queueErr == nil {
			changed = true
		}
	}
	if changed {
		if err := saveRun(dir, run); err != nil {
			_ = d.cancelJobs(ids)
			return nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: the record: %v", err)}
		}
	}
	if queueErr != nil {
		return nil, queueErr
	}
	return run, nil
}

// cascadedFrom reports whether job v was cancelled because the job it ran
// after ended other than done, and the table number of the gate whose job
// began that: the first of the chain to end so.
func cascadedFrom(q core.QueueView, run *gateRun, v core.JobView) (int, bool) {
	root, cascaded := v, false
	for root.State == sched.Cancelled && root.After != 0 {
		prev, ok := jobOf(q, root.After)
		if !ok || !prev.State.Finished() || prev.State == sched.Done {
			break
		}
		root, cascaded = prev, true
	}
	return run.gateOf(root.ID), cascaded
}

// judgeStep gives st its verdict from its ended job v (spec §5.2's table):
// the log copied to logPath first, then the job's end, then the vacuous
// check for a job that exited 0. A job that failed because another process
// held a port it binds (portClash) is FAIL (infra), which offers a retry: no
// fix to the code frees the port.
func judgeStep(conf flowConf, logPath string, st *gateStep, v core.JobView) {
	if err := copyFile(v.LogPath, logPath); err != nil {
		st.Verdict, st.Why = verdictInfra, fmt.Sprintf("job %d's output can't be copied: %v", v.ID, err)
		return
	}
	st.Log = logPath
	line, code := jobOutcome(v)
	sig, _, ran := ranAs(v)
	switch {
	case v.State == sched.Done:
		match, vacuous, err := conf.Vacuous(logPath)
		switch {
		case err != nil:
			st.Verdict, st.Why = verdictInfra, "the vacuous check failed: "+err.Error()
		case vacuous:
			st.Verdict, st.Why = verdictVacuous, match
		default:
			st.Verdict = verdictPass
		}
	case v.State == sched.Failed && ran && (sig != "" || code > 128):
		st.Verdict, st.Why = verdictKilled, line
	case v.State == sched.Failed && ran:
		st.Verdict, st.Why = verdictFail, line
		if why, clash := portClash(st, line); clash {
			st.Verdict, st.Why = verdictInfra, why
		}
	default:
		st.Verdict, st.Why = verdictInfra, line
	}
}

// portClash is the why of a failed step whose copied log says a port was
// already in use (core.PortInUse, the match a job's notice makes for its lock
// hint), and whether it says so. A log it can't read leaves the step FAIL.
func portClash(st *gateStep, line string) (string, bool) {
	lock, found, err := core.PortInUse(st.Log)
	if err != nil || !found {
		return "", false
	}
	why := line + "; its log says a port was already in use"
	if slices.Contains(st.Locks, lock) {
		why += fmt.Sprintf(", though the gate held %s, so a process outside loom's queue has it", lock)
	} else {
		why += fmt.Sprintf(" (if a queued job binds it too, give the gate locks = [%q] in the profile)", lock)
	}
	return why + ": retry once it is free", true
}

// copyFile copies src to dst whole; a src that doesn't exist (a job that
// never started) copies as empty.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	var in io.Reader = strings.NewReader("")
	if src != "" {
		f, err := os.Open(src)
		switch {
		case err == nil:
			defer f.Close()
			in = f
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// printRun writes run's table: one line per step, its verdict or where its
// job stands, then how the run stands.
func printRun(out io.Writer, q core.QueueView, run *gateRun) {
	fmt.Fprintf(out, "%s: %d steps\n", run.Name, len(run.Steps))
	for i, st := range run.Steps {
		status := st.Verdict
		if status == "" {
			status = pendingStatus(q, run, st)
		}
		fmt.Fprintf(out, "%d %s: %s%s\n", i+1, status, st.text(), detail(st))
	}
	switch {
	case run.Cancelled:
		fmt.Fprintln(out, "cancelled")
	case run.ended():
		fmt.Fprintf(out, "ended: %s\n", tally(run))
	default:
		fmt.Fprintf(out, "not ended: call loom flow gates wait %s again\n", run.Name)
	}
}

// pendingStatus is where an unjudged step stands: its job's standing, a
// held job by the gate it runs after.
func pendingStatus(q core.QueueView, run *gateRun, st gateStep) string {
	if run.Cancelled {
		return "cancelled"
	}
	v, ok := jobOf(q, st.job())
	switch {
	case !ok:
		return "waiting"
	case v.State == sched.Held:
		if k := run.gateOf(v.After); k > 0 {
			return fmt.Sprintf("held after gate %d", k)
		}
	}
	return standing(q, v)
}

// detail is what follows a judged step's line: why, and where the log of a
// failing one is.
func detail(st gateStep) string {
	var parts []string
	if st.Why != "" {
		parts = append(parts, st.Why)
	}
	if st.Log != "" && slices.Contains(failing, st.Verdict) {
		parts = append(parts, "log "+st.Log)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// tally counts an ended run's verdicts: "2 PASS, 1 FAIL, 1 SKIPPED".
func tally(run *gateRun) string {
	var parts []string
	for _, verdict := range append(append([]string{verdictPass}, failing...), verdictSkipped) {
		n := 0
		for _, s := range run.Steps {
			if s.Verdict == verdict {
				n++
			}
		}
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, verdict))
		}
	}
	if len(parts) == 0 {
		return "no gates"
	}
	return strings.Join(parts, ", ")
}

// runExit is a wait's exit as the run stands: 75 while it hasn't ended, 1
// when it ended cancelled or failing, else 0.
func runExit(run *gateRun) error {
	switch {
	case !run.ended():
		return &ExitError{Code: exitStillRunning}
	case run.Cancelled || run.failed():
		return &ExitError{Code: 1}
	}
	return nil
}

// cancelRun cancels run name's jobs that haven't ended and marks it
// cancelled (spec §5.3); a run that has ended is left as it is.
func (d *client) cancelRun(out io.Writer, dir, name string) error {
	unlock, err := lockRuns(dir)
	if err != nil {
		return err
	}
	defer unlock()
	run, err := readRun(runPath(dir, name))
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: %v; the record is left as it is", err)}
	}
	if run.ended() {
		fmt.Fprintf(out, "nothing to cancel: %s has ended\n", run.Name)
		return nil
	}
	d.events()
	var open []sched.JobID
	for _, id := range run.openJobs() {
		if v, ok := jobOf(d.Queue(), id); ok && !v.State.Finished() {
			open = append(open, id)
		}
	}
	refused := d.cancelJobs(open)
	run.Cancelled = true
	if err := saveRun(dir, run); err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: the record: %v", err)}
	}
	fmt.Fprintf(out, "cancelled %s: %s\n", run.Name, jobsText(open))
	if refused != nil {
		return &ExitError{Code: exitNotRun}
	}
	return nil
}

// jobsText lists ids: "no jobs", "job 4", "jobs 4, 5, 9".
func jobsText(ids []sched.JobID) string {
	switch len(ids) {
	case 0:
		return "no jobs"
	case 1:
		return fmt.Sprintf("job %d", ids[0])
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(int64(id), 10)
	}
	return "jobs " + strings.Join(parts, ", ")
}

// runPath is run name's record in dir.
func runPath(dir, name string) string { return filepath.Join(dir, name+".json") }

// readRun reads a run record, refusing one it can't decode or of another
// schema.
func readRun(path string) (*gateRun, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r gateRun
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != gateRunSchema {
		return nil, fmt.Errorf("%s: schema %d, and this loom reads %d", path, r.Schema, gateRunSchema)
	}
	return &r, nil
}

// saveRun writes r's record whole (a temp file renamed).
func saveRun(dir string, r *gateRun) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(runPath(dir, r.Name), append(b, '\n'), 0o600)
}

// activeRun is the name of a run in dir that hasn't ended, or "". A record
// it can't read is an error, never passed over.
func activeRun(dir string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return "", err
	}
	for _, p := range paths {
		r, err := readRun(p)
		if err != nil {
			return "", err
		}
		if !r.ended() {
			return r.Name, nil
		}
	}
	return "", nil
}

// lockRuns takes the state dir's run lock (planning amendment 1), so no two
// gates commands judge, queue or save at once; the function it returns
// releases it.
func lockRuns(dir string) (func(), error) {
	l := flock.New(filepath.Join(dir, "gates.lock"))
	ctx, cancel := context.WithTimeout(context.Background(), runLockWait)
	defer cancel()
	ok, err := l.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !ok {
		_ = l.Close()
		return nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow gates: another gates command holds %s (%v)", l.Path(), err)}
	}
	return func() {
		_ = l.Unlock()
		_ = l.Close()
	}, nil
}

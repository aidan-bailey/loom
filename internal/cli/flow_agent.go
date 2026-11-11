package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/sched"
	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
)

// agentRunSchema is the agent-run record's version (spec §5.5).
const agentRunSchema = 1

// An agent run's statuses beside a report's own (spec §5.5), and its
// record's types, which internal/flowrun holds for internal/flowview too.
const (
	agentRunning   = flowrun.AgentRunning
	agentFailed    = flowrun.AgentFailed
	agentCancelled = flowrun.AgentCancelled
)

// agentRun is one `loom flow agent run` and every job after it on its
// Claude session: <state>/agents/<id>.json (flowrun.AgentRecord).
type agentRun = flowrun.AgentRecord

// agentJob is one job of a run: its start, or an answer resuming it.
type agentJob = flowrun.AgentRecordJob

func newFlowAgentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "agent", Short: "Run a flow agent headless as a job, wait for its report, answer its question"}
	cmd.AddCommand(newFlowAgentRunCmd(), newFlowAgentWaitCmd(), newFlowAgentAnswerCmd(),
		newFlowAgentShowCmd(), newFlowAgentListCmd(), newFlowAgentCancelCmd())
	return cmd
}

// agentDir is the worktree's agents dir, and the flowConf's cleanup.
func agentDir() (string, func(), error) {
	conf, cleanup, err := newFlowConf()
	if err != nil {
		return "", nil, err
	}
	dir, err := flowStateDir(conf, "agents")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

func newFlowAgentRunCmd() *cobra.Command {
	var effort, name string
	cmd := &cobra.Command{
		Use:   "run ROLE [--effort E] [--name N] < BRIEF",
		Short: "Queue flow agent ROLE as a headless agent job, its brief on stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			root, err := flowRootOfSession()
			if err != nil {
				return err
			}
			role := args[0]
			if _, ok := agentDefinition(root, role); !ok {
				return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow agent: the flow plugin has no agent %s", role)}
			}
			if name != "" && !flowName.MatchString(name) {
				return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow agent: bad name %q", name)}
			}
			brief, err := io.ReadAll(io.LimitReader(c.InOrStdin(), maxBrief+1))
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the brief: %v", err)}
			}
			cwd, err := os.Getwd()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the working directory: %v", err)}
			}
			argv, err := agentArgv(root, role, effort, cwd, brief)
			if err != nil {
				return err
			}
			dir, cleanup, err := agentDir()
			if err != nil {
				return err
			}
			defer cleanup()
			return flowDaemon(c, func(d *client) error {
				return d.startAgent(c.OutOrStdout(), dir, &agentRun{Schema: agentRunSchema, ID: name, Role: role, Effort: effort, Cwd: cwd}, argv, brief)
			})
		},
	}
	cmd.Flags().StringVar(&effort, "effort", "", "The effort to run it at, in place of its pinned one")
	cmd.Flags().StringVar(&name, "name", "", "The run's name (default: ROLE-N)")
	return cmd
}

// startAgent names run, refuses a name whose run hasn't ended, writes the
// brief, queues the first agent job and saves the record.
func (d *client) startAgent(out io.Writer, dir string, run *agentRun, argv []string, brief []byte) error {
	unlock, err := lockAgents(dir)
	if err != nil {
		return err
	}
	defer unlock()
	if run.ID == "" {
		for n := 1; ; n++ {
			id := fmt.Sprintf("%s-%d", run.Role, n)
			if _, err := os.Stat(agentPath(dir, id)); errors.Is(err, fs.ErrNotExist) {
				run.ID = id
				break
			}
		}
	} else if old, err := readAgentRun(agentPath(dir, run.ID)); err == nil && !old.Ended() {
		return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow agent: %s hasn't ended (loom flow agent wait %s follows it)", run.ID, run.ID)}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %v; the record is left as it is", err)}
	}
	runDir := filepath.Join(dir, run.ID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
	}
	run.Brief = filepath.Join(runDir, "brief.md")
	if err := config.AtomicWriteFile(run.Brief, brief, 0o600); err != nil {
		return &ExitError{Code: exitNotRun, Msg: "loom flow agent: the brief: " + err.Error()}
	}
	id, err := d.queueAgent(run, argv)
	if err != nil {
		return err
	}
	run.Jobs, run.Status = []agentJob{{ID: id}}, agentRunning
	if err := saveAgentRun(dir, run); err != nil {
		_ = d.cancelJobs([]sched.JobID{id})
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the record: %v, so job %d was cancelled", err, id)}
	}
	fmt.Fprintf(out, "started %s: flow:%s, job %d\n", run.ID, run.Role, id)
	return nil
}

// queueAgent submits argv as run's agent job in run's directory.
func (d *client) queueAgent(run *agentRun, argv []string) (sched.JobID, error) {
	sub := flowJob(argv, fmt.Sprintf("agent %s: flow:%s", run.ID, run.Role), run.Cwd, 0)
	sub.Env, sub.Kind = agentEnv(sub.Env, run.Role), sched.KindAgent
	id, err := submitFlowJob(d, sub)
	if err != nil {
		return 0, &ExitError{Code: exitNotRun, Msg: "loom flow agent: nothing was queued: " + err.Error()}
	}
	return id, nil
}

func newFlowAgentWaitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wait [ID] [SECONDS]",
		Short: "Wait up to SECONDS (170, at most) for an agent run's job to end, then say how the run stands",
		Long: "Exits 0 when the run ended with a report (DONE, DONE_WITH_CONCERNS, NEEDS_CONTEXT or BLOCKED), 75 while " +
			"it runs, 1 when it ended without a usable report (failed or cancelled), and 125 when it can't follow it. A " +
			"daemon lost mid-wait (a restart) is joined again within SECONDS, saying so on stderr; still away then, it exits 75.",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			name, secs := runArgs(args)
			bound, err := waitSeconds(secs)
			if err != nil {
				return &ExitError{Code: exitUsage, Msg: "loom flow agent: " + err.Error()}
			}
			dir, cleanup, err := agentDir()
			if err != nil {
				return err
			}
			defer cleanup()
			if name, err = latestAgent(dir, name); err != nil {
				return err
			}
			deadline := time.Now().Add(bound)
			err = rejoin(c, deadline, 0, sayOnStderr(c), func(d *client) error { return d.waitAgent(c.OutOrStdout(), dir, name, deadline) })
			var away *outOfReachError
			if errors.As(err, &away) {
				fmt.Fprintf(c.OutOrStdout(), "running %s: its job goes on, but %v (loom flow agent wait %s follows it again)\n", name, away, name)
				return &ExitError{Code: exitStillRunning}
			}
			if up := (*daemonUpgradedError)(nil); errors.As(err, &up) {
				return up.exit("loom flow agent", "loom flow agent wait "+name)
			}
			return err
		},
	}
}

// waitAgent waits until deadline for run id's current job to end, records
// its result when it has, and prints how the run stands (spec §5.3). It is
// safe to run again on a daemon joined again (rejoin): settleAgent records
// an end once, under the lock.
func (d *client) waitAgent(out io.Writer, dir, id string, deadline time.Time) error {
	run, err := readAgentRun(agentPath(dir, id))
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %v; the record is left as it is", err)}
	}
	if !run.Ended() {
		v, known, err := d.jobWithin(run.Current().ID, time.Until(deadline))
		if err != nil {
			return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: job %d goes on, but %v (loom flow agent wait %s follows it again)", run.Current().ID, err, id)}
		}
		if known && !v.State.Finished() {
			fmt.Fprintf(out, "running %s: job %d %s\n", id, v.ID, standing(d.Queue(), v))
			return &ExitError{Code: exitStillRunning}
		}
		if run, err = d.settleAgent(dir, id, v, known); err != nil {
			return err
		}
	}
	return printAgentEnd(out, dir, run)
}

// settleAgent records the end of run id's current job, under the lock.
func (d *client) settleAgent(dir, id string, v core.JobView, known bool) (*agentRun, error) {
	unlock, err := lockAgents(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	run, err := readAgentRun(agentPath(dir, id))
	if err != nil {
		return nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %v; the record is left as it is", err)}
	}
	if run.Ended() || run.Current().Read {
		return run, nil
	}
	job := run.Current()
	job.Read = true
	switch {
	case !known:
		run.Status, run.Why = agentFailed, fmt.Sprintf("the queue no longer knows job %d", job.ID)
	case v.State == sched.Cancelled:
		run.Status, run.Why = agentCancelled, strings.TrimSpace("job was cancelled "+v.CancelReason)
	default:
		// The view was read first, so the log of an ended job is whole.
		log, err := os.ReadFile(v.LogPath)
		if err != nil {
			run.Status, run.Why = agentFailed, fmt.Sprintf("its output can't be read: %v", err)
			break
		}
		res, err := readResult(log)
		if res.Session != "" {
			run.Session = res.Session
		}
		run.CostUSD += res.CostUSD
		run.Turns += res.Turns
		if err != nil {
			line, _ := jobOutcome(v)
			run.Status, run.Why = agentFailed, fmt.Sprintf("%v (%s)", err, line)
			break
		}
		run.Status, run.Report, run.Question = res.Status, res.Report, res.Question
		if err := config.AtomicWriteFile(filepath.Join(dir, id, "report.md"), []byte(strings.TrimRight(res.Report, "\n")+"\n"), 0o600); err != nil {
			run.Why = "report.md: " + err.Error()
		}
	}
	if err := saveAgentRun(dir, run); err != nil {
		return nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the record: %v", err)}
	}
	return run, nil
}

// printAgentEnd prints an ended run's line and exits as it ended.
func printAgentEnd(out io.Writer, dir string, run *agentRun) error {
	switch run.Status {
	case agentFailed:
		fmt.Fprintf(out, "failed %s: %s\n", run.ID, run.Why)
		return &ExitError{Code: 1}
	case agentCancelled:
		fmt.Fprintf(out, "cancelled %s\n", run.ID)
		return &ExitError{Code: 1}
	}
	fmt.Fprintf(out, "done %s: %s\n", run.ID, run.Status)
	if run.Status == "NEEDS_CONTEXT" {
		fmt.Fprintf(out, "question: %s\n", oneLine(run.Question))
	}
	fmt.Fprintf(out, "report: %s\n", filepath.Join(dir, run.ID, "report.md"))
	return nil
}

func newFlowAgentAnswerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "answer ID < ANSWER",
		Short: "Resume an agent run at NEEDS_CONTEXT, BLOCKED or TURN_CAP with an answer on stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			root, err := flowRootOfSession()
			if err != nil {
				return err
			}
			answer, err := io.ReadAll(io.LimitReader(c.InOrStdin(), maxBrief+1))
			switch {
			case err != nil:
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the answer: %v", err)}
			case strings.TrimSpace(string(answer)) == "":
				return &ExitError{Code: exitUsage, Msg: "loom flow agent: the answer is empty; give it on stdin"}
			case len(answer) > maxBrief:
				return &ExitError{Code: exitUsage, Msg: "loom flow agent: the answer is at most 48 KiB"}
			}
			dir, cleanup, err := agentDir()
			if err != nil {
				return err
			}
			defer cleanup()
			return flowDaemon(c, func(d *client) error {
				return d.answerAgent(c.OutOrStdout(), dir, args[0], root, string(answer))
			})
		},
	}
}

// answerAgent queues a job resuming run id's session with answer.
func (d *client) answerAgent(out io.Writer, dir, id, root, answer string) error {
	unlock, err := lockAgents(dir)
	if err != nil {
		return err
	}
	defer unlock()
	run, err := readAgentRun(agentPath(dir, id))
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %v; the record is left as it is", err)}
	}
	if run.Status != "NEEDS_CONTEXT" && run.Status != "BLOCKED" && run.Status != agentTurnCap {
		return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow agent: %s is %s; only a run at NEEDS_CONTEXT, BLOCKED or TURN_CAP takes an answer", id, run.Status)}
	}
	settings, err := runSettings(run.Cwd)
	if err != nil {
		return err
	}
	definition, _ := agentDefinition(root, run.Role)
	argv := agentCommand(agentProgram(), run.Role, run.Effort, frontmatterMaxTurns(definition), agentPermissionMode(), root, settings, answer, run.Session)
	jobID, err := d.queueAgent(run, argv)
	if err != nil {
		return err
	}
	run.Jobs = append(run.Jobs, agentJob{ID: jobID, Answer: answer})
	run.Status, run.Question, run.Report = agentRunning, "", ""
	if err := saveAgentRun(dir, run); err != nil {
		_ = d.cancelJobs([]sched.JobID{jobID})
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the record: %v, so job %d was cancelled", err, jobID)}
	}
	fmt.Fprintf(out, "resumed %s: job %d\n", id, jobID)
	return nil
}

func newFlowAgentShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [ID]",
		Short: "Print an agent run's record",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			loomErrors(c)
			dir, cleanup, err := agentDir()
			if err != nil {
				return err
			}
			defer cleanup()
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name, err = latestAgent(dir, name); err != nil {
				return err
			}
			run, err := readAgentRun(agentPath(dir, name))
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %v; the record is left as it is", err)}
			}
			w := c.OutOrStdout()
			fmt.Fprintf(w, "%s: flow:%s, %s\n", run.ID, run.Role, run.Status)
			if run.Effort != "" {
				fmt.Fprintf(w, "effort: %s\n", run.Effort)
			}
			fmt.Fprintf(w, "session: %s\nbrief: %s\n", run.Session, run.Brief)
			for _, j := range run.Jobs {
				if j.Answer == "" {
					fmt.Fprintf(w, "job %d: the start\n", j.ID)
				} else {
					fmt.Fprintf(w, "job %d: answered %q\n", j.ID, oneLine(j.Answer))
				}
			}
			if run.Question != "" {
				fmt.Fprintf(w, "question: %s\n", oneLine(run.Question))
			}
			if run.Why != "" {
				fmt.Fprintf(w, "why: %s\n", run.Why)
			}
			fmt.Fprintf(w, "cost: $%.2f over %d turns\n", run.CostUSD, run.Turns)
			if run.Report != "" {
				fmt.Fprintf(w, "report:\n%s\n", strings.TrimRight(run.Report, "\n"))
			}
			return nil
		},
	}
}

func newFlowAgentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List this worktree's agent runs",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			dir, cleanup, err := agentDir()
			if err != nil {
				return err
			}
			defer cleanup()
			paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
			}
			for _, p := range paths {
				run, err := readAgentRun(p)
				if err != nil {
					fmt.Fprintf(c.OutOrStdout(), "%s: unreadable (%v)\n", filepath.Base(p), err)
					continue
				}
				fmt.Fprintf(c.OutOrStdout(), "%s  flow:%s  %s  %d jobs  $%.2f\n", run.ID, run.Role, run.Status, len(run.Jobs), run.CostUSD)
			}
			return nil
		},
	}
}

func newFlowAgentCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel [ID]",
		Short: "Cancel an agent run's job that hasn't ended",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			dir, cleanup, err := agentDir()
			if err != nil {
				return err
			}
			defer cleanup()
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name, err = latestAgent(dir, name); err != nil {
				return err
			}
			return flowDaemon(c, func(d *client) error {
				run, cancelled, err := d.cancelAgentRun(dir, name)
				switch {
				case run == nil:
					return err
				case !cancelled:
					fmt.Fprintf(c.OutOrStdout(), "nothing to cancel: %s is %s\n", name, run.Status)
					return nil
				}
				fmt.Fprintf(c.OutOrStdout(), "cancelled %s: job %d\n", name, run.Current().ID)
				return err
			})
		},
	}
}

// cancelAgentRun cancels agent run name's job if it hasn't ended and marks
// the run cancelled, under the lock: the run, whether it was cancelled (a
// run that has ended is left as it is), and the refusal of its job's
// cancel. A nil run is a record it couldn't read.
func (d *client) cancelAgentRun(dir, name string) (*agentRun, bool, error) {
	unlock, err := lockAgents(dir)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	run, err := readAgentRun(agentPath(dir, name))
	if err != nil {
		return nil, false, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %v; the record is left as it is", err)}
	}
	if run.Ended() {
		return run, false, nil
	}
	// A job that has ended already is only marked: its cancel would be
	// refused.
	var refused error
	d.events()
	if v, ok := jobOf(d.Queue(), run.Current().ID); ok && !v.State.Finished() {
		refused = d.cancelJobs([]sched.JobID{v.ID})
	}
	run.Current().Read = true
	run.Status = agentCancelled
	if err := saveAgentRun(dir, run); err != nil {
		return nil, false, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the record: %v", err)}
	}
	if refused != nil {
		return run, true, &ExitError{Code: exitNotRun}
	}
	return run, true, nil
}

func agentPath(dir, id string) string { return flowrun.AgentPath(dir, id) }

// latestAgent is name checked, or dir's latest run when name is "". A name
// with no record is exit 125.
func latestAgent(dir, name string) (string, error) {
	if name != "" {
		if !flowName.MatchString(name) {
			return "", &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow agent: bad name %q", name)}
		}
		if _, err := os.Stat(agentPath(dir, name)); err != nil {
			return "", &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: no agent run %s here", name)}
		}
		return name, nil
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return "", &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
	}
	var latest string
	var at time.Time
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(at) {
			latest, at = strings.TrimSuffix(filepath.Base(p), ".json"), info.ModTime()
		}
	}
	if latest == "" {
		return "", &ExitError{Code: exitUsage, Msg: "loom flow agent: no agent run here; loom flow agent run starts one"}
	}
	return latest, nil
}

// readAgentRun reads a record, refusing one it can't decode or of another schema.
func readAgentRun(path string) (*agentRun, error) { return flowrun.ReadAgent(path) }

// saveAgentRun writes r whole (a temp file renamed).
func saveAgentRun(dir string, r *agentRun) error { return flowrun.SaveAgent(dir, r) }

// lockAgents takes the agents dir's lock, so no two commands write a
// record at once; the function it returns releases it.
func lockAgents(dir string) (func(), error) {
	l := flock.New(filepath.Join(dir, "agents.lock"))
	ctx, cancel := context.WithTimeout(context.Background(), runLockWait)
	defer cancel()
	ok, err := l.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !ok {
		_ = l.Close()
		return nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: another command holds %s (%v)", l.Path(), err)}
	}
	return func() {
		_ = l.Unlock()
		_ = l.Close()
	}, nil
}

// runSettings is an agent job's --settings for a run in cwd: the git guard,
// and the account's and the project's plugins turned off (spec §2a, 11).
func runSettings(cwd string) (string, error) {
	guard, err := agentGuard()
	if err != nil {
		return "", &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
	}
	off, err := pluginsOff(os.Getenv("CLAUDE_CONFIG_DIR"), cwd)
	if err != nil {
		return "", &ExitError{Code: exitNotRun, Msg: "loom flow agent: the plugins to turn off: " + err.Error()}
	}
	settings, err := agentSettings(guard, off)
	if err != nil {
		return "", &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
	}
	return settings, nil
}

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
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/sched"
	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
)

// engineArgv is the argv of a run's engine job (flow in loom part 3b,
// planning amendment 10): this binary's `flow run engine`. Tests replace it.
var engineArgv = func(id string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return []string{exe, "flow", "run", "engine", id}, nil
}

// envFlowRun marks the jobs a run submits (planning amendment 1).
const envFlowRun = "LOOM_FLOW_RUN"

func newFlowRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a plan with the run engine: it drives the stories, and parks on every question for the manager",
	}
	cmd.AddCommand(newFlowRunStartCmd(), newFlowRunEngineCmd(), newFlowRunWaitCmd(), newFlowRunAnswerCmd(),
		newFlowRunShowCmd(), newFlowRunListCmd(), newFlowRunCancelCmd(), newFlowRunResumeCmd())
	return cmd
}

// runDirs is the worktree's runs, agents and gates dirs, and the conf's
// cleanup.
type runDirs struct {
	conf                flowConf
	runs, agents, gates string
	cleanup             func()
}

func openRunDirs() (*runDirs, error) {
	conf, cleanup, err := newFlowConf()
	if err != nil {
		return nil, err
	}
	d := &runDirs{conf: conf, cleanup: cleanup}
	for _, p := range []struct {
		dir *string
		sub string
	}{{&d.runs, "runs"}, {&d.agents, "agents"}, {&d.gates, "gates"}} {
		if *p.dir, err = flowStateDir(conf, p.sub); err != nil {
			cleanup()
			return nil, err
		}
	}
	return d, nil
}

func runStatePath(dir, id string) string { return filepath.Join(dir, id+".json") }

// loadRun reads run id's state, an exit 125 when it can't.
func loadRun(dir, id string) (*flowrun.State, error) {
	st, err := flowrun.Load(runStatePath(dir, id))
	if err != nil {
		return nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow run: %v; the state is left as it is", err)}
	}
	return st, nil
}

// lockRunState takes the runs dir's lock: a command holds it to change a
// state, and an engine for its life.
func lockRunState(dir string) (func(), error) {
	l := flock.New(filepath.Join(dir, "runs.lock"))
	ctx, cancel := context.WithTimeout(context.Background(), runLockWait)
	defer cancel()
	ok, err := l.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !ok {
		_ = l.Close()
		return nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow run: another command or an engine holds %s (%v)", l.Path(), err)}
	}
	return func() {
		_ = l.Unlock()
		_ = l.Close()
	}, nil
}

// latestRun is id checked, or dir's latest run when id is "".
func latestRun(dir, id string) (string, error) {
	if id != "" {
		if !flowName.MatchString(id) {
			return "", &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow run: bad run %q", id)}
		}
		if _, err := os.Stat(runStatePath(dir, id)); err != nil {
			return "", &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow run: no run %s here", id)}
		}
		return id, nil
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return "", &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
	}
	var latest string
	var at time.Time
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(at) {
			latest, at = strings.TrimSuffix(filepath.Base(p), ".json"), info.ModTime()
		}
	}
	if latest == "" {
		return "", &ExitError{Code: exitUsage, Msg: "loom flow run: no run here; loom flow run start starts one"}
	}
	return latest, nil
}

func optionalArg(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return ""
}

// queueEngine queues st's engine job in cwd: a run job, in this shell's
// environment less the Claude session's variables.
func (d *client) queueEngine(st *flowrun.State, cwd string) error {
	argv, err := engineArgv(st.ID)
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
	}
	sub := flowJob(argv, "flow run "+st.ID, cwd, 0)
	sub.Kind = sched.KindRun
	var env []string
	for _, kv := range sub.Env {
		if name, _, _ := strings.Cut(kv, "="); !scrubbedVar(name) && name != envFlowRun {
			env = append(env, kv)
		}
	}
	sub.Env = env
	id, err := submitFlowJob(d, sub)
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: "loom flow run: the engine was not queued: " + err.Error()}
	}
	st.Engine = int64(id)
	return nil
}

var leadingDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-`)

// planStem is plan's file name without .md.
func planStem(plan string) string { return strings.TrimSuffix(filepath.Base(plan), ".md") }

func newFlowRunStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start PLAN",
		Short: "Check the repo and the plan, write the run's state, and queue its engine",
		Long: "Refuses (exit 2) on a dirty tree, a doctor or check-plan FAIL, and while another run here hasn't " +
			"ended; exit 125 outside a session or when nothing was queued. Prints `started <run>, job <N>`.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			root, err := flowRootOfSession()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			}
			dirs, err := openRunDirs()
			if err != nil {
				return err
			}
			defer dirs.cleanup()
			tools := newRunTools(root)
			git := &runFX{cwd: cwd}
			if status, err := git.git("status", "--porcelain"); err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			} else if status != "" {
				return &ExitError{Code: exitUsage, Msg: "loom flow run: the tree isn't clean:\n" + status}
			}
			out, ok, err := tools.Doctor()
			switch {
			case err != nil:
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			case !ok:
				return &ExitError{Code: exitUsage, Msg: "loom flow run: loom flow conf doctor failed:\n" + failLines(out)}
			}
			plan := args[0]
			raw, fails, err := tools.CheckPlan(plan)
			switch {
			case err != nil:
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			case fails != "":
				return &ExitError{Code: exitUsage, Msg: "loom flow run: loom flow check-plan failed:\n" + strings.TrimSpace(fails)}
			}
			var checked struct {
				Spec    string          `json:"spec"`
				Stories []flowrun.Story `json:"stories"`
			}
			if err := json.Unmarshal(raw, &checked); err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: check_plan.py --json: " + err.Error()}
			}
			show, err := tools.Show()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			}
			services, err := tools.Services()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			}
			roster, err := os.ReadFile(filepath.Join(root, flow.RosterFile))
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: the roster: " + err.Error()}
			}
			base, err := git.Head()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			}
			text, err := os.ReadFile(plan)
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: the plan: " + err.Error()}
			}
			blocks := splitBlocks(show)
			st := &flowrun.State{
				Schema: flowrun.Schema, Status: flowrun.Running, Plan: plan, Spec: checked.Spec, Base: base,
				Start:   time.Now().UTC().Format(time.RFC3339),
				Roster:  "loom flow roster " + strings.TrimSpace(string(roster)),
				Profile: blockLine(blocks["supervisor"], "Profile: "), DesignStage: designStage(string(text)),
				RecordCopy: strings.TrimSuffix(blockLine(blocks["supervisor"], "Record copy in the repo: "), "none"),
				Blocks: flowrun.Blocks{Developer: blocks["developer"], Verifier: blocks["verifier"], Reviewer: blocks["reviewer"],
					Supervisor: blocks["supervisor"], Services: services},
				Stories: checked.Stories, Memo: map[string]json.RawMessage{},
			}
			if s := os.Getenv("CLAUDE_CODE_SESSION_ID"); s != "" {
				st.Managers = []string{s}
			}
			unlock, err := lockRunState(dirs.runs)
			if err != nil {
				return err
			}
			defer unlock()
			if active, err := activeRunState(dirs.runs); err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			} else if active != "" {
				return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow run: run %s hasn't ended here (loom flow run show %s)", active, active)}
			}
			st.ID = planStem(plan)
			for n := 2; ; n++ {
				if _, err := os.Stat(runStatePath(dirs.runs, st.ID)); errors.Is(err, fs.ErrNotExist) {
					break
				}
				st.ID = fmt.Sprintf("%s-%d", planStem(plan), n)
			}
			path := runStatePath(dirs.runs, st.ID)
			if err := flowrun.Save(path, st); err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: the state: " + err.Error()}
			}
			err = flowDaemon(c, func(d *client) error { return d.queueEngine(st, cwd) })
			if err != nil {
				_ = os.Remove(path)
				return err
			}
			if err := flowrun.Save(path, st); err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: the state: " + err.Error()}
			}
			fmt.Fprintf(c.OutOrStdout(), "started %s, job %d\n", st.ID, st.Engine)
			return nil
		},
	}
}

// failLines is out's FAIL lines.
func failLines(out string) string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "FAIL") {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// splitBlocks is `conf show --for supervisor`'s output by its [role]
// headings.
func splitBlocks(show string) map[string]string {
	blocks := map[string]string{}
	var name string
	var body []string
	flush := func() {
		if name != "" {
			blocks[name] = strings.TrimRight(strings.Join(body, "\n"), "\n")
		}
	}
	for _, l := range strings.Split(show, "\n") {
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			flush()
			name, body = strings.Trim(l, "[]"), nil
			continue
		}
		body = append(body, l)
	}
	flush()
	return blocks
}

// blockLine is the value of block's line starting with label, or "".
func blockLine(block, label string) string {
	for _, l := range strings.Split(block, "\n") {
		if v, ok := strings.CutPrefix(l, label); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// designStage is the text after the plan's first `**Design stage:**`.
func designStage(plan string) string {
	for _, l := range strings.Split(plan, "\n") {
		if v, ok := strings.CutPrefix(l, "**Design stage:**"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// activeRunState is the ID of a run in dir that hasn't ended, or "". A
// state it can't read is an error, never passed over.
func activeRunState(dir string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return "", err
	}
	for _, p := range paths {
		st, err := flowrun.Load(p)
		if err != nil {
			return "", err
		}
		if !flowrun.Ended(st.Status) {
			return st.ID, nil
		}
	}
	return "", nil
}

func newFlowRunEngineCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "engine RUN",
		Short:  "The run's engine, as its run job runs it",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			if job := os.Getenv(sched.EnvJob); job != "" {
				_ = os.Setenv(envFlowRun, job)
			}
			root, err := flowRootOfSession()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			}
			dirs, err := openRunDirs()
			if err != nil {
				return err
			}
			defer dirs.cleanup()
			unlock, err := lockRunState(dirs.runs)
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: another engine has this run, or a command holds its lock: " + err.Error()}
			}
			defer unlock()
			id := args[0]
			st, err := loadRun(dirs.runs, id)
			if err != nil || flowrun.Ended(st.Status) {
				return err
			}
			fx := &runFX{c: c, root: root, conf: dirs.conf, tools: newRunTools(root), agents: dirs.agents, gates: dirs.gates,
				runDir: filepath.Join(dirs.runs, id), statePath: runStatePath(dirs.runs, id), cwd: cwd, plan: st.Plan, out: c.OutOrStdout()}
			fx.logf("engine for %s", id)
			if err := flowrun.Run(st, fx); err != nil {
				fx.logf("the engine stopped: %v", err)
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow run: the engine stopped: %v (loom flow run resume %s carries on)", err, id)}
			}
			if flowrun.Ended(st.Status) {
				fx.writeRecord(st)
				if err := fx.Save(st); err != nil {
					return &ExitError{Code: exitNotRun, Msg: "loom flow run: the state: " + err.Error()}
				}
			}
			fx.logf("%s: %s", id, st.Status)
			return nil
		},
	}
}

// writeRecord writes st's record (spec §5.7) and its in-repo copy, and
// keeps its path in st; a failure goes in st.Why.
func (fx *runFX) writeRecord(st *flowrun.State) {
	if err := fx.record(st); err != nil {
		st.Why = "the record: " + err.Error()
		fx.logf("%s", st.Why)
	}
}

func (fx *runFX) record(st *flowrun.State) error {
	cost, err := fx.tools.Costs(st.ID)
	if err != nil {
		cost = "The cost couldn't be read: " + err.Error()
	}
	in := flowrun.RecordInputs{Cost: cost, Models: managerModels(cost)}
	if b, err := os.ReadFile(filepath.Join(fx.root, flow.RosterFile)); err == nil {
		in.RosterEnd = "loom flow roster " + strings.TrimSpace(string(b))
	}
	if in.Head, err = fx.Head(); err != nil {
		return err
	}
	commits, err := fx.Commits(st.Base, in.Head)
	if err != nil {
		return err
	}
	in.Commits = len(commits)
	text := flowrun.Record(st, in)
	key, err := fx.tools.Key()
	if err != nil {
		return err
	}
	data, err := flowDataDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(data, "records", key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	date := st.Start
	if len(date) >= 10 {
		date = date[:10]
	}
	base := date + "-" + leadingDate.ReplaceAllString(planStem(st.Plan), "")
	name := base + ".md"
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(dir, name)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s-%d.md", base, n)
	}
	path := filepath.Join(dir, name)
	if err := config.AtomicWriteFile(path, []byte(text), 0o600); err != nil {
		return err
	}
	st.Record = path
	if st.RecordCopy != "" {
		top, err := fx.git("rev-parse", "--show-toplevel")
		if err != nil {
			return err
		}
		copyDir := filepath.Join(top, st.RecordCopy)
		if err := os.MkdirAll(copyDir, 0o755); err != nil {
			return err
		}
		if err := config.AtomicWriteFile(filepath.Join(copyDir, name), []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

var managersComment = regexp.MustCompile(`<!-- agent-costs-managers (\{.*?\}) -->`)

// managerModels is the models each manager session used, from the costs
// output's managers comment.
func managerModels(cost string) map[string][]string {
	out := map[string][]string{}
	if m := managersComment.FindStringSubmatch(cost); m != nil {
		_ = json.Unmarshal([]byte(m[1]), &out)
	}
	return out
}

func newFlowRunWaitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wait [RUN]",
		Short: "Wait, with no time cap, until a run parks on a question or ends, and say which",
		Long: "Meant for a background Bash. Exits 0 when the run has parked or ended done or stopped, 1 when it ended " +
			"cancelled or its engine was lost (loom flow run resume carries on), 125 when it can't follow the run. A " +
			"daemon lost mid-wait (a restart) is joined again, saying so on stderr, for up to 10 minutes.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			dirs, err := openRunDirs()
			if err != nil {
				return err
			}
			defer dirs.cleanup()
			id, err := latestRun(dirs.runs, optionalArg(args))
			if err != nil {
				return err
			}
			err = rejoin(c, time.Time{}, rejoinFor, sayOnStderr(c), func(d *client) error { return d.waitFlowRun(c.OutOrStdout(), dirs.runs, id) })
			var away *outOfReachError
			if errors.As(err, &away) {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow run: the run goes on, but %v (loom flow run wait %s follows it again)", away, id)}
			}
			return err
		},
	}
}

// waitFlowRun follows run id until it parks or ends, and prints where it stands.
func (d *client) waitFlowRun(out io.Writer, dir, id string) error {
	for {
		st, err := loadRun(dir, id)
		if err != nil {
			return err
		}
		if st.Status != flowrun.Running {
			return printRunStop(out, st)
		}
		engine := sched.JobID(st.Engine)
		v, known := jobOf(d.Queue(), engine)
		if !known {
			if v, err = d.awaitJob(engine); err == nil {
				known = true
			} else if !errors.Is(err, errNotReported) {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow run: %v (loom flow run wait %s follows it again)", err, id)}
			}
		}
		if known && !v.State.Finished() {
			if _, _, err = d.jobWithin(engine, 5*time.Second); err != nil {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow run: the run goes on, but %v (loom flow run wait %s follows it again)", err, id)}
			}
			continue
		}
		// The engine's job has ended: what it left is in the state.
		if st, err = loadRun(dir, id); err != nil {
			return err
		}
		if st.Status != flowrun.Running {
			return printRunStop(out, st)
		}
		fmt.Fprintln(out, lostEngine(id, engine, v, known))
		return &ExitError{Code: 1}
	}
}

// lostEngine is the line for run id whose engine, job engine with view v
// (known: the queue holds it), ended without parking: how it ended and why,
// with the last line its log holds, so a manager doesn't resume into the
// same wall.
func lostEngine(id string, engine sched.JobID, v core.JobView, known bool) string {
	if !known {
		return fmt.Sprintf("lost %s: the engine (job %d) is no longer in the queue; loom flow run resume %s carries on", id, engine, id)
	}
	ended := "ended " + stateText(v)
	for _, why := range []string{v.CancelReason, v.Failure} {
		if why = oneLine(why); why != "" {
			ended += " (" + why + ")"
		}
	}
	if last := lastLogLine(v.LogPath); last != "" {
		ended += " without parking; its log ends: " + last
	} else {
		ended += " without parking"
	}
	return fmt.Sprintf("lost %s: the engine (job %d) %s; loom flow run resume %s carries on", id, engine, ended, id)
}

// lastLogLine is the last non-empty line of the log at path, read from its
// last logTail bytes and cut to lineLimit characters; "" when there is
// none or it can't be read.
func lastLogLine(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > logTail {
		if _, err := f.Seek(-logTail, io.SeekEnd); err != nil {
			return ""
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if r := []rune(strings.TrimSpace(lines[i])); len(r) > 0 {
			return string(r[:min(len(r), lineLimit)])
		}
	}
	return ""
}

// logTail is how much of a log's end lastLogLine reads, lineLimit how much
// of its line it keeps.
const (
	logTail   = 16 << 10
	lineLimit = 300
)

// printRunStop prints a parked or ended run, and exits as it stands.
func printRunStop(out io.Writer, st *flowrun.State) error {
	if st.Status == flowrun.Parked && st.Question != nil {
		q := st.Question
		where := ""
		if q.Story != "" {
			where = " (story " + q.Story + ")"
		}
		fmt.Fprintf(out, "parked %s: %s%s\n%s\n", st.ID, q.Kind, where, strings.TrimSpace(q.Text))
		if len(q.Paths) > 0 {
			fmt.Fprintf(out, "read: %s\n", strings.Join(q.Paths, " "))
		}
		fmt.Fprintf(out, "answer with: %s (loom flow run answer %s --from spec|owner < answer)\n", flowrun.Takes(q.Kind), st.ID)
		return nil
	}
	done := 0
	for _, s := range st.Stories {
		if s.Row.Status == "done" {
			done++
		}
	}
	fmt.Fprintf(out, "ended %s: %s\nstories: %d of %d\n", st.ID, st.Status, done, len(st.Stories))
	if st.Record != "" {
		fmt.Fprintf(out, "record: %s\n", st.Record)
	}
	if st.Why != "" {
		fmt.Fprintf(out, "note: %s\n", st.Why)
	}
	if st.Status == flowrun.Cancelled {
		return &ExitError{Code: 1}
	}
	return nil
}

func newFlowRunAnswerCmd() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "answer RUN --from spec|owner < ANSWER",
		Short: "Answer a parked run's question, on stdin, and queue its engine again",
		Long: "--from says where the answer came from: spec when you settled it without the owner (from the spec, " +
			"the plan or the code), owner when you asked the owner. The run's record counts them.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			if from != flowrun.FromSpec && from != flowrun.FromOwner {
				return &ExitError{Code: exitUsage, Msg: "loom flow run: --from spec or --from owner: say whether the spec settled it or you asked the owner"}
			}
			text, err := io.ReadAll(io.LimitReader(c.InOrStdin(), maxBrief+1))
			switch {
			case err != nil:
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow run: the answer: %v", err)}
			case strings.TrimSpace(string(text)) == "":
				return &ExitError{Code: exitUsage, Msg: "loom flow run: the answer is empty; give it on stdin"}
			case len(text) > maxBrief:
				return &ExitError{Code: exitUsage, Msg: "loom flow run: the answer is at most 48 KiB"}
			}
			cwd, err := os.Getwd()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			}
			dirs, err := openRunDirs()
			if err != nil {
				return err
			}
			defer dirs.cleanup()
			id, err := latestRun(dirs.runs, args[0])
			if err != nil {
				return err
			}
			unlock, err := lockRunState(dirs.runs)
			if err != nil {
				return err
			}
			defer unlock()
			st, err := loadRun(dirs.runs, id)
			if err != nil {
				return err
			}
			if st.Status != flowrun.Parked || st.Question == nil {
				return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow run: %s is %s; only a parked run takes an answer", id, st.Status)}
			}
			word, err := flowrun.CheckAnswer(st.Question.Kind, string(text))
			if err != nil {
				return &ExitError{Code: exitUsage, Msg: "loom flow run: " + err.Error()}
			}
			if word == flowrun.WordContinue {
				git := &runFX{cwd: cwd}
				if status, err := git.git("status", "--porcelain"); err != nil || status != "" {
					return &ExitError{Code: exitUsage, Msg: "loom flow run: continue takes a clean tree; this one isn't:\n" + status}
				}
			}
			head := takenHead(&runFX{cwd: cwd}, st)
			if err := st.Answer(string(text), os.Getenv("CLAUDE_CODE_SESSION_ID"), from, head); err != nil {
				return &ExitError{Code: exitUsage, Msg: "loom flow run: " + err.Error()}
			}
			if head != "" {
				fmt.Fprintf(c.OutOrStdout(), "the run takes HEAD %s with the answer: your commits to its spec and plan\n", shortSHA(head))
			}
			path := runStatePath(dirs.runs, id)
			if err := flowrun.Save(path, st); err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: the state: " + err.Error()}
			}
			if err := flowDaemon(c, func(d *client) error { return d.queueEngine(st, cwd) }); err != nil {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("%v; the answer is kept, and loom flow run resume %s queues the engine", err, id)}
			}
			if err := flowrun.Save(path, st); err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: the state: " + err.Error()}
			}
			fmt.Fprintf(c.OutOrStdout(), "answered %s: engine job %d\n", id, st.Engine)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Where the answer came from: spec (settled without the owner) or owner (you asked the owner)")
	return cmd
}

// takenHead is the HEAD a parked run takes with its answer: the manager's
// own commits to the run's spec and plan since the head the engine recorded
// (Question.At), which would otherwise park its next dispatch as tree-moved
// (the first real runs, 2026-11-06). It is "" when HEAD is still that head,
// the tree isn't clean, HEAD doesn't descend from it, a commit touches
// another file, or git can't say: the engine's tree check then asks.
func takenHead(fx *runFX, st *flowrun.State) string {
	if st.Question == nil || st.Question.At == "" {
		return ""
	}
	at := st.Question.At
	head, err := fx.git("rev-parse", "HEAD")
	if err != nil || head == at {
		return ""
	}
	if status, err := fx.git("status", "--porcelain"); err != nil || status != "" {
		return ""
	}
	if _, err := fx.git("merge-base", "--is-ancestor", at, head); err != nil {
		return ""
	}
	names, err := fx.git("diff", "--name-only", "--no-renames", at, head)
	if err != nil {
		return ""
	}
	for _, name := range strings.Split(names, "\n") {
		if name != st.Plan && (st.Spec == "" || name != st.Spec) {
			return ""
		}
	}
	return head
}

func newFlowRunResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume [RUN]",
		Short: "Queue a running run's engine again after it was lost",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			}
			dirs, err := openRunDirs()
			if err != nil {
				return err
			}
			defer dirs.cleanup()
			id, err := latestRun(dirs.runs, optionalArg(args))
			if err != nil {
				return err
			}
			unlock, err := lockRunState(dirs.runs)
			if err != nil {
				return err
			}
			defer unlock()
			st, err := loadRun(dirs.runs, id)
			if err != nil {
				return err
			}
			if st.Status != flowrun.Running {
				return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow run: %s is %s; resume queues the engine of a running run (a parked one takes loom flow run answer)", id, st.Status)}
			}
			return flowDaemon(c, func(d *client) error {
				if v, ok := jobOf(d.Queue(), sched.JobID(st.Engine)); ok && !v.State.Finished() {
					return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow run: the engine of %s is job %d, which hasn't ended", id, v.ID)}
				}
				if err := d.queueEngine(st, cwd); err != nil {
					return err
				}
				if err := flowrun.Save(runStatePath(dirs.runs, id), st); err != nil {
					return &ExitError{Code: exitNotRun, Msg: "loom flow run: the state: " + err.Error()}
				}
				fmt.Fprintf(c.OutOrStdout(), "resumed %s: engine job %d\n", id, st.Engine)
				return nil
			})
		},
	}
}

func newFlowRunCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel [RUN]",
		Short: "Cancel a run: its engine, its agent run and its gate run, and write its record",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			root, err := flowRootOfSession()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			}
			dirs, err := openRunDirs()
			if err != nil {
				return err
			}
			defer dirs.cleanup()
			id, err := latestRun(dirs.runs, optionalArg(args))
			if err != nil {
				return err
			}
			st, err := loadRun(dirs.runs, id)
			if err != nil {
				return err
			}
			if flowrun.Ended(st.Status) {
				fmt.Fprintf(c.OutOrStdout(), "nothing to cancel: %s is %s\n", id, st.Status)
				return nil
			}
			err = flowDaemon(c, func(d *client) error {
				d.events()
				engine := sched.JobID(st.Engine)
				if v, ok := jobOf(d.Queue(), engine); ok && !v.State.Finished() {
					if err := d.cancelJobs([]sched.JobID{engine}); err != nil {
						return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow run: the engine (job %d) was not cancelled: %v", engine, err)}
					}
					if _, _, err := d.jobWithin(engine, 30*time.Second); err != nil {
						return err
					}
				}
				if n := len(st.Agents); n > 0 {
					if _, _, err := d.cancelAgentRun(dirs.agents, st.Agents[n-1]); err != nil {
						return err
					}
				}
				if n := len(st.Gates); n > 0 {
					if r, err := readRun(runPath(dirs.gates, st.Gates[n-1])); err == nil && !r.ended() {
						if err := d.cancelRun(io.Discard, dirs.gates, st.Gates[n-1]); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			unlock, err := lockRunState(dirs.runs)
			if err != nil {
				return err
			}
			defer unlock()
			if st, err = loadRun(dirs.runs, id); err != nil {
				return err
			}
			st.Status, st.Question = flowrun.Cancelled, nil
			st.End = time.Now().UTC().Format(time.RFC3339)
			fx := &runFX{c: c, root: root, conf: dirs.conf, tools: newRunTools(root), agents: dirs.agents, gates: dirs.gates,
				runDir: filepath.Join(dirs.runs, id), statePath: runStatePath(dirs.runs, id), cwd: cwd, plan: st.Plan, out: io.Discard}
			fx.writeRecord(st)
			if err := fx.Save(st); err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: the state: " + err.Error()}
			}
			fmt.Fprintf(c.OutOrStdout(), "cancelled %s\n", id)
			if st.Record != "" {
				fmt.Fprintf(c.OutOrStdout(), "record: %s\n", st.Record)
			}
			return nil
		},
	}
}

func newFlowRunShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [RUN]",
		Short: "Print a run's phase, stories, open question and the step in flight",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			loomErrors(c)
			dirs, err := openRunDirs()
			if err != nil {
				return err
			}
			defer dirs.cleanup()
			id, err := latestRun(dirs.runs, optionalArg(args))
			if err != nil {
				return err
			}
			st, err := loadRun(dirs.runs, id)
			if err != nil {
				return err
			}
			status, lost := st.Status, ""
			if st.Status == flowrun.Running {
				if lost, err = engineLost(c, dirs.runs, id); err != nil {
					return err
				}
				if lost != "" {
					status = "lost"
				}
			}
			w := c.OutOrStdout()
			fmt.Fprintf(w, "%s: %s, %s\nplan: %s\n", st.ID, status, st.Phase, st.Plan)
			for _, s := range st.Stories {
				r := s.Row
				status := r.Status
				if status == "" {
					status = "not started"
				}
				dev := r.Developer
				if dev == "" {
					dev = s.Developer
				}
				fmt.Fprintf(w, "  Story %-4s %-11s %-17s %d commits, findings %d/%d/%d\n", s.ID, status, dev, r.Commits, r.Findings[0], r.Findings[1], r.Findings[2])
			}
			if q := st.Question; q != nil {
				fmt.Fprintf(w, "question (%s): %s\n", q.Kind, oneLine(q.Text))
				if len(q.Paths) > 0 {
					fmt.Fprintf(w, "read: %s\n", strings.Join(q.Paths, " "))
				}
				fmt.Fprintf(w, "answer with: %s\n", flowrun.Takes(q.Kind))
			}
			if lost != "" {
				fmt.Fprintln(w, lost)
			} else {
				fmt.Fprintf(w, "engine: job %d\n", st.Engine)
			}
			if n := len(st.Agents); n > 0 {
				fmt.Fprintf(w, "last agent run: %s\n", st.Agents[n-1])
			}
			if n := len(st.Gates); n > 0 {
				fmt.Fprintf(w, "last gate run: %s\n", st.Gates[n-1])
			}
			if st.Record != "" {
				fmt.Fprintf(w, "record: %s\n", st.Record)
			}
			return nil
		},
	}
}

// engineLost is lostEngine's line for run id when its state says running
// but its engine's job has ended or left the queue, asked of the daemon
// best-effort: "" when the engine runs, or when the daemon can't be
// reached, so show still prints the state without one.
func engineLost(c *cobra.Command, dir, id string) (string, error) {
	var line string
	var err error
	_ = withDaemon(c, func(d *client) error {
		d.events()
		var st *flowrun.State
		if st, err = loadRun(dir, id); err != nil || st.Status != flowrun.Running || st.Engine == 0 {
			return nil // read again under the daemon's view: an engine that has just parked isn't lost
		}
		engine := sched.JobID(st.Engine)
		if v, known := jobOf(d.Queue(), engine); !known || v.State.Finished() {
			line = lostEngine(id, engine, v, known)
		}
		return nil
	})
	return line, err
}

func newFlowRunListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List this worktree's runs",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			dirs, err := openRunDirs()
			if err != nil {
				return err
			}
			defer dirs.cleanup()
			paths, err := filepath.Glob(filepath.Join(dirs.runs, "*.json"))
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow run: " + err.Error()}
			}
			for _, p := range paths {
				st, err := flowrun.Load(p)
				if err != nil {
					fmt.Fprintf(c.OutOrStdout(), "%s: unreadable (%v)\n", filepath.Base(p), err)
					continue
				}
				done := 0
				for _, s := range st.Stories {
					if s.Row.Status == "done" {
						done++
					}
				}
				kind := ""
				if st.Question != nil {
					kind = "  asks: " + st.Question.Kind
				}
				fmt.Fprintf(c.OutOrStdout(), "%s  %s  %s  %d of %d stories%s\n", st.ID, st.Status, st.Phase, done, len(st.Stories), kind)
			}
			return nil
		},
	}
}

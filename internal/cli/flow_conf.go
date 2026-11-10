package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/spf13/cobra"
)

// flowConf is what flow's Go tools ask flowconf.py (flow in loom part 2,
// spec §5): the worktree's state dir, a gate expansion's lines, and whether
// a step's output is vacuous. Tests replace newFlowConf.
type flowConf interface {
	State() (string, error)
	Gates(args ...string) ([]string, error)
	Vacuous(file string) (match string, vacuous bool, err error)
}

// newFlowConf is the flowConf of this session's flow root (flowRoot), with
// the cleanup that removes a temp copy.
var newFlowConf = func() (flowConf, func(), error) {
	root, cleanup, err := flowRoot()
	if err != nil {
		return nil, nil, err
	}
	return scriptConf{root: root}, cleanup, nil
}

// scriptConf answers from root's lib/flowconf.py.
type scriptConf struct{ root string }

func (s scriptConf) run(args ...string) (stdout, stderr string, code int, err error) {
	var out, errOut bytes.Buffer
	code, err = flowScript(s.root, "flowconf.py", args, strings.NewReader(""), &out, &errOut)
	return out.String(), strings.TrimSpace(errOut.String()), code, err
}

// State is `flowconf.py state`: the state dir, made if missing.
func (s scriptConf) State() (string, error) {
	out, errOut, code, err := s.run("state")
	switch {
	case err != nil:
		return "", fmt.Errorf("loom flow conf state: %w", err)
	case code != 0:
		return "", fmt.Errorf("loom flow conf state exited %d: %s", code, errOut)
	}
	return strings.TrimSpace(out), nil
}

// Gates is `flowconf.py gates ARGS…`'s lines. It exits 1 both when a line
// is FAIL, which is still an expansion, and when it has none to give (no
// profile, git failing), which is an error.
func (s scriptConf) Gates(args ...string) ([]string, error) {
	out, errOut, code, err := s.run(append([]string{"gates"}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("loom flow conf gates: %w", err)
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			lines = append(lines, l)
		}
	}
	failLine := slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "FAIL ") })
	if code > 1 || (code == 1 && !failLine) {
		return nil, fmt.Errorf("loom flow conf gates exited %d: %s", code, errOut)
	}
	return lines, nil
}

// Vacuous is `flowconf.py vacuous FILE`: exit 0 with the matching lines, 1
// for none, anything else an error.
func (s scriptConf) Vacuous(file string) (string, bool, error) {
	out, errOut, code, err := s.run("vacuous", file)
	switch {
	case err != nil:
		return "", false, fmt.Errorf("loom flow conf vacuous: %w", err)
	case code == 0:
		first, _, _ := strings.Cut(out, "\n")
		return first, true, nil
	case code == 1:
		return "", false, nil
	}
	return "", false, fmt.Errorf("loom flow conf vacuous exited %d: %s", code, errOut)
}

// flowScript runs root's lib/<script> with args, stdio as given, in flow's
// environment (flow in loom spec §5.5), and returns its exit code. An error
// is one the script never got to answer: no interpreter, no roster, no data
// dir, a failed start.
func flowScript(root, script string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	interp, argv := "python3", []string{"-I", filepath.Join(root, "lib", script)}
	if strings.HasSuffix(script, ".sh") {
		interp, argv = "bash", argv[1:]
	}
	bin, err := exec.LookPath(interp)
	if err != nil {
		return 0, fmt.Errorf("%s is not on PATH; flow's tools need it", interp)
	}
	roster, err := os.ReadFile(filepath.Join(root, flow.RosterFile))
	if err != nil {
		return 0, err
	}
	data, err := flowDataDir()
	if err != nil {
		return 0, err
	}
	runner := exec.Command(bin, append(argv, args...)...)
	runner.Stdin, runner.Stdout, runner.Stderr = stdin, stdout, stderr
	runner.Env = append(os.Environ(),
		flow.EnvRoot+"="+root, "FLOW_ROOT="+root, "FLOW_DATA="+data,
		"FLOW_ROSTER="+strings.TrimSpace(string(roster)))
	err = runner.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit) && exit.ExitCode() > 0:
		return exit.ExitCode(), nil
	}
	return 0, err
}

// flowStateDir is sub under the worktree's state dir, made if missing.
func flowStateDir(conf flowConf, sub string) (string, error) {
	state, err := conf.State()
	if err != nil {
		return "", &ExitError{Code: exitNotRun, Msg: err.Error()}
	}
	dir := filepath.Join(state, sub)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow: %v", err)}
	}
	return dir, nil
}

// flowDaemon is withDaemon for flow's job commands: a daemon it can't reach
// ends the command with exitNotRun, as a tool that could not run.
func flowDaemon(c *cobra.Command, fn func(d *client) error) error {
	err := withDaemon(c, fn)
	var join *joinError
	if errors.As(err, &join) {
		return &ExitError{Code: exitNotRun, Msg: "loom flow: " + err.Error()}
	}
	return err
}

// flowJob is a flow step's job (spec §5.1, §5.5): argv in dir with this
// shell's environment, owned by its launch, and with no message when it
// ends, since the agent that started it waits on it.
func flowJob(argv []string, desc, dir string, after sched.JobID) sched.Submit {
	return sched.Submit{
		Argv: slices.Clone(argv), Cwd: dir, Env: os.Environ(), Description: cutDescription(desc),
		Tail: defaultTail, Launch: os.Getenv(claudemod.EnvLaunch), After: after,
	}
}

// cutDescription is s on one line, cut to descriptionLimit characters.
func cutDescription(s string) string {
	r := []rune(oneLine(s))
	if len(r) > descriptionLimit {
		r = r[:descriptionLimit]
	}
	return string(r)
}

// submitFlowJob submits sub. Tests replace it to refuse one.
var submitFlowJob = func(d *client, sub sched.Submit) (sched.JobID, error) { return d.submitJob(sub) }

// cancelJobs asks the daemon to cancel ids, the last first, so a held job
// is cancelled before the job it runs after. Each refusal is said on
// stderr; the first is returned.
func (d *client) cancelJobs(ids []sched.JobID) error {
	var first error
	for _, id := range slices.Backward(ids) {
		if err := d.request(func(req core.ReqID) { d.CancelJob(id, core.CancelFromCLI, req) }); err != nil {
			d.say("job %d was not cancelled: %v (loom job cancel %d cancels it)", id, err, id)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// runTools runs flow's scripts for loom flow run (flow in loom part 3b);
// tests replace newRunTools.
type runTools interface {
	// Doctor is `flowconf.py doctor`'s output, and whether it has no FAIL.
	Doctor() (out string, ok bool, err error)
	// CheckPlan is `check_plan.py --json PLAN`'s JSON, or its FAIL lines.
	CheckPlan(plan string) (json []byte, fails string, err error)
	Show() (string, error)       // flowconf.py show --for supervisor
	Services() ([]string, error) // flowconf.py services
	Key() (string, error)        // flowconf.py key
	// Premises is plan_premises.py's output when it flagged anything, else "".
	Premises(plan, rev string) (string, error)
	Conformance(plan, story, base, head string) (string, error)
	DocsCheck(base, head string) ([]string, error) // flowconf.py docs-check: the paths that aren't docs
	Costs(run string) (string, error)              // agent_costs.py run RUN
}

// newRunTools is the runTools of the flow root root.
var newRunTools = func(root string) runTools { return scriptTools{root: root} }

// scriptTools runs flow's scripts from root.
type scriptTools struct{ root string }

// run is script's stdout, its exit code, and an error naming it when it
// could not run or exited with a code not in ok.
func (s scriptTools) run(script string, ok []int, args ...string) (string, int, error) {
	var out, errOut bytes.Buffer
	code, err := flowScript(s.root, script, args, strings.NewReader(""), &out, &errOut)
	switch {
	case err != nil:
		return "", 0, fmt.Errorf("%s: %w", script, err)
	case !slices.Contains(ok, code):
		return "", code, fmt.Errorf("%s exited %d: %s", script, code, strings.TrimSpace(errOut.String()))
	}
	return out.String(), code, nil
}

func (s scriptTools) Doctor() (string, bool, error) {
	out, _, err := s.run("flowconf.py", []int{0, 1}, "doctor")
	if err != nil {
		return "", false, err
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "FAIL") {
			return out, false, nil
		}
	}
	return out, true, nil
}

func (s scriptTools) CheckPlan(plan string) ([]byte, string, error) {
	out, code, err := s.run("check_plan.py", []int{0, 1}, "--json", plan)
	if err != nil {
		return nil, "", err
	}
	if code == 1 {
		return nil, out, nil
	}
	return []byte(out), "", nil
}

func (s scriptTools) Show() (string, error) {
	out, _, err := s.run("flowconf.py", []int{0}, "show", "--for", "supervisor")
	return out, err
}

func (s scriptTools) Services() ([]string, error) {
	out, _, err := s.run("flowconf.py", []int{0}, "services")
	if err != nil {
		return nil, err
	}
	var services []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			services = append(services, l)
		}
	}
	return services, nil
}

func (s scriptTools) Key() (string, error) {
	out, _, err := s.run("flowconf.py", []int{0}, "key")
	return strings.TrimSpace(out), err
}

func (s scriptTools) Premises(plan, rev string) (string, error) {
	out, code, err := s.run("plan_premises.py", []int{0, 1}, plan, "--rev", rev)
	if err != nil || code == 0 {
		return "", err
	}
	return out, nil
}

func (s scriptTools) Conformance(plan, story, base, head string) (string, error) {
	out, _, err := s.run("plan_conformance.py", []int{0, 1}, plan, "--story", story, "--base", base, "--head", head)
	return out, err
}

func (s scriptTools) DocsCheck(base, head string) ([]string, error) {
	out, _, err := s.run("flowconf.py", []int{0, 1}, "docs-check", "--base", base, "--head", head)
	if err != nil {
		return nil, err
	}
	var strays []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			strays = append(strays, l)
		}
	}
	return strays, nil
}

func (s scriptTools) Costs(run string) (string, error) {
	out, _, err := s.run("agent_costs.py", []int{0}, "run", run)
	return out, err
}

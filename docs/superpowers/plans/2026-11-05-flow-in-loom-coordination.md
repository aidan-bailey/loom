# Flow in loom, part 2: coordination — Implementation Plan

> **For agentic workers:** executed inline (owner's choice, 2026-11-05) with superpowers:executing-plans, package by package, no checkpoint pauses. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `agent-wait.sh` with Go tools on loom's work queue: `loom flow wait` with visible job IDs and a poll refusal, and `loom flow gates`, a gate runner that chains a story's or the final gates and judges each step.

**Architecture:** Two new files in `internal/cli` beside `flow.go`, reusing the `loom job` client (`submitJob`, the replica's `core.JobView`s, `CancelJob`, `jobOutcome`). Flow's Python answers stay behind a small `flowConf` seam (state dir, gate expansion, vacuous check), which tests replace with a fake, so the Go tests need no Python. The prompts change to call the new tools; `agent-wait.sh` retires.

**Tech Stack:** Go (cobra, testify, `github.com/gofrs/flock`), the hermetic test daemon of `internal/cli` (`serveDaemon`, `sched.TestRunner`), bash/Python under `flow/lib`.

**Spec:** `docs/superpowers/specs/2026-11-05-flow-in-loom-coordination-design.md` (c2aee1cc).

**Rules that bind every package:**
- Scratch files go in the session scratchpad, never the worktree; commit with `git commit -m … -- <paths>`, never `-a`, `git add -A` or `.`.
- Never run loom itself (`./loom`, `go run .`) in a pane.
- Builds and test suites go through `loom job` (`/nix/store/wiwj13gf0qbgk9amrp9b1gcgrhlwk92a-loom-0.14.0/bin/loom` if the `loom` on PATH is refused as a different build); `go vet`, `gofmt` and single-package test runs are quick enough to run directly.
- `flow/CLAUDE.md`: no agent's model, effort, maxTurns, tools or cache lifetime changes; run `bash flow/lib/test.sh` after changing anything under `flow/`.
- `internal/cli/CLAUDE.md`: exit with a job's own code through `*ExitError`; follow a job by reading its view before its log.

---

## Planning amendments (they win where the spec differs)

1. **One run lock per state dir**, `<state>/gates/gates.lock`, not one per run: `start`'s one-active-run check and its submits must be atomic against another `start`.
2. **The record holds `cwd`**, the directory `start` ran in; every job of the run, resubmitted ones included, runs there.
3. **`gates wait` reads a lone all-digit argument as seconds**; run names are `final` or `story-…`.
4. **Job lists print as `jobs 1, 2, 5`**: other sessions' jobs interleave, so a run's IDs needn't be consecutive.
5. **`gates cancel` on a run that has ended changes nothing** and says so.
6. **`wait wait` on a job the queue no longer knows prints `lost <name>: the queue no longer knows job N`** and exits 70.
7. **A story chain continues past a VACUOUS step and a FAIL (infra) from the vacuous check**: their jobs ended done, and the daemon releases a dependent on done.
8. **`wait start` and `gates start` exit 125 when they can't reach the daemon** (`flowDaemon`), as a tool that could not run.

## File structure

| File | Responsibility |
|---|---|
| `internal/cli/flow_conf.go` (new) | `flowConf` and its script implementation; `flowScript` (moved from `runFlowTool`); the shared helpers: `flowStateDir`, `flowDaemon`, `flowJob`, `submitFlowJob`, `cancelJobs` |
| `internal/cli/flow_wait.go` (new) | `loom flow wait start|wait`, the poll refusal, the bounded job wait |
| `internal/cli/flow_gates.go` (new) | `loom flow gates start|wait|cancel`, the run record, verdicts, resubmission, the run lock |
| `internal/cli/flow.go` | `runFlowTool` on `flowScript`; `wait` leaves the script table; the new commands attach |
| `internal/cli/flow_conf_test.go`, `flow_wait_test.go`, `flow_gates_test.go` (new) | the tests |
| `flow/plugin/agents/verifier.md`, `plan-execution-supervisor.md`, `skills/habits/SKILL.md`, `docs/dispatch.md` | the prompts (spec §5.7) |
| `flow/lib/agent-wait.sh`, `agent_wait_test.py` (deleted); `doctor.py`, `banned-tokens.txt`, `check_agents_test.py` | the retirement (spec §5.8) |
| `flow/README.md`, `USAGE.md`, `internal/cli/CLAUDE.md`, the spec | docs |

---

## P1 — The seam and `loom flow wait` in Go

**Files:** create `internal/cli/flow_conf.go`, `internal/cli/flow_wait.go`, `internal/cli/flow_conf_test.go`, `internal/cli/flow_wait_test.go`; modify `internal/cli/flow.go`.

- [ ] **Step 1: Commit the plan and the spec's amendments.** Add to the spec, after §2, a section `## 2a. Planning amendments (2026-11-05)` that lists this plan's eight amendments in one line each, then commit both:

```bash
git commit -m "docs(plans): flow in loom, part 2, in four packages" -- docs/superpowers/plans/2026-11-05-flow-in-loom-coordination.md docs/superpowers/specs/2026-11-05-flow-in-loom-coordination-design.md
```

(`git add` the new plan file by its path first.)

- [ ] **Step 2: Write the failing tests.** `internal/cli/flow_conf_test.go`:

```go
package cli

import (
	"testing"

	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConf is a flowConf with the test's answers: its own state dir, the
// gate lines it is given, and a vacuous check that finds nothing unless the
// test says otherwise. It records each Gates call's arguments.
type fakeConf struct {
	state    string
	gates    []string
	gatesErr error
	vacuous  func(file string) (string, bool, error)
	gateArgs [][]string
}

func (f *fakeConf) State() (string, error) { return f.state, nil }

func (f *fakeConf) Gates(args ...string) ([]string, error) {
	f.gateArgs = append(f.gateArgs, args)
	return f.gates, f.gatesErr
}

func (f *fakeConf) Vacuous(file string) (string, bool, error) {
	if f.vacuous == nil {
		return "", false, nil
	}
	return f.vacuous(file)
}

// useFakeConf makes newFlowConf answer with a fakeConf for the test.
func useFakeConf(t *testing.T) *fakeConf {
	t.Helper()
	f := &fakeConf{state: t.TempDir()}
	old := newFlowConf
	newFlowConf = func() (flowConf, func(), error) { return f, func() {}, nil }
	t.Cleanup(func() { newFlowConf = old })
	return f
}

// flowJobsDaemon serves a test daemon for flow's job commands, with no
// launch and a fake flowconf whose state dir is the test's own.
func flowJobsDaemon(t *testing.T) (*testDaemon, *fakeConf) {
	t.Helper()
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	return d, useFakeConf(t)
}

const fakeFlowconf = `import sys
cmd = sys.argv[1]
if cmd == "state":
    print("/the/state")
elif cmd == "gates" and sys.argv[2] == "--final":
    print("RUN echo a")
    print("FAIL cargo doc {packages} (cannot expand {packages}: no metadata)")
    sys.exit(1)
elif cmd == "gates":
    print("flowconf: no usable profile", file=sys.stderr)
    sys.exit(1)
elif cmd == "vacuous" and sys.argv[2] == "hollow":
    print("0 passed")
    print("0 passed again")
elif cmd == "vacuous" and sys.argv[2] == "fine":
    sys.exit(1)
else:
    print("flowconf: bad pattern", file=sys.stderr)
    sys.exit(2)
`

func TestScriptConf_ReadsFlowconfsAnswers(t *testing.T) {
	needPython(t)
	conf := scriptConf{root: fakeFlowRoot(t, map[string]string{"flowconf.py": fakeFlowconf})}

	state, err := conf.State()
	require.NoError(t, err)
	assert.Equal(t, "/the/state", state)

	lines, err := conf.Gates("--final")
	require.NoError(t, err, "exit 1 with a FAIL line is still an expansion")
	assert.Equal(t, []string{"RUN echo a", "FAIL cargo doc {packages} (cannot expand {packages}: no metadata)"}, lines)
	_, err = conf.Gates("--story", "abc")
	assert.ErrorContains(t, err, "no usable profile", "exit 1 with no FAIL line is an error")

	match, vacuous, err := conf.Vacuous("hollow")
	require.NoError(t, err)
	assert.True(t, vacuous)
	assert.Equal(t, "0 passed", match)
	_, vacuous, err = conf.Vacuous("fine")
	require.NoError(t, err)
	assert.False(t, vacuous)
	_, _, err = conf.Vacuous("broken")
	assert.ErrorContains(t, err, "bad pattern")
}

func TestFlowDaemon_ADaemonItCantReachExits125(t *testing.T) {
	useFakeConf(t)
	old := Join
	Join = nil
	t.Cleanup(func() { Join = old })
	_, _, err := runFlow(t, "wait", "start", "build", "--", "true")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "no way to reach the loom daemon")
}
```

`internal/cli/flow_wait_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlowWait_StartQueuesTheStepAndWaitReportsItsExit(t *testing.T) {
	d, conf := flowJobsDaemon(t)

	out, _, err := runFlow(t, "wait", "start", "build", "--", "sh", "-c", "echo out; exit 3")
	require.NoError(t, err)
	logPath := filepath.Join(conf.state, "agent-wait", "build.log")
	assert.Equal(t, "started build: job 1; log "+logPath+"\n", out)
	j := d.job(1)
	assert.Equal(t, []string{"sh", "-c", "echo out; exit 3"}, j.Argv)
	assert.Equal(t, "build: sh -c 'echo out; exit 3'", j.Description)

	out, _, err = runFlow(t, "wait", "wait", "build", "60")
	assert.Equal(t, 3, flowExit(err), "the job's own exit code")
	assert.Equal(t, "done build exit 3\n", out)
	b, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "out\n", string(b))
}

func TestFlowWait_ABoundedWaitSaysWhereTheJobStandsAndTheLogGrows(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "slow", "--", "sh", "-c", "echo one; sleep 4; echo two")
	require.NoError(t, err)
	d.waitLog(t, 1, "one")
	logPath := filepath.Join(conf.state, "agent-wait", "slow.log")

	out, _, err := runFlow(t, "wait", "wait", "slow", "0")
	assert.Equal(t, exitStillRunning, flowExit(err))
	assert.Regexp(t, `^running slow: job 1 running \d+s\n$`, out)
	b, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "one\n", string(b))

	out, _, err = runFlow(t, "wait", "wait", "slow", "60")
	require.NoError(t, err)
	assert.Equal(t, "done slow exit 0\n", out)
	b, err = os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", string(b), "each call adds only what the job's output gained")
}

func TestFlowWait_ACancelledJobIsLost(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "slow", "--", "sh", "-c", "echo up; sleep 30")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	_, _, err = runJob(t, "cancel", "1")
	require.NoError(t, err)

	out, _, err := runFlow(t, "wait", "wait", "slow", "60")
	assert.Equal(t, exitLost, flowExit(err))
	assert.Regexp(t, `^lost slow: job 1 was cancelled`, out)
}

func TestFlowWait_RefusesANameWhoseJobHasntEnded(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "slow", "--", "sh", "-c", "echo up; sleep 30")
	require.NoError(t, err)

	_, _, err = runFlow(t, "wait", "start", "slow", "--", "true")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "slow is still running as job 1")

	_, _, err = runJob(t, "cancel", "1")
	require.NoError(t, err)
	d.waitFinished(t, 1)
	out, _, err := runFlow(t, "wait", "start", "slow", "--", "true")
	require.NoError(t, err, "a name whose job has ended starts again")
	assert.Contains(t, out, "started slow: job 2;")
}

func TestFlowWait_FollowsAJobByIDAndSaysWhenTheQueueDoesntKnowIt(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	_, _, err := runJob(t, "submit", "--no-notify", "--", "sh", "-c", "echo x")
	require.NoError(t, err)

	out, _, err := runFlow(t, "wait", "wait", "--job", "1", "60")
	require.NoError(t, err)
	assert.Equal(t, "done job-1 exit 0\n", out)
	b, err := os.ReadFile(filepath.Join(conf.state, "agent-wait", "job-1.log"))
	require.NoError(t, err)
	assert.Equal(t, "x\n", string(b))

	out, _, err = runFlow(t, "wait", "wait", "--job", "99")
	assert.Equal(t, exitLost, flowExit(err))
	assert.Equal(t, "lost job-99: the queue no longer knows job 99\n", out)
}

func TestFlowWait_RefusesPollsAndLoomCommands(t *testing.T) {
	d, _ := flowJobsDaemon(t)
	_, _, err := runFlow(t, "wait", "start", "poll", "--", "bash", "-c", "until grep -q x f; do sleep 10; done")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "a poll isn't a step")
	_, _, err = runFlow(t, "wait", "start", "nested", "--", "loom", "job", "run", "--", "make")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "already a job")
	assert.Empty(t, d.jobs(), "a refused step queues nothing")
}

func TestFlowWait_IsPoll(t *testing.T) {
	for _, c := range []struct {
		argv []string
		poll bool
	}{
		{[]string{"sleep", "5"}, true},
		{[]string{"/run/current-system/sw/bin/sleep", "5"}, true},
		{[]string{"bash", "-c", "until grep -q x f; do sleep 10; done"}, true},
		{[]string{"sh", "-c", "while :; do sleep 1; done"}, true},
		{[]string{"zsh", "-lc", "while true\ndo\n  sleep 2\ndone"}, true},
		{[]string{"bash", "--norc", "-c", "until [ -s f ]; do sleep 10; done"}, true},
		{[]string{"cargo", "test", "sleep_tests"}, false},
		{[]string{"bash", "-c", "sleep 2 && make"}, false},
		{[]string{"bash", "-c", "until make; do :; done"}, false},
		{[]string{"bash", "-c", "while read f; do make $f; done < list; sleep 1"}, false},
		{[]string{"grep", "-c", "until x; do sleep 1; done", "file"}, false},
		{[]string{"bash", "script.sh", "-c", "while x; do sleep 1; done"}, false},
	} {
		assert.Equal(t, c.poll, isPoll(c.argv), "%q", c.argv)
	}
}

func TestFlowWait_Usage(t *testing.T) {
	useFakeConf(t)
	for _, args := range [][]string{
		{"wait", "start", "build"},
		{"wait", "start", "build", "true"},
		{"wait", "start", "bad name", "--", "true"},
		{"wait", "wait"},
		{"wait", "wait", "--job", "1", "x", "60"},
		{"wait", "wait", "--job", "0"},
		{"wait", "wait", "build", "171"},
	} {
		_, _, err := runFlow(t, args...)
		assert.Equal(t, exitUsage, flowExit(err), "%q", args)
	}
	_, _, err := runFlow(t, "wait", "wait", "never")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "nothing started as never")
}

func TestFlowStanding(t *testing.T) {
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 1, State: sched.Running, Started: time.Now().Add(-70 * time.Second)},
		{ID: 2, State: sched.Queued},
		{ID: 3, State: sched.Queued},
		{ID: 4, State: sched.Held, Waiting: "after job 1 (running)"},
		{ID: 5, State: sched.Done},
	}}
	assert.Equal(t, "running 1m10s", standing(q, q.Jobs[0]))
	assert.Equal(t, "queued (2nd in line)", standing(q, q.Jobs[2]))
	assert.Equal(t, "held: runs after job 1 (running)", standing(q, q.Jobs[3]))
	assert.Equal(t, "ended done", standing(q, q.Jobs[4]))
}
```

- [ ] **Step 3: Run them to see them fail.** `CGO_ENABLED=0 go test ./internal/cli -run 'TestScriptConf|TestFlowDaemon|TestFlowWait|TestFlowStanding'`. Expected: build failure (`scriptConf`, `newFlowConf`, `exitStillRunning`, `isPoll`, `standing` … undefined).

- [ ] **Step 4: Write `internal/cli/flow_conf.go`.**

```go
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
```

- [ ] **Step 5: Move `runFlowTool` onto `flowScript`, and attach the new commands.** In `internal/cli/flow.go`, delete the `wait` entry from `flowTools`, add `newFlowWaitCmd(), newFlowGatesCmd()` to the `cmd.AddCommand(…)` call in `newFlowCmd` (P2 adds `newFlowGatesCmd`; until then add only `newFlowWaitCmd()`), and replace `runFlowTool`'s body:

```go
// runFlowTool runs tool's script from a flow root, its stdio the
// command's, and ends with the script's exit code (an *ExitError for a
// non-zero one).
func runFlowTool(c *cobra.Command, tool flowTool, args []string) error {
	root, cleanup, err := flowRoot()
	if err != nil {
		return err
	}
	defer cleanup()
	code, err := flowScript(root, tool.script, append(slices.Clone(tool.args), args...), c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr())
	switch {
	case err != nil:
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow %s: %v", tool.name, err)}
	case code != 0:
		return &ExitError{Code: code}
	}
	return nil
}
```

Add `"slices"` to `flow.go`'s imports; drop any import the compiler then reports unused.

- [ ] **Step 6: Write `internal/cli/flow_wait.go`.**

```go
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
			"job. Each call copies what the job's output gained to the step's log.",
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
			return flowDaemon(c, func(d *client) error { return d.waitStep(c.OutOrStdout(), name, id, logPath, bound) })
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

// waitStep waits up to bound for job id to end, copies what its output
// gained to logPath, and says how it stands as label (spec §5.6).
func (d *client) waitStep(out io.Writer, label string, id sched.JobID, logPath string, bound time.Duration) error {
	v, known, err := d.jobWithin(id, bound)
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
```

- [ ] **Step 7: Run the tests, gofmt and vet.** `gofmt -l internal/cli && CGO_ENABLED=0 go vet ./internal/cli && CGO_ENABLED=0 go test ./internal/cli -run 'TestScriptConf|TestFlowDaemon|TestFlowWait|TestFlowStanding|TestFlowTool'`. Expected: no gofmt output, vet clean, PASS.

- [ ] **Step 8: Mutation-check the poll refusal**, one term at a time, reverting each: (a) drop the `sleep` argv[0] case → `TestFlowWait_IsPoll` fails on `sleep 5`; (b) accept any program in `shellScript` → fails on the `grep -c` case; (c) drop `\bdo\b.*?` and the `done` from `pollLoop` → fails on `while read f … ; sleep 1`; (d) drop `\bsleep\b.*?` → fails on `until make; do :; done`. Then mutation-check the name guard: drop the `ok && !v.State.Finished()` check in `startStep` → `TestFlowWait_RefusesANameWhoseJobHasntEnded` fails.

- [ ] **Step 9: Commit.**

```bash
git add internal/cli/flow_conf.go internal/cli/flow_wait.go internal/cli/flow_conf_test.go internal/cli/flow_wait_test.go
git commit -m "feat(cli): loom flow wait in Go, on loom's queue with its job IDs, refusing polls" -- internal/cli/flow.go internal/cli/flow_conf.go internal/cli/flow_wait.go internal/cli/flow_conf_test.go internal/cli/flow_wait_test.go
```

---

## P2 — `loom flow gates`

**Files:** create `internal/cli/flow_gates.go`, `internal/cli/flow_gates_test.go`; modify `internal/cli/flow.go` (attach `newFlowGatesCmd()`).

- [ ] **Step 1: Write the failing tests.** `internal/cli/flow_gates_test.go`:

```go
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readTestRun is run name's record in the fake state dir.
func readTestRun(t *testing.T, conf *fakeConf, name string) *gateRun {
	t.Helper()
	r, err := readRun(runPath(filepath.Join(conf.state, "gates"), name))
	require.NoError(t, err)
	return r
}

func TestFlowGates_AStoryRunChainsItsGatesAndPasses(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo one", "SKIPPED cargo doc {packages} (the story changed no Cargo package)", "RUN echo two"}

	out, _, err := runFlow(t, "gates", "start", "--story", "abc1234", "def5678")
	require.NoError(t, err)
	assert.Equal(t, "started story-abc1234: 3 steps, jobs 1, 2\n2 SKIPPED: cargo doc {packages} (the story changed no Cargo package)\n", out)
	assert.Equal(t, [][]string{{"--story", "abc1234", "def5678"}}, conf.gateArgs)
	assert.Equal(t, []string{"bash", "-c", "echo one"}, d.job(1).Argv)
	assert.Equal(t, "gate 1/3: echo one", d.job(1).Description)
	assert.Equal(t, sched.JobID(1), d.job(2).After, "each gate runs after the one before")

	_, _, err = runFlow(t, "gates", "start", "--final")
	assert.Equal(t, exitUsage, flowExit(err), "one run at a time in a worktree")
	assert.ErrorContains(t, err, "run story-abc1234 hasn't ended here")

	out, _, err = runFlow(t, "gates", "wait", "60")
	require.NoError(t, err)
	assert.Equal(t, "story-abc1234: 3 steps\n1 PASS: echo one\n2 SKIPPED: cargo doc {packages} (the story changed no Cargo package)\n3 PASS: echo two\nended: 2 PASS, 1 SKIPPED\n", out)
	b, err := os.ReadFile(filepath.Join(conf.state, "gates", "story-abc1234", "1.log"))
	require.NoError(t, err)
	assert.Equal(t, "one\n", string(b))

	out, _, err = runFlow(t, "gates", "cancel", "story-abc1234")
	require.NoError(t, err)
	assert.Equal(t, "nothing to cancel: story-abc1234 has ended\n", out)
	_, _, err = runFlow(t, "gates", "start", "--final")
	require.NoError(t, err, "a run that has ended no longer holds the worktree")
}

func TestFlowGates_AStoryStopsAtItsFirstFail(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo bad; exit 3", "RUN echo never", "FAIL cargo test {packages} (cannot expand {packages}: no metadata)", "RUN echo after"}

	out, _, err := runFlow(t, "gates", "start", "--story", "abc")
	require.NoError(t, err)
	assert.Contains(t, out, "started story-abc: 4 steps, jobs 1, 2\n")
	assert.Contains(t, out, "4 SKIPPED: echo after (not run: gate 3 failed)\n", "no RUN line after the expansion's first FAIL is chained")

	out, _, err = runFlow(t, "gates", "wait", "60")
	assert.Equal(t, 1, flowExit(err))
	assert.Regexp(t, `1 FAIL: echo bad; exit 3 \(job 1 failed in \S+ \(exit 3\); log \S+/gates/story-abc/1\.log\)\n`, out)
	assert.Contains(t, out, "2 SKIPPED: echo never (not run: gate 1 failed)\n")
	assert.Contains(t, out, "3 FAIL: cargo test {packages} (cannot expand {packages}: no metadata)\n")
	assert.Contains(t, out, "ended: 2 FAIL, 2 SKIPPED\n")
}

func TestFlowGates_AFinalFailQueuesTheRestAgain(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN exit 3", "RUN echo two", "RUN echo three"}
	_, _, err := runFlow(t, "gates", "start", "--final")
	require.NoError(t, err)

	out, _, err := runFlow(t, "gates", "wait", "final", "0")
	assert.Equal(t, exitStillRunning, flowExit(err), "a bounded wait returns before the run ends")
	assert.Contains(t, out, "not ended: call loom flow gates wait final again\n")

	out, _, err = runFlow(t, "gates", "wait", "final", "60")
	assert.Equal(t, 1, flowExit(err), "a later call resumes the run from its record")
	assert.Contains(t, out, "1 FAIL: exit 3")
	assert.Contains(t, out, "2 PASS: echo two\n")
	assert.Contains(t, out, "3 PASS: echo three\n")
	run := readTestRun(t, conf, "final")
	require.Len(t, run.Steps[1].Jobs, 2, "gate 2 ran in a second generation")
	again := run.Steps[1].Jobs[1]
	assert.Equal(t, sched.Cancelled, d.job(run.Steps[1].Jobs[0]).State)
	assert.Equal(t, sched.JobID(0), d.job(again).After, "the new chain's first gate runs after nothing")
	assert.Equal(t, again, d.job(run.Steps[2].job()).After)
}

func TestFlowGates_EachVerdictComesFromTheJobsEnd(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo 0 tests ran", "RUN echo checker breaks", "RUN kill -KILL $$", "RUN echo fine"}
	conf.vacuous = func(file string) (string, bool, error) {
		b, err := os.ReadFile(file)
		require.NoError(t, err)
		switch {
		case strings.Contains(string(b), "0 tests ran"):
			return "0 tests ran", true, nil
		case strings.Contains(string(b), "checker breaks"):
			return "", false, errors.New("bad pattern")
		}
		return "", false, nil
	}
	_, _, err := runFlow(t, "gates", "start", "--final")
	require.NoError(t, err)

	out, _, err := runFlow(t, "gates", "wait", "60")
	assert.Equal(t, 1, flowExit(err))
	assert.Regexp(t, `1 VACUOUS: echo 0 tests ran \(0 tests ran; log \S+\)\n`, out)
	assert.Regexp(t, `2 FAIL \(infra\): echo checker breaks \(the vacuous check failed: bad pattern; log \S+\)\n`, out)
	assert.Regexp(t, `3 FAIL \(killed\): kill -KILL \$\$ \(job 3 was killed by SIGKILL after \S+; log \S+\)\n`, out)
	assert.Contains(t, out, "4 PASS: echo fine\n", "the gate after a killed one runs again")
}

func TestFlowGates_CancelEndsARunAndFreesTheWorktree(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo up; sleep 30", "RUN echo two"}
	_, _, err := runFlow(t, "gates", "start", "--story", "abc")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")

	out, _, err := runFlow(t, "gates", "wait", "1")
	assert.Equal(t, exitStillRunning, flowExit(err))
	assert.Regexp(t, `1 running \d+s: echo up; sleep 30\n`, out)
	assert.Contains(t, out, "2 held after gate 1: echo two\n")

	out, _, err = runFlow(t, "gates", "cancel")
	require.NoError(t, err)
	assert.Equal(t, "cancelled story-abc: jobs 1, 2\n", out)
	assert.Equal(t, sched.Cancelled, d.waitFinished(t, 1).State)
	assert.Equal(t, sched.Cancelled, d.waitFinished(t, 2).State)

	out, _, err = runFlow(t, "gates", "wait")
	assert.Equal(t, 1, flowExit(err))
	assert.Contains(t, out, "1 cancelled: echo up; sleep 30\n")
	assert.Contains(t, out, "\ncancelled\n")
	_, _, err = runFlow(t, "gates", "start", "--final")
	require.NoError(t, err, "a cancelled run no longer holds the worktree")
}

// A gate cancelled by someone else, while the one before it still ran, is
// an infra failure, not a gate skipped after a failure.
func TestFlowGates_AGateCancelledFromOutsideIsAnInfraFail(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo up; sleep 2", "RUN echo two"}
	_, _, err := runFlow(t, "gates", "start", "--story", "abc")
	require.NoError(t, err)
	_, _, err = runJob(t, "cancel", "2")
	require.NoError(t, err)
	d.waitFinished(t, 1)

	out, _, err := runFlow(t, "gates", "wait", "60")
	assert.Equal(t, 1, flowExit(err))
	assert.Contains(t, out, "1 PASS: echo up; sleep 2\n")
	assert.Regexp(t, `2 FAIL \(infra\): echo two \(job 2 was cancelled`, out)
}

func TestFlowGates_TwoWaitsQueueTheRestOnce(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN exit 3", "RUN echo two"}
	_, _, err := runFlow(t, "gates", "start", "--final")
	require.NoError(t, err)

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := runFlow(t, "gates", "wait", "final", "60")
			codes[i] = flowExit(err)
		}()
	}
	wg.Wait()
	assert.Equal(t, []int{1, 1}, codes)
	assert.Len(t, d.jobs(), 3, "the gate after the failure was queued again once")
}

func TestFlowGates_AChainItCantQueueWholeIsCancelledAndLeavesNoRecord(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo up; sleep 30", "RUN echo two"}
	calls := 0
	old := submitFlowJob
	submitFlowJob = func(c *client, sub sched.Submit) (sched.JobID, error) {
		if calls++; calls == 2 {
			return 0, errors.New("refused for the test")
		}
		return old(c, sub)
	}
	t.Cleanup(func() { submitFlowJob = old })

	_, _, err := runFlow(t, "gates", "start", "--story", "abc")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "gate 2 was not queued (refused for the test), so the 1 queued before it were cancelled")
	assert.Equal(t, sched.Cancelled, d.waitFinished(t, 1).State)
	records, err := filepath.Glob(filepath.Join(conf.state, "gates", "*.json"))
	require.NoError(t, err)
	assert.Empty(t, records)
}

func TestFlowGates_ARecordItCantReadIsLeftAsItIs(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	dir := filepath.Join(conf.state, "gates")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	bad := filepath.Join(dir, "final.json")
	require.NoError(t, os.WriteFile(bad, []byte("{not json"), 0o600))

	_, _, err := runFlow(t, "gates", "wait", "final")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "the record is left as it is")
	_, _, err = runFlow(t, "gates", "start", "--story", "abc")
	assert.Equal(t, exitNotRun, flowExit(err), "nor is it guessed past")
	b, err := os.ReadFile(bad)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(b))
}

func TestFlowGates_AJobTheQueueNoLongerKnowsIsAnInfraFail(t *testing.T) {
	_, conf := flowJobsDaemon(t)
	dir := filepath.Join(conf.state, "gates")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, saveRun(dir, &gateRun{Schema: gateRunSchema, Name: "final", Kind: "final",
		Steps: []gateStep{{Line: "RUN echo x", Run: true, Jobs: []sched.JobID{99}}}}))

	out, _, err := runFlow(t, "gates", "wait", "final")
	assert.Equal(t, 1, flowExit(err))
	assert.Contains(t, out, "1 FAIL (infra): echo x (the queue no longer knows job 99)\n")
}

func TestFlowGates_StartRefusesAnExpansionItCantRead(t *testing.T) {
	d, conf := flowJobsDaemon(t)
	conf.gates = []string{"RUN echo one", "MAYBE something"}
	_, _, err := runFlow(t, "gates", "start", "--final")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, `"MAYBE something"`)

	conf.gates, conf.gatesErr = nil, errors.New("loom flow conf gates exited 1: no usable profile")
	_, _, err = runFlow(t, "gates", "start", "--final")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "no usable profile")
	assert.Empty(t, d.jobs())
}

func TestFlowGates_Usage(t *testing.T) {
	useFakeConf(t)
	for _, args := range [][]string{
		{"gates", "start"},
		{"gates", "start", "--story"},
		{"gates", "start", "--final", "abc"},
		{"gates", "start", "--story", "--final", "abc"},
		{"gates", "wait", "final", "999"},
		{"gates", "wait", "bad name"},
	} {
		_, _, err := runFlow(t, args...)
		assert.Equal(t, exitUsage, flowExit(err), "%q", args)
	}
	_, _, err := runFlow(t, "gates", "wait")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "no gate run here")
}

func TestFlowGates_RunArgsAndSlug(t *testing.T) {
	for _, c := range []struct{ args []string; name, secs string }{
		{nil, "", ""},
		{[]string{"60"}, "", "60"},
		{[]string{"final"}, "final", ""},
		{[]string{"story-abc", "5"}, "story-abc", "5"},
	} {
		name, secs := runArgs(c.args)
		assert.Equal(t, c.name, name, "%q", c.args)
		assert.Equal(t, c.secs, secs, "%q", c.args)
	}
	assert.Equal(t, "abc1234def56", runSlug("abc1234def5678"))
	assert.Equal(t, "HEAD-2", runSlug("HEAD~2"))
}
```

(Gofmt will spread the anonymous struct in `TestFlowGates_RunArgsAndSlug` over lines.)

- [ ] **Step 2: Run them to see them fail.** `CGO_ENABLED=0 go test ./internal/cli -run TestFlowGates`. Expected: build failure (`gateRun`, `readRun`, `runPath`, `saveRun`, `runArgs`, `runSlug` … undefined).

- [ ] **Step 3: Write `internal/cli/flow_gates.go`.**

```go
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
	Line    string        `json:"line"`              // as the expansion printed it
	Run     bool          `json:"run,omitempty"`     // a RUN line, which a job runs
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
// job will run, SKIPPED and FAIL lines judged as printed, and in a story
// run every RUN line after the first FAIL SKIPPED.
func (r *gateRun) plan(lines []string) error {
	failedAt := 0
	for _, line := range lines {
		st := gateStep{Line: line}
		word, _, _ := strings.Cut(line, " ")
		switch word {
		case "none:":
			continue
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
			return fmt.Errorf("loom flow gates: the expansion printed %q, which is no RUN, SKIPPED or FAIL line", line)
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
			"before; a story's chain stops at the expansion's first FAIL line. Prints the run's name and jobs. " +
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
// run's directory, and adds each job to its step. A submit refused partway
// cancels the jobs already queued and adds none.
func (d *client) submitChain(run *gateRun, steps []int) ([]sched.JobID, error) {
	var ids []sched.JobID
	var after sched.JobID
	for _, i := range steps {
		cmd := run.Steps[i].text()
		sub := flowJob([]string{"bash", "-c", cmd}, fmt.Sprintf("gate %d/%d: %s", i+1, len(run.Steps), cmd), run.Cwd, after)
		id, err := submitFlowJob(d, sub)
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
// check for a job that exited 0.
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
	default:
		st.Verdict, st.Why = verdictInfra, line
	}
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
```

- [ ] **Step 4: Attach the command.** In `newFlowCmd`, the `AddCommand` call gains `newFlowGatesCmd()`.

- [ ] **Step 5: Run the tests, gofmt and vet.** `gofmt -l internal/cli && CGO_ENABLED=0 go vet ./internal/cli && CGO_ENABLED=0 go test ./internal/cli -run 'TestFlow|TestScriptConf'`. Expected: PASS (the chain tests take a few seconds each: the test daemon notices a job's end on its queue gate, every 3 s).

- [ ] **Step 6: Mutation-check the guards**, each reverted after: (a) drop `active != ""` in `startRun` → `TestFlowGates_AStoryRunChainsItsGatesAndPasses` fails on the refusal; (b) make `ended()` ignore `Cancelled` → `TestFlowGates_CancelEndsARunAndFreesTheWorktree` fails; (c) remove the `lockRuns` call in `advance` → `TestFlowGates_TwoWaitsQueueTheRestOnce` fails (run it with `-count=5`); (d) drop `_ = d.cancelJobs(ids)` in `submitChain` → the rollback test fails; (e) treat a vacuous-check error as PASS → the verdicts test fails; (f) drop `|| prev.State == sched.Done` from `cascadedFrom` → `TestFlowGates_AGateCancelledFromOutsideIsAnInfraFail` fails (its gate 2 turns SKIPPED).

- [ ] **Step 7: Commit.**

```bash
git add internal/cli/flow_gates.go internal/cli/flow_gates_test.go
git commit -m "feat(cli): loom flow gates, a story's or the final gates as a chain of jobs with each step's verdict" -- internal/cli/flow.go internal/cli/flow_gates.go internal/cli/flow_gates_test.go
```

---

## P3 — Prompts, the retirement and docs

**Files:** `flow/plugin/agents/verifier.md`, `flow/plugin/agents/plan-execution-supervisor.md`, `flow/plugin/skills/habits/SKILL.md`, `flow/plugin/docs/dispatch.md`; delete `flow/lib/agent-wait.sh`, `flow/lib/agent_wait_test.py`; `flow/lib/doctor.py`, `flow/lib/banned-tokens.txt`, `flow/lib/check_agents_test.py`; `flow/README.md`, `USAGE.md`, `internal/cli/CLAUDE.md`.

- [ ] **Step 1: The verifier.** In `flow/plugin/agents/verifier.md`, set the frontmatter description to `Check-only gate runner. It runs a story's gates or the final gates through loom flow gates and reports the verdict of each step, PASS, FAIL, FAIL (killed), FAIL (infra), VACUOUS or SKIPPED, with the exact command. Never mutates the tree. Pins set by the owner; change only on their instruction.` Replace the brief's first bullet (`- the lines \`loom flow conf gates\` printed, …` through `… \`FAIL <gate> (<why>)\`;`) and the second with:

```
- whether you run a story's gates, with the story's commits, or the final
  gates;
```

Then replace everything from `- **In order, one at a time.**` through the end of the `- **Long steps wait inside your turn.**` bullet (`… a FAIL to report with its symptoms.`), keeping the `- **Check-only.**` bullet as it is, with:

```
- **Start the gates.** Run `loom flow gates start --story <the story's
  commits>` for a story's gates, or `loom flow gates start --final`. It queues
  them in loom's work queue as a chain of jobs, one gate at a time in the
  profile's order, and prints the run's name and its jobs, with any gate the
  profile skipped or couldn't expand. A story's chain stops at its first FAIL
  and the rest are SKIPPED (not run); the final gates all run, each reported
  on its own. If `start` exits 2 because a run here hasn't ended, wait on
  that run as below when your brief says it is yours to finish, else report
  it.
- **Wait inside your turn.** Run `loom flow gates wait` as a foreground Bash
  call with a 600000 ms timeout, and run it again while it exits 75. Each call
  waits up to 170 seconds, then prints the whole table: per step PASS, FAIL,
  FAIL (killed), FAIL (infra), VACUOUS or SKIPPED once it has ended, and where
  it stands until then. It exits 0 when every step passed or was skipped, 1
  when any failed, and 125 when it can't follow the run: report that as FAIL
  (infra) with its message. Never use `run_in_background` and never end your
  turn to wait: Claude Code counts an agent waiting that way as not running,
  and a supervisor whose agents are all not running is made to hand back
  mid-run. Never wait with `sleep`, and never report while the run hasn't
  ended.
- **The verdicts are the tool's.** It judges each step from its job's own
  exit, runs the profile's vacuous check on each step's output (VACUOUS,
  never PASS), and calls a step a signal ended FAIL (killed), an infra
  failure. Don't rerun a gate to second-guess a verdict, and never rerun a
  FAIL (killed) silently: report its symptoms. Each failing line names the
  step's log; read it to quote the failure.
```

- [ ] **Step 2: The supervisor.** In `flow/plugin/agents/plan-execution-supervisor.md`, replace step 4 of a story (`4. Run \`loom flow conf gates --story <the story's commits>\` and` … `couldn't expand: it fails the story like any other gate.`) with:

```
4. Dispatch `flow:verifier` with the word "story", the story's commits and
   the `[verifier]` block; it runs `loom flow gates start --story <the
   story's commits>`. A gate the script couldn't expand is a FAIL in its
   table and fails the story like any other gate.
```

and step 1 after the last story (`1. Run \`loom flow conf gates --final\` and dispatch \`flow:verifier\` with` … `run, each reported on its own.`) with:

```
1. Dispatch `flow:verifier` with the word "final" and the `[verifier]` block;
   it runs `loom flow gates start --final`, and the final gates all run, each
   reported on its own.
```

- [ ] **Step 3: The habits.** In `flow/plugin/skills/habits/SKILL.md`, replace from `600000 ms), and run one that may not fit through the waiting script, staying` through `(output in \`<state>/agent-wait/<name>.log\`).` with:

```
  600000 ms), and run one that may not fit as a job, staying in your turn
  until it ends: `loom flow wait start <name> -- <command>` queues it in
  loom's work queue and prints its job ID, then `loom flow wait wait <name>`
  as a foreground call with a 600000 ms timeout, repeated while it prints
  `running <name>: …`, until `done <name> exit <status>` or
  `lost <name>: …`, which is final: report it (output in
  `<state>/agent-wait/<name>.log`). A job you queued another way is
  `loom flow wait wait --job <N>`. Never start a poll, a loop around
  `sleep`: `start` refuses one, and what it would watch is a job to wait on.
```

- [ ] **Step 4: The dispatch guide.** In `flow/plugin/docs/dispatch.md`, replace the verifier paragraphs (from `` `flow:verifier` (Haiku @ high) runs the lines `` through `` and waits for nothing. `` before `## The developers' git guard and turn caps`) with:

```
`flow:verifier` (Haiku @ high) runs the gates through `loom flow gates` — per
story `start --story <the story's commits>`, after the last story
`start --final` — and reports the table `loom flow gates wait` prints, each
step PASS, FAIL, FAIL (killed), FAIL (infra), VACUOUS or SKIPPED. Dispatch it
in the background to keep build logs out of the controller's context while
the compile wall-clock burns. Profile gates are check-only by definition: a
formatter appears in its check form, and drift is a FAIL for a developer to
apply. `start` queues the gates in loom's work queue as a chain, each job
after the one before, so the chain keeps its place in line and runs one gate
at a time. A story's chain stops at its first FAIL; after a final gate fails,
`wait` queues the gates after it again, so every final gate runs and one pass
shows every failing CI job. The verdicts are the tool's: it reads each job's
own exit, runs the profile's vacuous check on each output, and calls a step a
signal ended FAIL (killed), an infra failure. Never run the verifier while a
developer builds in the same worktree: both drive heavy builds.

`loom flow gates wait` waits at most 170 seconds a call, so the verifier waits
inside its turn ([why](#an-agent-waits-inside-its-turn)). The run's record is
in the state directory (`<state>/gates/<run>.json`): a verifier dispatched
after another was made to hand back finishes the run with
`loom flow gates wait <run>`, and `loom flow gates cancel <run>` ends one a
story abandons.
```

and in `## An agent waits inside its turn`, replace from `So the verifier and the developers run any step that can take more than a few` through `itself isn't queued twice.` with:

```
So the verifier and the developers wait in bounded foreground calls: the
verifier through `loom flow gates wait`, a developer through `loom flow wait`
for any step that can take more than a few minutes.
`start <name> -- <command>` queues the command in loom's work queue and prints
its job ID, and `wait <name>`, a foreground call that blocks for at most 170
seconds inside a 600000 ms Bash timeout, prints `done <name> exit <status>`
when the job ends, `running <name>: job <N> …` with where it stands when it
should be called again, or `lost <name>: …` for a job that ended without an
exit of its own. Each call copies what the job's output gained to
`<state>/agent-wait/<name>.log`, and `wait --job <N>` follows a job queued any
other way. That also keeps a step alive past the Bash tool's ten-minute
ceiling. The wait stays under three minutes because Claude Code 2.1.316 moves
a background subagent's foreground Bash call to the background once it has
run 180 seconds, a cap set on Anthropic's side: in the 2026-11-03 retro's runs
it moved 717 waits of 540 seconds, and each moved wait left a process that
later woke its agent with a notification. If a supervisor is made to hand back
anyway, it names its agents still out in the state file and in its `REFRESH`
report, and the manager forwards their late reports to the successor.

Every step is a job, never a process beside the queue: on 2026-11-05, 103 of
one repo's 169 steps ran detached on cores and memory the queue counted as
free, and their compiles landed in the shared sccache server, outside every
job. `start` refuses a poll, `sleep` or a shell loop around `sleep`, since a
queued poll holds a claim doing nothing (six did that day): wait on the step
it watches. It refuses a `loom …` command too, which is already a job to wait
on with `--job <N>`.
```

- [ ] **Step 5: Retire agent-wait.** `git rm flow/lib/agent-wait.sh flow/lib/agent_wait_test.py`. In `flow/lib/doctor.py`, delete `tail_pid_problem` and its `report("tail --pid", tail_pid_problem())` line, and in the module docstring delete `when \`tail --pid\` is missing (agent-wait.sh\nneeds it); ` (rejoin the sentence). Drop `subprocess` from its imports only if nothing else uses it. In `flow/lib/banned-tokens.txt`, add after the header comments:

```
# A retired tool's name, so no prompt sends an agent to it.
agent-wait.sh
```

In `flow/lib/check_agents_test.py`, after `test_a_banned_word_in_a_skill_fails`, add:

```python
    def test_a_retired_tools_name_fails(self):
        (self.root / "plugin" / "agents" / "dev.md").write_text(AGENT.replace("what the brief says", "what lib/agent-wait.sh prints"))
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL [banned]", out)
        self.assertIn("agent-wait.sh", out)
```

Run `grep -rn 'agent-wait.sh\|agent_wait' flow/` and fix any other hit (`flow/PROVENANCE.md` keeps its history as it is; its rows are records of their time). Then `bash flow/lib/test.sh`. Expected: every test and the agent lint pass.

- [ ] **Step 6: Docs.** `flow/README.md`'s tool table: the `wait` row becomes `` | `wait start NAME -- CMD`, `wait wait (NAME \| --job N) [SECONDS]` | Go: a step as a job in loom's queue, and a bounded wait for it | ``, and a new row after it: `` | `gates start (--story COMMIT… \| --final)`, `gates wait [RUN] [SECONDS]`, `gates cancel [RUN]` | Go: the gates as a chain of jobs, each step's verdict, and the resubmission after a final FAIL | ``. `USAGE.md`'s `flow wait` row becomes `` | `flow wait start <name> -- <cmd>`, `flow wait wait (<name> \| --job <N>) [secs]` | Queue a long step for an agent as a job and print its ID; wait for it inside a turn (75: still running, 70: lost); refuses polls | `` and gains after it `` | `flow gates start (--story <commits>… \| --final)`, `flow gates wait [<run>] [secs]`, `flow gates cancel [<run>]` | Queue a story's gates or the final gates as a chain of jobs, wait for the run inside a turn and print each step's verdict (0: passed, 1: failed, 75: not ended), or cancel it | ``. In `internal/cli/CLAUDE.md`, add before `## Pointers`:

```
- **Judge a gate step only from its job's end, its output copied first, and never PASS a step whose vacuous check could not run (FAIL (infra)); queue a gate chain whole or not at all, and read, judge or save a run's record only under the state dir's run lock (`lockRuns`).** A Haiku verifier reports the table as given, so a guessed verdict ships unverified work, and two waits outside the lock queue a final run's rest twice. **Enforced** by `TestFlowGates_EachVerdictComesFromTheJobsEnd`, `TestFlowGates_AChainItCantQueueWholeIsCancelledAndLeavesNoRecord` and `TestFlowGates_TwoWaitsQueueTheRestOnce`.
- **Keep every flow wait call under 170 s (`waitCap`) with its exit codes (75 not ended, 70 lost, 125 can't follow), and let `loom flow wait start` refuse a poll or a `loom` command.** Claude Code moves a subagent's foreground call to the background at 180 s, and flow's prompts branch on these codes; a queued poll holds a claim doing nothing. **Enforced** by `TestFlowWait_Usage`, `TestFlowWait_ABoundedWaitSaysWhereTheJobStandsAndTheLogGrows` and `TestFlowWait_IsPoll`.
```

Check the budget: `go run ./tools/claudemd -v | grep internal/cli`.

- [ ] **Step 7: Run the doc lint and the package tests.** `CGO_ENABLED=0 go test ./internal/cli ./flow/... . -run 'TestRepo_ClaudeMDStructure|TestLoomContext|TestFlow|TestScriptConf|TestWrite|TestGuarded'` and `bash flow/lib/test.sh`. Expected: PASS.

- [ ] **Step 8: Commit.**

```bash
git commit -m "feat(flow): the verifier runs loom flow gates, developers' waits show job IDs, agent-wait.sh retires" -- flow/plugin/agents/verifier.md flow/plugin/agents/plan-execution-supervisor.md flow/plugin/skills/habits/SKILL.md flow/plugin/docs/dispatch.md flow/lib/agent-wait.sh flow/lib/agent_wait_test.py flow/lib/doctor.py flow/lib/banned-tokens.txt flow/lib/check_agents_test.py flow/README.md USAGE.md internal/cli/CLAUDE.md
```

---

## P4 — Gates and the handoff

- [ ] **Step 1: Format and vet.** `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')` (no output) and `CGO_ENABLED=0 go vet ./...`. golangci-lint is skipped (the dev shell's v2 rejects the repo's config); say so in the handoff.
- [ ] **Step 2: Windows.** `GOOS=windows CGO_ENABLED=0 go build ./...` (the lock is `gofrs/flock`, which builds there; the commands refuse at `beginJob`).
- [ ] **Step 3: The full suite, then race, through the queue.** `loom job submit -d "go test ./... flow part 2" -- env CGO_ENABLED=0 go test ./...`, then `loom job submit -d "race internal/cli flow part 2" --after <its ID> -- env CC=clang CGO_ENABLED=1 go test -race ./internal/cli/... ./flow/...`. No pipe, so each job's exit is the suite's own. Expected: both exit 0; read each log for `FAIL`.
- [ ] **Step 4: Flow's own gate.** `bash flow/lib/test.sh` (after P3, again on the final tree).
- [ ] **Step 5: Execution notes.** Append `## Execution notes` to this plan: deviations, mutation results, gate results. Commit with `docs(plans): flow in loom part 2's execution notes`.
- [ ] **Step 6: Handoff.** Report: the commits, the gates, what wasn't run (golangci-lint, a live supervised run, which waits for part 1's cutover), and ask whether to fast-forward local `main`.

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/core"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/sched"
	"github.com/spf13/cobra"
)

// runFX is flowrun.Effects over the daemon: agent runs through 3a's agent
// code, gate runs through part 2's gate code, the services, git and flow's
// scripts, in the worktree cwd.
type runFX struct {
	c         *cobra.Command
	root      string // the launch's flow root
	conf      flowConf
	tools     runTools
	agents    string // <state>/agents
	gates     string // <state>/gates
	runDir    string // <state>/runs/<run>
	statePath string
	cwd       string
	plan      string
	out       io.Writer // the engine job's output.log
}

// logf writes one line to the engine's log.
func (fx *runFX) logf(format string, args ...any) {
	fmt.Fprintf(fx.out, "%s %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// daemon runs fn on a client, joining again with backoff while the daemon
// can't be reached or is lost mid-call, for up to rejoinFor an outage
// (rejoin). fn must be safe to run again: each effect reads its record
// before acting.
func (fx *runFX) daemon(fn func(d *client) error) error {
	return rejoin(fx.c, time.Time{}, rejoinFor, reexecOnUpgrade, func(line string) { fx.logf("%s", line) }, fn)
}

func (fx *runFX) AgentStarted(name string) (bool, error) {
	_, err := os.Stat(agentPath(fx.agents, name))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

// refused reports whether err is a refusal a run turns into a failed step
// (spec §6, a refused submit), not an error the engine stops on.
func refused(err error) (string, bool) {
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Msg, true
	}
	return "", false
}

func (fx *runFX) Agent(name, role, effort, brief string) (flowrun.Report, error) {
	started, err := fx.AgentStarted(name)
	if err != nil {
		return flowrun.Report{}, err
	}
	if !started {
		argv, err := agentArgv(fx.root, role, effort, fx.cwd, []byte(brief))
		if msg, ok := refused(err); ok {
			return flowrun.Report{Status: agentFailed, Why: msg}, nil
		}
		if err != nil {
			return flowrun.Report{}, err
		}
		fx.logf("start %s: flow:%s %s", name, role, effort)
		err = fx.daemon(func(d *client) error {
			if ok, err := fx.AgentStarted(name); err != nil || ok {
				return err // a start the lost daemon took after all
			}
			return d.startAgent(io.Discard, fx.agents, fx.root, &agentRun{Schema: agentRunSchema, ID: name, Role: role, Effort: effort, Cwd: fx.cwd}, argv, []byte(brief))
		})
		if msg, ok := refused(err); ok {
			return flowrun.Report{Status: agentFailed, Why: msg}, nil
		}
		if err != nil {
			return flowrun.Report{}, err
		}
	}
	return fx.follow(name)
}

// follow waits for agent run name's current job to end, records it, and
// is the run's report. Its path is report.md when the job left a report,
// else the job's output.log, which is all there is to read (feedback
// thread 12), or "" when the queue no longer knows the job.
func (fx *runFX) follow(name string) (flowrun.Report, error) {
	var run *agentRun
	var read string
	err := fx.daemon(func(d *client) error {
		for {
			var err error
			if run, err = readAgentRun(agentPath(fx.agents, name)); err != nil {
				return err
			}
			if run.Ended() {
				read = filepath.Join(fx.agents, name, "report.md")
				if run.Report == "" {
					v, known := jobOf(d.Queue(), run.Current().ID)
					read = ""
					if known {
						read = v.LogPath
					}
				}
				return nil
			}
			id := run.Current().ID
			v, known := jobOf(d.Queue(), id)
			if !known {
				// The view of a job just queued can come after its Reply.
				if v, err = d.awaitJob(id); err == nil {
					known = true
				} else if !errors.Is(err, errNotReported) {
					return err
				}
			}
			if known && !v.State.Finished() {
				if v, known, err = d.jobWithin(id, 170*time.Second); err != nil {
					return err
				}
				if known && !v.State.Finished() {
					continue
				}
			}
			if _, err := d.settleAgent(fx.agents, name, v, known); err != nil {
				return err
			}
		}
	})
	if err != nil {
		return flowrun.Report{}, err
	}
	fx.logf("end %s: %s %s", name, run.Status, run.Why)
	return flowrun.Report{Status: run.Status, Text: run.Report, Question: run.Question, Path: read, Why: run.Why, Limit: run.Limit}, nil
}

func (fx *runFX) Resume(name, text string) (flowrun.Report, error) {
	run, err := readAgentRun(agentPath(fx.agents, name))
	if err != nil {
		return flowrun.Report{}, err
	}
	if run.Current().Answer != text {
		if !resumable(run.Status) {
			return flowrun.Report{}, fmt.Errorf("agent run %s is %s, and only a run at NEEDS_CONTEXT, BLOCKED, TURN_CAP or USAGE_LIMIT is resumed", name, run.Status)
		}
		fx.logf("resume %s", name)
		err := fx.daemon(func(d *client) error {
			if r, err := readAgentRun(agentPath(fx.agents, name)); err != nil || r.Current().Answer == text {
				return err // the lost daemon took the answer after all
			}
			return d.answerAgent(io.Discard, fx.agents, name, fx.root, text)
		})
		if msg, ok := refused(err); ok {
			return flowrun.Report{Status: agentFailed, Why: msg}, nil
		}
		if err != nil {
			return flowrun.Report{}, err
		}
	}
	return fx.follow(name)
}

func (fx *runFX) GatesStarted(name string) (bool, error) {
	_, err := os.Stat(runPath(fx.gates, name))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

func (fx *runFX) Services(commands []string) error {
	for _, cmd := range commands {
		fx.logf("service: %s", cmd)
		svc := exec.Command("sh", "-c", cmd)
		svc.Dir, svc.Stdout, svc.Stderr = fx.cwd, fx.out, fx.out
		if err := svc.Run(); err != nil {
			return fmt.Errorf("%s: %w", cmd, err)
		}
	}
	return nil
}

func (fx *runFX) Gates(name string, commits []string) (flowrun.GateResult, error) {
	started, err := fx.GatesStarted(name)
	if err != nil {
		return flowrun.GateResult{}, err
	}
	if !started {
		run := &gateRun{Schema: gateRunSchema, Name: name, Kind: "final", Cwd: fx.cwd}
		args := []string{"--final"}
		if commits != nil {
			run.Kind, run.Commits = "story", commits
			args = append([]string{"--story"}, commits...)
		}
		lines, err := fx.conf.Gates(args...)
		if err == nil {
			err = planSteps(run, lines)
		}
		if err != nil {
			return flowrun.GateResult{Ended: "infra", Table: "the gates couldn't be expanded: " + err.Error()}, nil
		}
		fx.logf("start %s", name)
		err = fx.daemon(func(d *client) error {
			if ok, err := fx.GatesStarted(name); err != nil || ok {
				return err
			}
			return d.startRun(io.Discard, fx.gates, run)
		})
		if msg, ok := refused(err); ok {
			return flowrun.GateResult{Ended: "infra", Table: msg}, nil
		}
		if err != nil {
			return flowrun.GateResult{}, err
		}
	}
	var res flowrun.GateResult
	err = fx.daemon(func(d *client) error {
		for {
			run, err := d.advance(fx.conf, fx.gates, name)
			if err != nil {
				return err
			}
			if run.Ended() {
				res = gateResult(d.Queue(), run)
				return nil
			}
			open := openJobs(run)
			if _, err := d.await(170*time.Second, func([]core.Event) bool {
				return slices.ContainsFunc(open, func(id sched.JobID) bool {
					v, ok := jobOf(d.Queue(), id)
					return !ok || v.State.Finished()
				})
			}); err != nil {
				return err
			}
		}
	})
	if msg, ok := refused(err); ok {
		return flowrun.GateResult{Ended: "infra", Table: msg}, nil
	}
	fx.logf("end %s: %s", name, res.Ended)
	res.Free = freeBytes(fx.cwd)
	return res, err
}

// gateResult is an ended gate run as the engine reads it.
func gateResult(q core.QueueView, run *gateRun) flowrun.GateResult {
	var table bytes.Buffer
	printRun(&table, q, run)
	res := flowrun.GateResult{Ended: "passed", Table: table.String()}
	for _, st := range run.Steps {
		if !slices.Contains(failing, st.Verdict) {
			continue
		}
		res.Failing = append(res.Failing, flowrun.GateStep{Line: st.Text(), Verdict: st.Verdict, Log: st.Log})
		if st.Verdict == verdictInfra {
			res.Ended = "infra"
		} else if res.Ended == "passed" {
			res.Ended = "failed"
		}
	}
	return res
}

// git runs git args in the worktree.
func (fx *runFX) git(args ...string) (string, error) {
	var out, errOut bytes.Buffer
	cmd := internalexec.GitCommand(context.Background(), fx.cwd, args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (fx *runFX) Head() (string, error) { return fx.git("rev-parse", "HEAD") }

func (fx *runFX) Clean() (bool, error) {
	out, err := fx.git("status", "--porcelain")
	return out == "", err
}

func (fx *runFX) Commits(base, head string) ([]string, error) {
	out, err := fx.git("rev-list", "--reverse", base+".."+head)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

func (fx *runFX) Conformance(story, base, head string) (flowrun.Conformance, error) {
	out, err := fx.tools.Conformance(fx.plan, story, base, head)
	if err != nil {
		return flowrun.Conformance{}, err
	}
	path := filepath.Join(fx.runDir, fmt.Sprintf("conformance-%s-%s.txt", notName.ReplaceAllString(story, "-"), shortSHA(head)))
	if err := os.MkdirAll(fx.runDir, 0o700); err != nil {
		return flowrun.Conformance{}, err
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return flowrun.Conformance{}, err
	}
	c := flowrun.Conformance{Path: path}
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "MARKERS: "); ok {
			for _, m := range strings.Split(v, ", ") {
				if m = strings.TrimSpace(m); m != "" {
					c.Markers = append(c.Markers, m)
				}
			}
		}
	}
	return c, nil
}

func (fx *runFX) Premises(rev string) (string, error) { return fx.tools.Premises(fx.plan, rev) }

func (fx *runFX) DocsCheck(base, head string) ([]string, error) {
	return fx.tools.DocsCheck(base, head)
}

func (fx *runFX) Save(st *flowrun.State) error { return flowrun.Save(fx.statePath, st) }

// shortSHA is sha's first nine characters.
func shortSHA(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

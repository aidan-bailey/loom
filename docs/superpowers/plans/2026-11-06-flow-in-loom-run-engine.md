# Flow in loom, part 3b: the run engine — Implementation Plan

> **For agentic workers:** executed inline (owner's choice, 2026-11-06) with superpowers:executing-plans, package by package, no checkpoint pauses. P1 is a gate: if a premise fails in a way the amendments don't already cover, stop and take the design back to the owner. Steps use checkbox (`- [ ]`) syntax.

**Goal:** A deterministic Go engine, run as a job of its own kind, drives a plan's stories through headless flow agents and gate runs, asks the manager every judgement, and writes the run's record.

**Architecture:**
- **`internal/flowrun` (new):** the engine. It replays: every step has a deterministic name and its result is memoized in the run state, so a restarted engine runs the procedure again from the top and each finished step returns at once. Every side effect goes through `Effects`.
- **`sched` and `core`:** a `run` job kind (no claim, outside the caps, the advisor and the agent limit), and a `Run` marker on every job a run submits, which a pause spares.
- **`internal/cli`:** `loom flow run`, plus the `Effects` adapter onto 3a's agent code and part 2's gate code.
- **`flow/`:** the `/flow:run` skill, `check_plan.py --json`, a markers line in conformance, `flowconf.py services`, and `agent_costs.py run`.

**Tech Stack:** Go (cobra, testify, `gofrs/flock`), Python 3 (flow's tools), the `internal/cli` hermetic test daemon with a fake `claude`, Claude Code 2.1.318's `-p` stream-json.

**Spec:** `docs/superpowers/specs/2026-11-06-flow-in-loom-run-engine-design.md` (958f2254).

**Rules that bind every package:**
- Scratch files go outside the worktree; commit with `-- <paths>`.
- Never run loom itself in a pane.
- Suites and builds go through `loom job`.
- `sched/CLAUDE.md`:
  - `Admit` stays pure;
  - bounds and texts live in `Validate`;
  - `jobs.json` is written only through the store;
  - Windows and darwin cross-builds after touching `shim_unix.go`.
- `core/CLAUDE.md`: a job reads no model state.
- git runs only through `internalexec.GitCommand` (`TestNoRawGitGhExec`).
- `bash flow/lib/test.sh` after changing anything under `flow/`.

---

## Planning amendments (they win where the spec differs)

1. **A pause spares the run** (owner, 2026-11-06). Pausing or killing a session cancels every job it owns (`core.cancelOwnerJobs`), and every job of a run is owned by the manager's session through `LOOM_LAUNCH`.
   - A job submitted with `LOOM_FLOW_RUN=<N>` in its environment, where job N is a `run`-kind job, records `Run: N`.
   - The engine sets `LOOM_FLOW_RUN` to its own `LOOM_JOB` at its start, so its agents, its gate jobs and every job its agents queue inherit it.
   - A pause skips `run`-kind jobs and jobs with `Run != 0`. A kill and a reset still cancel them, since a kill removes the worktree.
2. **The engine replays.** Spec §5.3's "intent, then job ID, then result" is realised as follows:
   - **Names:** each step has a deterministic name (`<run>-s3-dev2`, `<run>-s3-gates1`).
   - **Memo:** its result is memoized in `State.Memo` under a key made from that name.
   - **Restart:** a restarted engine runs the procedure from the top. A memoized step returns its result. A step whose agent run or gate run already exists by name is followed, not submitted again. Only a step with neither is submitted.
   - **Rebuilt each pass:** derived fields (the story rows, the notes, the kept Minors, the phase) are reset and rebuilt on every pass.
3. **`check_plan.py --json`** prints the spec, the plan review and every story: ID, first and last plan lines, Developer, tier, review column, landmines, Interfaces, Verify. Its text output has no line ranges, and the engine needs them.
4. **`plan_conformance.py` prints `MARKERS: <marker>, …`** when an uncovered changed line holds one of the profile's landmine markers, so the engine never reads the profile.
5. **`flowconf.py services`** prints the profile's services one per line, raw. `show`'s backticked list can't be split safely.
6. **Developers end their reports with `DEVIATION:` lines** (the preamble asks for them), for the record's Deviations column. The reviewers' brief points at the developer's report as a whole.
7. **The `[developer]` block loses its landmine notes on a light story.** The engine drops the indented lines under `Landmine notes (deep stories only):`, as the supervisor did.
8. **The engine joins the daemon per effect** and, when the daemon is lost, rejoins with backoff (2 s doubling to 30 s) for up to 10 minutes. After that it exits 125, and the run shows as lost.
9. **Gate runs take the engine's names** (`<run>-s<N>-gates<k>`, `<run>-final-gates<k>`), so a later run never overwrites an earlier run's logs.
10. **The engine job's argv is `<os.Executable()> flow run engine <run>`.** In `internal/cli`'s tests the engine runs in-process: `engineArgv` is replaced by a job that exits at once, and the test calls `loom flow run engine` itself.
11. **`agent_costs.py run <run>`** reads `<state>/runs/<run>.json` (`managers`, `agents`, `start`, `end`) and each agent record's `session`, finds each transcript under `CLAUDE_CONFIG_DIR` by session ID, and adds `"managers": {"<id>": ["<model>", …]}` to the comment's JSON. The record's Manager line reads that.
12. **The engine checks HEAD only before a fresh dispatch:** an agent run that already exists by name is followed without the `tree-moved` check, since its own commits moved HEAD.
13. **Resumes count per agent run, in replay order** (`resume:<name>:<n>`), across NEEDS_CONTEXT answers and the turn-cap "Continue", so no two share a key.
14. **The final round's fix developers run under a pseudo-story `final`**, whose row is not in the record's table.
15. **A PLAN line is asked about once per story**, after its review loop. An answer that is a decision runs one more fix round with the PLAN lines as Important findings. The PLAN lines that round's re-review gives are noted, not asked about again.

## File structure

| File | Responsibility |
|---|---|
| `session/claude_flow_run_realclaude_test.go` (new) | the probe |
| `sched/job.go`, `sched/admit.go`, `sched/grant.go`, `sched/shim_unix.go` | `KindRun`, `Unclaimed`, `Record.Run`, the run branch of `Admit`, `Grant`'s pass-over, the hints |
| `core/queue_ops.go`, `core/queue_admit.go`, `core/requests.go`, `core/reset.go` | no sizing for a run job, `Run` from `LOOM_FLOW_RUN`, the pause's spare |
| `internal/cli/job_sizing.go`, `ui/work.go` | a run job's claim cell reads `run` |
| `flow/lib/check_plan.py`, `flow/lib/plan_conformance.py`, `flow/lib/flowconf.py`, `flow/lib/agent_costs.py` | `--json`, `MARKERS:`, `services`, `run` |
| `internal/cli/flow_agent.go`, `internal/cli/flow_agent_exec.go` | `TURN_CAP`, `answer` on it, the role preambles, `agentArgv`, `--max-turns` if P1 needs it |
| `internal/flowrun/` (new: `state.go`, `answers.go`, `parse.go`, `briefs.go`, `engine.go`, `record.go`, `CLAUDE.md`, `README.md`) | the engine |
| `internal/cli/flow_run.go`, `internal/cli/flow_run_fx.go` (new) | the commands and the `Effects` adapter |
| `flow/plugin/skills/run/SKILL.md` (new), `flow/plugin/docs/records.md`, `flow/plugin/docs/dispatch.md`, `flow/README.md` | the skill and its docs |
| `sched/CLAUDE.md`, `core/CLAUDE.md`, `internal/cli/CLAUDE.md`, `USAGE.md`, `docs/claude/the-work-queue.md` | rules and docs |

---

## P1 — The probe (the gate)

**Files:** create `session/claude_flow_run_realclaude_test.go`.

It is opt-in (`LOOM_TEST_REAL_CLAUDE=1`) and modelled on `TestRealClaude_FlowAgentRun` (`session/claude_flow_agent_realclaude_test.go`): the same scratch plugin layout, scrubbed environment, plugins-off settings and default account. Reuse that file's `agentProbeEvents` and `probeScrubbed` (same package).

- [ ] **Step 1: The test.** `TestRealClaude_FlowRunPremises` writes a scratch plugin `flowrunprobe` with two agents:
  - `probe-capped.md`: `model: haiku`, `maxTurns: 2`, `tools: Bash`. Body: *"Run `ls`, then `ls -a`, then `ls -l`, then `pwd`, each as its own Bash call, one at a time. Then report."*
  - `probe-reviewer.md`: `model: haiku`, `tools: Bash, Read`. Body: *"You review a change. Find the problem in `x.go` and report it."*

  `x.go` (committed in a temp git repo) holds `package x\nfunc Div(a, b int) int { return a / b }\n`.

  Premises:
  1. **`--agent` applies the definition's `maxTurns`.** `claude -p "<preamble>\n\nGo." --agent flowrunprobe:probe-capped …` ends with a `result` event whose `subtype` is `error_max_turns`, or whose `num_turns` is at most 3. Log `subtype`, `is_error`, `num_turns` and whether `result` text exists.
  2. **The same run with `--max-turns 2`** ends `error_max_turns`. This is the fallback for premise 1.
  3. **A resume of a capped run** (`--resume <session> -p "Continue from where you stopped."`) continues the session: the same `session_id`, and a new `result`.
  4. **A reviewer under the review preamble** (P3's `reviewPreamble`, copied here as a const) ends its result with a `FINDING:` line matching `^FINDING: (Critical|Important|Minor) (design )?\S+:\d+ .+`, then a `VERDICT:` line, then a `STATUS:` line.
- [ ] **Step 2: Run it once.** `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session -run TestRealClaude_FlowRunPremises -v -count=1` (through `loom job run`). Record each premise's outcome in this plan's execution notes.
- [ ] **Step 3: Gate.**
  - Premise 1 fails and 2 passes: P3 adds `--max-turns <definition's maxTurns>` to `agentCommand`.
  - Premise 1 and 2 both fail: stop; the turn-cap rules go back to the owner.
  - Premise 3 fails: stop.
  - Premise 4 fails: tighten the preamble's wording and run the premise again (at most twice); then stop.
- [ ] **Step 4: Commit:** `test(session): probe turn caps and review lines for flow's run engine`.

---

## P2 — The `run` job kind and the pause's spare (`sched`, `core`)

**Files:** `sched/job.go`, `sched/admit.go`, `sched/grant.go`, `sched/shim_unix.go`, `core/queue_ops.go`, `core/queue_admit.go`, `core/requests.go`, `core/reset.go`, `internal/cli/job_sizing.go`, `ui/work.go`, and their tests.

- [ ] **Step 1: The failing tests.** Read each file's existing table and helper shapes first.
  - **`sched/job_test.go` `TestSubmit_Validate`:**
    - a run job (`Kind: KindRun`) with `Claim{CPUs: 1}`, `Claim{IO: true}`, `Claim{Exclusive: true}` or `After: 3`: each refused with `a run job claims nothing and runs after nothing`;
    - a bare run job: accepted.
  - **`sched/admit_test.go`, new `TestAdmit_RunJobs`:**
    - a host whose caps a running build fills, and an account with 4 of `AgentJobs: 4` agents running: a queued run job starts;
    - a queued run job that has waited past `StarveAfter` doesn't stop the build behind it from starting;
    - `Policy{}`: a run job neither starts nor gets a reason.
  - **`sched/grant_test.go` `TestGrant`:** a starting run job is granted nothing and stops no grant.
  - **`sched/store_test.go`:** `Kind: KindRun, Run: 7` round-trips.
  - **`core`** (beside `TestJobQueue_AnAgentJobIsNeverSized`):
    - `TestJobQueue_ARunJobIsNeverSized`: zero `Claim`, no `Signature`, no advisor call, `JobView.Kind == "run"`;
    - `TestJobQueue_AJobOfARunRecordsItsRun`: a build submitted with `LOOM_FLOW_RUN=<id of a queued run job>` records `Run == id`. With `LOOM_FLOW_RUN` naming a build job, a missing job or `x`, it records `Run == 0`.
  - **`core`** (beside the pause tests that cover `cancelOwnerJobs`, e.g. `TestPause_CancelsTheSessionsJobs`):
    - `TestPause_SparesARunsJobs`: a session owning a run job, a job with `Run` set and a plain build. A pause cancels only the build.
    - `TestKill_CancelsARunsJobs`: a kill cancels all three.
  - **`internal/cli/job_test.go`:** `claimText` of a run `JobView` is `run`. **`ui/work_test.go`:** `workClaim` is `run`.
- [ ] **Step 2: See them fail.** `CGO_ENABLED=0 go test ./sched ./core ./internal/cli ./ui -run 'Validate|RunJob|Grant|Store|Pause|Kill|claimText|workClaim'`. Expected: build failures (`KindRun` undefined).
- [ ] **Step 3: `sched/job.go`.** Beside `KindAgent`:

```go
// KindRun is a flow run engine's kind (flow in loom part 3b, spec §5.2): a
// process that drives a plan's agents and gates, started at once, outside
// the caps, the advisor and the agent limit, and never sized.
const KindRun = "run"

// Unclaimed reports whether a job of kind claims nothing: an agent or a run
// job. Such jobs are never sized, granted or contained, and get no
// parallelism hints.
func Unclaimed(kind string) bool { return kind == KindAgent || kind == KindRun }
```

`Record` gains, after `AccountKey`:

```go
	// Run is the run job (KindRun) this job was submitted under, from its
	// environment's LOOM_FLOW_RUN (flow in loom part 3b, planning amendment
	// 1): a pause spares it. 0 for any other job.
	Run JobID `json:"run,omitempty"`
```

`Validate`'s switch becomes:

```go
	switch {
	case s.Kind != "" && s.Kind != KindAgent && s.Kind != KindRun:
		return fmt.Errorf("unknown job kind %q", s.Kind)
	case s.Kind == KindAgent && (s.Claim != Claim{} || s.After != 0):
		return errors.New("an agent job claims nothing and runs after nothing")
	case s.Kind == KindRun && (s.Claim != Claim{} || s.After != 0):
		return errors.New("a run job claims nothing and runs after nothing")
	}
```

- [ ] **Step 4: `sched/admit.go`.** In `admitWith`'s scan, before the agent cases:

```go
		case r.Kind == KindRun && r.State == Queued:
			runs = append(runs, r)
		case r.Kind == KindRun:
			// A running run job takes nothing from the caps.
```

Declare `runs` with `queued, agents`. Before the agents' loop:

```go
	// Run jobs (flow in loom part 3b, spec §5.2) start at once while
	// admission has a usable policy, and are never a head.
	slices.SortFunc(runs, byOrder)
	for _, r := range runs {
		if p.usable() {
			d.Start = append(d.Start, r.ID)
		}
	}
```

Add to the doc comment: "Run jobs (Kind KindRun) start at once, take nothing from the caps and are never the head."

- [ ] **Step 5: `sched/grant.go`.** `if r.Kind == KindAgent` becomes `if Unclaimed(r.Kind)`, and the doc comment says "Agent and run jobs are passed over". In `shim_unix.go`, `if spec.Kind == KindAgent` becomes `if Unclaimed(spec.Kind)`.
- [ ] **Step 6: `core`.**
  - **`queue_ops.go` `submitJob`:**
    - `agentJob := s.Kind == sched.KindAgent` becomes `unclaimed := sched.Unclaimed(s.Kind)`, and every use is renamed. The unsized text is `"an agent job is never sized"` for an agent and `"a run job is never sized"` for a run.
    - `rec.AccountKey` is still set only for `KindAgent`.
    - After the record literal:

```go
	// A job a flow run submitted (its environment's LOOM_FLOW_RUN names a
	// run job) records the run, which a pause spares (part 3b, planning
	// amendment 1). Any other value is ignored.
	if n, err := strconv.ParseInt(envValue(s.Env, "LOOM_FLOW_RUN"), 10, 64); err == nil {
		if i := q.index(sched.JobID(n)); i >= 0 && q.store.Jobs[i].Kind == sched.KindRun {
			rec.Run = sched.JobID(n)
		}
	}
```

    (Check `q.index`'s not-found value in `core/queue*.go` first, and match it.)
  - **`queue_admit.go`:** `if rec.Kind == sched.KindAgent` (the `Uncontained` branch) becomes `if sched.Unclaimed(rec.Kind)`.
  - **`queue_ops.go` `cancelOwnerJobs`:** gains a `spareRuns bool` parameter. In the loop, `continue` first when `spareRuns && (rec.Kind == sched.KindRun || rec.Run != 0)`. The doc comment adds: "A pause spares a flow run's jobs (spareRuns): the run goes on while its manager is paused (part 3b, planning amendment 1)."
  - **`requests.go`:** Pause passes `true`, Kill `false`. **`reset.go`** passes `false`.
- [ ] **Step 7: The claim cells.** `claimText` and `workClaim` return `"run"` when the kind is `sched.KindRun`, beside the agent case.
- [ ] **Step 8: Run, gofmt, vet, the cross-builds.**

```
gofmt -l sched core internal/cli ui && CGO_ENABLED=0 go vet ./sched/... ./core/... ./internal/cli/... ./ui/... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./...
```

  Then the Step 2 command. Expected: PASS. Then `go test ./core/rpc -run 'TestProtocolReference|TestGenerated_IsFresh'`: regenerate with `-update` if `JobView`'s reference changed.
- [ ] **Step 9: Mutation-check.** Each of these must fail its test:
  - drop the run branch of `Admit`: the full-host case;
  - drop the spare condition: `TestPause_SparesARunsJobs`;
  - pass `true` from Kill: `TestKill_CancelsARunsJobs`;
  - accept any `LOOM_FLOW_RUN` job: the build-job case;
  - drop `Validate`'s run rule: the claim cases.
- [ ] **Step 10: Commit:** `feat(sched,core,cli,ui): a run job kind for flow's engine, and a pause that spares a run's jobs`.

---

## P3 — Flow's tools and 3a's agent runs

**Files:** `flow/lib/check_plan.py`, `flow/lib/plan_conformance.py`, `flow/lib/flowconf.py`, `flow/lib/agent_costs.py` and their `_test.py`; `internal/cli/flow_agent.go`, `internal/cli/flow_agent_exec.go`, `internal/cli/flow_agent_test.go`.

- [ ] **Step 1: `check_plan.py --json`.**
  - `Story` gains `end: int = 0`. `parse` sets the open story's `end = n - 1` when the next story or H2 starts, and `len(lines)` at EOF.
  - `main` takes `--json` before the plan path. With it, for one plan, a passing check prints:

```json
{"spec": "<path>", "plan_review": "<text or null>",
 "stories": [{"id": "1", "line": 40, "end": 212, "developer": "intermediate-dev",
              "tier": "deep", "review": "vouched", "landmines": ["os.RemoveAll("],
              "interfaces": "<Interfaces field>", "verify": "<Verify field>"}]}
```

    A failing one prints its FAIL lines as today, exit 1.
  - Tests in `check_plan_test.py`: the end lines of two stories and a trailing H2; the JSON keys; FAIL lines unchanged with `--json`.
- [ ] **Step 2: `plan_conformance.py`.** After its report, when any uncovered changed line holds a profile landmine marker (`flowconf.load_profile(repo).markers`, matched as `markers_in` matches), print `MARKERS: <m1>, <m2>`. Test: an uncovered line holding a marker prints it; a covered one doesn't.
- [ ] **Step 3: `flowconf.py services`.** It prints each of `profile.services` on its own line, raw, and nothing for none. Test it in `flowconf_test.py`.
- [ ] **Step 4: `agent_costs.py run RUN`.**
  - Factor `session_report`'s body into `agents_report(title: str, agents: list[tuple[str, str, Path]], since, until, extra: dict) -> str`, where each agent is (type, label, transcript). `session_report` calls it with the main transcript and its subagents.
  - `run`:
    - reads `flowconf.state_dir(...)`'s `runs/RUN.json` through flowconf's state helper (as `state` prints it);
    - for each manager ID, finds the transcript with `find_session`, as type `main`;
    - for each name in `agents`, reads `agents/<name>.json`'s `role` and `session`, finds the transcript, as type `<role>`, labelled with the name;
    - uses `since`/`until` from `start`/`end`;
    - puts `"run": RUN` and `"managers": {id: [models]}` in the comment's data.
  - Tests: a fake `CLAUDE_CONFIG_DIR` with two transcripts and a run file prices both by type, and the comment carries the managers' models.
- [ ] **Step 5: `bash flow/lib/test.sh`.** Expected: PASS.
- [ ] **Step 6: 3a's turn cap.** In `flow_agent_exec.go`:
  - `agentStatuses` stays the report statuses. Add `const agentTurnCap = "TURN_CAP"`.
  - In `readResult`, read `Subtype` from the result. When `result.Subtype == "error_max_turns"`, return `res` with `Status: agentTurnCap` and no error (before the `IsError` check).
  - In `flow_agent.go` `answerAgent`, also allow `agentTurnCap`, and update the refusal text to "only a run at NEEDS_CONTEXT, BLOCKED or TURN_CAP takes an answer". `printAgentEnd` prints `done <id>: TURN_CAP` like a status.
  - Tests in `flow_agent_test.go`: the fake claude answers a `CAPPED` prompt with `{"type":"result","subtype":"error_max_turns","is_error":true,…}`. `wait` prints `done …: TURN_CAP`, and `answer` resumes it.
- [ ] **Step 7: The role preambles.** In `flow_agent_exec.go`:

```go
// reviewPreamble follows headlessPreamble for a reviewer's run (part 3b,
// spec §5.6): the engine routes by these lines.
const reviewPreamble = "Before your STATUS line, end your report with one line `FINDING: <Critical|Important|Minor> [design] <file>:<line> <the fix>` " +
	"per issue (write `design` when the fix needs a decision the spec doesn't make), one line `PLAN: <the problem>` " +
	"per problem in the plan's own code, and then `VERDICT: APPROVED` or `VERDICT: CHANGES_REQUESTED`. Write no FINDING or PLAN line when there is none."

// developerPreamble follows headlessPreamble for a developer's run (part 3b,
// planning amendment 6).
const developerPreamble = "Before your STATUS line, list each deviation from the plan on its own line `DEVIATION: <what and why>`."

// rolePreamble is headlessPreamble with role's own lines.
func rolePreamble(role string) string {
	switch role {
	case "story-reviewer", "code-quality-reviewer":
		return headlessPreamble + " " + reviewPreamble
	case "junior-dev", "intermediate-dev", "senior-dev":
		return headlessPreamble + " " + developerPreamble
	}
	return headlessPreamble
}
```

  `newFlowAgentRunCmd` uses `rolePreamble(role)`. Test that the reviewer's and the developer's argv carry their lines and a recon's doesn't.
- [ ] **Step 8: `agentArgv`.** Factor from `newFlowAgentRunCmd`'s `RunE`:

```go
// agentArgv is the argv of a first agent job of role with brief, run in
// cwd from the flow root root (spec §5.4), or an ExitError saying why not.
func agentArgv(root, role, effort, cwd string, brief []byte) ([]string, error)
```

  It covers the definition check (exitUsage), the brief checks (empty: exitUsage; over `maxBrief`: exitUsage), `preloadedSkills`, `runSettings` and `agentCommand`. The command calls it. Include `--max-turns` here only if P1's gate said so, read from the definition's `maxTurns:` frontmatter.
- [ ] **Step 9: Run.** `gofmt -l internal/cli && CGO_ENABLED=0 go vet ./internal/cli/... && CGO_ENABLED=0 go test ./internal/cli -run 'FlowAgent|StatusLines|ScrubbedVar'`. Mutation-check: drop the subtype case (the CAPPED test fails), and drop `rolePreamble` from run (the preamble test fails).
- [ ] **Step 10: Commit:** `feat(flow,cli): plan JSON, conformance markers, services, run costs; turn caps and role preambles for agent runs`.

---

## P4 — The engine (`internal/flowrun`)

**Files:** create `internal/flowrun/state.go`, `answers.go`, `parse.go`, `briefs.go`, `engine.go`, `record.go`, `CLAUDE.md`, `README.md`, and `state_test.go`, `answers_test.go`, `parse_test.go`, `briefs_test.go`, `engine_test.go`, `record_test.go`, `testdata/*.golden`.

- [ ] **Step 1: `state.go`.**

```go
// Package flowrun is flow in loom part 3b's run engine: it drives a plan's
// stories through headless flow agents and gate runs, one step at a time,
// and parks on every judgement for the manager. It replays: every step has a
// deterministic name and a memoized result, so a restarted engine runs the
// procedure again and only the first unfinished step does anything. Every
// side effect goes through Effects.
package flowrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/config"
)

// Schema is the run state's version.
const Schema = 1

// A run's statuses.
const (
	Running   = "running"   // an engine works on it, or is to
	Parked    = "parked"    // an open question waits for the manager
	Done      = "done"
	Stopped   = "stopped"   // the manager answered stop
	Cancelled = "cancelled" // loom flow run cancel
)

// Ended reports whether status is a run's end.
func Ended(status string) bool { return status == Done || status == Stopped || status == Cancelled }

// State is a run's state, <state>/runs/<id>.json (spec §4).
type State struct {
	Schema      int                        `json:"schema"`
	ID          string                     `json:"id"`
	Status      string                     `json:"status"`
	Plan        string                     `json:"plan"`
	Spec        string                     `json:"spec"`
	Base        string                     `json:"base"`
	Start       string                     `json:"start"` // UTC, RFC 3339
	End         string                     `json:"end,omitempty"`
	Roster      string                     `json:"roster"`
	Profile     string                     `json:"profile"`
	DesignStage string                     `json:"design_stage,omitempty"`
	RecordCopy  string                     `json:"record_copy,omitempty"` // the profile's in-repo record dir, "" for none
	Managers    []string                   `json:"managers"`
	Blocks      Blocks                     `json:"blocks"`
	Stories     []Story                    `json:"stories"`
	Engine      int64                      `json:"engine,omitempty"` // the latest engine job
	Agents      []string                   `json:"agents"`           // every agent run, in dispatch order
	Gates       []string                   `json:"gates"`            // every gate run, in start order
	Memo        map[string]json.RawMessage `json:"memo"`
	Question    *Question                  `json:"question,omitempty"`
	Answers     []Answer                   `json:"answers,omitempty"`
	Record      string                     `json:"record,omitempty"` // the record's path, once written
	Why         string                     `json:"why,omitempty"`
	// Rebuilt on every pass (planning amendment 2).
	Phase string    `json:"phase,omitempty"`
	Notes []string  `json:"notes,omitempty"`
	Kept  []Finding `json:"kept,omitempty"` // Minors kept for the final round
}

// Blocks are the profile's role blocks as `loom flow conf show --for
// supervisor` prints them, and its services.
type Blocks struct {
	Developer  string   `json:"developer"`
	Verifier   string   `json:"verifier"`
	Reviewer   string   `json:"reviewer"`
	Supervisor string   `json:"supervisor"`
	Services   []string `json:"services,omitempty"`
}

// Story is a plan's story as check_plan.py --json gives it, and its row.
type Story struct {
	ID         string   `json:"id"`
	Line       int      `json:"line"`
	End        int      `json:"end"`
	Developer  string   `json:"developer"`
	Tier       string   `json:"tier"`
	Review     string   `json:"review"`
	Landmines  []string `json:"landmines,omitempty"`
	Interfaces string   `json:"interfaces,omitempty"`
	Verify     string   `json:"verify,omitempty"`
	Row        Row      `json:"row"`
}

// Row is what the run made of a story, for show and the record.
type Row struct {
	Status      string   `json:"status,omitempty"` // developing, gating, reviewing, fixing, done
	Developer   string   `json:"developer,omitempty"`
	Base        string   `json:"base,omitempty"`
	Head        string   `json:"head,omitempty"`
	Commits     int      `json:"commits,omitempty"`
	Deviations  int      `json:"deviations,omitempty"`
	UpATier     int      `json:"up_a_tier,omitempty"`
	Escalated   int      `json:"escalated,omitempty"`
	Findings    [3]int   `json:"findings"` // Critical, Important, Minor of its first review
	TurnCap     bool     `json:"turn_cap,omitempty"`
	Corrections []string `json:"corrections,omitempty"`
}

// Question is a parked run's open question (spec §5.4).
type Question struct {
	Key   string   `json:"key"`
	Kind  string   `json:"kind"`
	Story string   `json:"story,omitempty"`
	Text  string   `json:"text"`
	Paths []string `json:"paths,omitempty"`
}

// Answer is an answered question, with the session that answered it.
type Answer struct {
	Question
	Answer  string `json:"answer"`
	Session string `json:"session,omitempty"`
}

// Finding is one FINDING line (spec §5.6).
type Finding struct {
	Severity string `json:"severity"` // Critical, Important or Minor
	Design   bool   `json:"design,omitempty"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Fix      string `json:"fix"`
}

func (f Finding) String() string {
	d := ""
	if f.Design {
		d = " (design)"
	}
	return fmt.Sprintf("%s%s %s:%d %s", f.Severity, d, f.File, f.Line, f.Fix)
}

// Load reads a run state, refusing one it can't decode or of another schema.
func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if st.Schema != Schema {
		return nil, fmt.Errorf("%s: schema %d, and this loom reads %d", path, st.Schema, Schema)
	}
	return &st, nil
}

// Save writes st whole at path (a temp file renamed).
func Save(path string, st *State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, append(b, '\n'), 0o600)
}

// Answer records text as the answer to st's open question, from session,
// and sets st running; CheckAnswer has passed it.
func (st *State) Answer(text, session string) error {
	if st.Status != Parked || st.Question == nil {
		return errors.New("the run has no open question")
	}
	text = strings.TrimSpace(text)
	raw, err := json.Marshal(text)
	if err != nil {
		return err
	}
	if st.Memo == nil {
		st.Memo = map[string]json.RawMessage{}
	}
	st.Memo["q:"+st.Question.Key] = raw
	st.Answers = append(st.Answers, Answer{Question: *st.Question, Answer: text, Session: session})
	if session != "" && !slices.Contains(st.Managers, session) {
		st.Managers = append(st.Managers, session)
	}
	st.Question, st.Status = nil, Running
	return nil
}
```

- [ ] **Step 2: `answers.go`.**

```go
package flowrun

import (
	"fmt"
	"slices"
	"strings"
)

// The question kinds (spec §5.4).
const (
	KindNeedsContext  = "needs-context"
	KindBlocked       = "blocked-decision"
	KindDesignFinding = "design-finding"
	KindPremiseDesign = "premise-design"
	KindUnresolved    = "unresolved-findings"
	KindGateFailure   = "gate-failure"
	KindAgentFailed   = "agent-failed"
	KindTreeMoved     = "tree-moved"
)

// The answer words.
const (
	WordStop     = "stop"
	WordAccept   = "accept"
	WordRetry    = "retry"
	WordContinue = "continue"
)

var allWords = []string{WordStop, WordAccept, WordRetry, WordContinue}

// takes is each kind's words, and whether it takes text (a decision).
var takes = map[string]struct {
	words []string
	text  bool
}{
	KindNeedsContext:  {nil, true},
	KindBlocked:       {[]string{WordStop}, true},
	KindDesignFinding: {[]string{WordAccept, WordStop}, true},
	KindPremiseDesign: {[]string{WordStop}, true},
	KindUnresolved:    {[]string{WordAccept, WordStop}, true},
	KindGateFailure:   {[]string{WordRetry, WordAccept, WordStop}, true},
	KindAgentFailed:   {[]string{WordRetry, WordStop}, false},
	KindTreeMoved:     {[]string{WordContinue, WordStop}, false},
}

// CheckAnswer checks answer against kind: the word it is, or "" for a text
// answer. An answer that is exactly a word is that word.
func CheckAnswer(kind, answer string) (string, error) {
	t, ok := takes[kind]
	if !ok {
		return "", fmt.Errorf("unknown question kind %q", kind)
	}
	a := strings.TrimSpace(answer)
	switch {
	case a == "":
		return "", fmt.Errorf("the answer is empty")
	case slices.Contains(t.words, a):
		return a, nil
	case slices.Contains(allWords, a):
		return "", fmt.Errorf("a %s question doesn't take %q; it takes %s", kind, a, Takes(kind))
	case !t.text:
		return "", fmt.Errorf("a %s question takes only %s", kind, Takes(kind))
	}
	return "", nil
}

// Takes is what kind takes, for the manager: "a decision, accept or stop".
func Takes(kind string) string {
	t := takes[kind]
	var parts []string
	if kind == KindNeedsContext {
		parts = append(parts, "an answer")
	} else if t.text {
		parts = append(parts, "a decision")
	}
	parts = append(parts, t.words...)
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " or " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}
```

- [ ] **Step 3: `parse.go`.**

```go
package flowrun

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Review is a review report's lines (spec §5.6).
type Review struct {
	Verdict  string
	Findings []Finding
	Plan     []string
}

var findingRE = regexp.MustCompile(`^(Critical|Important|Minor)\s+(design\s+)?(\S+?):(\d+)(?:-\d+)?\s+(.+)$`)

// tagLine is line trimmed of space and markdown's `*` and backticks, and its
// value after tag ("FINDING:"), if it starts with it.
func tagLine(line, tag string) (string, bool) {
	line = strings.Trim(strings.TrimSpace(line), "*`")
	v, ok := strings.CutPrefix(line, tag)
	return strings.Trim(strings.TrimSpace(v), "*`"), ok
}

// ParseReview reads a review's FINDING, PLAN and VERDICT lines. A review
// with no VERDICT line, or a FINDING line it can't read, is an error.
func ParseReview(text string) (Review, error) {
	var r Review
	for _, line := range strings.Split(text, "\n") {
		if v, ok := tagLine(line, "FINDING:"); ok {
			m := findingRE.FindStringSubmatch(v)
			if m == nil {
				return Review{}, fmt.Errorf("a FINDING line it can't read: %q", v)
			}
			n, _ := strconv.Atoi(m[4])
			r.Findings = append(r.Findings, Finding{Severity: m[1], Design: m[2] != "", File: m[3], Line: n, Fix: m[5]})
		} else if v, ok := tagLine(line, "PLAN:"); ok && v != "" {
			r.Plan = append(r.Plan, v)
		} else if v, ok := tagLine(line, "VERDICT:"); ok {
			r.Verdict = v
		}
	}
	if r.Verdict != "APPROVED" && r.Verdict != "CHANGES_REQUESTED" {
		return Review{}, errors.New("its report has no VERDICT line")
	}
	return r, nil
}

// Blocking reports whether r found a Critical or an Important.
func (r Review) Blocking() bool { return len(blocking(r.Findings)) > 0 }

func blocking(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Severity != "Minor" {
			out = append(out, f)
		}
	}
	return out
}

// Counts is r's Critical, Important and Minor counts.
func (r Review) Counts() [3]int {
	var c [3]int
	for _, f := range r.Findings {
		switch f.Severity {
		case "Critical":
			c[0]++
		case "Important":
			c[1]++
		default:
			c[2]++
		}
	}
	return c
}

// Minors is r's Minor findings.
func (r Review) Minors() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == "Minor" {
			out = append(out, f)
		}
	}
	return out
}

// Deviations counts a developer report's DEVIATION lines.
func Deviations(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if v, ok := tagLine(line, "DEVIATION:"); ok && v != "" {
			n++
		}
	}
	return n
}

// PremiseGroup is the flagged sections of up to three stories, for one
// premise-check.
type PremiseGroup struct {
	Stories []string
	Text    string
}

var premiseHeadRE = regexp.MustCompile(`^(Preamble|Story (\S+)) \(plan:\d+\): \d+ premises, (\d+) flagged$`)

// PremiseGroups splits plan_premises.py's output into groups of up to per
// flagged sections, keeping each section's header and flag lines.
func PremiseGroups(out string, per int) []PremiseGroup {
	var groups []PremiseGroup
	var cur *PremiseGroup
	keep := false
	for _, line := range strings.Split(out, "\n") {
		if m := premiseHeadRE.FindStringSubmatch(line); m != nil {
			keep = m[3] != "0"
			if !keep {
				continue
			}
			if cur == nil || len(cur.Stories) == per {
				groups = append(groups, PremiseGroup{})
				cur = &groups[len(groups)-1]
			}
			id := m[2]
			if id == "" {
				id = "preamble"
			}
			cur.Stories = append(cur.Stories, id)
			cur.Text += line + "\n"
			continue
		}
		if keep && strings.HasPrefix(line, "  plan:") {
			cur.Text += line + "\n"
		} else if !strings.HasPrefix(line, "  ") {
			keep = false
		}
	}
	return groups
}

// PremiseVerdict is one premise-check report line.
type PremiseVerdict struct {
	Line    int
	Verdict string // HOLDS, MOVED, RENAMED, STALE, CLASH or UNSURE
	Class   string // COSMETIC, DESIGN or "-"
	Fix     string
	Text    string // the line as reported
}

var verdictRE = regexp.MustCompile(`^plan:(\d+)\s*\|\s*([A-Z]+)\s*\|\s*([A-Z-]+)\s*\|([^|]*)\|?(.*)$`)

// PremiseVerdicts reads a premise-check report's verdict lines.
func PremiseVerdicts(text string) []PremiseVerdict {
	var out []PremiseVerdict
	for _, line := range strings.Split(text, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "`")
		m := verdictRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		out = append(out, PremiseVerdict{Line: n, Verdict: m[2], Class: m[3], Fix: strings.TrimSpace(m[5]), Text: line})
	}
	return out
}
```

- [ ] **Step 4: `briefs.go`.** Each brief is plain text built with `strings.Builder`. The fixed wording is below; golden files pin it (Step 9).

```go
package flowrun

import (
	"fmt"
	"strings"
)

// developerBlock is the [developer] block, without its landmine notes for a
// light story (planning amendment 7).
func developerBlock(st *State, s *Story) string {
	if s == nil || s.Tier != "light" {
		return st.Blocks.Developer
	}
	var out []string
	notes := false
	for _, line := range strings.Split(st.Blocks.Developer, "\n") {
		switch {
		case strings.HasPrefix(line, "Landmine notes"):
			notes = true
			out = append(out, line+" none (a light story)")
		case notes && strings.HasPrefix(line, "    "):
		default:
			notes = false
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// DeveloperBrief is a story's developer brief (spec §5.5).
func DeveloperBrief(st *State, s *Story) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Build Story %s of the plan %s: its section is plan lines %d-%d. Read those lines; the tasks are there, not here.\n", s.ID, st.Plan, s.Line, s.End)
	fmt.Fprintf(&b, "The spec is %s.\n", st.Spec)
	if len(s.Row.Corrections) > 0 {
		b.WriteString("\nCorrections to the plan's premises (checked against the base commit):\n")
		for _, c := range s.Row.Corrections {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	fmt.Fprintf(&b, "\nCommit once per task. Run the story's Verify command (%s) before you report DONE.\n", s.Verify)
	fmt.Fprintf(&b, "\n[developer]\n%s\n", developerBlock(st, s))
	return b.String()
}

// withEarlier is brief with the reports of the developers before and the
// manager's decision, for an escalated or decided developer.
func withEarlier(brief string, earlier []string, decision string) string {
	if len(earlier) == 0 && decision == "" {
		return brief
	}
	var b strings.Builder
	b.WriteString(brief)
	if len(earlier) > 0 {
		b.WriteString("\nThis story was attempted before; read each earlier report first and go on from the commits it made:\n")
		for _, p := range earlier {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	if decision != "" {
		fmt.Fprintf(&b, "\nThe manager's decision on what was left open:\n%s\n", decision)
	}
	return b.String()
}

// FixGatesBrief is a fix developer's brief for failing gates.
func FixGatesBrief(st *State, s *Story, base, head string, res GateResult, decision string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fix the gates %s fails. The story's commits are %s..%s; its section is plan lines %d-%d of %s.\n\n", storyName(s), base, head, s.Line, s.End, st.Plan)
	b.WriteString("Failing steps:\n")
	for _, f := range res.Failing {
		fmt.Fprintf(&b, "- %s: %s (log: %s)\n", f.Verdict, f.Line, f.Log)
	}
	if decision != "" {
		fmt.Fprintf(&b, "\nThe manager's decision:\n%s\n", decision)
	}
	fmt.Fprintf(&b, "\nCommit each fix. Run the failing commands again before you report DONE.\n\n[developer]\n%s\n", developerBlock(st, s))
	return b.String()
}

// FixFindingsBrief is a fix developer's brief for review findings.
func FixFindingsBrief(st *State, s *Story, base, head string, findings []Finding, plan []string, decision string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fix these review findings in %s. Its commits are %s..%s; its section is plan lines %d-%d of %s.\n\n", storyName(s), base, head, s.Line, s.End, st.Plan)
	for _, f := range findings {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	for _, p := range plan {
		fmt.Fprintf(&b, "- Plan: %s\n", p)
	}
	if decision != "" {
		fmt.Fprintf(&b, "\nThe manager's decision:\n%s\n", decision)
	}
	fmt.Fprintf(&b, "\nCommit each fix. Run the story's Verify command (%s) before you report DONE.\n\n[developer]\n%s\n", s.Verify, developerBlock(st, s))
	return b.String()
}

// ReviewBrief is a story's review brief.
func ReviewBrief(st *State, s *Story, base, head, conformance, devReport string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review Story %s of the plan %s: its section is plan lines %d-%d, and its commits are %s..%s.\n", s.ID, st.Plan, s.Line, s.End, base, head)
	fmt.Fprintf(&b, "The output of loom flow conformance for it is in %s.\n", conformance)
	fmt.Fprintf(&b, "The developer's report, with its deviations, is %s.\n", devReport)
	fmt.Fprintf(&b, "Its Interfaces line: %s\nThe Interfaces lines of the earlier stories it uses are in the plan.\n", s.Interfaces)
	fmt.Fprintf(&b, "\n[reviewer]\n%s\n", st.Blocks.Reviewer)
	return b.String()
}

// ReReviewBrief is the brief of a re-review of a fix round.
func ReReviewBrief(st *State, s *Story, base, head string, findings []Finding, plan []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Re-review a fix round of %s in the plan %s: the fix's commits are %s..%s. Check that each finding below is fixed, and review the fix itself.\n\n", storyName(s), st.Plan, base, head)
	for _, f := range findings {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	for _, p := range plan {
		fmt.Fprintf(&b, "- Plan: %s\n", p)
	}
	fmt.Fprintf(&b, "\n[reviewer]\n%s\n", st.Blocks.Reviewer)
	return b.String()
}

// AntiSeamBrief is the whole-implementation review's brief.
func AntiSeamBrief(st *State, head string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review the whole implementation of the plan %s, %s..%s, for its seams: contracts between stories, values one story produces that no other reads, units or limits that disagree across a boundary. The spec is %s.\n", st.Plan, st.Base, head, st.Spec)
	fmt.Fprintf(&b, "\n[reviewer]\n%s\n", st.Blocks.Reviewer)
	return b.String()
}

// PremiseBrief is a premise-check's brief.
func PremiseBrief(st *State, g PremiseGroup) string {
	return fmt.Sprintf("Settle these flags of loom flow premises on the plan %s, against the base commit %s:\n\n%s", st.Plan, st.Base, g.Text)
}

func storyName(s *Story) string {
	if s.ID == finalStory {
		return "the whole implementation"
	}
	return "Story " + s.ID
}
```

- [ ] **Step 5: `engine.go`.**

```go
package flowrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Report is an agent run's end, as 3a's record holds it.
type Report struct {
	Status   string `json:"status"` // DONE, DONE_WITH_CONCERNS, NEEDS_CONTEXT, BLOCKED, TURN_CAP, failed, cancelled
	Text     string `json:"text,omitempty"`
	Question string `json:"question,omitempty"`
	Path     string `json:"path,omitempty"` // report.md
	Why      string `json:"why,omitempty"`
}

func (r Report) done() bool { return r.Status == "DONE" || r.Status == "DONE_WITH_CONCERNS" }

// GateResult is a gate run's end.
type GateResult struct {
	Ended   string     `json:"ended"` // passed, failed or infra
	Failing []GateStep `json:"failing,omitempty"`
	Table   string     `json:"table,omitempty"`
}

// GateStep is a failing step of a gate run.
type GateStep struct {
	Line    string `json:"line"`
	Verdict string `json:"verdict"`
	Log     string `json:"log,omitempty"`
}

// Conformance is loom flow conformance's output for a story.
type Conformance struct {
	Path    string   `json:"path"`
	Markers []string `json:"markers,omitempty"`
}

// Effects is everything the engine does outside its state. Every wait is
// the effect's: Agent, Resume and Gates return once the run they start, or
// follow, has ended.
type Effects interface {
	AgentStarted(name string) (bool, error)
	Agent(name, role, effort, brief string) (Report, error)
	// Resume answers the ended agent run name with text, or follows the
	// answer already queued with that text.
	Resume(name, text string) (Report, error)
	GatesStarted(name string) (bool, error)
	Services(commands []string) error
	// Gates runs a story's gates over commits, or the final gates when
	// commits is nil.
	Gates(name string, commits []string) (GateResult, error)
	Head() (string, error)
	Clean() (bool, error)
	Commits(base, head string) ([]string, error)
	Conformance(story, base, head string) (Conformance, error)
	// Premises is plan_premises.py's output when it flagged anything, else "".
	Premises(rev string) (string, error)
	Save(st *State) error
}

var (
	errParked  = errors.New("the run parked")
	errStopped = errors.New("the manager stopped the run")
)

// finalStory is the pseudo-story the final round's fixes run under
// (planning amendment 14).
const finalStory = "final"

var notName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

type engine struct {
	st      *State
	fx      Effects
	head    string         // the head last recorded
	cur     string         // the story in hand, "" outside one
	seq     map[string]int // attempts at each name prefix, this pass
	resumes map[string]int // resumes of each agent run, this pass
	final   Story
}

// Run drives st until it parks or ends, saving it after every step. It
// returns nil when st has parked or ended, and otherwise the error it
// could not get past (a lost daemon, a file it can't write), with st left
// running for `loom flow run resume`.
func Run(st *State, fx Effects) error {
	e := &engine{st: st, fx: fx, head: st.Base, seq: map[string]int{}, resumes: map[string]int{}, final: Story{ID: finalStory}}
	st.Status, st.Question, st.Why = Running, nil, ""
	st.Phase, st.Notes, st.Kept = "", nil, nil
	for i := range st.Stories {
		st.Stories[i].Row = Row{}
	}
	err := e.run()
	switch {
	case errors.Is(err, errParked):
		return nil
	case errors.Is(err, errStopped):
		return e.end(Stopped)
	case err != nil:
		return err
	}
	return e.end(Done)
}

func (e *engine) end(status string) error {
	e.st.Status, e.st.Question = status, nil
	e.st.End = time.Now().UTC().Format(time.RFC3339)
	e.st.Phase = "ended"
	return e.fx.Save(e.st)
}

func (e *engine) run() error {
	if err := e.premises(); err != nil {
		return err
	}
	for i := range e.st.Stories {
		if err := e.story(&e.st.Stories[i]); err != nil {
			return err
		}
	}
	return e.finalRound()
}

func (e *engine) note(format string, args ...any) {
	e.st.Notes = append(e.st.Notes, fmt.Sprintf(format, args...))
}

// recall unmarshals key's memoized result into v.
func (e *engine) recall(key string, v any) bool {
	raw, ok := e.st.Memo[key]
	return ok && json.Unmarshal(raw, v) == nil
}

// remember memoizes v under key and saves the state.
func (e *engine) remember(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if e.st.Memo == nil {
		e.st.Memo = map[string]json.RawMessage{}
	}
	e.st.Memo[key] = raw
	return e.fx.Save(e.st)
}

// next is the name of prefix's next attempt this pass: <run>-<prefix><k>.
func (e *engine) next(prefix string) string {
	e.seq[prefix]++
	return fmt.Sprintf("%s-%s%d", e.st.ID, prefix, e.seq[prefix])
}

// storyPrefix is story id as a name part: s<id>.
func storyPrefix(id string) string { return "s" + notName.ReplaceAllString(id, "-") }

// headAt is HEAD memoized under key, which becomes the head last recorded.
func (e *engine) headAt(key string) (string, error) {
	var h string
	if !e.recall("head:"+key, &h) {
		var err error
		if h, err = e.fx.Head(); err != nil {
			return "", err
		}
		if err := e.remember("head:"+key, h); err != nil {
			return "", err
		}
	}
	e.head = h
	return h, nil
}

// commitsAt is base..HEAD's commits memoized under key.
func (e *engine) commitsAt(key, base string) ([]string, error) {
	var cs []string
	if e.recall("commits:"+key, &cs) {
		return cs, nil
	}
	cs, err := e.fx.Commits(base, e.head)
	if err != nil {
		return nil, err
	}
	return cs, e.remember("commits:"+key, cs)
}

// ask is the answer to q, or parks the run on it.
func (e *engine) ask(q Question) (string, error) {
	q.Story = e.cur
	if s := e.storyOf(e.cur); s != nil {
		s.Row.Escalated++
	}
	var a string
	if e.recall("q:"+q.Key, &a) {
		return a, nil
	}
	e.st.Question, e.st.Status = &q, Parked
	if err := e.fx.Save(e.st); err != nil {
		return "", err
	}
	return "", errParked
}

func (e *engine) storyOf(id string) *Story {
	for i := range e.st.Stories {
		if e.st.Stories[i].ID == id {
			return &e.st.Stories[i]
		}
	}
	return nil
}

// checkTree parks as tree-moved when HEAD or the tree isn't what the
// engine recorded, before a fresh dispatch of name (planning amendment 12).
func (e *engine) checkTree(name string) error {
	head, err := e.fx.Head()
	if err != nil {
		return err
	}
	clean, err := e.fx.Clean()
	if err != nil {
		return err
	}
	if head == e.head && clean {
		return nil
	}
	why := fmt.Sprintf("HEAD is %s, and the engine last recorded %s", head, e.head)
	if !clean {
		why = "the tree has uncommitted changes; " + why
	}
	a, err := e.ask(Question{Key: "tree:" + name, Kind: KindTreeMoved, Text: why + ", before it starts " + name})
	if err != nil {
		return err
	}
	if a == WordStop {
		return errStopped
	}
	_, err = e.headAt("tree:" + name)
	return err
}

// agent is agent run name's report: memoized, followed when it exists, else
// started after the tree check.
func (e *engine) agent(name, role, effort, brief string) (Report, error) {
	var r Report
	if e.recall("agent:"+name, &r) {
		return r, nil
	}
	started, err := e.fx.AgentStarted(name)
	if err != nil {
		return r, err
	}
	if !started {
		if err := e.checkTree(name); err != nil {
			return r, err
		}
	}
	if !slices.Contains(e.st.Agents, name) {
		e.st.Agents = append(e.st.Agents, name)
		if err := e.fx.Save(e.st); err != nil {
			return r, err
		}
	}
	if r, err = e.fx.Agent(name, role, effort, brief); err != nil {
		return r, err
	}
	return r, e.remember("agent:"+name, r)
}

// resume is the report of name resumed with text (planning amendment 13).
func (e *engine) resume(name, text string) (Report, error) {
	e.resumes[name]++
	key := fmt.Sprintf("resume:%s:%d", name, e.resumes[name])
	var r Report
	if e.recall(key, &r) {
		return r, nil
	}
	r, err := e.fx.Resume(name, text)
	if err != nil {
		return r, err
	}
	return r, e.remember(key, r)
}

// settle answers name's NEEDS_CONTEXT questions until it reports otherwise.
func (e *engine) settle(name string, r Report) (Report, error) {
	for n := 1; r.Status == "NEEDS_CONTEXT"; n++ {
		a, err := e.ask(Question{Key: fmt.Sprintf("ctx:%s:%d", name, n), Kind: KindNeedsContext, Text: r.Question, Paths: paths(r.Path)})
		if err != nil {
			return r, err
		}
		if r, err = e.resume(name, a); err != nil {
			return r, err
		}
	}
	return r, nil
}

func paths(p ...string) []string {
	var out []string
	for _, s := range p {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// failed asks the manager about name's failure: nil is retry.
func (e *engine) failed(name string, r Report) error {
	why := r.Why
	if why == "" {
		why = "it reported " + r.Status
	}
	a, err := e.ask(Question{Key: "failed:" + name, Kind: KindAgentFailed, Text: name + " ended without a usable report: " + why, Paths: paths(r.Path)})
	if err != nil {
		return err
	}
	if a == WordStop {
		return errStopped
	}
	return nil
}

// review runs a reviewer at prefix until its report reads (spec §5.6).
func (e *engine) review(prefix, role, brief string) (Review, Report, error) {
	for {
		name := e.next(prefix)
		r, err := e.agent(name, role, "", brief)
		if err == nil {
			r, err = e.settle(name, r)
		}
		if err != nil {
			return Review{}, r, err
		}
		if r.done() {
			rev, perr := ParseReview(r.Text)
			if perr == nil {
				return rev, r, nil
			}
			r.Why = perr.Error()
		}
		if err := e.failed(name, r); err != nil {
			return Review{}, r, err
		}
	}
}

// developerEffort is a story developer's effort: xhigh for an
// intermediate-dev on a story with landmines.
func developerEffort(s *Story, role string) string {
	if role == "intermediate-dev" && len(s.Landmines) > 0 {
		return "xhigh"
	}
	return ""
}

// build runs developers at prefix until one reports done with commits,
// escalating as spec §5.3 step 2 says. fix is true for a fix round, whose
// developers stay at their pinned effort.
func (e *engine) build(s *Story, prefix, role, brief string, fix bool) (Report, error) {
	before := e.head
	var earlier []string
	decision := ""
	effort := func() string {
		if fix {
			return ""
		}
		return developerEffort(s, role)
	}
	name := e.next(prefix)
	r, err := e.agent(name, role, effort(), brief)
	resumed := false
	for {
		if err == nil {
			r, err = e.settle(name, r)
		}
		if err != nil {
			return r, err
		}
		switch {
		case r.done():
			head, err := e.headAt(name)
			if err != nil {
				return r, err
			}
			if head != before {
				s.Row.Developer = role
				s.Row.Deviations += Deviations(r.Text)
				return r, nil
			}
			if err := e.failed(name, Report{Status: r.Status, Why: "it reported done with no commits", Path: r.Path}); err != nil {
				return r, err
			}
		case r.Status == "TURN_CAP" && role == "intermediate-dev" && !resumed:
			s.Row.TurnCap, resumed = true, true
			r, err = e.resume(name, "Continue from where you stopped.")
			continue
		case (r.Status == "TURN_CAP" || r.Status == "BLOCKED") && role == "junior-dev":
			s.Row.TurnCap = s.Row.TurnCap || r.Status == "TURN_CAP"
			s.Row.UpATier++
			e.note("%s: %s reported %s; a fresh intermediate-dev took it", storyName(s), name, r.Status)
			earlier, role = append(earlier, r.Path), "intermediate-dev"
		case r.Status == "BLOCKED" && role == "intermediate-dev" && decision != "":
			s.Row.UpATier++
			e.note("%s: %s was BLOCKED with the manager's decision; senior-dev took it", storyName(s), name)
			earlier, role = append(earlier, r.Path), "senior-dev"
		case r.Status == "BLOCKED":
			a, err := e.ask(Question{Key: "blocked:" + name, Kind: KindBlocked, Text: name + " is BLOCKED: its report says on what", Paths: paths(r.Path)})
			if err != nil {
				return r, err
			}
			if a == WordStop {
				return r, errStopped
			}
			earlier, decision = append(earlier, r.Path), a
		default:
			s.Row.TurnCap = s.Row.TurnCap || r.Status == "TURN_CAP"
			if err := e.failed(name, r); err != nil {
				return r, err
			}
		}
		name = e.next(prefix)
		r, err = e.agent(name, role, effort(), withEarlier(brief, earlier, decision))
		resumed = false
	}
}

// gates is gate run name's result: memoized, followed when it exists, else
// started after the profile's services.
func (e *engine) gates(name string, commits []string) (GateResult, error) {
	var r GateResult
	if e.recall("gates:"+name, &r) {
		return r, nil
	}
	started, err := e.fx.GatesStarted(name)
	if err != nil {
		return r, err
	}
	if !slices.Contains(e.st.Gates, name) {
		e.st.Gates = append(e.st.Gates, name)
	}
	if !started {
		if err := e.fx.Services(e.st.Blocks.Services); err != nil {
			r = GateResult{Ended: "infra", Table: "FAIL (infra): a service failed: " + err.Error()}
			return r, e.remember("gates:"+name, r)
		}
	}
	if r, err = e.fx.Gates(name, commits); err != nil {
		return r, err
	}
	return r, e.remember("gates:"+name, r)
}

func logsOf(r GateResult) []string {
	var out []string
	for _, f := range r.Failing {
		out = append(out, f.Log)
	}
	return paths(out...)
}

// gateLoop runs s's gates (final ones when s is the final pseudo-story)
// until they pass or the manager accepts them, with a fix after a failure.
func (e *engine) gateLoop(s *Story, prefix, base string) error {
	s.Row.Status = "gating"
	final := s.ID == finalStory
	for failures := 0; ; {
		name := e.next(prefix + "-gates")
		var commits []string
		if !final {
			var err error
			if commits, err = e.commitsAt(name, base); err != nil {
				return err
			}
		}
		res, err := e.gates(name, commits)
		if err != nil {
			return err
		}
		if res.Ended == "passed" {
			return nil
		}
		failures++
		decision := ""
		if res.Ended == "infra" || failures > 1 {
			a, err := e.ask(Question{Key: "gates:" + name, Kind: KindGateFailure, Text: name + " failed:\n" + res.Table, Paths: logsOf(res)})
			if err != nil {
				return err
			}
			switch a {
			case WordStop:
				return errStopped
			case WordAccept:
				e.note("%s: the manager accepted the failing gates of %s", storyName(s), name)
				return nil
			case WordRetry:
				continue
			}
			decision = a
		}
		if _, err := e.build(s, prefix+"-fix", "intermediate-dev", FixGatesBrief(e.st, s, base, e.head, res, decision), true); err != nil {
			return err
		}
	}
}

// premises sends the plan's flagged premises to premise-check and settles
// their verdicts (spec §5.3, before the first story).
func (e *engine) premises() error {
	e.st.Phase = "premises"
	var flagged string
	if !e.recall("premises", &flagged) {
		out, err := e.fx.Premises(e.st.Base)
		if err != nil {
			return err
		}
		if err := e.remember("premises", out); err != nil {
			return err
		}
		flagged = out
	}
	var design []PremiseVerdict
	for _, g := range PremiseGroups(flagged, 3) {
		r, err := e.dispatchReport("premise", "premise-check", PremiseBrief(e.st, g))
		if err != nil {
			return err
		}
		for _, v := range PremiseVerdicts(r.Text) {
			switch {
			case v.Verdict == "HOLDS":
			case v.Class == "COSMETIC":
				if s := e.storyAtLine(v.Line); s != nil {
					s.Row.Corrections = append(s.Row.Corrections, v.Text)
				}
			default:
				design = append(design, v)
			}
		}
	}
	if len(design) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("Premises that need a decision before any story starts:\n")
	for _, v := range design {
		b.WriteString(v.Text + "\n")
	}
	a, err := e.ask(Question{Key: "premise-design", Kind: KindPremiseDesign, Text: b.String()})
	if err != nil {
		return err
	}
	if a == WordStop {
		return errStopped
	}
	for _, v := range design {
		if s := e.storyAtLine(v.Line); s != nil {
			s.Row.Corrections = append(s.Row.Corrections, v.Text+" — the manager: "+a)
		}
	}
	return nil
}

// dispatchReport runs a non-developer agent at prefix until it reports
// done; a BLOCKED is a failure (spec §5.3).
func (e *engine) dispatchReport(prefix, role, brief string) (Report, error) {
	for {
		name := e.next(prefix)
		r, err := e.agent(name, role, "", brief)
		if err == nil {
			r, err = e.settle(name, r)
		}
		if err != nil || r.done() {
			return r, err
		}
		if err := e.failed(name, r); err != nil {
			return r, err
		}
	}
}

func (e *engine) storyAtLine(line int) *Story {
	for i := range e.st.Stories {
		if s := &e.st.Stories[i]; s.Line <= line && line <= s.End {
			return s
		}
	}
	return nil
}

// conformance is story s's conformance output, memoized under key.
func (e *engine) conformance(key string, s *Story, base, head string) (Conformance, error) {
	var c Conformance
	if e.recall("conformance:"+key, &c) {
		return c, nil
	}
	c, err := e.fx.Conformance(s.ID, base, head)
	if err != nil {
		return c, err
	}
	return c, e.remember("conformance:"+key, c)
}

// story runs one story: developer, gates, review, findings (spec §5.3).
func (e *engine) story(s *Story) error {
	e.cur, e.st.Phase = s.ID, "story "+s.ID
	defer func() { e.cur = "" }()
	p := storyPrefix(s.ID)
	base, err := e.headAt(p + ":base")
	if err != nil {
		return err
	}
	s.Row.Base, s.Row.Status = base, "developing"
	dev, err := e.build(s, p+"-dev", s.Developer, DeveloperBrief(e.st, s), false)
	if err != nil {
		return err
	}
	if err := e.gateLoop(s, p, base); err != nil {
		return err
	}
	s.Row.Status = "reviewing"
	conf, err := e.conformance(p, s, base, e.head)
	if err != nil {
		return err
	}
	role := "story-reviewer"
	if s.Review != "vouched" || dev.Status == "DONE_WITH_CONCERNS" || len(conf.Markers) > 0 {
		role = "code-quality-reviewer"
	}
	rev, rep, err := e.review(p+"-review", role, ReviewBrief(e.st, s, base, e.head, conf.Path, dev.Path))
	if err != nil {
		return err
	}
	s.Row.Findings = rev.Counts()
	plan := rev.Plan
	if err := e.findings(s, p, base, rev, rep, &plan); err != nil {
		return err
	}
	if err := e.planLines(s, p, base, plan); err != nil {
		return err
	}
	commits, err := e.commitsAt(p+":done", base)
	if err != nil {
		return err
	}
	s.Row.Head, s.Row.Commits, s.Row.Status = e.head, len(commits), "done"
	return nil
}

// findings routes a review's findings: Minors kept, a blocking review
// fixed and re-reviewed until clean or the manager decides (spec §5.3, 5).
func (e *engine) findings(s *Story, p, base string, rev Review, rep Report, plan *[]string) error {
	if !rev.Blocking() {
		e.st.Kept = append(e.st.Kept, rev.Minors()...)
		return nil
	}
	decision := ""
	if slices.ContainsFunc(blocking(rev.Findings), func(f Finding) bool { return f.Design }) {
		a, err := e.ask(Question{Key: "design:" + s.ID, Kind: KindDesignFinding, Text: "A review of " + storyName(s) + " found a design-level issue; its report has the FINDING lines", Paths: paths(rep.Path)})
		if err != nil {
			return err
		}
		switch a {
		case WordStop:
			return errStopped
		case WordAccept:
			e.note("%s: the manager left the design findings open", storyName(s))
		default:
			decision = a
		}
	}
	return e.fixRound(s, p, base, rev.Findings, nil, decision, plan)
}

// fixRound fixes findings and re-reviews them until the re-review is clean
// or the manager accepts what it found.
func (e *engine) fixRound(s *Story, p, base string, findings []Finding, planFix []string, decision string, plan *[]string) error {
	for round := 1; ; round++ {
		s.Row.Status = "fixing"
		fixBase := e.head
		if _, err := e.build(s, p+"-fix", "intermediate-dev", FixFindingsBrief(e.st, s, fixBase, e.head, findings, planFix, decision), true); err != nil {
			return err
		}
		if err := e.gateLoop(s, p, base); err != nil {
			return err
		}
		rev, rep, err := e.review(p+"-review", "story-reviewer", ReReviewBrief(e.st, s, fixBase, e.head, findings, planFix))
		if err != nil {
			return err
		}
		if plan != nil {
			*plan = append(*plan, rev.Plan...)
		}
		if !rev.Blocking() {
			e.st.Kept = append(e.st.Kept, rev.Minors()...)
			return nil
		}
		a, err := e.ask(Question{Key: fmt.Sprintf("unresolved:%s:%d", p, round), Kind: KindUnresolved, Text: "After a fix round, a re-review of " + storyName(s) + " still finds Critical or Important issues", Paths: paths(rep.Path)})
		if err != nil {
			return err
		}
		switch a {
		case WordStop:
			return errStopped
		case WordAccept:
			e.note("%s: the manager accepted %d findings left open", storyName(s), len(blocking(rev.Findings)))
			return nil
		}
		findings, planFix, decision = rev.Findings, nil, a
	}
}

// planLines asks the manager once about a story's PLAN lines (planning
// amendment 15).
func (e *engine) planLines(s *Story, p, base string, plan []string) error {
	if len(plan) == 0 {
		return nil
	}
	a, err := e.ask(Question{Key: "plan:" + s.ID, Kind: KindUnresolved, Text: "Reviews of " + storyName(s) + " found problems in the plan's own code:\n- " + strings.Join(plan, "\n- ")})
	if err != nil {
		return err
	}
	switch a {
	case WordStop:
		return errStopped
	case WordAccept:
		e.note("%s: the manager accepted the plan problems: %s", storyName(s), strings.Join(plan, "; "))
		return nil
	}
	var later []string
	if err := e.fixRound(s, p, base, nil, plan, a, &later); err != nil {
		return err
	}
	for _, l := range later {
		e.note("%s: a re-review's plan problem, not asked again: %s", storyName(s), l)
	}
	return nil
}

// finalRound runs the final gates, the anti-seam review and the final fix
// round (spec §5.3, after the last story).
func (e *engine) finalRound() error {
	e.st.Phase = "final"
	s := &e.final
	if err := e.gateLoop(s, "final", e.st.Base); err != nil {
		return err
	}
	rev, rep, err := e.review("final-review", "code-quality-reviewer", AntiSeamBrief(e.st, e.head))
	if err != nil {
		return err
	}
	findings := append(slices.Clone(rev.Findings), e.st.Kept...)
	if len(findings) == 0 {
		return nil
	}
	decision := ""
	if slices.ContainsFunc(blocking(rev.Findings), func(f Finding) bool { return f.Design }) {
		a, err := e.ask(Question{Key: "design:final", Kind: KindDesignFinding, Text: "The whole-implementation review found a design-level issue; its report has the FINDING lines", Paths: paths(rep.Path)})
		if err != nil {
			return err
		}
		switch a {
		case WordStop:
			return errStopped
		case WordAccept:
			e.note("the whole implementation: the manager left the design findings open")
		default:
			decision = a
		}
	}
	for round := 1; ; round++ {
		roundBase := e.head
		for _, group := range byFile(findings) {
			if _, err := e.build(s, "final-fix", "intermediate-dev", FixFindingsBrief(e.st, s, e.st.Base, e.head, group, nil, decision), true); err != nil {
				return err
			}
		}
		if err := e.gateLoop(s, "final", e.st.Base); err != nil {
			return err
		}
		rev, rep, err = e.review("final-review", "story-reviewer", ReReviewBrief(e.st, s, roundBase, e.head, findings, nil))
		if err != nil {
			return err
		}
		if !rev.Blocking() {
			return nil
		}
		a, err := e.ask(Question{Key: fmt.Sprintf("unresolved:final:%d", round), Kind: KindUnresolved, Text: "After the final fix round, its re-review still finds Critical or Important issues", Paths: paths(rep.Path)})
		if err != nil {
			return err
		}
		switch a {
		case WordStop:
			return errStopped
		case WordAccept:
			e.note("the whole implementation: the manager accepted %d findings left open", len(blocking(rev.Findings)))
			return nil
		}
		findings, decision = rev.Findings, a
	}
}

// byFile groups findings by file, in the order each file first appears.
func byFile(fs []Finding) [][]Finding {
	var order []string
	groups := map[string][]Finding{}
	for _, f := range fs {
		if _, ok := groups[f.File]; !ok {
			order = append(order, f.File)
		}
		groups[f.File] = append(groups[f.File], f)
	}
	out := make([][]Finding, 0, len(order))
	for _, file := range order {
		out = append(out, groups[file])
	}
	return out
}
```

- [ ] **Step 6: `record.go`.**

```go
package flowrun

import (
	"fmt"
	"strings"
)

// RecordInputs is what the record needs beside the state: the cost section
// as loom flow costs run prints it, each manager session's models, and the
// roster line at the end.
type RecordInputs struct {
	Cost      string
	Models    map[string][]string
	RosterEnd string
	Commits   int
	Head      string
}

// Record is the run's record (spec §5.7, flow/plugin/docs/records.md).
func Record(st *State, in RecordInputs) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Run: %s\n\n", st.ID)
	fmt.Fprintf(&b, "- Plan: %s (%d stories)\n- Spec: %s\n", st.Plan, len(st.Stories), st.Spec)
	fmt.Fprintf(&b, "- Commits: %s..%s (%d)\n", short(st.Base), short(in.Head), in.Commits)
	var managers []string
	for _, id := range st.Managers {
		if ms := in.Models[id]; len(ms) > 0 {
			managers = append(managers, strings.Join(ms, ", ")+" "+id)
		} else {
			managers = append(managers, id)
		}
	}
	fmt.Fprintf(&b, "- Manager: %s\n", strings.Join(managers, "; "))
	fmt.Fprintf(&b, "- Start: %s, End: %s\n", st.Start, st.End)
	roster := st.Roster
	if in.RosterEnd != "" && in.RosterEnd != st.Roster {
		roster += " → " + in.RosterEnd
	}
	fmt.Fprintf(&b, "- Definitions: %s\n- Profile: %s\n", roster, st.Profile)
	if st.DesignStage != "" {
		fmt.Fprintf(&b, "- Design stage: %s\n", st.DesignStage)
	}
	fmt.Fprintf(&b, "- Ended: %s\n- Engine: loom flow run (flow in loom part 3b)\n\n", st.Status)
	fmt.Fprintf(&b, "## Cost\n\n%s\n\n## Stories\n\n", strings.TrimSpace(in.Cost))
	b.WriteString("| Story | Developer | Tier | Review | Commits | Status | Deviations | BLOCKED, up a tier | Escalated to manager | Review findings | Turn cap hit |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range st.Stories {
		r := s.Row
		dev := r.Developer
		if dev == "" {
			dev = s.Developer
		}
		status := r.Status
		if status == "" {
			status = "not started"
		}
		cap := "no"
		if r.TurnCap {
			cap = "yes"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s | %d | %d | %d | %d/%d/%d | %s |\n",
			s.ID, dev, s.Tier, s.Review, r.Commits, status, r.Deviations, r.UpATier, r.Escalated, r.Findings[0], r.Findings[1], r.Findings[2], cap)
	}
	b.WriteString("\n## Notes\n\n")
	for _, a := range st.Answers {
		where := ""
		if a.Story != "" {
			where = ", Story " + a.Story
		}
		fmt.Fprintf(&b, "- Asked (%s%s): %s\n  Answered: %s\n", a.Kind, where, oneLine(a.Text), oneLine(a.Answer))
	}
	for _, n := range st.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	for _, g := range st.Gates {
		var r GateResult
		if raw, ok := st.Memo["gates:"+g]; ok && unmarshal(raw, &r) {
			fmt.Fprintf(&b, "- Gates %s: %s\n", g, r.Ended)
		}
	}
	if st.Why != "" {
		fmt.Fprintf(&b, "- %s\n", st.Why)
	}
	return b.String()
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
```

  `unmarshal(raw, v) bool` is `json.Unmarshal(raw, v) == nil`, placed in `state.go`.
- [ ] **Step 7: `CLAUDE.md` and `README.md`.** `CLAUDE.md` gets the rules:
  - **Make every engine decision from the state's inputs and memoized results only, and give every step a name from `next` and a memo key.** A restarted engine replays the procedure, and a decision on anything else (a clock, a fresh HEAD read outside `headAt`) takes another path on replay and submits a step twice or skips one. **Enforced** by `TestRun_ARestartAtEverySavePointSubmitsNothingTwice`.
  - **Check the tree only before a fresh dispatch** (`checkTree` inside `agent`, after `AgentStarted`). An agent followed after a restart moved HEAD with its own commits. **Enforced** by `TestRun_AFollowedAgentSkipsTheTreeCheck`.
  - **Reset the derived fields in `Run` and rebuild them on each pass.** Otherwise the rows, notes and kept Minors double on every restart. **Enforced** by `TestRun_ARestartRebuildsTheRowsOnce`.

  `README.md` holds the facts: the package's place, the state file, the replay model, and the `Effects` contract. Keep both inside the budgets (`go run ./tools/claudemd -v`).
- [ ] **Step 8: The engine's tests (`engine_test.go`).**

  The harness is a `fakeFX` that scripts each agent by name prefix and counts calls:

```go
// fakeFX is Effects scripted per agent and gate run: reports[name] is the
// report an agent run ends with, resumes[name] the reports of its resumes in
// order, gates[name] a gate run's result (passed when absent). It moves HEAD
// by one commit for each developer that reports done, records every call,
// and saves into saved.
type fakeFX struct {
	reports  map[string]Report
	resumes  map[string][]Report
	gates    map[string]GateResult
	started  map[string]bool // agent and gate runs that exist
	head     int
	dirty    bool
	premises string
	markers  []string
	calls    []string
	saves    int
	failAt   int // Save fails (a crash) at this save, 0 for never
	saved    *State
}
```

  Its methods:
  - `Agent` appends `agent <name> <role> <effort>`, marks it started and returns `reports[name]`, or `DONE` with `STATUS: DONE` text, or for a reviewer `VERDICT: APPROVED\nSTATUS: DONE`. When a developer's report is done, `head++` (`Head` returns `fmt.Sprintf("h%d", head)`).
  - `Resume` pops `resumes[name]`.
  - `Save` deep-copies the state (marshal/unmarshal) into `saved`, and returns an error when `saves == failAt`, as a crash.

  The cases, each as its own test, are below. Each asserts the `calls` list and the parked question or end.
  - `TestRun_AStoryRunsDeveloperGatesReview`: one vouched story ends `done`, calls in order. The kept Minors of an all-Minor review reach the final fix round.
  - `TestRun_NeedsContextParksAndTheAnswerResumes`: the run parks `needs-context`. After `st.Answer("blue", "m2")` it resumes the same agent with "blue", and `Managers` holds `m2`.
  - `TestRun_JuniorBlockedGoesToAnIntermediate`, `TestRun_JuniorTurnCapGoesToAnIntermediate`.
  - `TestRun_IntermediateTurnCapResumesOnceThenAsks`.
  - `TestRun_IntermediateBlockedAsksThenDecidesThenSenior`: blocked-decision; the decision appears in the next brief; a second BLOCKED goes to `senior-dev` without a question.
  - `TestRun_DoneWithNoCommitsIsAFailure`.
  - `TestRun_LandminesRunAtXHigh` (and a fix round at "").
  - `TestRun_GateFailureFixesThenAsks`: first FAIL → fix → gates again; a second FAIL → gate-failure. `retry`, `accept` and a decision each go their way. Infra parks at once.
  - `TestRun_AFailingServiceIsInfra`: Services errs, so the gates aren't started and the run parks gate-failure.
  - `TestRun_ReviewerChoice`: vouched → story-reviewer; full, DONE_WITH_CONCERNS, or markers → code-quality-reviewer.
  - `TestRun_CriticalIsFixedAndReReviewed`, `TestRun_DesignFindingAsksFirst`, `TestRun_StillBlockingAfterTheFixAsks`.
  - `TestRun_PlanLinesAreAskedOnce`.
  - `TestRun_AReviewWithoutAVerdictIsAFailure`.
  - `TestRun_ANonDeveloperBlockedIsAFailure`.
  - `TestRun_PremisesCosmeticAndDesign`: a COSMETIC verdict lands in the story's brief; DESIGN parks premise-design before any developer.
  - `TestRun_TreeMovedParks`, plus `continue` and `stop`.
  - `TestRun_AFollowedAgentSkipsTheTreeCheck`: an agent run that exists by name is followed with a moved HEAD and no question.
  - `TestRun_FinalRoundGroupsByFile`.
  - `TestRun_StopEndsStopped`.
  - `TestRun_ARestartAtEverySavePointSubmitsNothingTwice`, for a two-story script with a question. The fake logs a call to a run that already exists (`started`) as `follow <name>`, and moves HEAD only on a submit.
    1. Run the script once to completion (answering as it parks) and record N saves.
    2. For each k in 1..N, run again with `failAt: k` and restart from the last saved state until done, with `started` and `head` carried over.
    3. Assert that the multiset of submitted `agent …` and `gates …` calls equals the first run's.
  - `TestRun_ARestartRebuildsTheRowsOnce`.

  Plus `answers_test.go` (every kind × every word, text), `parse_test.go` (FINDING forms with markdown, a bad FINDING line, no VERDICT, PLAN lines, DEVIATION count, PremiseGroups of five flagged stories into 3+2, PremiseVerdicts), `briefs_test.go` (golden files in `testdata/`, regenerated with `-update`; a light story's developer block loses its landmine lines) and `record_test.go` (a two-story record against a golden file).
- [ ] **Step 9: Run.** `gofmt -l internal/flowrun && CGO_ENABLED=0 go vet ./internal/flowrun && CGO_ENABLED=0 go test ./internal/flowrun`. Mutation-check:
  - remove the memo check in `agent`: the restart test fails;
  - skip `checkTree` entirely: `TestRun_TreeMovedParks` fails;
  - check the tree for a started agent: the followed-agent test fails;
  - drop the junior escalation: its test fails;
  - drop the row reset in `Run`: the rebuild test fails;
  - route `full` to story-reviewer: the choice test fails.
- [ ] **Step 10: Commit:** `feat(flowrun): the run engine, replayed from memoized steps`.

---

## P5 — `loom flow run`, the skill and the docs

**Files:** create `internal/cli/flow_run.go`, `internal/cli/flow_run_fx.go`, `internal/cli/flow_run_test.go`, `flow/plugin/skills/run/SKILL.md`; modify `internal/cli/flow.go`, `internal/cli/flow_conf.go`, `flow/plugin/docs/records.md`, `flow/plugin/docs/dispatch.md`, `flow/README.md`, `internal/cli/CLAUDE.md`, `sched/CLAUDE.md`, `core/CLAUDE.md`, `USAGE.md`, `docs/claude/the-work-queue.md`.

- [ ] **Step 1: The tools seam (`flow_conf.go`).**

```go
// runTools runs flow's scripts for loom flow run (part 3b); tests replace
// newRunTools.
type runTools interface {
	Doctor() (out string, ok bool, err error)
	CheckPlan(plan string) (json []byte, fails string, err error)
	Show() (string, error)       // conf show --for supervisor
	Services() ([]string, error) // conf services
	Key() (string, error)        // conf key
	Premises(plan, rev string) (string, error)
	Conformance(plan, story, base, head string) (string, error)
	Costs(run string) (string, error)
}

var newRunTools = func(root string) runTools { return scriptTools{root: root} }
```

  `scriptTools` runs each through `flowScript`:
  - `flowconf.py doctor`: ok when no line starts with `FAIL`;
  - `check_plan.py --json PLAN`: exit 0 gives the JSON; exit 1 gives the FAIL lines as `fails`;
  - `flowconf.py show --for supervisor`, `flowconf.py services`, `flowconf.py key`;
  - `plan_premises.py PLAN --rev REV`: exit 0 gives "" and exit 1 the output;
  - `plan_conformance.py PLAN --story S --base B --head H`: exit 0 or 1 gives the output;
  - `agent_costs.py run RUN`.

  Any other exit is an error naming the script and its stderr.
- [ ] **Step 2: `flow_run_fx.go`, the adapter.** `runFX` implements `flowrun.Effects` over: the cobra command, the flow root, `flowConf`, `runTools`, the agents and gates dirs, the state path, cwd and the plan.
  - **`daemon(fn)`:** `flowDaemon`, retried on a `*joinError` with backoff from 2 s doubling to 30 s, for up to `rejoinFor = 10 * time.Minute` (planning amendment 8). It logs each retry to stdout, which is the engine job's `output.log`.
  - **`AgentStarted(name)`:** whether `agentPath(agents, name)` exists.
  - **`Agent(name, role, effort, brief)`:**
    - With no record: `agentArgv(root, role, effort, cwd, []byte(brief))`, then `d.startAgent(io.Discard, agents, &agentRun{Schema: agentRunSchema, ID: name, Role: role, Effort: effort, Cwd: cwd}, argv, []byte(brief))`.
    - An `*ExitError` from either is `Report{Status: "failed", Why: msg}`, not an error (spec §6, a refused submit).
    - Then `follow(name)`.
  - **`follow(name)`:** loops while the record isn't ended: `d.jobWithin(current, 170s)`; once the job has ended, `d.settleAgent(...)`. It maps the ended run to `flowrun.Report`: `Status` is the run's status, `Text` its report, `Question`, `Path: <agents>/<name>/report.md`, `Why`.
  - **`Resume(name, text)`:**
    - If the record's last job has `Answer == text`, follow it.
    - Else if its status is NEEDS_CONTEXT, BLOCKED or TURN_CAP: `d.answerAgent(io.Discard, agents, name, root, text)`, then follow.
    - Else an error.
  - **`GatesStarted(name)`:** whether `runPath(gates, name)` exists.
  - **`Services(cmds)`:** each runs as `exec.Command("sh", "-c", cmd)` in cwd, one after another, with output to stdout. The first non-zero exit is the error.
  - **`Gates(name, commits)`:**
    - With no record: build `&gateRun{Schema: gateRunSchema, Name: name, Kind: "story" or "final", Commits: commits, Cwd: cwd}`, get `conf.Gates("--story", commits…)` or `conf.Gates("--final")`, call `run.plan(lines)`, then `d.startRun(io.Discard, gates, run)`.
    - An `*ExitError` is `GateResult{Ended: "infra", Table: msg}`.
    - Then loop `d.advance(conf, gates, name)` and await its open jobs (as `waitRun` does, without a deadline) until `run.ended()`.
    - Map: `Failing` is the steps whose verdict is in `failing`, each with `Log`. `Ended` is `infra` when any is `verdictInfra`, else `failed` when any fails, else `passed`. `Table` is `printRun`'s text.
  - **`Head`, `Clean`, `Commits`:**
    - Head: `internalexec.GitCommand(ctx, cwd, "rev-parse", "HEAD")`.
    - Clean: `status --porcelain` is empty.
    - Commits: `rev-list --reverse base..head`.
  - **`Conformance(story, base, head)`:** `tools.Conformance(plan, …)`, written to `<state>/runs/<run>/conformance-<story>-<head[:9]>.txt`. `Markers` is parsed from its `MARKERS:` line.
  - **`Premises(rev)`:** `tools.Premises(plan, rev)`.
  - **`Save(st)`:** `flowrun.Save(statePath, st)`.
- [ ] **Step 3: `flow_run.go`, the commands.** `newFlowRunCmd()` holds `start`, `engine` (hidden), `wait`, `answer`, `show`, `list`, `cancel` and `resume`. `flow.go` attaches it.
  - **Helpers:**
    - `runsDir()` is `flowStateDir(conf, "runs")`.
    - `lockRunState(dir)` is a flock on `<dir>/runs.lock`, as `lockRuns` takes, with `runLockWait`.
    - `runStatePath(dir, id)`.
    - `latestRunState(dir, id)` mirrors `latestAgent`.
    - `engineArgv = func(id string) ([]string, error)` returns `[os.Executable(), "flow", "run", "engine", id]` (planning amendment 10).
    - `queueEngine(d, st)`: `flowJob(argv, "flow run "+st.ID, cwd, 0)` with `Kind: sched.KindRun` and the environment less `scrubbedVar`'s variables. It sets `st.Engine`.
  - **`start PLAN`** (spec §5.1):
    - `beginJob`, `flowRootOfSession`.
    - A clean tree (`status --porcelain`), else exit 2 with the status lines.
    - `tools.Doctor()`: not ok is exit 2 with the FAIL lines.
    - `tools.CheckPlan(plan)`: fails is exit 2 with them. Decode the JSON into `{spec, plan_review, stories: []flowrun.Story}`.
    - Lock. Refuse (exit 2) while any run here has a status that hasn't ended (`flowrun.Ended`).
    - ID: the plan's stem, then `-2`, `-3`, … while a state exists.
    - Build the state:
      - `Base` is HEAD and `Start` is now in UTC, RFC 3339;
      - `Roster` is `"loom flow roster " + <root>/ROSTER`;
      - `Profile`, `Blocks` and `RecordCopy` are split from `tools.Show()` (the `[name]` sections, and the `Profile:` and `Record copy in the repo:` lines; `none` is "");
      - `Blocks.Services` is `tools.Services()`;
      - `DesignStage` is the text after `**Design stage:**` on the plan's first such line;
      - `Managers` is `[CLAUDE_CODE_SESSION_ID]` when it is set;
      - `Status` is running.
    - Save, queue the engine, save again, and print `started <id>, job <N>`. A queue failure removes the state file and exits 125.
  - **`engine ID`** (hidden):
    - `os.Setenv("LOOM_FLOW_RUN", os.Getenv("LOOM_JOB"))` when `LOOM_JOB` is set.
    - Take `lockRunState` for the process's life; held is exit 125, "another engine has this run".
    - Load the state. An ended run is exit 0. Build `runFX` and call `flowrun.Run(st, fx)`.
    - An error: print it and exit 125, and the state stays running.
    - If the run ended: write the record (below), set `st.Record`, and save.
  - **`writeRecord(st)`:**
    - `tools.Costs(st.ID)`.
    - Parse its comment's JSON `managers` into `Models`.
    - `RosterEnd` is the `ROSTER` file again; `Head`; `Commits` is the count of `base..head`.
    - `text := flowrun.Record(st, in)`.
    - The path is `<flowDataDir()>/records/<tools.Key()>/<st.Start[:10]>-<plan stem without its leading YYYY-MM-DD->.md` (directory made 0o700); `AtomicWriteFile`.
    - When `RecordCopy != ""`, also write `<repo top>/<RecordCopy>/<same name>`.
    - A failure is put in `st.Why` and printed; the run stays ended.
  - **`wait [ID]`:**
    - Loop: load (no lock).
    - parked: print `parked <id>: <kind> (story <s>)`, then the text, `read: <paths>` and `answer with: <flowrun.Takes(kind)>`, and exit 0.
    - done or stopped: print `ended <id>: <status>`, `stories: <done> of <n>` and `record: <path>`, and exit 0. cancelled: the same, exit 1.
    - running: `d.jobWithin(st.Engine, 5s)`. If the job is unknown or finished, load again; if it is still running, print `lost <id>: the engine (job N) ended <state> without parking; loom flow run resume <id> carries on` and exit 1.
    - A lost daemon is exit 125.
  - **`answer ID < TEXT`** (spec §5.4):
    - stdin is 1 B to 48 KiB.
    - Lock and load. Not parked is exit 2.
    - `flowrun.CheckAnswer(kind, text)`: an error is exit 2 with its text.
    - `continue` with a dirty tree is exit 2.
    - `st.Answer(text, CLAUDE_CODE_SESSION_ID)`, save, queue the engine, save, and print `answered <id>: engine job <N>`.
  - **`resume [ID]`:** running, with the engine job unknown or finished: queue it. Otherwise exit 2, naming the status.
  - **`cancel [ID]`:**
    - Ended: say so, exit 0.
    - Cancel the engine job if it hasn't ended, and wait up to 30 s for it to end.
    - Cancel the last agent run in `st.Agents` that hasn't ended (its current job, marked cancelled as 3a's `cancel` does: factor that body into `d.cancelAgentRun(dir, name)`), and the last gate run in `st.Gates` that hasn't ended (`d.cancelRun`).
    - Lock and load. Set cancelled, `End`, no question; write the record; save; print `cancelled <id>`.
  - **`show [ID]`:** the ID, status and phase; then one row per story (ID, status, developer, commits, findings); then the open question with its paths and what it takes; then the engine job and the last agent and gate run; then `record:`.
  - **`list`:** one line per state: ID, status, phase, stories done of all, and the open question's kind.
- [ ] **Step 4: The tests (`flow_run_test.go`).**

  The fixture is `agentFixture` plus:
  - a fake flow root holding the roster's agents (`intermediate-dev`, `story-reviewer`, `code-quality-reviewer`, `premise-check`);
  - `useFakeConf` with `gates = ["RUN true"]`;
  - a `fakeTools` (replacing `newRunTools`) with a two-story JSON, empty premises, conformance `x` with no markers, and costs `COST`;
  - a temp git repo as cwd with one commit;
  - `engineArgv` replaced with `[]string{"true"}`.

  The fake `claude` is extended:
  - a prompt holding `Build Story 2` answers NEEDS_CONTEXT the first time;
  - every developer prompt (`Build Story` or `Fix`) makes a commit in its cwd (`git commit --allow-empty -m x`) before it reports `STATUS: DONE`;
  - `Review` prompts answer `VERDICT: APPROVED\nSTATUS: DONE`, except one holding `CRITICAL-STORY` (the first story's), whose first review is `FINDING: Critical a.go:3 fix it\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE`.

  `runRun(t, stdin, args…)` mirrors `runAgent`. The tests:
  - **`TestFlowRun_EndToEnd`:**
    1. `start` prints `started <stem>, job 1`, and job 1 is `Kind: run` with argv `true`.
    2. `engine <id>` parks: `wait` prints `parked …: needs-context (story 2)` and `answer with: an answer`.
    3. `answer <id>` with `blue` prints `answered …: engine job 2`.
    4. `engine <id>` runs to the end: `wait` prints `ended …: done`, and the record exists under `FLOW_DATA/records/<key>/` with its header, `COST`, two rows, and the question and answer in its notes.
    5. The agents' names are `<id>-s1-dev1`, `<id>-s1-review1`, `<id>-s1-fix1`, `<id>-s1-review2`, …, and the fix was followed by a second gate run `<id>-s1-gates2`.
  - **`TestFlowRun_ARestartedEngineFollowsWhatItQueued`:** stop the engine mid-run (the fake claude for Story 1 sleeps; cancel the in-process engine's context after the agent job is queued). `engine` again submits no second agent job for that name: the daemon holds one job per name.
  - **`TestFlowRun_Refusals`:**
    - outside a session (125);
    - a dirty tree (2);
    - a run not ended (2);
    - `answer` on a running run (2);
    - `answer` with `accept` on needs-context (2);
    - `continue` with a dirty tree (2);
    - `resume` on a parked run (2).
  - **`TestFlowRun_CancelEndsTheRunAndWritesItsRecord`.**
  - **`TestFlowRun_LostEngineSaysResume`:** the state is running and the engine job has ended: `wait` exits 1 with the `resume` line.
  - **`TestFlowRun_EngineJobEnvMarksTheRun`:** inside `engine`, an agent job's environment holds `LOOM_FLOW_RUN` with the `LOOM_JOB` the test sets, and the daemon records `Run` on it.
- [ ] **Step 5: The skill (`flow/plugin/skills/run/SKILL.md`).**

```markdown
---
name: run
description: Run a plan with loom's run engine from a fresh session (/clear, then /flow:run <plan>). The engine drives every story through headless flow agents and gates, and you, the manager, answer each question it parks on, from the spec or by asking the owner. It writes the run's record itself.
argument-hint: <plan path>
disable-model-invocation: true
---

Run the plan at `$ARGUMENTS` with loom's run engine. You are the manager.
Start from a fresh session (`/clear` first): every turn you take re-reads this
whole conversation, and your turns are the engine's questions, so keep them
short.

1. Run `loom flow run start $ARGUMENTS`. If it refuses, show its lines and
   stop. Read the spec its plan names: it is your only record of the design;
   answer from it, not from memory.
2. Run `loom flow run wait <run>` as a Bash call with `run_in_background`, and
   end your turn. It returns only when the run needs you or has ended.
3. When it returns parked, read the question and the paths it names. Answer
   from the spec when the spec settles it. When it doesn't, ask the owner;
   then write the answer into the spec and commit it by pathspec, so a later
   question finds it there. Then give your answer on stdin to
   `loom flow run answer <run>`, in the form the question says it takes
   (a decision as text, or one of its words), and start the background
   `wait` again. Stage nothing else, and start no developer or verifier in
   this worktree: the engine does.
4. For progress, run `loom flow run show <run>` rather than reading files.
   If `wait` says the engine was lost, run `loom flow run resume <run>` and
   wait again.
5. When it returns ended, read the record it names. When the profile's
   `Record copy in the repo:` names a directory, commit the copy there by
   pathspec. Then tell the owner what was built, what failed, and what needs
   them.
```

  Run `bash flow/lib/test.sh`: the agent lint covers skills and the banned words.
- [ ] **Step 6: Docs.**
  - `flow/plugin/docs/records.md`: the `- Ended:` and `- Engine:` lines, and that a run under `/flow:run` writes its own record.
  - `flow/plugin/docs/dispatch.md`: a section "The run engine (`/flow:run`)": what it replaces, the replay model, the questions, pause and kill.
  - `flow/README.md`: `loom flow run` and the skill.
  - `USAGE.md`: the commands, the `run` job kind in `loom job list`, and that a pause spares a run.
  - `docs/claude/the-work-queue.md`: the run kind and `Record.Run`.
  - Rules:
    - `sched/CLAUDE.md`: unclaimed kinds pass `Unclaimed`, never a `== KindAgent`, enforced by `TestAdmit_RunJobs` and `TestGrant`.
    - `core/CLAUDE.md`: a pause spares a run's jobs and a kill doesn't, enforced by `TestPause_SparesARunsJobs` and `TestKill_CancelsARunsJobs`.
    - `internal/cli/CLAUDE.md`: the engine holds `runs.lock` for its life, and `answer` writes only a parked run; a refused submit is a report, not an error. Enforced by `TestFlowRun_Refusals` and `TestFlowRun_EndToEnd`.
  - Check every budget with `go run ./tools/claudemd -v`, and move context into a guide rather than over a budget.
- [ ] **Step 7: Gates.**
  - `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')`.
  - `CGO_ENABLED=0 go vet ./...`.
  - `GOOS=windows CGO_ENABLED=0 go build ./...` and `GOOS=darwin CGO_ENABLED=0 go build ./...`.
  - `bash flow/lib/test.sh`.
  - The full suite as a job (`loom job submit -d "3b full suite" -- env CGO_ENABLED=0 go test ./...`), then race over `sched`, `core`, `internal/cli` and `internal/flowrun` (`CC=clang CGO_ENABLED=1 go test -race …`) with `--after`.
  - golangci-lint is skipped (CLAUDE.md's verification note).
- [ ] **Step 8: Mutation-check:**
  - `answer` accepting a running run: the refusal case;
  - the engine without the lock: a second `engine` in the restart test runs;
  - `queueEngine` without `Kind`: the start case.
- [ ] **Step 9: Commit:** `feat(cli,flow): loom flow run and /flow:run, the run engine's commands, record and skill`. The docs go in their own commit: `docs(flow,sched,core,cli): the run engine`.

---

## Execution notes

Executed inline on 2026-11-06.

- **P1, ccb40b47.** On Claude Code 2.1.318:
  - Premise 1 failed: `--agent` ignores a definition's `maxTurns` (5 turns against a cap of 2).
  - Premise 2 held: `--max-turns 2` ends the run with `subtype: error_max_turns`, `is_error: true` and no result text.
  - Premise 3 held: a resume of a capped run continues its session.
  - Premise 4 held after rewording. The first preamble's `[design]` notation was copied literally (`FINDING: Important [design] x.go:3 …`). The shipped wording spells it out (`FINDING: Important design a.go:12 <the fix>`), and the parser also accepts `[design]`.

  So agent jobs pass `--max-turns` from the definition (P3), on resumes too. Premise 1 is now logged, not asserted.
- **P2, 912f24cc.** Built as planned, with two differences:
  - A run job's `Uncontained` reason is its own (`runUncontained`).
  - The pause test pumps the shims' starts before the pause, as the agent test does.

  All five mutations went red.
- **P3, 887f921d.** Built as planned, with these differences:
  - `check_plan.py --json` takes one plan only.
  - `MARKERS:` matches a marker as a substring of an uncovered line.
  - `agent_costs.py run` puts the managers' models in a second comment, `<!-- agent-costs-managers {…} -->`, not in the totals comment, whose shape `records` reads (amendment 11 changed).
  - `rolePreamble` uses P1's wording.

  `bash flow/lib/test.sh`: 341 tests and the agent lint passed. All four Go mutations went red.
- **P4, edc4da5b.** Built from the plan's code, with two fixes the tests found:
  - A story's base is the head the engine last recorded (`e.head`), not a memoized fresh read. A fresh read absorbed a commit made outside the run, so the tree check never fired. It also broke the followed-agent case.
  - A fix round no longer sets the row's Developer (`if !fix`).

  Mutations:
  - Seven of eight went red.
  - Removing the agent memo survived: following an existing run by name already prevents a resubmit, so the memo only saves a re-follow.
  - Replaced it with two that bite, and both went red: names from a counter that outlives a pass, and HEAD read without its memo.
- **P5.** Built as planned, with these differences:
  - The wait method is `waitFlowRun`, since `waitRun` is the gate runner's.
  - The adapter awaits a just-queued job's view before following it.
  - `cancelAgentRun` is factored out of 3a's `cancel`.
  - The test fixture commits its plan files, so the tree is clean.
  - `core/CLAUDE.md` is 29 bytes under its budget, so the pause's spare is a rule in `docs/claude/the-work-queue.md` and in `internal/cli/CLAUDE.md`'s engine rule, not in core's.

  Mutations: three of four went red. The fourth (dropping `answer`'s status check) survived because a running run has no open question and `State.Answer` refuses it too: two layers.

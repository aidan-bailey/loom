# Flow in loom, part 3a: headless agent runs — Implementation Plan

> **For agentic workers:** executed inline (owner's choice, 2026-11-06) with superpowers:executing-plans, package by package, no checkpoint pauses. P1 is a gate: if premise 1, 5, 6 or 8 fails, stop and take the design back to the owner. Steps use checkbox (`- [ ]`) syntax.

**Goal:** The daemon runs one flow agent headless as a job of its own kind, reads its report, and resumes it with an answer.

**Architecture:**
- **`sched`:** a `Kind` field. Agent jobs take no claim, skip the advisor and the caps, and start under a per-account limit (`scheduler.agent_jobs`).
- **`core`:** skips sizing for them and shows the kind.
- **`loom flow agent`** (Go, `internal/cli`): builds the `claude -p --agent flow:<role>` command, submits it as an agent job, keeps a record in the worktree's state dir, and reads the stream-json result.
- **The probe:** an opt-in real-Claude test that gates the rest.

**Tech Stack:** Go (cobra, testify, `gofrs/flock`), the `internal/cli` hermetic test daemon with a fake `claude` script, Claude Code 2.1.318's `-p` stream-json.

**Spec:** `docs/superpowers/specs/2026-11-06-flow-in-loom-agent-runs-design.md` (965140c5).

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
- `config/CLAUDE.md`: a new setting goes in `Settings` with a getter, and pointers are deep-copied in `Clone`.

---

## Planning amendments (they win where the spec differs)

1. **The setting is `scheduler.agent_jobs`**, not `agent_limit`: `scheduler.agent` already names the advisor's block, and `agent_limit` beside it reads as the advisor's.
2. **No `rpc.Protocol` bump.** `core/rpc/rpc.go` says adding an optional field is compatible and needs none; `Kind` and `AccountKey` are optional fields.
3. **The probe runs as the default account**, as part 1's does: a private config dir has no login. Its transcript is under the default config dir's `projects/`.
4. **An agent job's environment drops the parent Claude session's variables:** `CLAUDECODE`, `AI_AGENT`, `CLAUDE_PID`, `CLAUDE_EFFORT`, `CLAUDE_JOB_DIR`, `LOOM_MOD_SOCKET`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_BRIDGE_SESSION_ID`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`, `CLAUDE_CODE_SESSION_ATTENDED` and `CLAUDE_CODE_EXECPATH`. They tie a process to the session that submitted it (its messaging bridge, its mod link, its effort), and a headless agent is a session of its own. A probe premise, 8, checks a scrubbed run started from inside a Claude session.
5. **Agent jobs get no parallelism hints.** `Spec.Kind` tells the shim to set only `LOOM_JOB` and `SCCACHE_CLIENT_SIDE`. The shim hints `max(HintCPUs, 1)`, so an agent's own `cargo check` would otherwise run single-threaded.
6. **`Grant` passes agent jobs over**, both their waits and their starts: an agent waiting for a slot isn't waiting for the caps, and would otherwise stop every grant.
7. **While admission is paused** (no usable policy), agent jobs wait too. `Admit` gives them no reason, so the view shows the pause's.
8. **The permission mode is read from the global `config.json`** (`Settings.PermissionMode()`), not each workspace's.

## File structure

| File | Responsibility |
|---|---|
| `session/claude_flow_agent_realclaude_test.go` (new) | the probe |
| `sched/job.go`, `sched/files.go`, `sched/hints.go`, `sched/shim_unix.go` | `Kind`, `AccountKey`, `Validate`'s agent rules, `Spec.Kind`, `AgentHints` |
| `sched/admit.go`, `sched/grant.go`, `sched/policy.go` | the agent branch of `Admit`, `Grant`'s pass-over, `Policy.AgentJobs` |
| `config/settings.go` | `SchedulerSettings.AgentJobs` |
| `core/queue_ops.go`, `core/queue_admit.go`, `core/queue_view.go` | submit without sizing, the spec's kind at start, `JobView.Kind` |
| `internal/cli/job_sizing.go`, `ui/work.go` | an agent job's claim cell reads `agent` |
| `internal/cli/flow_agent.go` (new) | `loom flow agent` commands and the record |
| `internal/cli/flow_agent_exec.go` (new) | the command builder, the environment scrub, the stream-json reader |
| `internal/cli/flow.go` | attach; the guard's `LOOM_FLOW_AGENT` fallback (only if premise 2 needs it) |
| tests beside each; docs in P4 | |

---

## P1 — The probe (the gate)

**Files:** create `session/claude_flow_agent_realclaude_test.go`.

- [ ] **Step 1: Commit this plan** (`git add` it, then `git commit -m "docs(plans): flow in loom, part 3a, in four packages" -- docs/superpowers/plans/2026-11-06-flow-in-loom-agent-runs.md`).

- [ ] **Step 2: Write the probe.**

```go
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentProbePlugin is the probe's plugin: an agent with pinned model and
// tools that preloads the plugin's skill, runs a Bash call the probe's
// PreToolUse hook allows and one it blocks, and reports in the schema.
var agentProbePlugin = map[string]string{
	".claude-plugin/plugin.json": `{"name": "flowprobe", "version": "0.0.1", "description": "loom agent-run probe"}` + "\n",
	"agents/probe-dev.md": "---\nname: probe-dev\ndescription: Probe agent for loom's agent-run test. Use only when told to.\nmodel: haiku\neffort: low\ntools: Bash\nskills:\n  - flowprobe:probe-habits\n---\n\n" +
		"Run these Bash commands, one call each, in this order: `git status --short`, `echo PROBE-ALLOWED`, `echo PROBE-BLOCKED`. " +
		"Then, if your brief asks a question of you, report NEEDS_CONTEXT with the question `Which colour?`; otherwise report DONE. " +
		"Put in your report the codeword your preloaded skill gives you and what each command returned.\n",
	"skills/probe-habits/SKILL.md": "---\nname: probe-habits\ndescription: Probe skill for loom's agent-run test.\nuser-invocable: false\n---\n\nThe codeword is QUINCE-7.\n",
}

const agentProbeSchema = `{"type":"object","required":["status","report"],"additionalProperties":false,` +
	`"properties":{"status":{"enum":["DONE","DONE_WITH_CONCERNS","NEEDS_CONTEXT","BLOCKED"]},"report":{"type":"string"},"question":{"type":"string"}}}`

// probeEvents splits a stream-json log into its init and result events.
func probeEvents(t *testing.T, out []byte) (initEv, result map[string]any) {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev["type"] == "system" && ev["subtype"] == "init":
			initEv = ev
		case ev["type"] == "result":
			result = ev
		}
	}
	return initEv, result
}

// TestRealClaude_FlowAgentRun checks the eight premises of flow in loom
// part 3a (spec §5.1, plan amendment 4) against the claude on PATH, as the
// default account. It spends a few cents of haiku, so it only runs with
// LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_FlowAgentRun(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"claude", "git", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	plugin := filepath.Join(root, "flowprobe")
	for rel, content := range agentProbePlugin {
		p := filepath.Join(plugin, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	pre := filepath.Join(root, "pretool")
	require.NoError(t, os.MkdirAll(pre, 0o700))
	hook := `f='` + pre + `/'"$(date +%s%N)-$$"'.json'; cat > "$f"; ` +
		`if grep -q PROBE-BLOCKED "$f"; then echo 'blocked by the probe hook' >&2; exit 2; fi; exit 0`
	settings, err := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "Bash|Write|Edit", "hooks": []any{map[string]any{"type": "command", "command": hook}},
	}}}})
	require.NoError(t, err)
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	require.NoError(t, exec.Command("git", "-C", work, "init", "-q").Run())

	// The probe's own shell is a loom session's, inside Claude Code: the
	// environment is scrubbed as an agent job's is (plan amendment 4), and
	// the default account is used.
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "CLAUDE_CONFIG_DIR" || probeScrubbed(name) {
			continue
		}
		env = append(env, kv)
	}
	run := func(prompt string, extra ...string) []byte {
		args := append([]string{"-p", prompt, "--agent", "flowprobe:probe-dev", "--plugin-dir", plugin,
			"--settings", string(settings), "--output-format", "stream-json", "--verbose",
			"--json-schema", agentProbeSchema}, extra...)
		cmd := exec.Command("claude", args...)
		cmd.Dir, cmd.Env = work, env
		out, err := cmd.Output()
		require.NoError(t, err, string(out))
		return out
	}

	out := run("Follow your instructions. Your brief asks you a question.")
	initEv, result := probeEvents(t, out)
	require.NotNil(t, initEv, "no init event:\n%s", out)
	require.NotNil(t, result, "no result event:\n%s", out)
	t.Logf("PROBE init: model=%v tools=%v session=%v", initEv["model"], initEv["tools"], initEv["session_id"])
	t.Logf("PROBE result: subtype=%v is_error=%v turns=%v cost=%v denials=%v", result["subtype"], result["is_error"], result["num_turns"], result["total_cost_usd"], result["permission_denials"])

	// Premise 1: the plugin agent is the main agent, with its pins.
	assert.Contains(t, initEv["model"], "haiku", "premise 1: the agent's pinned model")
	// Premise 8: a scrubbed run from inside a Claude session is a session of its own.
	assert.NotEqual(t, os.Getenv("CLAUDE_CODE_SESSION_ID"), initEv["session_id"], "premise 8")

	// Premise 2: the hook fired headless, exit 2 blocked, and what agent_type it saw.
	var seen []string
	payloads, err := os.ReadDir(pre)
	require.NoError(t, err)
	for _, p := range payloads {
		data, err := os.ReadFile(filepath.Join(pre, p.Name()))
		require.NoError(t, err)
		var in struct {
			AgentType string `json:"agent_type"`
		}
		require.NoError(t, json.Unmarshal(data, &in))
		seen = append(seen, in.AgentType)
	}
	t.Logf("PROBE RESULT: PreToolUse agent_type for a --agent main thread: %q", seen)
	require.NotEmpty(t, seen, "premise 2: the hook never fired")
	assert.Contains(t, string(out), "blocked by the probe hook", "premise 2: exit 2 blocked the call")

	// Premises 3, 4 and 6: the skill's codeword, the schema output, a routine command ran.
	so, ok := result["structured_output"].(map[string]any)
	require.True(t, ok, "premise 4: no structured_output:\n%s", out)
	assert.Equal(t, "NEEDS_CONTEXT", so["status"], "premise 4")
	assert.Equal(t, "Which colour?", so["question"], "premise 4")
	assert.Contains(t, so["report"], "QUINCE-7", "premise 3: the preloaded skill")
	assert.Contains(t, so["report"], "PROBE-ALLOWED", "premise 6: a routine command ran headless")

	// Premise 7: the transcript persists.
	session, _ := initEv["session_id"].(string)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", session+".jsonl"))
	assert.NotEmpty(t, matches, "premise 7: no transcript for session %s", session)

	// Premise 5: --resume continues with the agent's context.
	out = run("The answer is: blue. Finish, and say which colour you were given and the codeword.", "--resume", session)
	initEv, result = probeEvents(t, out)
	require.NotNil(t, result, "premise 5: no result on resume:\n%s", out)
	so, ok = result["structured_output"].(map[string]any)
	require.True(t, ok, "premise 5: no structured_output on resume:\n%s", out)
	assert.Equal(t, "DONE", so["status"], "premise 5")
	assert.Contains(t, strings.ToLower(so["report"].(string)), "blue", "premise 5: the answer reached it")
	assert.Contains(t, so["report"], "QUINCE-7", "premise 5: its context survived")
	assert.Equal(t, session, initEv["session_id"], "premise 5: the same session")
}

// probeScrubbed mirrors cli.scrubbedVar (plan amendment 4): the parent
// session's variables an agent job drops.
func probeScrubbed(name string) bool {
	switch name {
	case "CLAUDECODE", "AI_AGENT", "CLAUDE_PID", "CLAUDE_EFFORT", "CLAUDE_JOB_DIR", "LOOM_MOD_SOCKET",
		"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_BRIDGE_SESSION_ID",
		"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN",
		"CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_EXECPATH":
		return true
	}
	return false
}
```

- [ ] **Step 3: Run it.** `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session -run TestRealClaude_FlowAgentRun -v -count=1 2>&1 | tail -40`, directly: it's one headless haiku run, not a build. Expected: PASS, with the `PROBE` lines logged.

- [ ] **Step 4: Decide from the result.**
  - **Premise 2's `agent_type`:** if empty, P3 includes the guard fallback (P3 step 5); if `flowprobe:probe-dev`, P3 skips it.
  - **Premise 4:** if it failed, read the report from the result's `result` text instead (P3's `readResult` takes the last line of the form `STATUS: …`), and record that.
  - **Premise 1, 5, 6 or 8 failed:** stop, and take the findings to the owner.
  - Then append the findings to `## Execution notes` and commit the test and the plan by path: `test(session): probe headless flow agent runs against real Claude`.

---

## P2 — The agent job kind (`sched`, `config`, `core`)

**Files:** `sched/job.go`, `sched/files.go`, `sched/hints.go`, `sched/shim_unix.go`, `sched/admit.go`, `sched/grant.go`, `sched/policy.go`, `config/settings.go`, `core/queue_ops.go`, `core/queue_admit.go`, `core/queue_view.go`, `internal/cli/job_sizing.go`, `ui/work.go`, and their tests.

- [ ] **Step 1: The failing tests.** Add each case below to the named test file, reading the file's existing table and helper shapes first. These are the cases, each pinned from both sides:
  - **`sched/job_test.go` `TestSubmit_Validate`:**
    - an agent job (`Kind: KindAgent`) with `Claim{CPUs: 1}`, with `Claim{IO: true}`, with `Claim{Exclusive: true}`, and with `After: 3`: each refused with the text `an agent job claims nothing and runs after nothing`;
    - a bare agent job: accepted;
    - an unknown kind `"x"`: refused with `unknown job kind "x"`.
  - **`sched/admit_test.go`, new `TestAdmit_AgentJobs`:**
    - a host whose caps a running build fills: a queued agent starts and a queued build waits;
    - four running agents on account `a`, with `AgentJobs: 4`: a fifth on `a` waits with `waits for an agent slot (4 of 4 running on this account)`, and one on account `b` starts;
    - a queued agent that has waited past `StarveAfter`: the queued build behind it still starts (an agent is never the head);
    - a policy with no caps (`Policy{}`): an agent neither starts nor gets a reason.
  - **`sched/grant_test.go` `TestGrant`:** a queued agent waiting for a slot doesn't stop a grant, and a starting agent gets none.
  - **`sched/hints_test.go`, new `TestAgentHints_OnlyTheJobAndSccache`:** `AgentHints(env, 7)` adds `LOOM_JOB=7` and `SCCACHE_CLIENT_SIDE=1` and no `CARGO_BUILD_JOBS`, and replaces an inherited `LOOM_JOB`.
  - **`sched/policy_test.go` `TestResolve_Bounds` and `TestResolve_Defaults`:** `agent_jobs` defaults to 4, accepts 1 and 16, and refuses 17 and -1 with `scheduler.agent_jobs is 17: it must be 1–16`.
  - **`sched/store_test.go`:** a record with `Kind: KindAgent, AccountKey: "/acc/a"` round-trips through `SaveStore` and `LoadStore`.
  - **`core/queue_*_test.go`** (the file with `TestJobQueue_…` submit tests):
    - `TestJobQueue_AnAgentJobIsNeverSized`: submitting `Kind: KindAgent` with `CLAUDE_CONFIG_DIR=/acc/a` in its env makes a record with a zero `Claim`, no `Signature`, no `Sizing`, `AccountKey == "/acc/a"`, and no advisor call; its `JobView.Kind` is `agent`.
    - A build submit, as before, is sized.
  - **`internal/cli/job_test.go`:** `claimText` of an agent `JobView` is `agent`.
  - **`ui/work_test.go`:** `workClaim` of one is `agent`.

- [ ] **Step 2: Run them to see them fail.** `CGO_ENABLED=0 go test ./sched ./core ./internal/cli ./ui -run 'Validate|Agent|Grant|Hints|Resolve|Store|claimText|workClaim'`. Expected: build failures (`KindAgent`, `AgentHints`, `AgentJobs` undefined).

- [ ] **Step 3: `sched/job.go`.**

```go
// KindAgent is an agent job's kind (flow in loom part 3a, spec §5.2): a
// headless Claude process, admitted by a per-account limit, never by the
// caps, and never sized. A build job's kind is "".
const KindAgent = "agent"
```

`Record` gains, after `HoldBroke`:

```go
	// Agent runs (flow in loom part 3a, spec §5.2): the job's kind ("" for
	// a build, KindAgent), and an agent job's account: its environment's
	// CLAUDE_CONFIG_DIR ("" for the default account).
	Kind       string `json:"kind,omitempty"`
	AccountKey string `json:"account_key,omitempty"`
```

`Submit` gains `Kind string \`json:"kind,omitempty"\`` after `After`. In `Validate`, after the `After < 0` check:

```go
	switch {
	case s.Kind != "" && s.Kind != KindAgent:
		return fmt.Errorf("unknown job kind %q", s.Kind)
	case s.Kind == KindAgent && (s.Claim != Claim{} || s.After != 0):
		return errors.New("an agent job claims nothing and runs after nothing")
	}
```

Add to the comment of `Validate`: "an agent job claims nothing and runs after nothing, and no other kind is known."

- [ ] **Step 4: `sched/files.go`, `sched/hints.go`, `sched/shim_unix.go`.** `Spec` gains `Kind string \`json:"kind,omitempty"\`` (written at submit). In `hints.go`:

```go
// AgentHints is an agent job's environment (flow in loom part 3a, plan
// amendment 5): env with SCCACHE_CLIENT_SIDE added when it lacks it and
// LOOM_JOB set to id, and no parallelism variables, which a job with no
// claim would get as 1.
func AgentHints(env []string, id JobID) []string {
	out := make([]string, 0, len(env)+2)
	sccache := false
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		switch name {
		case EnvJob:
			continue
		case "SCCACHE_CLIENT_SIDE":
			sccache = true
		}
		out = append(out, e)
	}
	if !sccache {
		out = append(out, "SCCACHE_CLIENT_SIDE=1")
	}
	return append(out, EnvJob+"="+strconv.FormatInt(int64(id), 10))
}
```

In `shim_unix.go`, line 165 becomes:

```go
	env := Hints(spec.Env, max(spec.HintCPUs, 1), spec.ID)
	if spec.Kind == KindAgent {
		env = AgentHints(spec.Env, spec.ID)
	}
```

- [ ] **Step 5: `sched/policy.go` and `config/settings.go`.** `SchedulerSettings` gains `AgentJobs int \`json:"agent_jobs,omitempty"\`` (an int, so `Clone` needs nothing). `Policy` gains `AgentJobs int \`json:"agent_jobs"\``. `Resolve`, after `keep`:

```go
	agentJobs, err := resolveBounded("agent_jobs", s.AgentJobs, 4, 1, maxAgentJobs)
	if err != nil {
		return Policy{}, err
	}
```

Add `maxAgentJobs = 16` beside the other bounds, set `AgentJobs: agentJobs` in the returned `Policy`, and add `agent_jobs is 4` and `agent_jobs` to the doc comment's defaults and order.

- [ ] **Step 6: `sched/admit.go`.** At the top of `Admit`, the scan becomes:

```go
	d := Decision{Waiting: map[JobID]string{}}
	var used Need
	var queued, agents []Record
	running := map[string]int{} // running agent jobs per account key
	for _, r := range jobs {
		switch {
		case r.Kind == KindAgent && r.State == Running:
			running[r.AccountKey]++
		case r.Kind == KindAgent && r.State == Queued:
			agents = append(agents, r)
		case r.State == Running:
			used = used.plus(runningNeed(r, p, holds))
		case r.State == Queued:
			queued = append(queued, r)
		}
	}
	byOrder := func(a, b Record) int { return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID)) }
	// Agent jobs (flow in loom part 3a, spec §5.2) start by their account's
	// limit, never by the caps, and are never a head; while admission has
	// no usable policy they wait with no reason, so the view shows its.
	slices.SortFunc(agents, byOrder)
	for _, r := range agents {
		switch {
		case !p.usable():
		case running[r.AccountKey] >= p.AgentJobs:
			d.Waiting[r.ID] = fmt.Sprintf("waits for an agent slot (%d of %d running on this account)", running[r.AccountKey], p.AgentJobs)
		default:
			running[r.AccountKey]++
			d.Start = append(d.Start, r.ID)
		}
	}
	slices.SortFunc(queued, byOrder)
```

(The old `slices.SortFunc(queued, …)` call is replaced by the last line.) Extend `Admit`'s doc comment with a fourth point: "Agent jobs (Kind KindAgent) are apart: each starts, in queue order, while fewer than p.AgentJobs of its account run; they take nothing from the caps and are never the head."

- [ ] **Step 7: `sched/grant.go`.** In `Grant`, replace the `dec.Waiting` loop and skip agents among the starts and the running:

```go
	agent := map[JobID]bool{}
	for _, r := range jobs {
		if r.Kind == KindAgent {
			agent[r.ID] = true
		}
	}
	for id, why := range dec.Waiting {
		if why != waitOver && !agent[id] {
			return nil
		}
	}
```

In the `for _, r := range jobs` loop that sums `used`, `continue` first when `agent[r.ID]`. In the `for _, id := range dec.Start` loop, `continue` first when `agent[id]`. Add "Agent jobs are passed over: their waits stop nothing, and they are granted nothing." to the doc comment.

- [ ] **Step 8: `core`.**
  - **`queue_ops.go` `submitJob`:** after `prereq, err := …`, build the record by kind:

```go
	now := time.Now()
	agentJob := s.Kind == sched.KindAgent
	var (
		sig               *sched.Signature
		checkout, unsized string
	)
	if agentJob {
		unsized = "an agent job is never sized"
	} else {
		sig, checkout, unsized = jobSignature(s.Argv, s.Cwd)
	}
```

  Then, in the record literal, add `Kind: s.Kind`, and when `agentJob`, `rec.AccountKey = envValue(s.Env, "CLAUDE_CONFIG_DIR")`. Wrap `q.sizeRecord(&rec, policy)` and `sched.CheckClaim` in `if !agentJob { … }`; `askForPrior` is already skipped with `sig == nil`. Add the helper:

```go
// envValue is name's value in env, "" when it is unset.
func envValue(env []string, name string) string {
	for _, kv := range slices.Backward(env) {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}
```

  (If `jobSignature`'s first result isn't `*sched.Signature`, declare `sig` as that type.)
  - **`sched.WriteSpec`:** called with `Kind: rec.Kind` in the `Spec` literal of `submitJob`. In `queue_admit.go`, where the spec is rewritten with `HintCPUs`, the `Kind` read back from `spec.json` is kept as it is.
  - **`queue_view.go`:** `JobView` gains `Kind string // sched.KindAgent for an agent job (flow in loom part 3a)`, set as `Kind: r.Kind` in `queueJobView`.

- [ ] **Step 9: The claim cells.** In `internal/cli/job_sizing.go` `claimText` and `ui/work.go` `workClaim`, return `"agent"` first when `v.Kind == sched.KindAgent` (`j.Kind`).

- [ ] **Step 10: Run, gofmt, vet, the cross-builds.** `gofmt -l sched core config internal/cli ui && CGO_ENABLED=0 go vet ./sched/... ./core/... ./config/... ./internal/cli/... ./ui/... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./...`, then the Step 2 command. Expected: PASS. Run `go test ./core/rpc -run 'TestProtocolReference|TestGenerated_IsFresh'`: if the reference names `JobView`'s fields and fails, regenerate it with `-update` (a generated file, never edited by hand).

- [ ] **Step 11: Mutation-check:**
  - drop the agent branch's limit check: the five-agents case fails;
  - count running agents into `used`: the full-host case fails;
  - drop `Grant`'s `!agent[id]`: the grant case fails;
  - use `Hints` for agent jobs: `TestAgentHints_OnlyTheJobAndSccache`, or a core case asserting the spec's kind, fails;
  - drop `Validate`'s agent rule: the claim cases fail.

- [ ] **Step 12: Commit:** `feat(sched,core,config): an agent job kind, admitted by a per-account limit outside the caps and the advisor`, with every touched file by path.

---

## P3 — `loom flow agent`

**Files:** create `internal/cli/flow_agent.go`, `internal/cli/flow_agent_exec.go`, `internal/cli/flow_agent_test.go`; modify `internal/cli/flow.go`.

- [ ] **Step 1: The failing tests.** `internal/cli/flow_agent_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaude is a claude that writes its argv and environment under
// $FAKE_CLAUDE_LOG and answers in stream-json by its prompt: ASK →
// NEEDS_CONTEXT; BROKEN → no result; FAILING → an error result; BADSCHEMA →
// a status the schema doesn't know; MUTE → NEEDS_CONTEXT with no question;
// anything else, or a resume → DONE.
const fakeClaude = `#!/bin/sh
n=$(ls "$FAKE_CLAUDE_LOG" | grep -c "\.args$")
printf '%s\n' "$@" > "$FAKE_CLAUDE_LOG/$n.args"
env > "$FAKE_CLAUDE_LOG/$n.env"
prompt=""; resume=""
while [ $# -gt 0 ]; do
  case "$1" in
    -p) prompt="$2"; shift 2;;
    --resume) resume="$2"; shift 2;;
    *) shift;;
  esac
done
echo '{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-fake"}'
result() { echo '{"type":"result","subtype":"success","is_error":'"$1"',"num_turns":3,"total_cost_usd":0.25,"session_id":"sess-1","result":"text","structured_output":'"$2"'}'; }
if [ -n "$resume" ]; then result false '{"status":"DONE","report":"answered"}'; exit 0; fi
case "$prompt" in
  *BROKEN*) exit 0;;
  *FAILING*) result true 'null'; exit 1;;
  *BADSCHEMA*) result false '{"status":"MAYBE","report":"x"}';;
  *MUTE*) result false '{"status":"NEEDS_CONTEXT","report":"x"}';;
  *ASK*) result false '{"status":"NEEDS_CONTEXT","report":"need one","question":"Which colour?"}';;
  *) result false '{"status":"DONE","report":"all done"}';;
esac
`

// agentFixture serves the test daemon with a fake claude first on PATH, a
// flow root holding the agents named, and the fake flowconf's state dir.
func agentFixture(t *testing.T, agents ...string) (*testDaemon, *fakeConf, string) {
	t.Helper()
	d, conf := flowJobsDaemon(t)
	bin, logDir := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_CLAUDE_LOG", logDir)
	root := fakeFlowRoot(t, nil)
	for _, a := range agents {
		p := filepath.Join(root, "plugin", "agents", a+".md")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte("---\nname: "+a+"\n---\n"), 0o600))
	}
	t.Setenv(flow.EnvRoot, root)
	return d, conf, logDir
}

// runAgent runs `loom flow agent args…` with stdin.
func runAgent(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := newFlowCmd()
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"agent"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestFlowAgent_RunWaitDone(t *testing.T) {
	d, conf, logDir := agentFixture(t, "intermediate-dev")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "the-parent")
	t.Setenv("CLAUDE_CONFIG_DIR", "/acc/a")

	out, _, err := runAgent(t, "Build story 1.", "run", "intermediate-dev", "--effort", "xhigh")
	require.NoError(t, err)
	assert.Equal(t, "started intermediate-dev-1: flow:intermediate-dev, job 1\n", out)
	j := d.job(1)
	assert.Equal(t, sched.KindAgent, j.Kind)

	out, _, err = runAgent(t, "", "wait", "60")
	require.NoError(t, err)
	report := filepath.Join(conf.state, "agents", "intermediate-dev-1", "report.md")
	assert.Equal(t, "done intermediate-dev-1: DONE\nreport: "+report+"\n", out)
	b, err := os.ReadFile(report)
	require.NoError(t, err)
	assert.Equal(t, "all done\n", string(b))

	// One argument per line; the prompt holds newlines of its own.
	b, err = os.ReadFile(filepath.Join(logDir, "0.args"))
	require.NoError(t, err)
	args := string(b)
	assert.True(t, strings.HasPrefix(args, "-p\n"+headlessPreamble+"\n\nBuild story 1.\n--agent\nflow:intermediate-dev\n"), args)
	assert.Contains(t, args, "--effort\nxhigh\n")
	assert.Contains(t, args, "--output-format\nstream-json\n")
	assert.NotContains(t, args, "--resume")
	env, err := os.ReadFile(filepath.Join(logDir, "0.env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "LOOM_FLOW_AGENT=intermediate-dev")
	assert.Contains(t, string(env), "CLAUDE_CONFIG_DIR=/acc/a", "the starting session's account")
	assert.NotContains(t, string(env), "the-parent", "the parent session's variables are dropped")

	out, _, err = runAgent(t, "", "show")
	require.NoError(t, err)
	assert.Contains(t, out, "session: sess-1")
	assert.Contains(t, out, "cost: $0.25")
}

func TestFlowAgent_AQuestionIsAnsweredByAResume(t *testing.T) {
	_, _, logDir := agentFixture(t, "intermediate-dev")
	_, _, err := runAgent(t, "ASK me", "run", "intermediate-dev", "--name", "s1")
	require.NoError(t, err)

	out, _, err := runAgent(t, "", "wait", "s1", "60")
	require.NoError(t, err)
	assert.Contains(t, out, "done s1: NEEDS_CONTEXT\nquestion: Which colour?\n")

	_, _, err = runAgent(t, "", "answer", "s1")
	assert.Equal(t, exitUsage, flowExit(err), "an empty answer")
	out, _, err = runAgent(t, "Blue.", "answer", "s1")
	require.NoError(t, err)
	assert.Equal(t, "resumed s1: job 2\n", out)
	out, _, err = runAgent(t, "", "wait", "s1", "60")
	require.NoError(t, err)
	assert.Contains(t, out, "done s1: DONE\n")

	args, err := os.ReadFile(filepath.Join(logDir, "1.args"))
	require.NoError(t, err)
	assert.Contains(t, string(args), "--resume\nsess-1\n")
	assert.Contains(t, string(args), "-p\nBlue.\n")

	_, _, err = runAgent(t, "Again.", "answer", "s1")
	assert.Equal(t, exitUsage, flowExit(err), "a run that is DONE takes no answer")
}

func TestFlowAgent_AJobWithoutAUsableReportFails(t *testing.T) {
	agentFixture(t, "intermediate-dev")
	for prompt, why := range map[string]string{
		"BROKEN":    "no result",
		"FAILING":   "an error result",
		"BADSCHEMA": `status "MAYBE"`,
		"MUTE":      "a question was asked but not given",
	} {
		name := strings.ToLower(prompt)
		_, _, err := runAgent(t, prompt, "run", "intermediate-dev", "--name", name)
		require.NoError(t, err)
		out, _, err := runAgent(t, "", "wait", name, "60")
		assert.Equal(t, 1, flowExit(err), prompt)
		assert.Contains(t, out, "failed "+name+": ", prompt)
		assert.Contains(t, out, why, prompt)
	}
}

func TestFlowAgent_Refusals(t *testing.T) {
	agentFixture(t, "intermediate-dev")
	for _, c := range []struct {
		stdin string
		args  []string
		code  int
		says  string
	}{
		{"x", []string{"run", "no-such-agent"}, exitUsage, "no agent no-such-agent"},
		{"", []string{"run", "intermediate-dev"}, exitUsage, "the brief is empty"},
		{strings.Repeat("x", 49<<10), []string{"run", "intermediate-dev"}, exitUsage, "at most 48 KiB"},
		{"x", []string{"run", "intermediate-dev", "--name", "bad name"}, exitUsage, "bad name"},
		{"", []string{"wait", "never"}, exitNotRun, "never"},
	} {
		_, _, err := runAgent(t, c.stdin, c.args...)
		assert.Equal(t, c.code, flowExit(err), "%q", c.args)
		assert.ErrorContains(t, err, c.says, "%q", c.args)
	}
	t.Setenv(flow.EnvRoot, "")
	_, _, err := runAgent(t, "x", "run", "intermediate-dev")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "inside a session")
}

func TestFlowAgent_ANameWhoseRunHasntEndedIsRefusedAndCancelEndsIt(t *testing.T) {
	d, _, _ := agentFixture(t, "intermediate-dev")
	_, _, err := runAgent(t, "ASK", "run", "intermediate-dev", "--name", "s1")
	require.NoError(t, err)
	_, _, err = runAgent(t, "ASK", "run", "intermediate-dev", "--name", "s1")
	assert.Equal(t, exitUsage, flowExit(err))
	assert.ErrorContains(t, err, "s1 hasn't ended")

	out, _, err := runAgent(t, "", "cancel", "s1")
	require.NoError(t, err)
	assert.Contains(t, out, "cancelled s1")
	d.waitFinished(t, 1)
	_, _, err = runAgent(t, "x", "run", "intermediate-dev", "--name", "s1")
	require.NoError(t, err, "a cancelled run's name is free again")
}

func TestScrubbedVar(t *testing.T) {
	for _, name := range []string{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_TOKEN", "LOOM_MOD_SOCKET", "CLAUDE_EFFORT"} {
		assert.True(t, scrubbedVar(name), name)
	}
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "PATH", "LOOM_FLOW_ROOT", "LOOM_LAUNCH", "CLAUDE_CODE_USE_BEDROCK", "ANTHROPIC_API_KEY"} {
		assert.False(t, scrubbedVar(name), name)
	}
}
```

- [ ] **Step 2: Run them to see them fail.** `CGO_ENABLED=0 go test ./internal/cli -run 'TestFlowAgent|TestScrubbedVar'`. Expected: build failure.

- [ ] **Step 3: `internal/cli/flow_agent_exec.go`**, the pure parts:

```go
package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/flow"
)

// headlessPreamble goes before every agent run's brief (flow in loom part
// 3a, spec §5.4), so the agent definitions stay as they are.
const headlessPreamble = "You run headless as a loom agent job: nobody can message you, and SendMessage reaches no one. " +
	"Your final answer is your report, in the given schema: status DONE, DONE_WITH_CONCERNS or BLOCKED, or " +
	"NEEDS_CONTEXT with your question, whose answer resumes you. Run long steps through `loom flow wait`, as your rules say."

// agentReportSchema is the report every agent run answers in (spec §5.6).
const agentReportSchema = `{"type":"object","required":["status","report"],"additionalProperties":false,` +
	`"properties":{"status":{"enum":["DONE","DONE_WITH_CONCERNS","NEEDS_CONTEXT","BLOCKED"]},` +
	`"report":{"type":"string"},"question":{"type":"string"}}}`

// envAgentRole names the flow agent an agent job runs, for the guard's
// fallback when a hook's agent_type is empty.
const envAgentRole = "LOOM_FLOW_AGENT"

// maxBrief bounds a brief: a job's argv is capped at 64 KiB.
const maxBrief = 48 << 10

// agentCommand is the argv of an agent job (spec §5.4): a first run when
// session is "", else a resume of it with prompt as the answer.
func agentCommand(program, role, effort, mode, root, guard, prompt, session string) ([]string, error) {
	settings, err := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "Bash|Write|Edit",
		"hooks":   []any{map[string]any{"type": "command", "command": guard, "timeout": 10}},
	}}}})
	if err != nil {
		return nil, err
	}
	argv := []string{program, "-p", prompt, "--agent", "flow:" + role,
		"--plugin-dir", root + "/plugin", "--settings", string(settings)}
	if session != "" {
		argv = append(argv, "--resume", session)
	}
	if mode != "" && mode != "default" {
		argv = append(argv, "--permission-mode", mode)
	}
	if effort != "" {
		argv = append(argv, "--effort", effort)
	}
	return append(argv, "--output-format", "stream-json", "--verbose", "--json-schema", agentReportSchema), nil
}

// scrubbedVar reports whether an agent job drops the variable name from
// its submitter's environment (plan amendment 4): the parent Claude
// session's own.
func scrubbedVar(name string) bool {
	switch name {
	case "CLAUDECODE", "AI_AGENT", "CLAUDE_PID", "CLAUDE_EFFORT", "CLAUDE_JOB_DIR", "LOOM_MOD_SOCKET",
		"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_BRIDGE_SESSION_ID",
		"CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN",
		"CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_EXECPATH":
		return true
	}
	return false
}

// agentEnv is an agent job's environment: env without the scrubbed
// variables, with LOOM_FLOW_AGENT set to role.
func agentEnv(env []string, role string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !scrubbedVar(name) && name != envAgentRole {
			out = append(out, kv)
		}
	}
	return append(out, envAgentRole+"="+role)
}

// agentProgram is the Claude CLI agent jobs run: the first word of the
// global config's program, as sessions launch it, else "claude".
func agentProgram() string {
	if words := strings.Fields(claudeProgram()); len(words) > 0 {
		return words[0]
	}
	return "claude"
}

// agentPermissionMode is the global config's Claude permission mode (plan
// amendment 8).
func agentPermissionMode() string {
	dir, err := config.GetGlobalConfigDir()
	if err != nil {
		return "default"
	}
	return config.LoadConfigFrom(dir).Snapshot().PermissionMode()
}

// agentGuard is the guard hook's command: this loom binary's `flow guard`.
func agentGuard() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if strings.Contains(exe, "'") {
		return "", fmt.Errorf("the loom binary's path %s holds a quote", exe)
	}
	return "'" + exe + "' flow guard", nil
}

// agentRoleOK reports whether root's plugin defines the agent role.
func agentRoleOK(root, role string) bool {
	if !flowName.MatchString(role) {
		return false
	}
	_, err := os.Stat(root + "/plugin/agents/" + role + ".md")
	return err == nil
}

// agentResult is what an agent job's stream-json log says when it ended.
type agentResult struct {
	Session  string
	Status   string
	Report   string
	Question string
	CostUSD  float64
	Turns    int
}

var agentStatuses = []string{"DONE", "DONE_WITH_CONCERNS", "NEEDS_CONTEXT", "BLOCKED"}

// readResult reads an ended agent job's stream-json log: the session from
// its init event and the report from its result event. An error says why
// the job left no usable report.
func readResult(log []byte) (agentResult, error) {
	var res agentResult
	var final map[string]json.RawMessage
	sc := bufio.NewScanner(bytes.NewReader(log))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "system" && ev.Subtype == "init":
			res.Session = ev.SessionID
		case ev.Type == "result":
			final = nil
			if err := json.Unmarshal(sc.Bytes(), &final); err != nil {
				return res, fmt.Errorf("its result can't be read: %w", err)
			}
			if ev.SessionID != "" {
				res.Session = ev.SessionID
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("its output can't be read: %w", err)
	}
	if final == nil {
		return res, errors.New("it ended with no result")
	}
	var meta struct {
		IsError bool    `json:"is_error"`
		Cost    float64 `json:"total_cost_usd"`
		Turns   int     `json:"num_turns"`
	}
	raw, _ := json.Marshal(final)
	_ = json.Unmarshal(raw, &meta)
	res.CostUSD, res.Turns = meta.Cost, meta.Turns
	if meta.IsError {
		return res, errors.New("it ended with an error result")
	}
	var report struct {
		Status   string `json:"status"`
		Report   string `json:"report"`
		Question string `json:"question"`
	}
	so := final["structured_output"]
	if len(so) == 0 || string(so) == "null" || json.Unmarshal(so, &report) != nil {
		return res, errors.New("it gave no structured report")
	}
	if !slices.Contains(agentStatuses, report.Status) {
		return res, fmt.Errorf("its report has status %q", report.Status)
	}
	if report.Status == "NEEDS_CONTEXT" && strings.TrimSpace(report.Question) == "" {
		return res, errors.New("a question was asked but not given")
	}
	res.Status, res.Report, res.Question = report.Status, report.Report, report.Question
	return res, nil
}

// flowRootOfSession is the launch's flow root, refused outside a session.
func flowRootOfSession() (string, error) {
	root := os.Getenv(flow.EnvRoot)
	switch {
	case root == "":
		return "", &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + flow.EnvRoot + " is unset; run it inside a session loom launched"}
	case !flow.IsRoot(root):
		return "", &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %s=%s is not a flow root", flow.EnvRoot, root)}
	}
	return root, nil
}
```

  The fake root `fakeFlowRoot` makes is a flow root (`flow.IsRoot`); if it isn't, give the fixture what `IsRoot` checks.

- [ ] **Step 4: `internal/cli/flow_agent.go`**, the commands and the record:

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
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/gofrs/flock"
	"github.com/spf13/cobra"
)

// agentRunSchema is the agent-run record's version (spec §5.5).
const agentRunSchema = 1

// An agent run's statuses beside a report's own (spec §5.5).
const (
	agentRunning   = "running"
	agentFailed    = "failed"
	agentCancelled = "cancelled"
)

// agentRun is one `loom flow agent run` and every job after it on its
// Claude session: <state>/agents/<id>.json.
type agentRun struct {
	Schema   int        `json:"schema"`
	ID       string     `json:"id"`
	Role     string     `json:"role"`
	Effort   string     `json:"effort,omitempty"`
	Cwd      string     `json:"cwd"`
	Brief    string     `json:"brief"`
	Session  string     `json:"session,omitempty"`
	Jobs     []agentJob `json:"jobs"`
	Status   string     `json:"status"`
	Question string     `json:"question,omitempty"`
	Report   string     `json:"report,omitempty"`
	CostUSD  float64    `json:"cost_usd,omitempty"`
	Turns    int        `json:"turns,omitempty"`
	Why      string     `json:"why,omitempty"`
}

// agentJob is one job of a run: its start, or an answer resuming it.
type agentJob struct {
	ID     sched.JobID `json:"id"`
	Answer string      `json:"answer,omitempty"`
	Read   bool        `json:"read,omitempty"` // its result is in the record
}

func (r *agentRun) current() *agentJob { return &r.Jobs[len(r.Jobs)-1] }

// ended reports whether the run has a report or ended without one.
func (r *agentRun) ended() bool { return r.Status != agentRunning }

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
			if !agentRoleOK(root, role) {
				return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow agent: the flow plugin has no agent %s", role)}
			}
			if name != "" && !flowName.MatchString(name) {
				return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow agent: bad name %q", name)}
			}
			brief, err := io.ReadAll(io.LimitReader(c.InOrStdin(), maxBrief+1))
			switch {
			case err != nil:
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the brief: %v", err)}
			case strings.TrimSpace(string(brief)) == "":
				return &ExitError{Code: exitUsage, Msg: "loom flow agent: the brief is empty; give it on stdin"}
			case len(brief) > maxBrief:
				return &ExitError{Code: exitUsage, Msg: "loom flow agent: the brief is at most 48 KiB"}
			}
			guard, err := agentGuard()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
			}
			cwd, err := os.Getwd()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the working directory: %v", err)}
			}
			dir, cleanup, err := agentDir()
			if err != nil {
				return err
			}
			defer cleanup()
			argv, err := agentCommand(agentProgram(), role, effort, agentPermissionMode(), root, guard, headlessPreamble+"\n\n"+string(brief), "")
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
			}
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
	} else if old, err := readAgentRun(agentPath(dir, run.ID)); err == nil && !old.ended() {
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
			"it runs, 1 when it ended without a usable report (failed or cancelled), and 125 when it can't follow it.",
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
			return flowDaemon(c, func(d *client) error { return d.waitAgent(c.OutOrStdout(), dir, name, bound) })
		},
	}
}

// waitAgent waits up to bound for run id's current job to end, records its
// result when it has, and prints how the run stands (spec §5.3).
func (d *client) waitAgent(out io.Writer, dir, id string, bound time.Duration) error {
	run, err := readAgentRun(agentPath(dir, id))
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %v; the record is left as it is", err)}
	}
	if !run.ended() {
		v, known, err := d.jobWithin(run.current().ID, bound)
		if err != nil {
			return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: job %d goes on, but %v (loom flow agent wait %s follows it again)", run.current().ID, err, id)}
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
	if run.ended() || run.current().Read {
		return run, nil
	}
	job := run.current()
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
		Short: "Resume an agent run at NEEDS_CONTEXT or BLOCKED with an answer on stdin",
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
			guard, err := agentGuard()
			if err != nil {
				return &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
			}
			dir, cleanup, err := agentDir()
			if err != nil {
				return err
			}
			defer cleanup()
			return flowDaemon(c, func(d *client) error {
				return d.answerAgent(c.OutOrStdout(), dir, args[0], root, guard, string(answer))
			})
		},
	}
}

// answerAgent queues a job resuming run id's session with answer.
func (d *client) answerAgent(out io.Writer, dir, id, root, guard, answer string) error {
	unlock, err := lockAgents(dir)
	if err != nil {
		return err
	}
	defer unlock()
	run, err := readAgentRun(agentPath(dir, id))
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %v; the record is left as it is", err)}
	}
	if run.Status != "NEEDS_CONTEXT" && run.Status != "BLOCKED" {
		return &ExitError{Code: exitUsage, Msg: fmt.Sprintf("loom flow agent: %s is %s; only a run at NEEDS_CONTEXT or BLOCKED takes an answer", id, run.Status)}
	}
	argv, err := agentCommand(agentProgram(), run.Role, run.Effort, agentPermissionMode(), root, guard, answer, run.Session)
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: "loom flow agent: " + err.Error()}
	}
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
				unlock, err := lockAgents(dir)
				if err != nil {
					return err
				}
				defer unlock()
				run, err := readAgentRun(agentPath(dir, name))
				if err != nil {
					return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: %v; the record is left as it is", err)}
				}
				if run.ended() {
					fmt.Fprintf(c.OutOrStdout(), "nothing to cancel: %s is %s\n", name, run.Status)
					return nil
				}
				// A job that has ended already is only marked: its cancel
				// would be refused.
				var refused error
				d.events()
				if v, ok := jobOf(d.Queue(), run.current().ID); ok && !v.State.Finished() {
					refused = d.cancelJobs([]sched.JobID{v.ID})
				}
				run.current().Read = true
				run.Status = agentCancelled
				if err := saveAgentRun(dir, run); err != nil {
					return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow agent: the record: %v", err)}
				}
				fmt.Fprintf(c.OutOrStdout(), "cancelled %s: job %d\n", name, run.current().ID)
				if refused != nil {
					return &ExitError{Code: exitNotRun}
				}
				return nil
			})
		},
	}
}

func agentPath(dir, id string) string { return filepath.Join(dir, id+".json") }

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
func readAgentRun(path string) (*agentRun, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r agentRun
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != agentRunSchema {
		return nil, fmt.Errorf("%s: schema %d, and this loom reads %d", path, r.Schema, agentRunSchema)
	}
	if len(r.Jobs) == 0 {
		return nil, fmt.Errorf("%s: a run with no job", path)
	}
	return &r, nil
}

// saveAgentRun writes r whole (a temp file renamed).
func saveAgentRun(dir string, r *agentRun) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(agentPath(dir, r.ID), append(b, '\n'), 0o600)
}

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
```

  Attach `newFlowAgentCmd()` in `newFlowCmd`'s `AddCommand`.

- [ ] **Step 5: The guard's fallback (only if P1 found `agent_type` empty).**
  - **The change:** in `runFlowGuard` (`internal/cli/flow.go`), after decoding `call`, add `if call.AgentType == "" { if role := os.Getenv(envAgentRole); role != "" { call.AgentType = "flow:" + role } }`. Add the test `TestFlowGuard_AnAgentJobsRoleStandsInForAnEmptyAgentType`: with `LOOM_FLOW_AGENT=junior-dev` and an empty `agent_type`, a Bash `git stash` is checked and blocked; with `LOOM_FLOW_AGENT=verifier`, it passes. Add a sentence to `internal/cli/CLAUDE.md`'s guard rule.
  - **Skip** this step when P1 saw `flowprobe:probe-dev`.

- [ ] **Step 6: Run, gofmt, vet.** `gofmt -l internal/cli && CGO_ENABLED=0 go vet ./internal/cli && CGO_ENABLED=0 go test ./internal/cli -run 'TestFlowAgent|TestScrubbedVar|TestFlowGuard|TestLoomContext'`. Expected: PASS.

- [ ] **Step 7: Mutation-check:**
  - drop the scrub: the run test's `the-parent` assertion fails;
  - let `answer` take a DONE run: the answer test fails;
  - drop the NEEDS_CONTEXT-without-question check: MUTE passes as a report, and the failure test fails;
  - drop the not-ended-name refusal: the name test fails;
  - reading the log before the view can't be mutation-checked; it stays **Convention**, as for `jobFollower`.

- [ ] **Step 8: Commit:** `feat(cli): loom flow agent, a flow agent run headless as an agent job, its report and its answers`, with the touched files by path.

---

## P4 — Docs and the gates

- [ ] **Step 1: Docs.**
  - **`sched/CLAUDE.md`,** a rule: **Keep agent jobs (`KindAgent`) out of the caps, the advisor, grants and parallelism hints, and admit them only by `Policy.AgentJobs` per account key.** An agent waiting on builds it queued itself would starve them under a claim, and a hinted 1 would run its own `cargo check` single-threaded. **Enforced** by `TestAdmit_AgentJobs`, `TestGrant` (its agent case) and `TestAgentHints_OnlyTheJobAndSccache`.
  - **`internal/cli/CLAUDE.md`,** a rule: **Give an agent job its submitter's environment without the parent Claude session's variables (`scrubbedVar`), plus `LOOM_FLOW_AGENT`; read its result only from its `output.log`, after its view says it ended; and fail a run that left no usable report, never guess one.** A headless agent with the parent's messaging socket or mod link acts as its child, and a guessed report sends unverified work on. **Enforced** by `TestScrubbedVar`, `TestFlowAgent_RunWaitDone` and `TestFlowAgent_AJobWithoutAUsableReportFails`.
  - **`docs/claude/the-work-queue.md`,** a short paragraph: the agent kind, its admission and its no-hints rule.
  - **`USAGE.md`:** the `flow agent` rows, and `agent_jobs` in the scheduler block's table.
  - **`flow/README.md`:** the `agent` row in the tool table.
  - Check the budgets with `go run ./tools/claudemd -v`.
- [ ] **Step 2: Gates.**
  - gofmt over every tracked Go file;
  - `CGO_ENABLED=0 go vet ./...`;
  - Windows and darwin builds;
  - `CGO_ENABLED=0 go test ./tools/claudemd`;
  - the full suite as a job: `loom job submit -d "go test ./... part 3a" -- env CGO_ENABLED=0 go test ./...`;
  - race on `sched`, `core` and `internal/cli` with `--after` it;
  - `bash flow/lib/test.sh`.

  golangci-lint is skipped, as before.
- [ ] **Step 3: Execution notes and the handoff.** Append the execution notes (the probe's findings, deviations, mutations, gates), commit, and ask the owner whether to fast-forward local `main`.

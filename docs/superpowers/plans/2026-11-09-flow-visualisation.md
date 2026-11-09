# Flow visualisation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show a flow's stages, documents, run progress, story states and costs on the phone: a flow tab with the pipeline board, a story detail, a document viewer, a fleet chip and a host-wide Flows screen. Spec: `docs/superpowers/specs/2026-11-09-flow-visualisation-design.md`.

**Architecture:** A pure reader, `internal/flowview`, turns a worktree's flow files (plans, specs, drafts, `<state>/runs|agents|gates`, records) into plain values. `core` runs it on job goroutines when a run's jobs change state, on a Stop and at first sight, keeps a `FlowSummary` list per instance on `InstanceView`, and answers three new read requests (`ReadFlow`, `ReadFlows`, `ReadFlowDoc`) by `Reply`. Costs come from `agent_costs.py` alone, through two new `loom flow costs` forms, `run --json` and `design`. The web layer adds three routes and the app four screens.

**Tech Stack:** Go, testify, Python 3 (`flow/lib`, unittest), Preact + htm `.mjs` modules. Every build and test runs through the work queue (`loom job run -d "…" -- …`).

**Execution:** inline, one package at a time (owner's choice, 2026-11-09), no checkpoint pauses. Each package ends with its gate and one commit by path. Never `git add -A`/`.`/`-a`.

**Gate commands** (each through the queue, e.g. `loom job run -d "flowviz P1 gate" -- env CGO_ENABLED=0 go test ./internal/flowrun/ ./internal/flowview/ ./internal/cli/ -count=1`):
- `env CGO_ENABLED=0 go test <packages> -count=1`
- `env CGO_ENABLED=0 go vet ./...`
- `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')`, run directly (it compiles nothing), and expect no output.
- After touching `flow/`: `bash flow/lib/test.sh`.
- After touching `internal/web/static/js/`: `node --check <file>` for each changed module, run directly.

**Deviations from the spec, decided while planning (each written into the spec in the P4 docs task):**
1. The moved record types are named `AgentRecord`, `AgentRecordJob`, `GateRecord` and `GateRecordStep`. `flowrun` already has a `GateStep`, the failing step of a `GateResult`.
2. The flowview tests build their fixtures with `flowrun.Save`, `flowrun.SaveAgent` and `flowrun.SaveGate`, because the engine's `fakeFX` is a `_test.go` type of `flowrun`. The guarantee that the engine's own names parse sits in `flowrun`, where `fakeFX` is: `TestStepOf_ParsesEveryNameTheEngineSubmits` drives the existing scenarios and parses every name they record.
3. A step's start and end times come from the queue (`JobView`), filled in by core for the jobs the queue still holds; flowview returns the job ids only. An agent record holds no times.
4. flowview reads the profile's `paths.state` and `paths.drafts` with a small reader of its own (`profilePaths`). `loom flow conf state` and `loom flow conf drafts` both create the directory they print, and a reader must not.
5. The branch's files are `git log --first-parent --no-merges --name-only <base>..HEAD` plus `git status --porcelain --untracked-files=all`. `git diff <base>` would count every file a merge of main brought in.
6. `ReadFlowDoc` draws documents through `buildTreeLimit(text, flowDocLimit)`, with a 1 MiB limit; `buildTree`'s 64 KiB cap cuts most plans.

---

## File map

| file | package | change |
|---|---|---|
| `internal/flowrun/records.go` (new) | P1 | `AgentRecord`, `AgentRecordJob`, `GateRecord`, `GateRecordStep`, their methods, `AgentSchema`, `GateSchema`, `LoadAgent`, `LoadGate`, `SaveAgent`, `SaveGate`, `AgentPath`, `GatePath` |
| `internal/flowrun/steps.go` (new) | P1 | `StepOf`, `Step`, the kinds, `QuestionStep` |
| `internal/cli/flow_agent.go`, `flow_agent_exec.go`, `flow_gates.go`, `flow_run_fx.go` | P1 | aliases to the moved types; method calls renamed |
| `internal/flowview/` (new) | P1, P2 | `CLAUDE.md`, `README.md`, `testmain_test.go`, `paths.go` (`profilePaths`, `stateDir`, `draftsDir`), `key.go` (`RepoKey`), `branch.go` (`branchFiles`), `find.go` (`Find`, `Summary`), `stages.go` (`Stage`, the stage table), `detail.go` (`Detail`, `FlowDetail`, `StoryView`, `StepView`, `Cell`), `history.go` (`History`, `PastRun`), `costs.go` (P2: `Priced`, `Costs`, `CostsOf`, `Projection`, `Comparison`, `designStageUSD`) |
| `flow/lib/agent_costs.py`, `flow/lib/agent_costs_test.py` | P2 | `run --json`, `--session`, `design` |
| `flow/README.md`, `flow/plugin/docs/records.md` | P2 | the new forms |
| `core/flows.go` (new) | P3 | `flowState`, `lookForFlows`, `applyFlows`, `wantFlowLook`, `maybeFlowLooks`, `flowJobsKey`, `ReadFlow`, `ReadFlows`, `ReadFlowDoc`, their jobs and results, `priceRun`, `priceDesign` |
| `core/view.go`, `core/views.go` | P3 | `InstanceView.Flows`, `FlowSummary` |
| `core/iface.go`, `core/loop_core.go`, `core/requests.go`, `core/events.go`, `core/events_json.go`, `core/model.go`, `core/hook_scan.go`, `core/tick.go` | P3 | the methods, ops, preconditions, `Reply.Flow`/`Flows`/`FlowDoc`, delivery, triggers |
| `core/rpc/methods_gen.go`, `docs/specs/protocol.md` | P3 | regenerated |
| `internal/web/flow.go` (new), `internal/web/api.go`, `internal/web/fleet.go`, `internal/web/markdown.go` | P3 | the three routes, their JSON, `sessionJSON.Flows`, `buildTreeLimit` |
| `tools/webshot/fixture/flow.json`, `flows.json`, `flow-doc.json`, `tools/webshot/canned.go`, `internal/web/webshot_fixture_test.go` | P3 | fixtures for the screens |
| `internal/web/static/js/flow.mjs`, `flowdoc.mjs`, `flows.mjs` (new), `app.mjs`, `screens.mjs`, `chrome.mjs`, `session.mjs`, `fleet.mjs`, `internal/web/static/app.css`, `internal/web/static/sw.js` | P4 | the screens, the routes, the tab, the chip, the styles, the shell list |
| `USAGE.md`, `internal/web/README.md`, `internal/flowrun/README.md`, `core/README.md`, the spec | P4 | docs |

---

## Package 1: the reader

**Files:**
- Create: `internal/flowrun/records.go`, `internal/flowrun/records_test.go`, `internal/flowrun/steps.go`, `internal/flowrun/steps_test.go`, `internal/flowview/{CLAUDE.md,README.md,testmain_test.go,paths.go,paths_test.go,key.go,key_test.go,branch.go,find.go,find_test.go,stages.go,detail.go,detail_test.go,history.go,history_test.go}`
- Modify: `internal/cli/flow_agent.go:25-61,535,567-594`, `internal/cli/flow_gates.go:53-110` and every call of the renamed methods, `internal/flowrun/README.md`

### Task 1.1: the record types move into `flowrun`

- [ ] **Step 1: Write `internal/flowrun/records_test.go`**

```go
package flowrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentRecord_SaveLoadAndRefusals(t *testing.T) {
	dir := t.TempDir()
	rec := &AgentRecord{Schema: AgentSchema, ID: "r-s1-dev1", Role: "intermediate-dev", Status: AgentRunning, Jobs: []AgentRecordJob{{ID: 7}}}
	require.NoError(t, SaveAgent(dir, rec))
	got, err := LoadAgent(dir, "r-s1-dev1")
	require.NoError(t, err)
	assert.Equal(t, rec, got)
	assert.False(t, got.Ended())
	assert.Equal(t, int64(7), int64(got.Current().ID))

	require.NoError(t, os.WriteFile(AgentPath(dir, "other"), []byte(`{"schema":99,"jobs":[{"id":1}]}`), 0o600))
	_, err = LoadAgent(dir, "other")
	assert.ErrorContains(t, err, "schema 99")
	require.NoError(t, os.WriteFile(AgentPath(dir, "nojob"), []byte(`{"schema":1,"jobs":[]}`), 0o600))
	_, err = LoadAgent(dir, "nojob")
	assert.ErrorContains(t, err, "no job")
}

func TestGateRecord_SaveLoadAndVerdicts(t *testing.T) {
	dir := t.TempDir()
	rec := &GateRecord{Schema: GateSchema, Name: "r-s1-gates1", Kind: "story", Steps: []GateRecordStep{
		{Line: "RUN go test ./...", Run: true, Jobs: []sched.JobID{3, 9}, Verdict: "PASS"},
		{Line: "RUN go vet ./...", Run: true, Jobs: []sched.JobID{4}, Verdict: "FAIL"},
	}}
	require.NoError(t, SaveGate(dir, rec))
	got, err := LoadGate(dir, "r-s1-gates1")
	require.NoError(t, err)
	assert.True(t, got.Ended())
	assert.True(t, got.Failed())
	assert.Equal(t, sched.JobID(9), got.Steps[0].Job())
	assert.Equal(t, "go test ./...", got.Steps[0].Text())
	assert.Equal(t, 2, got.GateOf(4))
	_, err = LoadGate(dir, "missing")
	assert.ErrorIs(t, err, os.ErrNotExist)
	_ = filepath.Join
}
```

(Import `github.com/aidan-bailey/loom/sched` as well; drop the `filepath` line if it isn't needed.)

- [ ] **Step 2: Run it and see it fail to compile**

Run: `loom job run -d "flowviz records red" -- env CGO_ENABLED=0 go test ./internal/flowrun/ -run 'TestAgentRecord|TestGateRecord' -count=1`
Expected: FAIL, `undefined: AgentRecord`.

- [ ] **Step 3: Write `internal/flowrun/records.go`.** Move the types from `internal/cli/flow_agent.go:25-61` and `internal/cli/flow_gates.go:40-110` (with their comments), exported:

```go
package flowrun

// The records of flow in loom part 3a's agent runs and part 2's gate runs:
// <state>/agents/<name>.json and <state>/gates/<name>.json. internal/cli
// writes them; internal/flowview reads them. One type, two users.

// AgentSchema and GateSchema are the records' versions.
const (
	AgentSchema = 1
	GateSchema  = 1
)

// An agent run's statuses beside a report's own (part 3a spec §5.5).
const (
	AgentRunning   = "running"
	AgentFailed    = "failed"
	AgentCancelled = "cancelled"
)

// AgentRecord is one `loom flow agent run` and every job after it on its
// Claude session.
type AgentRecord struct { /* the fields of agentRun, unchanged, with their json tags */ }

// AgentRecordJob is one job of a run: its start, or an answer resuming it.
type AgentRecordJob struct { /* the fields of agentJob */ }

func (r *AgentRecord) Current() *AgentRecordJob { return &r.Jobs[len(r.Jobs)-1] }
func (r *AgentRecord) Ended() bool             { return r.Status != AgentRunning }

// GateRecord, GateRecordStep: gateRun and gateStep, with Text, Job, Ended,
// Failed and GateOf as their exported methods, and failing (the verdicts
// that fail a run) moved with them as gateFailing.

func AgentPath(dir, name string) string { return filepath.Join(dir, name+".json") }
func GatePath(dir, name string) string  { return filepath.Join(dir, name+".json") }

// LoadAgent reads a record, refusing one it can't decode, of another
// schema, or with no job. LoadGate is the same for a gate record (no job
// check).
func LoadAgent(dir, name string) (*AgentRecord, error) { /* readAgentRun's body, on AgentPath */ }
func LoadGate(dir, name string) (*GateRecord, error)   { /* the gates' reader's body */ }

// SaveAgent and SaveGate write a record whole (config.AtomicWriteFile).
func SaveAgent(dir string, r *AgentRecord) error { /* saveAgentRun's body */ }
func SaveGate(dir string, r *GateRecord) error   { /* the gates' writer's body */ }
```

Copy the bodies from `readAgentRun`/`saveAgentRun` (`internal/cli/flow_agent.go:567-594`) and the gate record's reader and writer in `internal/cli/flow_gates.go` (find them with `grep -n 'func readGateRun\|func saveGateRun\|gatePath' internal/cli/flow_gates.go`), word for word, on the exported names. The verdict constants `verdictFail`, `verdictKilled`, `verdictInfra` and `verdictVacuous` that `failing` uses stay in `internal/cli`. Write their string values into `gateFailing` (`"FAIL"`, `"FAIL (killed)"`, `"FAIL (infra)"`, `"VACUOUS"`, copied from their definitions), and add `TestGateFailing_IsTheCLIsVerdicts` in `internal/cli` asserting `flowrun.GateFailing()` equals `failing`. `GateFailing` is an exported func returning a copy.

- [ ] **Step 4: Point `internal/cli` at them.** In `internal/cli/flow_agent.go` replace the type declarations with aliases, keeping the CLI's names so its many uses stay:

```go
type agentRun = flowrun.AgentRecord
type agentJob = flowrun.AgentRecordJob

const (
	agentRunning   = flowrun.AgentRunning
	agentFailed    = flowrun.AgentFailed
	agentCancelled = flowrun.AgentCancelled
	agentRunSchema = flowrun.AgentSchema
)
```

and `type gateRun = flowrun.GateRecord`, `type gateStep = flowrun.GateRecordStep`, `gateRunSchema = flowrun.GateSchema` in `flow_gates.go`. Make `readAgentRun(path)` call `flowrun.LoadAgent(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), ".json"))`, `saveAgentRun` call `flowrun.SaveAgent`, `agentPath` call `flowrun.AgentPath`, and the same for the gate reader, writer and path. Then rename the method calls throughout `internal/cli`:

```bash
grep -rln '\.current()\|\.ended()\|\.text()\|\.job()\|\.failed()\|\.gateOf(' internal/cli/*.go
```

and change `.current()`→`.Current()`, `.ended()`→`.Ended()`, `.text()`→`.Text()`, `.job()`→`.Job()`, `.failed()`→`.Failed()`, `.gateOf(`→`.GateOf(`, **only where the receiver is an `agentRun`, `gateRun` or `gateStep`**. Read each hit: other types in the package may have methods of the same names. `failing` stays where `printAgentEnd` and the tallies use it, defined as `var failing = flowrun.GateFailing()`.

- [ ] **Step 5: Run the flowrun and cli tests**

Run: `loom job run -d "flowviz records green" -- env CGO_ENABLED=0 go test ./internal/flowrun/ ./internal/cli/ -count=1`
Expected: PASS: the new tests, and every existing `TestFlowAgent_…`, `TestFlowGates_…` and `TestFlowRun_…` unchanged.

### Task 1.2: `StepOf` and `QuestionStep`

- [ ] **Step 1: Write `internal/flowrun/steps_test.go`**

```go
package flowrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStepOf(t *testing.T) {
	stories := []Story{{ID: "1"}, {ID: "2"}, {ID: "2-b"}, {ID: "10"}}
	for name, want := range map[string]Step{
		"r-premise1":        {Kind: KindPremise, K: 1},
		"r-s1-dev1":         {Story: "1", Kind: KindDev, K: 1},
		"r-s1-dev12":        {Story: "1", Kind: KindDev, K: 12},
		"r-s2-gates3":       {Story: "2", Kind: KindGates, K: 3},
		"r-s2-b-review1":    {Story: "2-b", Kind: KindReview, K: 1},
		"r-s10-fix2":        {Story: "10", Kind: KindFix, K: 2},
		"r-final-gates1":    {Final: true, Kind: KindGates, K: 1},
		"r-final-review2":   {Final: true, Kind: KindReview, K: 2},
		"r-final-fix4":      {Final: true, Kind: KindFix, K: 4},
	} {
		got, ok := StepOf("r", name, stories)
		assert.True(t, ok, name)
		assert.Equal(t, want, got, name)
	}
	for _, name := range []string{"other-s1-dev1", "r-s3-dev1", "r-s1-build1", "r-s1-dev", "r-s1-dev0", "r-final-dev1", "r"} {
		_, ok := StepOf("r", name, stories)
		assert.False(t, ok, name)
	}
}

// TestStepOf_ParsesEveryNameTheEngineSubmits drives scenarios through the
// engine and parses every agent and gate run name it recorded: the
// naming (next, storyPrefix) and the parsing live together here.
func TestStepOf_ParsesEveryNameTheEngineSubmits(t *testing.T) {
	for _, build := range []func() (*State, *fakeFX, map[string]string){
		/* copy the setups of TestRun_AStoryRunsDeveloperGatesReview,
		   TestRun_GateFailures' first case, TestRun_FindingsRoutes' first
		   case, TestRun_Premises and TestRun_KeptMinorsReachTheFinalRound:
		   each returns its state, its fake and its answers */
	} {
		st, f, answers := build()
		drive(t, st, f, answers)
		for _, name := range append(append([]string{}, st.Agents...), st.Gates...) {
			s, ok := StepOf(st.ID, name, st.Stories)
			assert.True(t, ok, name)
			if !s.Final && s.Kind != KindPremise {
				assert.NotEmpty(t, s.Story, name)
			}
		}
	}
}

func TestQuestionStep(t *testing.T) {
	stories := []Story{{ID: "1"}, {ID: "3"}}
	for _, c := range []struct {
		q    Question
		want Step
		ok   bool
	}{
		{Question{Key: "blocked:r-s3-dev1", Story: "3"}, Step{Story: "3", Kind: KindDev, K: 1}, true},
		{Question{Key: "gates:r-s1-gates2", Story: "1"}, Step{Story: "1", Kind: KindGates, K: 2}, true},
		{Question{Key: "ctx:r-s3-review1:1", Story: "3"}, Step{Story: "3", Kind: KindReview, K: 1}, true},
		{Question{Key: "design:3", Story: "3"}, Step{Story: "3", Kind: KindReview}, true},
		{Question{Key: "unresolved:s3:1", Story: "3"}, Step{Story: "3", Kind: KindReview}, true},
		{Question{Key: "plan:3", Story: "3"}, Step{Story: "3", Kind: KindReview}, true},
		{Question{Key: "design:final"}, Step{Final: true, Kind: KindReview}, true},
		{Question{Key: "unresolved:final:2"}, Step{Final: true, Kind: KindReview}, true},
		{Question{Key: "premise-design"}, Step{Kind: KindPremise}, true},
		{Question{Key: "something-new"}, Step{}, false},
	} {
		got, ok := QuestionStep("r", c.q, stories)
		assert.Equal(t, c.ok, ok, c.q.Key)
		assert.Equal(t, c.want, got, c.q.Key)
	}
}
```

Before writing the scenario setups, read each named test's opening lines (`grep -n 'func TestRun_AStoryRunsDeveloperGatesReview' -A14 internal/flowrun/engine_test.go` and so on) and copy its state, fake and answers.

- [ ] **Step 2: Run them red**

Run: `loom job run -d "flowviz stepof red" -- env CGO_ENABLED=0 go test ./internal/flowrun/ -run 'TestStepOf|TestQuestionStep' -count=1`
Expected: FAIL, `undefined: StepOf`.

- [ ] **Step 3: Write `internal/flowrun/steps.go`**

```go
package flowrun

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The step kinds a run's names carry (engine.next's prefixes).
const (
	KindPremise = "premise"
	KindDev     = "dev"
	KindGates   = "gates"
	KindReview  = "review"
	KindFix     = "fix"
)

// Step is what an attempt's name says: the story it belongs to ("" for the
// premises and the final round), whether it is the final round's, its kind
// and its attempt number (0 when a question names a kind, not an attempt).
type Step struct {
	Story string
	Final bool
	Kind  string
	K     int
}

var attempt = regexp.MustCompile(`^(dev|gates|review|fix)([1-9][0-9]*)$`)

// StepOf parses run's attempt name <run>-<prefix><k> (engine.next), with
// the run's stories so an id that storyPrefix keeps with a '-' matches
// whole: the longest story prefix wins. ok is false for another run's
// name, a story the run doesn't have, or a kind next doesn't make.
func StepOf(run, name string, stories []Story) (Step, bool) {
	rest, ok := strings.CutPrefix(name, run+"-")
	if !ok {
		return Step{}, false
	}
	if k, ok := strings.CutPrefix(rest, "premise"); ok {
		n, err := strconv.Atoi(k)
		return Step{Kind: KindPremise, K: n}, err == nil && n > 0 && k[0] != '0'
	}
	if tail, ok := strings.CutPrefix(rest, finalStory+"-"); ok {
		m := attempt.FindStringSubmatch(tail)
		if m == nil || m[1] == KindDev {
			return Step{}, false
		}
		n, _ := strconv.Atoi(m[2])
		return Step{Final: true, Kind: m[1], K: n}, true
	}
	ids := make([]string, len(stories))
	for i, s := range stories {
		ids[i] = s.ID
	}
	sort.Slice(ids, func(i, j int) bool { return len(storyPrefix(ids[i])) > len(storyPrefix(ids[j])) })
	for _, id := range ids {
		tail, ok := strings.CutPrefix(rest, storyPrefix(id)+"-")
		if !ok {
			continue
		}
		if m := attempt.FindStringSubmatch(tail); m != nil {
			n, _ := strconv.Atoi(m[2])
			return Step{Story: id, Kind: m[1], K: n}, true
		}
	}
	return Step{}, false
}

// QuestionStep is the step a parked run's question is about, for the
// board's amber cell: a key that names an attempt (tree:, ctx:, failed:,
// blocked:, gates:) is that attempt; a review's question (design:,
// unresolved:, plan:) is its story's, or the final round's, review; the
// premises' question is the premise row's. ok is false for a key this
// build doesn't know.
func QuestionStep(run string, q Question, stories []Story) (Step, bool) {
	head, rest, _ := strings.Cut(q.Key, ":")
	switch head {
	case "tree", "ctx", "failed", "blocked", "gates":
		name, _, _ := strings.Cut(rest, ":")
		return StepOf(run, name, stories)
	case "design", "unresolved", "plan":
		if rest == finalStory || strings.HasPrefix(rest, finalStory+":") {
			return Step{Final: true, Kind: KindReview}, true
		}
		return Step{Story: q.Story, Kind: KindReview}, q.Story != ""
	case "premise-design":
		return Step{Kind: KindPremise}, true
	}
	return Step{}, false
}
```

Check while writing: the premise attempt's `k[0] != '0'` must not index an empty `k`. Guard it with `k != ""`.

- [ ] **Step 4: Run them green**

Run: `loom job run -d "flowviz stepof green" -- env CGO_ENABLED=0 go test ./internal/flowrun/ -count=1`
Expected: PASS.

### Task 1.3: `internal/flowview`'s frame, the profile's paths, the repo key

- [ ] **Step 1: Write the package's frame.**
  - `internal/flowview/testmain_test.go`: copy `internal/flowrun/testmain_test.go` (logging init, `testenv.IsolateLoomDirs`, `os.Exit`), with the package name changed.
  - `internal/flowview/README.md`: what the package is (the spec's §2), its files and its tests.
  - `internal/flowview/CLAUDE.md`: the shape `tools/claudemd` checks, with these rules, each with its **Enforced** test once the tests exist:
    - **Read, never write: no file, no directory, no `git` that changes anything.** The model calls it on every look, in worktrees that agents are working in. **Enforced** by `TestFind_LeavesTheWorktreeAsItWas`.
    - **Decode a run state only with `flowrun.Load`, and agent and gate records only with `flowrun.LoadAgent`/`LoadGate`.** A second decoder drifts from the writer's schema, and a half-read state draws a wrong board. **Convention**.
    - **Say `unreadable: <why>` for a file you can't read or decode, and derive nothing after it from that file.** A guessed stage or cell is the one thing the board must not show. **Enforced** by `TestFind_AnUnreadableRunStateSaysWhy` and `TestDetail_AnUnreadableAgentRecordMarksItsStep`.
    - **Place steps only with `flowrun.StepOf` and `flowrun.QuestionStep`.** The names are the engine's; a parser here goes grey on the engine's next new prefix. **Convention**.

- [ ] **Step 2: Write `paths_test.go` and `key_test.go`**

```go
func TestProfilePaths(t *testing.T) {
	for _, c := range []struct{ toml, state, drafts, err string }{
		{"", "", "", ""},
		{"schema = 1\n[gates]\nstory = []\n", "", "", ""},
		{"[paths]\nstate = \"/var/flow\"\ndrafts = 'notes/drafts'\n", "/var/flow", "notes/drafts", ""},
		{"paths.state = \"st\"   # trailing comment\n", "st", "", ""},
		{"[paths]\nstate = \"\"\"multi\"\"\"\n", "", "", "paths.state"},
		{"[paths]\nrecords = \"r\"\n[review]\nidioms = \"state = x\"\n", "", "", ""},
	} {
		dir := t.TempDir()
		if c.toml != "" {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude", "flow.toml"), []byte(c.toml), 0o600))
		}
		state, drafts, err := profilePaths(dir)
		if c.err != "" {
			assert.ErrorContains(t, err, c.err, c.toml)
			continue
		}
		require.NoError(t, err, c.toml)
		assert.Equal(t, c.state, state, c.toml)
		assert.Equal(t, c.drafts, drafts, c.toml)
	}
}

func TestRepoKey_IsFlowconfsKey(t *testing.T) {
	repo := gitRepo(t) // a temp repo with one commit, its path resolved
	key, err := RepoKey(repo)
	require.NoError(t, err)
	assert.Equal(t, regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(repo, "-"), key)
	linked := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", linked)
	k2, err := RepoKey(linked)
	require.NoError(t, err)
	assert.Equal(t, key, k2, "every worktree of a repo shares its key, as flowconf_test pins")
}
```

`gitRepo` and `git` are test helpers in `find_test.go` (Task 1.4). Both run git through `internalexec.GitCommand(context.Background(), dir, args...)`, with `user.name` and `user.email` set by `-c`, and `gitRepo` returns `filepath.EvalSymlinks` of the temp dir.

- [ ] **Step 3: Run them red** (`loom job run -d "flowviz paths red" -- env CGO_ENABLED=0 go test ./internal/flowview/ -count=1`). Expected: FAIL, undefined.

- [ ] **Step 4: Write `paths.go` and `key.go`**

```go
// profilePaths reads paths.state and paths.drafts from top's
// .claude/flow.toml without a TOML library: a [paths] table's or a dotted
// paths.<key>'s basic ("…") or literal ('…') one-line string. Any other
// form of either key is refused by name, so a reader never guesses a dir.
// No profile is no paths.
func profilePaths(top string) (state, drafts string, err error)

// stateDir is where the worktree's runs live: paths.state (made absolute
// against top, ~ expanded), else <git-dir>/flow.
func stateDir(top, state string) (string, error)

// draftsDir is the plan stem's drafts dir: <top>/<paths.drafts, else
// .flow/drafts>/<stem>.
func draftsDir(top, drafts, stem string) string
```

The line reader: strip a `#` comment outside quotes, track the current `[table]`, match `^\s*(paths\.)?(state|drafts)\s*=\s*(.*)$` in the root table (with the `paths.` prefix) or in `[paths]` (without it), and accept only `"…"` with no `\` escape in it, or `'…'`. Anything else for those keys is `fmt.Errorf("%s: paths.%s: only a one-line string is read here", path, key)`. `stateDir` runs `git rev-parse --path-format=absolute --git-dir` through `internalexec.GitCommand`.

```go
// RepoKey is the repo key flowconf.repo_key prints: the main checkout's
// resolved path (the parent of git's common dir, or the common dir itself
// for a bare repository) with every character outside [A-Za-z0-9] made -.
func RepoKey(worktree string) (string, error)
```

- [ ] **Step 5: Run them green.** Expected: PASS.

### Task 1.4: `Find` and the stages

- [ ] **Step 1: Write `find_test.go`.** The helpers: `gitRepo(t)`; `git(t, dir, args...)`; `commitFile(t, dir, path, text)`, which writes, adds and commits one file; and `writeRun(t, stateDir, st *flowrun.State)`, which calls `flowrun.Save(filepath.Join(stateDir, "runs", st.ID+".json"), st)`. The tests:

```go
func TestFind_TheBranchsOwnPlansOnly(t *testing.T)
// main has docs/superpowers/plans/old.md with the marker; the session's base
// is main's HEAD; the branch commits plans/new.md with the marker and
// specs/2026-11-09-x-design.md; main then gets plans/other.md, which is
// merged into the branch. Find returns new.md only (the merge's plan is
// not the branch's), its Stage "draft" or "design" per the table, never
// old.md or other.md.

func TestFind_ASpecWithNoPlanIsAFlowInDesign(t *testing.T)
// an uncommitted docs/superpowers/specs/2026-11-09-y-design.md: one Summary,
// Plan = the spec's path, Stage "design", Status "designing".

func TestFind_ARunStateNamesItsPlanEvenOutsideTheDiff(t *testing.T)
// a run state whose Plan is a plan committed before the base: listed, Stage "run".

func TestFind_TheStages(t *testing.T)
// one subtest per row of the spec's stage table, each building just the
// files that row needs: design done/in progress, spec, draft in progress
// (D/S1.md of a skeleton with stories 1 and 2: "1 of 2"), draft done
// (**Design stage:** line), run running/parked/done, run supervised
// (agent-reports/supervisor-<stem>.md with "**Phase:** story 2"), record
// (FLOW_DATA records/<key>/2026-11-10-<stem without date>.md).

func TestFind_AnUnreadableRunStateSaysWhy(t *testing.T)
// runs/r.json holds {"schema": 2}: the Summary's Unreadable says "schema 2".

func TestFind_TwoRunsOfOnePlanShowTheNewest(t *testing.T)

func TestFind_LeavesTheWorktreeAsItWas(t *testing.T)
// git status --porcelain --ignored and a walk of the tree are the same
// before and after Find, with a drafts dir and a state dir present.
```

Write each test's body in full: the fixture files with their exact contents, and the asserted `Summary` values. A skeleton's story list is its `## Stories` heading followed by entries starting `- **Story <id>`. Read `flow/lib/plan_join.py` (`grep -n 'Stories\|^def ' flow/lib/plan_join.py`) for the exact entry pattern before writing the fixture, and parse with the same pattern.

- [ ] **Step 2: Run them red.**

- [ ] **Step 3: Write `branch.go`, `stages.go` and `find.go`**

```go
// branchFiles is every path the branch's own commits since base touched
// (git log --first-parent --no-merges --name-only --format= base..HEAD),
// and every path git status reports (untracked files included), relative
// to top, without duplicates. A base git doesn't know is an error.
func branchFiles(top, base string) ([]string, error)

// Summary is one flow of a worktree, as the fleet chip and the Flows
// screen show it (spec §2).
type Summary struct {
	Plan       string  // the plan's path from top; a spec's for a flow in design
	Spec       string  // the plan's **Spec:** path, "" when unknown
	Stage      string  // design, spec, draft, run, record: the furthest begun
	Status     string  // designing, drafting, running, parked, done, stopped, cancelled, supervised
	Run        string  // the shown run's id, "" for none
	Done       int     // stories done
	Total      int     // stories in the plan
	Drafted    int     // stories drafted, while drafting
	Asks       string  // the open question's kind, "" for none
	Phase      string  // the run's Phase, or a supervised run's / draft's **Phase** line
	Updated    time.Time
	Unreadable string  // why a file of the flow can't be read, "" when all can
}

// Find returns top's flows, newest first (by Updated): the plans in
// branchFiles(top, base) holding the plan_join marker or a **Spec:** line,
// the plans the state dir's run states name, and the specs in design
// (spec §1). FLOW_DATA (else <home>/.loom/flow) holds the records.
func Find(top, base, recordsDir string) ([]Summary, error)
```

`stages.go` holds the stage names, the `planHeader` parser (the `**Spec:**` path, the `**Design stage:**` line, the skeleton's story ids, everything above the marker), and `stagesOf(top, plan, …) []Stage`. Each `Stage` is `{Name, State ("done", "now", "todo", "unreadable"), Detail string, Doc string}`, where `Doc` is the doc key the stage opens (`spec`, `skeleton`, `plan`, `record`, `drafts-state`). `Find` keeps the summary and `Detail` (Task 1.5) reuses `stagesOf`. The marker is the exact line `<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->`; take it from `flow/lib/plan_join.py`'s constant, not retyped.

**Signature change from the spec:** `Find` takes `recordsDir` so the tests pass a temp dir; core passes `<global dir>/flow/records`.

- [ ] **Step 4: Run them green.**

### Task 1.5: `Detail`

- [ ] **Step 1: Write `detail_test.go`**

```go
func TestDetail_TheBoard(t *testing.T)
// A run state written by hand with flowrun's types: stories 1, 2, 3; Agents
// r-premise1, r-s1-dev1, r-s1-dev2, r-s1-review1, r-s2-dev1, r-s2-review1,
// r-s3-dev1; Gates r-s1-gates1 (memo gates:r-s1-gates1 = failed),
// r-s1-gates2 (passed), r-s2-gates1 (passed); memo agent:<name> reports
// DONE for the finished ones; Question {Key: "blocked:r-s3-dev1", Story: "3",
// Kind: blocked-decision}; agent records for each, r-s3-dev1's status
// BLOCKED. Assert each story's cells:
//   1: dev green, gates green (latest attempt passed), review green, fix "—"
//   2: dev green, gates green, review green, fix "—"
//   3: dev amber, gates grey, review grey, fix grey
//   premise row: one green cell; final row: absent (no final step yet).
// And the steps of story 1 in dispatch order with their kinds, attempts,
// job ids (from the agent records' jobs, the gate records' latest jobs)
// and Report flags (agents/<name>/report.md exists).

func TestDetail_RunningAndFailedCells(t *testing.T)
// latest dev attempt's agent record running, no memo: blue; a gate run
// whose memo says failed and no later attempt: red; a fix that ran:
// green; the final row once final-gates1 exists.

func TestDetail_AnUnreadableAgentRecordMarksItsStep(t *testing.T)
// agents/r-s1-dev1.json holds "{": the step's Unreadable names the file,
// its cell is grey with an unreadable flag, and the rest of the board is
// drawn.

func TestDetail_OtherRunsOfThePlan(t *testing.T)
```

- [ ] **Step 2: Run red.**

- [ ] **Step 3: Write `detail.go`**

```go
// Cell is one step kind's state on the board.
const (
	CellTodo    = "todo"    // grey: no attempt yet
	CellRunning = "running" // blue
	CellPassed  = "passed"  // green
	CellFailed  = "failed"  // red: latest attempt failed, no later pass
	CellAsks    = "asks"    // amber ?: the open question's step
	CellSkipped = "skipped" // —: the story went past without one
)

type StepView struct {
	Name, Kind  string
	K           int
	Role        string // an agent's role; "" for a gate run
	Status      string // the report's status (DONE, BLOCKED, …), running, failed, cancelled; a gate run's passed/failed/infra/running
	Jobs        []sched.JobID
	Report      bool   // agents/<name>/report.md exists
	Gates       []GateLine // a gate run's lines: Text, Verdict, Job
	Unreadable  string
}

type StoryView struct {
	ID, Developer, Tier, Review string
	Row                         flowrun.Row
	Line, End                   int // its plan range, for the plan link
	Cells                       map[string]string // kind → Cell
	Steps                       []StepView
}

// FlowDetail is one flow's board (spec §2).
type FlowDetail struct {
	Summary
	Stages   []Stage
	Base     string
	Start    string
	End      string
	Engine   int64
	Managers []string
	Premise  *StoryView // nil when the run has no premise step
	Stories  []StoryView
	Final    *StoryView // nil until a final step exists
	Question *flowrun.Question
	Kept     []flowrun.Finding
	Others   []OtherRun // the plan's other runs: ID, Status, Start
}

// Detail reads the flow whose plan (or spec in design) is plan, under top.
func Detail(top, base, recordsDir, plan string) (FlowDetail, error)
```

The cell rule, per story and kind, over the story's steps of that kind in attempt order:
- no step of the kind: `CellSkipped` when the story's `Row.Status` is `done`, or when it has moved to a later kind (order: dev, gates, review, fix), else `CellTodo`;
- the latest step's status is `running` (the agent record, or a gate record that hasn't ended and has no memo): `CellRunning`;
- the memo or record says passed/DONE/DONE_WITH_CONCERNS: `CellPassed`;
- otherwise (failed, infra, BLOCKED, NEEDS_CONTEXT, TURN_CAP, cancelled): `CellFailed`.

The question's step (`flowrun.QuestionStep`) is then set to `CellAsks`, whatever it was. Read the memo with `json.Unmarshal(st.Memo["agent:"+name], &flowrun.Report{})` and `st.Memo["gates:"+name]` into `flowrun.GateResult`.

- [ ] **Step 4: Run green.**

### Task 1.6: `History`

- [ ] **Step 1: Write `history_test.go`.** Fixtures copy the shape of `~/.loom/flow/records/-tb-Source-Personal-loom/2026-11-06-pwa-conversation-first.md`: the header list, `## Cost`, the `### Cost by agent` table with two `-final-` rows, the `<!-- agent-costs {...} -->` comment with `"total": {"usd": 10.08}`, and `## Stories`. Write them inline as Go string constants, with the client-repo-free names `r-s1-dev1`, `r-final-review1` and so on. Tests:

```go
func TestHistory_ReadsEachRecord(t *testing.T)
// key dir "-k" with two records: PastRun{Key: "-k", File, Stem, Stories: 4,
// Ended: "stopped", End, USD: 10.08, FinalUSD: 1.31}; a record without
// "- Ended:" reads Ended "supervised"; two agent-costs comments are summed.

func TestHistory_AnUndecodableRecordIsListedUnreadable(t *testing.T)
// a record with no Plan line, or a comment that isn't JSON: listed with
// Unreadable set, never skipped.

func TestHistory_LeavesRetrosOut(t *testing.T)
```

- [ ] **Step 2: Run red.** Then **Step 3:** write `history.go`:

```go
// PastRun is a record of a run (spec §2).
type PastRun struct {
	Key, File, Stem string
	Stories         int
	Ended           string // done, stopped, cancelled, supervised
	End             time.Time
	USD             float64 // the agent-costs comments' total.usd, summed
	FinalUSD        float64 // the Cost by agent table's -final- rows' USD, summed
	HasFinal        bool    // the table has -final- rows
	Unreadable      string
}

// History reads recordsDir/<key>/*.md, the retros dir left out, newest end first.
func History(recordsDir string) ([]PastRun, error)
```

Parse `- Plan: … (N stories)`, `- Start: …, End: …`, `- Ended: …`, every `<!-- agent-costs {` comment (only that exact prefix; `agent-costs-managers` is another comment), and the `### Cost by agent` table, finding its `USD` column by header name and stripping `$` and `,`.

- [ ] **Step 4: Run green.**

### Task 1.7: Package 1's gate and commit

- [ ] **Step 1:** `loom job run -d "flowviz P1 gate" -- env CGO_ENABLED=0 go test ./internal/flowrun/ ./internal/flowview/ ./internal/cli/ ./tools/claudemd/ -count=1`, then `loom job run -d "flowviz P1 vet" -- env CGO_ENABLED=0 go vet ./...`, then the gofmt check. Expected: PASS, no output.
- [ ] **Step 2:** Add `records.go`, `StepOf` and `QuestionStep` to `internal/flowrun/README.md`'s model section.
- [ ] **Step 3:** Commit by path:

```bash
git add internal/flowrun/records.go internal/flowrun/records_test.go internal/flowrun/steps.go internal/flowrun/steps_test.go internal/flowrun/README.md internal/flowview internal/cli/flow_agent.go internal/cli/flow_agent_exec.go internal/cli/flow_gates.go internal/cli/flow_run_fx.go internal/cli/flow_gates_test.go
git commit -m "feat(flowrun,flowview): the flow reader, shared run records and step names" -- <the same paths>
```

---

## Package 2: costs

**Files:**
- Modify: `flow/lib/agent_costs.py:399-437,483-531,606-630`, `flow/lib/agent_costs_test.py`, `flow/README.md` (Tools table), `flow/plugin/docs/records.md`
- Create: `internal/flowview/costs.go`, `internal/flowview/costs_test.go`

### Task 2.1: `costs run --json` and `--session`

- [ ] **Step 1: Write the failing tests in `flow/lib/agent_costs_test.py`.** Read the existing `run` test first (`grep -n 'def test.*run' flow/lib/agent_costs_test.py`) and build on its fixtures (a state dir with `runs/<id>.json`, `agents/<name>.json`, transcripts under a temp `CLAUDE_CONFIG_DIR/projects/x/`):

```python
def test_run_json_rows(self):
    # two agent runs, one ended (session s1), one running (status "running",
    # session s2); one manager session m1.
    out = json.loads(self.run_main(["run", "R", "--json"]))
    self.assertEqual([a["name"] for a in out["agents"]], ["R-s1-dev1", "R-s1-review1"])
    self.assertFalse(out["agents"][0]["running"])
    self.assertTrue(out["agents"][1]["running"])
    self.assertAlmostEqual(out["agents"][0]["usd"], <the fixture's priced figure>)
    self.assertEqual(out["managers"][0]["session"], "m1")
    self.assertAlmostEqual(out["total"]["usd"], sum(a["usd"] for a in out["agents"]) + out["managers"][0]["usd"])

def test_run_json_takes_a_session_hint(self):
    # the running record has no session; --session R-s1-review1=s2 prices it
    out = json.loads(self.run_main(["run", "R", "--json", "--session", "R-s1-review1=s2"]))
    self.assertIsNotNone(out["agents"][1]["usd"])

def test_run_json_lists_an_agent_with_no_session_unpriced(self):
    out = json.loads(self.run_main(["run", "R", "--json"]))
    self.assertIsNone(out["agents"][1]["usd"])
```

- [ ] **Step 2: Run red:** `bash flow/lib/test.sh` (or `python3 -m unittest flow/lib/agent_costs_test.py -k run_json`). Expected: FAIL.

- [ ] **Step 3: Implement.** Split `agents_report` into `agents_rows(entries, since, until, models_of)` (the loop that builds `agents`, `ratios`, `by_type`, `total`) and the formatter, so `run_report` and the JSON share one pricing pass. Add `run_entries(run, hints)`, which returns `(entries, managers, st, missing)`, where `missing` is the agent names with no session. Then:

```python
def run_json(run: str, hints: dict[str, str]) -> str:
    """run_report's figures as one JSON object (flow visualisation spec §3)."""
    entries, managers, st, missing = run_entries(run, hints)
    since, until = parse_time(st.get("start")), parse_time(st.get("end"))
    rows, total = agents_rows(entries, since, until)  # each row: kind, label, usage, models
    statuses = {name: status for name, status in agent_statuses(st)}  # from agents/<name>.json
    agents, mans = [], []
    for kind, label, usage, models in rows:
        cell = {"name": label, "role": kind, "model": ", ".join(sorted(models)),
                "usd": usage.usd, "usd_incl_est": usage.usd + usage.usd_thinking}
        if label in managers or kind == "main" or label.startswith("manager "):
            mans.append({**cell, "session": managers.get(label, "")})
        else:
            agents.append({**cell, "running": statuses.get(label) == "running"})
    for name in missing:
        agents.append({"name": name, "role": role_of(st, name), "model": "", "usd": None,
                       "usd_incl_est": None, "running": statuses.get(name) == "running"})
    agents.sort(key=lambda a: (st.get("agents") or []).index(a["name"]) if a["name"] in (st.get("agents") or []) else 1 << 30)
    return json.dumps({"run": run, "since": st.get("start"), "until": st.get("end"),
                       "agents": agents, "managers": mans,
                       "total": {"usd": total.usd, "usd_incl_est": total.usd + total.usd_thinking}}, sort_keys=True)
```

A manager session's subagents (`subagent_entries`) are summed into their manager's row, so `managers` has one row per session. The parser gets `run.add_argument("--json", action="store_true")` and `run.add_argument("--session", action="append", default=[], metavar="NAME=ID")`, and a hint without `=` exits 2 with a usage message. `until` is "now" (None) while the run has no end, so a running run is priced to now. That's `transcript_usage`'s existing behaviour with `until=None`.

- [ ] **Step 4: Run green.** `bash flow/lib/test.sh` passes, the existing `run` tests included.

### Task 2.2: `costs design`

- [ ] **Step 1: Write the failing tests.** Two transcripts in the worktree's project dir: one with a `tool_use` `Write` whose `input.file_path` is the spec's absolute path, one without. The plan is committed in a temp repo at a known time.

```python
def test_design_prices_only_the_transcripts_that_wrote_the_spec(self):
    out = json.loads(self.run_main(["design", "docs/superpowers/specs/2026-11-09-x-design.md", "--json"], cwd=self.repo))
    self.assertEqual([s["session"] for s in out["sessions"]], ["writer"])

def test_design_stops_at_the_plans_commit(self):
    out = json.loads(self.run_main(["design", SPEC, "--plan", PLAN, "--json"], cwd=self.repo))
    self.assertEqual(out["until"], self.plan_commit_time)
    # a request after that time is not counted
```

- [ ] **Step 2: Run red.**
- [ ] **Step 3: Implement `design_json(spec, plan)`.** The project dir is `projects_root() / re.sub(r"[^A-Za-z0-9]", "-", top)`. A transcript wrote the spec when one of its `assistant` records holds a `tool_use` named `Write`, `Edit` or `MultiEdit` whose `input.file_path` resolves to the spec. `until` is `git log -1 --diff-filter=A --format=%cI -- <plan>`, or None when the plan isn't committed. Each session is priced with `transcript_usage(path, None, until)`. Output `{"spec", "until", "sessions": [{"session", "usd", "usd_incl_est"}], "total": {…}}`. The parser takes `design SPEC [--plan PLAN] [--json]`, and without `--json` prints a one-line summary.
- [ ] **Step 4: Run green.** Then update `flow/README.md`'s Tools table (`costs`: `session`, `records`, `run [--json] [--session NAME=ID]`, `design SPEC [--plan PLAN] [--json]`) and the Cost paragraph in `flow/plugin/docs/records.md`.

### Task 2.3: flowview's costs

- [ ] **Step 1: Write `costs_test.go`**

```go
func TestCostsOf_SumsByStoryAndRole(t *testing.T)
// Priced{Agents: r-premise1 $0.10, r-s1-dev1 $0.50, r-s1-review1 $0.70,
// r-s2-dev1 $0.40 running, r-final-review1 $1.00; Managers: m $0.90}:
// ByStory "P" 0.10, "1" 1.20, "2" 0.40, "F" 1.00; ByRole intermediate-dev
// 0.90, code-quality-reviewer 1.70, premise-check 0.10, manager 0.90;
// Spent 3.60.

func TestProjection(t *testing.T)
// cases, each with its arithmetic in a comment:
// - 2 of 5 stories finished at $1.20 and $0.80 (mean 1.00), spent 2.40,
//   no history: (2.40 + 1.00*3) / (1 - 0.15) = 6.3529…
// - none finished, history per-story median 2.52 (records 10.08/4, 14.20/6
//   = 2.3667, 31.77/9 = 3.53 → median 2.52): (spent + 2.52*5) / 0.85
// - none finished, no history: no projection, Why "no story has finished
//   and the repo has no past runs"
// - final round started, story-phase spent 5.00 (spent 6.00, final spent
//   1.00), final share 0.15: max(6.00, 5.00/0.85 = 5.882) = 6.00
// - final share from two records with final rows: (1.31/10.08, 2.00/14.20)
//   median of two = their mean = 0.1354…

func TestComparison(t *testing.T)
// last five records of the key by End, their per-story figures, median,
// and This = spent / finished stories (absent with none finished).

func TestDesignStageUSD(t *testing.T)
// "**Design stage:** /flow:draft session …: $12.05 logged, $12.16 with
// estimated thinking; …" → 12.05, true; a line without "$… logged" → false.
```

- [ ] **Step 2: Run red.** **Step 3:** Write `costs.go`:

```go
// Priced is `loom flow costs run --json`'s output.
type Priced struct {
	Run      string        `json:"run"`
	Agents   []PricedAgent `json:"agents"`
	Managers []PricedAgent `json:"managers"`
	Total    struct {
		USD        float64 `json:"usd"`
		USDInclEst float64 `json:"usd_incl_est"`
	} `json:"total"`
}

type PricedAgent struct {
	Name       string   `json:"name"`
	Role       string   `json:"role"`
	Model      string   `json:"model"`
	USD        *float64 `json:"usd"`
	USDInclEst *float64 `json:"usd_incl_est"`
	Running    bool     `json:"running"`
	Session    string   `json:"session,omitempty"`
}

// Costs is the cost box (spec §3).
type Costs struct {
	Spent      float64
	ByStory    map[string]float64 // story id, "P" premises, "F" final
	ByRole     map[string]float64
	Unpriced   []string           // agent runs with no figure yet
	Design     *float64           // the design session's, nil when not priced
	Draft      *float64           // the **Design stage:** line's, nil when absent
	Flow       float64            // the parts that have figures
	FlowParts  int                // how many of design, draft, run have one
	Projection *float64
	Why        string             // why there's no projection
	Compare    Comparison
	Error      string             // the pricer's failure: no figures at all
}

type Comparison struct {
	Past   []float64 // per-story figures of the repo's last five runs, oldest first
	Median *float64
	This   *float64
}

// CostsOf builds the cost box from the run, its pricing, the design figure,
// the plan's draft line and the repo's history (spec §3's formulas).
func CostsOf(d FlowDetail, p Priced, design *float64, history []PastRun) Costs

// designStageUSD reads the **Design stage:** line's "$N logged".
func designStageUSD(line string) (float64, bool)
```

- [ ] **Step 4: Run green.**

### Task 2.4: Package 2's gate and commit

- [ ] `bash flow/lib/test.sh`; `loom job run -d "flowviz P2 gate" -- env CGO_ENABLED=0 go test ./internal/flowview/ ./flow/ -count=1`; vet; gofmt.
- [ ] Commit by path: `flow/lib/agent_costs.py flow/lib/agent_costs_test.py flow/README.md flow/plugin/docs/records.md internal/flowview/costs.go internal/flowview/costs_test.go`, with the message `feat(flow,flowview): price a run as JSON, price the design session, and project a run's cost`.

---

## Package 3: the daemon and the web layer

**Files:**
- Create: `core/flows.go`, `core/flows_test.go`, `internal/web/flow.go`, `internal/web/flow_test.go`, `tools/webshot/fixture/{flow.json,flows.json,flow-doc.json}`
- Modify: `core/view.go:63-66`, `core/views.go:112`, `core/iface.go:89-98`, `core/loop_core.go`, `core/requests.go:76-92,120-125,154-168,245`, `core/events.go:227-243`, `core/events_json.go` (`replyJSON`), `core/model.go:540`, `core/hook_scan.go:24-28,62-66` and its delivery, `core/tick.go` (the tick's look), `core/requests_test.go` (refusal cases), `internal/web/api.go:70-89`, `internal/web/fleet.go:94-136,258-307`, `internal/web/markdown.go:23,55`, `internal/web/webshot_fixture_test.go:19-44`, `tools/webshot/canned.go` (`apiRoutes`)
- Regenerate: `core/rpc/methods_gen.go`, `docs/specs/protocol.md`

### Task 3.1: the look, its triggers and `InstanceView.Flows`

- [ ] **Step 1: Write `core/flows_test.go`**

```go
func TestFlows_ARunJobsStateChangeTriggersALook(t *testing.T)
// a booted model with one instance; swap findFlows (the seam over
// flowview.Find) to count calls and return one Summary; submit a KindRun job
// owned by the instance, then a job whose Run is it; each state change
// (queued→running, running→done) followed by a Tick runs one look; the
// view's Flows holds the summary.

func TestFlows_AJobWithoutRunOrRunKindTriggersNothing(t *testing.T)
// an ordinary job of the same owner changes state: no look (the guard's
// other side).

func TestFlows_ATriggerDuringALookRunsOneMore(t *testing.T)
// findFlows blocks on a channel; two triggers arrive while it blocks; on
// release, exactly one more look runs.

func TestFlows_AStopTriggersALook(t *testing.T)
// a hook scan delivering a parent Stop (handsBack) runs a look.

func TestFlows_FirstSightLooksOnce(t *testing.T)
// a restored instance gets one look on the first tick, none on the second.

func TestFlows_ARunningRunWhoseEngineEndedIsLost(t *testing.T)
// Summary{Status: "running", Engine: <a job that finished>} → view Status
// "lost"; with the engine job running → "running".
```

Build the models with `bootModel` (`core/boot_test.go`), and submit jobs through `m.submitJob(sched.Submit{…, Kind: sched.KindRun, Env: …})`. `LOOM_FLOW_RUN` in a child's `Env` names the run job, as `core/queue_run_test.go` does; read it before writing the fixtures.

- [ ] **Step 2: Run red** (`loom job run -d "flowviz core red" -- env CGO_ENABLED=0 go test ./core/ -run TestFlows -count=1`).

- [ ] **Step 3: Implement `core/flows.go`.**

```go
// The flow visualisation (spec docs/superpowers/specs/2026-11-09-flow-visualisation-design.md).
// The model looks for a session's flows (flowview.Find) on a job when a
// job of one of its runs changes state, when a hook scan reads a Stop, and
// once at first sight; it keeps the summaries per instance, and viewOf
// publishes them as InstanceView.Flows.

// findFlows is flowview.Find; a test swaps it.
var findFlows = flowview.Find

// flowState is what the model keeps of one instance's flows.
type flowState struct {
	summaries []flowview.Summary
	looking   bool   // a look is in flight
	dirty     bool   // a trigger came during it
	looked    bool   // a look has been delivered
	jobsKey   string // flowJobsKey at the last look
	priced    map[string]pricedRun // per run id: its key and Priced output (Task 3.2)
	design    map[string]*float64  // per spec path, once its plan is committed
}

// flowJobsKey is the instance's run jobs and their children with their
// states, in id order: "12:running,13:done,…". A change in it is a step's
// start or end.
func (m *Model) flowJobsKey(id InstanceID) string

// wantFlowLook spawns a look for inst, or marks one wanted when one is in
// flight (spawnBackground: the model's own job).
func (m *Model) wantFlowLook(inst *session.Instance)

// maybeFlowLooks runs on the tick: a look for each active instance never
// looked at, or whose flowJobsKey moved.
func (m *Model) maybeFlowLooks()

// flowLookResult is a look's result: the instance and what Find said.
type flowLookResult struct {
	inst *session.Instance
	sums []flowview.Summary
	err  error
}

// deliverFlowLook stores the result, clears looking, and runs one more
// look when dirty.
func (m *Model) deliverFlowLook(r flowLookResult)
```

Capture the inputs on the loop when building the job (core's rule: a `Job` reads no model state): `inst.GetWorktreePath()`, the base from `inst.GetGitWorktree()` → `GetBaseCommitSHA()` (skip a look when there is no worktree or no base), and the records dir `filepath.Join(config.GetGlobalConfigDir()…, "flow", "records")`. For the global dir, use whatever `internal/cli`'s flow commands use for `FLOW_DATA`: `grep -n 'FLOW_DATA' internal/cli/*.go`. On a Find error, keep the old summaries and log `core.flows.find_failed` at debug; never clear them on a doubt. Add `case flowLookResult: m.deliverFlowLook(msg)` to `Deliver` (`core/model.go:540`). Call `m.maybeFlowLooks()` from `tickInst` after the probe dispatch. Add `flows *flowLook` to `hookScanResult`, set it like `mockup` when `handsBack`, and in its delivery call `m.wantFlowLook(inst)`. Drop `m.flows[id]` where an instance is removed: find the mockup's or the subagents' cleanup with `grep -n 'removeEverywhere' core/*.go`.

In `core/view.go`:

```go
	// Flows are the flows of the session's worktree as the model last
	// found them (core/flows.go), newest first; nil for none.
	Flows []FlowSummary
```

```go
// FlowSummary is a flow's summary (flowview.Summary) with what only the
// model knows: Lost when its run's engine job has ended or left the queue
// while its state says running, and SpentUSD, the run's last priced total.
type FlowSummary struct {
	flowview.Summary
	Lost     bool
	SpentUSD *float64
}
```

In `core/views.go`, after the mockup lines, `v.Flows = m.flowSummaries(id)` builds the slice (a copy), sets `Lost` from the queue (`q.index(sched.JobID(engine))`, finished or absent), and fills `SpentUSD` from the priced cache. `Summary` needs the engine job id for this: add `Engine int64` to `flowview.Summary` (from `State.Engine`) in this task, and in Task 1.4's code.

- [ ] **Step 4: Run green.** Then `loom job run -d "flowviz wire" -- env CGO_ENABLED=0 go test ./core/rpc/ -run TestWire_EveryFieldSurvives -count=1`. The new `InstanceView` field must round-trip. A `time.Time` inside `Summary` (`Updated`) loses its monotonic reading, so build the fixture with `Round(0)`.

### Task 3.2: `ReadFlow`, `ReadFlows`, `ReadFlowDoc` and the pricer

- [ ] **Step 1: Write the failing tests** in `core/flows_test.go`:

```go
func TestReadFlow_AnswersTheDetailWithItsCosts(t *testing.T)
// swap detailFlow (seam over flowview.Detail) and priceRunCmd (seam over the
// pricer's exec) to fixed values; ReadFlow(id, plan, req) → Reply.Flow
// holds the detail, Costs.Spent from the fake Priced, and the steps' job
// times from the queue.

func TestReadFlow_PricesOnlyWhenTheRunsAgentsMoved(t *testing.T)
// two ReadFlows with the same agents and ends: one pricer call; an agent
// ending between them: two. And none within 30 s while an agent runs
// (clock seam).

func TestReadFlow_APricerFailureIsTheCostsError(t *testing.T)
// the fake exits 1 with stderr "agent_costs: boom\nmore": Costs.Error
// "cost unavailable: agent_costs: boom", Spent 0, no Projection.

func TestReadFlow_ARunningAgentsSessionComesFromItsJobLog(t *testing.T)
// an agent record with no Session, its current job running, the job's
// output.log's first line {"type":"system","subtype":"init","session_id":"S"}:
// the pricer is called with --session <name>=S; a log without the line
// passes no --session.

func TestReadFlows_EverySessionsFlowsAndTheHistory(t *testing.T)

func TestReadFlowDoc_OnlyTheFlowsOwnDocuments(t *testing.T)
// spec, skeleton, plan, record, drafts-state, report:<a run agent> answer;
// report:<a name not in the run>, "../etc/passwd", "plan/../x" and an
// unknown key are refused (ErrRefused, the text naming the doc).
```

Add the three requests to `TestRequests_RefuseWhatTheTUIRefuses` (`core/requests_test.go`): each is refused on a workspace terminal and allowed on a paused session, as `opReadMockup` is.

- [ ] **Step 2: Run red.**

- [ ] **Step 3: Implement.** In `core/iface.go`, after `ReadJobLog`:

```go
	// A session's flows, read for the client that asks (spec
	// 2026-11-09-flow-visualisation): one flow's board with its costs, the
	// Reply's Flow (flowview.Detail, flowview.CostsOf); every session's
	// flows and the host's past runs, the Reply's Flows; one of a flow's
	// documents, the Reply's FlowDoc.
	ReadFlow(id InstanceID, plan string, req ReqID)
	ReadFlows(req ReqID)
	ReadFlowDoc(id InstanceID, plan string, doc string, req ReqID)
```

In `core/events.go`'s `Reply`, add `Flow FlowView`, `Flows FlowsView` and `FlowDoc FlowDocView`, and copy each into `replyJSON` in both directions (`core/events_json.go`), as `Mockup` is copied.

```go
// FlowView is ReadFlow's answer: the board, its costs, and the steps' job
// times the queue still knows (by job id).
type FlowView struct {
	Detail flowview.FlowDetail
	Costs  flowview.Costs
	Times  map[sched.JobID]JobTimes
}

type JobTimes struct{ Started, Ended time.Time }

// FlowsView is ReadFlows' answer.
type FlowsView struct {
	Live    []LiveFlow // each session's flows: Instance, Title, Flow FlowSummary
	History []flowview.PastRun
}

// FlowDocView is ReadFlowDoc's answer: the document's text, its path from
// the worktree's top, and whether it was cut at flowDocLimit.
type FlowDocView struct {
	Path string
	Text string
	Cut  bool
}
```

`ReadFlow` runs `admit(opReadFlow, id, req)`. On the loop it captures the worktree, the base, the records dir, the account's env (`m.AccountEnv(inst.Account)`'s view, as `state_views.go` builds it: nil for the default account, else `CLAUDE_CONFIG_DIR=…`), the cached `priced` entry and the queue's job views. It then spawns `m.track(req, id, readFlowJob(…))`. The job calls `detailFlow`, works out the pricing key from the detail's steps (each agent step's name with its status), and prices when the key differs from the cache's or 30 s have passed while an agent runs. Pricing reads each running agent's session from its job's `output.log`: `q.jobDir(job)` + `sched.LogFile`, the first JSON line with `type == "system" && subtype == "init"`, at most 64 KiB read. It then runs `os.Executable()` `flow costs run <run> --json [--session …]` in the worktree, with the account env and a 60 s context. The design figure, uncached, runs `flow costs design <spec> --plan <plan> --json`. The job reads `History`, calls `CostsOf`, and returns a `flowResult`. `deliverFlow` stores the priced cache and the design figure (once the plan is committed). `deliverTracked` copies `res.view` into `r.Flow`, and `outcome` and `Deliver` get the result type, as `mockupResult` has (`core/requests.go:87,122`, `core/model.go:540`). `ReadFlows` and `ReadFlowDoc` follow the same shape. `ReadFlowDoc` resolves `doc` through a switch over the allowed keys to a path built from the detail (never from the request) and reads at most `flowDocLimit = 1 << 20` bytes. Add `opReadFlow`, `opReadFlows` and `opReadFlowDoc` to `requestOp`, and put `opReadFlow` and `opReadFlowDoc` in `precondition`'s `opReadMockup, opReadJobLog` case. `ReadFlows` names no instance and runs no `admit`.

Forward the three in `core/loop_core.go` as `ReadMockup` is forwarded. Then:

```bash
go generate ./core/rpc
loom job run -d "flowviz protocol" -- env CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update -count=1
```

- [ ] **Step 4: Run green:** `loom job run -d "flowviz core" -- env CGO_ENABLED=0 go test ./core/ ./core/rpc/ -count=1`.

### Task 3.3: the web routes

- [ ] **Step 1: Write `internal/web/flow_test.go`**, with the `webStack` fixtures of `web_test.go` (read how `TestAPI_TurnsIsAPageOfTheConversation` builds its session and answers):

```go
func TestAPI_FlowIsTheBoardAsJSON(t *testing.T)
// GET /api/v1/sessions/{id}/flow?plan=docs/…/p.md → 200, the JSON's stages,
// stories with cells, question, costs (spent, by_story, by_role,
// projection, flow, compare), steps with started/ended RFC 3339.

func TestAPI_FlowsListsLiveAndPast(t *testing.T)

func TestAPI_FlowDocIsATree(t *testing.T)
// GET …/flow/doc?plan=…&doc=spec → {"path", "blocks": [...], "cut": false};
// a doc the model refuses → 409 with its text; no Reply → 504.

func TestSessionJSON_CarriesTheFlows(t *testing.T)
// a view with two FlowSummary values → s.flows with plan, stage, status
// ("lost" when Lost), done, total, drafted, asks, spent.
```

- [ ] **Step 2: Run red.**

- [ ] **Step 3: Implement `internal/web/flow.go`:** `flowJSON`, `flowStageJSON`, `flowStoryJSON` (`cells` a map of kind → cell), `flowStepJSON`, `flowCostsJSON`, `flowsJSON`, `pastRunJSON`, `flowDocJSON`, `flowSummaryJSON`, all snake_case, with times as RFC 3339 through `stamp`. Add `readFlow`, `readFlows` and `readFlowDoc` handlers shaped like `readTurns`. The plan comes from `r.URL.Query().Get("plan")`, refused with 400 when empty or longer than 4096 bytes. The doc's text goes through `buildTreeLimit(view.Text, flowDocLimit)`, a new function in `markdown.go`: `buildTree`'s body with the limit as a parameter, which `buildTree` then calls with `maxMessageBytes`. Register the routes in `routeAPI`:

```go
	m.Handle("GET /api/v1/sessions/{id}/flow", l.authed(l.readFlow))
	m.Handle("GET /api/v1/sessions/{id}/flow/doc", l.authed(l.readFlowDoc))
	m.Handle("GET /api/v1/flows", l.authed(l.readFlows))
```

In `fleet.go`, add `Flows []flowSummaryJSON \`json:"flows"\`` to `sessionJSON` (an empty list for none), with a comment, filled in `sessionOf`.

- [ ] **Step 4: Webshot fixtures.** Write `tools/webshot/fixture/flow.json` (the board in the spec's example: plan `docs/superpowers/plans/2026-11-09-flow-visualisation.md`; stories 1–5; story 1 done with 7 commits and $1.41, its gates failing once then passing; story 2 done, $1.10; story 3 asking blocked-decision; stories 4 and 5 not started; a premise row; spent $4.71, flow $16.92, projected ~$9.80), `flows.json` and `flow-doc.json` (a short spec as blocks). Give one fixture session in `snapshot.json` a `flows` entry. Add the three routes to `apiRoutes` in `tools/webshot/canned.go`, and the three files to `TestWebshotFixture_IsTheJSONTheLayerServes`'s map, raising its `Len` from 8 to 11.

- [ ] **Step 5: Run green:** `loom job run -d "flowviz web" -- env CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/ -count=1`.

### Task 3.4: Package 3's gate and commit

- [ ] `loom job run -d "flowviz P3 gate" -- env CGO_ENABLED=0 go test ./... -count=1`, then `loom job run -d "flowviz P3 race" -- env CC=clang CGO_ENABLED=1 go test -race ./core/ ./internal/web/ -count=1`, vet and gofmt.
- [ ] Commit by path: every file in Package 3's list, with the message `feat(core,rpc,web): flows on the instance view, and the flow, flows and flow-doc reads`.

---

## Package 4: the phone

**Files:**
- Create: `internal/web/static/js/flow.mjs`, `flowdoc.mjs`, `flows.mjs`
- Modify: `internal/web/static/js/app.mjs:20-50`, `screens.mjs`, `chrome.mjs:59-89`, `session.mjs:339-360`, `fleet.mjs:147-180` and its head, `internal/web/static/app.css`, `internal/web/static/sw.js` (the shell's file list, if it lists modules by hand: check `grep -n 'mjs' internal/web/static/sw.js` and `internal/web/static.go`)

### Task 4.1: routes, the tab, the chip, the flows link

- [ ] **Step 1: `parseRoute`** (`app.mjs`). Before the session regex, add:

```js
  const f = h.match(/^s\/(\d+)\/flow(?:\/(doc\/([a-z-]+(?::[A-Za-z0-9._-]+)?)|[A-Za-z0-9._-]+))?(?:\?(.*))?$/);
  if (f) {
    const q = new URLSearchParams(f[4] || "");
    const plan = q.get("plan") || "";
    if (f[3]) return { name: "flowdoc", id: Number(f[1]), plan, doc: f[3], at: q.get("at") || "" };
    if (f[2]) return { name: "flowstory", id: Number(f[1]), plan, story: f[2] };
    return { name: "flow", id: Number(f[1]), plan };
  }
  if (h === "flows") return { name: "flows" };
```

and document the routes in the comment above `parseRoute`. Then check how `location.hash` reaches it: the `?` is part of the hash.
- [ ] **Step 2: `screens.mjs`.** Add `flow: { Screen: Flow, Head: SessionHead }`, `flowstory: { Screen: FlowStory, Head: SessionHead }`, `flowdoc: { Screen: FlowDoc, Head: SessionHead }` and `flows: { Screen: Flows, Head: FlowsHead }`.
- [ ] **Step 3: `SessionTabs`** (`chrome.mjs`). After the mockup tab:

```js
  // The flow tab shows while the session's worktree holds a flow
  // (s.flows), and on its own screens.
  const onFlow = current === "flow" || current === "flowstory" || current === "flowdoc";
  if ((s.flows && s.flows.length > 0) || onFlow) {
    tabs.push({ key: "flow", label: "flow", href: base + "/flow", attention: (s.flows || []).some((f) => f.asks) });
  }
```

with `current` mapped so that `flowstory` and `flowdoc` light the flow tab: compute `const on = t.key === current || (t.key === "flow" && onFlow)`. Draw an attention tab's dot amber (a `sc-tab-dot-attn` class). `SessionHead` passes `current` as it does now.
- [ ] **Step 4: the chip** (`fleet.mjs` `Row`). Before the jobs badge:

```js
  for (const f of s.flows || []) meta.push(html`<span class=${"sc-flow" + (f.asks ? " sc-flow-asks" : "") + (f.status === "lost" ? " sc-flow-lost" : "")}>${flowChip(f)}</span>`);
```

and export from `flow.mjs`:

```js
// flowChip is a flow's fleet chip (spec §5): flow 2/5 · reviewing · $4.71
// during a run, asks while parked, flow · design / flow · draft 3/4 before.
export function flowChip(f) {
  if (f.stage === "design") return "flow · design";
  if (f.stage === "draft" && f.status === "drafting") return `flow · draft ${f.drafted}/${f.total}`;
  const parts = [`flow ${f.done}/${f.total}`];
  parts.push(f.asks ? "asks" : f.status === "lost" ? "lost" : f.phase || f.status);
  if (typeof f.spent === "number") parts.push("$" + f.spent.toFixed(2));
  return parts.join(" · ");
}
```

- [ ] **Step 5: the flows link.** In `FleetHead` (`fleet.mjs`), put a link to `#flows` beside the settings link, shown when any session has flows or past runs exist. The fleet doesn't know about past runs, so show it whenever the snapshot has a session with `flows`, and always reachable by URL.
- [ ] **Step 6:** run `node --check` on each changed module.

### Task 4.2: the board and the story (`flow.mjs`)

- [ ] **Step 1: Write `flow.mjs`.** The module's shape, written in full:

```js
// The flow screens (spec 2026-11-09 §5): the board (#s/<id>/flow), and a
// story (#s/<id>/flow/<story>), read from GET …/flow?plan= on open and
// again whenever the session's flows in the snapshot change; elapsed times
// tick on a 30 s clock. Everything is drawn as text or elements, never
// from an HTML string.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { sessionOf } from "./fleet.mjs";

const KINDS = { story: ["dev", "gates", "review", "fix"], premise: ["premise"], final: ["gates", "review", "fix"] };
const LABEL = { dev: "dev", gates: "gate", review: "rev", fix: "fix", premise: "prem" };

// useFlow reads the board of session id's flow plan, again when key (the
// session's flows, JSON-stringified) changes.
function useFlow(id, plan, key) { /* useState/useEffect over call("GET", `/api/v1/sessions/${id}/flow?plan=${encodeURIComponent(plan)}`) */ }

function Strip({ flow, base }) { /* the five stages, sc-stage-done / -now / -todo, each an <a> to its doc when it has one */ }
function Question({ q, id }) { /* sc-box sc-attn-box, legend "parked · <kind> · story <id>", text as text, <a href="#s/<id>">open manager ›</a> */ }
function Cells({ cells, kinds }) { /* one <i> per kind, class sc-cell sc-cell-<state>, text LABEL[kind] (+ "?" when asks, "—" when skipped) */ }
function StoryRow({ s, base, plan }) { /* <a> to the story: id, Cells, right side commits + spend or tier·review */ }
function CostBox({ costs }) { /* run spent, flow total ("of N parts" when FlowParts < 3), "~$x projected" or the why, a bar by role with a legend; costs.error shown alone */ }

export function Flow({ snap, route }) {
  const s = sessionOf(snap, route.id);
  const flows = (s && s.flows) || [];
  const plan = route.plan || (flows[0] && flows[0].plan) || "";
  const { data, err } = useFlow(route.id, plan, JSON.stringify(flows));
  /* a plan picker (<select>, no style attribute) when flows.length > 1;
     Strip, Question, stories box (premise row, stories, final row), CostBox,
     the other runs' list */
}

export function FlowStory({ snap, route }) {
  /* the same read; the story's header line, its steps as a timeline
     (sc-tl: dot class by outcome, role, status, elapsed from times, cost
     from costs by name), a step's tap: an agent with a report → the doc
     route report:<name>, a gate line → #s/<id>/job/<job>; findings counts
     and kept Minors; reads: the plan link (doc plan, ?at=<line>), the spec
     sections its Reading line names (as doc spec links), commits → #s/<id>/diff */
}
```

Write the bodies in full while executing. The behaviour each must have is in the spec's §5; the look is the brainstorm's option A (`.superpowers/brainstorm/*/content/flow-tab-layout.html`, card "board", and `flow-drilldowns.html`, card "story"). Reuse `elapsed` and `ago` from `session.mjs` and `fleet.mjs`.

- [ ] **Step 2: Add the CSS** to `app.css`, from the palette's roles only: `.sc-stage*`, `.sc-cell`, `.sc-cell-todo|running|passed|failed|asks|skipped`, `.sc-flow`, `.sc-flow-asks`, `.sc-flow-lost`, `.sc-tl` and its dots, `.sc-bar` and its role segments (`--accent`, `--ok`, `--workspace`, `--dim`), and `.sc-tab-dot-attn`. A segment's width can't be a `style` attribute (the CSP), so the bar uses twenty fixed-width classes `.sc-w5`…`.sc-w100` in steps of five, and each segment takes its share rounded to the nearest five.
- [ ] **Step 3:** `node --check internal/web/static/js/flow.mjs`.

### Task 4.3: the document viewer and the Flows screen

- [ ] **Step 1: `flowdoc.mjs`:** `FlowDoc({snap, route})` reads `GET …/flow/doc?plan=&doc=`, draws `Markdown({blocks})` from `markdown.mjs`, and builds a section index from the blocks' headings (level 2 and 3). It scrolls to `route.at`: a heading's text match for spec sections, or, for the plan, the nearest heading at or after the line. This needs each heading's source line: add `line` to `mdNode` in `buildTreeLimit` (goldmark's `Lines().At(0).Start`, mapped to a line number), only for headings, `omitempty`. Then shows "cut at 1 MiB" when `cut`.
- [ ] **Step 2: `flows.mjs`:** `Flows({snap})` reads `GET /api/v1/flows` on open, and again on a pull-to-refresh: the existing pattern if one exists (`grep -n 'refresh' internal/web/static/js/*.mjs`), else a refresh button in the head. It draws the live box (each with `flowChip`, a link to the board), the past boxes by repo key (`key` shown as the repo's last path segment, decoded from `-`… so shown as the key's tail after the last `-Source-` or as is), each row with stories, `$usd`, the ended word, and a link to the board when the flow is live, else to nothing (the record is opened from a live flow's strip), and the comparison bars. `FlowsHead` is a `ContextRow` with back to the fleet and the title "flows".
- [ ] **Step 3:** `node --check` both.

### Task 4.4: webshots, docs and the final gate

- [ ] **Step 1: Screenshots.** Run the webshot tool as the `loom-dev`/webshot docs say (`grep -n 'webshot' docs/claude/web-layer.md CONTRIBUTING.md`) for `#s/<fixture id>/flow`, `#s/<id>/flow/1`, `#s/<id>/flow/doc/spec` and `#flows`, at the iPhone 15 size. Look at each image (Read the PNG). Fix what differs from option A, and save the images outside the worktree.
- [ ] **Step 2: Docs.**
  - `USAGE.md`: the flow tab, the chip and the Flows screen, in the phone app's section.
  - `internal/web/README.md`: `flow.go`'s row, the routes, the modules (`flow.mjs`, `flowdoc.mjs`, `flows.mjs`), following `docs/claude/web-layer.md`'s checklist.
  - `core/README.md`: the flows look and the three reads.
  - The spec: append a "Planning amendments" section listing this plan's six deviations, and `Find`'s `recordsDir` and `Summary.Engine`.
- [ ] **Step 3: Final gate:**

```bash
loom job run -d "flowviz final build+test" -- env CGO_ENABLED=0 go test ./... -count=1
loom job run -d "flowviz final race" -- env CC=clang CGO_ENABLED=1 go test -race ./... -count=1
loom job run -d "flowviz final vet" -- env CGO_ENABLED=0 go vet ./...
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
bash flow/lib/test.sh
loom job run -d "flowviz windows" -- env GOOS=windows CGO_ENABLED=0 go build ./...
```

Expected: all PASS, gofmt prints nothing. golangci-lint is skipped (CI pins v1; the machine has v2): say so in the summary.
- [ ] **Step 4:** Commit by path: the new and changed `internal/web/static/` files, `internal/web/markdown.go` (heading lines), its test, `USAGE.md`, the READMEs and the spec, with the message `feat(web): the flow tab, the story, documents and the Flows screen`.

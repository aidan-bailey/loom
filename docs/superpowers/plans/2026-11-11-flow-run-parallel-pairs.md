# Parallel pairs in the run engine: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. The owner chose inline execution (2026-11-11).

**Goal:** Let `/flow:run`'s engine run a plan-declared pair's second story's developer in a lane worktree beside the first story, and pick its commits in after.

**Spec:** `docs/superpowers/specs/2026-11-11-flow-run-parallel-pairs-design.md`

**Architecture:** `check-plan` reads a `**Parallel:**` line and marks each laned story `lane: true` in `--json`. The engine (`internal/flowrun`) becomes tracks under one lock: the main track runs today's procedure, and a lane track runs a laned story's `build()` in a worktree it makes through new `Effects` (`Lane`, `Pick`, `DropLane`, `OnlyPlanCommits`). The CLI adapter (`internal/cli/flow_run_fx.go`) implements those on git.

**Tech stack:** Go (testify), Python 3 (unittest), git worktrees and cherry-pick.

**Repo rules that bind every task:**
- Stage and commit by path only: `git add <paths>`, `git commit -m … -- <paths>`.
- Compiling commands go through loom's queue: `loom job run -d "<what>" -- <cmd>`.
- No raw git exec in production code: use `internalexec.GitCommand`.
- After changing `flow/`, run `bash flow/lib/test.sh`.
- Format with `gofmt -w <files>`, never `gofmt -w .`.

---

## File structure

| File | Responsibility |
|---|---|
| `flow/lib/check_plan.py` | `declared_pairs`, `pair_problems`, `lane` in `--json`, the declared-pairs output line |
| `flow/lib/plan_packet.py` | the seams packet's "Declared pairs" section |
| `internal/flowrun/engine.go` | `track`, the lock (`wait`, `finish`, `start`), `laneRun`, `startLane`, `laneDev`, `join`, `pick`, `landed`, the §5.7 adoption |
| `internal/flowrun/state.go` | `Story.Lane`, `Row.Lane`, `Restory` keeping the run's pairs |
| `internal/flowrun/answers.go` | `KindLane`, `WordRedo` |
| `internal/flowrun/briefs.go` | `LaneNote`, `RedoDecision` |
| `internal/flowrun/record.go` | the `(lane)` developer cell |
| `internal/cli/flow_run_fx.go` | `Agent`, `Head` and `Clean` by dir; `Lane`, `Pick`, `DropLane`, `OnlyPlanCommits`; `logf` under a mutex |
| `internal/cli/flow_run.go` | `takenHead` on the shared `onlyPlanCommits`; `show`'s lane state; `runFX.spec` |
| docs | `flow/plugin/docs/dispatch.md`, `flow/plugin/skills/draft/SKILL.md`, `flow/plugin/skills/run/SKILL.md`, `flow/plugin/docs/records.md`, `internal/flowrun/README.md`, `internal/flowrun/CLAUDE.md` |

---

### Task 1: Probe a lane's environment (spike, no commit)

The spec's §8 probe asks whether a headless agent working in a lane resolves `loom job`, `loom flow` and the git guard the same way it does from the session's worktree. Reading the code settled most of it:
- `loom job` takes any absolute `Cwd` (`core/queue_ops.go`), and finds its owner through `LOOM_LAUNCH`, not the cwd (`internal/cli/job.go`).
- `loom flow costs` finds transcripts by session ID across every project dir (`agent_costs.py`'s `locate_session`).
- Loom lists and prunes worktrees only under its own `.loom/worktrees`.

What's left is checked by hand, in a scratch clone and never in this worktree.

- [ ] **Step 1: Make a scratch clone with a lane**

```bash
S=$(mktemp -d /tmp/lane-probe.XXXX)
git clone -q /tb/Source/Personal/loom/.loom/worktrees/aidanb/flow-depr-supervise_18e6f02a6de4238a "$S/repo"
git -C "$S/repo" worktree add -q --detach "$S/lane" HEAD
```

- [ ] **Step 2: Run the flow tools and a job from the lane**

```bash
cd "$S/lane" && loom flow conf key && loom flow conf state && loom flow conf doctor | tail -3
loom flow check-plan docs/superpowers/plans/2026-11-06-pwa-conversation-first.md | head -2
loom job run -d "lane probe" -- git -C "$S/lane" status --short
```

Expected:
- `conf key` prints the repo's key, the same as from `$S/repo`.
- `conf state` prints the lane's own `<git-dir>/flow`, which `git worktree remove` deletes with the lane: developers' logs there are scratch.
- `doctor` passes.
- `check-plan` prints OK.
- The job runs and exits 0.

- [ ] **Step 3: Run the guard as a developer would from the lane**

```bash
cd "$S/lane" && printf '%s' '{"agent_type":"flow:intermediate-dev","tool_name":"Bash","tool_input":{"command":"git stash"},"cwd":"'"$S/lane"'"}' | loom flow guard; echo "exit $?"
printf '%s' '{"agent_type":"flow:intermediate-dev","tool_name":"Bash","tool_input":{"command":"git commit -m x -- a.go"},"cwd":"'"$S/lane"'"}' | loom flow guard; echo "exit $?"
```

Expected: `exit 2` for the stash, `exit 0` for the pathspec commit.

- [ ] **Step 4: Record the outcome and clean up**

Write the results under "Execution notes" at the end of this plan. On any surprise, stop and amend the spec before Task 3. Then `rm -rf "$S"`.

---

### Task 2: The `**Parallel:**` line in check-plan and the seams packet

**Files:**
- Modify: `flow/lib/check_plan.py`
- Modify: `flow/lib/plan_packet.py`
- Test: `flow/lib/check_plan_test.py`, `flow/lib/plan_packet_test.py`

- [ ] **Step 1: Write the failing check-plan tests**

Append to `flow/lib/check_plan_test.py`:

```python
def paired(line: str) -> str:
    """GOOD with `line` below its Spec line."""
    return GOOD.replace("**Spec:** `docs/spec.md`, approved.\n", f"**Spec:** `docs/spec.md`, approved.\n\n{line}\n")


class ParallelLineTest(unittest.TestCase):
    """The `**Parallel:**` line (parallel pairs spec §5.1)."""

    def test_a_declared_adjacent_safe_pair_passes(self):
        code, out = run(paired("**Parallel:** 1+2, 2+3"))
        self.assertEqual(code, 0, out)
        self.assertIn("Declared pairs (the second story's developer runs in a lane): 1+2, 2+3", out)

    def test_without_the_line_every_story_runs_in_order(self):
        code, out = run(GOOD)
        self.assertEqual(code, 0, out)
        self.assertIn("Declared pairs (the second story's developer runs in a lane): none, so every story runs in order", out)

    def test_json_marks_each_laned_story(self):
        code, out = run(paired("**Parallel:** 2+3."), as_json=True)
        self.assertEqual(code, 0, out)
        self.assertEqual([s.get("lane", False) for s in json.loads(out)["stories"]], [False, False, True])

    def test_each_failing_entry(self):
        cases = {
            "**Parallel:** 1+3": "Story 3 must be the story right after Story 1",
            "**Parallel:** 2+9": "names Story 9, which the plan doesn't have",
            "**Parallel:** 1-2": "the **Parallel:** entry '1-2' is not two story ids joined by +",
            "**Parallel:** 1+2, 1+2": "the **Parallel:** pair 1+2 is declared twice",
            "**Parallel:** 1+2\n**Parallel:** 2+3": "a second **Parallel:** line",
        }
        for line, want in cases.items():
            with self.subTest(line=line):
                code, out = run(paired(line))
                self.assertEqual(code, 1, out)
                self.assertIn(want, out)

    def test_a_pair_sharing_a_file_fails(self):
        text = paired("**Parallel:** 2+3").replace("- **Files:** `ui/panel.ts`", "- **Files:** `ui/panel.ts`, `a/route.rs`")
        code, out = run(text)
        self.assertEqual(code, 1, out)
        self.assertIn("the **Parallel:** pair 2+3: both stories' **Files** name a/route.rs", out)

    def test_a_pair_that_both_run_an_exclusive_build_fails(self):
        code, out = run(paired("**Parallel:** 2+3").replace("`pnpm test panel`", "`cargo test -p ui`"))
        self.assertEqual(code, 1, out)
        self.assertIn("the **Parallel:** pair 2+3: both stories' **Verify** run an exclusive build", out)

    def test_a_line_below_the_first_story_is_not_read(self):
        code, out = run(GOOD.replace("### Task 3.1: Route", "**Parallel:** 1+3\n\n### Task 3.1: Route"))
        self.assertEqual(code, 0, out)
        self.assertIn("runs in a lane): none", out)
```

- [ ] **Step 2: Run them and see them fail**

Run: `cd flow/lib && python3 -m unittest -q check_plan_test.ParallelLineTest`
Expected: FAIL. No "Declared pairs" line is printed, and the bad entries pass.

- [ ] **Step 3: Implement**

In `flow/lib/check_plan.py`, beside `REVIEW_RE`:

```python
PARALLEL_RE = re.compile(r"^\*\*Parallel:\*\*\s*(.*)$")
PAIR_RE = re.compile(r"^([^\s+,]+)\s*\+\s*([^\s+,]+)$")
```

After `plan_review`:

```python
def declared_pairs(text: str) -> tuple[list[tuple[str, str]], list[str]]:
    """The pairs the plan's `**Parallel:**` line declares before its first story, and what keeps the line from parsing."""
    pairs: list[tuple[str, str]] = []
    problems: list[str] = []
    seen = False
    fenced = False
    for n, line in enumerate(text.splitlines(), 1):
        if FENCE_RE.match(line):
            fenced = not fenced
            continue
        if fenced:
            continue
        if STORY_RE.match(line):
            break
        declared = PARALLEL_RE.match(line)
        if not declared:
            continue
        if seen:
            problems.append(f"line {n}: a second **Parallel:** line: declare every pair on one line")
            continue
        seen = True
        for entry in (e.strip() for e in declared.group(1).strip().removesuffix(".").split(",")):
            pair = PAIR_RE.match(entry)
            if pair:
                pairs.append((pair.group(1), pair.group(2)))
            else:
                problems.append(f"line {n}: the **Parallel:** entry '{entry}' is not two story ids joined by +, as in 3+4")
    return pairs, problems


def pair_problems(stories: list[Story], pairs: list[tuple[str, str]], exclusive: Sequence[str]) -> list[str]:
    """What keeps each declared pair's second story from running its developer in a lane beside its first."""
    index = {story.sid: i for i, story in enumerate(stories)}
    problems: list[str] = []
    seen: set[tuple[str, str]] = set()
    for a, b in pairs:
        name = f"the **Parallel:** pair {a}+{b}"
        if (a, b) in seen:
            problems.append(f"{name} is declared twice")
            continue
        seen.add((a, b))
        missing = [sid for sid in (a, b) if sid not in index]
        if missing:
            problems.append(f"{name} names {', '.join('Story ' + sid for sid in missing)}, which the plan doesn't have")
            continue
        first, second = stories[index[a]], stories[index[b]]
        if index[b] != index[a] + 1:
            problems.append(f"{name}: Story {b} must be the story right after Story {a}")
        shared = sorted(first.files & second.files)
        if shared:
            problems.append(f"{name}: both stories' **Files** name {', '.join(shared)}")
        if first.builds(exclusive) and second.builds(exclusive):
            problems.append(f"{name}: both stories' **Verify** run an exclusive build")
    return problems
```

Give `story_json` a `lane` flag:

```python
def story_json(story: Story, vouch: dict[str, bool], markers: Sequence[str], lane: bool = False) -> dict:
    """A story as `--json` gives it, for loom flow run (flow in loom part 3b); a laned story carries `"lane": true`."""
    out = {
        "id": story.sid, "line": story.line, "end": story.end,
        "developer": story.fields["Developer"].strip("` ").removeprefix("flow:"),
        "tier": story.tier, "review": "vouched" if vouch[story.sid] else "full",
        "landmines": [marker for marker, _ in markers_in(story, markers)],
        "interfaces": story.fields.get("Interfaces", ""), "verify": story.fields.get("Verify", ""),
    }
    if lane:
        out["lane"] = True
    return out
```

In `main`, directly after `stories, problems, spec = check(text, Path(arg), profile.markers)`:

```python
        pairs, pair_errors = declared_pairs(text)
        problems += pair_errors + pair_problems(stories, pairs, profile.exclusive)
```

In the `--json` branch, mark each laned story:

```python
            laned = {b for _, b in pairs}
            print(json.dumps({"spec": spec, "plan_review": review,
                              "stories": [story_json(s, vouch, profile.markers, s.sid in laned) for s in stories]}))
```

After the "Parallel-safe pairs" print, add the declared pairs:

```python
        declared = ", ".join(f"{a}+{b}" for a, b in pairs) if pairs else "none, so every story runs in order"
        print(f"  Declared pairs (the second story's developer runs in a lane): {declared}")
```

In the module docstring, after the paragraph on the Review line, add:

```
A plan may declare, before its first story, the pairs of stories whose second story's developer runs in
a lane beside the first under loom flow run: `**Parallel:** 3+4, 7+8`. Each pair is a story and the one
right after it, with disjoint Files and at most one exclusive build; a pair that isn't fails the check,
as does a second Parallel line. --json marks each pair's second story `"lane": true`.
```

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd flow/lib && python3 -m unittest -q check_plan_test`
Expected: OK.

- [ ] **Step 5: Write the failing seams-packet test**

In `flow/lib/plan_packet_test.py`, add to `UseAndDefinitionRulesTest`. Before writing it, read `TS_PLAN` and confirm Story 1 defines `PriceRow` and `formatPrice` and Story 2 uses them; adapt the ids if not.

```python
    def test_seams_list_what_a_declared_pairs_first_story_gives_its_second(self):
        text = self.write_plan("ts-paired", "**Parallel:** 1+2\n\n" + TS_PLAN)
        packet = plan_packet.seams_packet(text, "HEAD", self.root / "no-drafts")
        self.assertIn("## Declared pairs", packet)
        self.assertIn("- 1+2: names Story 1 defines that Story 2 uses: PriceRow, formatPrice", packet)

    def test_seams_have_no_pairs_section_without_the_line(self):
        packet = plan_packet.seams_packet(self.write_plan("ts-unpaired", TS_PLAN), "HEAD", self.root / "no-drafts")
        self.assertNotIn("## Declared pairs", packet)
```

Run: `cd flow/lib && python3 -m unittest -q plan_packet_test`
Expected: FAIL on the first test.

- [ ] **Step 6: Implement**

In `flow/lib/plan_packet.py`, import `declared_pairs` beside `FENCE_RE`:

```python
from check_plan import FENCE_RE, declared_pairs, repo_root  # noqa: E402
```

Add a constant after `SEAMS_READING_RULES`:

```python
PAIRS_RULE = (
    "Each pair's second story runs its developer in a lane at the first story's base, without the first's code: "
    "a name it uses from the first breaks it, and so does a file, flag or behaviour it needs that no name shows."
)
```

In `seams_packet`, before the "## Files line of every story" block:

```python
    pairs, _ = declared_pairs(text)
    if pairs:
        out += ["", "## Declared pairs", "", PAIRS_RULE, ""]
        for a, b in pairs:
            names = sorted(name for name in defs.get(a, {}) if name in used.get(b, {}) and name not in own.get(b, {}))
            out.append(f"- {a}+{b}: names Story {a} defines that Story {b} uses: {', '.join(names) if names else '(none found)'}")
```

- [ ] **Step 7: Run the flow suite**

Run: `bash flow/lib/test.sh`
Expected: every suite OK, and the agent lint passes.

- [ ] **Step 8: Commit**

```bash
git add flow/lib/check_plan.py flow/lib/check_plan_test.py flow/lib/plan_packet.py flow/lib/plan_packet_test.py
git commit -m "feat(flow): a plan declares the pairs whose second story runs in a lane" -- flow/lib/check_plan.py flow/lib/check_plan_test.py flow/lib/plan_packet.py flow/lib/plan_packet_test.py
```

---

### Task 3: Tracks and the lock (no change in behaviour)

**Files:**
- Modify: `internal/flowrun/engine.go`
- Modify: `internal/cli/flow_run_fx.go` (signatures only), `internal/cli/flow_run.go` (`takenHead` callers if needed)
- Test: `internal/flowrun/engine_test.go`

- [ ] **Step 1: Split the engine into shared state and a track**

In `internal/flowrun/engine.go`, replace the `engine` struct and `Run` with:

```go
var errClosed = errors.New("the run closed while this track waited")

type engine struct {
	st        *State
	fx        Effects
	seq       map[string]int // attempts at each name prefix, this pass
	resumes   map[string]int // resumes of each agent run, this pass
	limits    map[string]int // usage limits each agent run stopped on, this pass
	lastReset map[string]time.Time
	final     Story
	accepted  map[string]map[string]string

	// mu is held by whichever track runs engine code. A track lets go of it
	// only while it waits (wait), so another track can run meanwhile
	// (parallel pairs spec §5.3).
	mu      sync.Mutex
	closed  bool  // the run parked, stopped, ended or failed: no track touches the state again
	outcome error // what closed it: nil when the main track ended
	over    chan struct{}
	wg      sync.WaitGroup // the tracks' goroutines, which tests wait for
	main    *track
}

// track is one line of the engine's work (parallel pairs spec §5.3): the
// main track in the session's worktree, or a lane track running a laned
// story's developer in its lane.
type track struct {
	*engine
	head string        // the head last recorded
	cur  string        // the story in hand, "" outside one
	sent []SentFinding // the story in hand's findings sent to a fix round, for its doc pass
	dir  string        // the lane's path; "" for the session's worktree
}

// Run drives st until it parks or ends, saving it after every step. It
// returns nil when st has parked or ended, and otherwise the error it
// could not get past (a lost daemon, a file it can't write), with st left
// running for `loom flow run resume`. It returns at the run's first
// outcome: a track still waiting on a job finds the run closed when it
// wakes, and touches nothing.
func Run(st *State, fx Effects) error { return newEngine(st, fx).start() }

func newEngine(st *State, fx Effects) *engine {
	e := &engine{st: st, fx: fx, seq: map[string]int{}, resumes: map[string]int{}, limits: map[string]int{},
		lastReset: map[string]time.Time{}, final: Story{ID: finalStory}, accepted: map[string]map[string]string{},
		over: make(chan struct{})}
	e.main = &track{engine: e, head: st.Base}
	return e
}

func (e *engine) start() error {
	st := e.st
	st.Status, st.Question, st.Why = Running, nil, ""
	st.Phase, st.Notes, st.Kept = "", nil, nil
	for i := range st.Stories {
		st.Stories[i].Row = Row{}
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.mu.Lock()
		defer e.mu.Unlock()
		e.finish(e.main.run())
	}()
	<-e.over
	e.mu.Lock()
	defer e.mu.Unlock()
	switch err := e.outcome; {
	case errors.Is(err, errParked):
		return nil
	case errors.Is(err, errStopped):
		return e.end(Stopped)
	case err != nil:
		return err
	}
	return e.end(Done)
}

// finish closes the run with err, its first outcome (nil: the main track
// ended); a later outcome changes nothing.
func (e *engine) finish(err error) {
	if e.closed {
		return
	}
	e.closed, e.outcome = true, err
	close(e.over)
}

// wait runs fn, which waits on a job or a track, without the engine's lock.
// It is errClosed when the run closed meanwhile; the caller then returns
// at once, touching nothing.
func (e *engine) wait(fn func() error) error {
	e.mu.Unlock()
	err := fn()
	e.mu.Lock()
	if e.closed {
		return errClosed
	}
	return err
}
```

Add `"sync"` to the imports.

- [ ] **Step 2: Move the per-track methods onto `track`**

Change the receiver from `(e *engine)` to `(e *track)` on: `run`, `note`, `headAt`, `commitsAt`, `ask`, `checkTree`, `agent`, `resume`, `outlast`, `resumeOnce`, `settle`, `failed`, `review`, `build`, `gates`, `gateLoop`, `premises`, `dispatchReport`, `conformance`, `story`, `findings`, `fixRound`, `planLines`, `sendFindings`, `docs`, `docPass`, `docsCheck` and `finalRound`.

These stay on `engine`: `end`, `recall`, `remember`, `next`, `storyOf`, `storyAtLine`, `accept`, `acceptedAt`, `finish`, `wait` and `start`.

The bodies read `e.st`, `e.fx` and the maps through the embedded `*engine`, so they don't change.

- [ ] **Step 3: Give the waiting effects a working dir, and wait without the lock**

Change the `Effects` interface:

```go
	// Agent starts agent run name in dir ("" for the session's worktree;
	// parallel pairs spec §5.3), or follows it.
	Agent(dir, name, role, effort, brief string) (Report, error)
	...
	Head(dir string) (string, error)
	Clean(dir string) (bool, error)
```

In `agent`, replace `if r, err = e.fx.Agent(name, role, effort, brief); err != nil {` with:

```go
	if err := e.wait(func() (err error) { r, err = e.fx.Agent(e.dir, name, role, effort, brief); return err }); err != nil {
		return r, err
	}
```

In `resumeOnce`, replace `r, err := e.fx.Resume(name, text)` and its check with:

```go
	var r2 Report
	if err := e.wait(func() (err error) { r2, err = e.fx.Resume(name, text); return err }); err != nil {
		return r2, err
	}
	r = r2
```

In `gates`, wrap both the `e.fx.Services(…)` call and the `e.fx.Gates(name, commits)` call:

```go
	if !started {
		var serr error
		if err := e.wait(func() error { serr = e.fx.Services(e.st.Blocks.Services); return nil }); err != nil {
			return r, err
		}
		if serr != nil {
			r = GateResult{Ended: "infra", Table: "FAIL (infra): a service failed: " + serr.Error()}
			return r, e.remember("gates:"+name, r)
		}
	}
	if err := e.wait(func() (err error) { r, err = e.fx.Gates(name, commits); return err }); err != nil {
		return r, err
	}
	return r, e.remember("gates:"+name, r)
```

In `headAt`, call `e.fx.Head(e.dir)`. In `checkTree`, call `e.fx.Head(e.dir)` and `e.fx.Clean(e.dir)`.

- [ ] **Step 4: Update the fake and the adapter to the new signatures**

In `internal/flowrun/engine_test.go`:
- Give `fakeFX` a `mu sync.Mutex`, and take it at the top of every method (`f.mu.Lock(); defer f.mu.Unlock()`).
- Change `Agent` to `func (f *fakeFX) Agent(dir, name, role, effort, brief string) (Report, error)`, and record `f.dirs[name] = dir` (add `dirs map[string]string` to the struct and to `newFake`).
- Change `Head(dir string)` and `Clean(dir string)` to take a dir. Task 4 gives them meaning for lanes; until then they ignore it.

Add a test helper that waits for every track:

```go
// runSettled is Run, waiting after its outcome for every track's goroutine,
// so a test's next Run never shares the fake with a track still waking.
func runSettled(st *State, fx Effects) error {
	e := newEngine(st, fx)
	err := e.start()
	e.wg.Wait()
	return err
}
```

Make `drive` and `TestRun_ARestartAtEverySavePointSubmitsNothingTwice` call `runSettled` instead of `Run`.

In `internal/cli/flow_run_fx.go`:
- Add `mu sync.Mutex` to `runFX`, and take it in `logf` around the `Fprintf`.
- Add the dir parameters:

```go
// dirOf is dir, or the run's worktree for "".
func (fx *runFX) dirOf(dir string) string {
	if dir == "" {
		return fx.cwd
	}
	return dir
}

// gitIn runs git args in dir.
func (fx *runFX) gitIn(dir string, args ...string) (string, error) {
	var out, errOut bytes.Buffer
	cmd := internalexec.GitCommand(context.Background(), dir, args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// git runs git args in the run's worktree.
func (fx *runFX) git(args ...string) (string, error) { return fx.gitIn(fx.cwd, args...) }

func (fx *runFX) Head(dir string) (string, error) { return fx.gitIn(fx.dirOf(dir), "rev-parse", "HEAD") }

func (fx *runFX) Clean(dir string) (bool, error) {
	out, err := fx.gitIn(fx.dirOf(dir), "status", "--porcelain")
	return out == "", err
}
```

- In `Agent(dir, name, role, effort, brief string)`, use `cwd := fx.dirOf(dir)` in place of `fx.cwd` in `agentArgv` and in `agentRun{… Cwd: cwd}`.
- Update every other caller of `fx.Head()` and `fx.Clean()` in `internal/cli` to pass `""`. Find them with `grep -rn 'fx.Head()\|fx.Clean()\|\.Head()$' internal/cli`.

- [ ] **Step 5: Build, then run the engine and CLI tests under -race**

Run: `loom job run -d "flowrun+cli race" -- sh -c 'CC=clang CGO_ENABLED=1 go test -race ./internal/flowrun/ ./internal/cli/ -run "TestRun|TestFlowRun|TestTakenHead|TestRestory"'`
Expected: PASS. The behaviour is unchanged, and the race detector is clean.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/flowrun/engine.go internal/flowrun/engine_test.go internal/cli/flow_run_fx.go internal/cli/flow_run.go
git add internal/flowrun/engine.go internal/flowrun/engine_test.go internal/cli/flow_run_fx.go internal/cli/flow_run.go
git commit -m "refactor(flowrun): the engine runs as tracks under one lock" -- internal/flowrun/engine.go internal/flowrun/engine_test.go internal/cli/flow_run_fx.go internal/cli/flow_run.go
```

---

### Task 4: Lanes in the engine

**Files:**
- Modify: `internal/flowrun/engine.go`, `state.go`, `answers.go`, `briefs.go`, `record.go`
- Test: `internal/flowrun/engine_test.go`, `internal/flowrun/restory_test.go`

- [ ] **Step 1: The new state, kinds, effects and brief lines**

In `state.go`, give `Story` and `Row` their lane fields:

```go
	// Lane is true for a laned story: its developer runs in a lane beside
	// the story before it (parallel pairs spec §5.1).
	Lane bool `json:"lane,omitempty"`
```

```go
	Lane        string   `json:"lane,omitempty"` // a laned story's lane path
```

In `Restory`, inside the loop after the developer/tier/review check:

```go
		if f.Lane != s.Lane {
			kept = append(kept, fmt.Sprintf("Story %s %s, as the run started (the plan now says it %s)", s.ID, laneWords(s.Lane), laneWords(f.Lane)))
		}
```

and add:

```go
func laneWords(lane bool) string {
	if lane {
		return "runs its developer in a lane"
	}
	return "runs in order"
}
```

In `answers.go`:

```go
	// KindLane is a laned story's lane left dirty or a pick of its commits
	// that conflicted (parallel pairs spec §5.6).
	KindLane = "lane"
```

```go
	// WordRedo runs a laned story's developer again in the session's worktree.
	WordRedo = "redo"
```

Add `WordRedo` to `allWords`, and `KindLane: {[]string{WordRetry, WordRedo, WordStop}, false},` to `takes`.

In `engine.go`, add to `Effects`:

```go
	// Lane is the lane a laned story's developer runs in (parallel pairs
	// spec §5.3): a worktree of the repository named name, detached at
	// base, made, or reused when it is already one.
	Lane(name, base string) (string, error)
	// Pick cherry-picks commits in order onto the session's worktree, whose
	// HEAD is onto, or onto with a prefix of commits already picked (a crash
	// mid-pick), and is the commits it made. A conflict aborts the pick as a
	// *PickConflict.
	Pick(commits []string, onto string) ([]string, error)
	// DropLane removes the lane at dir, refusing one that isn't clean.
	DropLane(dir string) error
	// OnlyPlanCommits reports whether head descends from base and every
	// commit in base..head touches only the run's spec and plan (spec §5.7).
	OnlyPlanCommits(base, head string) (bool, error)
```

and:

```go
// PickConflict is a pick that didn't apply: the lane commit it stopped at
// and the files it conflicted on. Pick has aborted it.
type PickConflict struct {
	Commit string
	Files  []string
}

func (c *PickConflict) Error() string {
	files := strings.Join(c.Files, ", ")
	if files == "" {
		files = "no file (the pick came out empty)"
	}
	return fmt.Sprintf("the lane commit %s conflicts on %s", short(c.Commit), files)
}
```

In `briefs.go`:

```go
// LaneNote tells a laned story's developer where it works (parallel pairs
// spec §5.3).
func LaneNote(dir, base string) string {
	return fmt.Sprintf("\nYou work in a lane: a worktree of its own at %s, detached at %s, beside the story before yours, which runs in the session's worktree. Commit there as the plan says; the run picks your commits onto its branch once that story is done.\n", dir, short(base))
}

// RedoDecision is the decision a laned story's developer runs with when its
// lane didn't land and it builds the story again in the session's worktree.
func RedoDecision(dir string) string {
	return fmt.Sprintf("An earlier attempt ran in the lane %s, and its commits are not on this branch: they didn't apply, or the lane was left dirty. Build the story here, from this branch's HEAD; `git -C %s log` shows what that attempt did.", dir, dir)
}
```

In `record.go`'s story rows, after `dev = s.Developer`:

```go
		if r.Lane != "" {
			dev += " (lane)"
		}
```

- [ ] **Step 2: Teach the fake lanes**

In `engine_test.go`:

```go
// fakeLane is a lane the fake made: its base, its developer's commits, and
// whether it is dirty.
type fakeLane struct {
	base  string
	n     int
	dirty bool
}
```

Add these fields to `fakeFX`, and initialise the maps in `newFake`:

```go
	lanes     map[string]*fakeLane // by dir
	conflicts int                  // picks that conflict, from the next one on
	picked    map[string][]string  // each pick's commits, by onto, for a pick asked again
	planOnly  map[string]bool      // OnlyPlanCommits' answer, by head
	dropErr   error
	dirtyAfter map[string]bool         // an agent run that leaves its lane dirty
	hold      map[string]chan struct{} // an Agent call waits on it before it returns
	entered   map[string]chan struct{} // closed when that Agent call begins
```

Give `Agent`, `Head`, `Clean` and `Commits` lanes:

```go
func (f *fakeFX) Agent(dir, name, role, effort, brief string) (Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch := f.entered[name]; ch != nil {
		close(ch)
		delete(f.entered, name)
	}
	r := f.report(name, role)
	if f.started[name] {
		f.calls = append(f.calls, "follow "+name)
		return r, nil
	}
	f.started[name], f.roles[name], f.briefs[name], f.dirs[name] = true, role, brief, dir
	f.calls = append(f.calls, strings.TrimSpace(fmt.Sprintf("agent %s %s %s", name, role, effort)))
	if commits(role) && (r.done() || f.commitsOn[name]) && !f.noCommit[name] {
		if l := f.lanes[dir]; l != nil {
			l.n++
			l.dirty = f.dirtyAfter[name]
		} else {
			f.head++
		}
	}
	if ch := f.hold[name]; ch != nil {
		f.mu.Unlock()
		<-ch
		f.mu.Lock()
	}
	return r, nil
}

func (f *fakeFX) Head(dir string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l := f.lanes[dir]; l != nil {
		if l.n == 0 {
			return l.base, nil // a new lane's HEAD is its base, as git's is
		}
		return fmt.Sprintf("%s+%d", l.base, l.n), nil
	}
	return "h" + strconv.Itoa(f.head), nil
}

func (f *fakeFX) Clean(dir string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l := f.lanes[dir]; l != nil {
		return !l.dirty, nil
	}
	return !f.dirty, nil
}
```

In `Commits`, before the main-tree arithmetic:

```go
	if lb, n, ok := strings.Cut(head, "+"); ok {
		k, _ := strconv.Atoi(n)
		var cs []string
		for i := 1; i <= k; i++ {
			cs = append(cs, fmt.Sprintf("%s+c%d", lb, i))
		}
		return cs, nil
	}
```

A resume's commit goes to the agent's lane when it has one. In `Resume`, replace `f.head++` with:

```go
		if l := f.lanes[f.dirs[name]]; l != nil {
			l.n++
		} else {
			f.head++
		}
```

Add the new effects:

```go
func (f *fakeFX) Lane(name, base string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir := "/lanes/" + name
	f.calls = append(f.calls, "lane "+name+" "+base)
	if f.lanes[dir] == nil {
		f.lanes[dir] = &fakeLane{base: base}
	}
	return dir, nil
}

func (f *fakeFX) Pick(commits []string, onto string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if done, ok := f.picked[onto]; ok {
		return done, nil // a pick asked again after a crash
	}
	if onto != "h"+strconv.Itoa(f.head) {
		return nil, fmt.Errorf("HEAD is h%d, not %s", f.head, onto)
	}
	f.calls = append(f.calls, fmt.Sprintf("pick %d onto %s", len(commits), onto))
	if f.conflicts > 0 {
		f.conflicts--
		return nil, &PickConflict{Commit: commits[0], Files: []string{"a.go"}}
	}
	var out []string
	for range commits {
		f.head++
		out = append(out, "h"+strconv.Itoa(f.head))
	}
	f.picked[onto] = out
	return out, nil
}

func (f *fakeFX) DropLane(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "drop "+dir)
	return f.dropErr
}

func (f *fakeFX) OnlyPlanCommits(_, head string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.planOnly[head], nil
}

// laned is s marked as a laned story.
func laned(s Story) Story {
	s.Lane = true
	return s
}
```

- [ ] **Step 3: Write the failing engine tests**

Append to `engine_test.go`:

```go
// A laned story's developer runs in its lane at its partner's base, and its
// commits are picked onto the head its partner left (parallel pairs spec §5.5).
func TestRun_ALaneRunsBesideItsPartnerAndIsPicked(t *testing.T) {
	f := newFake()
	st := newState(story("1", "intermediate-dev", "vouched"), laned(story("2", "intermediate-dev", "vouched")))
	drive(t, st, f, nil)
	require.Equal(t, Done, st.Status)
	assert.Contains(t, f.calls, "lane r-s2 h0")
	assert.Equal(t, "/lanes/r-s2", f.dirs["r-s2-dev1"])
	assert.Equal(t, "", f.dirs["r-s1-dev1"])
	assert.Contains(t, f.briefs["r-s2-dev1"], "You work in a lane: a worktree of its own at /lanes/r-s2")
	assert.Contains(t, f.calls, "pick 1 onto h1")
	assert.Contains(t, f.calls, "drop /lanes/r-s2")
	row := st.Stories[1].Row
	assert.Equal(t, "h1", row.Base, "its base is the head before the pick")
	assert.Equal(t, 1, row.Commits, "its commits are exactly the picked one")
	assert.Equal(t, "/lanes/r-s2", row.Lane)
	assert.Contains(t, st.Notes, "Story 2: its developer ran in a lane beside Story 1; 1 commit was picked onto h1")
	assert.Contains(t, Record(st, RecordInputs{}), "| 2 | intermediate-dev (lane) |")
}

// A lane's question parks the run while its partner's developer is still
// at work; the restarted engine follows that developer (spec §5.4).
func TestRun_ALaneQuestionParksWhileItsPartnerWorks(t *testing.T) {
	f := newFake()
	f.reports["r-s2-dev1"] = Report{Status: "NEEDS_CONTEXT", Question: "Which table?"}
	release := make(chan struct{})
	f.hold["r-s1-dev1"] = release
	st := newState(story("1", "intermediate-dev", "vouched"), laned(story("2", "intermediate-dev", "vouched")))
	e := newEngine(st, f)
	require.NoError(t, e.start())
	require.Equal(t, Parked, st.Status)
	assert.Equal(t, "2", st.Question.Story)
	assert.Equal(t, "h0", st.Question.At, "the main track's head, which answer compares HEAD with")
	close(release)
	e.wg.Wait()
	require.NoError(t, st.Answer("the users table", "m", FromSpec, ""))
	drive(t, st, f, nil)
	require.Equal(t, Done, st.Status)
	assert.Contains(t, f.calls, "follow r-s1-dev1")
	assert.Contains(t, f.calls, "resume r-s2-dev1 the users table")
	assert.Equal(t, 1, strings.Count(strings.Join(f.submits(), "\n"), "agent r-s1-dev1 "))
}

// The main track's question parks the run while the lane's developer is at
// work; the restarted engine follows it.
func TestRun_AMainQuestionParksWhileTheLaneWorks(t *testing.T) {
	f := newFake()
	f.reports["r-s1-dev1"] = Report{Status: "NEEDS_CONTEXT", Question: "Which colour?"}
	laneIn, mainGo, laneGo := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f.entered["r-s2-dev1"], f.hold["r-s1-dev1"], f.hold["r-s2-dev1"] = laneIn, mainGo, laneGo
	go func() { <-laneIn; close(mainGo) }()
	st := newState(story("1", "intermediate-dev", "vouched"), laned(story("2", "intermediate-dev", "vouched")))
	e := newEngine(st, f)
	require.NoError(t, e.start())
	require.Equal(t, Parked, st.Status)
	assert.Equal(t, "1", st.Question.Story)
	close(laneGo)
	e.wg.Wait()
	require.NoError(t, st.Answer("blue", "m", FromSpec, ""))
	drive(t, st, f, nil)
	require.Equal(t, Done, st.Status)
	assert.Contains(t, f.calls, "follow r-s2-dev1")
}

// A dirty lane or a conflicting pick asks `lane` (spec §5.6).
func TestRun_ALaneThatDoesNotLand(t *testing.T) {
	setup := func(t *testing.T, conflict bool) (*State, *fakeFX) {
		f := newFake()
		if conflict {
			f.conflicts = 1
		} else {
			f.dirtyAfter["r-s2-dev1"] = true
		}
		st := newState(story("1", "intermediate-dev", "vouched"), laned(story("2", "intermediate-dev", "vouched")))
		require.NoError(t, runSettled(st, f))
		require.Equal(t, Parked, st.Status)
		require.Equal(t, KindLane, st.Question.Kind)
		assert.Equal(t, "2", st.Question.Story)
		return st, f
	}
	t.Run("dirty, then retry", func(t *testing.T) {
		st, f := setup(t, false)
		assert.Contains(t, st.Question.Text, "has uncommitted changes")
		f.lanes["/lanes/r-s2"].dirty = false // the manager cleaned it
		require.NoError(t, st.Answer("retry", "m", FromSpec, ""))
		drive(t, st, f, nil)
		require.Equal(t, Done, st.Status)
		assert.Contains(t, f.calls, "pick 1 onto h1")
	})
	t.Run("a conflict, then retry", func(t *testing.T) {
		st, f := setup(t, true)
		assert.Contains(t, st.Question.Text, "didn't apply: the lane commit h0+c1 conflicts on a.go")
		require.NoError(t, st.Answer("retry", "m", FromSpec, ""))
		drive(t, st, f, nil)
		require.Equal(t, Done, st.Status)
		assert.Equal(t, 2, strings.Count(strings.Join(f.calls, "\n"), "pick 1 onto h1"))
	})
	t.Run("redo", func(t *testing.T) {
		st, f := setup(t, true)
		require.NoError(t, st.Answer("redo", "m", FromSpec, ""))
		drive(t, st, f, nil)
		require.Equal(t, Done, st.Status)
		assert.Equal(t, "", f.dirs["r-s2-dev2"], "the redo runs in the session's worktree")
		assert.Contains(t, f.briefs["r-s2-dev2"], "An earlier attempt ran in the lane /lanes/r-s2")
		assert.NotContains(t, f.calls, "drop /lanes/r-s2", "the lane is kept")
		assert.Equal(t, "h1", st.Stories[1].Row.Base)
	})
	t.Run("stop", func(t *testing.T) {
		st, f := setup(t, false)
		require.NoError(t, st.Answer("stop", "m", FromSpec, ""))
		drive(t, st, f, nil)
		assert.Equal(t, Stopped, st.Status)
		assert.NotContains(t, f.calls, "drop /lanes/r-s2")
	})
}

// In a chain, the next lane starts from the head after the pick (spec §5.5).
func TestRun_AChainOfLanes(t *testing.T) {
	f := newFake()
	st := newState(story("1", "intermediate-dev", "vouched"), laned(story("2", "intermediate-dev", "vouched")), laned(story("3", "intermediate-dev", "vouched")))
	drive(t, st, f, nil)
	require.Equal(t, Done, st.Status)
	assert.Contains(t, f.calls, "lane r-s2 h0")
	assert.Contains(t, f.calls, "lane r-s3 h2")
	assert.Contains(t, f.calls, "pick 1 onto h2")
}

// A HEAD moved only by spec and plan commits is taken, not asked about;
// any other move still parks as tree-moved (spec §5.7).
func TestRun_SpecAndPlanCommitsAreTaken(t *testing.T) {
	f := newFake()
	f.head, f.planOnly["h2"] = 2, true
	st := newState(story("1", "intermediate-dev", "vouched"))
	drive(t, st, f, nil) // a tree-moved question fails the test
	require.Equal(t, Done, st.Status)
	assert.Contains(t, st.Notes, "took the spec and plan commits up to h2 before r-s1-dev1")
}

// A pair replays the same way after a crash at every save of either track.
func TestRun_APairRestartedAtEverySaveSubmitsNothingTwice(t *testing.T) {
	script := func() (*State, *fakeFX, map[string]string) {
		f := newFake()
		f.reports["r-s2-dev1"] = Report{Status: "NEEDS_CONTEXT", Question: "Which table?"}
		st := newState(story("1", "intermediate-dev", "vouched"), laned(story("2", "intermediate-dev", "vouched")), story("3", "junior-dev", "vouched"))
		return st, f, map[string]string{"ctx:r-s2-dev1:1": "users"}
	}
	st, f, answers := script()
	drive(t, st, f, answers)
	require.Equal(t, Done, st.Status)
	want := f.submits()
	slices.Sort(want)
	wantHead := f.head
	for k := 1; k <= f.saves; k++ {
		t.Run(fmt.Sprintf("a crash at save %d", k), func(t *testing.T) {
			st, f, answers := script()
			f.failAt = k
			err := runSettled(st, f)
			for err != nil || st.Status == Parked {
				if err != nil {
					require.ErrorIs(t, err, errCrash)
					st = clone(f.saved)
					if f.saved == nil {
						st, _, _ = script()
					}
				} else {
					require.NoError(t, st.Answer(answers[st.Question.Key], "m", FromSpec, ""))
				}
				err = runSettled(st, f)
			}
			require.Equal(t, Done, st.Status)
			got := f.submits()
			slices.Sort(got)
			assert.Equal(t, want, got)
			assert.Equal(t, wantHead, f.head, "every commit picked once")
		})
	}
}
```

In `restory_test.go`, add a case where the fresh plan pairs a story the run doesn't. Assert that `Restory` keeps `Lane` false and returns `Story 2 runs in order, as the run started (the plan now says it runs its developer in a lane)`. Model it on that file's existing cases.

Run: `loom job run -d "flowrun tests" -- sh -c 'CGO_ENABLED=0 go test ./internal/flowrun/'`
Expected: FAIL. `laned` stories run in order, and `TestRun_SpecAndPlanCommitsAreTaken` parks as tree-moved.

- [ ] **Step 4: Implement the lane track, the join and the adoption**

In `engine.go`, add `lane *laneRun` to `track` (non-nil only for a lane track), and:

```go
// laneRun is a laned story's developer phase in its lane (spec §5.3).
type laneRun struct {
	s, partner *Story
	t          *track        // the lane track
	base       string        // the lane's base: its partner's base
	done       chan struct{} // closed once the developer phase has ended
	dev        Report        // its developer's report, when it ended without error
	notes      []string      // the lane track's notes, which the join appends
}

// laneCheck is the join's look at a lane, memoized (spec §5.5).
type laneCheck struct {
	Head    string   `json:"head"`
	Clean   bool     `json:"clean"`
	Commits []string `json:"commits,omitempty"` // base..head, when clean
}

// pickResult is a pick's end, memoized: the head it picked onto and the
// commits it made, or the conflict it stopped at.
type pickResult struct {
	Onto     string   `json:"onto"`
	Picked   []string `json:"picked,omitempty"`
	Conflict string   `json:"conflict,omitempty"`
}
```

`note` sends a lane track's notes to its buffer:

```go
func (e *track) note(format string, args ...any) {
	n := fmt.Sprintf(format, args...)
	if e.lane != nil {
		e.lane.notes = append(e.lane.notes, n)
		return
	}
	e.st.Notes = append(e.st.Notes, n)
}
```

`ask` gives a lane question the main track's head, and applies an answer's taken head only on the main track:

```go
	q.At = e.head
	if e.lane != nil {
		q.At = e.main.head // answer compares it with HEAD in the session's worktree (spec §5.4)
	}
```

```go
		if e.lane == nil && e.recall("took:"+q.Key, &head) {
			e.head = head // the manager's own commits, taken with its answer
		}
```

`run` starts and joins lanes:

```go
func (e *track) run() error {
	if err := e.premises(); err != nil {
		return err
	}
	var lane *laneRun
	for i := range e.st.Stories {
		s := &e.st.Stories[i]
		base, dev, joined := e.head, Report{}, lane != nil
		if joined {
			var err error
			if base, dev, err = e.join(lane); err != nil {
				return err
			}
		}
		lane = nil
		if i+1 < len(e.st.Stories) && e.st.Stories[i+1].Lane {
			lane = e.startLane(&e.st.Stories[i+1], s)
		}
		if err := e.story(s, base, dev, joined); err != nil {
			return err
		}
	}
	return e.finalRound()
}
```

`story` takes its base and, for a joined story, its developer's report:

```go
// story runs one story: developer, gates, review, findings, doc pass (spec
// §5.3). A joined story's developer ran in its lane: dev is its report, and
// base the head before its pick.
func (e *track) story(s *Story, base string, dev Report, joined bool) error {
	e.cur, e.st.Phase, e.sent = s.ID, "story "+s.ID, nil
	defer func() { e.cur = "" }()
	p := storyPrefix(s.ID)
	s.Row.Base = base
	if !joined {
		// The story's base is the head the engine last recorded, never a fresh
		// read: a commit made outside the run is the tree check's to catch,
		// not the story's to absorb.
		s.Row.Status = "developing"
		var err error
		if dev, err = e.build(s, p+"-dev", s.Developer, DeveloperBrief(e.st, s), false); err != nil {
			return err
		}
	}
	if err := e.gateLoop(s, p, base); err != nil {
		return err
	}
	// … the rest of the body stays as it is.
```

The lane track:

```go
// startLane starts s's developer phase in a lane at the main track's head,
// beside partner, on a goroutine of its own (spec §5.3).
func (e *track) startLane(s, partner *Story) *laneRun {
	l := &laneRun{s: s, partner: partner, base: e.head, done: make(chan struct{})}
	l.t = &track{engine: e.engine, head: e.head, cur: s.ID, lane: l}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.mu.Lock()
		defer e.mu.Unlock()
		defer close(l.done)
		if e.closed {
			return
		}
		dev, err := l.t.laneDev()
		if err != nil {
			e.finish(err)
			return
		}
		l.dev = dev
	}()
	return l
}

// laneDev is the lane track's work: its lane, then its story's developer in it.
func (e *track) laneDev() (Report, error) {
	s := e.lane.s
	var dir string
	if !e.recall("lane:"+s.ID, &dir) {
		name := e.st.ID + "-" + storyPrefix(s.ID)
		if err := e.wait(func() (err error) { dir, err = e.fx.Lane(name, e.lane.base); return err }); err != nil {
			return Report{}, err
		}
		if err := e.remember("lane:"+s.ID, dir); err != nil {
			return Report{}, err
		}
	}
	e.dir = dir
	s.Row.Status, s.Row.Lane = "developing", dir
	return e.build(s, storyPrefix(s.ID)+"-dev", s.Developer, DeveloperBrief(e.st, s)+LaneNote(dir, e.lane.base), false)
}
```

The join:

```go
// join waits for l's developer phase, then picks its lane's commits onto the
// session's worktree (spec §5.5). It is the laned story's base (the head
// before the pick) and its developer's report.
func (e *track) join(l *laneRun) (string, Report, error) {
	s := l.s
	e.cur, e.st.Phase = s.ID, "story "+s.ID
	if err := e.wait(func() error { <-l.done; return nil }); err != nil {
		return "", Report{}, err // a lane that failed has closed the run
	}
	e.st.Notes = append(e.st.Notes, l.notes...)
	for attempt := 1; ; attempt++ {
		key := fmt.Sprintf("%s:%d", s.ID, attempt)
		var c laneCheck
		if !e.recall("lane-check:"+key, &c) {
			var err error
			if c.Head, err = e.fx.Head(l.t.dir); err != nil {
				return "", Report{}, err
			}
			if c.Clean, err = e.fx.Clean(l.t.dir); err != nil {
				return "", Report{}, err
			}
			if c.Clean {
				if c.Commits, err = e.fx.Commits(l.base, c.Head); err != nil {
					return "", Report{}, err
				}
			}
			if err := e.remember("lane-check:"+key, c); err != nil {
				return "", Report{}, err
			}
		}
		problem := "has uncommitted changes"
		if c.Clean {
			r, err := e.pick(s, attempt, c.Commits)
			if err != nil {
				return "", Report{}, err
			}
			if r.Conflict == "" {
				return r.Onto, l.dev, e.landed(l, key, r)
			}
			problem = "didn't apply: " + r.Conflict
		}
		a, err := e.ask(Question{Key: "lane:" + key, Kind: KindLane,
			Text: fmt.Sprintf("The lane of %s at %s (head %s) %s", storyName(s), l.t.dir, short(c.Head), problem)})
		if err != nil {
			return "", Report{}, err
		}
		switch a {
		case WordStop:
			return "", Report{}, errStopped
		case WordRedo:
			e.note("%s: its lane at %s didn't land, and is kept; its developer runs again here", storyName(s), l.t.dir)
			base := e.head
			s.Row.Base, s.Row.Status = base, "developing"
			dev, err := e.build(s, storyPrefix(s.ID)+"-dev", s.Developer, withEarlier(DeveloperBrief(e.st, s), paths(l.dev.Path), RedoDecision(l.t.dir)), false)
			return base, dev, err
		}
		// retry: the manager fixed the lane by hand; look at it again.
	}
}

// pick picks commits onto the session's worktree after its tree check, as
// the join's attempt for s: memoized, and marked before it starts, so a
// crash mid-pick resumes it rather than reading its commits as the tree
// moving.
func (e *track) pick(s *Story, attempt int, commits []string) (pickResult, error) {
	key := fmt.Sprintf("%s:%d", s.ID, attempt)
	name := fmt.Sprintf("%s-%s-pick%d", e.st.ID, storyPrefix(s.ID), attempt)
	var r pickResult
	if e.recall("pick:"+key, &r) {
		e.retake(name)
		return r, nil
	}
	if e.recall("picking:"+key, &r.Onto) {
		e.retake(name)
	} else {
		if err := e.checkTree(name); err != nil {
			return r, err
		}
		e.retake(name)
		r.Onto = e.head
		if err := e.remember("picking:"+key, r.Onto); err != nil {
			return r, err
		}
	}
	picked, err := e.fx.Pick(commits, r.Onto)
	var conflict *PickConflict
	switch {
	case errors.As(err, &conflict):
		r.Conflict = conflict.Error()
	case err != nil:
		return r, err
	default:
		r.Picked = picked
	}
	return r, e.remember("pick:"+key, r)
}

// landed records a pick that applied (spec §5.5): the head after it, the
// lane removed, and the notes.
func (e *track) landed(l *laneRun, key string, r pickResult) error {
	if _, err := e.headAt("picked:" + key); err != nil {
		return err
	}
	var why string
	if !e.recall("dropped:"+l.s.ID, &why) {
		if err := e.fx.DropLane(l.t.dir); err != nil {
			why = err.Error()
		}
		if err := e.remember("dropped:"+l.s.ID, why); err != nil {
			return err
		}
	}
	commits := fmt.Sprintf("%d commits were", len(r.Picked))
	if len(r.Picked) == 1 {
		commits = "1 commit was"
	}
	e.note("%s: its developer ran in a lane beside %s; %s picked onto %s", storyName(l.s), storyName(l.partner), commits, short(r.Onto))
	if why != "" {
		e.note("%s: its lane at %s is left: %s", storyName(l.s), l.t.dir, why)
	}
	return nil
}
```

Check that `storyName` renders `Story 2`, as the tests expect. It is defined in briefs or engine; `grep -n "func storyName" internal/flowrun`.

The §5.7 adoption. In `checkTree`, between the `head == e.head && clean` return and the existing `why :=` line:

```go
	if clean {
		only, err := e.fx.OnlyPlanCommits(e.head, head)
		if err != nil {
			return err
		}
		if only {
			_, err := e.headAt("adopt:" + name)
			return err
		}
	}
```

`retake` sets the head a tree check took before step name, and notes an adoption, so both survive a replay that skips the check:

```go
// retake is the head the tree check before step name took, when it took
// one (a continue, or spec and plan commits it adopted), set again on a
// replay that skips the check, and the note an adoption leaves (spec §5.7).
func (e *track) retake(name string) {
	var h string
	if e.recall("head:tree:"+name, &h) {
		e.head = h
	}
	if e.recall("head:adopt:"+name, &h) {
		e.head = h
		e.note("took the spec and plan commits up to %s before %s", short(h), name)
	}
}
```

In `agent`, after the `if !started { … checkTree … }` block, call `e.retake(name)` on both paths:

```go
	if !started {
		if err := e.checkTree(name); err != nil {
			return r, err
		}
	}
	e.retake(name)
```

- [ ] **Step 5: Run the engine tests, under -race too**

Run: `loom job run -d "flowrun race" -- sh -c 'CC=clang CGO_ENABLED=1 go test -race -count=3 ./internal/flowrun/'`
Expected: PASS three times, with the race detector clean.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/flowrun/*.go
git add internal/flowrun/engine.go internal/flowrun/engine_test.go internal/flowrun/state.go internal/flowrun/answers.go internal/flowrun/briefs.go internal/flowrun/record.go internal/flowrun/restory_test.go
git commit -m "feat(flowrun): a laned story's developer runs in a lane beside its partner" -- internal/flowrun/engine.go internal/flowrun/engine_test.go internal/flowrun/state.go internal/flowrun/answers.go internal/flowrun/briefs.go internal/flowrun/record.go internal/flowrun/restory_test.go
```

---

### Task 5: The adapter's lanes, picks and show

**Files:**
- Modify: `internal/cli/flow_run_fx.go`, `internal/cli/flow_run.go`
- Create: `internal/cli/flow_run_lane_test.go`

- [ ] **Step 1: Write the failing real-git tests**

Create `internal/cli/flow_run_lane_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// laneRepo is a repository with one commit, and a runFX on it.
func laneRepo(t *testing.T) (string, *runFX) {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	for _, f := range []string{"plan.md", "spec.md", "a.go", "b.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(repo, f), []byte("v1\n"), 0o600))
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	return repo, &runFX{cwd: repo, tools: fakeRunTools{}, plan: "plan.md", spec: "spec.md"}
}

func commitIn(t *testing.T, dir, file, body string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600))
	gitIn(t, dir, "commit", "-q", "-am", "edit "+file)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

func TestRunFX_LaneMakesAWorktreeAtItsBaseAndReusesIt(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	commitIn(t, repo, "a.go", "v2\n")
	dir, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	assert.Equal(t, base, gitIn(t, dir, "rev-parse", "HEAD"))
	again, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	assert.Equal(t, dir, again)

	other := filepath.Join(filepath.Dir(dir), "r-s3")
	require.NoError(t, os.MkdirAll(other, 0o700))
	_, err = fx.Lane("r-s3", base)
	assert.ErrorContains(t, err, "is no worktree of this repository")
}

func TestRunFX_PickPicksInOrderAndTakesAnEarlierPartialPick(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	dir, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	l1 := commitIn(t, dir, "b.go", "lane 1\n")
	l2 := commitIn(t, dir, "b.go", "lane 2\n")
	onto := commitIn(t, repo, "a.go", "main\n")

	gitIn(t, repo, "cherry-pick", l1) // a crash after the first pick
	picked, err := fx.Pick([]string{l1, l2}, onto)
	require.NoError(t, err)
	require.Len(t, picked, 2)
	assert.Equal(t, picked[1], gitIn(t, repo, "rev-parse", "HEAD"))
	assert.Equal(t, "lane 2", gitIn(t, repo, "show", "HEAD:b.go"))

	again, err := fx.Pick([]string{l1, l2}, onto)
	require.NoError(t, err)
	assert.Equal(t, picked, again, "a pick asked again picks nothing twice")
}

func TestRunFX_APickThatConflictsAbortsBackToOnto(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	dir, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	l1 := commitIn(t, dir, "b.go", "lane\n")
	l2 := commitIn(t, dir, "a.go", "lane\n")
	onto := commitIn(t, repo, "a.go", "main\n")
	_, err = fx.Pick([]string{l1, l2}, onto)
	var conflict *flowrun.PickConflict
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, l2, conflict.Commit)
	assert.Equal(t, []string{"a.go"}, conflict.Files)
	assert.Equal(t, onto, gitIn(t, repo, "rev-parse", "HEAD"), "the whole pick is undone")
	assert.False(t, fx.picking())
}

func TestRunFX_PickAbortsAPickACrashLeftInProgress(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	dir, err := fx.Lane("r-s2", base)
	require.NoError(t, err)
	clash := commitIn(t, dir, "a.go", "lane\n")
	gitIn(t, dir, "reset", "-q", "--hard", base) // the clash stays an object, off the lane
	l1 := commitIn(t, dir, "b.go", "lane\n")
	onto := commitIn(t, repo, "a.go", "main\n")
	_, _ = runGit(repo, "cherry-pick", clash) // conflicts, and stays in progress
	require.True(t, fx.picking())
	picked, err := fx.Pick([]string{l1}, onto)
	require.NoError(t, err)
	require.Len(t, picked, 1)
	assert.False(t, fx.picking())
}

func TestRunFX_DropLaneRefusesADirtyLane(t *testing.T) {
	repo, fx := laneRepo(t)
	dir, err := fx.Lane("r-s2", gitIn(t, repo, "rev-parse", "HEAD"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o600))
	assert.Error(t, fx.DropLane(dir))
	assert.DirExists(t, dir)
	require.NoError(t, os.Remove(filepath.Join(dir, "stray.txt")))
	require.NoError(t, fx.DropLane(dir))
	assert.NoDirExists(t, dir)
}

func TestRunFX_OnlyPlanCommits(t *testing.T) {
	repo, fx := laneRepo(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	commitIn(t, repo, "spec.md", "v2\n")
	head := commitIn(t, repo, "plan.md", "v2\n")
	ok, err := fx.OnlyPlanCommits(base, head)
	require.NoError(t, err)
	assert.True(t, ok)
	code := commitIn(t, repo, "a.go", "v2\n")
	ok, err = fx.OnlyPlanCommits(base, code)
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = fx.OnlyPlanCommits(code, base)
	require.NoError(t, err)
	assert.False(t, ok, "a head that doesn't descend from base")
}
```

`runGit` is a raw git call that may fail. Add it beside `gitIn` in `flow_run_test.go` if no such helper exists, and check first with `grep -n "func runGit" internal/cli`:

```go
// runGit runs git args in dir and is its combined output, failing or not.
func runGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	return string(out), err
}
```

Check that `gitIn` sets an identity for commits, as `TestTakenHead` relies on, and if it doesn't, give `runGit` the same treatment it has.

Run: `loom job run -d "cli lane tests" -- sh -c 'CGO_ENABLED=0 go test ./internal/cli/ -run "TestRunFX_"'`
Expected: FAIL to compile, since `fx.Lane`, `fx.Pick`, `fx.DropLane`, `fx.OnlyPlanCommits`, `fx.picking` and `runFX.spec` don't exist yet.

- [ ] **Step 2: Implement the effects**

In `internal/cli/flow_run_fx.go`, add `spec string` to `runFX`, set as `spec: st.Spec` where the engine command builds it in `flow_run.go`. Add `"github.com/aidan-bailey/loom/config"` to the imports if it isn't there. Then:

```go
// Lane is the lane named name for a laned story (parallel pairs spec §5.3):
// a worktree of this repository at <global dir>/flow/lanes/<key>/<name>,
// detached at base, outside .loom/worktrees, where loom's orphan sweep
// looks. A path that is already this repository's worktree is reused, and
// anything else there refused.
func (fx *runFX) Lane(name, base string) (string, error) {
	key, err := fx.tools.Key()
	if err != nil {
		return "", err
	}
	global, err := config.GetGlobalConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(global, "flow", "lanes", key, name)
	switch _, err := os.Stat(dir); {
	case err == nil:
		if !fx.ownWorktree(dir) {
			return "", fmt.Errorf("the lane %s exists and is no worktree of this repository", dir)
		}
		return dir, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", err
	}
	if _, err := fx.git("worktree", "add", "--detach", dir, base); err != nil {
		return "", err
	}
	return dir, nil
}

// ownWorktree reports whether dir is a worktree of the run's repository:
// its own top level, sharing the run's git common dir. git run in a plain
// directory walks up to whatever repository encloses it.
func (fx *runFX) ownWorktree(dir string) bool {
	top, err := fx.gitIn(dir, "rev-parse", "--show-toplevel")
	if err != nil || config.CanonicalPath(top) != config.CanonicalPath(dir) {
		return false
	}
	theirs, err := fx.gitIn(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false
	}
	mine, err := fx.git("rev-parse", "--path-format=absolute", "--git-common-dir")
	return err == nil && config.CanonicalPath(mine) == config.CanonicalPath(theirs)
}

// picking reports whether a cherry-pick is in progress in the run's worktree.
func (fx *runFX) picking() bool {
	if _, err := fx.git("rev-parse", "-q", "--verify", "CHERRY_PICK_HEAD"); err == nil {
		return true
	}
	seq, err := fx.git("rev-parse", "--path-format=absolute", "--git-path", "sequencer")
	if err != nil {
		return false
	}
	_, err = os.Stat(seq)
	return err == nil
}

// Pick cherry-picks commits onto the run's worktree (spec §5.5). A pick a
// crash left in progress is aborted first, which returns HEAD to where that
// pick began; commits already on HEAD past onto are matched to a prefix of
// commits by patch ID and not picked again. A conflict aborts the whole
// pick and is a *flowrun.PickConflict.
func (fx *runFX) Pick(commits []string, onto string) ([]string, error) {
	if fx.picking() {
		if _, err := fx.git("cherry-pick", "--abort"); err != nil {
			return nil, err
		}
	}
	head, err := fx.Head("")
	if err != nil {
		return nil, err
	}
	done, err := fx.pickedSoFar(commits, onto, head)
	if err != nil {
		return nil, err
	}
	if rest := commits[len(done):]; len(rest) > 0 {
		if _, err := fx.git(append([]string{"cherry-pick"}, rest...)...); err != nil {
			if !fx.picking() {
				return nil, err
			}
			stopped, _ := fx.git("rev-parse", "CHERRY_PICK_HEAD")
			files, _ := fx.git("diff", "--name-only", "--diff-filter=U")
			if _, aerr := fx.git("cherry-pick", "--abort"); aerr != nil {
				return nil, fmt.Errorf("%v; and its abort: %v", err, aerr)
			}
			return nil, &flowrun.PickConflict{Commit: stopped, Files: strings.Fields(files)}
		}
	}
	if head, err = fx.Head(""); err != nil {
		return nil, err
	}
	return fx.Commits(onto, head)
}

// pickedSoFar is the commits from onto to head, when they are picks of a
// prefix of commits, matched by patch ID.
func (fx *runFX) pickedSoFar(commits []string, onto, head string) ([]string, error) {
	if head == onto {
		return nil, nil
	}
	if _, err := fx.git("merge-base", "--is-ancestor", onto, head); err != nil {
		return nil, fmt.Errorf("HEAD %s doesn't descend from %s, the head the pick is onto", shortSHA(head), shortSHA(onto))
	}
	have, err := fx.Commits(onto, head)
	if err != nil {
		return nil, err
	}
	if len(have) > len(commits) {
		return nil, fmt.Errorf("HEAD moved %d commits past %s, more than the %d to pick", len(have), shortSHA(onto), len(commits))
	}
	for i, c := range have {
		a, err := fx.patchID(c)
		if err != nil {
			return nil, err
		}
		b, err := fx.patchID(commits[i])
		if err != nil {
			return nil, err
		}
		if a != b {
			return nil, fmt.Errorf("HEAD moved past %s: %s is no pick of %s", shortSHA(onto), shortSHA(c), shortSHA(commits[i]))
		}
	}
	return have, nil
}

// patchID is commit's stable patch ID.
func (fx *runFX) patchID(commit string) (string, error) {
	var diff, out, errOut bytes.Buffer
	show := internalexec.GitCommand(context.Background(), fx.cwd, "diff-tree", "-p", "--no-color", "--no-ext-diff", commit)
	show.Stdout, show.Stderr = &diff, &errOut
	if err := show.Run(); err != nil {
		return "", fmt.Errorf("git diff-tree %s: %v: %s", commit, err, strings.TrimSpace(errOut.String()))
	}
	id := internalexec.GitCommand(context.Background(), fx.cwd, "patch-id", "--stable")
	id.Stdin, id.Stdout, id.Stderr = &diff, &out, &errOut
	if err := id.Run(); err != nil {
		return "", fmt.Errorf("git patch-id: %v: %s", err, strings.TrimSpace(errOut.String()))
	}
	if f := strings.Fields(out.String()); len(f) > 0 {
		return f[0], nil
	}
	return "", nil
}

// DropLane removes the lane at dir with git, which refuses one that isn't clean.
func (fx *runFX) DropLane(dir string) error {
	_, err := fx.git("worktree", "remove", dir)
	return err
}

func (fx *runFX) OnlyPlanCommits(base, head string) (bool, error) {
	return onlyPlanCommits(fx.git, fx.plan, fx.spec, base, head)
}

// onlyPlanCommits reports whether head descends from base and base..head
// touches only plan and spec: the manager's commits that loom flow run
// answer takes, and the engine adopts (spec §5.7).
func onlyPlanCommits(git func(...string) (string, error), plan, spec, base, head string) (bool, error) {
	if base == head {
		return false, nil
	}
	if _, err := git("merge-base", "--is-ancestor", base, head); err != nil {
		return false, nil
	}
	names, err := git("diff", "--name-only", "--no-renames", base, head)
	if err != nil {
		return false, err
	}
	for _, name := range strings.Split(names, "\n") {
		if name != plan && (spec == "" || name != spec) {
			return false, nil
		}
	}
	return true, nil
}
```

In `flow_run.go`, make `takenHead` use the shared check after its clean-tree test:

```go
	if ok, err := onlyPlanCommits(fx.git, st.Plan, st.Spec, at, head); err != nil || !ok {
		return ""
	}
	return head
```

This replaces its own `merge-base` and `diff --name-only` loop. `TestTakenHead` keeps passing unchanged.

- [ ] **Step 3: Show a lane**

In `newFlowRunShowCmd`'s story loop, after `status` is set:

```go
				if r.Lane != "" && status == "developing" {
					status = "developing (lane)"
				}
```

Widen the status column from `%-11s` to `%-17s`. After the loop:

```go
			for _, s := range st.Stories {
				if s.Row.Lane != "" && s.Row.Status == "developing" {
					fmt.Fprintf(w, "lane: Story %s at %s\n", s.ID, s.Row.Lane)
				}
			}
```

While the run is parked, name the agent runs still at work. Read `readAgentRun`'s struct for how a run says its job has ended (a `Status` field, or the job state of `Current()`), and print `still running: <agentRunLine>` for each one in `st.Agents` that hasn't ended. Then update any `show` test whose expected text the wider column changes:

```bash
grep -rn "not started\|developing" internal/cli/flow_run_test.go
```

- [ ] **Step 4: Run the CLI tests**

Run: `loom job run -d "cli flow tests" -- sh -c 'CGO_ENABLED=0 go test ./internal/cli/ -run "TestRunFX_|TestFlowRun|TestTakenHead"'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/cli/flow_run_fx.go internal/cli/flow_run.go internal/cli/flow_run_lane_test.go internal/cli/flow_run_test.go
git add internal/cli/flow_run_fx.go internal/cli/flow_run.go internal/cli/flow_run_lane_test.go internal/cli/flow_run_test.go
git commit -m "feat(cli): lanes, picks and spec-commit adoption for the run engine" -- internal/cli/flow_run_fx.go internal/cli/flow_run.go internal/cli/flow_run_lane_test.go internal/cli/flow_run_test.go
```

---

### Task 6: Docs

**Files:** `flow/plugin/docs/dispatch.md`, `flow/plugin/skills/draft/SKILL.md`, `flow/plugin/skills/run/SKILL.md`, `flow/plugin/docs/records.md`, `internal/flowrun/README.md`, `internal/flowrun/CLAUDE.md`

- [ ] **Step 1: The dispatch guide**

In `### The skeleton`, extend the header item:

```
- the header: writing-plans' title, Goal, Architecture and Tech Stack, the
  `**Spec:**` line, and, when stories may overlap, a `**Parallel:**` line
  ([parallel pairs](#parallel-pairs));
```

Add a section before `## The developers' git guard and turn caps`:

```markdown
### Parallel pairs

A plan may let `/flow:run` overlap two stories: `**Parallel:** 3+4, 7+8`,
one line before the first story. In each pair the second story's developer
runs in a lane, a worktree the engine makes at the first story's base under
`<global dir>/flow/lanes/`, while the first story runs whole in the session's
worktree. When the first is done, the engine picks the lane's commits onto
HEAD and runs the second from its gates on, as any story. Pairs may chain
(`3+4, 4+5`); one lane runs at a time.

The design session declares a pair only when the second story uses nothing
the first creates (no type, function, file or flag the first story's
**Interfaces** exposes) and builds from a fresh checkout: a lane has no
untracked files. `loom flow check-plan` fails a pair that isn't adjacent,
shares a file or holds an exclusive build in both stories, and the seams
check lists what the first story defines that the second uses. A lane left
dirty, or whose commits don't apply, asks the manager `lane`: `retry`
after fixing it by hand, `redo` to build the story again in the session's
worktree, or `stop`. A lane is removed only once its commits are on the
branch.
```

In `## The run engine (\`/flow:run\`)`, change "Stories run one at a time." to:

```
Stories run in plan order; a declared pair's second story develops in a
lane beside its first ([parallel pairs](#parallel-pairs)), and a question
from either parks the run while the other's agent works on. The tree check
takes commits that touch only the spec and the plan, as `answer` does.
```

In `### The seams check`, add to the questions it lists: "for each declared pair, that the second story uses nothing the first creates."

- [ ] **Step 2: /flow:draft and /flow:run**

In `flow/plugin/skills/draft/SKILL.md`:
- Step 3, after the landmine paragraph, add: "A `**Parallel:**` failure is yours, not a drafter's: drop the pair from the skeleton's line (the stories then run in order), and name it in the hand-over."
- Step 4, add a third question for the recon: "For each pair the `**Parallel:**` line declares, does the second story use anything the first creates? The packet's Declared pairs section lists the names; check files, flags and behaviour too."
- Step 5, add: "Drop from the `**Parallel:**` line a pair the seams findings refute, and name it in the hand-over."

In `flow/plugin/skills/run/SKILL.md`, step 3, after the usage-limit sentence, add: "A plan's laned story can ask `lane` when its lane is left dirty or its commits don't apply: `retry` once you have fixed the lane by hand (its path is in the question), `redo` to build the story again in this worktree, or `stop`. With a lane, an agent may still be working in this worktree while you answer: commit only by pathspec."

- [ ] **Step 3: Records, flowrun README and CLAUDE.md**

In `flow/plugin/docs/records.md`, in the stories table's description, add: "a laned story's developer cell ends `(lane)`, and the notes say how many commits were picked onto which head, and name any lane left."

In `internal/flowrun/README.md`, describe tracks: the main track, the lane track, the lock, `join`, `pick` and `retake`.

In `internal/flowrun/CLAUDE.md`, add a rule:

```
- **Run engine code only under the engine's lock, let go of it only in a waiting effect or the join (`wait`), and return at once, touching nothing, when `wait` says the run closed (`errClosed`).** A lane track runs beside the main track, and `Run` returns at the first outcome while a track still waits on a job; a mutation outside the lock races the other track's save, and one after the close writes over a parked run's state. **Enforced** by `TestRun_ALaneQuestionParksWhileItsPartnerWorks`, `TestRun_AMainQuestionParksWhileTheLaneWorks` and `TestRun_APairRestartedAtEverySaveSubmitsNothingTwice` under `-race`.
```

- [ ] **Step 4: Check the docs**

Run: `bash flow/lib/test.sh`, then `loom job run -d "claudemd lint" -- sh -c 'CGO_ENABLED=0 go run ./tools/claudemd -v'`
Expected: OK, and within budget.

- [ ] **Step 5: Commit**

```bash
git add flow/plugin/docs/dispatch.md flow/plugin/skills/draft/SKILL.md flow/plugin/skills/run/SKILL.md flow/plugin/docs/records.md internal/flowrun/README.md internal/flowrun/CLAUDE.md
git commit -m "docs(flow): parallel pairs in the run engine" -- flow/plugin/docs/dispatch.md flow/plugin/skills/draft/SKILL.md flow/plugin/skills/run/SKILL.md flow/plugin/docs/records.md internal/flowrun/README.md internal/flowrun/CLAUDE.md
```

---

### Final gates

- [ ] `loom job submit -d "suite" -- sh -c 'CGO_ENABLED=0 go test ./...'`
- [ ] `loom job submit -d "race" -- sh -c 'CC=clang CGO_ENABLED=1 go test -race ./...'`
- [ ] `loom job submit -d "vet+cross" -- sh -c 'CGO_ENABLED=0 go vet ./... && GOOS=windows CGO_ENABLED=0 go build ./...'`
- [ ] `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')` prints nothing
- [ ] `bash flow/lib/test.sh`

## Execution notes

(Filled in during execution.)

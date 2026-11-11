# Flow: parallel pairs in the run engine

Status: design approved 2026-11-11; awaiting review of this file.

Loom's run engine (`/flow:run`, `internal/flowrun`) runs a plan's stories one at a time. The Opus supervisor (`/flow:supervise`, `flow:plan-execution-supervisor`) could run two at once when `loom flow check-plan` listed them as a parallel-safe pair, and did so in about five of the 67 recorded runs ("Parallel pairs run: 5+7, 6+8 and 10+11. Their commits interleave"). The owner means to remove `/flow:supervise` in favour of `/flow:run` (§9), and wants the pairs to move to the engine first. This spec lets the engine run a story's developer in a second worktree, a *lane*, while the story before it runs in the session's worktree, and pick the lane's commits onto the run's branch when the first story is done.

## 1. Problem

- The engine runs every story in plan order. On a plan whose stories are independent, each developer waits for the whole of the story before it: its developer, gates, reviews, fix rounds and doc pass.
- Removing `/flow:supervise` would remove the only controller that runs stories at once.
- `check-plan`'s pairs are only half the judgement. It pairs stories by disjoint **Files** and at most one exclusive build. It can't see that the second story *uses* an interface the first creates, which the supervisor judged for itself. Records also show plans whose pairs had to run in order anyway: a pre-commit hook that runs the whole suite, a story the plan runs last and alone.

## 2. Decisions

- **Order: this first, then the removal** (owner, 2026-11-11). `/flow:supervise` is removed outright afterwards (owner, 2026-11-11), in its own spec and plan (§9). Rejected: the removal first, which would leave no controller that runs stories at once until this lands; one spec for both, whose two halves share nothing but the motive.
- **Approach 1, the lane developer** (owner, 2026-11-11). For a declared pair (A, B), B's developer phase runs in an engine-owned worktree at A's base, beside A's whole story in the session's worktree. When A is done, B's commits are cherry-picked onto HEAD, and B runs from its gates onward in the session's worktree, as today. Each story's commit range stays contiguous, gates never see a half-edited tree, and the tree check and every brief keep their meaning. Rejected:
  - 2, whole stories overlapping in the shared tree, as the supervisor did: commits interleave, so the tree check, every `base..head` range in the briefs and the conformance check, and the attribution of a gate failure would all have to work by story; gates would wait until no sibling is editing; and a developer straying outside its **Files** would break the attribution.
  - 3, the whole of B in a lane, merged back at the end: twice the gate builds, and two doc passes editing the same README collide at the merge.
- **The plan declares its pairs** (owner, 2026-11-11), in a `**Parallel:**` line the design stage writes, and `check-plan` validates. A plan without the line runs in order. Rejected: pairing every adjacent pair `check-plan` finds safe, unless the plan opts out (the interface judgement no script makes, and the records' plans that had to run in order).
- **A pair is a story and the one right after it, and one lane runs at a time.** B continues in the session's worktree straight after A, so the stories still run in plan order there. Chains are allowed: in `3+4, 4+5`, Story 5's lane starts when Story 4 moves into the session's worktree. Not built: triples, or a lane for a story that doesn't follow its partner.
- **The engine stays one procedure under one lock** (§5.3). The lane is a second *track* of the same engine, running today's `build()` for B. Engine code runs only while it holds the lock, and a track releases it only while it waits on a job. The memo and replay discipline (`internal/flowrun/CLAUDE.md`) holds for each track. Rejected: a second run job per lane (two runs for the manager to answer); turning the engine into an event loop of non-blocking effects (a rewrite).
- **Cherry-pick, not merge.** The engine reads a story's commits as a `base..head` range, and a merge commit would put A's commits in B's range. Picks are recognized on replay by patch ID, so a crash mid-pick neither applies a commit twice nor needs a trailer in the history.
- **A lane lives under loom's global dir**, at `<global dir>/flow/lanes/<key>/<run>-s<id>`. Under `.loom/worktrees`, loom's orphan sweep (`DiscoverOrphans`) would find it, and inside the session's worktree the gates' tools would walk it.
- **A lane is removed only after its commits are on the run's branch**, with `git worktree remove` and never `-f`. A stop, a lost engine or a failed pick keeps it, and the record names it.
- **Plan edits keep the run's pairs** as the run started (`State.Restory`), noted where the plan now differs, as for a story's developer, tier and review.
- **Commits that touch only the spec and the plan are adopted** where the engine checks the tree (§5.7). With a lane, the manager answers a lane question while the session's worktree is mid-story, and the commit it makes with its answer would otherwise park the run as `tree-moved`. These are the commits `loom flow run answer` already takes.
- **A new question kind, `lane`** (§5.6), with the words `retry`, `redo` and `stop`.
- **A probe gates the build** (§8): story 1 of the plan checks that a headless agent whose working dir is a lane resolves `loom job`, `loom flow` and the git guard as it does from the session's worktree. The code can't settle that.

## 3. Scope

In: the `**Parallel:**` line, `check-plan`'s check of it and its `--json` field; the skeleton format and `/flow:draft`'s seams check and handling of a refuted pair; the engine's lane track, lock, join and pick; the `lane` question kind and `redo` word; the tree check's adoption of spec and plan commits; `Story.Lane`, `Row.Lane` and the record's notes; the `Effects` changes and their adapter in `internal/cli`; `loom flow run show`'s lane state; the docs (§7); tests.

Out: removing `/flow:supervise` (§9); triples, or a lane that isn't the next story; a whole story in a lane; lanes for the final round; a profile switch that turns lanes off (a plan without the line is that switch); copying untracked files (`.env`, `node_modules`) into a lane.

## 4. Concepts and names

- **Pair**: two adjacent stories the plan's `**Parallel:**` line names, `A+B`, where B is the story right after A.
- **Lane**: the git worktree the engine makes for B's developer phase, detached at A's base. **Laned story**: B.
- **Track**: a line of engine work with its own head, current story, tree and notes. The **main track** runs today's procedure in the session's worktree; the **lane track** runs a laned story's developer phase in its lane.
- **Join**: the point after A's story where the main track waits for the lane track, checks the lane, picks its commits and removes it.
- **Pick**: cherry-picking the lane's commits, one at a time and in order, onto the session's worktree.

## 5. Behaviour

### 5.1 The plan's line and `check-plan`

The line sits before the first story, beside `**Spec:**`:

    **Parallel:** 3+4, 7+8

Each entry is two story ids joined by `+`, the entries separated by commas. `check-plan` fails the plan when an entry:

- names a story the plan doesn't have;
- isn't adjacent: B must be the story right after A;
- shares a file between the two stories' **Files**;
- has an exclusive build (`build.exclusive` in the profile) in both stories' **Verify**;
- repeats an earlier entry.

A second `**Parallel:**` line fails the plan too. Chains (`3+4, 4+5`) pass. The plain output prints the declared pairs after its "Parallel-safe pairs" line, which stays as the design stage's candidates. `--json` gives each laned story `"lane": true`, and leaves the field out for the rest.

### 5.2 The design stage

- The skeleton may carry the line (the dispatch guide's skeleton section), for pairs whose second story uses nothing the first creates (no type, function, file or flag the first story's **Interfaces** exposes) and whose second story builds from a fresh checkout.
- `/flow:draft`'s seams recon gets one more check: for each declared pair, that the second story uses nothing the first creates. A dependency is a seams finding.
- `/flow:draft` removes from the line a pair `check-plan` fails or the seams check refutes, commits the line with the plan, and tells the owner which pairs it dropped and why. Dropping is safe: the stories then run in order.

### 5.3 The engine: tracks and the lock

- `run()` walks the stories as today. Before `story(A)`, when A+1 is laned, it starts a lane track for A+1. After `story(A)` returns, it joins (§5.5).
- The lane track creates the lane (`Effects.Lane`, memoized under `lane:<id>`) and runs `build()` for B there: escalations, NEEDS_CONTEXT, BLOCKED, failed agents and turn caps behave as they do in the session's worktree. It sets B's row to `developing` with `Row.Lane` true, and never sets `State.Phase`, which stays the main track's.
- Each track has its own head, current story, sent findings and working dir. Shared state (the `State`, its memo, the attempt counters) is touched only under the engine's lock, which a track holds whenever it runs engine code. It releases the lock only while it waits: inside `Agent`, `Resume`, `Gates` and `Services`, and while the main track waits at the join.
- Step names stay deterministic: each track names its steps from its own story's prefix (`<run>-s4-dev1`), so the two tracks never share a counter.
- A lane track's notes go to a buffer on its story, which the main track appends to `State.Notes` at the join, so the notes' order doesn't depend on which track woke first.
- A lane track's tree check compares the lane's HEAD and status with the lane track's head. The main track's compares the session's worktree with the main track's head, as today. Neither sees the other's tree.

### 5.4 Parking and replay

- A question from either track parks the run as today. `ask` saves the state with the question and returns `errParked`, and `Run` returns at once. The run job exits.
- The other track may be waiting on a job. That job keeps running in loom's queue, and the restarted engine follows it by name (`AgentStarted`, then the follow path), as it follows any job a lost engine left. A followed agent skips the tree check, as today.
- One question at a time: once the run has parked, a track that wakes or asks returns `errParked` without touching the state. Its question is asked on replay, after the manager's answer.
- A lane track's question names its story (`Question.Story`). Its `At` is the main track's head, since `loom flow run answer` compares it with HEAD in the session's worktree. Its answer's `took:` head is not applied to either track on replay; §5.7 adopts the manager's commits instead.
- `Run`'s first terminal error from either track wins: a park, a stop (`errStopped` ends the run `stopped`), or an error it can't get past (the state stays running, for `loom flow run resume`). A goroutine still waiting when `Run` returns finds the engine closed when it wakes, and returns without touching the state.
- A restart replays from the top: it starts the lane track again at the same point, whose memoized steps return at once.

### 5.5 The join

After A's story, the main track:

1. Waits for the lane track's developer phase to end, releasing the lock.
2. Reads the lane's HEAD (memoized as `lane-head:<id>:<attempt>`) and checks the lane is clean (`Effects.Clean` on the lane). A dirty lane asks `lane` (§5.6).
3. Adopts any spec and plan commits in the session's worktree (§5.7), then picks the lane's commits (`<lane base>..<lane head>`) onto HEAD with `Effects.Pick`, memoized as `pick:<id>:<attempt>`. A conflict aborts the pick (`git cherry-pick --abort`) and asks `lane`.
4. Removes the lane (`Effects.DropLane`). A removal that fails is a note and leaves the lane, whose commits are already on the branch.
5. Runs B from its gates onward, with B's base the head before the pick, so B's range holds exactly its picked commits and its later fixes. B's row keeps `Lane` and records its picked commits as its commits.

When B+1 is laned too, its lane track starts as B enters the session's worktree, from the head after the pick.

### 5.6 The `lane` question

Asked when the lane is dirty at the join, or its pick conflicted. The text names the lane's path, its head, and the dirty paths or the conflicting commit and files. It takes:

- `retry`: the manager fixed the lane by hand (committed or removed the leftovers, or rewrote the lane's commits so they apply). The engine reads the lane's head again, checks it and picks again, under the next attempt's keys.
- `redo`: B's developer runs again in the session's worktree, from the head after A, as the next attempt of B's developer prefix, with the lane developer's report among its earlier reports and a line saying its lane attempt didn't apply. The lane is kept and the record names it.
- `stop`: the run ends `stopped`, and the lane is kept.

`redo` is a new word. `lane` takes no text decision.

### 5.7 Adopting spec and plan commits

`checkTree`, and the join before its pick, adopt a moved HEAD when the tree is clean and every commit from the track's head to HEAD touches only the run's spec and plan: the commits `loom flow run answer` takes. They memoize the adopted head (`headAt`, as today's `tree:<name>`) and note it. Any other move parks as `tree-moved`, as today. This applies to runs without lanes too. A spec or plan commit was never something the engine couldn't run past.

### 5.8 Plan edits mid-run

`State.Restory` keeps each story's `Lane` as the run started. Where the plan's line now pairs differently, it says so in what it returns, as it does for a story's developer, tier and review. A run started before this change has no `Lane` set anywhere and runs in order.

### 5.9 Show, the board and the record

- `loom flow run show` gives a laned story's row as `developing (lane)` while its developer works in the lane, and names the lane's path. While the run is parked, it names the jobs still running.
- The flow board reads the rows as they are. A run with a lane shows two stories in progress.
- The record's notes say, for each laned story, that its developer ran in a lane beside its partner, how many commits were picked and onto which head, and the path of any lane left. A row's `Lane` adds `lane` to its developer cell.

## 6. Failure handling

- **The lane can't be created** (`git worktree add` fails, a full disk): an error the engine can't get past. The state stays running, and the cause is the engine's last log line, which `loom flow run show` and `wait` print.
- **A lane path exists from an earlier pass**: `Effects.Lane` reuses it when it is a worktree of this repository, whatever its HEAD (the lane developer's commits). Otherwise it refuses with an error.
- **A crash mid-pick**: the replay calls `Pick` again. It matches each commit already on HEAD past the step's starting head to the lane's commits by patch ID, and picks only the rest. A cherry-pick left in progress is aborted only when HEAD is the step's starting head, since the abort then discards only the engine's own partial pick. Any other state is an error.
- **The session's worktree moved while the lane worked**: `Pick` refuses when HEAD isn't the main track's head after §5.7's adoption, and the join asks `tree-moved`.
- **A lane developer strays outside B's Files**: a pick that still applies is fine; B's gates and review run on the picked commits. One that conflicts asks `lane`.
- **Kill or stop**: killing the manager's session ends the run, as today, and a stopped run keeps its lane. Nothing removes a lane whose commits aren't on the branch.

## 7. Components

| Where | Change |
|---|---|
| `flow/lib/check_plan.py`, its tests | parse and check `**Parallel:**`; `lane` in `--json`; print the declared pairs |
| `internal/flowrun/state.go` | `Story.Lane`, `Row.Lane`; `Restory` keeps the run's pairs |
| `internal/flowrun/engine.go` | tracks, the lock, the lane track, the join, `lane` asking, §5.7's adoption |
| `internal/flowrun/answers.go` | `KindLane`, `WordRedo` |
| `internal/flowrun/record.go`, `records.go` | the lane notes and cell |
| `internal/flowrun` `Effects` | a working dir for `Agent`, `Head` and `Clean`; new `Lane`, `Pick`, `DropLane` and `OnlyPlanCommits` |
| `internal/cli/flow_run_fx.go` | the adapter, on `internalexec.GitCommand` |
| `internal/cli/flow_run.go` | `show`'s lane state |
| `flow/plugin/docs/dispatch.md` | the skeleton's line; the seams check's pair check; the run engine section's lanes |
| `flow/plugin/skills/draft/SKILL.md` | the dropped-pair step |
| `flow/plugin/skills/run/SKILL.md` | the `lane` question; an agent may be working in the tree while the manager answers, so commit only by pathspec |
| `flow/plugin/docs/records.md` | the lane notes |
| `internal/flowrun/README.md`, `internal/flowrun/CLAUDE.md` | the tracks, and a rule: engine code runs only under the lock, released only in a waiting effect or the join |

## 8. Testing

- **Probe (story 1).** A real-tmux, opt-in test on a private server. It runs a headless flow agent with its working dir in a lane of a temp repo, and checks that its `loom job run`, `loom flow conf state` and a guarded git call resolve and behave as they do from the session's worktree. A failure stops the plan for a spec amendment.
- **`check_plan_test.py`**: each failing entry kind in §5.1, a chain, `--json`'s field, and a plan without the line.
- **Engine, on fake effects**:
  - a pair runs its lane developer while the first story runs, picks, and gates the laned story over exactly its picked commits;
  - a lane question parks the run while the main track's agent is in flight, and the restart follows that agent;
  - a main question parks while the lane's agent is in flight, and likewise;
  - a dirty lane and a conflicting pick, answered `retry`, `redo` and `stop`;
  - a chain;
  - a plan without the line runs as today;
  - a spec-only commit is adopted, and a code commit still parks as `tree-moved`.
- **`TestRun_ARestartAtEverySavePointSubmitsNothingTwice`**, extended to a paired plan, crashing at every save of either track.
- **Real git**: `Lane`, `Pick` and `DropLane` on a temp repo, covering a crash after each pick, a cherry-pick left in progress, a conflict, and a dirty lane that `DropLane` refuses.
- **Race**: the engine's tests under `-race`, which CI runs.

## 9. Known limits and later work

- **Removing `/flow:supervise`** comes next, in its own spec. Scope agreed 2026-11-11:
  - remove the skill and `flow:plan-execution-supervisor`, with its `flow/PROVENANCE.md` row, `lib/check-agents.sh`'s spawning list and the dispatch guide's supervisor-only sections;
  - point writing-plans' option 3 (loom's context), `/flow:draft`'s last line, `/flow:init`, the docs, `USAGE.md` and the plugin's manifest at `/flow:run`;
  - keep the profile's `supervisor` role block, which the engine reads, and flowview's readers of earlier supervised runs.
- **A cold build in each lane**: cheap for Go, which shares `GOCACHE`; a Rust lane gets a new target dir. The plan's line is the design stage's call on whether that pays.
- **A lane starts from a fresh checkout**: untracked files the session's worktree has (`.env`, `node_modules`) aren't there. §5.2's rule keeps such stories out of pairs.
- **The lane developer reads the spec and plan at the lane's base**: an answer committed mid-run reaches it only through the answer text its resume carries.
- **A downgrade mid-run** to a binary without lanes would replay a laned story's memoized developer as if it ran in the session's worktree. Not handled.

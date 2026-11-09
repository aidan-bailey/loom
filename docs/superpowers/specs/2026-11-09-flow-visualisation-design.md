# Loom PWA: a flow's design, spec, progress, story states and costs on the phone

**Date:** 2026-11-09
**Status:** Approved design. Brainstormed with the owner. The board's layout was
chosen in the visual companion (option A, the pipeline board); the story
detail, document viewer and Flows screen were approved there as sketched.

## Problem

A flow runs from a design session (brainstorm, spec, skeleton) through
`/flow:draft` (drafters write the stories) to a run (`/flow:run`'s engine or
`/flow:supervise`'s supervisor), and ends in a record under
`~/.loom/flow/records/`. Away from the desk the owner sees none of it: the
phone app shows the sessions, their conversations, diffs and jobs, but not
which stage a flow is at, which story the run is on, what each story's steps
did, what the run has cost so far or what it will cost. Today
`loom flow run show` prints a run's rows as text in a terminal, and the cost
exists only after the run, in the record.

This spec adds a **flow** tab to a session's views in the app (the pipeline
board, a story's detail, the flow's documents), a chip on the session's fleet
row, and a **Flows** screen for the host's live and past flows. The screens
show; they never act. The owner acts where they do today, in the manager
session's conversation.

## Findings

Read from the code at 2c995bc9:

- **Engine runs keep structured state.** `<git-dir>/flow/runs/<id>.json` is
  `flowrun.State` (schema 1): `Status` (`running`, `parked`, `done`,
  `stopped`, `cancelled`), `Phase`, `Plan`, `Spec`, `Base`, `Start`, `End`,
  `Managers` (the manager sessions' ids), `Stories` (each with `Developer`,
  `Tier`, `Review` and a `Row`: `Status` in `developing`, `gating`,
  `reviewing`, `fixing`, `done`, plus `Commits`, `Findings` [C, I, M],
  `Deviations`, `TurnCap`), `Agents` and `Gates` (every agent and gate run's
  name, in dispatch and start order), `Question` (`Kind`, `Story`, `Text`,
  `Paths`) and `Answers`. `flowrun.Load` refuses another schema. The state
  dir is the profile's `paths.state`, else `<git-dir>/flow`, one per worktree
  (`flowconf.state_dir`).
- **Step names are deterministic.** `engine.next(prefix)` names an attempt
  `<run>-<prefix><k>`. The prefixes: `premise`, `s<id>-dev`, `s<id>-gates`,
  `s<id>-review`, `s<id>-fix` (story ids through `storyPrefix`),
  `final-gates`, `final-review`, `final-fix`.
- **Agent and gate runs have records.** `agents/<name>.json` is
  `internal/cli`'s `agentRun` (`Role`, `Status`, `Session`, `Jobs`,
  `CostUSD`, `Turns`, `Question`, `Why`); its report is
  `agents/<name>/report.md`. `gates/<name>.json` is `gateRun` (`Kind`,
  `Commits`, `Steps` with each step's `Line`, `Jobs`, `Verdict`, `Why`).
  `agentRun.Session` is set only when the agent's job has ended
  (`readResult`), although the job's `output.log` names the session in its
  first `system`/`init` line.
- **The daemon sees the jobs, not the flow.** A job submitted with
  `LOOM_FLOW_RUN` naming a `KindRun` job records that run in `Record.Run`
  (`core/queue_ops.go`). So every agent and gate step's start and end is a
  job state change the model already handles, tied to its run and owner.
- **Supervised runs keep prose.** `/flow:supervise` gives its supervisor the
  state file `<state dir>/agent-reports/supervisor-<plan stem>.md`, which
  holds a **Phase** line and story rows in the supervisor's own words.
- **Drafting leaves files.** `/flow:draft` keeps a plan's drafts in
  `D = <paths.drafts, else .flow/drafts>/<plan stem>/`: `D/S<id>.md` per
  story and `D/STATE.md` with a **Phase** line. When it commits the plan it
  writes the plan's `**Design stage:**` line, which carries the draft
  session's window and logged cost.
- **Records.** `~/.loom/flow/records/<key>/<YYYY-MM-DD>-<stem>.md`, `<key>`
  from `loom flow conf key`. The header's `- Plan:` line carries the story
  count, `(N stories)`; the cost section ends with an
  `<!-- agent-costs {…} -->` comment whose `total.usd` is the run's priced
  total.
- **One pricer.** `flow/lib/agent_costs.py` prices transcripts at API list
  prices (`flow/lib/prices.toml`), with cache tiers and estimated thinking.
  `run_report` prices a run's manager sessions as `main` and each agent run
  by its role, finding transcripts under `$CLAUDE_CONFIG_DIR/projects`. On a
  subscription the figures are relative weights, not a bill.
- **The app's patterns.** The mockup tab is the model for a lookup the model
  runs on a job (`core/mockup.go`: `lookForMockup`, `applyMockup`, and
  `ReadMockup` answered by `Reply`). Read routes go through `l.read`. The
  fleet row's meta already carries the jobs badge (`s.jobs_label`).

## Decisions

| Question | Decision |
|---|---|
| Surface | The PWA first. The TUI is out of scope. |
| What "design" and "specification" mean | A lifecycle strip (design, spec, draft, run, record), where each stage opens its documents. |
| Which runs get story rows | Engine runs, live. A supervised run shows as a stage with its elapsed time and its state file's **Phase** line, and fills in from its record once it ends. |
| Costs | Live spend by story and role, the whole flow's total, a projection, and a comparison with past runs. |
| Entry points | A **flow** tab and a fleet chip per session, plus a host-wide **Flows** screen. |
| Actions | None. The question box links to the manager session's conversation. |
| Data path | The daemon reads the flow files on a job when something it sees changes (approach A). The engine doesn't report, and the web layer reads no files. |
| Layout | The pipeline board (option A). |

## Design

### 1. Flows and their stages

A **flow** is a plan file, or a spec on its way to one. In a session's
worktree, the flows are:

1. each plan file in the branch's diff against the session's base that holds
   the `plan_join` marker line or a `**Spec:**` line;
2. each plan that a run state in the state dir's `runs/` names;
3. each spec in the branch's diff that no plan of 1 or 2 names: a file named
   `*-design.md` under a directory named `specs` (the brainstorming skill's
   `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`). It is a flow in
   design, keyed by the spec's path until a plan names it.

The branch's diff keeps a worktree cut from `main` to its own flows: the
hundreds of older plans aren't in it. A plan named by a run state counts even
when it isn't in the diff.

Each stage is read from what that stage leaves behind, never inferred from
another:

| Stage | Done when | In progress when | Shows |
|---|---|---|---|
| design | the plan's `**Spec:**` file and the plan are committed | the flow is a spec in design (rule 3 above), or its plan is uncommitted | the spec's title; the design cost (§3) |
| spec | the plan's `**Spec:**` file exists | — | opens the spec and the skeleton (the plan's text above the `plan_join` marker) |
| draft | the plan has a `**Design stage:**` line | the plan's drafts dir `D` exists | drafted *n* of *m* (`D/S<id>.md` files among the skeleton's story list), `D/STATE.md`'s **Phase** line, the line's cost once done |
| run (engine) | the run state's `Status` is an end (`flowrun.Ended`) | a run state names the plan | the board (§5) |
| run (supervised) | the record exists | `agent-reports/supervisor-<stem>.md` exists and no run state names the plan | `updated <age> ago` from the file's mtime, and its **Phase** line verbatim; no story rows |
| record | a file `<records>/<key>/<YYYY-MM-DD>-<s>.md` exists, `<s>` being the plan's stem without its leading date (the newest when several do) | — | opens the record |

`<key>` is the repo key `loom flow conf key` prints: the main checkout's
resolved path (the parent of git's common dir) with every character outside
`[A-Za-z0-9]` made `-` (`flowconf.repo_key`). flowview ports that rule, and
its test pins the Python's own examples.

When a plan has more than one run state, the newest by `Start` is the run
shown, and the others are listed under it by id and status.

A stage whose file can't be read or decoded shows `unreadable: <why>` and
no stage after it is derived from that file. A run state of another schema
says `schema N, this loom reads 1`.

### 2. The reader (`internal/flowview`)

A new package that reads files and returns plain values. It holds no model
and no UI, and starts no process except git, through
`internalexec.GitCommand`.

```go
// Find returns the flows in worktree whose branch diff is taken against base.
func Find(worktree, base string) ([]Summary, error)

// Detail returns one flow's stages, stories, steps, question and the parts
// of its costs that need no pricer run.
func Detail(worktree, plan string) (FlowDetail, error)

// History returns the past runs the records dir holds, per repo key.
func History(recordsDir string) ([]PastRun, error)
```

- **`Summary`**: `Plan` (the spec's path for a flow in design), `Stage` (the
  furthest stage begun), `Status` (the stage's state: `running`, `parked`,
  `done`, `stopped`, `cancelled`, `drafting`, `designing`; core turns
  `running` into `lost` when the run's engine job has ended or left the
  queue, which only the model can see), `Done` and `Total` stories,
  `Asks` (the open question's kind, or ""), `SpentUSD` (the last priced
  figure, §3, or absent), `Unreadable` (a reason, or "").
- **`FlowDetail`**: the stage table's rows; the run's `ID`, `Status`,
  `Phase`, `Start`, `End`, `Engine`; its stories, each with its row, its
  steps and its cells (§5); the premises row and the final row; the open
  question; the other runs of the plan. The manager is the session whose
  worktree holds the flow, so the board's question box links to its own
  session.
- **Steps** are the run's `Agents` and `Gates` names, joined with their
  records and placed by `flowrun.StepOf`. An agent step carries its role,
  status, start and end (from its jobs), cost (§3) and whether a report
  exists. A gate step carries each gate line's verdict and the job of its
  latest generation, so the app opens the log through the existing job-log
  route.
- **`PastRun`**: the repo key, the record's file name, the plan's stem, its
  story count, `Ended` (or `supervised` for a record without the `Ended:`
  line), `End`, `total.usd` from the `agent-costs` comment (summed over a
  record's several comments, as `costs records` does), or `Unreadable`.

**Shared record types.** `agentRun`, `agentJob`, `gateRun` and `gateStep` move
from `internal/cli` into `internal/flowrun` as `AgentRecord`, `AgentJob`,
`GateRecord` and `GateStep`, with `LoadAgent(dir, name)` and
`LoadGate(dir, name)`. The CLI writes them and the reader decodes them with
one type. The CLI's rules about them stay in `internal/cli`.

**`flowrun.StepOf`.** It sits beside `next` and `storyPrefix`:

```go
// StepOf parses an attempt's name, <run>-<prefix><k>, into the story it
// belongs to ("" for premise and final steps), its kind (premise, dev,
// gates, review, fix) and k. ok is false for a name of another run or a
// prefix next doesn't make.
func StepOf(run, name string, stories []Story) (story, kind string, final bool, k int, ok bool)
```

It takes the stories so a story id holding `-` (`storyPrefix` keeps `-`) is
matched against the real ids, longest first, never split on the first `-`.

**A running agent's session.** An agent run's record gets its `Session`
only when its job ends, and the engine's follower (`runFX.Agent`) blocks in
`jobWithin` until then, so the record isn't the place to learn it early.
The model knows each job's `LogPath`: for an agent run whose record has no
`Session` and whose current job runs, core reads that `output.log`'s
`system`/`init` line (the first line of the stream, the same field
`readResult` takes) and hands the pricer the pair. Nothing about the agent
records' writers changes.

### 3. Costs

All figures come from `agent_costs.py`, so the live board and the record
agree. They are API list prices, and the cost box says "list prices" once.

- **`loom flow costs run <id> --json`** (a new flag): one object with the
  run, its window, a row per agent run (`name`, `role`, `model`, `usd`,
  `usd_incl_est`, `running`) and per manager session (`name`, `session`,
  `usd`, `usd_incl_est`), and the totals. It takes `--session <agent name>=<session
  id>` (repeatable) for agent runs whose record has no `Session` yet (§2). An
  agent run that isn't ended is priced up to now and marked `running`; one
  with no session from either is listed with `usd: null`.
- **`loom flow costs design <spec> --json`** (new): the transcripts in the
  worktree's project dir (`$CLAUDE_CONFIG_DIR/projects/<the top level's path
  with every non-alphanumeric as ->`) that hold a Write or Edit tool use
  whose `file_path` is the spec, each priced from its first request up to
  the commit that added the plan (`git log --diff-filter=A` on the plan), or
  up to now when the plan isn't committed. It prints the sessions and the
  total.
- **The draft's cost** is read from the plan's `**Design stage:**` line (its
  `$… logged` figure).
- **The whole flow** is design + draft + run, each part named, with a part
  that has no figure shown as "not priced" and left out of the sum, which
  then says "of 3 parts" with the count it has.

**Where the pricer runs.** The model runs the `loom` binary's own
`flow costs …` (as `os.Executable`) on a job goroutine, in the worktree,
with the session's account's `CLAUDE_CONFIG_DIR` in its environment, so it
finds that account's transcripts. The pricing is a few hundred milliseconds
of transcript reading, so it isn't queued as a work-queue job.

**Caching.** The model keeps each flow's last priced output with its key:
the run id plus the set of agent runs and which of them have ended. It
prices again only when the key changes, and at most every 30 s while any
agent runs. A design figure, once its plan is committed, is kept until the
session ends. `Summary.SpentUSD` is the run's last priced total.

**By story and role.** flowview sums the run's rows by `StepOf`'s story (the
premises and final rows have their own) and by role; the manager sessions
are their own role, `manager`.

**Projection** (`~` always):

- *spent*: the run's priced total;
- *per story*: the mean of the finished stories' sums in this run when one
  or more has finished, else the median of the repo's past runs'
  `total.usd / stories`, else none;
- *final share*: the median over the repo's past engine runs of
  (final-row USD / total USD), when two or more past records give the
  final rows, else 0.15. A record's final rows are the rows of its
  `### Cost by agent` table whose Agent cell holds `-final-`, their USD read
  from the column headed `USD`;
- projected = (spent + per story × stories not finished) / (1 − final share)
  while the final round hasn't started; once it has, the larger of spent and
  (spent − final spent) / (1 − final share). With no *per story*, there's no
  projection, and the box says so.

**Comparison.** The repo's last five past runs as `total.usd / stories`, in
end order, their median, and this run's figure so far
(spent / finished stories, once one has finished).

### 4. The model (core)

- **The look.** `lookForFlows(worktree, base)` calls `flowview.Find` on a
  job's goroutine and `applyFlows` stores the result on the instance; the
  instance exposes it as `InstanceView.Flows []FlowSummary`. `FlowSummary`
  is `flowview.Summary` with the spend.
- **What only the model knows**, added to the reader's values on the loop:
  `lost` for a `running` run whose `Engine` job has ended or left the queue
  (as `engineLost` judges it for `loom flow run show`), and each running
  agent job's `LogPath`, from which the pricing job reads the session id
  (§2).
- **Triggers.** A look runs for an instance:
  1. when a job it owns with `Kind == KindRun`, or with `Run` set, changes
     state;
  2. on a Stop hook event in its session;
  3. once after a restore.
  At most one look is in flight per instance. A trigger during a look sets
  a dirty bit, and one more look runs when it ends.
- **Reads**, each a `Core` method answered by `Reply`, by the adding-a-Core
  method guide:

| Core method | Answers |
|---|---|
| `ReadFlow(id InstanceID, plan string, req ReqID)` | `flowview.Detail` with the costs (§3), pricing first when the cache's key is stale |
| `ReadFlows(req ReqID)` | every instance's `FlowSummary`s with their instance ids, and `flowview.History(<global dir>/flow/records)` |
| `ReadFlowDoc(id InstanceID, plan, doc string, req ReqID)` | the text of one of the flow's documents |

  `doc` is one of `spec`, `skeleton`, `plan`, `record`, `drafts-state`, or
  `report:<agent name>` for an agent run of the flow's runs. Any other value
  is refused, and the path each names is built by the reader, never taken
  from the request. A document over 1 MiB is cut there and says so.
- `rpc.Protocol` stays: these are added methods, and `InstanceView` gains a
  field.

### 5. The phone

**Routes** (added to `parseRoute`):

| Route | Module | Screen |
|---|---|---|
| `#s/<id>/flow` | `flow.mjs` | the board; with two or more flows, a plan picker at its top, newest first |
| `#s/<id>/flow/<story>` | `flow.mjs` | the story detail; `P` and `F` name the premises and final rows |
| `#s/<id>/flow/doc/<doc>` | `flowdoc.mjs` | a document as `buildTree`'s nodes, opened at the section `?at=` names, with a section index |
| `#flows` | `flows.mjs` | the host's flows |

The plan rides in each route's `?plan=` when the session has more than one
flow, and is otherwise the only one.

**Web routes** (each through `authed`, each a read through `l.read`):
`GET /api/v1/sessions/{id}/flow?plan=`, `GET /api/v1/flows`,
`GET /api/v1/sessions/{id}/flow/doc?plan=&doc=`. The doc route answers
`{"blocks": …}` from `buildTree`, never raw markdown.

**The tab and the chip.** `SessionTabs` gets a **flow** tab when `s.flows`
is non-empty. The fleet row's meta gets a chip beside the jobs badge:

- during a run: `flow 2/5 · <status>` and the spend (`· $4.71`) when priced;
  `asks` in place of the status while parked, the chip amber;
- before a run: `flow · design`, `flow · draft 3/4`;
- `lost` in the error colour.

The fleet's header gets a **flows** link to `#flows`.

**The board** (option A), top to bottom:

1. the stage strip: design, spec, draft, run (`run 2/5`), record, each ✓,
   current or to come; a tap opens the stage's document;
2. the question box while parked: `parked · <kind> · story <id>`, the text,
   and **open manager ›** to `#s/<id>`, the session's own conversation (its
   Claude is the run's manager);
3. the stories box: a row per story (and `P`, `F` rows when they exist):
   its id, its cells, and on the right its commits and spend, or its tier
   and review while not started. A tap opens the story;
4. the cost box: run spent, flow total, projection; a bar by role with its
   legend.

**Cells.** One cell per step kind in a story's order: dev, gate, rev, fix.
The premises row has one cell, and the final row has gate, rev and fix.

| Cell | When |
|---|---|
| grey | no attempt of the kind yet |
| blue | its latest attempt runs |
| green | its latest attempt passed (gates) or reported done (agents) |
| red | its latest attempt failed, with no later pass |
| amber `?` | the open question's story is this row and its key names this kind |
| `—` | the story's row is `done` (or past this kind) without an attempt of it |

**The story detail**: its developer, tier, review, commits, elapsed time and
spend; its steps as a timeline (each with its role, outcome, time and cost;
a tap opens the agent's report through the doc route, or the gate's job
log); its findings (the counts, and the Minors kept for the final round);
and its reads: its plan range (`?at=` its line), the spec sections its
**Reading** line names, and its commits (to the changes tab).

**The Flows screen**: *live*, every session's flows with their chip and a
tap to the board; *past*, by repo key, each record with its stories, total,
end and how it ended, a tap opening it; and the comparison of §3 as bars
(the last five runs, the median, this run's figure in the accent colour).

**Updates.** The board and the story detail fetch on open, and again
whenever the stream's snapshot changes that session's `FlowSummary`. Elapsed
times tick on the page's own 30 s clock, as the fleet's ages do. The Flows
screen fetches on open and on pull-to-refresh.

## Failures

- A file that can't be read or decoded: its stage, row or step says
  `unreadable: <why>`; nothing after it is derived from it.
- The pricer fails: the cost box says `cost unavailable: <its first stderr
  line>` and shows no figure. It never falls back to the agents' own
  `CostUSD`, which is another pricer.
- No projection basis: no projection, and the box says why.
- A run state that says `running` while its engine job has ended or left the
  queue: the run's status is `lost`, as `loom flow run show` says it.
- A record that doesn't decode: listed as `unreadable` on the Flows screen,
  never skipped.
- A read route whose `Reply` doesn't come within `replyWait`: 504, as the
  other reads.

## Testing

- **flowview.** Fixtures are written by the engine itself: `flowrun`'s fake
  `Effects` drives a run, and at every save point the reader's `Detail` is
  asserted against the state. Plus: each row of the stage table; a run state
  of another schema refused; two runs of one plan; a supervised run's
  **Phase** line; a draft's *n* of *m*; the projection and the comparison
  against fixed records; a story id holding `-`.
- **`StepOf`.** Round-trips every name `next` makes for each prefix, and
  refuses another run's name and an unknown prefix.
- **The shared record types.** The CLI's existing agent and gate tests pass
  on the moved types unchanged.
- **A running agent's session.** core reads the session id from a running
  agent job's `output.log` init line and passes it to the pricer; a log
  without the line passes nothing, and the agent is listed unpriced.
- **The pricer.** Python tests for `costs run --json` (a running agent priced
  to now, one with no session listed with `null`) and `costs design` (only
  the transcripts that wrote the spec, cut at the plan's commit).
- **core.** A `Run`-marked job's state change triggers a look; a trigger
  during a look runs exactly one more; a `running` run whose engine job has
  ended reads `lost`, and one whose engine runs doesn't; the summary lands
  on `InstanceView`;
  each `Read…` answers by `Reply`; `ReadFlowDoc` refuses every `doc` but the
  listed ones. Both sides of each trigger's guard are pinned: a job without
  `Run` and of another kind triggers nothing.
- **web.** The three routes through `webStack`, their JSON, the doc route's
  tree, and a 504 on a missing `Reply`.
- **The app.** `node --check` on the new modules; the webshot fixture at the
  iPhone 15 size for the board (running, parked, lost), a story, a document
  and the Flows screen.
- Every test that reaches `config` isolates the loom dirs
  (`internal/testenv.IsolateLoomDirs`); none reads the real `~/.loom`.

## Docs

- `internal/flowview/CLAUDE.md` and `README.md` (a new package needs its
  CLAUDE.md, by the coverage rule).
- `internal/flowrun/README.md`: the record types and `StepOf`.
- `internal/web/README.md`: the routes, the modules, by
  `docs/claude/web-layer.md`'s checklist.
- `flow/README.md` and `flow/plugin/docs/records.md`: `costs run --json` and
  `costs design`.
- `docs/specs/protocol.md`, regenerated.
- `USAGE.md`: the flow tab, the chip and the Flows screen.

## Out of scope

- A TUI view of flows.
- Any action on these screens: answering, cancelling, resuming.
- Story rows for supervised runs while they run.
- Push notifications for flow events. A question that reaches the owner
  already pushes through the manager session's stop verdict.
- More than one host.

## Work packages

1. **The reader.** `internal/flowview` (`Find`, `Detail`, `History`, the
   repo key), `flowrun.StepOf`, the shared record types. Go only, no daemon.
2. **Costs.** `costs run --json`, `costs design`, and flowview's sums by story
   and role, the projection and the comparison.
3. **The daemon.** The look and its triggers, `lost`, a running agent's
   session from its job log, the pricer's runs and cache, `FlowSummary` on
   `InstanceView`, `ReadFlow`, `ReadFlows`, `ReadFlowDoc`, the rpc
   regeneration and the web routes.
4. **The phone.** The tab, the chip, the board, the story detail, the
   document viewer and the Flows screen, with their webshots.

## Planning amendments

Decided while planning and building (plan `docs/superpowers/plans/2026-11-09-flow-visualisation.md`):

1. The moved record types are `flowrun.AgentRecord`, `AgentRecordJob`, `GateRecord` and `GateRecordStep` (`flowrun` already had a `GateStep`); `internal/cli` keeps its names as aliases, and the gate run's CLI-only methods became functions (`openJobs`, `planSteps`, `gateKey`).
2. flowview's tests write their fixtures through `flowrun.Save`, `SaveAgent` and `SaveGate`; the engine's own names are parsed in `flowrun` (`TestStepOf_ParsesEveryNameTheEngineSubmits`), where its fake effects live.
3. A step's start and end come from the queue's records (`FlowView.Times`), for the jobs the queue still holds; an agent record holds no times.
4. flowview reads `paths.state` and `paths.drafts` itself (`profilePaths`): `loom flow conf state` and `drafts` make the directories they print. It runs `git status` with `--no-optional-locks`, so a look never refreshes the index.
5. The branch's files are `git log --first-parent --no-merges --name-only <base>..HEAD` and `git status`: a merge of main brings in none of main's plans.
6. A document's tree is built by `buildTreeLimit` (1 MiB, 40,000 nodes, headings with their source line); a message's stays at `buildTree`'s 64 KiB.
7. `Find` and `Detail` take the records dir; `Summary` carries the run's `Engine` job, which `lost` is judged by.
8. A spec in design stops being a flow once any plan on the branch mentions it, flow's or not: a design carried on without flow leaves no chip behind.
9. The draft stage is done for a plan with joined stories and no drafts dir ("written by hand"), and a flow's status before a run is `designing`, `designed`, `drafting` or `drafted`.
10. `ReadFlow` prices the design session too (`loom flow costs design`), cached once the design stage is done.

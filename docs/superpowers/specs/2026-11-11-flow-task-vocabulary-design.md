# Flow: Story, Task, Subtask

Status: approved by the owner 2026-11-11, then pinned the same day before planning: spec splitting comes first.

The flow calls a plan's unit of developer work a story (`## Story 3`), and the TDD cycles inside it tasks (`### Task 3.1`). This spec renames them: what one plan delivers is a **Story**, made of **Tasks** (today's stories), each made of **Subtasks** (today's tasks). It lands in three stages, readers first, so that a session whose plugin copy predates the change keeps working against a new `loom` binary. It prepares spec splitting, a later spec, in which a spec too big for one Story splits into several Stories that together form an **Epic**.

## 1. Why

- **The names sit one level too low.** In agile use a story is a shippable increment, which is what a flow plan delivers: one spec (or one part of one), drafted once, run once, merged green. Today's `## Story N` is an implementation slice, one developer and one review, often invisible to a user; that is a task.
- **Splitting needs the level above.** The 2026-11-11 `/flow:draft` run that prompted it drafted 18 stories averaging 105 KB each, each holding what earlier plans split into 5 to 7. Splitting such a spec into several plans needs a name for each plan (a Story) and for the whole (an Epic), and the splitting spec is written once, in the final vocabulary, if the rename lands first.

## 2. Decisions

All by the owner, 2026-11-11.

- **Epic → Story → Task → Subtask**, with the checkbox steps inside a subtask unchanged (`- [ ] **Step 1: …**`). Rejected: Subtask for today's story with Step for today's task, because "step" already names a run-engine job (`flowrun.Step`: build, review, fix, docs) and a gate command (about 450 uses across the engine, the flow view, the CLI, the PWA and the verifier); "Part" for today's task.
- **"Plan" stays the document's name.** A Story has a spec and a plan; `/flow:draft <plan>`, `loom flow check-plan`, `docs/superpowers/plans/` and superpowers:writing-plans keep their names.
- **Every layer is renamed**: text, the plan format, tool output, flags, records, the PWA, Go and Python identifiers, JSON keys and the daemon's wire. Every reader accepts the old forms as well, forever. Rejected: renaming only what people see (loom's own code would keep saying story for a task); a one-time migration with new-only readers (plans committed in other repos would need converting before they ran again).
- **Three stages, readers first** (approach A). Rejected: one branch with everything (about 2,500 edits landing together, a reader's mistake breaking running sessions at the merge); text first (writers switching before every reader knows the new forms).
- **Spec splitting is a separate spec, after this one**, and defines the Epic. This spec names no Epic in any format.

## 3. The names and formats

| What | Today | New |
|---|---|---|
| Skeleton list heading | `## Stories` | `## Tasks` |
| Skeleton entry | `- **Story 3** — title.` | `- **Task 3** — title.` |
| Contract owner, proposal | `Story 3`, `(Story 3 proposes)` | `Task 3`, `(Task 3 proposes)` |
| Plan section | `## Story 3: title` | `## Task 3: title` |
| Inside it | `### Task 3.1: name` | `### Subtask 3.1: name` |
| Checkbox steps | `- [ ] **Step 1: …**` | unchanged |
| Profile key | `[gates] story = […]` | `[gates] task = […]` |
| Flags | `loom flow gates start --story`, `conformance --story`, `premises --stories` | `--task`, `--task`, `--tasks` |
| `check-plan --json`, run state, `join.json` | `"stories"`, `"story"` | `"tasks"`, `"task"` |
| Drafts | `D/S3.md`, `D/questions/S3.md`, `D/findings/S3.md` | `D/T3.md`, and the same under `questions/` and `findings/` |
| The vouched-story reviewer | `flow:story-reviewer` | `flow:task-reviewer` |
| Records, tool output, PWA labels | "Story 3", "story 2 documenting", "story gates" | "Task 3", "task 2 documenting", "task gates" |

Unchanged: ids and their forms (`3`, `3.1`, `2b`); the `**Parallel:**` and `**Plan review:**` lines, which hold only ids; the word "plan"; the history documents under `docs/superpowers/`.

## 4. Compatibility

1. **Every reader accepts both forms, forever.** Old plans committed in other repos, the 69 run records in `~/.loom/flow/records/`, the run state of runs in flight, and profiles keep working unedited.
2. **Every writer writes only the new form**, from stage 2 on.
3. **The binary talks to a script copy in the oldest form.** Each session's plugin and scripts are a copy written at its launch, while the `loom` binary is shared and may be newer. So the binary keeps sending old flag names to scripts (`flowconf.py gates --story`), every script keeps accepting them, and the binary parses both forms of every script's output (`check-plan --json`, premises' head lines).
4. **A conflict fails closed** (§8). A mixed file, some sections in each form, is not a conflict.

## 5. Stage 1: every reader accepts both forms

Nothing visible changes; this stage merges before any writer switches.

- **`flow/lib`.**
  - `check_plan.py`, `plan_premises.py`, `plan_conformance.py`, `plan_packet.py`: `## Story N` and `## Task N` are both a task heading; any `###` inside a task is a subtask, as any `###` is a task today.
  - `plan_join.py`, `plan_skeleton.py`: `## Stories` or `## Tasks`; entries `**Story <id>**` or `**Task <id>**`; contract owners and `(… N proposes)` with either word; a draft named `S<id>.md` or `T<id>.md`; `join.json` with either key.
  - `flowconf.py`: `gates.story` or `gates.task` (both set: a doctor FAIL); `gates --story` or `--task`.
  - Flags: `premises --stories|--tasks`, `conformance --story|--task`.
  - `agent_costs.py`: record tables headed "Story" or "Task", and `story-reviewer` and `task-reviewer` counted as one role.
- **Go.**
  - `internal/flowrun`: `check-plan --json` with `"stories"` or `"tasks"`; premises head lines starting "Story" or "Task" (`premiseHeadRE`); run state with either key and either phase word ("story 2", "task 2"), so a run in flight survives a daemon upgrade.
  - `internal/flowview`: both heading and entry patterns, both list headings, both run-state keys and phase words.
  - `internal/cli`: `loom flow gates start` takes `--task` beside `--story`; run state JSON with either key.
  - The engine dispatches `task-reviewer` when the session's copy holds it, else `story-reviewer`.

## 6. Stage 2: the writers switch

- **Prompts.** The draft, run, retro and init skills, every agent definition, the habits skill and the dispatch guide use the new words. The drafter's format becomes `## Task <id>: <title>` with `### Subtask <id>.1: <name>`; `/flow:draft` writes `D/T<id>.md`. The dispatch guide gains a short vocabulary: a Story is what one plan delivers, made of Tasks, each made of Subtasks. The retro skill says once that records written before the change say "Story" for a task and "Task" for a subtask. `session/loom_context_flow.md` says the run engine runs a plan task by task.
- **`flow:story-reviewer` becomes `flow:task-reviewer`**: the file, its name and its description, and the PWA's role map (`lib/guarded-agents.txt` lists only editing agents, so not this one). `flow/PROVENANCE.md` moves the agent's history under the new heading with a line recording the rename (no pin changes).
- **Tool output.** `check-plan`'s lines ("Task 3 (line n)"), the join's and the skeleton lint's messages, premises' heads, the seams packet's "the Task 3 entry" and its section names, and "task gates" in `flowconf.py`. `check-plan --json` and `join.json` write `"tasks"`.
- **The engine's output.** Records (the "Task" column and lines), phase strings ("task 2 documenting"), briefs and questions.
- **The CLI.** `--task` and `--tasks` become the documented flags; the old ones stay as hidden aliases. `/flow:init` writes `[gates] task`; `loom flow conf doctor` WARNs, without failing, on `gates.story`, naming `gates.task`.
- **The PWA's labels.** Its JSON keys wait for stage 3.
- **Docs.** `flow/README.md`, `docs/ARCHITECTURE.md`, `USAGE.md`, and the CLAUDE.md files that use the word for the flow's unit.

The binary keeps sending old flags to scripts (§4.3).

## 7. Stage 3: the code's own names and the wire

Nothing a user or an agent reads changes.

- **Go.** `flowrun.Story` becomes `Task`, `State.Stories` becomes `Tasks`, and the patterns, functions and fields across `internal/flowrun`, `internal/flowview` and `internal/cli` follow. Run state writes `"tasks"`.
- **The wire.** `Flow.Detail.Stories` (`internal/flowview/detail.go`'s `Stories []StoryView`) becomes `Tasks []TaskView`, so `rpc.Protocol` goes from 2 to 3; `docs/specs/protocol.md` is regenerated (`go test ./core/rpc -run TestProtocolReference -update`); the PWA's `internal/web/static/js/flow.mjs` reads the new keys, served by the same daemon. A TUI or CLI of another build gets the existing mismatch handling.
- **Python.** The `Story` dataclass becomes `Task`; `STORY_RE` becomes the task-heading pattern, and today's `TASK_RE` (any `###`) becomes `SUBTASK_RE`; `story_json` and the rest follow.
- **Docs that name identifiers**: the CLAUDE.md files and READMEs the claudemd lint checks for symbols.

The old-form readers and the binary's old flags to scripts stay.

## 8. Failure handling

Each conflict fails with one sentence naming both forms; no reader guesses or merges:

- a task id given twice, once in each form (two headings, or two entries);
- `S3.md` and `T3.md` side by side in D: "S3.md and T3.md both draft Task 3: keep one";
- a profile holding `gates.story` and `gates.task`: a doctor FAIL;
- JSON holding both `"stories"` and `"tasks"`.

An unknown heading word is not a task, as today.

## 9. Testing

- **Stage 1.** Each reader's tests pin both forms and each conflict of §8, the doctor's FAIL on both gate keys included. Go tests feed `flowrun` and `flowview` the output an old script copy writes (`check-plan --json` with `"stories"`, premises heads starting "Story", run state with `"stories"`): a new binary with a frozen copy. A run state written in the old form and read by the new code: a run in flight across an upgrade. One old-form plan and one new-form plan, otherwise identical, run through `check-plan`, the join, the skeleton lint, premises and the seams packet, and give the same results apart from the names. The engine picks `task-reviewer` or `story-reviewer` by what a copy holds.
- **Stage 2.** The golden outputs change to the new words; the agent lint (`lib/check-agents.sh`) and `TestLoomContext_NamesRealSessionFlags` check the prompts and flags; a test pins that the binary still sends `--story` to `flowconf.py`; the doctor WARNs on `gates.story` alone.
- **Stage 3.** `TestProtocolReference`, `TestGenerated_IsFresh`, the race run (`CC=clang CGO_ENABLED=1 go test -race ./...`), and `go test -tags e2e ./e2e/...`, since the wire changes.
- **Every stage.** `bash flow/lib/test.sh`, `CGO_ENABLED=0 go test ./...`, `go run ./tools/claudemd`. At the end of stage 3, a grep of `flow/` and `internal/` for "story" finds only old-form compatibility, history, and Story in its new sense.

## 10. Components

| Stage | Files |
|---|---|
| 1 | `flow/lib/check_plan.py`, `plan_join.py`, `plan_skeleton.py`, `plan_premises.py`, `plan_conformance.py`, `plan_packet.py`, `flowconf.py`, `agent_costs.py` and their tests; `internal/flowrun` (`parse.go`, `state.go`, `engine.go`), `internal/flowview/plan.go` and `find.go`, `internal/cli/flow_gates.go` and `flow_run.go`, and their tests |
| 2 | `flow/plugin/**` (skills, agents with `story-reviewer.md` renamed, `docs/dispatch.md`, `docs/records.md`), the writers among stage 1's Python and Go files, `internal/web/static/**` labels, `session/loom_context_flow.md`, `flow/README.md`, `flow/PROVENANCE.md`, `docs/ARCHITECTURE.md`, `USAGE.md`, CLAUDE.md files |
| 3 | Go and Python identifiers in `flow/lib`, `internal/flowrun`, `internal/flowview`, `internal/cli` (the flow view's wire fields are `internal/flowview/detail.go`'s), `core/rpc/rpc.go` (`Protocol`), `docs/specs/protocol.md` (generated), `internal/web/static/js/flow.mjs`, CLAUDE.md and README symbol references |

## 11. Known limits

- In old records and old prose, "Task 3.1" means a subtask; readers tell the levels apart by heading depth and by the id's shape, a reader of prose by the id's shape alone.
- The binary sends old flag names to scripts for as long as a session can hold an old copy; dropping them would need a check of the copy's version, which nothing does today.
- A plan in the old form keeps working forever; nothing converts it.

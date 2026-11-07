# internal/flowrun

The run engine of flow in loom part 3b (spec `docs/superpowers/specs/2026-11-06-flow-in-loom-run-engine-design.md`). It replaces the Opus `plan-execution-supervisor` subagent's mechanical work with Go, and sends every judgement to the manager as a question.

## The model

- **The run state** (`State`) lives at `<state>/runs/<id>.json`, written whole by `Save`. It holds the run's inputs (the plan, the spec, the base commit, the profile's blocks, the stories from `check_plan.py --json`), the memo, the open question, the answers and the derived rows.
- **Replay.** `Run` runs the whole procedure every time an engine starts:
  - Each step has a deterministic name (`<run>-s3-dev2`, `<run>-s3-gates1`), and its result is memoized under a key (`agent:<name>`, `gates:<name>`, `head:<key>`, `q:<key>`).
  - A memoized step returns at once. A step whose agent run or gate run exists by name is followed, not submitted.
  - So a restart at any point submits nothing twice.
- **Parking.** A judgement is a `Question` with a kind (`needs-context`, `blocked-decision`, `design-finding`, `premise-design`, `unresolved-findings`, `gate-failure`, `agent-failed`, `tree-moved`). `Run` saves it and returns. `State.Answer` memoizes the answer as `q:<key>`, and the next pass reads it. The question records the head the engine had (`At`); an answer that carries the manager's own commits to the spec and plan memoizes that HEAD as `took:<key>`, and the engine takes it as its head, so its tree check doesn't park on them. An `agent-failed` question takes a decision as well as `retry` or `stop`: `failed` returns it, and the next attempt's brief carries it (`withEarlier`).
- **Plan edits.** A story's `Line` and `End` are its plan range, read by the briefs only. When the manager's answer carries an edited plan, `loom flow run answer` re-reads check-plan and `State.Restory` moves each story's range, `Verify`, `Interfaces` and `Landmines` by ID; the developer, tier and review stay as the run started, since they choose the roles a replay follows, and a changed set or order of stories is refused. `BaseLine` and `BaseEnd` keep the range at the base, which premise flags are numbered by (`storyAtLine`).
- **Effects.** `Effects` covers agent runs (3a's `loom flow agent`), gate runs (part 2's `loom flow gates`), the profile's services, git and flow's scripts. `Agent`, `Resume` and `Gates` return once the run they start or follow has ended.

## The procedure

1. The premises: flagged lines go to `premise-check`; COSMETIC verdicts become corrections, DESIGN and UNSURE ones a question.
2. Each story in plan order:
   1. The developer, escalated as the spec's §5.3 says.
   2. The gates, with a fix after a failure.
   3. The review, by its column.
   4. A fix round and re-review for blocking findings.
   5. One question for any PLAN lines.
3. The final gates, the anti-seam review, the final fix round grouped by file, and its re-review.
4. `Record` writes the run's record from the state and the cost section.

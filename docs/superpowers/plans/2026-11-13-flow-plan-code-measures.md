# Flow plan-code measures and stale-copy warning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `loom flow costs records` prints the plan review's three judging figures per engine run; the Design stage line records the review's cost and the draft session's flow copy; a session whose flow copy isn't the installed loom's is warned.

**Architecture:** A new `plan_measures.py` reads one run record's text into figures and renders a table; `agent_costs.py records` appends it. `loom flow` (Go, always the installed binary) passes its roster ID to scripts, `doctor.py` compares it with the copy's, and `loom flow run start` warns directly.

**Tech Stack:** Python 3 unittest, Go (testify), Markdown prompts.

**Spec:** `docs/superpowers/specs/2026-11-13-flow-plan-code-measures-design.md`. Inline execution; heavy commands through `loom job run`; commit by pathspec.

---

### Task 1: `plan_measures.py` and the records table

**Files:** Create `flow/lib/plan_measures.py`, `flow/lib/plan_measures_test.py`; modify `flow/lib/agent_costs.py` (`records_report`), `flow/lib/agent_costs_test.py`.

- [ ] **Step 1: Write the failing tests** — `flow/lib/plan_measures_test.py`:

```python
"""Tests for plan_measures.py. Run from lib/: python3 -m unittest -q plan_measures_test.py"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plan_measures  # noqa: E402

HEADER = ("| Agent | Type | Model | Effort | Requests | Input | Output | Cache read | Write 5m | Write 1h | Over 100K "
          "| USD | Est. thinking | USD incl. est. |\n|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")


def agent(name, usd):
    return f"| {name} | intermediate-dev | claude-sonnet-5-5 | high | 1 | 1 | 1 | 1 | 0 | 0 | 0 | ${usd} | 0 | ${usd + 1} |\n"


STAGE = ("- Design stage: /flow:draft session 7e7a8520-e18b-42d8-9b20-6eb9f39a2788, 2026-11-12T07:57:50Z to "
         "2026-11-12T09:07:19Z UTC: $20.00 logged, $20.50 with estimated thinking; 10,000 plan lines, $2.00 per "
         "1,000 lines; plan review $6.00; loom flow roster aaaaaaaaaaaa.\n")
RECORD = (
    "# Run: r\n\n- Plan: p.md (3 stories)\n- Definitions: loom flow roster bbbbbbbbbbbb → loom flow roster cccccccccccc\n"
    + STAGE
    + "- Ended: done\n- Engine: loom flow run (flow in loom part 3b)\n\n## Cost\n\n### Cost by agent\n\n" + HEADER
    + agent("r-s1-dev1", 2) + agent("r-s1-review1", 1) + agent("r-s1-fix1", 3) + agent("r-s1-review2", 4)
    + agent("r-s2-fix1", 5) + agent("r-s3-fix1", 7)
    + "\n## Session 2\n\n### Cost by agent\n\n" + HEADER + agent("r-s3-review2", 8)
    + "\n## Stories\n\n| Story | Developer | Tier | Review | Commits |\n|---|---|---|---|---|\n"
    "| 1 | intermediate-dev | deep | vouched | 2 |\n| 2 | junior-dev | light | full | 1 |\n| 3 | intermediate-dev | deep | full | 4 |\n"
    "\n## Notes\n\n"
    "- Asked (unresolved-findings, Story 1): Reviews of Story 1 found problems in the plan's own code: - a\n"
    "- Asked (design-finding, Story 2): A review of Story 2 found a design-level issue\n"
    "- Asked (unresolved-findings, Story 3): Reviews of Story 3 found problems in the plan's own code: - b - c\n"
    "- Asked (unresolved-findings, Story 3): After a fix round, a re-review of Story 3 still finds Critical issues\n"
)


class MeasureTest(unittest.TestCase):
    def test_an_engine_record_gives_each_figure(self):
        m = plan_measures.measure("r", RECORD, 40.0)
        self.assertEqual(m.draft_copy, "aaaaaaaaaaaa")
        self.assertEqual(m.run_copy, "cccccccccccc", "the last copy of a run that changed copies")
        self.assertEqual(m.plan_lines, 10000)
        self.assertEqual(m.questions, 2, "only the plan's-own-code questions")
        self.assertEqual(m.draft_usd, 20.0)
        self.assertEqual(m.run_usd, 40.0)
        # Story 1: fix1 3 + review2 4; Story 3: fix1 7 + review2 8 (the second session's table); never Story 2's.
        self.assertEqual(m.fix_usd, 22.0)
        self.assertEqual((m.stories, m.vouched), (3, 1))
        self.assertEqual(m.review_usd, 6.0)

    def test_a_design_stage_line_without_the_clauses_gives_none(self):
        text = RECORD.replace("; plan review $6.00; loom flow roster aaaaaaaaaaaa.", ".")
        m = plan_measures.measure("r", text, 40.0)
        self.assertIsNone(m.review_usd)
        self.assertIsNone(m.draft_copy)
        self.assertEqual(m.plan_lines, 10000)

    def test_a_record_without_a_design_stage_line_has_no_plan_lines(self):
        m = plan_measures.measure("r", RECORD.replace(STAGE, ""), 40.0)
        self.assertIsNone(m.plan_lines)
        self.assertIsNone(m.draft_usd)

    def test_a_record_the_engine_didnt_write_gives_nothing(self):
        self.assertIsNone(plan_measures.measure("r", RECORD.replace("- Engine: loom flow run", "- Engine: other"), 1.0))


class TableTest(unittest.TestCase):
    def test_a_row_per_run_and_a_pooled_row(self):
        a = plan_measures.measure("a", RECORD, 40.0)
        b = plan_measures.measure("b", RECORD.replace(STAGE, ""), 10.0)
        out = plan_measures.table([a, b])
        self.assertIn("## Plan code", out)
        self.assertIn("| a | aaaaaaaaaaaa | cccccccccccc | 10,000 | 2 | 0.20 | $20.00 | $40.00 | $6.00 | $22.00 | 3 | 1 | $6.00 | $2.00 |", out)
        self.assertIn("| b | n/a | cccccccccccc | n/a | 2 | n/a | n/a | $10.00 | n/a | $22.00 | 3 | 1 | n/a | n/a |", out)
        # Pooled: the per-1,000 figures only over a (the run with plan lines); questions and dollars over both.
        self.assertIn("| Pooled (2 runs) | | | 10,000 | 4 | 0.20 | $20.00 | $50.00 | $6.00 | $44.00 | 6 | 2 | $6.00 | $2.00 |", out)


if __name__ == "__main__":
    unittest.main()
```

In `agent_costs_test.py`, class `AgentCostsTest`, add:

```python
    def test_records_add_the_plan_code_table_for_an_engine_record(self):
        _, out, _ = run("session", str(self.main), "--since", "2026-10-13T09:30:00Z")
        plain = Path(self.tmp.name, "2026-10-13-plain.md")
        plain.write_text("# Run\n\n" + out)
        code, report, err = run("records", str(plain))
        self.assertEqual(code, 0, err)
        self.assertNotIn("## Plan code", report)
        engine = Path(self.tmp.name, "2026-10-14-engine.md")
        engine.write_text("# Run\n\n- Engine: loom flow run (flow in loom part 3b)\n\n" + out)
        code, report, err = run("records", str(engine))
        self.assertEqual(code, 0, err)
        self.assertIn("## Plan code", report)
        self.assertIn("| 2026-10-14-engine | n/a | n/a | n/a | 0 |", report)
```

- [ ] **Step 2: Run them to see them fail** — `loom job run -d "measures tests (red)" -- sh -c 'cd flow/lib && python3 -m unittest -q plan_measures_test agent_costs_test'`. Expected: `ModuleNotFoundError: plan_measures`.

- [ ] **Step 3: Implement** `flow/lib/plan_measures.py`:

```python
#!/usr/bin/env python3
"""The plan-code figures of loom flow run records, for `agent_costs.py records`.

A record the run engine wrote (`- Engine: loom flow run`) gives, per run, from its text alone:
the draft session's flow copy and the run's (the `loom flow roster <hex>` of its Design stage line,
and the last of its Definitions line); the plan's lines, the drafting's logged USD and the plan
review's USD (`<n> plan lines`, `$<n> logged` and `plan review $<n>` in the Design stage line); its
plan-code questions (`- Asked (unresolved-findings, Story N): Reviews of Story N found problems in
the plan's own code`); what the fix rounds and re-reviews of those stories cost (every Cost by agent
row named `…-s<N>-fix<k>`, or `…-s<N>-review<k>` with k >= 2, its USD column); and its stories and
how many its Stories table marks vouched.

The table gives each run and the pooled figures the plan review's provenance entry judges: plan-code
questions per 1,000 plan lines, drafting plus run USD per 1,000 plan lines and the review's USD per
story. A run without a figure shows n/a; each pooled ratio uses only the runs with both its figures.
Logged USD throughout, the Design stage line's basis.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

ENGINE_RE = re.compile(r"^- Engine: loom flow run\b", re.M)
STAGE_RE = re.compile(r"^- Design stage: (.*)$", re.M)
DEFINITIONS_RE = re.compile(r"^- Definitions: (.*)$", re.M)
ROSTER_RE = re.compile(r"loom flow roster ([0-9a-f]{12})")
LOGGED_RE = re.compile(r"\$([0-9][0-9,]*(?:\.[0-9]+)?) logged")
PLAN_LINES_RE = re.compile(r"([0-9][0-9,]*) plan lines")
REVIEW_RE = re.compile(r"plan review \$([0-9][0-9,]*(?:\.[0-9]+)?)")
ASKED_RE = re.compile(
    r"^- Asked \(unresolved-findings, Story ([^)]+)\): Reviews of Story \S+ found problems in the plan's own code", re.M
)
AGENT_RE = re.compile(r"-s([^-\s]+)-(fix|review)(\d+)$")


@dataclass
class RunMeasures:
    """One engine run's figures; None where its record doesn't say."""

    name: str
    draft_copy: str | None
    run_copy: str | None
    plan_lines: int | None
    questions: int
    draft_usd: float | None
    run_usd: float
    fix_usd: float
    stories: int
    vouched: int
    review_usd: float | None


def number(text: str) -> float:
    return float(text.replace(",", "").lstrip("$"))


def cells(line: str) -> list[str] | None:
    """A markdown table row's cells, None for a line that is no row."""
    line = line.strip()
    if not line.startswith("|"):
        return None
    return [cell.strip() for cell in line.strip("|").split("|")]


def table_rows(text: str, first: str) -> list[dict[str, str]]:
    """Every row, as {header: cell}, of every markdown table whose header's first cell is `first`."""
    lines = text.splitlines()
    rows: list[dict[str, str]] = []
    for i, line in enumerate(lines):
        header = cells(line)
        if not header or header[0] != first:
            continue
        for row in lines[i + 2:]:
            values = cells(row)
            if values is None:
                break
            rows.append(dict(zip(header, values)))
    return rows


def found(pattern: re.Pattern[str], text: str) -> str | None:
    match = pattern.search(text)
    return match.group(1) if match else None


def measure(name: str, text: str, run_usd: float) -> RunMeasures | None:
    """One engine run's figures from its record's text, None for a record the run engine didn't write."""
    if not ENGINE_RE.search(text):
        return None
    stage = found(STAGE_RE, text) or ""
    definitions = found(DEFINITIONS_RE, text) or ""
    run_copies = ROSTER_RE.findall(definitions)
    asked = ASKED_RE.findall(text)
    fix_usd = 0.0
    for row in table_rows(text, "Agent"):
        named = AGENT_RE.search(row.get("Agent", ""))
        if not named or named.group(1) not in asked:
            continue
        if named.group(2) == "fix" or int(named.group(3)) >= 2:
            try:
                fix_usd += number(row.get("USD", "0"))
            except ValueError:
                pass
    stories = table_rows(text, "Story")
    lines, logged, review = found(PLAN_LINES_RE, stage), found(LOGGED_RE, stage), found(REVIEW_RE, stage)
    return RunMeasures(
        name=name,
        draft_copy=found(ROSTER_RE, stage),
        run_copy=run_copies[-1] if run_copies else None,
        plan_lines=int(number(lines)) if lines else None,
        questions=len(asked),
        draft_usd=number(logged) if logged else None,
        run_usd=run_usd,
        fix_usd=fix_usd,
        stories=len(stories),
        vouched=sum(1 for row in stories if row.get("Review") == "vouched"),
        review_usd=number(review) if review else None,
    )


def usd(value: float | None) -> str:
    return "n/a" if value is None else f"${value:,.2f}"


def per_thousand(amount: float | None, lines: int | None) -> float | None:
    return None if amount is None or not lines else amount * 1000 / lines


def row(name: str, draft_copy: str, run_copy: str, lines: int | None, questions: int, draft: float | None,
        run: float, review: float | None, fix: float, stories: int, vouched: int) -> str:
    rate = per_thousand(questions, lines)
    spend = per_thousand(None if draft is None else draft + run, lines)
    each = None if review is None or not stories else review / stories
    return (f"| {name} | {draft_copy} | {run_copy} | {'n/a' if lines is None else f'{lines:,}'} | {questions} | "
            f"{'n/a' if rate is None else f'{rate:.2f}'} | {usd(draft)} | {usd(run)} | {usd(spend)} | {usd(fix)} | "
            f"{stories} | {vouched} | {usd(review)} | {usd(each)} |")


def table(runs: list[RunMeasures]) -> str:
    """The Plan code section: a row per run, then the pooled row."""
    lines = [
        "## Plan code", "",
        "Engine runs only. Plan-code questions are the run's \"found problems in the plan's own code\" questions, "
        "and fix + re-review is what the fix rounds and re-reviews of their stories cost. Logged USD; a pooled "
        "ratio uses only the runs with both its figures.", "",
        "| Run | Draft copy | Run copy | Plan lines | Plan-code questions | per 1,000 lines | Drafting | Run "
        "| Drafting + run per 1,000 lines | Fix + re-review on them | Stories | Vouched | Plan review | per story |",
        "|---|---|---|" + "---:|" * 11,
    ]
    for r in runs:
        lines.append(row(r.name, r.draft_copy or "n/a", r.run_copy or "n/a", r.plan_lines, r.questions, r.draft_usd,
                         r.run_usd, r.review_usd, r.fix_usd, r.stories, r.vouched))
    sized = [r for r in runs if r.plan_lines]
    priced = [r for r in sized if r.draft_usd is not None]
    reviewed = [r for r in runs if r.review_usd is not None]
    pooled_lines = sum(r.plan_lines or 0 for r in sized) or None
    rate = per_thousand(sum(r.questions for r in sized), pooled_lines)
    priced_lines = sum(r.plan_lines or 0 for r in priced) or None
    spend = per_thousand(sum((r.draft_usd or 0) + r.run_usd for r in priced), priced_lines) if priced else None
    review_stories = sum(r.stories for r in reviewed)
    each = sum(r.review_usd or 0 for r in reviewed) / review_stories if reviewed and review_stories else None
    drafted = [r.draft_usd for r in runs if r.draft_usd is not None]
    lines.append(
        f"| Pooled ({len(runs)} run{'' if len(runs) == 1 else 's'}) | | | "
        f"{'n/a' if pooled_lines is None else f'{pooled_lines:,}'} | {sum(r.questions for r in runs)} | "
        f"{'n/a' if rate is None else f'{rate:.2f}'} | {usd(sum(drafted) if drafted else None)} | "
        f"{usd(sum(r.run_usd for r in runs))} | {usd(spend)} | {usd(sum(r.fix_usd for r in runs))} | "
        f"{sum(r.stories for r in runs)} | {sum(r.vouched for r in runs)} | "
        f"{usd(sum(r.review_usd or 0 for r in reviewed) if reviewed else None)} | {usd(each)} |"
    )
    return "\n".join(lines)
```

The pooled questions-per-1,000 counts only the questions of runs with plan lines; the pooled questions column counts every run's.

In `agent_costs.py`, import it after `flowconf` (`import plan_measures  # noqa: E402`) and change `records_report`'s loop and returns:

```python
    runs: list[tuple[str, dict]] = []
    measures: list[plan_measures.RunMeasures] = []
    for arg in paths:
        text = Path(arg).read_text()
        comments = [json.loads(m.group(1)) for m in COMMENT_RE.finditer(text)]
        if comments:
            data = merge_sessions(comments)
            runs.append((arg, data))
            measured = plan_measures.measure(Path(arg).stem, text, data["total"]["usd"])
            if measured is not None:
                measures.append(measured)
        else:
            print(f"WARN {arg}: no agent-costs comment", file=sys.stderr)
    if not runs:
        return "agent_costs: no run records with an agent-costs comment", 1
    plan_code = "\n\n" + plan_measures.table(measures) if measures else ""
    groups: dict[Path, list[tuple[str, dict]]] = {}
    for path, data in runs:
        groups.setdefault(Path(path).resolve().parent, []).append((path, data))
    if len(groups) == 1:
        return runs_table(runs) + plan_code, 0
    ...
    sections.append(f"## Pooled\n\n{runs_table(runs)}")
    return "\n\n".join(sections) + plan_code, 0
```

- [ ] **Step 4: Run the tests** — `loom job run -d "measures tests" -- sh -c 'cd flow/lib && python3 -m unittest -q plan_measures_test agent_costs_test'`. Expected: OK.

- [ ] **Step 5: Commit** — `git add -- flow/lib/plan_measures.py flow/lib/plan_measures_test.py`, then `git commit -m "feat(flow): costs records prints each engine run's plan-code figures" -- flow/lib/plan_measures.py flow/lib/plan_measures_test.py flow/lib/agent_costs.py flow/lib/agent_costs_test.py`.

### Task 2: the stale-copy warning

**Files:** modify `internal/cli/flow.go`, `internal/cli/flow_run.go`, `internal/cli/flow_test.go`, `flow/lib/doctor.py`, `flow/lib/doctor_test.py`.

- [ ] **Step 1: Failing tests.** In `internal/cli/flow_test.go`:

```go
func TestFlowTool_PassesTheInstalledRoster(t *testing.T) {
	needPython(t)
	t.Setenv(flow.EnvRoot, fakeFlowRoot(t, map[string]string{"check_plan.py": "import os\nprint(os.environ['FLOW_INSTALLED_ROSTER'])\n"}))
	out, _, err := runFlow(t, "check-plan")
	require.NoError(t, err)
	assert.Equal(t, flow.RosterID()+"\n", out)
}

func TestCopyWarning_NamesBothCopiesOnlyWhenTheyDiffer(t *testing.T) {
	assert.Empty(t, copyWarning(flow.RosterID()), "this binary's copy")
	assert.Empty(t, copyWarning(""), "no copy to compare")
	w := copyWarning("loom flow roster 0123456789ab")
	assert.True(t, strings.HasPrefix(w, "WARN flow copy: this session runs loom flow roster 0123456789ab, the installed loom has "+flow.RosterID()+":"), w)
}
```

In `flow/lib/doctor_test.py` (with `from unittest import mock`):

```python
class CopyLineTest(unittest.TestCase):
    def test_no_line_without_the_installed_roster(self):
        with mock.patch.dict(os.environ, {"FLOW_ROSTER": "loom flow roster aaaaaaaaaaaa"}, clear=False):
            os.environ.pop("FLOW_INSTALLED_ROSTER", None)
            self.assertIsNone(doctor.copy_line())

    def test_pass_when_the_copies_match(self):
        same = {"FLOW_ROSTER": "loom flow roster aaaaaaaaaaaa", "FLOW_INSTALLED_ROSTER": "loom flow roster aaaaaaaaaaaa"}
        with mock.patch.dict(os.environ, same):
            self.assertEqual(doctor.copy_line(), "PASS flow copy")

    def test_warn_naming_both_when_they_differ(self):
        differ = {"FLOW_ROSTER": "loom flow roster aaaaaaaaaaaa", "FLOW_INSTALLED_ROSTER": "loom flow roster bbbbbbbbbbbb"}
        with mock.patch.dict(os.environ, differ):
            line = doctor.copy_line()
        self.assertTrue(line.startswith("WARN flow copy: this session runs loom flow roster aaaaaaaaaaaa, "
                                        "the installed loom has loom flow roster bbbbbbbbbbbb:"), line)
```

- [ ] **Step 2: Implement.** In `internal/cli/flow.go`:

```go
// copyWarning is the WARN line for a session whose flow copy (its root's
// ROSTER) isn't this binary's, "" when it is or there is none: a session
// keeps the copy it launched with, so a prompt or tool changed since never
// reaches it, and the daemon writes each launch's copy from its own build.
func copyWarning(copy string) string {
	installed := flow.RosterID()
	if copy == "" || copy == installed {
		return ""
	}
	return "WARN flow copy: this session runs " + copy + ", the installed loom has " + installed +
		": its prompts and tools are the ones it launched with; relaunch it (pause, then resume) for the " +
		"daemon's current copy, once the daemon runs the installed build"
}
```

In `internal/cli/flow_conf.go`'s `flowScript` environment, after `"FLOW_ROSTER="+…`: `"FLOW_INSTALLED_ROSTER="+flow.RosterID()`. In `flow_run.go`'s start, after reading `roster`: `if w := copyWarning(strings.TrimSpace(string(roster))); w != "" { fmt.Fprintln(c.ErrOrStderr(), w) }`.

In `flow/lib/doctor.py`:

```python
def copy_line() -> str | None:
    """PASS or WARN for this session's flow copy against the installed loom's; None when an older loom
    passes no FLOW_INSTALLED_ROSTER. The words match `loom flow run start`'s (`copyWarning`)."""
    installed = os.environ.get("FLOW_INSTALLED_ROSTER", "").strip()
    copy = os.environ.get("FLOW_ROSTER", "").strip()
    if not installed or not copy:
        return None
    if copy == installed:
        return "PASS flow copy"
    return (f"WARN flow copy: this session runs {copy}, the installed loom has {installed}: its prompts and tools "
            "are the ones it launched with; relaunch it (pause, then resume) for the daemon's current copy, once "
            "the daemon runs the installed build")
```

and in `doctor()`, after `report("guard", guard_problem())`: `copy = copy_line()` / `if copy: lines.append(copy)`.

- [ ] **Step 3: Run** — `loom job run -d "doctor tests" -- sh -c 'cd flow/lib && python3 -m unittest -q doctor_test'` and `loom job run -d "go cli flow" -- env CGO_ENABLED=0 go test ./internal/cli/ -run 'TestFlowTool|TestCopyWarning|TestFlowRun'`. Expected: OK; `gofmt -l internal/cli` prints nothing.

- [ ] **Step 4: Commit** — `git commit -m "feat(flow,cli): warn a session whose flow copy isn't the installed loom's" -- internal/cli/flow.go internal/cli/flow_conf.go internal/cli/flow_run.go internal/cli/flow_test.go flow/lib/doctor.py flow/lib/doctor_test.py`.

### Task 3: prompts, docs and the baseline

- [ ] **Step 1: The draft skill.** Step 1's doctor bullet gains: "A `WARN flow copy` line means this session runs older prompts and tools than the installed loom: tell the owner, and go on unless they stop you." Step 6's pricing bullet gains: "Add the plan-reviewer and risk-reviewer rows' `USD` for the plan review's cost, and run `loom flow conf roster` for this session's copy." The Design stage line's format becomes `… $<logged per 1,000 lines> per 1,000 lines; plan review $<plan-reviewer and risk-reviewer USD>; <loom flow conf roster's output>.`
- [ ] **Step 2: README.** The `costs` row: "`records [FILE ...]` (…)" gains ", with a Plan code table of each engine run's plan-code questions, drafting and run cost per 1,000 plan lines, fix and re-review cost and plan review cost per story".
- [ ] **Step 3: Baseline.** Run `loom flow costs records` over the 7 engine records of 2026-11-11 to 2026-11-13 (research-a, virtualised-sessions B, local-agents L1, research-b, workspace-store A1, lsqb, rmat) and put its pooled questions per 1,000 lines and drafting + run per 1,000 lines into plan-reviewer's provenance entry in place of 0.35 and $5.16, saying they come from the table; update spec 2026-11-13-flow-plan-review-design.md §8 to match.
- [ ] **Step 4: Verify and commit.** `loom job run -d "flow suite" -- bash flow/lib/test.sh` (OK, lint OK), `loom job run -d "go flow claudemd" -- env CGO_ENABLED=0 go test ./flow/ ./tools/claudemd/`. Commit the skill, README, provenance and spec by pathspec.

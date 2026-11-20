#!/usr/bin/env python3
"""The plan-code figures of loom flow run records, for `agent_costs.py records`.

A record the run engine wrote (`- Engine: loom flow run`) gives, per run, from its text alone:
the draft session's flow copy and the run's (the `loom flow roster <hex>` of its Design stage line,
and the last of its Definitions line); the plan's lines, the drafting's logged USD and the plan
review's and the skeleton review's USD (`<n> plan lines`, `$<n> logged`, the plan review's
`risk review $<n>` or `plan review $<n>`, and `skeleton review $<n>` in the Design stage line); its
plan-code questions (`- Asked (unresolved-findings, Story N): Reviews of Story N found problems in
the plan's own code`); what the fix rounds and re-reviews of those stories cost (every Cost by agent
row named `…-s<N>-fix<k>`, or `…-s<N>-review<k>` with k >= 2, its USD column); and its stories.

The plan review's token says who reviewed: `risk review $<n>` is flow:risk-reviewer alone, the plan
review since the owner removed flow:plan-reviewer on 2026-11-20 (`risk_only`), and `plan review $<n>`,
in the Design stage lines of drafts before then, the two reviewers together.

The table gives each run and the pooled figures the plan review's provenance entry judges: plan-code
questions per 1,000 plan lines, drafting plus run USD per 1,000 plan lines and the review's USD per
story, and the skeleton review's USD a plan, which its own provenance entry judges. A run without a
figure shows n/a; each pooled ratio uses only the runs with both its figures, and the pooled skeleton
review is the average over the runs that have one.
Logged USD throughout, the Design stage line's basis.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field

ENGINE_RE = re.compile(r"^- Engine: loom flow run\b", re.M)
STAGE_RE = re.compile(r"^- Design stage: (.*)$", re.M)
DEFINITIONS_RE = re.compile(r"^- Definitions: (.*)$", re.M)
ROSTER_RE = re.compile(r"loom flow roster ([0-9a-f]{12})")
LOGGED_RE = re.compile(r"\$([0-9][0-9,]*(?:\.[0-9]+)?) logged")
PLAN_LINES_RE = re.compile(r"([0-9][0-9,]*) plan lines")
# The plan review's USD: `risk review $` flow:risk-reviewer's alone, `plan review $` (drafts before 2026-11-20) both reviewers'.
RISK_REVIEW_RE = re.compile(r"risk review \$([0-9][0-9,]*(?:\.[0-9]+)?)")
PLAN_REVIEW_RE = re.compile(r"plan review \$([0-9][0-9,]*(?:\.[0-9]+)?)")
SKELETON_RE = re.compile(r"skeleton review \$([0-9][0-9,]*(?:\.[0-9]+)?)")
ASKED_RE = re.compile(
    r"^- Asked \(unresolved-findings, Story ([^)]+)\): Reviews of Story \S+ found problems in the plan's own code", re.M
)
AGENT_RE = re.compile(r"-s([^-\s]+)-(fix|review)(\d+)$")
# Any agent run of one story: `<run>-s<id>-<role><k>` (dev, review, fix, docs, …).
STORY_AGENT_RE = re.compile(r"-s([^-\s]+)-[a-z]+\d+$")
RECORD_BUDGET_RE = re.compile(r"^- Budget: (.*)$", re.M)
BUDGET_RE = re.compile(r"draft \$([0-9][0-9,]*(?:\.[0-9]+)?); run \$([0-9][0-9,]*(?:\.[0-9]+)?)")
FORMS_RE = re.compile(r"\bforms: ([^;]*)")
FORM_ITEM_RE = re.compile(r"(full|mechanical|brief) (\d+) stor(?:y|ies) \$([0-9][0-9,]*(?:\.[0-9]+)?)")


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
    review_usd: float | None
    # Whether review_usd is flow:risk-reviewer's alone (`risk review $`), not both reviewers' (`plan review $`).
    risk_only: bool = False
    skeleton_usd: float | None = None
    # The plan's approved Budget line, as the run's record copied it (plan treatment spec §6).
    budget_draft: float | None = None
    budget_run: float | None = None
    # By form: the stories and their USD, all-inclusive (part 2 settlements): the Design stage line's
    # `forms:` segment for drafting, and the record's stories with an even share of the unowned rows for the run.
    forms_draft: dict[str, tuple[int, float]] = field(default_factory=dict)
    forms_run: dict[str, tuple[int, float]] = field(default_factory=dict)


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
    budget = BUDGET_RE.search(found(RECORD_BUDGET_RE, text) or "")
    lines, logged = found(PLAN_LINES_RE, stage), found(LOGGED_RE, stage)
    risk = found(RISK_REVIEW_RE, stage)
    review = risk if risk is not None else found(PLAN_REVIEW_RE, stage)
    skeleton = found(SKELETON_RE, stage)
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
        review_usd=number(review) if review else None,
        risk_only=risk is not None,
        skeleton_usd=number(skeleton) if skeleton else None,
        budget_draft=number(budget.group(1)) if budget else None,
        budget_run=number(budget.group(2)) if budget else None,
        forms_draft=draft_forms(stage),
        forms_run=run_forms(text, stories, run_usd),
    )


def draft_forms(stage: str) -> dict[str, tuple[int, float]]:
    """A Design stage line's `forms:` segment, form → (stories, USD); empty without one."""
    segment = found(FORMS_RE, stage) or ""
    return {form: (int(n), number(amount)) for form, n, amount in FORM_ITEM_RE.findall(segment)}


def run_forms(text: str, stories: list[dict[str, str]], run_usd: float) -> dict[str, tuple[int, float]]:
    """Each form's stories and run USD: a story's own agent rows (`…-s<id>-…`) plus an even share of every other
    row of the run, so the forms' USD sums to the run's. A Stories table without a Form column is all full."""
    if not stories:
        return {}
    owned: dict[str, float] = {}
    for agent_row in table_rows(text, "Agent"):
        named = STORY_AGENT_RE.search(agent_row.get("Agent", ""))
        if named:
            try:
                owned[named.group(1)] = owned.get(named.group(1), 0.0) + number(agent_row.get("USD", "0"))
            except ValueError:
                pass
    ids = [story.get("Story", "") for story in stories]
    share = (run_usd - sum(owned.get(sid, 0.0) for sid in ids)) / len(ids)
    forms: dict[str, tuple[int, float]] = {}
    for story in stories:
        form = story.get("Form") or "full"
        count, amount = forms.get(form, (0, 0.0))
        forms[form] = (count + 1, amount + owned.get(story.get("Story", ""), 0.0) + share)
    return forms


def draft_by_form(run: RunMeasures) -> dict[str, tuple[int, float]]:
    """A run's drafting by form: its Design stage line's forms, else all its stories full at its drafting less its
    skeleton and plan reviews (the plan treatment spec's Part 2 settlements); empty when it has no drafting figure."""
    if run.forms_draft:
        return run.forms_draft
    if run.draft_usd is None or not run.stories:
        return {}
    return {"full": (run.stories, run.draft_usd - (run.skeleton_usd or 0.0) - (run.review_usd or 0.0))}


def signed(actual: float | None, budget: float | None) -> str:
    """How far actual ran from budget, as a signed whole percentage."""
    if actual is None or not budget:
        return "n/a"
    return f"{(actual - budget) * 100 / budget:+.0f}%"


def estimate_table(runs: list[RunMeasures]) -> str:
    """The Estimate section: each budgeted run against its budget, then each form's cost a story; "" when no run
    has a budget or a form other than full (the plan treatment spec §6)."""
    budgeted = [r for r in runs if r.budget_draft is not None or r.budget_run is not None]
    formed = any(r.forms_draft or set(r.forms_run) - {"full"} for r in runs)
    if not budgeted and not formed:
        return ""
    lines = [
        "## Estimate", "",
        "Each run with a Budget line against the budget the owner approved, then each form's cost a story over every "
        "engine run. Every figure is all-inclusive: a stage's cost no story owns is spread evenly over its stories, and "
        "the drafting excludes the skeleton and plan reviews. Logged USD.", "",
        "| Run | Draft budget | Draft | Draft error | Run budget | Run | Run error |", "|---|" + "---:|" * 6,
    ]
    for r in budgeted:
        lines.append(f"| {r.name} | {usd(r.budget_draft)} | {usd(r.draft_usd)} | {signed(r.draft_usd, r.budget_draft)} | "
                     f"{usd(r.budget_run)} | {usd(r.run_usd)} | {signed(r.run_usd, r.budget_run)} |")
    lines += ["", "| Form | Stories drafted | Draft a story | Stories run | Run a story |", "|---|" + "---:|" * 4]
    for form in ("full", "mechanical", "brief"):
        drafted = [draft_by_form(r).get(form, (0, 0.0)) for r in runs]
        ran = [r.forms_run.get(form, (0, 0.0)) for r in runs]
        n_draft, usd_draft = sum(n for n, _ in drafted), sum(u for _, u in drafted)
        n_run, usd_run = sum(n for n, _ in ran), sum(u for _, u in ran)
        if n_draft or n_run:
            lines.append(f"| {form} | {n_draft} | {usd(usd_draft / n_draft if n_draft else None)} | {n_run} | "
                         f"{usd(usd_run / n_run if n_run else None)} |")
    return "\n".join(lines)


def usd(value: float | None) -> str:
    return "n/a" if value is None else f"${value:,.2f}"


def per_thousand(amount: float | None, lines: int | None) -> float | None:
    return None if amount is None or not lines else amount * 1000 / lines


def row(name: str, draft_copy: str, run_copy: str, lines: int | None, questions: int, draft: float | None,
        run: float, review: float | None, fix: float, stories: int, skeleton: str) -> str:
    rate = per_thousand(questions, lines)
    spend = per_thousand(None if draft is None else draft + run, lines)
    each = None if review is None or not stories else review / stories
    return (f"| {name} | {draft_copy} | {run_copy} | {'n/a' if lines is None else f'{lines:,}'} | {questions} | "
            f"{'n/a' if rate is None else f'{rate:.2f}'} | {usd(draft)} | {usd(run)} | {usd(spend)} | {usd(fix)} | "
            f"{stories} | {usd(review)} | {usd(each)} | {skeleton} |")


def table(runs: list[RunMeasures]) -> str:
    """The Plan code section: a row per run, then the pooled row."""
    lines = [
        "## Plan code", "",
        "Engine runs only. Plan-code questions are the run's \"found problems in the plan's own code\" questions, "
        "and fix + re-review is what the fix rounds and re-reviews of their stories cost. The plan review is "
        "flow:risk-reviewer's alone where the Design stage line says `risk review $`, and flow:plan-reviewer's and "
        "flow:risk-reviewer's together where it says `plan review $` (drafts before 2026-11-20). Logged USD; a pooled "
        "ratio uses only the runs with both its figures.", "",
        "| Run | Draft copy | Run copy | Plan lines | Plan-code questions | per 1,000 lines | Drafting | Run "
        "| Drafting + run per 1,000 lines | Fix + re-review on them | Stories | Plan review | per story "
        "| Skeleton review |",
        "|---|---|---|" + "---:|" * 11,
    ]
    for r in runs:
        lines.append(row(r.name, r.draft_copy or "n/a", r.run_copy or "n/a", r.plan_lines, r.questions, r.draft_usd,
                         r.run_usd, r.review_usd, r.fix_usd, r.stories, usd(r.skeleton_usd)))
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
    skeletons = [r.skeleton_usd for r in runs if r.skeleton_usd is not None]
    skeleton = f"{usd(sum(skeletons) / len(skeletons))} a plan" if skeletons else "n/a"
    lines.append(
        f"| Pooled ({len(runs)} run{'' if len(runs) == 1 else 's'}) | | | "
        f"{'n/a' if pooled_lines is None else f'{pooled_lines:,}'} | {sum(r.questions for r in runs)} | "
        f"{'n/a' if rate is None else f'{rate:.2f}'} | {usd(sum(drafted) if drafted else None)} | "
        f"{usd(sum(r.run_usd for r in runs))} | {usd(spend)} | {usd(sum(r.fix_usd for r in runs))} | "
        f"{sum(r.stories for r in runs)} | "
        f"{usd(sum(r.review_usd or 0 for r in reviewed) if reviewed else None)} | {usd(each)} | {skeleton} |"
    )
    return "\n".join(lines)

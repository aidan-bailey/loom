#!/usr/bin/env python3
"""The plan-code figures of loom flow run records, for `agent_costs.py records`.

A record the run engine wrote (`- Engine: loom flow run`) gives, per run, from its text alone:
the draft session's flow copy and the run's (the `loom flow roster <hex>` of its Design stage line,
and the last of its Definitions line); the plan's lines, the drafting's logged USD and the plan
review's and the skeleton review's USD (`<n> plan lines`, `$<n> logged`, `plan review $<n>` and
`skeleton review $<n>` in the Design stage line); its
plan-code questions (`- Asked (unresolved-findings, Story N): Reviews of Story N found problems in
the plan's own code`); what the fix rounds and re-reviews of those stories cost (every Cost by agent
row named `…-s<N>-fix<k>`, or `…-s<N>-review<k>` with k >= 2, its USD column); and its stories and
how many its Stories table marks vouched.

The table gives each run and the pooled figures the plan review's provenance entry judges: plan-code
questions per 1,000 plan lines, drafting plus run USD per 1,000 plan lines and the review's USD per
story, and the skeleton review's USD a plan, which its own provenance entry judges. A run without a
figure shows n/a; each pooled ratio uses only the runs with both its figures, and the pooled skeleton
review is the average over the runs that have one.
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
SKELETON_RE = re.compile(r"skeleton review \$([0-9][0-9,]*(?:\.[0-9]+)?)")
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
    skeleton_usd: float | None = None


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
        vouched=sum(1 for row in stories if row.get("Review") == "vouched"),
        review_usd=number(review) if review else None,
        skeleton_usd=number(skeleton) if skeleton else None,
    )


def usd(value: float | None) -> str:
    return "n/a" if value is None else f"${value:,.2f}"


def per_thousand(amount: float | None, lines: int | None) -> float | None:
    return None if amount is None or not lines else amount * 1000 / lines


def row(name: str, draft_copy: str, run_copy: str, lines: int | None, questions: int, draft: float | None,
        run: float, review: float | None, fix: float, stories: int, vouched: int, skeleton: str) -> str:
    rate = per_thousand(questions, lines)
    spend = per_thousand(None if draft is None else draft + run, lines)
    each = None if review is None or not stories else review / stories
    return (f"| {name} | {draft_copy} | {run_copy} | {'n/a' if lines is None else f'{lines:,}'} | {questions} | "
            f"{'n/a' if rate is None else f'{rate:.2f}'} | {usd(draft)} | {usd(run)} | {usd(spend)} | {usd(fix)} | "
            f"{stories} | {vouched} | {usd(review)} | {usd(each)} | {skeleton} |")


def table(runs: list[RunMeasures]) -> str:
    """The Plan code section: a row per run, then the pooled row."""
    lines = [
        "## Plan code", "",
        "Engine runs only. Plan-code questions are the run's \"found problems in the plan's own code\" questions, "
        "and fix + re-review is what the fix rounds and re-reviews of their stories cost. Logged USD; a pooled "
        "ratio uses only the runs with both its figures.", "",
        "| Run | Draft copy | Run copy | Plan lines | Plan-code questions | per 1,000 lines | Drafting | Run "
        "| Drafting + run per 1,000 lines | Fix + re-review on them | Stories | Vouched | Plan review | per story "
        "| Skeleton review |",
        "|---|---|---|" + "---:|" * 12,
    ]
    for r in runs:
        lines.append(row(r.name, r.draft_copy or "n/a", r.run_copy or "n/a", r.plan_lines, r.questions, r.draft_usd,
                         r.run_usd, r.review_usd, r.fix_usd, r.stories, r.vouched, usd(r.skeleton_usd)))
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
        f"{sum(r.stories for r in runs)} | {sum(r.vouched for r in runs)} | "
        f"{usd(sum(r.review_usd or 0 for r in reviewed) if reviewed else None)} | {usd(each)} | {skeleton} |"
    )
    return "\n".join(lines)

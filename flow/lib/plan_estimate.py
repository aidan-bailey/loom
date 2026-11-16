#!/usr/bin/env python3
"""Price a plan skeleton's stories by form, stage by stage, for its `**Budget:**` line.

usage: plan_estimate.py PLAN [--records DIR ...] [--date YYYY-MM-DD]

The design session runs it once the skeleton passes `loom flow check-plan --skeleton` (the plan
treatment spec §5): it reads the skeleton's story entries and each one's **Form** (full when none),
prices each story's drafting and run from the run records, and prints a row a story, the stage totals,
the design session's spend so far, each figure's basis, and the Budget line to paste:

    **Budget:** draft $<X>; run $<Y> (estimate YYYY-MM-DD)

The records are the repo's engine runs (FLOW_DATA/records/<repo key>/) when it has `[estimate]
min_runs` of them (estimates.toml), else every engine run in the home store; `--records` names the
directories instead. Every figure is all-inclusive (the spec's Part 2 settlements): a full story's
drafting is the records' drafting less their skeleton and plan reviews, over their stories, and its run
is their run USD over their stories. A mechanical or brief story is priced from that form's stories in
the records once they hold `min_stories` of them, else as estimates.toml's multiple of a full story's
figure; with no records at all, a full story's figure is estimates.toml's `[full]`. The draft adds the
skeleton review a plan and the plan review for each full story, from the records once `min_runs` of
them hold one, else estimates.toml's priors.

Exit 0 with the estimate; 1, with the reason on stderr and nothing on stdout, when the plan can't be
read, has no story list, gives a Form that isn't full, mechanical or brief, or its repo has no usable
profile; 2 on a usage error.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import tomllib
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import agent_costs  # noqa: E402
import flowconf  # noqa: E402
import plan_measures  # noqa: E402
from check_plan import CODE_RE, ESTIMATES, FORM_VALUE_RE, SPEC_RE, repo_root  # noqa: E402
from flowconf import FlowError  # noqa: E402
from plan_join import JoinError, entries  # noqa: E402

FORMS = ("full", "mechanical", "brief")


class EstimateError(Exception):
    """What keeps the estimate from being made; the message is a sentence."""


@dataclass
class Figure:
    """A USD figure a story or a plan, and where it came from."""

    usd: float
    basis: str


def runs_in(directories: list[Path]) -> list[plan_measures.RunMeasures]:
    """The engine runs among the records in these directories, the retros left out."""
    runs: list[plan_measures.RunMeasures] = []
    for directory in directories:
        for path in sorted(directory.glob("*.md")):
            text = path.read_text(encoding="utf-8", errors="replace")
            comments = [json.loads(m.group(1)) for m in agent_costs.COMMENT_RE.finditer(text)]
            if not comments:
                continue
            measured = plan_measures.measure(path.stem, text, agent_costs.merge_sessions(comments)["total"].get("usd", 0.0))
            if measured is not None:
                runs.append(measured)
    return runs


def chosen_runs(top: Path, given: list[Path] | None, min_runs: int) -> tuple[list[plan_measures.RunMeasures], str]:
    """The runs the estimate reads, and how to name them."""
    if given:
        runs = runs_in(given)
        return runs, f"{len(runs)} engine runs in {', '.join(str(d) for d in given)}"
    root = flowconf.flow_data() / "records"
    own = runs_in([root / flowconf.repo_key(top)])
    if len(own) >= min_runs:
        return own, f"{len(own)} engine runs of this repo"
    pooled = runs_in([d for d in sorted(root.glob("*")) if d.is_dir() and d.name != "retros"])
    return pooled, f"{len(pooled)} engine runs, pooled"


def pooled_form(runs: list[plan_measures.RunMeasures], form: str, stage: str) -> tuple[int, float]:
    """A form's stories and USD at a stage ("draft" or "run") over the runs."""
    stories, usd = 0, 0.0
    for run in runs:
        by_form = plan_measures.draft_by_form(run) if stage == "draft" else run.forms_run
        n, amount = by_form.get(form, (0, 0.0))
        stories, usd = stories + n, usd + amount
    return stories, usd


def form_figures(runs: list[plan_measures.RunMeasures], priors: dict) -> tuple[dict[str, dict[str, float]], dict[str, str]]:
    """Each form's draft and run USD a story, and each form's basis."""
    min_stories = priors["estimate"]["min_stories"]
    figures: dict[str, dict[str, float]] = {}
    bases: dict[str, str] = {}
    full_runs = sum(1 for run in runs if plan_measures.draft_by_form(run).get("full") or run.forms_run.get("full"))
    full: dict[str, float] = {}
    for stage in ("draft", "run"):
        n, usd = pooled_form(runs, "full", stage)
        full[stage] = usd / n if n else priors["full"][stage]
    figures["full"] = full
    bases["full"] = f"full from {full_runs} runs" if full_runs else "full prior"
    for form in ("mechanical", "brief"):
        figure: dict[str, float] = {}
        measured: dict[str, int] = {}
        for stage in ("draft", "run"):
            n, usd = pooled_form(runs, form, stage)
            if n >= min_stories:
                figure[stage], measured[stage] = usd / n, n
            else:
                figure[stage] = priors[form][stage] * full[stage]
        figures[form] = figure
        if len(measured) == 2:
            bases[form] = f"{form} from {min(measured.values())} stories"
        elif not measured:
            bases[form] = f"{form} prior ({priors[form]['draft']}× draft, {priors[form]['run']}× run)"
        else:
            stage = next(iter(measured))
            other = "run" if stage == "draft" else "draft"
            bases[form] = f"{form} {stage} from {measured[stage]} stories, {other} prior"
    return figures, bases


def review_figures(runs: list[plan_measures.RunMeasures], priors: dict) -> tuple[Figure, Figure]:
    """The skeleton review a plan and the plan review a full story."""
    min_runs = priors["estimate"]["min_runs"]
    skeletons = [run.skeleton_usd for run in runs if run.skeleton_usd is not None]
    skeleton = (Figure(sum(skeletons) / len(skeletons), f"skeleton review from {len(skeletons)} runs")
                if len(skeletons) >= min_runs else Figure(priors["skeleton_review"]["usd_per_plan"], "skeleton review prior"))
    reviewed = [run for run in runs if run.review_usd is not None]
    full_stories = sum(run.forms_run.get("full", (0, 0.0))[0] for run in reviewed)
    review = (Figure(sum(run.review_usd or 0.0 for run in reviewed) / full_stories, f"plan review from {len(reviewed)} runs")
              if len(reviewed) >= min_runs and full_stories else
              Figure(priors["plan_review"]["usd_per_full_story"], "plan review prior"))
    return skeleton, review


def skeleton_stories(text: str) -> list[tuple[str, str]]:
    """Each story entry's id and form, in order."""
    try:
        listed = entries(text)
    except JoinError as err:
        raise EstimateError(str(err)) from None
    stories = []
    for entry in listed:
        value = entry.fields.get("Form", "full")
        form = FORM_VALUE_RE.match(value)
        if not form:
            raise EstimateError(f"Story {entry.sid}'s **Form:** must be full, mechanical or brief, not {value!r}")
        stories.append((entry.sid, form.group(1)))
    return stories


def design_spend(text: str, plan: str) -> str:
    """The design session's spend so far, from loom flow costs design up to the plan's commit (now, before it is
    committed), or why it is unknown."""
    spec = next((CODE_RE.findall(m.group(1)) for line in text.splitlines() if (m := SPEC_RE.match(line))), None)
    if not spec:
        return "unknown (the plan has no **Spec:** line)"
    try:
        return plan_measures.usd(agent_costs.design_json(spec[0], plan)["total"]["usd"])
    except Exception as err:  # noqa: BLE001 - the estimate stands without it; say why it's missing
        return f"unknown ({err})"


def whole(usd: float) -> str:
    return f"${round(usd):,}"


def estimate(plan: Path, given: list[Path] | None, date: str) -> str:
    top = repo_root(plan)
    flowconf.load_profile(top)
    try:
        text = plan.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as err:
        raise EstimateError(f"the plan cannot be read: {err}") from None
    stories = skeleton_stories(text)
    priors = tomllib.loads(ESTIMATES.read_text(encoding="utf-8"))
    runs, named = chosen_runs(top, given, priors["estimate"]["min_runs"])
    figures, bases = form_figures(runs, priors)
    skeleton, review = review_figures(runs, priors)
    cell = plan_measures.usd
    lines = [f"Estimate for {plan}, {date}: " + (named if runs else "no engine runs: every figure is a prior"), "",
             "| Story | Form | Draft | Run | As mechanical | As brief |", "|---|---|---:|---:|---|---|"]
    for sid, form in stories:
        own = figures[form]
        row = f"| {sid} | {form} | {cell(own['draft'])} | {cell(own['run'])} |"
        if form == "full":
            row += "".join(f" {cell(figures[f]['draft'])} / {cell(figures[f]['run'])} |" for f in ("mechanical", "brief"))
        else:
            row += " | |"
        lines.append(row)
    full = sum(1 for _, form in stories if form == "full")
    stories_draft = sum(figures[form]["draft"] for _, form in stories)
    reviews = review.usd * full
    draft = stories_draft + skeleton.usd + reviews
    run = sum(figures[form]["run"] for _, form in stories)
    lines += [
        "",
        f"Draft: {cell(draft)} (stories {cell(stories_draft)}, skeleton review {cell(skeleton.usd)}, "
        f"plan review {cell(reviews)} for {full} full {'story' if full == 1 else 'stories'})",
        f"Run: {cell(run)}",
        f"Design so far: {design_spend(text, os.path.relpath(plan.resolve(), top))}, spent, not budgeted",
        "Basis: " + "; ".join([*(bases[form] for form in FORMS), skeleton.basis, review.basis]),
        "",
        f"**Budget:** draft {whole(draft)}; run {whole(run)} (estimate {date})",
    ]
    return "\n".join(lines)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="plan_estimate.py", description="Price a plan skeleton's stories by form, stage by stage, for its Budget line.")
    parser.add_argument("plan", type=Path, help="the plan skeleton")
    parser.add_argument("--records", type=Path, action="append", help="a records directory to read instead of the home store's (repeatable)")
    parser.add_argument("--date", help="the estimate's date (default: today, UTC)")
    args = parser.parse_args(argv)
    date = args.date or datetime.now(timezone.utc).strftime("%Y-%m-%d")
    try:
        print(estimate(args.plan, args.records, date))
    except (OSError, FlowError, EstimateError, tomllib.TOMLDecodeError, KeyError) as err:
        print(f"plan_estimate.py: {args.plan}: {err}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

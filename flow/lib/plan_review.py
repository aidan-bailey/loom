#!/usr/bin/env python3
"""The `**Plan review:**` line of a plan /flow:draft reviewed, and which stories its review reads.

usage: plan_review.py [--select] PLAN

/flow:draft's plan review (the dispatch guide's) copies each story's draft to D/reviewing/S<id>.md
when its review starts, and to D/reviewed/S<id>.md once that review, or its one re-check, is clean.
A deep story is vouched only when D/reviewed/S<id>.md is byte-identical to D/S<id>.md, the text the
join puts in the plan, so any change after the review un-vouches it. This prints the line the
manager writes above the skeleton's `## Stories`:

    **Plan review:** /flow:draft plan review; deep: 1, 6; light: 2, 3; full: 4

`deep:` lists the vouched deep stories, `light:` every light story (the run always reviews a light
story in full) and `full:` every other deep story; an empty list is left out. The stories and their
tiers come from the joined PLAN, D is the plan's drafts directory.

With --select, it prints instead which stories the plan review reads, one line a story and a count,
before the review starts:

    Story 1: review (landmines: user_id = $; risk: Task 1.2)
    Story 2: skip
    Story 3: review (no risk file)
    2 of 3 stories to review: 1, 3

A story is reviewed when its fenced code holds one of the profile's landmine markers, or its drafter's
risk file, D/risk/S<id>.md, lists a task whose code invents logic for store writes, transactions,
concurrency, outside calls, bounds or auth, or that file is missing or is neither `none` nor a `- `
list (so a draft from an older flow copy is reviewed). Every other story is skipped: the run reviews
it in full, as it does every story the plan review doesn't vouch for (the owner, 2026-11-16).

A mechanical or brief story (its `**Form:**` line; the plan treatment spec §4) is skipped whatever its
markers or risk file say, `Story 2: skip (mechanical)`: the plan holds none of its code to review. But
when its drafter's risk file lists a task its Form reason doesn't name (`Task 2.1` in the reason), the
story hides risky logic in a form without plan code, and --select fails naming each such story and
task: the manager takes it to the owner, who makes the story full or names the task in the reason.

Exit 0 with the line on stdout; 1, with the reason on stderr and nothing on stdout, when the plan
holds no story, D/join.json is missing (D itself, or a plan never joined), a story has no draft or,
with --select, the repo has no usable profile or a mechanical or brief story's drafter lists a task its
reason doesn't name; 2 on a usage error.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import check_plan  # noqa: E402
from flowconf import FlowError, load_profile  # noqa: E402
from plan_join import drafts_dir  # noqa: E402

LABEL = "/flow:draft plan review"
RISK_DIR = "risk"


class ReviewError(Exception):
    """What keeps the line from being written; the message is a sentence."""


def drafted(text: str, drafts: Path) -> list[check_plan.Story]:
    """The joined plan's stories, each with its draft in D; ReviewError when the plan isn't joined or a draft is missing."""
    stories, _problems, _spec = check_plan.parse(text)
    if not stories:
        raise ReviewError("the plan holds no `## Story` section: join it first")
    if not (drafts / "join.json").is_file():
        raise ReviewError(f"{drafts / 'join.json'} is missing: join the plan first")
    for story in stories:
        draft = drafts / f"S{story.sid}.md"
        if not draft.is_file():
            raise ReviewError(f"Story {story.sid} has no draft {draft}")
    return stories


def tiers(text: str, drafts: Path) -> dict[str, list[str]]:
    """The plan's story ids under `deep` (vouched), `light` and `full`, in plan order."""
    lists: dict[str, list[str]] = {"deep": [], "light": [], "full": []}
    for story in drafted(text, drafts):
        draft = drafts / f"S{story.sid}.md"
        if story.tier == "light":
            lists["light"].append(story.sid)
            continue
        reviewed = drafts / "reviewed" / f"S{story.sid}.md"
        clean = reviewed.is_file() and reviewed.read_bytes() == draft.read_bytes()
        lists["deep" if clean else "full"].append(story.sid)
    return lists


def risk_file(path: Path) -> tuple[str | None, list[str]]:
    """What a drafter's risk file says: a problem that sends its story to review, else the tasks it lists.

    The file is the single line `none`, or one `- Task <x.y>: <what it invents>` line a task; blank
    lines don't count. A missing file, or one that is neither, is a problem.
    """
    if not path.is_file():
        return "no risk file", []
    lines = [entry.strip() for entry in path.read_text(encoding="utf-8").splitlines() if entry.strip()]
    if lines == ["none"]:
        return None, []
    if not lines or not all(entry.startswith("- ") for entry in lines):
        return "risk file is neither `none` nor a list", []
    return None, [entry[2:].split(":", 1)[0].strip() for entry in lines]


def risk_reason(path: Path) -> str | None:
    """Why a drafter's risk file sends its story to review, or None when it says `none`."""
    problem, tasks = risk_file(path)
    if problem:
        return problem
    return "risk: " + ", ".join(tasks) if tasks else None


def unnamed(tasks: list[str], reason: str) -> list[str]:
    """The tasks a Form reason doesn't name; `Task 1.1` is not named by `Task 1.10`."""
    return [task for task in tasks if not re.search(re.escape(task) + r"(?![\w.])", reason)]


def selection(text: str, drafts: Path, markers: tuple[str, ...]) -> list[tuple[str, list[str], str]]:
    """Each story's id, the reasons the plan review reads it (none: skip it) and its form, in plan order.

    ReviewError naming each mechanical or brief story whose drafter lists a task its Form reason doesn't name.
    """
    chosen: list[tuple[str, list[str], str]] = []
    hidden: list[str] = []
    for story in drafted(text, drafts):
        if story.form != "full":
            _problem, tasks = risk_file(drafts / RISK_DIR / f"S{story.sid}.md")
            left = unnamed(tasks, story.form_reason)
            if left:
                hidden.append(
                    f"Story {story.sid} is {story.form} but its drafter lists {', '.join(left)} (risky logic): "
                    "make it full, or name each task in the Form line's reason"
                )
            chosen.append((story.sid, [], story.form))
            continue
        reasons = []
        found = check_plan.markers_in(story, markers)
        if found:
            reasons.append("landmines: " + ", ".join(marker for marker, _ in found))
        risk = risk_reason(drafts / RISK_DIR / f"S{story.sid}.md")
        if risk:
            reasons.append(risk)
        chosen.append((story.sid, reasons, story.form))
    if hidden:
        raise ReviewError("; ".join(hidden))
    return chosen


def selection_lines(chosen: list[tuple[str, list[str], str]]) -> list[str]:
    """--select's output: a line a story, then how many are reviewed and which."""
    out = []
    for sid, reasons, form in chosen:
        if reasons:
            out.append(f"Story {sid}: review ({'; '.join(reasons)})")
        else:
            out.append(f"Story {sid}: skip ({form})" if form != "full" else f"Story {sid}: skip")
    reviewed = [sid for sid, reasons, _ in chosen if reasons]
    out.append(f"{len(reviewed)} of {len(chosen)} stories to review: {', '.join(reviewed) or 'none'}")
    return out


def line(lists: dict[str, list[str]]) -> str:
    """The `**Plan review:**` line: the label, then each non-empty list in deep, light, full order."""
    parts = [LABEL, *(f"{name}: {', '.join(ids)}" for name, ids in lists.items() if ids)]
    return "**Plan review:** " + "; ".join(parts)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="plan_review.py", description="Print the Plan review line of a plan /flow:draft reviewed, or with --select which stories its review reads.")
    parser.add_argument("--select", action="store_true", help="print which stories the plan review reads, before it starts")
    parser.add_argument("plan", type=Path, help="the joined plan")
    args = parser.parse_args(argv)
    plan: Path = args.plan
    try:
        text = plan.read_text(encoding="utf-8")
        if args.select:
            markers = load_profile(check_plan.repo_root(plan)).markers
            result = "\n".join(selection_lines(selection(text, drafts_dir(plan), markers)))
        else:
            result = line(tiers(text, drafts_dir(plan)))
    except (OSError, UnicodeDecodeError, FlowError, ReviewError) as err:
        print(f"plan_review.py: {plan}: {err}", file=sys.stderr)
        return 1
    print(result)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

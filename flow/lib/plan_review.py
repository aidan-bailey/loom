#!/usr/bin/env python3
"""Which stories of a plan /flow:draft drafted its plan review (flow:risk-reviewer) reads.

usage: plan_review.py --select PLAN

It prints, before the review starts, one line a story and a count:

    Story 1: review (landmines: user_id = $; risk: Task 1.2)
    Story 2: skip
    Story 3: review (no risk file)
    2 of 3 stories to review: 1, 3

The stories come from the joined PLAN, each with its draft in D, the plan's drafts directory. A story is
reviewed when its fenced code holds one of the profile's landmine markers, or its drafter's risk file,
D/risk/S<id>.md, lists a task whose code invents logic for store writes, transactions, concurrency,
outside calls, bounds or auth, or that file is missing or is neither `none` nor a `- ` list (so a draft
from an older flow copy is reviewed). Every other story is skipped (the owner, 2026-11-16); the run gives
every story the full review, read here or not (the owner, 2026-11-20).

A mechanical or brief story (its `**Form:**` line; the plan treatment spec §4) is skipped whatever its
markers or risk file say, `Story 2: skip (mechanical)`: the plan holds none of its code to review. But
when its drafter's risk file lists a task its Form reason doesn't name (`Task 2.1` in the reason), the
story hides risky logic in a form without plan code, and it fails naming each such story and task: the
manager takes it to the owner, who makes the story full or names the task in the reason.

--select is required. Without it the script once printed a plan's `**Plan review:**` line, which the
owner removed on 2026-11-20; it is a usage error now.

Exit 0 with the selection on stdout; 1, with the reason on stderr and nothing on stdout, when the plan
holds no story, D/join.json is missing (D itself, or a plan never joined), a story has no draft, the
repo has no usable profile or a mechanical or brief story's drafter lists a task its reason doesn't
name; 2 on a usage error, --select missing among them.
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

RISK_DIR = "risk"


class ReviewError(Exception):
    """What keeps the selection from being made; the message is a sentence."""


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


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="plan_review.py", usage="%(prog)s --select PLAN",
                                     description="Print which stories /flow:draft's plan review reads, before it starts.")
    parser.add_argument("--select", action="store_true", help="print which stories the plan review reads (required)")
    parser.add_argument("plan", type=Path, help="the joined plan")
    args = parser.parse_args(argv)
    if not args.select:
        # parser.error exits 2 with the usage line, as any other usage error does.
        parser.error("--select is required (the **Plan review:** line the bare form printed was removed on 2026-11-20)")
    plan: Path = args.plan
    try:
        text = plan.read_text(encoding="utf-8")
        markers = load_profile(check_plan.repo_root(plan)).markers
        result = "\n".join(selection_lines(selection(text, drafts_dir(plan), markers)))
    except (OSError, UnicodeDecodeError, FlowError, ReviewError) as err:
        print(f"plan_review.py: {plan}: {err}", file=sys.stderr)
        return 1
    print(result)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

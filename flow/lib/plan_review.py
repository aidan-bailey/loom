#!/usr/bin/env python3
"""The `**Plan review:**` line of a plan /flow:draft reviewed.

usage: plan_review.py PLAN

/flow:draft's plan review (the dispatch guide's) copies each story's draft to D/reviewing/S<id>.md
when its review starts, and to D/reviewed/S<id>.md once that review, or its one re-check, is clean.
A deep story is vouched only when D/reviewed/S<id>.md is byte-identical to D/S<id>.md, the text the
join puts in the plan, so any change after the review un-vouches it. This prints the line the
manager writes above the skeleton's `## Stories`:

    **Plan review:** /flow:draft plan review; deep: 1, 6; light: 2, 3; full: 4

`deep:` lists the vouched deep stories, `light:` every light story (the run always reviews a light
story in full) and `full:` every other deep story; an empty list is left out. The stories and their
tiers come from the joined PLAN, D is the plan's drafts directory.

Exit 0 with the line on stdout; 1, with the reason on stderr and nothing on stdout, when the plan
holds no story, D/join.json is missing (D itself, or a plan never joined) or a story has no draft;
2 on a usage error.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import check_plan  # noqa: E402
from flowconf import FlowError  # noqa: E402
from plan_join import drafts_dir  # noqa: E402

LABEL = "/flow:draft plan review"


class ReviewError(Exception):
    """What keeps the line from being written; the message is a sentence."""


def tiers(text: str, drafts: Path) -> dict[str, list[str]]:
    """The plan's story ids under `deep` (vouched), `light` and `full`, in plan order."""
    stories, _problems, _spec = check_plan.parse(text)
    if not stories:
        raise ReviewError("the plan holds no `## Story` section: join it first")
    if not (drafts / "join.json").is_file():
        raise ReviewError(f"{drafts / 'join.json'} is missing: join the plan first")
    lists: dict[str, list[str]] = {"deep": [], "light": [], "full": []}
    for story in stories:
        draft = drafts / f"S{story.sid}.md"
        if not draft.is_file():
            raise ReviewError(f"Story {story.sid} has no draft {draft}")
        if story.tier == "light":
            lists["light"].append(story.sid)
            continue
        reviewed = drafts / "reviewed" / f"S{story.sid}.md"
        clean = reviewed.is_file() and reviewed.read_bytes() == draft.read_bytes()
        lists["deep" if clean else "full"].append(story.sid)
    return lists


def line(lists: dict[str, list[str]]) -> str:
    """The `**Plan review:**` line: the label, then each non-empty list in deep, light, full order."""
    parts = [LABEL, *(f"{name}: {', '.join(ids)}" for name, ids in lists.items() if ids)]
    return "**Plan review:** " + "; ".join(parts)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="plan_review.py", description="Print the Plan review line of a plan /flow:draft reviewed.")
    parser.add_argument("plan", type=Path, help="the joined plan")
    args = parser.parse_args(argv)
    plan: Path = args.plan
    try:
        result = line(tiers(plan.read_text(encoding="utf-8"), drafts_dir(plan)))
    except (OSError, UnicodeDecodeError, FlowError, ReviewError) as err:
        print(f"plan_review.py: {plan}: {err}", file=sys.stderr)
        return 1
    print(result)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

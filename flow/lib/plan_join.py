#!/usr/bin/env python3
"""Rebuild a plan's stories from their drafts.

A plan skeleton holds no `## Story` heading. Its story list is the `## Stories`
section, one `- **Story <id>** ...` entry per story, and one line of the plan is
exactly MARKER. Everything after the marker is the join's output: each story's
draft, `S<id>.md` in the plan's drafts directory, in the order of the list. That
directory is `<drafts>/<plan stem>` under the worktree's top level, `<drafts>`
being the profile's `paths.drafts` (`.flow/drafts` by default), and
`flowconf.py drafts <plan stem>` makes it. The drafts stay the source; edit them
and join again. Marker and list lookups skip fenced code, so a plan can quote
the marker in an example. The story list is read from above the marker only: a
`## Stories` section below it is generated text.

Usage: plan_join.py PLAN [--drafts DIR]

Rewrites PLAN and writes DIR/join.json (DIR defaults to the plan's drafts
directory), which maps each story to the plan lines its draft landed on. Exit 0
when joined, 1 when a draft, the skeleton or the profile is wrong, 2 on a usage
error.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import flowconf  # noqa: E402
from check_plan import FENCE_RE, H2_RE, STORY_RE, repo_root  # noqa: E402
from flowconf import FlowError  # noqa: E402

MARKER = "<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->"
STORIES_HEADING = "## Stories"
ENTRY_RE = re.compile(r"^-\s+\*\*Story\s+([^\s*:.]+)\*\*(?:\s|$)")
DRAFT_RE = re.compile(r"^S([^.]+)\.md$")
# /flow:draft keeps its state file in the drafts directory (part 2 spec §4.4); it is not a story draft.
RESERVED = frozenset({"STATE.md"})


class JoinError(Exception):
    """The skeleton or a draft is wrong; the message is a sentence naming what."""


def describe(err: OSError | UnicodeDecodeError) -> str:
    """The reason an I/O or decode error gives, as a phrase."""
    return (err.strerror if isinstance(err, OSError) else None) or str(err)


def drafts_dir(plan: Path) -> Path:
    """The plan's drafts directory: flowconf's for the plan's worktree, then the plan's stem. FlowError on a profile it refuses."""
    top = repo_root(plan)
    return flowconf.drafts_dir(top, flowconf.optional_profile(top)) / plan.stem


def unfenced(text: str) -> list[tuple[int, str]]:
    """Each line outside a fenced code block as (0-based index, line); the fence lines are dropped."""
    kept: list[tuple[int, str]] = []
    fenced = False
    for i, line in enumerate(text.split("\n")):
        if FENCE_RE.match(line):
            fenced = not fenced
        elif not fenced:
            kept.append((i, line))
    return kept


def story_order(text: str) -> list[str]:
    """The ids of the `## Stories` entries, in order."""
    ids: list[str] = []
    found = False
    in_section = False
    for _, line in unfenced(text):
        if line.rstrip() == STORIES_HEADING:
            found = in_section = True
        elif H2_RE.match(line):
            in_section = False
        elif in_section:
            entry = ENTRY_RE.match(line)
            if entry:
                sid = entry.group(1)
                if sid in ids:
                    raise JoinError(f"Story {sid} is listed twice in the {STORIES_HEADING} section")
                ids.append(sid)
    if not found:
        raise JoinError(f"the plan has no {STORIES_HEADING} section")
    if not ids:
        raise JoinError(f"the {STORIES_HEADING} section lists no stories")
    return ids


def join(text: str, drafts: Path) -> tuple[str, dict[str, list[int]]]:
    """The plan with its stories rebuilt from the drafts, and where each draft landed.

    The map is `{id: [start, lines]}`: `start` is the 1-based plan line of the
    draft's first line (its `## Story` heading, unless the draft opens with blank
    lines) and `lines` the draft's line count once trailing whitespace is gone.
    A draft's line k is plan line `start + k - 1`.
    """
    lines = text.split("\n")
    marks = [i for i, line in unfenced(text) if line.rstrip() == MARKER]
    if not marks:
        raise JoinError(f"the plan has no marker line; add this line where the generated stories begin: {MARKER}")
    if len(marks) > 1:
        raise JoinError(f"the plan has {len(marks)} marker lines, expected one")
    # Only the head is the skeleton: a `## Stories` section after the marker is generated text.
    head_text = "\n".join(lines[: marks[0] + 1])
    try:
        order = story_order(head_text)
    except JoinError:
        below = any(i > marks[0] and line.rstrip() == STORIES_HEADING for i, line in unfenced(text))
        if below and not any(line.rstrip() == STORIES_HEADING for _, line in unfenced(head_text)):
            raise JoinError(f"the {STORIES_HEADING} section must sit above the marker line") from None
        raise
    files: dict[str, Path] = {}
    if drafts.is_dir():
        try:
            entries = sorted(drafts.iterdir())
        except OSError as err:
            raise JoinError(f"{drafts} cannot be read: {describe(err)}") from err
        for path in entries:
            if path.name in RESERVED:
                continue
            named = DRAFT_RE.match(path.name)
            if named and path.is_file():
                files[named.group(1)] = path
    for sid in order:
        if sid not in files:
            raise JoinError(f"Story {sid} is listed in the plan but S{sid}.md does not exist")
    for sid in files:
        if sid not in order:
            raise JoinError(f"S{sid}.md is not in the plan's story list")
    bodies: list[str] = []
    for sid in order:
        try:
            body = files[sid].read_text(encoding="utf-8").rstrip()
        except (OSError, UnicodeDecodeError) as err:
            raise JoinError(f"{files[sid]} cannot be read: {describe(err)}") from err
        first = next((line for line in body.split("\n") if line.strip()), "")
        heading = STORY_RE.match(first)
        if heading is None or heading.group(1) != sid:
            raise JoinError(f"S{sid}.md must start with '## Story {sid}' on its first non-blank line")
        bodies.append(body)
    head = lines[: marks[0] + 1]
    cursor = len(head)
    story_map: dict[str, list[int]] = {}
    for sid, body in zip(order, bodies):
        start = cursor + 2
        count = body.count("\n") + 1
        story_map[sid] = [start, count]
        cursor = start + count - 1
    joined = "\n".join(head) + "\n\n" + "\n\n".join(bodies) + "\n"
    # A draft that holds the marker outside a fence would make the next join see two markers.
    for index, line in unfenced(joined):
        if line.rstrip() != MARKER or index == marks[0]:
            continue
        number = index + 1
        for sid, (start, count) in story_map.items():
            if start <= number < start + count:
                raise JoinError(
                    f"S{sid}.md holds the marker line outside a fence (plan line {number}); quote it inside a fence"
                )
        raise JoinError(f"a draft holds the marker line outside a fence (plan line {number}); quote it inside a fence")
    return joined, story_map


def load_map(drafts: Path) -> dict[str, tuple[int, int]]:
    """`join.json` read back as `{id: (start, lines)}`; `{}` when it is missing."""
    path = drafts / "join.json"
    if not path.is_file():
        return {}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
        return {entry["id"]: (entry["start"], entry["lines"]) for entry in data["stories"]}
    except (ValueError, KeyError, TypeError) as err:
        raise JoinError(f"{path} is not a join map; re-run loom flow join") from err


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="plan_join.py", description="Rebuild a plan's stories from their drafts.")
    parser.add_argument("plan", type=Path, help="the plan to rewrite")
    parser.add_argument(
        "--drafts", type=Path, help="the drafts directory (default: <paths.drafts>/<plan stem> under the worktree's top level)"
    )
    args = parser.parse_args(argv)
    plan: Path = args.plan
    try:
        drafts: Path = args.drafts or drafts_dir(plan)
    except FlowError as err:
        print(f"FAIL [join] {plan}: {err}")
        return 1
    try:
        text = plan.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as err:
        print(f"FAIL [join] {plan}: {describe(err)}")
        return 1
    try:
        joined, story_map = join(text, drafts)
    except JoinError as err:
        print(f"FAIL [join] {plan}: {err}")
        return 1
    stories = [{"id": sid, "start": start, "lines": count} for sid, (start, count) in story_map.items()]
    # The map first: a failed map write leaves the plan untouched.
    try:
        (drafts / "join.json").write_text(json.dumps({"stories": stories}, indent=1) + "\n", encoding="utf-8")
        plan.write_text(joined, encoding="utf-8")
    except OSError as err:
        print(f"FAIL [join] {plan}: {describe(err)}")
        return 1
    total = joined.count("\n")
    print(f"joined {len(stories)} stories into {plan}: {total} lines")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

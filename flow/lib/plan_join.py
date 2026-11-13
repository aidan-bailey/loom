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

Each entry gives its story's opening lines as bullets nested under it (Developer, Files, Interfaces,
Verify and, when the story has it, Review), and a draft holds none of them: the join writes each
story as its draft's heading, a blank line, the entry's opening lines, a blank line, and the rest of
the draft. A draft written before that rule opens with a copy, which the join drops and reports.
An entry's **Reading** line and its optional **Drafts after** line (the stories whose drafts its
drafter reads, drafted in an earlier wave) are for the drafter: the join writes neither.

Usage: plan_join.py PLAN [--drafts DIR]

Rewrites PLAN and writes DIR/join.json (DIR defaults to the plan's drafts
directory), which maps each story to the plan lines its draft and its entry's opening lines landed on. Exit 0
when joined, 1 when a draft, the skeleton or the profile is wrong, 2 on a usage
error.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import NamedTuple

sys.path.insert(0, str(Path(__file__).resolve().parent))
import flowconf  # noqa: E402
from check_plan import FENCE_RE, FIELD_RE, FIELDS, H2_RE, STORY_RE, repo_root  # noqa: E402
from flowconf import FlowError  # noqa: E402

MARKER = "<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->"
STORIES_HEADING = "## Stories"
ENTRY_RE = re.compile(r"^-\s+\*\*Story\s+([^\s*:.]+)\*\*(?:\s|$)")
DRAFT_RE = re.compile(r"^S([^.]+)\.md$")
# /flow:draft keeps its state file in the drafts directory (part 2 spec §4.4); it is not a story draft.
RESERVED = frozenset({"STATE.md"})
READING_RE = re.compile(r"^\s*[-*]\s+\*\*Reading:\*\*")
# A story drafted in a later wave names the stories whose drafts it reads: `- **Drafts after:** Story 3, Story 6`.
DRAFTS_AFTER_RE = re.compile(r"^\s*[-*]\s+\*\*Drafts after:\*\*\s*(.*)$")
AFTER_WORD_RE = re.compile(r"\b(?:Stor(?:y|ies)|and)\b")
COPIED_NOTE = "dropped its copied opening lines; joined the entry's"


class JoinError(Exception):
    """The skeleton or a draft is wrong; the message is a sentence naming what."""


@dataclass
class Entry:
    """One `## Stories` entry: its id, its plan line, and the opening lines the join writes into its story."""

    sid: str
    line: int
    # Each opening line (Developer, Files, Interfaces, Verify, Review), stripped, in the entry's order.
    opening: list[str] = field(default_factory=list)
    fields: dict[str, str] = field(default_factory=dict)
    repeated: list[str] = field(default_factory=list)
    reading: bool = False
    # The ids its **Drafts after:** line names (only the drafter reads it; the join writes it nowhere), or None without one.
    after: list[str] | None = None

    def missing(self) -> list[str]:
        """The required opening lines the entry lacks, in FIELDS order."""
        return [name for name in FIELDS if not self.fields.get(name)]


class Span(NamedTuple):
    """Where a story landed: its draft line k after the heading is plan line `start + k - 1`, and
    `entry` is the first and last plan line of the opening lines the join wrote from its entry."""

    start: int
    lines: int
    entry: tuple[int, int]


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


def entries(text: str) -> list[Entry]:
    """The `## Stories` entries, in order, each with the opening lines nested under it."""
    listed: list[Entry] = []
    current: Entry | None = None
    found = False
    in_section = False
    for index, line in unfenced(text):
        if line.rstrip() == STORIES_HEADING:
            found = in_section = True
            current = None
        elif H2_RE.match(line):
            in_section = False
            current = None
        elif in_section:
            entry = ENTRY_RE.match(line)
            if entry:
                sid = entry.group(1)
                if any(e.sid == sid for e in listed):
                    raise JoinError(f"Story {sid} is listed twice in the {STORIES_HEADING} section")
                current = Entry(sid, index + 1)
                listed.append(current)
            elif current is not None:
                opening = FIELD_RE.match(line)
                if opening:
                    name = opening.group(1)
                    if name in current.fields:
                        current.repeated.append(name)
                    else:
                        current.fields[name] = opening.group(2).strip()
                        current.opening.append(line.strip())
                elif READING_RE.match(line):
                    current.reading = True
                elif (later := DRAFTS_AFTER_RE.match(line)):
                    if current.after is not None:
                        current.repeated.append("Drafts after")
                    else:
                        current.after = after_ids(later.group(1))
    if not found:
        raise JoinError(f"the plan has no {STORIES_HEADING} section")
    if not listed:
        raise JoinError(f"the {STORIES_HEADING} section lists no stories")
    return listed


def after_ids(text: str) -> list[str]:
    """The story ids a **Drafts after:** line names: `Story 3, Story 6`, `Stories 3 and 6` or `3, 6`."""
    return [token.strip("`.") for token in re.split(r"[,\s]+", AFTER_WORD_RE.sub(" ", text)) if token.strip("`.")]


def story_order(text: str) -> list[str]:
    """The ids of the `## Stories` entries, in order."""
    return [entry.sid for entry in entries(text)]


def join(text: str, drafts: Path) -> tuple[str, dict[str, Span], list[str]]:
    """The plan with its stories rebuilt from the drafts and the entries, where each landed, and the notes.

    Each story is its draft's heading, a blank line, its entry's opening lines, and the draft from its
    second line on, a blank line between: the draft's own blank line when it has one. Leading blank
    lines are dropped, and so is a block of opening lines copied into the draft, with a note
    (`S<id>.md: dropped its copied opening lines; joined the entry's`). A draft line k after the
    heading is plan line `start + k - 1`, so `k = n - start + 1` still turns a plan line into a draft
    line; `entry` gives the plan lines of the opening lines.
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
        listed = entries(head_text)
    except JoinError:
        below = any(i > marks[0] and line.rstrip() == STORIES_HEADING for i, line in unfenced(text))
        if below and not any(line.rstrip() == STORIES_HEADING for _, line in unfenced(head_text)):
            raise JoinError(f"the {STORIES_HEADING} section must sit above the marker line") from None
        raise
    for entry in listed:
        gaps = entry.missing()
        if gaps:
            raise JoinError(
                f"the Story {entry.sid} entry lacks {', '.join(f'**{name}:**' for name in gaps)}: "
                "an entry gives every opening line the join writes into its story"
            )
        if entry.repeated:
            raise JoinError(f"the Story {entry.sid} entry gives {', '.join(f'**{name}:**' for name in entry.repeated)} twice")
    order = [entry.sid for entry in listed]
    by_id = {entry.sid: entry for entry in listed}
    files: dict[str, Path] = {}
    if drafts.is_dir():
        try:
            listing = sorted(drafts.iterdir())
        except OSError as err:
            raise JoinError(f"{drafts} cannot be read: {describe(err)}") from err
        for path in listing:
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
    stories: list[str] = []
    story_map: dict[str, Span] = {}
    notes: list[str] = []
    for sid, body in zip(order, bodies):
        draft = body.split("\n")
        h = next(i for i, line in enumerate(draft) if line.strip())
        rest = h + 1
        text_at = next((i for i in range(rest, len(draft)) if draft[i].strip()), len(draft))
        copied_end = text_at
        while copied_end < len(draft) and FIELD_RE.match(draft[copied_end]):
            copied_end += 1
        if copied_end > text_at:
            notes.append(f"S{sid}.md: {COPIED_NOTE}")
            rest = copied_end
        opening = by_id[sid].opening
        block = ["", *opening]
        if rest < len(draft) and draft[rest].strip():
            block.append("")
        heading = cursor + 2
        start = heading + 1 + len(block) - rest
        story_map[sid] = Span(start, len(draft), (heading + 2, heading + 1 + len(opening)))
        stories.append("\n".join([draft[h], *block, *draft[rest:]]))
        cursor = heading + len(block) + len(draft) - rest
    joined = "\n".join(head) + "\n\n" + "\n\n".join(stories) + "\n"
    # A draft that holds the marker outside a fence would make the next join see two markers.
    for index, line in unfenced(joined):
        if line.rstrip() != MARKER or index == marks[0]:
            continue
        number = index + 1
        for sid, span in story_map.items():
            if span.entry[0] - 2 <= number <= span.start + span.lines - 1:
                raise JoinError(
                    f"S{sid}.md holds the marker line outside a fence (plan line {number}); quote it inside a fence"
                )
        raise JoinError(f"a draft holds the marker line outside a fence (plan line {number}); quote it inside a fence")
    return joined, story_map, notes


def load_map(drafts: Path) -> dict[str, Span]:
    """`join.json` read back as `{id: Span}`; `{}` when it is missing."""
    path = drafts / "join.json"
    if not path.is_file():
        return {}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
        return {
            entry["id"]: Span(entry["start"], entry["lines"], (entry["entry"][0], entry["entry"][1]))
            for entry in data["stories"]
        }
    except (ValueError, KeyError, TypeError, IndexError) as err:
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
        joined, story_map, notes = join(text, drafts)
    except JoinError as err:
        print(f"FAIL [join] {plan}: {err}")
        return 1
    stories = [
        {"id": sid, "start": span.start, "lines": span.lines, "entry": list(span.entry)} for sid, span in story_map.items()
    ]
    # The map first: a failed map write leaves the plan untouched.
    try:
        (drafts / "join.json").write_text(json.dumps({"stories": stories}, indent=1) + "\n", encoding="utf-8")
        plan.write_text(joined, encoding="utf-8")
    except OSError as err:
        print(f"FAIL [join] {plan}: {describe(err)}")
        return 1
    for note in notes:
        print(note)
    total = joined.count("\n")
    print(f"joined {len(stories)} stories into {plan}: {total} lines")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

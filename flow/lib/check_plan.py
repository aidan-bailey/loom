#!/usr/bin/env python3
"""Check that a plan is ready for the `supervised` execution option.

A supervised plan names its spec before the first story, in a `**Spec:**` (or
`**Design:**`) line holding the spec's repo-relative path in backticks, and
groups its tasks into stories. Each story opens with four required lines (and an optional Review
line, below) the plan-execution-supervisor routes, parallelises and reviews from:

    ## Story 2: Wire the ledger route

    - **Developer:** intermediate-dev
    - **Files:** `server/src/routes/ledger.rs`, `server/tests/ledger.rs`
    - **Interfaces:** exposes `GET /ledger/{address}`; uses `LedgerStore` from Story 1
    - **Verify:** `cargo nextest run -p server ledger`

    ### Task 2.1: ...

A plan whose code was reviewed before it was committed (the dispatch guide's
plan review) records it in a `**Plan review:**` line before the first story.
The supervisor reads it: a story the plan review vouches for (below) gets the narrow
story-reviewer, which reads only what the plan doesn't vouch for; any other
gets the full code-quality review.

A story may carry a fifth opening line, `- **Review:** deep` or `- **Review:** light`, either with
a reason in parentheses. A story without it is deep. A light story whose fenced code holds one of
the profile's landmine markers fails the check unless its reason names the marker. A Plan review line
may list the deep and light story ids as `deep: 1, 6; light: 2, 3`: a story it lists under the
wrong tier, or not at all, fails the check, as does a token under either list that is no story id
(ids are listed one at a time, on one physical line), and so does a light story under a line that
lists no tiers at all. The output says per story whether the plan review vouches for it (a deep story
the line lists, or a deep story of a line written before tiers).

A plan may declare, before its first story, the pairs of stories whose second story's developer runs in
a lane beside the first under loom flow run: `**Parallel:** 3+4, 7+8`. Each pair is a story and the one
right after it, with disjoint Files and at most one exclusive build; a pair that isn't fails the check,
as does a second Parallel line. --json marks each pair's second story `"lane": true`.

Usage: check_plan.py PLAN.md [PLAN.md ...]
       check_plan.py --json PLAN.md

The markers and the exclusive builds come from the profile of the repo holding each plan
(flowconf.py); a plan whose repo has no usable profile fails. For a plan that passes it prints the
spec's path, its plan review, each story's developer, file count, whether its Verify line holds an
exclusive build, its tier, whether the plan review vouches for it and the landmine markers its code
holds, then the pairs of stories that may run at once: disjoint files and at most one exclusive
build. With --json, a plan that passes prints one JSON object instead: its spec, its plan review and
each story's id, first and last plan lines, developer, tier, review column, landmines, Interfaces and
Verify lines, which loom flow run reads (flow in loom part 3b); a failing one prints its FAIL lines.
Exit 0 when every plan passes, 1 when any fails, 2 on a usage error.
"""

from __future__ import annotations

import itertools
import json
import posixpath
import re
import sys
from collections.abc import Sequence
from dataclasses import dataclass, field
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import flowconf  # noqa: E402
from flowconf import FlowError, load_profile  # noqa: E402

DEVELOPERS = ("junior-dev", "intermediate-dev", "senior-dev")
# Opus writes code only as a last resort (owner, 2026-10-13): the supervisor dispatches
# senior-dev after an intermediate-dev fails a slice twice, so a plan shouldn't assign it.
LAST_RESORT = {"senior-dev"}
FIELDS = ("Developer", "Files", "Interfaces", "Verify")
OPTIONAL = ("Review",)

STORY_RE = re.compile(r"^##\s+Story\s+([^\s:.]+)[:.]?\s*(.*)$")
H2_RE = re.compile(r"^##\s")
TASK_RE = re.compile(r"^###\s")
FIELD_RE = re.compile(
    r"^\s*[-*]\s+\*\*(" + "|".join(re.escape(name) for name in FIELDS + OPTIONAL) + r"):\*\*\s*(.*)$"
)
SPEC_RE = re.compile(r"^\*\*(?:Spec|Design):\*\*\s*(.*)$")
REVIEW_RE = re.compile(r"^\*\*Plan review:\*\*\s*(.*)$")
PARALLEL_RE = re.compile(r"^\*\*Parallel:\*\*\s*(.*)$")
PAIR_RE = re.compile(r"^([^\s+,]+)\s*\+\s*([^\s+,]+)$")
FENCE_RE = re.compile(r"^\s*(```|~~~)")
CODE_RE = re.compile(r"`([^`]+)`")

# The deep criteria are the profile's landmines.markers: a light story's fenced code may hold one
# only when the story's Review reason names it.

REVIEW_VALUE_RE = re.compile(r"^(deep|light)(?:\s*\((.*)\))?$")
TIER_RE = re.compile(r"\b(deep|light):([^;]*)", re.IGNORECASE)


@dataclass
class Story:
    """One `## Story` section: its header fields and how many tasks it holds."""

    sid: str
    line: int
    fields: dict[str, str] = field(default_factory=dict)
    # The story's last plan line: the line before the next story or H2 heading, or the plan's last.
    end: int = 0
    tasks: int = 0
    # Each fenced line inside the story's section, with its plan line number.
    code: list[tuple[int, str]] = field(default_factory=list)

    @property
    def files(self) -> set[str]:
        # Normalised, so `./src/x.rs` and `src/x.rs` are one file.
        return {posixpath.normpath(path) for path in CODE_RE.findall(self.fields.get("Files", ""))}

    def builds(self, exclusive: Sequence[str]) -> bool:
        """Whether the story's Verify line holds one of the profile's exclusive builds."""
        verify = self.fields.get("Verify", "")
        return any(entry in verify for entry in exclusive)

    @property
    def tier(self) -> str:
        """`deep` or `light`; `deep` when there is no Review line or its value doesn't parse."""
        value = REVIEW_VALUE_RE.match(self.fields.get("Review", ""))
        return value.group(1) if value else "deep"

    @property
    def reason(self) -> str:
        """The parenthesised text of the Review line, else an empty string."""
        value = REVIEW_VALUE_RE.match(self.fields.get("Review", ""))
        return (value.group(2) or "").strip() if value else ""


def parse(text: str) -> tuple[list[Story], list[str], str | None]:
    """Split a plan into stories, reading header fields up to each story's first task."""
    stories: list[Story] = []
    problems: list[str] = []
    spec: str | None = None
    current: Story | None = None
    in_header = False
    fenced = False
    lines = text.splitlines()
    for n, line in enumerate(lines, 1):
        if FENCE_RE.match(line):
            fenced = not fenced
            continue
        if fenced:
            if current is not None:
                current.code.append((n, line))
            continue
        if current is not None and (STORY_RE.match(line) or H2_RE.match(line)):
            current.end = n - 1
        named = SPEC_RE.match(line)
        if named and not stories and spec is None:
            paths = CODE_RE.findall(named.group(1))
            spec = paths[0] if paths else ""
        story = STORY_RE.match(line)
        if story:
            current = Story(story.group(1), n)
            stories.append(current)
            in_header = True
        elif H2_RE.match(line):
            current, in_header = None, False
        elif TASK_RE.match(line):
            if current is None:
                problems.append(f"line {n}: task heading outside any story: {line.strip()}")
            else:
                current.tasks += 1
                in_header = False
        elif current is not None and in_header:
            header = FIELD_RE.match(line)
            if header:
                current.fields[header.group(1)] = header.group(2).strip()
    if current is not None:
        current.end = len(lines)
    return stories, problems, spec


def plan_review(text: str) -> str | None:
    """The plan's `**Plan review:**` line, if one comes before the first story."""
    fenced = False
    for line in text.splitlines():
        if FENCE_RE.match(line):
            fenced = not fenced
        elif not fenced:
            if STORY_RE.match(line):
                return None
            review = REVIEW_RE.match(line)
            if review:
                return review.group(1).strip() or None
    return None


def declared_pairs(text: str) -> tuple[list[tuple[str, str]], list[str]]:
    """The pairs the plan's `**Parallel:**` line declares before its first story, and what keeps the line from parsing."""
    pairs: list[tuple[str, str]] = []
    problems: list[str] = []
    seen = False
    fenced = False
    for n, line in enumerate(text.splitlines(), 1):
        if FENCE_RE.match(line):
            fenced = not fenced
            continue
        if fenced:
            continue
        if STORY_RE.match(line):
            break
        declared = PARALLEL_RE.match(line)
        if not declared:
            continue
        if seen:
            problems.append(f"line {n}: a second **Parallel:** line: declare every pair on one line")
            continue
        seen = True
        for entry in (e.strip() for e in declared.group(1).strip().removesuffix(".").split(",")):
            pair = PAIR_RE.match(entry)
            if pair:
                pairs.append((pair.group(1), pair.group(2)))
            else:
                problems.append(f"line {n}: the **Parallel:** entry '{entry}' is not two story ids joined by +, as in 3+4")
    return pairs, problems


def markers_in(story: Story, markers: Sequence[str]) -> list[tuple[str, int]]:
    """Each marker found in the story's fenced code, with the first plan line holding it, in `markers` order."""
    found: list[tuple[str, int]] = []
    for marker in markers:
        for number, line in story.code:
            if marker in line:
                found.append((marker, number))
                break
    return found


def review_tiers(review: str | None) -> tuple[set[str], set[str]] | None:
    """The story ids after `deep:` and after `light:` in a Plan review line.

    Each list runs to the next `;` or the end of the line, its ids separated by commas and
    spaces, one trailing `.` stripped from each. None when the line has neither token (a line
    written before tiers) or is missing.
    """
    if review is None:
        return None
    found = TIER_RE.findall(review)
    if not found:
        return None
    ids: dict[str, set[str]] = {"deep": set(), "light": set()}
    for tier, rest in found:
        ids[tier.lower()].update(token.removesuffix(".") for token in re.split(r"[,\s]+", rest) if token.removesuffix("."))
    return ids["deep"], ids["light"]


def vouched(review: str | None, stories: list[Story]) -> dict[str, bool]:
    """Whether the plan review vouches for each story: no line, none; a line without tiers, the deep stories; else the deep ids."""
    if review is None:
        return {story.sid: False for story in stories}
    tiers = review_tiers(review)
    if tiers is None:
        return {story.sid: story.tier == "deep" for story in stories}
    deep, _ = tiers
    return {story.sid: story.sid in deep for story in stories}


def repo_root(path: Path) -> Path:
    for parent in [path.resolve().parent, *path.resolve().parents]:
        if (parent / ".git").exists():
            return parent
    return flowconf.toplevel(Path.cwd())


def tier_problems(stories: list[Story], review: str | None) -> list[str]:
    """What a Plan review line's deep:/light: lists get wrong against the stories; none without a line."""
    if review is None:
        return []
    tiers = review_tiers(review)
    problems: list[str] = []
    if tiers is None:
        # A line without tiers vouches only for deep stories, so a light story would get no review.
        for story in stories:
            if story.tier == "light":
                problems.append(
                    f"Story {story.sid} (line {story.line}): light, but the Plan review line lists no "
                    "deep:/light: tiers: list every story under one"
                )
        return problems
    known = {story.sid for story in stories}
    for name, ids in zip(("deep", "light"), tiers):
        for token in sorted(ids - known):
            problems.append(
                f"the Plan review line lists {token} under {name}:, which is no story id "
                "(list ids one at a time, on one physical line: no ranges, punctuation or backticks)"
            )
    for story in stories:
        listed = [name for name, ids in zip(("deep", "light"), tiers) if story.sid in ids]
        if not listed:
            problems.append(f"Story {story.sid}: the Plan review line lists it under neither deep: nor light:")
        elif len(listed) == 2:
            problems.append(f"Story {story.sid}: the Plan review line lists it under both deep: and light:")
        elif "Review" in story.fields and not REVIEW_VALUE_RE.match(story.fields["Review"]):
            continue  # its Review value already fails the check
        elif listed != [story.tier]:
            says = f"its Review line says {story.tier}" if "Review" in story.fields else f"its tier is {story.tier} (no Review line)"
            problems.append(f"Story {story.sid}: the Plan review line lists it as {listed[0]}, {says}")
    return problems


def check(text: str, plan: Path, markers: Sequence[str]) -> tuple[list[Story], list[str], str | None]:
    """Parse a plan and list everything that keeps it from a supervised run."""
    stories, problems, spec = parse(text)
    if spec is None:
        problems.append("no **Spec:** line before the first story: the supervisor and a fresh manager find the spec through it")
    elif not spec:
        problems.append("the **Spec:** line names no `backticked` path")
    elif not (repo_root(plan) / spec).is_file():
        problems.append(f"the spec {spec} does not exist")
    if not stories:
        problems.append('no "## Story" headings: a supervised plan groups its tasks into stories')
    seen: set[str] = set()
    for story in stories:
        where = f"Story {story.sid} (line {story.line})"
        if story.sid in seen:
            problems.append(f"{where}: duplicate story id")
        seen.add(story.sid)
        for name in FIELDS:
            if not story.fields.get(name):
                problems.append(f"{where}: missing **{name}:** line before its first task")
        developer = story.fields.get("Developer", "").strip("` ").removeprefix("flow:")
        if developer and developer not in DEVELOPERS:
            problems.append(f"{where}: Developer '{developer}' is not one of {', '.join(DEVELOPERS)}")
        if story.fields.get("Files") and not story.files:
            problems.append(f"{where}: **Files:** names no `backticked` path")
        if story.fields.get("Verify") and not CODE_RE.search(story.fields["Verify"]):
            problems.append(f"{where}: **Verify:** names no `backticked` command")
        if story.tasks == 0:
            problems.append(f"{where}: no ### task headings")
    for story in stories:
        where = f"Story {story.sid} (line {story.line})"
        if "Review" in story.fields and not REVIEW_VALUE_RE.match(story.fields["Review"]):
            problems.append(f"{where}: **Review:** must be deep or light, optionally with a reason in parentheses")
        if story.tier == "light":
            for marker, at in markers_in(story, markers):
                if marker not in story.reason:
                    problems.append(
                        f"{where}: light, but its code has {marker} (line {at}): "
                        "make it deep, or name the marker in the Review line's reason"
                    )
    problems += tier_problems(stories, plan_review(text))
    return stories, problems, spec


def parallel_safe(stories: list[Story], exclusive: Sequence[str]) -> list[tuple[str, str]]:
    """Pairs of stories with disjoint files of which at most one holds an exclusive build."""
    return [
        (a.sid, b.sid)
        for a, b in itertools.combinations(stories, 2)
        if not (a.files & b.files) and not (a.builds(exclusive) and b.builds(exclusive))
    ]


def pair_problems(stories: list[Story], pairs: list[tuple[str, str]], exclusive: Sequence[str]) -> list[str]:
    """What keeps each declared pair's second story from running its developer in a lane beside its first."""
    index = {story.sid: i for i, story in enumerate(stories)}
    problems: list[str] = []
    seen: set[tuple[str, str]] = set()
    for a, b in pairs:
        name = f"the **Parallel:** pair {a}+{b}"
        if (a, b) in seen:
            problems.append(f"{name} is declared twice")
            continue
        seen.add((a, b))
        missing = [sid for sid in (a, b) if sid not in index]
        if missing:
            problems.append(f"{name} names {', '.join('Story ' + sid for sid in missing)}, which the plan doesn't have")
            continue
        first, second = stories[index[a]], stories[index[b]]
        if index[b] != index[a] + 1:
            problems.append(f"{name}: Story {b} must be the story right after Story {a}")
        shared = sorted(first.files & second.files)
        if shared:
            problems.append(f"{name}: both stories' **Files** name {', '.join(shared)}")
        if first.builds(exclusive) and second.builds(exclusive):
            problems.append(f"{name}: both stories' **Verify** run an exclusive build")
    return problems


def story_json(story: Story, vouch: dict[str, bool], markers: Sequence[str], lane: bool = False) -> dict:
    """A story as `--json` gives it, for loom flow run (flow in loom part 3b); a laned story carries `"lane": true`."""
    out = {
        "id": story.sid, "line": story.line, "end": story.end,
        "developer": story.fields["Developer"].strip("` ").removeprefix("flow:"),
        "tier": story.tier, "review": "vouched" if vouch[story.sid] else "full",
        "landmines": [marker for marker, _ in markers_in(story, markers)],
        "interfaces": story.fields.get("Interfaces", ""), "verify": story.fields.get("Verify", ""),
    }
    if lane:
        out["lane"] = True
    return out


def main(argv: list[str]) -> int:
    as_json = bool(argv) and argv[0] == "--json"
    if as_json:
        argv = argv[1:]
    if not argv or (as_json and len(argv) != 1):
        print("usage: check_plan.py [--json PLAN.md | PLAN.md [PLAN.md ...]]", file=sys.stderr)
        return 2
    failed = False
    for arg in argv:
        try:
            text = Path(arg).read_text()
        except OSError as err:
            print(f"FAIL [plan] {arg}: {err.strerror}")
            failed = True
            continue
        try:
            plan_repo = repo_root(Path(arg))
            plan_main, cwd_main = flowconf.main_checkout(plan_repo), flowconf.main_checkout(Path.cwd())
        except FlowError as err:
            print(f"FAIL [repo] {arg}: {err}")
            failed = True
            continue
        if plan_main != cwd_main:
            # Every flowconf command reads the profile of the repo the session runs in, so a plan elsewhere would be checked against another profile.
            print(f"FAIL [repo] {arg}: the plan is in {plan_main} but this session is in {cwd_main}; keep the plan in the repo it changes")
            failed = True
            continue
        try:
            profile = load_profile(plan_repo)
        except FlowError as err:
            print(f"FAIL [profile] {arg}: {err}")
            failed = True
            continue
        stories, problems, spec = check(text, Path(arg), profile.markers)
        pairs, pair_errors = declared_pairs(text)
        problems += pair_errors + pair_problems(stories, pairs, profile.exclusive)
        if problems:
            failed = True
            for problem in problems:
                print(f"FAIL [plan] {arg}: {problem}")
            continue
        review = plan_review(text)
        if as_json:
            vouch = vouched(review, stories)
            laned = {b for _, b in pairs}
            print(json.dumps({"spec": spec, "plan_review": review,
                              "stories": [story_json(s, vouch, profile.markers, s.sid in laned) for s in stories]}))
            continue
        print(f"OK: {arg}: {len(stories)} stories; spec {spec}")
        print(f"  Plan review: {review}" if review else "  Plan review: none recorded, so every story gets the full code-quality review")
        vouch = vouched(review, stories)
        for story in stories:
            developer = story.fields["Developer"].strip("` ").removeprefix("flow:")
            if developer in LAST_RESORT:
                print(f"  WARN Story {story.sid}: names {developer}, the last resort. Opus writes code only after an "
                      "intermediate-dev fails a slice twice: name intermediate-dev and settle in the plan what the story leaves to judgment")
            build = "build" if story.builds(profile.exclusive) else "no build"
            review_col = "review: vouched" if vouch[story.sid] else "review: full"
            found = ", ".join(marker for marker, _ in markers_in(story, profile.markers))
            landmines = f"  landmines: {found}" if found else ""
            print(f"  Story {story.sid:<4} {developer:<17} {len(story.files):>3} files  {build:<8}  {story.tier:<5}  {review_col}{landmines}")
        safe = parallel_safe(stories, profile.exclusive)
        listed = ", ".join(f"{a}+{b}" for a, b in safe) if safe else "none"
        print(f"  Parallel-safe pairs (disjoint files, at most one exclusive build): {listed}")
        declared = ", ".join(f"{a}+{b}" for a, b in pairs) if pairs else "none, so every story runs in order"
        print(f"  Declared pairs (the second story's developer runs in a lane): {declared}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

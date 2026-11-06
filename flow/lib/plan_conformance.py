#!/usr/bin/env python3
"""Show which of a story's plan code landed as written, and what else changed.

A supervised plan gives most of a story's code, and the plan's code is reviewed
before it is committed. Once a developer has built the story, this script
looks for each block the story adds, replaces with or creates in the files
the story changed, at the story's last commit, and maps the story's diff onto
the blocks it found. A story reviewer then reads only what the plan doesn't
vouch for: blocks that didn't land as written, and changed lines no block
covers.

A block matches line for line, ignoring indentation, or with all whitespace
removed when rustfmt has rewrapped it. A long block that doesn't match whole
(a file the plan writes in two steps, or one a developer changed in a few
places) is aligned line by line with its file: the lines that align count as
landed, and the rest of the file's changes are left for the reviewer. Blocks
fenced as `bash`, `sh`, `console` or `text` are commands and output, not code,
and are skipped.

Usage: plan_conformance.py PLAN.md --story ID --base BASE [--head HEAD]

BASE is the commit before the story's first; HEAD defaults to HEAD. Exit 0
when every block landed as written and every changed line outside generated
files is covered, 1 otherwise, 2 on a usage error.
"""

from __future__ import annotations

import argparse
import difflib
import fnmatch
import posixpath
import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from check_plan import parse as parse_stories  # noqa: E402
from check_plan import repo_root  # noqa: E402
from plan_premises import ELIDED, Plan, Tree, Walker, find_span, gather  # noqa: E402
from flowconf import FlowError, load_profile  # noqa: E402

COMMAND_FENCES = {"bash", "sh", "shell", "console", "text", "txt"}
# Lockfiles a story regenerates rather than writes: their lines are counted, not listed. A repo
# adds its own generated files with its profile's `generated` globs.
COMMON_LOCKFILES = {"Cargo.lock", "pnpm-lock.yaml", "package-lock.json", "uv.lock", "go.sum", "flake.lock"}


def is_generated(path: str, globs: tuple[str, ...]) -> bool:
    """Whether a changed file is generated: a common lockfile, or a match for one of the profile's
    globs, tried on the whole path and on the file's name."""
    name = posixpath.basename(path)
    return name in COMMON_LOCKFILES or any(fnmatch.fnmatch(path, glob) or fnmatch.fnmatch(name, glob) for glob in globs)


HUNK_RE = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@")
# A block this long that doesn't match whole is aligned line by line instead.
ALIGN_FROM = 8
# Share of a block's lines that must align for it to count as landed with changes.
LANDED_SHARE = 0.5


class Landing(Walker):
    """Collects a story's code blocks and the file each one names."""

    def __init__(self, plan: Plan, tree: Tree, story: str):
        super().__init__(plan, tree, {story})
        # (plan line, kind, target path or None, block lines)
        self.blocks: list[tuple[int, str, str | None, list[str]]] = []

    def on_block(self, start: int, intro: str, block: list[str], kind: str, lang: str):
        if kind == "current":
            target = self.target(intro, kind)
            found = self.tree.resolve(target) if target else []
            return set(), found[0] if len(found) == 1 else None
        if lang.lower() not in COMMAND_FENCES and any(line.strip() for line in block):
            self.blocks.append((start, kind, self.target(intro, kind), block))
        return None


def added_lines(root: Path, base: str, head: str) -> dict[str, list[int]]:
    """The non-blank lines the diff adds, per file, numbered in the file at HEAD."""
    out = subprocess.run(
        ["git", "-C", str(root), "diff", "--unified=0", "--no-color", "--no-ext-diff", base, head],
        capture_output=True, text=True, check=True,
    ).stdout
    added: dict[str, list[int]] = {}
    path: str | None = None
    number = 0
    for line in out.splitlines():
        if line.startswith("+++ "):
            path = line[6:] if line.startswith("+++ b/") else None
            continue
        hunk = HUNK_RE.match(line)
        if hunk:
            number = int(hunk.group(1))
            continue
        if path and line.startswith("+"):
            if line[1:].strip():
                added.setdefault(path, []).append(number)
            number += 1
    return added


def align(block: list[str], lines: list[str]) -> tuple[set[int], int, int]:
    """Align a block's non-blank lines with a file's, ignoring indentation.

    Returns the file lines (1-based) that aligned, how many block lines did,
    and how many there were. A lone aligned line counts toward the share only
    when it is long enough to be specific: a `}` aligns anywhere. A stretch
    where the lines differ but match with whitespace removed is rustfmt
    rewrapping, and counts as aligned.
    """
    ours = [line.strip() for line in block if line.strip() and line.strip() not in ELIDED]
    theirs = [(n, line.strip()) for n, line in enumerate(lines, 1) if line.strip()]
    matcher = difflib.SequenceMatcher(None, ours, [text for _, text in theirs], autojunk=False)
    covered: set[int] = set()
    aligned = 0
    for op, a1, a2, b1, b2 in matcher.get_opcodes():
        if op == "equal":
            covered.update(theirs[b][0] for b in range(b1, b2))
            if a2 - a1 >= 2 or len(ours[a1]) >= 30:
                aligned += a2 - a1
        elif op == "replace" and squeeze(ours[a1:a2]) == squeeze(text for _, text in theirs[b1:b2]):
            covered.update(theirs[b][0] for b in range(b1, b2))
            aligned += a2 - a1
    return covered, aligned, len(ours)


def squeeze(lines) -> str:
    return "".join("".join(line.split()) for line in lines)


def ranges(numbers: list[int]) -> str:
    spans: list[list[int]] = []
    for n in sorted(numbers):
        if spans and n == spans[-1][1] + 1:
            spans[-1][1] = n
        else:
            spans.append([n, n])
    return ", ".join(f"{a}" if a == b else f"{a}-{b}" for a, b in spans)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="plan_conformance.py", description=__doc__.split("\n\n")[0])
    parser.add_argument("plan", type=Path)
    parser.add_argument("--story", required=True, help="the story's id, as in its ## Story heading")
    parser.add_argument("--base", required=True, help="the commit before the story's first")
    parser.add_argument("--head", default="HEAD", help="the story's last commit (default HEAD)")
    try:
        args = parser.parse_args(argv)
    except SystemExit as err:
        return 2 if err.code else 0
    try:
        text = args.plan.read_text()
    except OSError as err:
        print(f"plan_conformance: {args.plan}: {err.strerror}", file=sys.stderr)
        return 2
    try:
        root = repo_root(args.plan)
        profile = load_profile(root)
    except FlowError as err:
        print(f"plan_conformance: {err}", file=sys.stderr)
        return 2
    try:
        tree = Tree(root, args.head)
        added = added_lines(root, args.base, args.head)
    except subprocess.CalledProcessError as err:
        print(f"plan_conformance: git failed in {root}: {err.stderr.strip()}", file=sys.stderr)
        return 2
    plan = gather(text, tree.ignored_root)
    line = next((n for sid, n in plan.stories if sid == args.story), None)
    if line is None:
        print(f"plan_conformance: no Story {args.story} in {args.plan}", file=sys.stderr)
        return 2
    landing = Landing(plan, tree, args.story)
    landing.walk()

    covered: dict[str, set[int]] = {}
    landed, partial, missed, unplaced = 0, [], [], 0
    changed = sorted(added)
    for start, kind, target, block in landing.blocks:
        resolved = tree.resolve(target) if target else []
        candidates = list(dict.fromkeys(resolved[:1] + changed))
        span = path = None
        for candidate in candidates:
            lines = tree.lines(candidate)
            span = find_span(lines, block) if lines is not None else None
            if span:
                path = candidate
                break
        if span and path:
            landed += 1
            covered.setdefault(path, set()).update(range(span[0], span[1] + 1))
            continue
        first = next((l.strip() for l in block if l.strip()), "")
        best = None
        if len([l for l in block if l.strip()]) >= ALIGN_FROM:
            for candidate in candidates:
                lines = tree.lines(candidate)
                if lines is not None:
                    found = align(block, lines)
                    if best is None or found[1] > best[1][1]:
                        best = (candidate, found)
        if best and best[1][1] >= LANDED_SHARE * best[1][2]:
            path, (lines_covered, aligned, total) = best
            covered.setdefault(path, set()).update(lines_covered)
            partial.append(f"  plan:{start}  {kind:<7} {path}: {aligned} of {total} lines landed as written (block starting `{first[:50]}`)")
        elif kind in ("replace", "add", "new"):
            missed.append(f"  plan:{start}  {kind:<7} {target or 'file?'}: block starting `{first[:60]}` is not there as written")
        else:
            unplaced += 1

    story_files = next((s.files for s in parse_stories(text)[0] if s.sid == args.story), set())
    uncovered, generated = [], []
    for path in changed:
        rest = [n for n in added[path] if n not in covered.get(path, set())]
        if is_generated(path, profile.generated):
            generated.append(f"{path} ({len(added[path])} lines)")
        elif rest:
            uncovered.append((path, rest))
    outside = [p for p in changed if posixpath.normpath(p) not in story_files and not is_generated(p, profile.generated)]

    print(f"plan_conformance: Story {args.story} (plan:{line}), {args.base}..{args.head}")
    print(f"Plan code: {landed} blocks landed as written, {len(partial)} with changes, {len(missed)} not at all"
          + (f"; {unplaced} other blocks not in the changed files (examples, or text outside the code)" if unplaced else ""))
    for entry in partial + missed:
        print(entry)
    total = sum(len(rest) for _, rest in uncovered)
    print(f"Changed lines no plan block covers (read these): {total} in {len(uncovered)} files")
    for path, rest in uncovered:
        print(f"  {path}: {ranges(rest)}")
    if generated:
        print(f"Generated, not listed: {', '.join(generated)}")
    print(f"Changed files the story's **Files** line doesn't name: {', '.join(outside) if outside else 'none'}")
    found = uncovered_markers(tree, uncovered, profile.markers)
    if found:
        print(f"MARKERS: {', '.join(found)}")
    return 1 if missed or uncovered else 0


def uncovered_markers(tree: "Tree", uncovered: list[tuple[str, list[int]]], markers: list[str]) -> list[str]:
    """The profile's landmine markers an uncovered changed line holds, in the profile's order: a vouched
    story with one gets the full review (flow in loom part 3b's engine reads the MARKERS line)."""
    found = set()
    for path, rest in uncovered:
        lines = tree.lines(path) or []
        for n in rest:
            if 0 < n <= len(lines):
                found.update(m for m in markers if m in lines[n - 1])
    return [m for m in markers if m in found]


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

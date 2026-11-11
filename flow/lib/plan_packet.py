#!/usr/bin/env python3
"""Write the seams packet: every name one story of a plan defines and another's code uses.

A plan of many stories is too long for one reader to cross-check whole. The
seams packet lists each name a story's added code defines that another story's
code uses, cited to the draft lines that define and use it, then every story's
Files line, for the one `flow:recon` that checks the contracts between the stories
and the files outside the plan that list or count what it adds (part 2 spec
§4.3, step 4).

Usage: plan_packet.py PLAN --seams [--rev REV] [--drafts DIR]

REV defaults to HEAD, DIR to the plan's drafts directory. The packet is written
to DIR/packets/seams.md. When DIR holds a join map, the plan must be the join of
its drafts. Exit 0 when the packet is written, 1 when the plan is refused or the
packet can't be written, 2 on a usage error.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from check_plan import FENCE_RE, declared_pairs, repo_root  # noqa: E402
from flowconf import FlowError  # noqa: E402
from langs import DEF_RES  # noqa: E402
from plan_join import JoinError, describe, drafts_dir, join, load_map  # noqa: E402
from plan_premises import Plan, Tree, Walker, defined_names, find_block, gather, intro_path  # noqa: E402

SEAMS_READING_RULES = (
    "Reading rules: read this packet whole first, then the plan's names contract, in reads of up to 2,000 lines. "
    "Search outside them only for the draft lines a row cites, several searches to one Bash call. "
    "Never re-read what you have read."
)
PAIRS_RULE = (
    "Each pair's second story runs its developer in a lane at the first story's base, without the first's code: "
    "a name it uses from the first breaks it, and so does a file, flag or behaviour it needs that no name shows."
)

DOC_LINES_MAX = 10
DOC_PREFIXES = ("///", "//!", "#", "*", '"""')
# A definition's own lines (a wrapped signature, a struct's fields) stop here.
SIGNATURE_LINES_MAX = 15
# Fenced blocks in these languages are commands and prose: their words are not uses of a name.
NOT_CODE_LANGS = frozenset("bash sh shell zsh console text txt plain markdown md".split())
FIELD_LINE_RE = re.compile(r"^\s*[-*]\s+\*\*(\w+):\*\*\s*(.*)$")
# A word not preceded by `.`: `"".join` and `self.cite` are not uses of a bare `join` or `cite`.
TOKEN_RE = re.compile(r"(?<![\w$.])[\w$]+")
TEST_FILE_RE = re.compile(r"(?:^|/)(?:tests/|test_[^/]*\.py$|[^/]*_test\.py$|[^/]*\.(?:test|spec)\.ts$)")
# "replace ... currently:" may introduce the text a step replaces, not text it adds, unless "with" follows;
# it is quoted text only when the tree or an earlier block holds it (see `Regions.on_block`).
ANCHOR_RE = re.compile(r"\b(?:replace[sd]?|currently)\b", re.IGNORECASE)
WITH_RE = re.compile(r"\bwith\b", re.IGNORECASE)
WITH_START_RE = re.compile(r"^(?:\d+\.\s*)?(?:then\s+)?with\b", re.IGNORECASE)
STRING_RE = re.compile(r'"(?:\\.|[^"\\])*"')
BODY_ITEM_RE = re.compile(r"^(?:pub(?:\([^)]*\))?\s+|export\s+(?:default\s+)?)?(?:struct|enum|union|trait|interface)\b")
PY_DEF_RE = re.compile(r"^(?:async\s+)?(?:def|class)\s")
DOCSTRING_RE = re.compile(r'^[rRuUbB]?("""|\'\'\')')
PLACEHOLDER = "@@LENGTH@@"


def cite(plan_line: int, story_map: dict[str, tuple[int, int]]) -> str:
    """`S<id>.md:<k>` for the draft holding a plan line, else `plan:<n>`."""
    for sid, (start, lines) in story_map.items():
        if start <= plan_line < start + lines:
            return f"S{sid}.md:{plan_line - start + 1}"
    return f"plan:{plan_line}"


def lang_of(info: str) -> str:
    """The language word of a fence's info string, lower-cased; empty when it has none."""
    words = info.split()
    return words[0].lower() if words else ""


def is_test_path(path: str) -> bool:
    """A path the plan's code is tested in, not shipped from."""
    return bool(TEST_FILE_RE.search(path))


def is_anchor(intro: str) -> bool:
    """An intro that may name text to replace ("Replace", "..., currently") rather than text to add.

    It also introduces the replacement code itself, so a block is quoted text only once it is found in the tree
    or in an earlier block.
    """
    found = ANCHOR_RE.search(intro)
    return bool(found) and not WITH_RE.search(intro, found.end())


@dataclass
class BlockInfo:
    """What the walk made of one fenced block."""

    role: str  # "current" (quoted from the file), "anchor" (text a step replaces) or "new" (code a step adds)
    lang: str
    target: str | None  # the file the plan puts the block in, as the plan names it


class Regions(Walker):
    """Records what each fenced block of the plan is: text quoted from a file, text a step replaces, or new code."""

    def __init__(self, plan: Plan, tree: Tree, wanted: set[str] | None = None):
        super().__init__(plan, tree, wanted)
        # plan line of a block's opening fence -> its role, language and target file
        self.blocks: dict[int, BlockInfo] = {}
        # The file the last quoted or replaced block is in, for the "with" block that follows it.
        self.anchor_path: str | None = None

    def on_block(self, start: int, intro: str, block: list[str], kind: str, lang: str):
        named = intro_path(intro, self.tree.ignored_root)
        candidates = [named] if named else [p for p in dict.fromkeys([self.prose_path, *self.task_paths]) if p]
        quoted = kind == "current" or (kind != "new" and is_anchor(intro))
        found: str | None = None
        if quoted:
            # Text the plan quotes from a file: the first candidate file that holds it.
            found = next(
                (
                    path
                    for candidate in candidates
                    for path in self.tree.resolve(candidate)[:1]
                    for lines in [self.tree.lines(path)]
                    if lines is not None and find_block(lines, block) is not None
                ),
                None,
            )
        # A "current" block is quoted text wherever it is; an anchor-like intro earns the role only by evidence:
        # the tree holds the block, or an earlier block of the plan does (as `Checker.check_current` reads it).
        if quoted and (kind == "current" or found or any(find_block(earlier, block) for earlier in self.added)):
            role = "current" if kind == "current" else "anchor"
            target = found if found else (candidates[0] if candidates else None)
        else:
            role = "new"
            if kind == "new":
                target = named
            elif not named and self.anchor_path and (kind == "replace" or WITH_START_RE.match(intro)):
                target = self.anchor_path
            else:
                target = self.target(intro, kind)
        self.blocks[start] = BlockInfo(role, lang_of(lang), target)
        self.anchor_path = target if role != "new" else None
        return super().on_block(start, intro, block, kind, lang)


def story_lines(plan: Plan, sid: str) -> list[tuple[int, str]]:
    """Every line of the story's section, with its plan line number."""
    return [(n, line) for n, line in enumerate(plan.lines, 1) if plan.story_at[n - 1] == sid]


@dataclass
class Fenced:
    """One fenced block of a story: the plan line of its opening fence, its language, its lines."""

    start: int
    lang: str
    lines: list[tuple[int, str]]


def code_blocks(plan: Plan, sid: str) -> list[Fenced]:
    """The story's fenced blocks, each line with its plan line number."""
    blocks: list[Fenced] = []
    current: Fenced | None = None
    for n, line in story_lines(plan, sid):
        if FENCE_RE.match(line):
            if current is None:
                current = Fenced(n, lang_of(line.strip()[3:]), [])
            else:
                blocks.append(current)
                current = None
        elif current is not None:
            current.lines.append((n, line))
    if current and current.lines:
        blocks.append(current)
    return blocks


def field_text(plan: Plan, sid: str, name: str) -> str:
    """A header field of the story (`- **Files:** ...`), its indented continuation lines joined."""
    header: list[str] = []
    for _, line in story_lines(plan, sid):
        if line.startswith("###"):
            break
        header.append(line)
    for i, line in enumerate(header):
        match = FIELD_LINE_RE.match(line)
        if match and match.group(1) == name:
            parts = [match.group(2).strip()]
            for follow in header[i + 1 :]:
                if not follow.strip() or not follow[0].isspace() or FIELD_LINE_RE.match(follow):
                    break
                parts.append(follow.strip())
            return " ".join(parts)
    return ""


def depth(line: str, opens: str, closes: str) -> int:
    """How far a line opens more brackets of one kind than it closes, ignoring double-quoted strings."""
    bare = STRING_RE.sub("", line)
    return sum(bare.count(c) for c in opens) - sum(bare.count(c) for c in closes)


def definition_lines(texts: list[str], at: int) -> tuple[list[str], list[str]]:
    """The doc comments above the definition at `texts[at]`, and the definition's own lines.

    The definition runs from its line until the brackets balance and a line ends in `{`, `;` or `:` (a wrapped
    signature), and a struct-like item on to its closing brace. A Python definition also takes the docstring
    that follows it. A run longer than SIGNATURE_LINES_MAX lines is cut and ends in `...`.
    """
    docs: list[str] = []
    back = at - 1
    while back >= 0 and len(docs) < DOC_LINES_MAX and texts[back].strip().startswith(DOC_PREFIXES):
        docs.insert(0, texts[back].rstrip())
        back -= 1
    first = texts[at].strip()
    shown = [texts[at].rstrip()]
    last, parens, cut = at, depth(first, "([", ")]"), False

    def take(i: int) -> bool:
        nonlocal last, parens, cut
        if len(shown) >= SIGNATURE_LINES_MAX:
            cut = True
            return False
        last = i
        shown.append(texts[i].rstrip())
        parens += depth(texts[i], "([", ")]")
        return True

    while not (parens <= 0 and shown[-1].endswith(("{", ";", ":", "}"))) and last + 1 < len(texts):
        nxt = texts[last + 1].strip()
        if not nxt or any(pattern.match(nxt) for pattern in DEF_RES) or not take(last + 1):
            break
    if BODY_ITEM_RE.match(first):
        braces = sum(depth(line, "{", "}") for line in shown)
        while braces > 0 and last + 1 < len(texts) and take(last + 1):
            braces += depth(texts[last], "{", "}")
        cut = cut or braces > 0
    elif PY_DEF_RE.match(first) and last + 1 < len(texts):
        opening = DOCSTRING_RE.match(texts[last + 1].strip())
        if opening:
            quote, doc = opening.group(1), []
            for i in range(last + 1, len(texts)):
                doc.append(texts[i].rstrip())
                closed = texts[i].count(quote) >= (2 if i == last + 1 else 1)
                if closed or len(doc) >= DOC_LINES_MAX:
                    break
            shown += doc
    if cut:
        shown.append("...")
    return docs, shown


def definitions(
    plan: Plan, sid: str, blocks: dict[int, BlockInfo], tests: bool = False
) -> dict[str, tuple[int, list[str], list[str]]]:
    """The names the story's added code defines: name -> (plan line, doc lines above it, the definition's lines).

    Quoted ("current") text and the text a step replaces define nothing, nor does a command or prose block.
    Code going into a test file is skipped unless `tests` is set.
    """
    found: dict[str, tuple[int, list[str], list[str]]] = {}
    for block in code_blocks(plan, sid):
        info = blocks.get(block.start)
        if info is None or info.role != "new" or block.lang in NOT_CODE_LANGS:
            continue
        if not tests and info.target and is_test_path(info.target):
            continue
        texts = [text for _, text in block.lines]
        for name, shown in defined_names(texts).items():
            at = next(i for i, text in enumerate(texts) if text.strip() == shown)
            docs, lines = definition_lines(texts, at)
            found.setdefault(name, (block.lines[at][0], docs, lines))
    return found


def identifiers(plan: Plan, sid: str) -> dict[str, int]:
    """Every whole word in the story's code, with the first plan line holding it.

    Commands and prose (bash, markdown, text blocks) are not code, and a word after a `.` is a member of
    something, not a bare name.
    """
    seen: dict[str, int] = {}
    for block in code_blocks(plan, sid):
        if block.lang in NOT_CODE_LANGS:
            continue
        for n, line in block.lines:
            for token in TOKEN_RE.findall(line):
                seen.setdefault(token, n)
    return seen


def fenced(lines: list[str], info: str = "") -> list[str]:
    """Lines inside a code fence longer than any run of backticks in them."""
    longest = max((len(run) for line in lines for run in re.findall(r"`+", line)), default=0)
    fence = "`" * max(3, longest + 1)
    return [fence + info, *lines, fence]


def finish(out: list[str]) -> str:
    """Join a packet's lines and write its own line count on the second one."""
    text = "\n".join(out).rstrip("\n") + "\n"
    return text.replace(PLACEHOLDER, f"Packet length: {len(text.splitlines())} lines", 1)


def checked_map(text: str, drafts: Path) -> dict[str, tuple[int, int]]:
    """The join map of the drafts, once the plan is shown to be their join; `{}` when there is none.

    A plan edited, or drafts changed, since the join leaves the map pointing at the wrong lines, and every
    citation with it: the joined text and the recomputed map must both match what is on disk.
    """
    story_map = load_map(drafts)
    if story_map:
        joined, fresh = join(text, drafts)
        # An edit above the marker leaves the joined text as it was and moves every start line.
        if joined != text or {sid: tuple(span) for sid, span in fresh.items()} != story_map:
            raise JoinError("the plan is out of date with its drafts; re-run loom flow join")
    return story_map


def seams_packet(plan: Path, rev: str, drafts: Path) -> str:
    """Every name one story defines and another's code uses, then every story's Files line."""
    text = plan.read_text(encoding="utf-8")
    tree = Tree(repo_root(plan), rev)
    parsed = gather(text, tree.ignored_root)
    order = [story for story, _ in parsed.stories]
    story_map = checked_map(text, drafts)
    regions = Regions(parsed, tree)
    regions.walk()
    defs = {story: definitions(parsed, story, regions.blocks) for story in order}
    own = {story: definitions(parsed, story, regions.blocks, tests=True) for story in order}
    used = {story: identifiers(parsed, story) for story in order}
    rows = []
    for name in sorted({name for found in defs.values() for name in found}):
        definers = [story for story in order if name in defs[story]]
        users = [story for story in order if name in used[story] and name not in own[story]]
        if users:
            defined = ", ".join(cite(defs[story][name][0], story_map) for story in definers)
            uses = ", ".join(cite(used[story][name], story_map) for story in users)
            rows.append(f"{name} | defined {defined} | used {uses}")
    out = [f"# Seams packet for {plan.name}", PLACEHOLDER, "", f"Written against the tree at {rev}.", "", SEAMS_READING_RULES, ""]
    out += ["## Names one story defines and another uses", ""]
    out += fenced(rows, "text") if rows else ["(none)"]
    pairs, _ = declared_pairs(text)
    if pairs:
        out += ["", "## Declared pairs", "", PAIRS_RULE, ""]
        for a, b in pairs:
            names = sorted(name for name in defs.get(a, {}) if name in used.get(b, {}) and name not in own.get(b, {}))
            out.append(f"- {a}+{b}: names Story {a} defines that Story {b} uses: {', '.join(names) if names else '(none found)'}")
    out += ["", "## Files line of every story", ""]
    for story in order:
        out.append(f"- Story {story}: {field_text(parsed, story, 'Files') or '(none stated)'}")
    return finish(out)


def resolve_rev(root: Path, rev: str) -> str | None:
    """The commit sha a revision names in the repo at `root`, or None when it names none."""
    try:
        done = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "--verify", "--quiet", f"{rev}^{{commit}}"],
            capture_output=True,
            text=True,
            check=True,
        )
    except (subprocess.CalledProcessError, OSError):
        return None
    return done.stdout.strip() or None


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="plan_packet.py", description=(__doc__ or "").split("\n\n")[0])
    parser.add_argument("plan", type=Path)
    parser.add_argument("--seams", action="store_true", required=True, help="write the packet of names shared between stories")
    parser.add_argument("--rev", default="HEAD", help="read the tree at this commit (default HEAD)")
    parser.add_argument("--drafts", type=Path, help="the drafts directory (default: the plan's)")
    try:
        args = parser.parse_args(argv)
    except SystemExit as err:
        return 2 if err.code else 0
    try:
        # probe: a missing plan is a usage error, an unreadable one a FAIL
        args.plan.read_text(encoding="utf-8")
    except FileNotFoundError as err:
        print(f"plan_packet: {args.plan}: {err.strerror}", file=sys.stderr)
        return 2
    except (OSError, UnicodeDecodeError) as err:
        print(f"FAIL [packet] {args.plan}: {describe(err)}")
        return 1
    try:
        root = repo_root(args.plan)
        drafts = args.drafts or drafts_dir(args.plan)
    except FlowError as err:
        print(f"FAIL [packet] {args.plan}: {err}")
        return 1
    sha = resolve_rev(root, args.rev)
    if sha is None:
        print(f"plan_packet: --rev {args.rev} is not a commit in {root}", file=sys.stderr)
        return 2
    try:
        body = seams_packet(args.plan, sha, drafts)
    except subprocess.CalledProcessError as err:
        print(f"plan_packet: git failed in {root}: {err.stderr.strip()}", file=sys.stderr)
        return 2
    except JoinError as err:
        print(f"FAIL [packet] {args.plan}: {err}")
        return 1
    except (OSError, UnicodeDecodeError) as err:
        print(f"FAIL [packet] {args.plan}: {describe(err)}")
        return 1
    target = drafts / "packets" / "seams.md"
    try:
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(body, encoding="utf-8")
    except OSError as err:
        print(f"FAIL [packet] {args.plan}: {target}: {describe(err)}")
        return 1
    print(f"wrote {target}: {len(body.splitlines())} lines")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

#!/usr/bin/env python3
"""List the premises a plan makes about the tree that the tree doesn't bear out.

A plan quotes the tree it was written against: the files each task modifies
and the lines it names, the "current" text a step replaces, the items a step
adds beside existing ones. By the time it runs, the tree may have moved. This
script checks those quotes mechanically and prints only the ones that fail,
so the `flow:premise-check` agent settles a short list instead of reading the plan.

Flags, per story:

  missing  a file a task modifies, tests or deletes does not exist, or a path
           the prose names under one of the repo's top-level directories
           doesn't; no task creates it either. A path that is the unique
           suffix of an existing one (`e2e/fixtures/x.ts`) exists.
  lines    a `path:L-M` reference runs past the end of its file
  exists   a file a task creates already exists
  current  a block introduced as "current" (`current:`, `Current (lines 3-4):`)
           is neither in its file nor in a block an earlier step adds. A `...`
           line inside it stands for elided text.
  defined  an item a step adds (`fn`, `struct`, `const`, a TS `class`, ...)
           is already defined in the file it is added to. The block may only
           quote it as context: premise-check decides.
  file?    a "current" block whose file the plan doesn't name

Not flagged, only counted: "current" blocks found away from the lines the plan
names (developers find a block by its text), and prose paths outside the
repo's top-level directories that resolve to nothing (a crate's registry
source, a file the app writes at run time).

Usage: plan_premises.py PLAN.md [--stories 1-4,7] [--rev REV] [--verbose]

--rev reads the tree at a commit (the supervisor passes its base commit)
instead of the working tree. Exit 0 when nothing is flagged, 1 when something
is, 2 on a usage error.
"""

from __future__ import annotations

import argparse
import posixpath
import re
import subprocess
import sys
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from check_plan import CODE_RE, FENCE_RE, H2_RE, STORY_RE, TASK_RE, repo_root  # noqa: E402
from flowconf import FlowError  # noqa: E402
from langs import COMMON_NAMES, DEF_RES, EXTENSIONS  # noqa: E402

FILES_ITEM_RE = re.compile(r"^\s*[-*]\s+(Create|Modify|Test|Delete)\b[^:`]*:\s*(.*)$")
STORY_FILES_RE = re.compile(r"^\s*[-*]\s+\*\*Files:\*\*\s*(.*)$")
PATH_TOKEN_RE = re.compile(
    r"^(?:\./)?(?P<path>[\w@.+-]+(?:/[\w@.+-]+)*/?)(?::(?P<start>\d+)(?:-(?P<end>\d+))?)?$"
)
LINE_ONLY_RE = re.compile(r"^:(?P<start>\d+)(?:-(?P<end>\d+))?$")
HINT_RE = re.compile(r"(?:\(`?:|\(lines?\s+|ends\s+`?:)(\d+)")
CURRENT_RE = re.compile(r"\bcurrent\b[^`]*:\s*$|\bcurrent\b.*\):\s*$", re.IGNORECASE)
REPLACE_RE = re.compile(r"\breplace with\b", re.IGNORECASE)
ADD_RE = re.compile(r"\b(add|append|insert)\b.*:\s*$", re.IGNORECASE)
ELIDED = {"...", "…", "// ...", "// …", "# ...", "/* ... */", "<!-- ... -->"}
# A rewrapped block must squeeze to at least this many characters to match: `}` is everywhere.
MIN_SQUEEZED = 40


@dataclass
class Flag:
    line: int
    kind: str
    message: str


@dataclass
class StoryReport:
    sid: str
    line: int
    premises: int = 0
    drifted: int = 0
    from_earlier: int = 0
    unanchored: int = 0
    flags: list[Flag] = field(default_factory=list)
    held: list[str] = field(default_factory=list)


class Tree:
    """The repo's files, in the working tree or at a commit."""

    def __init__(self, root: Path, rev: str | None):
        self.root, self.rev = root, rev
        listing = (
            self._git("ls-tree", "-r", "--name-only", rev)
            if rev
            else self._git("ls-files", "--cached", "--others", "--exclude-standard")
        )
        self.paths = set(listing.splitlines())
        self.dirs = {
            "/".join(parts[:i]) for parts in (p.split("/") for p in self.paths) for i in range(1, len(parts))
        }
        self.top = {p.split("/", 1)[0] for p in self.paths}
        self._everything = sorted(self.paths | self.dirs)
        self._cache: dict[str, list[str] | None] = {}
        self._ignored: dict[str, bool] = {}

    def ignored_root(self, name: str) -> bool:
        """Whether the repo's ignore rules cover the top-level entry `name`, as a file or a directory:
        build output a plan may name but the tree never holds. Each repo's .gitignore decides."""
        if name in self.top:
            # A directory holding tracked files is not build output, whatever its rules say of the rest.
            return False
        if name not in self._ignored:
            # Exit 0 when either path is ignored; `name/x` catches a directory-only rule such as `build/`.
            # (`--quiet` takes one path only.)
            probe = subprocess.run(
                ["git", "-C", str(self.root), "check-ignore", "--", name, f"{name}/x"],
                capture_output=True,
            )
            self._ignored[name] = probe.returncode == 0
        return self._ignored[name]

    def _git(self, *args: str) -> str:
        return subprocess.run(
            ["git", "-C", str(self.root), *args], capture_output=True, text=True, check=True
        ).stdout

    def resolve(self, path: str) -> list[str]:
        """The repo paths a plan's path names: itself, or every path it is a suffix of."""
        path = posixpath.normpath(path.rstrip("/"))
        if path in self.paths or path in self.dirs:
            return [path]
        return [p for p in self._everything if p.endswith("/" + path)]

    def lines(self, path: str) -> list[str] | None:
        if path not in self._cache:
            try:
                if self.rev:
                    text = self._git("show", f"{self.rev}:{path}")
                else:
                    text = (self.root / path).read_text(errors="replace")
                self._cache[path] = text.splitlines()
            except (OSError, subprocess.CalledProcessError):
                self._cache[path] = None
        return self._cache[path]


def expand_braces(token: str) -> list[str]:
    match = re.search(r"\{([^{}]*)\}", token)
    if not match:
        return [token]
    head, tail = token[: match.start()], token[match.end() :]
    return [out for part in match.group(1).split(",") for out in expand_braces(head + part + tail)]


def path_token(token: str, ignored: Callable[[str], bool]) -> tuple[str, int | None, int | None] | None:
    """A backticked token read as `path[:start[-end]]`, or None when it isn't a path. A path whose
    top-level entry git ignores (`ignored`; Tree.ignored_root provides it) is build output, not a premise."""
    match = PATH_TOKEN_RE.match(token)
    if not match:
        return None
    path = match.group("path")
    segments = path.rstrip("/").split("/")
    if any(set(s) == {"."} and len(s) > 1 for s in segments):
        return None
    last = segments[-1]
    has_ext = "." in last and last.rsplit(".", 1)[1] in EXTENSIONS
    if not (has_ext or (path.endswith("/") and len(segments) > 1)):
        return None
    if ignored(segments[0]):
        return None
    start = int(match.group("start")) if match.group("start") else None
    end = int(match.group("end")) if match.group("end") else start
    return path, start, end


def defined_names(block: list[str]) -> dict[str, str]:
    """The items a block defines at its own top level, with the line defining each."""
    indents = [len(line) - len(line.lstrip()) for line in block if line.strip()]
    top = min(indents) if indents else 0
    names: dict[str, str] = {}
    for line in block:
        if not line.strip() or len(line) - len(line.lstrip()) != top:
            continue
        for pattern in DEF_RES:
            match = pattern.match(line.strip())
            if match and match.group(1) not in COMMON_NAMES:
                names.setdefault(match.group(1), line.strip())
                break
    return names


def definition_line(lines: list[str], name: str) -> int | None:
    for n, line in enumerate(lines, 1):
        for pattern in DEF_RES:
            match = pattern.match(line.strip())
            if match and match.group(1) == name:
                return n
    return None


def find_block(lines: list[str], block: list[str]) -> int | None:
    """Where a quoted block starts in a file, letting `...` lines stand for elided text."""
    span = find_span(lines, block)
    return span[0] if span else None


def find_span(lines: list[str], block: list[str]) -> tuple[int, int] | None:
    """The first and last file lines (1-based) a quoted block covers, or None.

    Lines match exactly, then ignoring indentation. A block that is one piece
    may also be a fragment of wrapped prose, or code rustfmt has rewrapped: it
    matches with all whitespace removed when it is long enough to be specific.
    """
    body = [line.rstrip() for line in block]
    while body and not body[0].strip():
        body.pop(0)
    while body and not body[-1].strip():
        body.pop()
    chunks: list[list[str]] = [[]]
    for line in body:
        if line.strip() in ELIDED:
            chunks.append([])
        else:
            chunks[-1].append(line)
    chunks = [chunk for chunk in chunks if chunk]
    if not chunks:
        return None
    for norm in (str.rstrip, str.strip):
        file_lines = [norm(line) for line in lines]
        wanted = [[norm(line) for line in chunk] for chunk in chunks]
        head = wanted[0]
        for start in (i for i, line in enumerate(file_lines) if line == head[0]):
            if file_lines[start : start + len(head)] != head:
                continue
            at, ok = start + len(head), True
            for chunk in wanted[1:]:
                found = next(
                    (i for i in range(at, len(file_lines) - len(chunk) + 1) if file_lines[i : i + len(chunk)] == chunk),
                    None,
                )
                if found is None:
                    ok = False
                    break
                at = found + len(chunk)
            if ok:
                return start + 1, at
    if len(chunks) == 1:
        # A fragment of wrapped prose: "Current sentence:" quotes part of a line.
        fragment = "\n".join(line.strip() for line in chunks[0])
        text = "\n".join(line.strip() for line in lines)
        at = text.find(fragment)
        if at >= 0:
            return text.count("\n", 0, at) + 1, text.count("\n", 0, at + len(fragment)) + 1
        # Code rustfmt rewrapped: compare with every whitespace character removed.
        squeezed = "".join("".join(line.split()) for line in chunks[0])
        if len(squeezed) >= MIN_SQUEEZED:
            owner = [n for n, line in enumerate(lines, 1) for _ in "".join(line.split())]
            at = "".join("".join(line.split()) for line in lines).find(squeezed)
            if at >= 0:
                return owner[at], owner[at + len(squeezed) - 1]
    return None


@dataclass
class Plan:
    """The plan's lines, its stories, and the files its tasks create."""

    lines: list[str]
    story_at: list[str | None] = field(default_factory=list)
    stories: list[tuple[str, int]] = field(default_factory=list)
    created: set[str] = field(default_factory=set)
    story_files: set[str] = field(default_factory=set)


def gather(text: str, ignored: Callable[[str], bool]) -> Plan:
    plan = Plan(text.splitlines())
    sid: str | None = None
    fenced = False
    for n, line in enumerate(plan.lines, 1):
        if FENCE_RE.match(line):
            fenced = not fenced
        elif not fenced:
            story = STORY_RE.match(line)
            if story:
                sid = story.group(1)
                plan.stories.append((sid, n))
            elif H2_RE.match(line):
                sid = None
            item = FILES_ITEM_RE.match(line)
            if item and item.group(1) == "Create":
                for token in CODE_RE.findall(item.group(2)):
                    for expanded in expand_braces(token):
                        parsed = path_token(expanded, ignored)
                        if parsed:
                            plan.created.add(posixpath.normpath(parsed[0].rstrip("/")))
            files = STORY_FILES_RE.match(line)
            if files:
                plan.story_files.update(posixpath.normpath(t) for t in CODE_RE.findall(files.group(1)))
        plan.story_at.append(sid)
    return plan


def intro_path(intro: str, ignored: Callable[[str], bool]) -> str | None:
    """The last repo path an intro line names, if any."""
    paths = [parsed[0] for token in CODE_RE.findall(intro) for parsed in [path_token(token, ignored)] if parsed]
    return paths[-1] if paths else None


class Walker:
    """One pass over a plan, tracking what each fenced block refers to.

    It knows the story each line is in, the files the current task modifies,
    the file the prose last named, and the "current" block a replacement
    follows. Subclasses check the paths and blocks it hands them.
    """

    def __init__(self, plan: Plan, tree: Tree, wanted: set[str] | None):
        self.plan, self.tree, self.wanted = plan, tree, wanted
        self.planned = plan.created | {p for p in plan.story_files if not tree.resolve(p)}
        self.task_paths: list[str] = []
        self.prose_path: str | None = None
        # The names a "current" block defines and its file, for the replacement that follows it.
        self.pending: tuple[set[str], str | None] | None = None
        # Every block the plan adds before the line being read: later "current" blocks may quote them.
        self.added: list[list[str]] = []

    def in_scope(self, n: int) -> bool:
        return self.wanted is None or (self.plan.story_at[n - 1] or "preamble") in self.wanted

    def is_planned(self, path: str) -> bool:
        path = posixpath.normpath(path.rstrip("/"))
        return any(p == path or p.endswith("/" + path) or f"/{path}/" in f"/{p}" for p in self.planned)

    def kind_of(self, intro: str) -> str:
        """How a step uses its block: "current" text, a "replace"ment, an "add"ition, a "new" file, or "other"."""
        if CURRENT_RE.search(intro) and not REPLACE_RE.search(intro):
            return "current"
        if REPLACE_RE.search(intro):
            return "replace"
        named = intro_path(intro, self.tree.ignored_root)
        if named and self.is_planned(named):
            return "new"
        return "add" if ADD_RE.search(intro) else "other"

    def target(self, intro: str, kind: str) -> str | None:
        """The file a step's block goes into, as the plan names it."""
        named = intro_path(intro, self.tree.ignored_root)
        if named:
            return named
        if kind == "replace" and self.pending and self.pending[1]:
            return self.pending[1]
        if self.prose_path:
            return self.prose_path
        return self.task_paths[0] if len(self.task_paths) == 1 else None

    def walk(self) -> None:
        fenced, block, start, intro, lang = False, [], 0, "", ""
        for n, line in enumerate(self.plan.lines, 1):
            scoped = self.in_scope(n)
            if FENCE_RE.match(line):
                if not fenced:
                    fenced, block, start, lang = True, [], n, line.strip()[3:].strip()
                    back = n - 2
                    while back >= 0 and not self.plan.lines[back].strip():
                        back -= 1
                    intro = self.plan.lines[back].strip() if back >= 0 else ""
                    continue
                fenced = False
                kind = self.kind_of(intro)
                self.pending = self.on_block(start, intro, block, kind, lang) if scoped else None
                if kind != "current":
                    self.added.append(block)
                continue
            if fenced:
                block.append(line)
                continue
            if TASK_RE.match(line) or H2_RE.match(line):
                self.task_paths, self.prose_path, self.pending = [], None, None
            if not scoped or STORY_FILES_RE.match(line):
                continue
            item = FILES_ITEM_RE.match(line)
            if item:
                self.read_files_item(n, item.group(1), item.group(2))
            else:
                for token in CODE_RE.findall(line):
                    for expanded in expand_braces(token):
                        parsed = path_token(expanded, self.tree.ignored_root)
                        if parsed:
                            self.prose_path = self.on_path(n, *parsed, "Path") or self.prose_path

    def read_files_item(self, n: int, kind: str, rest: str) -> None:
        current: str | None = None
        for token in CODE_RE.findall(rest):
            only = LINE_ONLY_RE.match(token)
            if only and current:
                start = int(only.group("start"))
                self.on_path(n, current, start, int(only.group("end") or start), kind)
                continue
            for expanded in expand_braces(token):
                parsed = path_token(expanded, self.tree.ignored_root)
                if parsed:
                    current = parsed[0]
                    resolved = self.on_path(n, *parsed, kind)
                    if kind in ("Modify", "Test") and resolved:
                        self.task_paths.append(resolved)

    def on_path(self, n: int, path: str, start: int | None, end: int | None, kind: str) -> str | None:
        """A path the plan names; returns the repo path it resolves to, if exactly one."""
        found = self.tree.resolve(path)
        return found[0] if kind != "Create" and len(found) == 1 else None

    def on_block(self, start: int, intro: str, block: list[str], kind: str, lang: str):
        """A fenced block; returns what a following replacement needs (see `pending`)."""
        return None


class Checker(Walker):
    """Checks each premise a plan makes about the tree."""

    def __init__(self, plan: Plan, tree: Tree, wanted: set[str] | None, verbose: bool):
        super().__init__(plan, tree, wanted)
        self.verbose = verbose
        self.planned_top = {p.split("/", 1)[0] for p in self.planned}
        self.reports = {sid: StoryReport(sid, line) for sid, line in plan.stories}
        self.preamble = StoryReport("preamble", 1)

    def report_for(self, n: int) -> StoryReport:
        sid = self.plan.story_at[n - 1]
        return self.reports[sid] if sid else self.preamble

    def run(self) -> list[StoryReport]:
        self.walk()
        reports = ([self.preamble] if self.wanted is None else []) + list(self.reports.values())
        return [r for r in reports if self.wanted is None or r.sid in self.wanted]

    def on_path(self, n: int, path: str, start: int | None, end: int | None, kind: str) -> str | None:
        """Check one path premise; return the repo path it resolves to, if exactly one."""
        report = self.report_for(n)
        found = self.tree.resolve(path)
        if kind == "Create":
            report.premises += 1
            if posixpath.normpath(path) in self.tree.paths:
                report.flags.append(Flag(n, "exists", f"`{path}` is to be created but already exists"))
            return None
        if not found:
            if self.is_planned(path):
                report.premises += 1
            elif kind == "Path" and path.split("/", 1)[0] not in self.tree.top | self.planned_top:
                report.unanchored += 1
            else:
                report.premises += 1
                report.flags.append(Flag(n, "missing", f"`{path}` does not exist and no task creates it"))
            return None
        report.premises += 1
        if len(found) > 1:
            return None
        if start is not None and found[0] in self.tree.paths:
            length = len(self.tree.lines(found[0]) or [])
            if max(start, end or start) > length:
                span = f"{start}-{end}" if end and end != start else f"{start}"
                report.flags.append(Flag(n, "lines", f"`{path}:{span}` but {found[0]} has {length} lines"))
        if self.verbose:
            report.held.append(f"plan:{n} {kind.lower()} {path}")
        return found[0]

    def on_block(self, start: int, intro: str, block: list[str], kind: str, lang: str):
        report = self.report_for(start)
        if kind == "current":
            return self.check_current(report, start, intro, block)
        if kind in ("replace", "add"):
            self.check_addition(report, start, intro, block, kind)
        return None

    def check_current(self, report: StoryReport, start: int, intro: str, block: list[str]):
        """A block quoted as the file's current text: is it there?"""
        report.premises += 1
        named = intro_path(intro, self.tree.ignored_root)
        candidates = [named] if named else [p for p in dict.fromkeys([self.prose_path, *self.task_paths]) if p]
        names = set(defined_names(block))
        if not candidates:
            report.flags.append(Flag(start, "file?", "a \"current\" block whose file the plan doesn't name"))
            return names, None
        for candidate in candidates:
            for path in self.tree.resolve(candidate)[:1]:
                lines = self.tree.lines(path)
                at = find_block(lines, block) if lines is not None else None
                if at is not None:
                    hint = HINT_RE.search(intro)
                    if hint and int(hint.group(1)) != at:
                        report.drifted += 1
                    return names, path
        if any(find_block(earlier, block) for earlier in self.added):
            report.from_earlier += 1
        else:
            where = candidates[0] if named or len(candidates) == 1 else " or ".join(candidates)
            first = next((line.strip() for line in block if line.strip()), "")
            report.flags.append(Flag(start, "current", f"block starting `{first[:60]}` is not in {where}"))
        resolved = self.tree.resolve(candidates[0])
        return names, resolved[0] if len(resolved) == 1 else None

    def check_addition(self, report: StoryReport, start: int, intro: str, block: list[str], kind: str) -> None:
        """A block a step adds or replaces with: does it define something its file already has?"""
        target = self.target(intro, kind)
        if not target or self.is_planned(target):
            return
        resolved = self.tree.resolve(target)
        lines = self.tree.lines(resolved[0]) if len(resolved) == 1 else None
        if lines is None:
            return
        replaced = self.pending[0] if kind == "replace" and self.pending else set()
        clashes = []
        for name in defined_names(block):
            if name in replaced:
                continue
            report.premises += 1
            at = definition_line(lines, name)
            if at is not None:
                clashes.append(f"`{name}` (:{at})")
        if clashes:
            report.flags.append(Flag(start, "defined", f"adds {', '.join(clashes)}, already defined in {resolved[0]}"))


def story_filter(spec: str | None) -> set[str] | None:
    if not spec:
        return None
    wanted: set[str] = set()
    for part in spec.split(","):
        part = part.strip()
        if re.fullmatch(r"\d+-\d+", part):
            low, high = map(int, part.split("-"))
            wanted.update(str(i) for i in range(low, high + 1))
        elif part:
            wanted.add(part)
    return wanted


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="plan_premises.py", description=__doc__.split("\n\n")[0])
    parser.add_argument("plan", type=Path)
    parser.add_argument("--stories", help="story ids to check, e.g. 1-4,7 (default: all, plus the preamble)")
    parser.add_argument("--rev", help="read the tree at this commit instead of the working tree")
    parser.add_argument("--verbose", action="store_true", help="also list the path premises that held")
    try:
        args = parser.parse_args(argv)
    except SystemExit as err:
        return 2 if err.code else 0
    try:
        text = args.plan.read_text()
    except OSError as err:
        print(f"plan_premises: {args.plan}: {err.strerror}", file=sys.stderr)
        return 2
    try:
        root = repo_root(args.plan)
    except FlowError as err:
        print(f"plan_premises: {err}", file=sys.stderr)
        return 2
    try:
        tree = Tree(root, args.rev)
    except subprocess.CalledProcessError as err:
        print(f"plan_premises: git failed in {root}: {err.stderr.strip()}", file=sys.stderr)
        return 2
    reports = Checker(gather(text, tree.ignored_root), tree, story_filter(args.stories), args.verbose).run()
    where = f"the tree at {args.rev}" if args.rev else "the working tree"
    print(f"plan_premises: {args.plan} against {where}")
    for report in reports:
        name = "Preamble" if report.sid == "preamble" else f"Story {report.sid}"
        print(f"{name} (plan:{report.line}): {report.premises} premises, {len(report.flags)} flagged")
        for flag in report.flags:
            print(f"  plan:{flag.line}  {flag.kind:<8} {flag.message}")
        for held in report.held:
            print(f"  ok {held}")
    flagged = sum(len(r.flags) for r in reports)
    print(
        f"{flagged} of {sum(r.premises for r in reports)} premises flagged. Not flagged: "
        f"{sum(r.drifted for r in reports)} current blocks away from the lines the plan names, "
        f"{sum(r.from_earlier for r in reports)} quoting text an earlier step adds, "
        f"{sum(r.unanchored for r in reports)} prose paths outside the repo's directories."
    )
    if flagged:
        print("Send the flagged lines to premise-check.")
    return 1 if flagged else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

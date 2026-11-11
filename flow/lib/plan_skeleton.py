#!/usr/bin/env python3
"""Check a plan skeleton before /flow:draft drafts it: its head, its names contract and its story list.

`check_plan.py --skeleton PLAN` runs `lint`, which reads the plan above its marker line
(plan_join.MARKER) and returns one problem per failure:

- the head: a `**Spec:**` line naming a file that exists, the exact heading `## Names contract` above
  `## Stories`, one marker line, and no `## Story` or `###` heading;
- rule 1, every shared name in full, over each backticked span of the contract: no `..`, `...` or `…`
  in a span with parentheses or braces (a range such as `0..5` or `..=` passes); no parameter of a
  `name(args)` span that is a lone identifier starting in lower case, unless it is a primitive type
  or a receiver; no `struct`, `class`, `interface` or `enum` declared without `{`, a tuple form,
  `(opaque)` on its line or a bullet nested under it. Attributes (`#[`, `@`), spans holding a
  `<placeholder>`, and calls (a literal argument, or `Some`, `Ok`, `Err`) are not signatures;
- rule 2, an owner: a contract line holding a signature or a declaration names a listed story as
  `Story <id>` on itself, on a bullet it is nested under, or on the unindented line that introduces
  its list; a blank line ends the walk up;
- rule 3: `(Story <id> proposes)` names a listed story, and waives rule 1 on its line;
- the entries: each gives its Developer (a developer the run knows), a Files line with a backticked
  path, Interfaces, a Verify line with a backticked command, a Review line (when present) of deep or
  light, and a Reading line, each once; and every bold Story bullet of the list is in the grammar the
  join reads (`- **Story <id>**` at column 0).

The rules are heuristics over backticked spans: a contract written as an expression rather than a
signature can draw a false flag, and the cure is to type it.
"""

from __future__ import annotations

import re
from pathlib import Path

from check_plan import CODE_RE, DEVELOPERS, FIELDS, H2_RE, REVIEW_VALUE_RE, SPEC_RE, STORY_RE, TASK_RE, repo_root
from plan_join import ENTRY_RE, MARKER, STORIES_HEADING, Entry, JoinError, entries, unfenced

CONTRACT_HEADING = "## Names contract"
PRIMITIVES = frozenset(
    "bool char str String u8 u16 u32 u64 u128 usize i8 i16 i32 i64 i128 isize f32 f64 "
    "int int8 int16 int32 int64 uint uint8 uint16 uint32 uint64 float float32 float64 byte rune "
    "string number boolean bigint any unknown void never null undefined object bytes error".split()
)
RECEIVER_RE = re.compile(r"^(?:&\s*(?:'\w+\s+)?)?self$|^this$")
SIGNATURE_RE = re.compile(r"([A-Za-z_][\w:.<>']*)\s*(<[^()]*>)?\(([^()]*(?:\([^()]*\)[^()]*)*)\)")
DECLARATION_RE = re.compile(r"\b(struct|class|interface|enum)\s+([A-Z]\w*)")
ELISION_RE = re.compile(r"\.\.\.?|…")
RANGE_RE = re.compile(r"\d\.\.\d|\.\.=")
PLACEHOLDER_RE = re.compile(r"<[a-z][a-z ]*>")
VISIBILITY_RE = re.compile(r"\bpub\((?:crate|super|self|in [^)]*)\)")
LITERAL_RE = re.compile(r"'[^']*'|\"[^\"]*\"|-?\d+(?:\.\d+)?")
BARE_RE = re.compile(r"[a-z_][A-Za-z0-9_]*")
CALLS = frozenset({"Some", "Ok", "Err"})
OWNER_RE = re.compile(r"\bStor(?:y|ies)\s+([0-9A-Za-z][\w-]*)")
PROPOSES_RE = re.compile(r"\(Story\s+([0-9A-Za-z][\w-]*)\s+proposes\)")
BULLET_RE = re.compile(r"^\s*[-*]\s")
LOOSE_ENTRY_RE = re.compile(r"^\s*(?:[-*]|\d+\.)\s+\*\*Story\b")
SHORT = 80


def indent(line: str) -> int:
    return len(line) - len(line.lstrip())


def short(span: str) -> str:
    """A span cut to SHORT characters for a problem line."""
    return span if len(span) <= SHORT else span[: SHORT - 1] + "…"


def split_args(text: str) -> list[str]:
    """A parameter list split at its top-level commas; the `>` of `->` and `=>` is no bracket."""
    parts: list[str] = []
    depth = 0
    current = ""
    for i, ch in enumerate(text):
        if ch in "<([{":
            depth += 1
        elif ch in ")]}" or (ch == ">" and text[i - 1 : i] not in ("-", "=")):
            depth -= 1
        if ch == "," and depth == 0:
            parts.append(current)
            current = ""
        else:
            current += ch
    parts.append(current)
    return [part.strip() for part in parts if part.strip()]


def span_problems(span: str, fields_here: bool) -> list[str]:
    """What rule 1 finds in one backticked span; `fields_here` when its line says (opaque) or has bullets nested under it."""
    if span.startswith(("#[", "@")) or PLACEHOLDER_RE.search(span):
        return []
    span = VISIBILITY_RE.sub("pub", span)
    if ELISION_RE.search(span) and re.search(r"[({]", span) and not RANGE_RE.search(span):
        return [f"`{short(span)}` elides with .., ... or …: write every parameter and field"]
    problems: list[str] = []
    for signature in SIGNATURE_RE.finditer(span):
        args = split_args(signature.group(3))
        if signature.group(1).split("::")[-1] in CALLS or any(LITERAL_RE.fullmatch(arg) for arg in args):
            continue
        for arg in args:
            if ":" in arg or " " in arg.strip("&* ") or RECEIVER_RE.match(arg):
                continue
            bare = arg.lstrip("&*").strip()
            if BARE_RE.fullmatch(bare) and bare not in PRIMITIVES:
                problems.append(f"`{short(span)}` gives its parameter `{arg}` no type")
    for declared in DECLARATION_RE.finditer(span):
        kind, name = declared.groups()
        if "{" in span or fields_here or re.search(re.escape(name) + r"\s*(?:<[^>]*>)?\(", span):
            continue
        problems.append(
            f"`{short(span)}` declares {kind} {name} without its fields: "
            "list them inline or in bullets nested under it, or say (opaque)"
        )
    return problems


def defines(line: str) -> bool:
    """Whether a contract line holds a signature or a declaration outside an attribute."""
    return any(
        SIGNATURE_RE.search(span) or DECLARATION_RE.search(span)
        for span in CODE_RE.findall(line)
        if not span.startswith(("#[", "@"))
    )


def nested_below(contract: list[tuple[int, str]], i: int) -> bool:
    """Whether the next non-blank contract line is a bullet indented deeper than line i."""
    depth = indent(contract[i][1])
    for _, below in contract[i + 1 :]:
        if below.strip():
            return bool(BULLET_RE.match(below)) and indent(below) > depth
    return False


def owner_lines(contract: list[tuple[int, str]], i: int) -> list[str]:
    """Contract line i, the bullets it is nested under, and the unindented line that introduces its list."""
    line = contract[i][1]
    found = [line]
    if not BULLET_RE.match(line):
        return found
    depth = indent(line)
    for _, above in reversed(contract[:i]):
        if not above.strip():
            break
        if BULLET_RE.match(above):
            if indent(above) < depth:
                found.append(above)
                depth = indent(above)
        elif indent(above) == 0:
            found.append(above)
            break
        # An indented line that is no bullet continues one: skip it.
    return found


def section(head: list[tuple[int, str]], heading: str) -> list[tuple[int, str]] | None:
    """The numbered lines under a `##` heading up to the next one; None without the heading."""
    out: list[tuple[int, str]] | None = None
    for n, line in head:
        if line.rstrip() == heading:
            out = []
        elif H2_RE.match(line):
            if out is not None:
                break
        elif out is not None:
            out.append((n, line))
    return out


def entry_problems(entry: Entry) -> list[str]:
    """What keeps an entry from giving the join its story's opening lines and the drafter its reading."""
    where = f"Story {entry.sid} entry (line {entry.line})"
    problems = [f"{where}: no **{name}:** line" for name in entry.missing()]
    developer = entry.fields.get("Developer", "").strip("` ").removeprefix("flow:")
    if developer and developer not in DEVELOPERS:
        problems.append(f"{where}: Developer '{developer}' is not one of {', '.join(DEVELOPERS)}")
    if entry.fields.get("Files") and not CODE_RE.search(entry.fields["Files"]):
        problems.append(f"{where}: **Files:** names no `backticked` path")
    if entry.fields.get("Verify") and not CODE_RE.search(entry.fields["Verify"]):
        problems.append(f"{where}: **Verify:** names no `backticked` command")
    if "Review" in entry.fields and not REVIEW_VALUE_RE.match(entry.fields["Review"]):
        problems.append(f"{where}: **Review:** must be deep or light, optionally with a reason in parentheses")
    problems += [f"{where}: gives **{name}:** twice" for name in entry.repeated]
    if not entry.reading:
        problems.append(f"{where}: no **Reading:** line: the drafter reads what it names")
    return problems


def contract_problems(contract: list[tuple[int, str]], ids: set[str]) -> list[str]:
    """Rules 1 to 3 over the names contract's lines."""
    problems: list[str] = []
    for i, (n, line) in enumerate(contract):
        proposes = PROPOSES_RE.findall(line)
        for sid in proposes:
            if sid not in ids:
                problems.append(f"line {n}: (Story {sid} proposes) names no story in the list")
        if not proposes:
            fields_here = "(opaque)" in line or nested_below(contract, i)
            for span in CODE_RE.findall(line):
                problems += [f"line {n}: {problem}" for problem in span_problems(span, fields_here)]
        if defines(line) and not any(sid in ids for part in owner_lines(contract, i) for sid in OWNER_RE.findall(part)):
            problems.append(
                f"line {n}: names no owner: put `Story <id>` on the line, on a bullet it is nested under, "
                "or on the line that introduces its list"
            )
    return problems


def lint(text: str, plan: Path) -> tuple[list[str], int, int]:
    """Every problem of the skeleton above its marker, its story count, and its contract's non-blank line count."""
    problems: list[str] = []
    numbered = [(index + 1, line) for index, line in unfenced(text)]
    marks = [n for n, line in numbered if line.rstrip() == MARKER]
    if not marks:
        problems.append(f"no marker line: the skeleton ends with {MARKER}")
    elif len(marks) > 1:
        problems.append(f"{len(marks)} marker lines, expected one")
    end = marks[0] if marks else len(text.split("\n")) + 1
    head = [(n, line) for n, line in numbered if n < end]
    head_text = "\n".join(text.split("\n")[: end - 1])

    spec = next((CODE_RE.findall(m.group(1)) for _, line in head if (m := SPEC_RE.match(line))), None)
    if spec is None:
        problems.append("no **Spec:** line: the drafters and the run find the spec through it")
    elif not spec:
        problems.append("the **Spec:** line names no `backticked` path")
    elif not (repo_root(plan) / spec[0]).is_file():
        problems.append(f"the spec {spec[0]} does not exist")
    for n, line in head:
        if STORY_RE.match(line):
            problems.append(f"line {n}: a ## Story heading: the join writes the stories below the marker")
        elif TASK_RE.match(line):
            problems.append(f"line {n}: a ### heading: the skeleton's sections are ## headings or plain text")

    contract = section(head, CONTRACT_HEADING)
    if contract is None:
        problems.append(f"no {CONTRACT_HEADING} heading: the names contract sits under it, above {STORIES_HEADING}")
        contract = []
    headings = {line.rstrip(): n for n, line in reversed(head) if line.rstrip() in (CONTRACT_HEADING, STORIES_HEADING)}
    if len(headings) == 2 and headings[STORIES_HEADING] < headings[CONTRACT_HEADING]:
        problems.append(f"the names contract must sit above {STORIES_HEADING}")

    try:
        listed = entries(head_text)
    except JoinError as err:
        problems.append(str(err))
        listed = []
    for entry in listed:
        problems += entry_problems(entry)
    for n, line in section(head, STORIES_HEADING) or []:
        if LOOSE_ENTRY_RE.match(line) and not ENTRY_RE.match(line):
            problems.append(f"line {n}: the join reads an entry only as `- **Story <id>**` at column 0: {line.strip()}")
    if listed:
        problems += contract_problems(contract, {entry.sid for entry in listed})
    return problems, len(listed), sum(1 for _, line in contract if line.strip())

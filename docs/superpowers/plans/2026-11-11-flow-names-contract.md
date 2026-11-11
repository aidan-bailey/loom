# Flow names contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Settle a plan's cross-story names before any drafter writes: a skeleton lint for the names contract's form, a question round in `/flow:draft`, and a join that writes each story's opening lines from its skeleton entry.

**Architecture:** `plan_join.py` gains `entries()` (each `## Stories` entry's opening lines) and writes those lines into each joined story, recording their plan lines as `entry` in `join.json`; `plan_packet.py` cites them as the entry. A new `plan_skeleton.py` holds the skeleton lint (`lint()`), which `check_plan.py --skeleton` runs. The draft skill, the drafter's definition, the dispatch guide and loom's flow context describe the question round and the lint.

**Tech Stack:** Python 3 (stdlib `unittest`), Markdown prompts, `bash flow/lib/test.sh` as the gate; Go only to run `go test ./session/ ./flow/` over the embedded files.

**Spec:** `docs/superpowers/specs/2026-11-11-flow-names-contract-design.md`

**Execution:** inline in the design session (the owner's choice, 2026-11-11), four packages, one commit each, no checkpoint pauses. Every test command runs through loom's queue: `loom job run -d "<what>" -- <cmd>`.

---

## File structure

| File | Responsibility |
|---|---|
| `flow/lib/plan_join.py` | `Entry`, `Span`, `entries()`; the join writes entry lines, drops copied ones, returns notes; `join.json` gains `entry` |
| `flow/lib/plan_join_test.py` | fixtures with full entries; the new join behaviour |
| `flow/lib/plan_packet.py` | `cite()` and `checked_map()` on `Span` |
| `flow/lib/plan_packet_test.py` | fixtures: fields move from drafts to the skeleton; `cite()` on entries |
| `flow/lib/plan_skeleton.py` (new) | `lint()`: the head, rules 1–3, the entries |
| `flow/lib/plan_skeleton_test.py` (new) | both sides of every rule; the anonymised fixture |
| `flow/lib/check_plan.py` | `--skeleton PLAN`; `repo_problem()` shared with the plan check |
| `flow/lib/check_plan_test.py` | the `--skeleton` command line |
| `flow/plugin/skills/draft/SKILL.md` | step 1's lint, step 2's question round, entry amendments without fix jobs, the `questions` phase |
| `flow/plugin/agents/drafter.md` | the question-round job and its file; no copied opening lines |
| `flow/plugin/docs/dispatch.md` | the design session's lint, the contract rules, the question round, the join's entry lines |
| `session/loom_context_flow.md` | the design session runs the lint before committing |
| `flow/README.md`, `flow/PROVENANCE.md` | the tool table; the drafter's entry |

---

## Task 1: The join writes each story's opening lines

**Files:**
- Modify: `flow/lib/plan_join.py`
- Modify: `flow/lib/plan_join_test.py`
- Modify: `flow/lib/plan_packet.py:67-72` (`cite`), `flow/lib/plan_packet.py:309-321` (`checked_map`)
- Modify: `flow/lib/plan_packet_test.py` (fixtures `SKELETON`, `DRAFT_1`, `DRAFT_2`, `build`, `CiteTest`)

Layout of a joined story (heading at plan line P, the entry's E opening lines, a draft whose kept body starts at 0-based index `rest`):

```text
P            ## Story <id>: <title>         draft line h+1 (h leading blank lines, dropped)
P+1          (blank)
P+2..P+1+E   the entry's opening lines      join.json "entry": [P+2, P+1+E]
P+2+E        (blank, inserted only when draft line rest+1 is not blank)
...          draft lines rest+1 .. L        draft line k sits at plan line start + k - 1
```

`start = P + 1 + len(block) - rest`, where `block` is `["", *opening]` plus a trailing `""` when the first kept draft line isn't blank. `rest` is `h + 1`, or the index after a block of copied opening lines (and the blank lines before it).

- [ ] **Step 1: Write the failing join tests**

In `flow/lib/plan_join_test.py`, replace the fixtures from `SKELETON_LINES = [` through `EXPECTED_MAP = …` with:

```python
from plan_join import MARKER, JoinError, Span  # noqa: E402  (replaces the existing import line)

ENTRY_1 = [
    "- **Developer:** junior-dev",
    "- **Files:** `a.py`",
    "- **Interfaces:** none",
    "- **Verify:** `python3 -m unittest a_test.py`",
]
ENTRY_2 = [
    "- **Developer:** intermediate-dev",
    "- **Files:** `b.py`",
    "- **Interfaces:** uses `a` from Story 1",
    "- **Verify:** `python3 -m unittest b_test.py`",
    "- **Review:** light",
]
LIST_1 = ["- **Story 1** — first.", "", "  The paragraph.", "", *("  " + line for line in ENTRY_1), "  - **Reading:** spec §1"]
LIST_2 = ["- **Story 2b** — second.", *("  " + line for line in ENTRY_2), "  - **Reading:** spec §2"]


def skeleton(*blocks):
    """A skeleton whose story list holds the given entry blocks, in order."""
    return "\n".join(["# Plan", "", "## Stories", "", *(line for block in blocks for line in block), "", MARKER]) + "\n"


SKELETON = skeleton(LIST_1, LIST_2)
SKELETON_LINES = SKELETON.rstrip("\n").split("\n")
DRAFT_1 = "## Story 1: First\n\n### Task 1.1: x\n"
DRAFT_2 = "## Story 2b: Second\n\ntext\n"
# The marker is plan line 22. Story 1: heading 24, entry 26-29, draft lines 2-3 at 30-31 (start 29).
# Story 2b: heading 33, entry 35-39, draft lines 2-3 at 40-41 (start 39).
STORY_1 = ["## Story 1: First", "", *ENTRY_1, "", "### Task 1.1: x"]
STORY_2 = ["## Story 2b: Second", "", *ENTRY_2, "", "text"]
EXPECTED = SKELETON + "\n" + "\n".join(STORY_1) + "\n\n" + "\n".join(STORY_2) + "\n"
EXPECTED_MAP = {"1": Span(29, 3, (26, 29)), "2b": Span(39, 3, (35, 39))}
COPIED = "dropped its copied opening lines; joined the entry's"
```

Every `out, story_map = plan_join.join(...)` and `out, _ = plan_join.join(...)` in the file becomes a three-way unpack (`out, story_map, notes = …`, `out, _, _ = …`); `first, first_map = …` becomes `first, first_map, _ = …`.

Replace these `JoinTest` methods:

```python
    def test_order_follows_the_list_not_the_file_names(self):
        t = self.tree()
        out, story_map, _ = plan_join.join(skeleton(LIST_2, LIST_1), t.drafts)
        self.assertLess(out.index("## Story 2b"), out.index("## Story 1:"))
        self.assertEqual(story_map, {"2b": Span(30, 3, (26, 30)), "1": Span(39, 3, (36, 39))})

    def test_map_points_at_each_drafts_lines(self):
        t = self.tree()
        out, story_map, _ = plan_join.join(SKELETON, t.drafts)
        out_lines = out.split("\n")
        for sid, body, opening in (("1", DRAFT_1, ENTRY_1), ("2b", DRAFT_2, ENTRY_2)):
            span = story_map[sid]
            draft_lines = body.rstrip().split("\n")
            self.assertEqual(span.lines, len(draft_lines))
            first, last = span.entry
            self.assertEqual(out_lines[first - 3], draft_lines[0])
            self.assertEqual(out_lines[first - 1 : last], opening)
            for k, line in enumerate(draft_lines[1:], 2):
                self.assertEqual(out_lines[span.start + k - 2], line)

    def test_leading_blank_lines_keep_the_drafts_line_numbers(self):
        t = self.tree(drafts={"1": "\n" + DRAFT_1, "2b": DRAFT_2})
        out, story_map, _ = plan_join.join(SKELETON, t.drafts)
        self.assertEqual(out, EXPECTED)
        # Draft line 4 (the task) is still plan line 31: start + 4 - 1.
        self.assertEqual(story_map, {"1": Span(28, 4, (26, 29)), "2b": EXPECTED_MAP["2b"]})

    def test_a_draft_holding_the_marker_outside_a_fence_fails_the_first_join(self):
        t = self.tree(drafts={"1": DRAFT_1, "2b": DRAFT_2 + MARKER + "\n"})
        with self.assertRaisesRegex(JoinError, r"S2b.md holds the marker line outside a fence \(plan line 42\)"):
            plan_join.join(SKELETON, t.drafts)

    def test_a_marker_above_the_story_list_fails_instead_of_dropping_it(self):
        t = self.tree()
        text = "# Plan\n\n" + MARKER + "\n\n## Stories\n\n" + "\n".join(LIST_1 + LIST_2) + "\n"
        with self.assertRaisesRegex(JoinError, "## Stories section must sit above the marker line"):
            plan_join.join(text, t.drafts)
```

In `test_files_that_are_not_story_drafts_are_ignored`, after the `packets` lines, add:

```python
        (t.drafts / "questions").mkdir()
        (t.drafts / "questions" / "S1.md").write_text("## Names used\nnone\n")
```

Append to `JoinTest`:

```python
    def test_the_entry_gives_the_opening_lines_and_not_its_paragraph_or_reading(self):
        t = self.tree()
        out, _, notes = plan_join.join(SKELETON, t.drafts)
        generated = out[len(SKELETON):]
        self.assertNotIn("Reading", generated)
        self.assertNotIn("The paragraph.", generated)
        self.assertIn("\n- **Review:** light\n", generated)
        self.assertEqual(notes, [])

    def test_copied_opening_lines_are_dropped_and_reported(self):
        stale = "## Story 1: First\n\n- **Developer:** senior-dev\n- **Files:** `old.py`\n\n### Task 1.1: x\n"
        t = self.tree(drafts={"1": stale, "2b": DRAFT_2})
        out, story_map, notes = plan_join.join(SKELETON, t.drafts)
        self.assertEqual(out, EXPECTED)
        self.assertEqual(notes, [f"S1.md: {COPIED}"])
        # The task is draft line 6 and plan line 31.
        self.assertEqual(story_map["1"], Span(26, 6, (26, 29)))
        self.assertEqual(out.split("\n")[26 + 6 - 2], "### Task 1.1: x")

    def test_a_heading_followed_by_text_gets_a_blank_line_after_the_entry(self):
        t = self.tree(drafts={"1": DRAFT_1, "2b": "## Story 2b: Second\ntext\n"})
        out, story_map, _ = plan_join.join(SKELETON, t.drafts)
        self.assertEqual(out, EXPECTED)
        self.assertEqual(story_map["2b"], Span(40, 2, (35, 39)))

    def test_a_draft_of_only_its_heading_ends_at_its_entry(self):
        t = self.tree(drafts={"1": DRAFT_1, "2b": "## Story 2b: Second\n"})
        out, story_map, _ = plan_join.join(SKELETON, t.drafts)
        self.assertTrue(out.endswith("\n".join(["## Story 2b: Second", "", *ENTRY_2]) + "\n"))
        self.assertEqual(story_map["2b"], Span(39, 1, (35, 39)))

    def test_an_entry_without_an_opening_line_fails(self):
        t = self.tree()
        listed = [line for line in LIST_2 if "**Verify:**" not in line]
        with self.assertRaisesRegex(JoinError, r"the Story 2b entry lacks \*\*Verify:\*\*"):
            plan_join.join(skeleton(LIST_1, listed), t.drafts)

    def test_an_entry_giving_a_line_twice_fails(self):
        t = self.tree()
        listed = [*LIST_2, "  - **Files:** `c.py`"]
        with self.assertRaisesRegex(JoinError, r"the Story 2b entry gives \*\*Files:\*\* twice"):
            plan_join.join(skeleton(LIST_1, listed), t.drafts)
```

Add an `EntriesTest` class after `StoryOrderTest`:

```python
class EntriesTest(unittest.TestCase):
    def test_each_entry_gives_its_line_opening_lines_and_reading(self):
        first, second = plan_join.entries(SKELETON)
        self.assertEqual((first.sid, first.line, first.opening, first.reading), ("1", 5, ENTRY_1, True))
        self.assertEqual(first.fields["Files"], "`a.py`")
        self.assertEqual((second.sid, second.line, second.opening), ("2b", 14, ENTRY_2))
        self.assertEqual(first.missing(), [])

    def test_a_missing_and_a_repeated_line_are_recorded(self):
        listed = [line for line in LIST_2 if "**Reading:**" not in line and "**Files:**" not in line]
        (entry,) = plan_join.entries(skeleton(listed + ["  - **Review:** deep"]))
        self.assertEqual(entry.missing(), ["Files"])
        self.assertEqual(entry.repeated, ["Review"])
        self.assertFalse(entry.reading)
```

In `LoadMapTest`, replace `test_map_is_read_back_as_tuples` and extend the malformed list:

```python
    def test_malformed_map_is_a_join_error(self):
        t = self.tree()
        bad_maps = [
            "{not json", "{}", '{"stories": [{"id": "1"}]}', '{"stories": 3}',
            # A map written before entries: re-join.
            '{"stories": [{"id": "1", "start": 4, "lines": 2}]}',
            '{"stories": [{"id": "1", "start": 4, "lines": 2, "entry": [3]}]}',
        ]
        for bad in bad_maps:
            (t.drafts / "join.json").write_text(bad)
            with self.assertRaisesRegex(JoinError, "join.json is not a join map; re-run loom flow join"):
                plan_join.load_map(t.drafts)

    def test_map_is_read_back_as_spans(self):
        t = self.tree()
        payload = {"stories": [{"id": "1", "start": 40, "lines": 120, "entry": [37, 40]},
                               {"id": "2b", "start": 161, "lines": 7, "entry": [158, 161]}]}
        (t.drafts / "join.json").write_text(json.dumps(payload))
        self.assertEqual(plan_join.load_map(t.drafts), {"1": Span(40, 120, (37, 40)), "2b": Span(161, 7, (158, 161))})
```

In `CliTest.test_rewrites_the_plan_and_writes_the_map`, replace the last three assertions with:

```python
        written = {"stories": [{"id": "1", "start": 29, "lines": 3, "entry": [26, 29]},
                               {"id": "2b", "start": 39, "lines": 3, "entry": [35, 39]}]}
        self.assertEqual((t.drafts / "join.json").read_text(), json.dumps(written, indent=1) + "\n")
        self.assertEqual(plan_join.load_map(t.drafts), EXPECTED_MAP)
```

and append to `CliTest`:

```python
    def test_a_dropped_copy_is_printed_before_the_joined_line(self):
        t = self.tree(drafts={"1": "## Story 1: First\n\n- **Files:** `old.py`\n\n### Task 1.1: x\n", "2b": DRAFT_2})
        result = run_cli(t.plan)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        total = len(EXPECTED.splitlines())
        self.assertEqual(result.stdout, f"S1.md: {COPIED}\njoined 2 stories into {t.plan}: {total} lines\n")
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "plan_join tests (red)" -- sh -c 'cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q plan_join_test.py'`
Expected: an ImportError on `Span` (nothing else runs).

- [ ] **Step 3: Implement the join**

In `flow/lib/plan_join.py`:

1. Module docstring: after the paragraph that ends "a `## Stories` section below it is generated text.", add:

```text
Each entry gives its story's opening lines as bullets nested under it (Developer, Files, Interfaces,
Verify and, when the story has it, Review), and a draft holds none of them: the join writes each
story as its draft's heading, a blank line, the entry's opening lines, a blank line, and the rest of
the draft. A draft written before that rule opens with a copy, which the join drops and reports.
```

and replace "which maps each story to the plan lines its draft landed on." with "which maps each story to the plan lines its draft and its entry's opening lines landed on."

2. Imports and constants:

```python
from dataclasses import dataclass, field
from typing import NamedTuple

from check_plan import FENCE_RE, FIELD_RE, FIELDS, H2_RE, STORY_RE, repo_root  # noqa: E402

READING_RE = re.compile(r"^\s*[-*]\s+\*\*Reading:\*\*")
COPIED_NOTE = "dropped its copied opening lines; joined the entry's"
```

3. After `JoinError`, add:

```python
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

    def missing(self) -> list[str]:
        """The required opening lines the entry lacks, in FIELDS order."""
        return [name for name in FIELDS if not self.fields.get(name)]


class Span(NamedTuple):
    """Where a story landed: its draft line k after the heading is plan line `start + k - 1`, and
    `entry` is the first and last plan line of the opening lines the join wrote from its entry."""

    start: int
    lines: int
    entry: tuple[int, int]
```

4. Replace `story_order` with `entries` and a `story_order` on top of it:

```python
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
    if not found:
        raise JoinError(f"the plan has no {STORIES_HEADING} section")
    if not listed:
        raise JoinError(f"the {STORIES_HEADING} section lists no stories")
    return listed


def story_order(text: str) -> list[str]:
    """The ids of the `## Stories` entries, in order."""
    return [entry.sid for entry in entries(text)]
```

5. In `join`, change the signature and docstring to:

```python
def join(text: str, drafts: Path) -> tuple[str, dict[str, Span], list[str]]:
    """The plan with its stories rebuilt from the drafts and the entries, where each landed, and the notes.

    Each story is its draft's heading, a blank line, its entry's opening lines, and the draft from its
    second line on, a blank line between: the draft's own blank line when it has one. Leading blank
    lines are dropped, and so is a block of opening lines copied into the draft, with a note
    (`S<id>.md: dropped its copied opening lines; joined the entry's`). A draft line k after the
    heading is plan line `start + k - 1`, so `k = n - start + 1` still turns a plan line into a draft
    line; `entry` gives the plan lines of the opening lines.
    """
```

replace `order = story_order(head_text)` with:

```python
        listed = entries(head_text)
```

(the `try`/`except JoinError` around it stays), then right after that block:

```python
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
```

and replace everything from `head = lines[: marks[0] + 1]` through `return joined, story_map` with:

```python
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
```

6. `load_map`:

```python
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
```

7. `main`: `joined, story_map, notes = join(text, drafts)`; the map line becomes

```python
    stories = [
        {"id": sid, "start": span.start, "lines": span.lines, "entry": list(span.entry)} for sid, span in story_map.items()
    ]
```

and before `total = …`, add `for note in notes: print(note)` (two lines).

- [ ] **Step 4: Run the join tests to see them pass**

Run: `loom job run -d "plan_join tests" -- sh -c 'cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q plan_join_test.py'`
Expected: `OK`.

- [ ] **Step 5: Write the failing packet tests**

In `flow/lib/plan_packet_test.py`:
- `SKELETON`'s story list becomes:

```text
    - **Story 1** — Prices.
      - **Developer:** junior-dev
      - **Files:** `src/rows.rs`, `src/short.rs`, `src/new.rs`
      - **Interfaces:** exposes `lookup_price`
      - **Verify:** `cargo test`
    - **Story 2** — Screen.
      - **Developer:** junior-dev
      - **Files:** `src/short.rs`
      - **Interfaces:** uses `lookup_price` from Story 1
      - **Verify:** `cargo test`
```

- `DRAFT_1` and `DRAFT_2` lose their four opening lines and the blank line after them, so each goes from its heading and a blank line straight to its `### Task` heading. The joined `PLAN` is the same text as before.
- `build` returns the join's map unchanged: `text, story_map, _ = plan_join.join(skeleton, Path(tmp))`, then `return text, story_map`, with its docstring saying `{id: Span}`.
- `CiteTest` becomes:

```python
class CiteTest(unittest.TestCase):
    # Story 1: heading 5, entry 7-8, draft lines 2-5 at 9-12. Story 2: heading 14, entry 16-17, draft lines 2-3 at 18-19.
    MAP = {"1": plan_join.Span(8, 5, (7, 8)), "2": plan_join.Span(17, 3, (16, 17))}

    def test_a_plan_line_inside_a_draft_cites_the_draft_line(self):
        self.assertEqual(plan_packet.cite(9, self.MAP), "S1.md:2")
        self.assertEqual(plan_packet.cite(12, self.MAP), "S1.md:5")
        self.assertEqual(plan_packet.cite(19, self.MAP), "S2.md:3")

    def test_a_line_the_join_wrote_from_an_entry_cites_the_entry(self):
        self.assertEqual(plan_packet.cite(7, self.MAP), "the Story 1 entry (plan:7)")
        # Plan line 8 is also `start`: the entry wins, as no draft line sits there.
        self.assertEqual(plan_packet.cite(8, self.MAP), "the Story 1 entry (plan:8)")
        self.assertEqual(plan_packet.cite(16, self.MAP), "the Story 2 entry (plan:16)")

    def test_a_plan_line_outside_every_draft_cites_the_plan(self):
        for line in (4, 5, 6, 13, 14, 15, 20):
            self.assertEqual(plan_packet.cite(line, self.MAP), f"plan:{line}")
        self.assertEqual(plan_packet.cite(7, {}), "plan:7")
```

- `SeamsTest.test_a_shared_name_is_listed_with_its_definition_and_use` computes `k` and `j` with `JOIN_MAP["1"].start` and `JOIN_MAP["2"].start`.

- [ ] **Step 6: Run them to see them fail**

Run: `loom job run -d "plan_packet tests (red)" -- sh -c 'cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q plan_packet_test.py'`
Expected: failures in `CiteTest` (`cite` unpacks two values from a `Span`) and errors in the seams and command-line tests (`checked_map` unpacks two values from `join`).

- [ ] **Step 7: Implement cite and checked_map**

In `flow/lib/plan_packet.py`, import `Span` from `plan_join` beside `load_map`, and replace `cite` and `checked_map`:

```python
def cite(plan_line: int, story_map: dict[str, Span]) -> str:
    """`S<id>.md:<k>` for the draft line a plan line holds, the story's entry for an opening line the
    join wrote from it, else `plan:<n>`."""
    for sid, span in story_map.items():
        first, last = span.entry
        if first <= plan_line <= last:
            return f"the Story {sid} entry (plan:{plan_line})"
        if last < plan_line < span.start + span.lines:
            return f"S{sid}.md:{plan_line - span.start + 1}"
    return f"plan:{plan_line}"
```

```python
def checked_map(text: str, drafts: Path) -> dict[str, Span]:
    """The join map of the drafts, once the plan is shown to be their join; `{}` when there is none.

    A plan edited, or drafts changed, since the join leaves the map pointing at the wrong lines, and every
    citation with it: the joined text and the recomputed map must both match what is on disk.
    """
    story_map = load_map(drafts)
    if story_map:
        joined, fresh, _ = join(text, drafts)
        # An edit above the marker leaves the joined text as it was and moves every start line.
        if joined != text or fresh != story_map:
            raise JoinError("the plan is out of date with its drafts; re-run loom flow join")
    return story_map
```

- [ ] **Step 8: Run every flow test**

Run: `loom job run -d "flow lib tests" -- bash flow/lib/test.sh`
Expected: `OK` from unittest and no FAIL from the agent lint.

- [ ] **Step 9: Commit**

```bash
git add flow/lib/plan_join.py flow/lib/plan_join_test.py flow/lib/plan_packet.py flow/lib/plan_packet_test.py
git commit -m "feat(flow): the join writes each story's opening lines from its skeleton entry" -- flow/lib/plan_join.py flow/lib/plan_join_test.py flow/lib/plan_packet.py flow/lib/plan_packet_test.py
```

---

## Task 2: `loom flow check-plan --skeleton`

**Files:**
- Create: `flow/lib/plan_skeleton.py`
- Create: `flow/lib/plan_skeleton_test.py`
- Modify: `flow/lib/check_plan.py` (docstring, `main`, a new `repo_problem` and `skeleton_main`)
- Modify: `flow/lib/check_plan_test.py` (a `SkeletonCliTest` class)

- [ ] **Step 1: Write the failing lint tests**

Create `flow/lib/plan_skeleton_test.py`:

```python
"""Tests for plan_skeleton.py. Run from lib/: python3 -m unittest -q plan_skeleton_test.py"""

import re
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plan_skeleton  # noqa: E402
from plan_join import MARKER  # noqa: E402


def entry(sid, files="`a.py`", extra=()):
    """A well-formed story-list entry."""
    return [
        f"- **Story {sid}** — story {sid}.",
        "  - **Developer:** intermediate-dev",
        f"  - **Files:** {files}",
        "  - **Interfaces:** none",
        "  - **Verify:** `python3 -m unittest a_test.py`",
        *extra,
        "  - **Reading:** spec §1",
    ]


def skeleton(contract, stories=None, head=("# Plan", "", "**Spec:** `docs/spec.md`", "")):
    """A skeleton around contract lines, with Stories 1 to 5 unless `stories` gives the list's lines."""
    if stories is None:
        stories = [line for sid in "12345" for line in entry(sid)]
    return "\n".join([*head, "## Names contract", "", *contract, "", "## Stories", "", *stories, "", MARKER]) + "\n"


def lint(text, spec=True):
    """lint's problems for a skeleton written into a throwaway repo."""
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        (root / ".git").mkdir()
        if spec:
            (root / "docs").mkdir()
            (root / "docs" / "spec.md").write_text("# Spec\n")
        plan = root / "plan.md"
        plan.write_text(text)
        problems, _, _ = plan_skeleton.lint(text, plan)
        return problems


def flagged(text, problems):
    """The stripped text of each plan line a `line <n>:` problem names."""
    lines = text.split("\n")
    return {lines[int(m.group(1)) - 1].strip() for p in problems if (m := re.match(r"line (\d+): ", p))}


class SpanRuleTest(unittest.TestCase):
    def problems(self, span, fields_here=False):
        return plan_skeleton.span_problems(span, fields_here)

    def test_an_elision_fails_and_a_range_passes(self):
        for span in ("delete_snapshot(..)", "open_tab(.., …)", "search(handles: &H, ...) -> Vec<Hit>", "Item { id, .. }"):
            self.assertTrue(self.problems(span), span)
        for span in ("slice[0..5]", "take(0..5)", "get(0..=3)"):
            self.assertEqual(self.problems(span), [], span)

    def test_a_bare_parameter_fails_and_a_typed_one_or_a_primitive_or_a_receiver_passes(self):
        self.assertEqual(self.problems("changed(scope_key, id: &Id)"), ["`changed(scope_key, id: &Id)` gives its parameter `scope_key` no type"])
        self.assertEqual(len(self.problems("subscribe(id, onEvent)")), 2)
        for span in ("f(x: u32) -> f64", "f(&self, cell: u32)", "f(&mut self)", "f(self)", "m(this)", "f(f64)",
                     "update(&Id, HeaderUpdate)", "list(running: &dyn Fn(&Id) -> bool, limit: u32)",
                     "on(cb: (x: number) => void, n: number)", "f()"):
            self.assertEqual(self.problems(span), [], span)

    def test_a_fieldless_type_fails_and_fields_opaque_a_tuple_or_nested_bullets_pass(self):
        self.assertTrue(self.problems("pub struct HubSet"))
        self.assertTrue(self.problems("export interface Props"))
        for span in ("pub struct S { a: u32 }", "pub struct Id(String)", "pub enum E { A, B }"):
            self.assertEqual(self.problems(span), [], span)
        self.assertEqual(self.problems("pub struct Cache", fields_here=True), [])

    def test_attributes_patterns_visibility_and_calls_are_not_signatures(self):
        for span in ('#[serde(tag = "kind")]', "@Component(x)", "<workspace>/cells/<cell>.parquet",
                     "pub(crate) fn path(&self, cell: u32) -> PathBuf", 'plan_launch("nope")', "retry(3)",
                     "Some(id)", "Ok(value)", "Err(e)"):
            self.assertEqual(self.problems(span), [], span)


class OwnerRuleTest(unittest.TestCase):
    def test_an_unowned_defining_line_fails(self):
        text = skeleton(["- `pub fn rows(id: u32) -> Vec<Row>`."])
        self.assertEqual(flagged(text, lint(text)), {"- `pub fn rows(id: u32) -> Vec<Row>`."})

    def test_an_owner_on_the_line_a_parent_bullet_or_the_introducing_line_passes(self):
        contract = [
            "- `pub fn a(x: u32)` (Story 1).",
            "- `Store` (Story 2), with:",
            "  - `get(id: u32) -> Row`;",
            "  continued text",
            "  - `put(row: Row)`.",
            "",
            "**crate-b** (Story 3)",
            "- `pub fn b(y: u32)`.",
            "  - `pub fn c(z: u32)`.",
            "",
            "**Story 4's interim arms.** Each is replaced later.",
            "- `pub fn d(w: u32)`.",
        ]
        self.assertEqual(lint(skeleton(contract)), [])

    def test_a_blank_line_ends_the_walk_and_an_unlisted_owner_does_not_count(self):
        contract = ["**crate-b** (Story 3)", "", "- `pub fn b(y: u32)`.", "- `pub fn c(z: u32)` (Story 9)."]
        text = skeleton(contract)
        self.assertEqual(flagged(text, lint(text)), {"- `pub fn b(y: u32)`.", "- `pub fn c(z: u32)` (Story 9)."})

    def test_a_line_without_a_signature_needs_no_owner(self):
        self.assertEqual(lint(skeleton(["Wire types use `#[serde(rename_all = \"camelCase\")]` and `u32` ids."])), [])


class ProposesRuleTest(unittest.TestCase):
    def test_proposes_waives_rule_one_and_names_the_owner(self):
        self.assertEqual(lint(skeleton(["- `pub struct CollectHandles` and `search(handles, …)` (Story 4 proposes)."])), [])

    def test_proposes_for_an_unlisted_story_fails(self):
        problems = lint(skeleton(["- `pub struct Handles` (Story 99 proposes)."]))
        self.assertEqual(len(problems), 2, problems)
        self.assertIn("(Story 99 proposes) names no story in the list", problems[0])


class HeadTest(unittest.TestCase):
    def test_a_good_skeleton_passes_with_its_counts(self):
        text = skeleton(["- `pub fn a(x: u32)` (Story 1).", "", "- `pub fn b(x: u32)` (Story 2)."])
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / ".git").mkdir()
            (Path(tmp) / "docs").mkdir()
            (Path(tmp) / "docs" / "spec.md").write_text("# Spec\n")
            self.assertEqual(plan_skeleton.lint(text, Path(tmp) / "plan.md"), ([], 5, 2))

    def test_a_missing_contract_heading_fails(self):
        text = skeleton([]).replace("## Names contract\n", "## Contract\n")
        self.assertTrue(any("no ## Names contract heading" in p for p in lint(text)))

    def test_a_contract_below_the_stories_fails(self):
        stories = [line for sid in "12345" for line in entry(sid)]
        text = "\n".join(["**Spec:** `docs/spec.md`", "", "## Stories", "", *stories, "", "## Names contract", "", MARKER]) + "\n"
        self.assertTrue(any("must sit above ## Stories" in p for p in lint(text)))

    def test_no_marker_and_two_markers_fail(self):
        text = skeleton([])
        self.assertTrue(any("no marker line" in p for p in lint(text.replace(MARKER + "\n", ""))))
        self.assertTrue(any("2 marker lines" in p for p in lint(text + MARKER + "\n")))

    def test_the_spec_line_must_name_a_file_that_exists(self):
        self.assertTrue(any("no **Spec:** line" in p for p in lint(skeleton([], head=("# Plan", "")))))
        self.assertTrue(any("the spec docs/spec.md does not exist" in p for p in lint(skeleton([]), spec=False)))

    def test_a_story_heading_or_a_task_heading_above_the_marker_fails(self):
        text = skeleton([], head=("**Spec:** `docs/spec.md`", "", "## Story 1: early", "", "### Section", ""))
        problems = lint(text)
        self.assertTrue(any("a ## Story heading" in p for p in problems), problems)
        self.assertTrue(any("a ### heading" in p for p in problems), problems)

    def test_a_contract_line_quoted_in_a_fence_is_not_checked(self):
        self.assertEqual(lint(skeleton(["```text", "- `f(x)`", "```"])), [])


class EntryTest(unittest.TestCase):
    def stories(self, *blocks):
        return lint(skeleton([], stories=[line for block in blocks for line in block]))

    def test_each_missing_line_fails(self):
        for name in ("Developer", "Files", "Interfaces", "Verify", "Reading"):
            block = [line for line in entry("1") if f"**{name}:**" not in line]
            problems = self.stories(block)
            self.assertTrue(any(f"Story 1 entry (line " in p and f"**{name}:**" in p for p in problems), (name, problems))

    def test_bad_values_fail_and_good_ones_pass(self):
        self.assertTrue(any("not one of" in p for p in self.stories([l.replace("intermediate-dev", "staff-dev") for l in entry("1")])))
        self.assertTrue(any("names no `backticked` path" in p for p in self.stories(entry("1", files="a.py"))))
        self.assertTrue(any("names no `backticked` command" in p for p in self.stories([l.replace("`python3 -m unittest a_test.py`", "run it") for l in entry("1")])))
        self.assertTrue(any("deep or light" in p for p in self.stories(entry("1", extra=["  - **Review:** shallow"]))))
        self.assertEqual(self.stories(entry("1", extra=["  - **Review:** light (a doc change)"]), [l.replace("intermediate-dev", "flow:junior-dev") for l in entry("2")]), [])

    def test_a_repeated_line_fails(self):
        self.assertTrue(any("gives **Files:** twice" in p for p in self.stories(entry("1", extra=["  - **Files:** `b.py`"]))))

    def test_an_entry_the_join_cannot_read_fails(self):
        for bad in ("- **Story 2:** title", "- **Story 2**: title", "1. **Story 2** — title", "  - **Story 2** — title"):
            problems = self.stories(entry("1"), [bad])
            self.assertTrue(any("the join reads an entry only as" in p for p in problems), (bad, problems))

    def test_an_empty_list_fails(self):
        self.assertTrue(any("lists no stories" in p for p in self.stories([])))


# A contract modelled on a real run's (2026-11-11), with its names replaced: what the lint must flag and what it must pass.
MODELLED = textwrap.dedent(
    """\
    Every name below is new unless marked "existing". Wire types use `#[serde(rename_all = "camelCase")]` and never `i64`: ids are `u32`.

    **notes-protocol** (Story 1)
    - `pub fn is_note_id(s: &str) -> bool`: `n` then 12 characters, beside `is_task_id`.
    - `ANCHOR_KINDS: [&str; 9]`, with `"note"` appended last.

    **notes-core**
    - **`anchor.rs` (Story 1):**
      - `pub struct NoteId(String)`, mirroring `TaskId`.
      - `SessionAnchor::Note { id: NoteId }`, with `SessionAnchor::note(id: NoteId)`.
    - **`store.rs` (Story 2):** `pub(crate) fn cell_path(&self, cell: u32) -> PathBuf`: `<workspace>/cells/<cell>.parquet`.
    - `pub enum NoteError { Gone, Invalid(String), Busy(String) }` (Story 2).
    - `pub struct NoteStore` (Story 2), with:
      - `create(name: Option<String>) -> Result<NoteId, NoteError>`;
      - `list(running: &dyn Fn(&NoteId) -> bool) -> Result<Vec<NoteSummary>, NoteError>`.
    - `pub enum SnapshotSource` (Story 2):
      - `Spot { pair: String }`;
      - `Perp { key: String }`.
    - `pub struct CellCache` (Story 3) (opaque).
    - `COLLAPSE_WINDOW = Duration::from_secs(300)` (Story 3).
    - `plan_launch("nope")` refuses a note anchor until Story 5 replaces it.
    - `pub struct HubSet` (Story 3).
    - `NotesHub::changed(scope_key, &NoteId)` (Story 3).
    - `publish(store: &NoteStore, id: &NoteId, running)` (Story 3).
    - `search(handles: &CollectHandles, …) -> Vec<Hit>` (Story 4).
    - `delete_snapshot(..)` (Story 4).
    - `run_stale(&self, cancel) -> u32` (Story 4).
    - `subscribe(id, onEvent)` (Story 5).
    - `pub struct CollectHandles` (Story 4 proposes).
    - `pub fn rows_for(id: &NoteId, window: Window) -> Vec<Row>`.
    """
).rstrip("\n").split("\n")
MODELLED_FLAGS = {
    "- `pub struct HubSet` (Story 3).",
    "- `NotesHub::changed(scope_key, &NoteId)` (Story 3).",
    "- `publish(store: &NoteStore, id: &NoteId, running)` (Story 3).",
    "- `search(handles: &CollectHandles, …) -> Vec<Hit>` (Story 4).",
    "- `delete_snapshot(..)` (Story 4).",
    "- `run_stale(&self, cancel) -> u32` (Story 4).",
    "- `subscribe(id, onEvent)` (Story 5).",
    "- `pub fn rows_for(id: &NoteId, window: Window) -> Vec<Row>`.",
}


class ModelledContractTest(unittest.TestCase):
    def test_the_modelled_contract_flags_exactly_its_gaps(self):
        text = skeleton(MODELLED)
        problems = lint(text)
        self.assertEqual(flagged(text, problems), MODELLED_FLAGS)
        # subscribe's two bare parameters are two problems.
        self.assertEqual(len(problems), len(MODELLED_FLAGS) + 1, problems)


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "plan_skeleton tests (red)" -- sh -c 'cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q plan_skeleton_test.py'`
Expected: `ModuleNotFoundError: No module named 'plan_skeleton'`.

- [ ] **Step 3: Implement the lint**

Create `flow/lib/plan_skeleton.py`:

```python
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
```

- [ ] **Step 4: Run the lint tests to see them pass**

Run: `loom job run -d "plan_skeleton tests" -- sh -c 'cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q plan_skeleton_test.py'`
Expected: `OK`.

- [ ] **Step 5: Write the failing command-line tests**

Append to `flow/lib/check_plan_test.py`, before `if __name__`, or at the end when it has none:

```python
SKELETON_TEXT = textwrap.dedent(
    """\
    # Plan

    **Spec:** `docs/spec.md`

    ## Names contract

    - `pub fn get(id: u32) -> Row` (Story 1).

    ## Stories

    - **Story 1** — store.
      - **Developer:** junior-dev
      - **Files:** `a/store.rs`
      - **Interfaces:** exposes `get`
      - **Verify:** `cargo nextest run -p a store`
      - **Reading:** spec §1

    <!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
    """
)


def run_skeleton(text: str, *extra: str) -> tuple[int, str, str]:
    with tempfile.TemporaryDirectory() as tmp:
        subprocess.run(["git", "-C", tmp, "init", "-q"], check=True, capture_output=True, env=CLEAN_ENV)
        Path(tmp, "docs").mkdir()
        Path(tmp, "docs", "spec.md").write_text("# Spec\n")
        plan = Path(tmp, "plan.md")
        plan.write_text(text)
        env = {**CLEAN_ENV, "FLOW_HOME": str(Path(tmp, "flow-home"))}
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--skeleton", str(plan), *extra], capture_output=True, text=True, env=env, cwd=tmp
        )
        return result.returncode, result.stdout.replace(str(plan), "PLAN"), result.stderr


class SkeletonCliTest(unittest.TestCase):
    def test_a_good_skeleton_passes_without_a_profile(self):
        code, out, _ = run_skeleton(SKELETON_TEXT)
        self.assertEqual((code, out), (0, "OK: PLAN: skeleton; 1 stories; contract 1 lines\n"))

    def test_each_problem_is_one_fail_line(self):
        code, out, _ = run_skeleton(SKELETON_TEXT.replace("get(id: u32)", "get(id)").replace("  - **Reading:** spec §1\n", ""))
        self.assertEqual(code, 1)
        self.assertEqual(out.splitlines(), [
            "FAIL [skeleton] PLAN: Story 1 entry (line 11): no **Reading:** line: the drafter reads what it names",
            "FAIL [skeleton] PLAN: line 7: `pub fn get(id) -> Row` gives its parameter `id` no type",
        ])

    def test_skeleton_takes_one_plan(self):
        code, _, err = run_skeleton(SKELETON_TEXT, "other.md")
        self.assertEqual(code, 2)
        self.assertIn("usage", err)
```

- [ ] **Step 6: Run them to see them fail**

Run: `loom job run -d "check_plan skeleton tests (red)" -- sh -c 'cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q check_plan_test.SkeletonCliTest'`
Expected: 3 failures (`--skeleton` is read as a plan path).

- [ ] **Step 7: Implement `--skeleton` in check_plan.py**

1. Docstring: after the `**Parallel:**` paragraph, add:

```text
check_plan.py --skeleton PLAN.md checks a skeleton before /flow:draft drafts it instead: its head, its
names contract's rules and its story list (plan_skeleton.py says which). It needs no profile, prints
one FAIL line a problem, and on a pass `OK: <plan>: skeleton; <n> stories; contract <m> lines`.
```

and add `       check_plan.py --skeleton PLAN.md` under the two usage lines.

2. Before `main`, add:

```python
USAGE = "usage: check_plan.py [--json PLAN.md | --skeleton PLAN.md | PLAN.md [PLAN.md ...]]"


def repo_problem(plan: Path) -> str | None:
    """The FAIL line for a plan outside the repo this session runs in, or for a repo flowconf can't place."""
    try:
        plan_main, cwd_main = flowconf.main_checkout(repo_root(plan)), flowconf.main_checkout(Path.cwd())
    except FlowError as err:
        return f"FAIL [repo] {plan}: {err}"
    if plan_main != cwd_main:
        # Every flowconf command reads the profile of the repo the session runs in, so a plan elsewhere would be checked against another profile.
        return f"FAIL [repo] {plan}: the plan is in {plan_main} but this session is in {cwd_main}; keep the plan in the repo it changes"
    return None


def skeleton_main(arg: str) -> int:
    """`--skeleton PLAN`: plan_skeleton's problems, one FAIL line each, or the OK line."""
    # Imported here: plan_skeleton imports plan_join, which imports this module.
    from plan_skeleton import lint

    plan = Path(arg)
    try:
        text = plan.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as err:
        print(f"FAIL [skeleton] {arg}: {getattr(err, 'strerror', None) or err}")
        return 1
    problem = repo_problem(plan)
    if problem:
        print(problem)
        return 1
    problems, stories, contract = lint(text, plan)
    for problem in problems:
        print(f"FAIL [skeleton] {arg}: {problem}")
    if problems:
        return 1
    print(f"OK: {arg}: skeleton; {stories} stories; contract {contract} lines")
    return 0
```

3. In `main`, at the top:

```python
    if argv and argv[0] == "--skeleton":
        if len(argv) != 2:
            print(USAGE, file=sys.stderr)
            return 2
        return skeleton_main(argv[1])
```

replace the usage `print(…)` with `print(USAGE, file=sys.stderr)`, and replace the `try: plan_repo = …` / `if plan_main != cwd_main:` blocks with:

```python
        problem = repo_problem(Path(arg))
        if problem:
            print(problem)
            failed = True
            continue
        plan_repo = repo_root(Path(arg))
```

- [ ] **Step 8: Run every flow test**

Run: `loom job run -d "flow lib tests" -- bash flow/lib/test.sh`
Expected: `OK`, and no FAIL from the agent lint.

- [ ] **Step 9: Check the lint against the real skeleton the spec measured**

Run (a read, no build; the skeleton's copy from the design session is `/tmp/claude-1000/costcalc/skel.md`):

```bash
cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -c 'import sys, plan_skeleton; from pathlib import Path; t = open(sys.argv[1]).read(); p, n, m = plan_skeleton.lint(t, Path(sys.argv[1])); print("\n".join(p)); print(len(p), "problems;", n, "stories;", m, "contract lines")' /tmp/claude-1000/costcalc/skel.md
```

Expected: about 24 rule-1 flags on about 12 lines (more where a camelCase parameter now counts), 1 rule-2 flag (the convention line for every story), and one "spec does not exist" line, since the copy sits outside its repo. Not a gate: a check that the heuristics still match spec §4.2's evidence. Record the numbers in the commit message body.

- [ ] **Step 10: Commit**

```bash
git add flow/lib/plan_skeleton.py flow/lib/plan_skeleton_test.py flow/lib/check_plan.py flow/lib/check_plan_test.py
git commit -m "feat(flow): check-plan --skeleton lints the names contract and the story list" -- flow/lib/plan_skeleton.py flow/lib/plan_skeleton_test.py flow/lib/check_plan.py flow/lib/check_plan_test.py
```

---

## Task 3: The question round in the prompts

**Files:**
- Modify: `flow/plugin/skills/draft/SKILL.md`
- Modify: `flow/plugin/agents/drafter.md`
- Modify: `flow/plugin/docs/dispatch.md` (the design-session steps, `STATE.md` and resume, `### The skeleton`, `### Drafting the stories`, `### Fixing the plan`)
- Modify: `session/loom_context_flow.md`

No unit test covers prose; the agent lint (`lib/check-agents.sh`) is the gate, and a read-through against spec §4.3 and §4.4.

- [ ] **Step 1: The draft skill**

In `flow/plugin/skills/draft/SKILL.md`:

1. Frontmatter description:

```text
description: Draft a supervised plan from its committed spec and skeleton with the flow roster, from a fresh session (/clear, then /flow:draft <plan>): drafters settle the names contract in a question round, then write the stories; it joins and checks them, one recon checks the seams, fresh drafters fix what the checks find, and it commits the plan for /flow:run.
```

2. Intro: "You write only the names contract, the plan's header," becomes "You write only the names contract, the story entries, the plan's header,". "Only your answer to a drafter's own NEEDS_CONTEXT report goes to it, and that resumes it." becomes "Only your answer to a drafter's own NEEDS_CONTEXT report, and step 2's message that ends the question round, go to a drafter, and each resumes it."

3. Step 1: after the `loom flow conf doctor` bullet, add:

```text
   - Run `loom flow check-plan --skeleton $ARGUMENTS`. On any FAIL, show the
     FAIL lines and stop: the owner fixes the skeleton in a design session,
     or marks the line `(Story N proposes)`.
```

In the resume bullet, after "Go by the job its **In flight** line names.", add:

```text
   A drafter on a questions job has reported when its
   `D/questions/S<first id>.md` exists, its hand-back aside: dispatch again,
   fresh, only a drafter without one, with step 2a's brief. When drafting
   starts in a session other than the one that dispatched the round (after a
   `/clear`), its drafters can't be resumed: dispatch each fresh with step
   2b's drafting brief.
```

4. Step 2 becomes:

```text
2. **Questions, then drafting.** One `flow:drafter` per one or two stories,
   all in one parallel batch. Give each the plan's path, the spec's path, D,
   its story ids and their files `D/S<id>.md`, and the drafter's profile
   block. Dispatch with `effort: xhigh` a drafter whose story's entry names
   one of the block's landmine markers; this sentence is the instruction the
   Agent tool needs to set a per-call effort. Every other drafter runs at its
   pinned high, a story whose shape only the block's notes describe included.
   - **2a. Questions.** The brief adds: "Question round: write only
     `D/questions/S<first id>.md`, then report." The drafter reads what its
     entries need and writes the file's four lists (its definition gives
     them); its hand-back is the path and each list's count. A questions file
     that isn't the four lists goes to a fresh drafter with the same brief;
     don't guess its content. A drafter that drafts in the round keeps its
     `D/S<id>.md`: its 2b message adds "finish `D/S<id>.md` against the
     settled contract".
   - **The wait.** Wait until every drafter of the batch has reported, and
     settle everything in one pass: read the questions files; write each
     decision into the names contract, choosing where a user's proposal and a
     definer's differ; amend the entries; take to the owner only what the
     spec doesn't settle (see the intro); then run
     `loom flow check-plan --skeleton $ARGUMENTS` until it passes. Record in
     each story's row of `STATE.md` what was settled for it.
   - **2b. Drafting.** Resume every drafter of the batch with SendMessage to
     the id on its report: "The contract is settled. Re-read `## Names
     contract` and your entries, and draft.", then the contract lines that
     changed among those its questions file raised or its stories own.
   - After the round, a drafter that finds a gap in the contract stops and
     reports NEEDS_CONTEXT. Amend the names contract (in the skeleton, above
     `## Stories`), run the skeleton check again, then answer it with
     SendMessage to the id on its report.
   - When an amendment changes a name that other drafts use, wait until their
     drafters have reported, then give each affected draft a fix job whose
     findings are the amendment. An amended entry needs no fix job: the join
     writes each story's opening lines from its entry.
```

5. Step 3: the join bullet's "which writes the plan from the skeleton's head and `D/S*.md` (never `STATE.md`)" becomes "which writes the plan from the skeleton's head, each story's opening lines from its entry, and `D/S*.md` (never `STATE.md`)". "A join or `loom flow check-plan` failure goes to a fresh drafter as a fix job, its findings the failure text." becomes "A join or `loom flow check-plan` failure goes to a fresh drafter as a fix job, its findings the failure text, unless it names an entry: fix that in the skeleton." After "with `start` the story's `start` in `D/join.json`.", add: "A finding on a line inside a story's `entry` in `D/join.json` is the entry's: amend the entry, never a draft." "Change the story's entry under `## Stories`, and have the draft's fix job copy the new line." becomes "Change the story's entry under `## Stories`; the next join writes it into the story."

6. Step 5: after the "Write each cross-story decision" bullet, add:

```text
   - A finding that names a story's entry (the seams packet cites one as
     `the Story <id> entry`) is yours: amend the entry, and join again.
```

7. The quiet-drafter paragraph: "then dispatch a fresh drafter with step 2's drafting brief plus" becomes "then dispatch a fresh drafter with the brief of the job it was on: step 2a's in the question round, else step 2's drafting brief plus".

8. `STATE.md`'s template: the Phase line's list becomes `<questions | drafting | checking | seams | fixing | committed>`, and the In flight line becomes `- <agent id>: <questions | drafting | fix> <story ids>`.

- [ ] **Step 2: The drafter**

In `flow/plugin/agents/drafter.md`:

1. Description:

```text
description: Writes, or fixes, stories of a supervised plan as plan text in its drafts directory, from the spec and the skeleton's names contract, after a question round that settles the contract's cross-story names; a name the contract still lacks comes back as a question. Never edits tracked files, stages, commits or builds. Dispatch it unnamed, never as a fork, and each fix job to a fresh one, at high. Sonnet at high effort by the owner's instruction (2026-10-14).
```

2. Under "## What the dispatch gives you", before the drafting-job bullet, add:

```text
- **A question round:** the drafting job's brief plus "Question round:
  write only `D/questions/S<first id>.md`, then report". Read what your
  entries need, as for drafting, and grep the tree for each name your
  stories share, then write the file, four lists under these headings, each
  `none` when empty:

      ## Names used
      - `<name>` from Story <id>: <its exact form>; <why the contract's form can't work, or "not given">
      ## Names defined
      - `<its exact form>` (Story <id>), used by Story <id>: <where the use shows>
      ## Proposed
      - `<the contract line's name>` (Story <id> proposes): `<its exact form>`
      ## Entry lines
      - Story <id> **<line>:**: <the line it needs>; <why>

  Names used are those your stories take from another story that the
  contract doesn't give exactly or that can't work against the code (a clash
  with a name the tree already has included). Names defined are those your
  stories define that another story uses (another entry's **Interfaces**
  line or paragraph names it, or the spec says another story reads it) and
  that the contract doesn't give exactly. Proposed are the contract lines
  marked `(Story <one of your ids> proposes)`. Entry lines are the lines of
  your entries that can't hold: a file the story must also touch, a
  **Developer** that doesn't fit, a **Verify** that misses its tests. Write
  no draft. Your hand-back is at most 5 lines: the file's path and each
  list's count. The manager settles every file at once, then resumes you
  with "The contract is settled" and the lines that changed: re-read
  `## Names contract` and your entries, and draft.
```

3. In the drafting-job bullet, replace from "Copy each story's opening lines" through "don't change it." with:

```text
  A draft holds no opening lines (**Developer**, **Files**,
  **Interfaces**, **Verify**, **Review**): the join writes them from the
  story's entry under `## Stories`, which the design session chose. The
  heading is `## Story <id>: <title>` with the entry's id; the join matches
  drafts to entries by it. A line that can't hold (a file the story must
  also touch, a **Developer** that doesn't fit its code) goes in your
  report; don't change it.
```

4. In the fix-job bullet, "a contract amendment, an entry line to copy again, or the path" becomes "a contract amendment, or the path".

5. "## The story format": the template drops its five opening lines, so it reads:

```text
    ## Story <id>: <title>

    <one paragraph: what the story does and why>

    ### Task <id>.1: <name>
```

followed by the unchanged task lines, and the first bullet below it becomes:

```text
- The entry's **Developer** writes the story. It is `flow:junior-dev` when
  every task's code is complete and lands in one or two files with no wiring
  between them, otherwise `flow:intermediate-dev` (never `flow:senior-dev`:
  it is the run's last resort, not a plan's choice). If your code doesn't
  fit the entry's choice, say so in your report.
```

6. "## Report": "At most 15 lines:" becomes "At most 15 lines (a question round's, at most 5: above):".

- [ ] **Step 3: The dispatch guide**

In `flow/plugin/docs/dispatch.md`:

1. Design-session step 2 ends "…never generated from a prototype of the code." Add: "It runs `loom flow check-plan --skeleton <plan>` until it passes: the names contract's rules and the story list ([the skeleton](#the-skeleton))."

2. The `/flow:draft` paragraph: "It dispatches the `flow:drafter`s ([drafting the stories](#drafting-the-stories))," becomes "It checks the skeleton, settles the names contract with the `flow:drafter`s in a question round and has them draft ([drafting the stories](#drafting-the-stories)),".

3. The `STATE.md` paragraph: "(drafting, checking, seams, fixing or committed)" becomes "(questions, drafting, checking, seams, fixing or committed)"; after "only an agent with no hand-back is dispatched again.", add: "A drafter of the question round has reported when its questions file exists, and only one without a file is dispatched again; drafting that starts after a `/clear` dispatches its drafters fresh."

4. `### The skeleton`: after the names-contract bullet's text "…only `/flow:draft`'s manager edits it;", the bullet continues: "it sits under the exact heading `## Names contract`, above `## Stories`;". After the bullet list, add:

```text
The contract's rules are what lets every drafter write against it at once:

1. **Every shared name is written in full.** A function or method gives
   every parameter's name and type and its return type. A type lists its
   fields with their types, inline (`{ a: T, b: U }`, or a tuple's
   `(T, U)`) or in bullets nested under it, or says `(opaque)` when no other
   story reads its fields. No `..`, `...` or `…` stands for parameters or
   fields.
2. **Every line that defines a name names its owning story:** `Story <id>`
   on the line, on a bullet it is nested under, or on the unindented line
   that introduces its list (`**crate-a** (Story 1)`).
3. **A line may hand its exact form on:** `(Story N proposes)` waives rule 1
   on that line, and the question round asks Story N's drafter for the form.

`loom flow check-plan --skeleton <plan>` checks them, with the head (the
`**Spec:**` line, the headings, the marker) and each entry's lines, and prints
one FAIL line a problem. Its rules are heuristics over backticked spans: a
contract written as an expression draws a false flag, and the cure is to type
it.
```

In the entry paragraph, "which its drafter copies into the story verbatim, then a **Reading** line, which it doesn't copy:" becomes "which the join writes into the story (a draft holds none of them), then a **Reading** line, which only the drafter reads:".

5. `### Drafting the stories`: replace the paragraph that begins "A drafter's brief holds" from "A drafter that needs a name another story shares" to its end with:

```text
The batch drafts in two phases. In the question round, each drafter reads what
its entries need and writes only `D/questions/S<first id>.md`: the names its
stories use from others and those they define for others that the contract
doesn't give exactly (each with its proposed form, a clash with the tree's
names included), its `(Story N proposes)` lines with their forms, and the
entry lines that can't hold. The manager waits for every drafter, settles
everything in one pass (contract, entries, the owner's answers in the spec),
runs the skeleton check, then resumes every drafter with the lines that
changed. Every drafter waits, one that asked nothing included: a name it
defines may change in the settlement. A gap that shows only while drafting
takes the old path: the drafter stops and reports NEEDS_CONTEXT, the manager
amends the contract and answers, and once the drafters whose stories use an
amended name have reported, a fresh drafter per affected story fixes its
draft. An amended entry costs nothing: the join writes it. The manager never
sends a running drafter new work by message: a parent's message arrives framed
as another agent's request.
```

In the join paragraph, "regenerates everything after the skeleton's marker line from the drafts, in story-list order, and writes `D/join.json`: each story's first plan line and length." becomes "regenerates everything after the skeleton's marker line, in story-list order: each story is its draft's heading, its entry's opening lines, and the rest of its draft (a block of opening lines a draft copied is dropped, with a note). It writes `D/join.json`: each story's `start`, `lines`, and `entry`, the plan lines of its opening lines." After "draft line = plan line − the story's `start` in `join.json` + 1.", add: "A finding inside a story's `entry` lines is the entry's, which the manager amends."

6. `### Fixing the plan`, step 2: after "A finding that states its fix goes to the drafter as it stands.", add: "A finding on a story's opening lines goes to its entry, which the manager amends; no fixer copies it."

- [ ] **Step 4: Loom's flow context**

In `session/loom_context_flow.md`, "writes only the skeleton, in the format of the skeleton section of flow's dispatch guide, and commits both by pathspec" becomes "writes only the skeleton, in the format of the skeleton section of flow's dispatch guide, runs `loom flow check-plan --skeleton <plan>` until it passes, and commits both by pathspec".

- [ ] **Step 5: Run the gates**

Run: `loom job run -d "flow lib tests and agent lint" -- bash flow/lib/test.sh`
Expected: `OK`, no FAIL.
Run: `loom job run -d "session and flow Go tests" -- sh -c 'CGO_ENABLED=0 go test ./session/ ./flow/'`
Expected: `ok` for both packages.

- [ ] **Step 6: Read through against the spec**

Read the edited step 2 of the skill, the drafter's question-round bullet and the dispatch guide's drafting section against spec §4.3 (four lists, the hand-back's 5 lines, the wait, 2b's message, the resume rules, the `questions` phase) and §4.4 (no opening lines in drafts, entry findings to the manager). Fix any gap before the commit.

- [ ] **Step 7: Commit**

```bash
git add flow/plugin/skills/draft/SKILL.md flow/plugin/agents/drafter.md flow/plugin/docs/dispatch.md session/loom_context_flow.md
git commit -m "feat(flow): /flow:draft settles the names contract in a question round before drafting" -- flow/plugin/skills/draft/SKILL.md flow/plugin/agents/drafter.md flow/plugin/docs/dispatch.md session/loom_context_flow.md
```

---

## Task 4: README, provenance, and the full gates

**Files:**
- Modify: `flow/README.md` (the Tools table)
- Modify: `flow/PROVENANCE.md` (`## drafter`)

- [ ] **Step 1: README**

In `flow/README.md`'s Tools table, the `check-plan` row becomes `| \`check-plan\` | \`check_plan.py\`; \`--skeleton PLAN\` checks a skeleton's names contract and story list (\`plan_skeleton.py\`) |`, and the `join` row becomes `| \`join\` | \`plan_join.py\`: the stories from the drafts, each story's opening lines from its skeleton entry |`.

- [ ] **Step 2: Provenance**

Under `## drafter` in `flow/PROVENANCE.md`, add first:

```text
- 2026-11-11, a question round before drafting and no copied opening lines (`agents/drafter.md`, the draft skill's step 2, `docs/dispatch.md`); no pin changed. On the owner's instruction (`docs/superpowers/specs/2026-11-11-flow-names-contract-design.md`), from an 18-story `/flow:draft` run the same day: 6 of its 11 drafters stopped with NEEDS_CONTEXT, the 5 that didn't included the definers of what the askers asked about and drafted against the old contract, about 10 of the 11 drafts needed fix jobs, and 8 fix-job rows only copied an amended entry line. Each drafter now reports its names in `D/questions/` and waits for the manager's one settlement, and the join writes each story's opening lines from its entry.
```

- [ ] **Step 3: The full gates**

Run: `loom job run -d "flow lib tests and agent lint" -- bash flow/lib/test.sh`
Run: `loom job run -d "go test" -- sh -c 'CGO_ENABLED=0 go test ./...'`
Run: `loom job run -d "CLAUDE.md lint" -- go run ./tools/claudemd`
Expected: all pass. `go test ./...` includes `TestRepo_ClaudeMDStructure` over the README's links.

- [ ] **Step 4: Commit**

```bash
git add flow/README.md flow/PROVENANCE.md
git commit -m "docs(flow): the skeleton lint and the question round in the README and provenance" -- flow/README.md flow/PROVENANCE.md
```

---

## Self-review

- Spec §4.1–4.2 → Task 2 (`plan_skeleton.py`, `--skeleton`); §4.3 → Task 3 (skill, drafter, guide); §4.4 → Task 1 (join, map, cite) and Task 3 (no copies, entry findings); §5 → Task 3's skill text (unparsable file, drafting in the round, quiet drafter) and Task 1 (the dropped-copies note); §6 → every row has a task; §7 → Tasks 1, 2 and 3's gates. §7's comparison with the next real run is the owner's, after merge.
- Names: `Entry`, `Span`, `entries`, `story_order`, `COPIED_NOTE`, `load_map`, `cite`, `checked_map`, `lint`, `span_problems`, `owner_lines`, `contract_problems`, `entry_problems`, `repo_problem`, `skeleton_main`, `USAGE` are used with one spelling throughout.
- Task 2 step 9 is a check against the evidence, not a gate; its numbers go in the commit body.

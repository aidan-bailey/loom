# Flow fix hygiene Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Catch the defects `/flow:draft`'s own fixes introduce: a seams re-check of the shared names step 5's fixes changed, one wave of fix jobs for what it finds, and rules for fixers, the re-check and the manager's findings.

**Architecture:** `plan_packet.py` gains `--changed`, which diffs each draft against its plan-review snapshot `D/reviewing/S<id>.md`, attributes each changed code line to the nearest definition above it, and lists the names one story defines and another uses where a changed line belongs to the definition or uses it. The draft skill's step 5 runs it after the fixers, sends a scoped recon when it lists any, and gives the recon's findings to one wave of fresh drafters. The rest is rule text in the drafter, the two plan reviewers, the draft skill and the dispatch guide.

**Tech Stack:** Python 3 (stdlib `difflib`, `unittest`), Markdown agent and skill files, `bash flow/lib/test.sh`.

**Spec:** `docs/superpowers/specs/2026-11-13-flow-fix-hygiene-design.md`

**Rules for whoever executes this** (from `CLAUDE.md` and `flow/CLAUDE.md`):
- Stage and commit by path only (`git add <paths>`, `git commit -- <paths>`), never `-a`, `-A` or `.`.
- Scratch files go under `/tmp`, never in the worktree.
- Heavy work goes through `loom job run`/`submit`; the flow suite is `bash flow/lib/test.sh` (unit tests plus the agent lint).
- No agent pin (model, effort, maxTurns, tools, cacheTtl) changes; the PROVENANCE entries say "no pin changed".
- Keep client repos' names out of `flow/`.

---

### Task 1: `loom flow packet --seams --changed`

**Files:**
- Modify: `flow/lib/plan_packet.py` (module docstring; imports; new constants, `PacketError`, `changed_draft_lines`, `line_ranges`, `draft_ranges`, `owners`, `changed_uses`, `changed_packet`; `main`)
- Test: `flow/lib/plan_packet_test.py` (new `ChangedTest` class after `PremisesSectionTest`; three tests in `CliTest`)

- [ ] **Step 1: Write the failing tests**

Add after `class PremisesSectionTest` (before `class CliTest`) in `flow/lib/plan_packet_test.py`:

```python
class ChangedTest(RepoCase):
    """The --changed packet: each draft against its plan-review snapshot."""

    def changed(self, edits=None, snapshots=("1", "2")):
        """The --changed packet and its row count once the drafts take `edits` ({id: [(old, new)]}).

        The fixture drafts are the review snapshots; the edited drafts are joined into a plan named for the test.
        """
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        drafts = Path(tmp.name) / "drafts"
        (drafts / "reviewing").mkdir(parents=True)
        self.bodies = {}
        for sid, body in {"1": DRAFT_1, "2": DRAFT_2}.items():
            if sid in snapshots:
                (drafts / "reviewing" / f"S{sid}.md").write_text(body, encoding="utf-8")
            for old, new in (edits or {}).get(sid, []):
                self.assertIn(old, body)
                body = body.replace(old, new, 1)
            (drafts / f"S{sid}.md").write_text(body, encoding="utf-8")
            self.bodies[sid] = body
        plan = self.root / "docs" / "plans" / f"{self._testMethodName}.md"
        plan.write_text(SKELETON, encoding="utf-8")
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(plan_join.main([str(plan), "--drafts", str(drafts)]), 0)
        return plan_packet.changed_packet(plan, "HEAD", drafts)

    def at(self, sid, wanted):
        """The line of `wanted` in Story `sid`'s edited draft."""
        return line_of(self.bodies[sid], wanted)

    def test_a_body_edit_in_a_providers_definition_lists_the_stories_that_use_it(self):
        text, count = self.changed({"1": [("    private_helper(id)\n", "    private_helper(id).filter(|p| *p > 0)\n")]})
        k = self.at("1", "pub fn lookup_price(id: u32) -> Option<u32> {")
        j = self.at("2", "lookup_price(7)")
        c = self.at("1", "private_helper(id).filter(|p| *p > 0)")
        self.assertEqual(seam_rows(text), [f"lookup_price | defined S1.md:{k} | used S2.md:{j} | changed in S1.md:{c}"])
        self.assertEqual(count, 1)

    def test_a_changed_line_that_uses_another_storys_name_is_listed(self):
        text, count = self.changed({"2": [("lookup_price(7)", "lookup_price(8)")]})
        k = self.at("1", "pub fn lookup_price(id: u32) -> Option<u32> {")
        j = self.at("2", "lookup_price(8)")
        self.assertEqual(seam_rows(text), [f"lookup_price | defined S1.md:{k} | used S2.md:{j} | changed in S2.md:{j}"])
        self.assertEqual(count, 1)

    def test_a_use_in_a_test_file_counts(self):
        tail = "grep -r private_helper src/\n~~~\n"
        test_block = (
            "\nAdd to `tests/screen.rs`:\n\n"
            "~~~rust\n#[test]\nfn screen_prices() {\n    assert_eq!(lookup_price(9), Some(9));\n}\n~~~\n"
        )
        text, count = self.changed({"2": [(tail, tail + test_block)]})
        k = self.at("1", "pub fn lookup_price(id: u32) -> Option<u32> {")
        a = self.at("2", "assert_eq!(lookup_price(9), Some(9));")
        # `screen_prices` is defined in a test file, so it is no provider; the line inside it uses Story 1's name.
        self.assertEqual(seam_rows(text), [f"lookup_price | defined S1.md:{k} | used S2.md:{a} | changed in S2.md:{a}"])
        self.assertEqual(count, 1)

    def test_a_deletion_inside_a_definition_is_attributed_to_it(self):
        text, count = self.changed({"1": [("    private_helper(id)\n", "")]})
        k = self.at("1", "pub fn lookup_price(id: u32) -> Option<u32> {")
        j = self.at("2", "lookup_price(7)")
        # The deleted line's place is marked on the line above it, the definition's own.
        self.assertEqual(seam_rows(text), [f"lookup_price | defined S1.md:{k} | used S2.md:{j} | changed in S1.md:{k}"])
        self.assertEqual(count, 1)

    def test_a_change_that_touches_no_shared_name_lists_none(self):
        # `private_helper` is named in Story 2 only after a dot and in a command: no use.
        text, count = self.changed({"1": [("    Some(id)\n", "    Some(id + 1)\n")]})
        self.assertEqual((seam_rows(text), count), ([], 0))
        self.assertIn("## Shared names a fix changed\n\n(none)", text)
        self.assertIn(f"- Story 1: S1.md:{self.at('1', 'Some(id + 1)')}", text)
        self.assertNotIn("- Story 2:", text)

    def test_unchanged_drafts_list_none(self):
        text, count = self.changed()
        self.assertEqual((seam_rows(text), count), ([], 0))
        self.assertIn("(none: every draft matches its review snapshot)", text)
        self.assertIn(plan_packet.CHANGED_RULE, text)
        self.assertIn(plan_packet.SEAMS_READING_RULES, text)
        lines = text.splitlines()
        self.assertEqual(lines[1], f"Packet length: {len(lines)} lines")

    def test_a_story_without_a_review_snapshot_is_refused(self):
        with self.assertRaises(plan_packet.PacketError) as caught:
            self.changed(snapshots=("1",))
        message = str(caught.exception)
        self.assertTrue(message.startswith("Story 2 has no review snapshot "), message)
        self.assertTrue(message.endswith(str(Path("reviewing") / "S2.md")), message)

    def test_a_plan_without_a_join_map_is_refused(self):
        with self.assertRaises(plan_packet.PacketError) as caught:
            plan_packet.changed_packet(self.plan, "HEAD", self.root / "no-drafts")
        self.assertIn("holds no join map", str(caught.exception))
```

Add to `class CliTest`, after `test_seams_writes_the_packet_into_the_plans_drafts_directory`:

```python
    def snapshot_drafts(self):
        """A copy of the drafts whose review snapshots are the drafts themselves."""
        drafts = self.copy_drafts()
        (drafts / "reviewing").mkdir()
        for sid in ("1", "2"):
            shutil.copy(drafts / f"S{sid}.md", drafts / "reviewing" / f"S{sid}.md")
        return drafts

    def test_changed_writes_its_own_packet_and_counts_the_rows(self):
        drafts = self.snapshot_drafts()
        code, out, err = self.run_cli("--seams", "--changed", "--drafts", str(drafts))
        self.assertEqual(code, 0, out + err)
        packet = drafts / "packets" / "seams-changed.md"
        text = packet.read_text(encoding="utf-8")
        self.assertEqual(out.strip(), f"wrote {packet}: {len(text.splitlines())} lines, 0 shared names changed")
        self.assertFalse((drafts / "packets" / "seams.md").exists())

    def test_changed_refuses_a_story_without_a_snapshot(self):
        drafts = self.copy_drafts()
        code, out, err = self.run_cli("--seams", "--changed", "--drafts", str(drafts))
        self.assertEqual(code, 1, out + err)
        self.assertEqual(out, f"FAIL [packet] {self.plan}: Story 1 has no review snapshot {drafts / 'reviewing' / 'S1.md'}\n")

    def test_changed_refuses_a_plan_out_of_date_with_its_drafts(self):
        drafts = self.snapshot_drafts()
        with (drafts / "S2.md").open("a", encoding="utf-8") as handle:
            handle.write("\nA line added after the join.\n")
        code, out, err = self.run_cli("--seams", "--changed", "--drafts", str(drafts))
        self.assertEqual(code, 1, out + err)
        self.assertEqual(out, f"FAIL [packet] {self.plan}: the plan is out of date with its drafts; re-run loom flow join\n")
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `cd flow/lib && python3 -m unittest -q plan_packet_test.ChangedTest plan_packet_test.CliTest`
Expected: errors `AttributeError: module 'plan_packet' has no attribute 'changed_packet'` (and `PacketError`), and the three CLI tests fail with exit 2 (`unrecognized arguments: --changed`).

- [ ] **Step 3: Implement `--changed`**

In `flow/lib/plan_packet.py`:

Replace the docstring's usage paragraph:

```python
Usage: plan_packet.py PLAN --seams [--changed] [--rev REV] [--drafts DIR]

REV defaults to HEAD, DIR to the plan's drafts directory. The packet is written
to DIR/packets/seams.md. When DIR holds a join map, the plan must be the join of
its drafts. Exit 0 when the packet is written, 1 when the plan is refused or the
packet can't be written, 2 on a usage error.

With --changed, the packet is the seams re-check's, DIR/packets/seams-changed.md:
only the names one story defines and another uses where a change since the plan
review (each draft against DIR/reviewing/S<id>.md) touched the definition or a
line that uses it, then each story's changed lines. It needs the join map and
every story's review snapshot, and prints how many names it lists.
"""
```

Add `import difflib` to the imports (alphabetical, before `import re`).

After `PLACEHOLDER = "@@LENGTH@@"`, add:

```python
REVIEWING_DIR = "reviewing"
CHANGED_RULE = (
    "Each row is a name one story defines and another uses, where a fix since the plan review changed the "
    "definition (a provider changed) or a line that uses it (a consumer changed): check only these rows, "
    "that the contract and the behaviour across each still match, reading the changed lines first."
)


class PacketError(Exception):
    """A packet refused for what the drafts directory lacks."""
```

After `unchecked_premises`, add:

```python
def changed_draft_lines(before: list[str], after: list[str]) -> set[int]:
    """The 1-based lines of `after` a diff from `before` changed: each replaced or inserted line, and for a
    pure deletion the line above its place (the first line when it is at the top)."""
    changed: set[int] = set()
    for tag, _, _, j1, j2 in difflib.SequenceMatcher(None, before, after, autojunk=False).get_opcodes():
        if tag in ("replace", "insert"):
            changed.update(range(j1 + 1, j2 + 1))
        elif tag == "delete":
            changed.add(max(j1, 1))
    return changed


def line_ranges(lines: set[int]) -> list[tuple[int, int]]:
    """The runs of consecutive numbers in `lines`, as (first, last) pairs in order."""
    runs: list[tuple[int, int]] = []
    for n in sorted(lines):
        if runs and n == runs[-1][1] + 1:
            runs[-1] = (runs[-1][0], n)
        else:
            runs.append((n, n))
    return runs


def draft_ranges(sid: str, lines: set[int]) -> str:
    """`S<id>.md:<a>-<b>` for each run of draft lines, comma-separated."""
    return ", ".join(f"S{sid}.md:{a}" if a == b else f"S{sid}.md:{a}-{b}" for a, b in line_ranges(lines))


def owners(
    blocks: list[Fenced], found: dict[str, tuple[int, list[str], list[str]]], lines: set[int]
) -> dict[str, set[int]]:
    """The definitions the plan lines in `lines` belong to, each with its lines.

    A line in a code block belongs to the nearest definition at or above it in that block, so an edit to a
    body counts as well as one to a signature; a line above the block's first definition belongs to none.
    """
    starts = sorted((at, name) for name, (at, _, _) in found.items())
    owned: dict[str, set[int]] = {}
    for block in blocks:
        if block.lang in NOT_CODE_LANGS or not block.lines:
            continue
        first, last = block.lines[0][0], block.lines[-1][0]
        inside = [(at, name) for at, name in starts if first <= at <= last]
        for n in sorted(lines):
            above = [name for at, name in inside if at <= n]
            if first <= n <= last and above:
                owned.setdefault(above[-1], set()).add(n)
    return owned


def changed_uses(blocks: list[Fenced], lines: set[int]) -> dict[str, set[int]]:
    """Every whole word on a code line in `lines`, with the lines holding it."""
    found: dict[str, set[int]] = {}
    for block in blocks:
        if block.lang in NOT_CODE_LANGS:
            continue
        for n, line in block.lines:
            if n in lines:
                for token in TOKEN_RE.findall(line):
                    found.setdefault(token, set()).add(n)
    return found


def changed_packet(plan: Path, rev: str, drafts: Path) -> tuple[str, int]:
    """The seams re-check's packet, and how many rows it lists.

    Each story's draft is diffed against its plan-review snapshot. A row is a name one story defines outside a
    test file and another uses, where a changed line belongs to its definition (the provider changed) or uses
    it (a consumer changed); its changed lines are cited as draft lines.
    """
    text = plan.read_text(encoding="utf-8")
    story_map = checked_map(text, drafts)
    if not story_map:
        raise PacketError(f"{drafts} holds no join map; run loom flow join first")
    tree = Tree(repo_root(plan), rev)
    parsed = gather(text, tree.ignored_root)
    order = [story for story, _ in parsed.stories]
    regions = Regions(parsed, tree)
    regions.walk()
    defs = {story: definitions(parsed, story, regions.blocks) for story in order}
    own = {story: definitions(parsed, story, regions.blocks, tests=True) for story in order}
    used = {story: identifiers(parsed, story) for story in order}
    changed: dict[str, set[int]] = {}
    for story in order:
        snapshot = drafts / REVIEWING_DIR / f"S{story}.md"
        if not snapshot.is_file():
            raise PacketError(f"Story {story} has no review snapshot {snapshot}")
        before = snapshot.read_text(encoding="utf-8").splitlines()
        after = (drafts / f"S{story}.md").read_text(encoding="utf-8").splitlines()
        # Draft line 1 is the heading; line k after it is plan line start + k - 1.
        lines = {k for k in changed_draft_lines(before, after) if k > 1}
        if lines:
            changed[story] = lines
    rows: list[str] = []
    for story, draft_lines in changed.items():
        start = story_map[story].start
        plan_lines = {start + k - 1 for k in draft_lines}
        blocks = code_blocks(parsed, story)
        for name, at in sorted(owners(blocks, own[story], plan_lines).items()):
            users = [other for other in order if other != story and name in used[other] and name not in own[other]]
            if name in defs[story] and users:
                defined = cite(defs[story][name][0], story_map)
                uses = ", ".join(cite(used[other][name], story_map) for other in users)
                rows.append(f"{name} | defined {defined} | used {uses} | changed in {draft_ranges(story, {n - start + 1 for n in at})}")
        for name, at in sorted(changed_uses(blocks, plan_lines).items()):
            definers = [other for other in order if other != story and name in defs[other]]
            if definers and name not in own[story]:
                defined = ", ".join(cite(defs[other][name][0], story_map) for other in definers)
                rows.append(
                    f"{name} | defined {defined} | used {cite(min(at), story_map)} | "
                    f"changed in {draft_ranges(story, {n - start + 1 for n in at})}"
                )
    out = [
        f"# Seams re-check packet for {plan.name}",
        PLACEHOLDER,
        "",
        f"Written against the tree at {rev}. A change is a draft's difference from its review snapshot "
        f"({REVIEWING_DIR}/S<id>.md).",
        "",
        SEAMS_READING_RULES,
        "",
        CHANGED_RULE,
        "",
        "## Shared names a fix changed",
        "",
    ]
    out += fenced(rows, "text") if rows else ["(none)"]
    out += ["", "## Changed spans", ""]
    if changed:
        out += [f"- Story {story}: {draft_ranges(story, lines)}" for story, lines in changed.items()]
    else:
        out.append("(none: every draft matches its review snapshot)")
    return finish(out), len(rows)
```

In `main`, add the flag after `--seams`:

```python
    parser.add_argument(
        "--changed", action="store_true",
        help="only the shared names a fix changed since the plan review (DIR/reviewing/S<id>.md)",
    )
```

Replace the block from `try:\n        body = seams_packet(...)` through the final `print(...)` with:

```python
    count: int | None = None
    try:
        if args.changed:
            body, count = changed_packet(args.plan, sha, drafts)
        else:
            body = seams_packet(args.plan, sha, drafts)
    except subprocess.CalledProcessError as err:
        print(f"plan_packet: git failed in {root}: {err.stderr.strip()}", file=sys.stderr)
        return 2
    except (JoinError, PacketError) as err:
        print(f"FAIL [packet] {args.plan}: {err}")
        return 1
    except (OSError, UnicodeDecodeError) as err:
        print(f"FAIL [packet] {args.plan}: {describe(err)}")
        return 1
    target = drafts / "packets" / ("seams-changed.md" if args.changed else "seams.md")
    try:
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(body, encoding="utf-8")
    except OSError as err:
        print(f"FAIL [packet] {args.plan}: {target}: {describe(err)}")
        return 1
    counted = "" if count is None else f", {count} shared names changed"
    print(f"wrote {target}: {len(body.splitlines())} lines{counted}")
    return 0
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `cd flow/lib && python3 -m unittest -q plan_packet_test`
Expected: `OK`.

- [ ] **Step 5: Commit**

```bash
git add flow/lib/plan_packet.py flow/lib/plan_packet_test.py
git commit -m "feat(flow): loom flow packet --changed lists the shared names a fix changed" -- flow/lib/plan_packet.py flow/lib/plan_packet_test.py
```

---

### Task 2: The rules

**Files:**
- Modify: `flow/plugin/skills/draft/SKILL.md` (intro; step 1's resume rule; step 2's amendment rule; step 5; step 6's hand-over; `STATE.md`)
- Modify: `flow/plugin/agents/drafter.md` (the fix-job rule under "Before you report")
- Modify: `flow/plugin/agents/plan-reviewer.md`, `flow/plugin/agents/risk-reviewer.md` (the re-check)
- Modify: `flow/plugin/docs/dispatch.md` (the plan review's last sentence; Fixing the plan, items 1 to 4)

- [ ] **Step 1: The draft skill's intro gains the finding rules**

Replace:

```
before it starts, and step 5's re-check or the run's full review checks the story again. Steps 2,
3 and 5 name only the findings. Never
```

with:

```
before it starts, and step 5's re-check or the run's full review checks the story again. Steps 2,
3 and 5 name only the findings. A finding you write states the defect and the
result it needs, never the lines to edit or a "nothing else" fence: "edit only
lines 137-141" once put a bullet under the wrong heading. A defect with two
sides names both (both directions of a match, both stories of a duplicate),
and a decision between two stories' versions names what each keeps and what
each drops, in both stories' findings in the same round. Never
```

- [ ] **Step 2: Step 1's resume rule names the wave**

Replace:

```
     `D/S<id>.md` without redoing what is there". A drafter on a fix job (step
     2's amendment fixes, step 3's fix jobs, step 5's fixers) is dispatched
     again, fresh, with its fix-job brief as the intro gives it, with that
     step's findings (for step 5, `D/findings/S<id>.md` and the story's
     `start` from `D/join.json`), plus "some of these may already be fixed in
```

with:

```
     `D/S<id>.md` without redoing what is there". A drafter on a fix job (step
     2's amendment fixes, step 3's fix jobs, step 5's fixers and its wave) is
     dispatched again, fresh, with its fix-job brief as the intro gives it,
     with that step's findings (for step 5, `D/findings/S<id>.md` or
     `D/findings/wave-S<id>.md`, "wave job" for the second, and the story's
     `start` from `D/join.json`), plus "some of these may already be fixed in
```

- [ ] **Step 3: Step 2's amendment rule widens**

Replace:

```
   - When an amendment changes a name that other drafts use, wait until their
     drafters have reported, then give each affected draft a fix job whose
     findings are the amendment.
```

with:

```
   - When an amendment changes a name, or what a named thing does, that other
     drafts use, wait until their drafters have reported, then give each
     affected draft a fix job whose findings are the amendment.
```

- [ ] **Step 4: Step 5 gains the seams re-check, the wave and the settling**

Replace:

```
   - Then run step 3 again, and step 4's seams recon too when a fix changed
     the contract; the plan review doesn't run again.
```

with:

```
   - Then run step 3 again; the plan review doesn't run again.
   - Settle each finding a fixer left unapplied with its reason, and each
     clash a fixer reports: close it with your reason written in its findings
     file, give it to the wave (below), or take it to the owner. Leave none
     unanswered.
   - **The seams re-check.** When a fix changed the contract, run step 4's
     seams packet and recon again, its report to `D/findings/seams-rerun.md`.
     Otherwise run `loom flow packet $ARGUMENTS --seams --changed --rev <REV>`:
     it writes `D/packets/seams-changed.md`, the names one story defines and
     another uses where a fix since the plan review changed the definition or
     a line that uses it, and prints how many (`<k> shared names changed`).
     When k is above 0, dispatch one `flow:recon` with the packet's path, the
     plan's path and two questions: do the listed pairs' contracts still
     match, and does the behaviour across them (step 4's second question)?
     Write its report to `D/findings/seams-changed.md`.
   - **The wave.** For each story the re-check's findings, or a settled
     finding, name, write `D/findings/wave-S<id>.md` and dispatch one fresh
     `flow:drafter` with the fix-job brief, that file, the story's `start` and
     "wave job". Then run step 3 once more. The wave runs once: what a wave
     fixer reports unapplied goes to the owner.
```

Then replace:

```
   - A finding still open after its fix round goes to the owner: there is no
     second automatic round.
```

with:

```
   - A finding still open after its fix round and the wave goes to the owner:
     there is no further automatic round.
```

- [ ] **Step 5: Step 6's hand-over and `STATE.md`**

Replace:

```
   - Set the **Phase** to committed. Tell the owner what was drafted, what
     the checks, the seams recon and the plan review found, which stories the
```

with:

```
   - Set the **Phase** to committed. Tell the owner what was drafted, what
     the checks, the seams recon and the plan review found, the seams
     re-check's line (`Seams re-check: <k> shared names changed, <n>
     findings, <m> wave jobs`, with `full recon` in place of the count when a
     fix changed the contract), which stories the
```

Replace:

```
    - <D/findings/premises.md, seams.md, review-S<id>.md or S<id>.md: which agent (or the manager), and when>
```

with:

```
    - <D/findings/premises.md, seams.md, review-S<id>.md, S<id>.md, seams-changed.md, seams-rerun.md or wave-S<id>.md: which agent (or the manager), and when>
```

Replace:

```
    - <agent id>: <questions | drafting | waiting on Stories <ids> | review | risk review | re-check | fix> <story ids>
```

with:

```
    - <agent id>: <questions | drafting | waiting on Stories <ids> | review | risk review | re-check | fix | seams re-check | wave fix> <story ids>
```

- [ ] **Step 6: The drafter's fix-job rule**

In `flow/plugin/agents/drafter.md`, replace:

```
On a fix job, fix each finding you were given and nothing else, and leave
unchanged every text the contract names as quoted by another story. A finding
you think is wrong goes in your report with your reason, unapplied.
```

with:

```
On a fix job, fix each finding you were given, and what your fixes make false
in your own draft: before you report, grep the draft for each name whose code
or behaviour you changed, and bring its uses along (a test's expected value, a
`Fails without:` line, a doc line, a count, a caller). A use in another
story's draft is the seams re-check's, not yours. Change nothing else, and
leave unchanged every text the contract names as quoted by another story. A
finding you think is wrong goes in your report with your reason, unapplied.
On a wave job (the dispatch says so), a fix that would change a name or
behaviour another story uses goes in your report, unapplied, with why: the
wave runs once.
```

- [ ] **Step 7: The re-check follows uses**

In `flow/plugin/agents/plan-reviewer.md`, replace:

```
Important finding answer `FIXED` or `OPEN`, then check the changed spans as
above and report anything new as a `FINDING`. Review nothing outside the diff.
```

with:

```
Important finding answer `FIXED` or `OPEN`, then check the changed spans as
above and report anything new as a `FINDING`. Then grep the draft for each
name the changed spans define or now call differently, and check that each
use outside the diff still holds (a test's expected value, a doc line, a
count, a caller): a fix that changed what code returns can leave a test
elsewhere in the story asserting the old value. Review nothing else outside
the diff.
```

In `flow/plugin/agents/risk-reviewer.md`, replace:

```
Important finding of yours answer `FIXED` or `OPEN`, then check the changed
spans as above and report anything new as a `FINDING`.
```

with:

```
Important finding of yours answer `FIXED` or `OPEN`, then check the changed
spans as above and report anything new as a `FINDING`. Then grep the draft for
each name the changed spans define or now call differently, and check that
each use outside the diff still holds. Review nothing else outside the diff.
```

- [ ] **Step 8: The dispatch guide**

In `flow/plugin/docs/dispatch.md`, replace:

```
verdict. Findings join the story's fix job. After the fixes, a story whose
draft changed since its review gets one re-check of the diff, never a loop.
```

with:

```
verdict. Findings join the story's fix job. After the fixes, a story whose
draft changed since its review gets one re-check of the diff and of the uses
of what it changed, never a loop.
```

Replace:

```
   code only as a last resort. Name there, too, each text a later story quotes
   from an earlier one (its "current" block). Every fixer leaves a named text
   unchanged, so fixers working at once can't break a later story's quote.
```

with:

```
   code only as a last resort. Name there, too, each text a later story quotes
   from an earlier one (its "current" block). Every fixer leaves a named text
   unchanged, so fixers working at once can't break a later story's quote. A
   finding states the defect and the result it needs, never the lines to edit:
   one that said "edit only lines 137-141" put a bullet under the wrong
   heading. A defect with two sides names both, and a decision between two
   stories' versions names what each keeps and drops, in both stories'
   findings in the same round: two fixers given halves of one duplicate's
   decision left a test that the kept code fails.
```

Replace:

```
   findings that name the story, and its plan review's open findings.
3. **Join and check again:** `loom flow join`, `loom flow check-plan` and
   `loom flow premises`, with `flow:premise-check` on any new flag, and the seams
   check once more when a fix changed the contract. Then a story whose draft
   changed since its plan review gets its one re-check.
4. **One round.** A finding still open after its fix round goes to the owner;
   there is no second automatic round.
```

with:

```
   findings that name the story, and its plan review's open findings. A fixer
   also fixes what its fixes make false in its own draft, grepping it for the
   uses of each name whose code or behaviour it changed.
3. **Join and check again:** `loom flow join`, `loom flow check-plan` and
   `loom flow premises`, with `flow:premise-check` on any new flag. Then the
   seams re-check: the full seams check when a fix changed the contract, else
   `loom flow packet <plan> --seams --changed`, which lists only the names one
   story defines and another uses where a fix since the plan review changed
   the definition (a body edit counts) or a line that uses it, from each
   draft's `D/reviewing/` copy; when it lists any, one `flow:recon` checks
   their contracts and behaviour. Its findings go to one wave of fresh
   drafters, one per story they name, which runs once: a wave fixer that would
   change what another story uses reports it instead. The seams check used to
   re-run only when a fix changed a name, and 2 to 3 of the 11 defects that
   planning's fixes introduced in the 8 drafted plans of 2026-11-10 to
   2026-11-13 were behaviour another story relied on. After the wave, join and
   check once more, and a story whose draft changed since its plan review gets
   its one re-check.
4. **One round.** A finding still open after its fix round and the wave goes
   to the owner; there is no further automatic round. Each finding a fixer
   leaves unapplied, and each clash it reports, is settled: closed with a
   reason, given to the wave, or taken to the owner.
```

- [ ] **Step 9: Run the agent lint**

Run: `bash flow/lib/check-agents.sh`
Expected: `OK` (descriptions are unchanged).

- [ ] **Step 10: Commit**

```bash
git add flow/plugin/skills/draft/SKILL.md flow/plugin/agents/drafter.md flow/plugin/agents/plan-reviewer.md flow/plugin/agents/risk-reviewer.md flow/plugin/docs/dispatch.md
git commit -m "feat(flow): a seams re-check and one wave after /flow:draft's fixes, and fix-hygiene rules" -- flow/plugin/skills/draft/SKILL.md flow/plugin/agents/drafter.md flow/plugin/agents/plan-reviewer.md flow/plugin/agents/risk-reviewer.md flow/plugin/docs/dispatch.md
```

---

### Task 3: README, provenance, spec, gates

**Files:**
- Modify: `flow/README.md` (the `packet` row)
- Modify: `flow/PROVENANCE.md` (new first entries under `## drafter`, `## plan-reviewer`, `## recon`, `## risk-reviewer`)
- Modify: `docs/superpowers/specs/2026-11-13-flow-fix-hygiene-design.md` (§2.1: changed lines cited as draft lines)

- [ ] **Step 1: README**

Replace:

```
| `packet` | `plan_packet.py`: the seams packet, the drafters' unchecked premises (`D/premises/`) included |
```

with:

```
| `packet` | `plan_packet.py`: the seams packet, the drafters' unchecked premises (`D/premises/`) included; with `--changed`, the seams re-check's: only the shared names a fix changed since the plan review (`D/reviewing/`) |
```

- [ ] **Step 2: Provenance**

Under `## drafter`, before its first entry:

```
- 2026-11-13, a fix job also fixes what its fixes make false in its own draft, grepping it for the uses of each name whose code or behaviour it changed, and a wave job reports instead of changing what another story uses (`agents/drafter.md`, the draft skill's steps 2 and 5, `docs/dispatch.md`); no pin changed. On the owner's instruction (`docs/superpowers/specs/2026-11-13-flow-fix-hygiene-design.md`), from the transcripts of the 8 `/flow:draft` sessions of 2026-11-10 to 2026-11-13: about 11 of the ~95 plan-code defects were introduced by planning's own fixes, 0.07 per 1,000 plan lines over 152,028. Three were a fix that left its own story's test, text or expected value asserting the old behaviour, one after the fixer's own grep printed that test; five were the manager's own finding or amendment (a pinned span, one side of a two-sided defect, one story's version chosen without saying what the other kept). **Wrong if:** the next retro's transcript trace finds planning's fixes introducing defects at 0.035 or more per 1,000 plan lines.

```

Under `## plan-reviewer`, before its first entry:

```
- 2026-11-13, the re-check also greps the draft for the uses of each name its diff changed and checks those outside the diff (`agents/plan-reviewer.md`); no pin changed. On the owner's instruction (`docs/superpowers/specs/2026-11-13-flow-fix-hygiene-design.md`), from the same trace: a fix that changed what code does left a test or a statement elsewhere in its story asserting the old behaviour, outside any diff. **Wrong if:** as the drafter's entry of the same date.

```

Under `## recon`, before its first entry:

```
- 2026-11-13, a seams re-check after `/flow:draft`'s step 5 fixes: `loom flow packet --seams --changed` lists only the names one story defines and another uses where a fix since the plan review changed the definition or a line that uses it, and one recon checks their contracts and behaviour when it lists any; its findings go to one wave of fix jobs (the draft skill's step 5, `docs/dispatch.md`, `lib/plan_packet.py`); no pin changed. On the owner's instruction (`docs/superpowers/specs/2026-11-13-flow-fix-hygiene-design.md`), from the same trace: 2 to 3 defects were a fix or amendment that changed behaviour another story relied on, which the seams recon, re-run only when a fix changed a name, never saw. **Wrong if:** as the drafter's entry of the same date.

```

Under `## risk-reviewer`, before its first entry:

```
- 2026-11-13, the re-check also greps the draft for the uses of each name its diff changed and checks those outside the diff (`agents/risk-reviewer.md`); no pin changed. On the owner's instruction, as plan-reviewer's entry of the same date.

```

- [ ] **Step 3: The spec cites draft lines**

In `docs/superpowers/specs/2026-11-13-flow-fix-hygiene-design.md` §2.1, replace:

```
Each row is `name | defined <cite> | used <cites> | changed in Story <x> at <plan ranges>`, cites as the seams packet writes them. A section `## Changed spans` gives each changed story's changed plan ranges,
```

with:

```
Each row is `name | defined <cite> | used <cites> | changed in S<x>.md:<a>-<b>, …`, cites as the seams packet writes them (draft lines, since the packet needs the join map). A section `## Changed spans` gives each changed story's changed draft lines,
```

- [ ] **Step 4: Run the gates**

Run: `loom job run -d "flow suite" -- bash flow/lib/test.sh`
Expected: the unit tests `OK` and the agent lint `OK`.

Run: `loom job run -d "flow embed and claudemd tests" -- env CGO_ENABLED=0 go test ./flow/... ./tools/claudemd/...`
Expected: `ok` for each package.

- [ ] **Step 5: Commit**

```bash
git add flow/README.md flow/PROVENANCE.md docs/superpowers/specs/2026-11-13-flow-fix-hygiene-design.md
git commit -m "docs(flow): the seams re-check in the README, provenance and spec" -- flow/README.md flow/PROVENANCE.md docs/superpowers/specs/2026-11-13-flow-fix-hygiene-design.md
```

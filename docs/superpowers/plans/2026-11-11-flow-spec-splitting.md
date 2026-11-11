# Flow spec splitting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a spec too big for one plan become an epic of parts, each a plan designed after the previous merges, with a story budget in the skeleton lint, story-size estimates in the question round, and epic-aware records, costs and flow view.

**Architecture:** `plan_skeleton.py` checks a skeleton's `**Part:**` line against its spec's `## Delivery` list and its story count against the profile's `[size] plan` (with a `**Size:**` escape hatch); `check_plan.py --json` passes the part on. `agent_costs.py` narrows a later part's design window and gains `epic`. The run engine copies the Part line into the record; the flow view labels parts and epics, through to the PWA. The drafter's questions file gains a `## Size` list, and the draft skill, run skill, retro skill and dispatch guide describe epics.

**Tech Stack:** Python 3 (`unittest`), Go (testify), Preact modules (`.mjs`), Markdown prompts.

**Spec:** `docs/superpowers/specs/2026-11-11-flow-spec-splitting-design.md`

**Execution:** inline (the owner's choice, 2026-11-11), four packages, one commit each, no checkpoint pauses. Tests run through loom's queue (`loom job run -d "<what>" -- <cmd>`) from the repo's top level.

---

## File structure

| File | Responsibility |
|---|---|
| `flow/lib/check_plan.py` | `PART_RE`, `part_line()`, `plan_part()`; `--json`'s `"part"` |
| `flow/lib/flowconf.py` | `[size] plan` (`POSITIVE` kind), `DEFAULT_PLAN_SIZE`, `Profile.plan_size` |
| `flow/lib/plan_skeleton.py` | `delivery_parts()`, `part_problems()`, `size_problems()`; the budget from the profile |
| `flow/lib/agent_costs.py` | `plan_head()`, `epic_plans()`, `epic_records()`, a part's design window, `epic_report()` |
| `internal/flowrun/state.go`, `record.go` | `State.Part`; the record's `- Part:` line |
| `internal/cli/flow_run.go` | `partLine()` fills `State.Part` |
| `internal/flowview/plan.go`, `find.go` | `planHeader.Part`, `.Parts`; `Summary.Part` and `partLabel()` |
| `internal/web/flow.go`, `static/js/flow.mjs`, `static/js/flows.mjs` | `part` in the flow summary JSON and on screen |
| `flow/plugin/agents/drafter.md`, `skills/draft`, `skills/run`, `skills/retro`, `docs/dispatch.md`, `docs/records.md` | the Size list, settling splits, the hand-over, epics |
| `session/loom_context_flow.md`, `flow/README.md`, `flow/PROVENANCE.md`, `internal/web/README.md` | the epic sentence; tools; provenance; the JSON field |

---

## Task 1: The part and the story budget in the lint

**Files:**
- Modify: `flow/lib/check_plan.py`, `flow/lib/flowconf.py`, `flow/lib/plan_skeleton.py`
- Test: `flow/lib/plan_skeleton_test.py`, `flow/lib/check_plan_test.py`, `flow/lib/flowconf_test.py`

- [ ] **Step 1: Write the failing tests**

In `flow/lib/plan_skeleton_test.py`:

1. After the imports, keep every test off the real home store:

```python
import os

_HOME = tempfile.TemporaryDirectory()


def setUpModule():
    os.environ["FLOW_HOME"] = _HOME.name


def tearDownModule():
    _HOME.cleanup()
```

2. Replace `lint` with a version that takes the spec's text and a profile:

```python
def lint(text, spec=True, spec_text="# Spec\n", profile=None):
    """lint's problems for a skeleton written into a throwaway repo, with its spec's text and an optional profile."""
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        (root / ".git").mkdir()
        if spec:
            (root / "docs").mkdir()
            (root / "docs" / "spec.md").write_text(spec_text)
        if profile is not None:
            (root / ".claude").mkdir()
            (root / ".claude" / "flow.toml").write_text(profile)
        plan = root / "plan.md"
        plan.write_text(text)
        problems, _, _ = plan_skeleton.lint(text, plan)
        return problems


def stories(n):
    """The story-list lines of Stories 1 to n."""
    return [line for sid in range(1, n + 1) for line in entry(str(sid))]


DELIVERY = textwrap.dedent(
    """\
    # Spec

    ## Delivery

    An epic of 3 parts.

    1. **Part 1: kernel.** Stage 0.
    2. **Part 2: markets.** Stages 1 and 2.
    3. **Part 3: identity.** Stage 3.

    ### Part 2 settlements (2026-11-14)

    1. **Part 9: not a part.** A settlement's own list.
    """
)
PART_HEAD = ("# Plan", "", "**Spec:** `docs/spec.md`", "**Part:** 2 of 3", "")
```

3. Append:

```python
class PartTest(unittest.TestCase):
    def test_a_part_the_delivery_list_holds_passes(self):
        self.assertEqual(lint(skeleton([], head=PART_HEAD), spec_text=DELIVERY), [])

    def test_a_split_spec_needs_a_part_line(self):
        problems = lint(skeleton([]), spec_text=DELIVERY)
        self.assertTrue(any("the spec delivers 3 parts: name this plan's part" in p for p in problems), problems)

    def test_a_part_line_needs_a_delivery_list(self):
        problems = lint(skeleton([], head=PART_HEAD))
        self.assertTrue(any("the spec has no ## Delivery section" in p for p in problems), problems)

    def test_a_part_outside_the_list_or_a_wrong_count_fails(self):
        for line in ("**Part:** 4 of 3", "**Part:** 2 of 4", "**Part:** 0 of 3"):
            head = ("# Plan", "", "**Spec:** `docs/spec.md`", line, "")
            problems = lint(skeleton([], head=head), spec_text=DELIVERY)
            self.assertTrue(any("doesn't match the spec's ## Delivery list of 3 parts" in p for p in problems), (line, problems))

    def test_a_part_line_that_is_not_n_of_m_fails(self):
        head = ("# Plan", "", "**Spec:** `docs/spec.md`", "**Part:** two", "")
        problems = lint(skeleton([], head=head), spec_text=DELIVERY)
        self.assertTrue(any("must read `N of M`" in p for p in problems), problems)

    def test_a_delivery_list_out_of_order_fails(self):
        spec = DELIVERY.replace("2. **Part 2: markets.**", "3. **Part 3: markets.**")
        problems = lint(skeleton([], head=PART_HEAD), spec_text=spec)
        self.assertTrue(any("number its parts 1 to M in order" in p for p in problems), problems)

    def test_delivery_parts_stops_at_the_settlements(self):
        self.assertEqual(plan_skeleton.delivery_parts(DELIVERY), [(1, 1), (2, 2), (3, 3)])
        self.assertIsNone(plan_skeleton.delivery_parts("# Spec\n"))


class BudgetTest(unittest.TestCase):
    def test_the_default_budget_is_fifteen_stories(self):
        self.assertEqual(lint(skeleton([], stories=stories(15))), [])
        problems = lint(skeleton([], stories=stories(16)))
        self.assertTrue(any("16 stories, over the plan budget of 15" in p for p in problems), problems)

    def test_a_size_line_with_a_reason_lets_it_through_and_an_empty_one_does_not(self):
        reasoned = ("# Plan", "", "**Spec:** `docs/spec.md`", "**Size:** one migration ties every stage", "")
        self.assertEqual(lint(skeleton([], stories=stories(16), head=reasoned)), [])
        empty = ("# Plan", "", "**Spec:** `docs/spec.md`", "**Size:**", "")
        problems = lint(skeleton([], stories=stories(16), head=empty))
        self.assertTrue(any("the **Size:** line gives no reason" in p for p in problems), problems)

    def test_the_profile_sets_the_budget(self):
        self.assertEqual(lint(skeleton([], stories=stories(16)), profile="schema = 1\n[size]\nplan = 20\n"), [])
        problems = lint(skeleton([], stories=stories(4)), profile="schema = 1\n[size]\nplan = 3\n")
        self.assertTrue(any("4 stories, over the plan budget of 3" in p for p in problems), problems)

    def test_a_profile_the_flow_refuses_is_a_problem_not_a_crash(self):
        problems = lint(skeleton([]), profile="schema = 1\n[size]\nplan = 0\n")
        self.assertTrue(any("the profile can't be read" in p and "positive integer" in p for p in problems), problems)
```

In `flow/lib/check_plan_test.py`, append to `JsonTest`:

```python
    def test_a_part_line_is_passed_on_and_absent_without_one(self):
        code, out = run(GOOD.replace("**Spec:** `docs/spec.md`, approved.", "**Spec:** `docs/spec.md`, approved.\n**Part:** 2 of 3"), as_json=True)
        self.assertEqual(code, 0, out)
        self.assertEqual(json.loads(out)["part"], [2, 3])
        code, out = run(GOOD, as_json=True)
        self.assertNotIn("part", json.loads(out))
```

In `flow/lib/flowconf_test.py`, append (the module already imports `flowconf`, `Path` and `unittest`):

```python
class SizeKeyTest(unittest.TestCase):
    def test_size_plan_is_read_and_defaults_to_fifteen(self):
        self.assertEqual(flowconf.validate({"schema": 1, "size": {"plan": 20}}, Path("p.toml"), "d").plan_size, 20)
        self.assertEqual(flowconf.validate({"schema": 1}, Path("p.toml"), "d").plan_size, flowconf.DEFAULT_PLAN_SIZE)
        self.assertEqual(flowconf.DEFAULT_PLAN_SIZE, 15)

    def test_size_plan_must_be_a_positive_integer(self):
        for bad in (0, -1, "15", True, 1.5):
            with self.assertRaisesRegex(flowconf.FlowError, r"size\.plan: must be a positive integer"):
                flowconf.validate({"schema": 1, "size": {"plan": bad}}, Path("p.toml"), "d")
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "lint part and budget tests (red)" -- sh -c 'cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q plan_skeleton_test check_plan_test.JsonTest flowconf_test.SizeKeyTest'`
Expected: failures in `PartTest`, `BudgetTest`, the JSON part test and `SizeKeyTest` (`delivery_parts`, `plan_size` and `DEFAULT_PLAN_SIZE` don't exist; `size` is an unknown key).

- [ ] **Step 3: Implement**

`flow/lib/flowconf.py`:

```python
POSITIVE = "positive"
# The most stories a plan lists before the skeleton lint wants an epic or a reason (the spec-splitting spec, §4).
DEFAULT_PLAN_SIZE = 15
```

beside `TEXT` and `GATES`; in `KEYS` add `"size": {"plan": POSITIVE},`; in `validate`'s loop, after the `elif kind is str:` branch:

```python
            elif kind == POSITIVE:
                if type(item) is not int or item < 1:
                    raise FlowError(f"{path}: {dotted}: must be a positive integer")
                values[dotted] = item
```

`Profile` gains `plan_size: int = DEFAULT_PLAN_SIZE` after `doc_paths`, and `validate`'s `Profile(...)` gains `plan_size=values.get("size.plan", DEFAULT_PLAN_SIZE),`.

`flow/lib/check_plan.py`, beside `REVIEW_RE`:

```python
PART_RE = re.compile(r"^\*\*Part:\*\*\s*(.*)$")
PART_VALUE_RE = re.compile(r"^(\d+) of (\d+)\.?$")
```

after `plan_review`:

```python
def part_line(text: str) -> str | None:
    """The text of the plan's `**Part:**` line before its first story, None when it has none."""
    fenced = False
    for line in text.splitlines():
        if FENCE_RE.match(line):
            fenced = not fenced
        elif not fenced:
            if STORY_RE.match(line):
                return None
            named = PART_RE.match(line)
            if named:
                return named.group(1).strip()
    return None


def plan_part(text: str) -> tuple[int, int] | None:
    """The plan's part as (N, M) from a `**Part:** N of M` line, None when it has none or the line doesn't parse."""
    raw = part_line(text)
    value = PART_VALUE_RE.match(raw) if raw is not None else None
    return (int(value.group(1)), int(value.group(2))) if value else None
```

and in `main`'s `--json` branch build the object first:

```python
            out = {"spec": spec, "plan_review": review,
                   "stories": [story_json(s, vouch, profile.markers, s.sid in laned) for s in stories]}
            part = plan_part(text)
            if part:
                out["part"] = list(part)
            print(json.dumps(out))
```

Add to the module docstring's `--json` sentence: "and `part`, `[N, M]`, when the plan has a `**Part:** N of M` line".

`flow/lib/plan_skeleton.py`:

- imports: `from check_plan import ..., PART_RE, PART_VALUE_RE, ...` and `from flowconf import DEFAULT_PLAN_SIZE, FlowError, optional_profile`;
- constants:

```python
SIZE_RE = re.compile(r"^\*\*Size:\*\*\s*(.*)$")
DELIVERY_HEADING = "## Delivery"
DELIVERY_ITEM_RE = re.compile(r"^(\d+)\.\s+\*\*Part\s+(\d+):")
```

- functions, before `lint`:

```python
def delivery_parts(spec_text: str) -> list[tuple[int, int]] | None:
    """Each part of the spec's `## Delivery` list as (item number, part number), up to the section's first `###`;
    None when the spec has no such section."""
    found: list[tuple[int, int]] | None = None
    for _, line in unfenced(spec_text):
        if line.rstrip() == DELIVERY_HEADING:
            found = []
        elif found is not None and (H2_RE.match(line) or line.startswith("### ")):
            break
        elif found is not None:
            item = DELIVERY_ITEM_RE.match(line)
            if item:
                found.append((int(item.group(1)), int(item.group(2))))
    return found


def part_problems(head: list[tuple[int, str]], spec_text: str) -> list[str]:
    """What keeps the skeleton's `**Part:**` line and its spec's `## Delivery` list from agreeing."""
    parts = delivery_parts(spec_text)
    if parts is not None and (not parts or any(item != i or part != i for i, (item, part) in enumerate(parts, 1))):
        return ["the spec's ## Delivery list must number its parts 1 to M in order, each as `N. **Part N: <title>.**`"]
    raw = next((m.group(1).strip() for _, line in head if (m := PART_RE.match(line))), None)
    if raw is None:
        if parts:
            return [f"the spec delivers {len(parts)} parts: name this plan's part in a **Part:** line, as `**Part:** 1 of {len(parts)}`"]
        return []
    value = PART_VALUE_RE.match(raw)
    if not value:
        return [f"the **Part:** line must read `N of M`, not {raw!r}"]
    n, m = int(value.group(1)), int(value.group(2))
    if parts is None:
        return ["the **Part:** line names a part, but the spec has no ## Delivery section listing its parts"]
    if m != len(parts) or not 1 <= n <= m:
        return [f"**Part:** {n} of {m} doesn't match the spec's ## Delivery list of {len(parts)} parts"]
    return []


def size_problems(head: list[tuple[int, str]], stories: int, budget: int) -> list[str]:
    """The story budget: more stories than `budget` need a `**Size:**` line with a reason."""
    if stories <= budget:
        return []
    reason = next((m.group(1).strip() for _, line in head if (m := SIZE_RE.match(line))), None)
    if reason:
        return []
    problem = (
        f"{stories} stories, over the plan budget of {budget} ([size] plan in the profile): split the spec into an "
        "epic in its ## Delivery section, or say why it stays one plan in a **Size:** line"
    )
    return [problem + ("; the **Size:** line gives no reason" if reason == "" else "")]
```

- in `lint`: replace the spec block with one that keeps the spec's text,

```python
    spec_text: str | None = None
    spec = next((CODE_RE.findall(m.group(1)) for _, line in head if (m := SPEC_RE.match(line))), None)
    if spec is None:
        problems.append("no **Spec:** line: the drafters and the run find the spec through it")
    elif not spec:
        problems.append("the **Spec:** line names no `backticked` path")
    elif not (repo_root(plan) / spec[0]).is_file():
        problems.append(f"the spec {spec[0]} does not exist")
    else:
        try:
            spec_text = (repo_root(plan) / spec[0]).read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as err:
            problems.append(f"the spec {spec[0]} cannot be read: {getattr(err, 'strerror', None) or err}")
    if spec_text is not None:
        problems += part_problems(head, spec_text)
```

  and before `return`:

```python
    try:
        profile = optional_profile(repo_root(plan))
        budget = profile.plan_size if profile else DEFAULT_PLAN_SIZE
    except FlowError as err:
        problems.append(f"the profile can't be read: {err}")
        budget = DEFAULT_PLAN_SIZE
    if listed:
        problems += size_problems(head, len(listed), budget)
```

- the module docstring's list gains: "the part: a `**Part:** N of M` line agreeing with the spec's `## Delivery` list (`N. **Part N: <title>.**`, numbered 1 to M), needed when the spec has one; the story budget: more stories than the profile's `[size] plan` (15 when unset) need a `**Size:**` line with a reason".

- [ ] **Step 4: Run every flow test**

Run: `loom job run -d "flow lib tests" -- bash flow/lib/test.sh`
Expected: `OK`; no FAIL from the agent lint.

- [ ] **Step 5: Commit**

```bash
git add flow/lib/check_plan.py flow/lib/flowconf.py flow/lib/plan_skeleton.py flow/lib/plan_skeleton_test.py flow/lib/check_plan_test.py flow/lib/flowconf_test.py
git commit -m "feat(flow): the skeleton lint checks an epic's part and a story budget" -- flow/lib/check_plan.py flow/lib/flowconf.py flow/lib/plan_skeleton.py flow/lib/plan_skeleton_test.py flow/lib/check_plan_test.py flow/lib/flowconf_test.py
```

---

## Task 2: An epic's costs

**Files:**
- Modify: `flow/lib/agent_costs.py`
- Test: `flow/lib/agent_costs_test.py`

- [ ] **Step 1: Write the failing tests**

Append to `flow/lib/agent_costs_test.py` (it already has `record`, `reply`, `SCRIPT`, `re`, `json`, `os`, `subprocess`, `sys`, `tempfile`, `Path`):

```python
import flowconf  # noqa: E402

DESIGN1, DRAFT1, MANAGER1, DESIGN2 = (f"{n}{n}{n}{n}{n}{n}{n}{n}-{n}{n}{n}{n}-{n}{n}{n}{n}-{n}{n}{n}{n}-{n * 12}" for n in "1234")


def epic_fixture(root: Path):
    """A repo whose spec delivers 2 parts: part 1's plan (committed 12:00, its Design stage naming DRAFT1) and part 2's
    (committed 18:00); part 1's run record names MANAGER1; DESIGN1, DRAFT1, MANAGER1 and DESIGN2 each wrote the spec, at
    11:00, 13:00, 14:00 and 16:00. Returns (repo, env)."""
    repo = root / "repo"
    (repo / "docs").mkdir(parents=True)
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    git = ["git", "-C", str(repo), "-c", "user.name=t", "-c", "user.email=t@e", "-c", "commit.gpgsign=false"]
    subprocess.run(["git", "-C", str(repo), "init", "-q"], check=True, env=env)
    spec = repo / "docs" / "x-design.md"
    spec.write_text("# X\n\n## Delivery\n\n1. **Part 1: a.** A.\n2. **Part 2: b.** B.\n")
    stage = f"**Design stage:** /flow:draft session {DRAFT1}, 2026-11-09T12:10:00Z to 2026-11-09T12:50:00Z UTC: $10.00 logged, $10.00 with estimated thinking; 5000 plan lines, $2.00 per 1,000 lines."
    for name, part, when, extra in (("plan1.md", 1, "2026-11-09T12:00:00Z", stage + "\n"), ("plan2.md", 2, "2026-11-09T18:00:00Z", "")):
        (repo / "docs" / name).write_text(f"# Plan\n\n**Spec:** `docs/x-design.md`\n**Part:** {part} of 2\n{extra}\n## Stories\n")
        dated = {**env, "GIT_AUTHOR_DATE": when, "GIT_COMMITTER_DATE": when}
        subprocess.run([*git, "add", f"docs/{name}", "docs/x-design.md"], check=True, env=dated)
        subprocess.run([*git, "commit", "-q", "-m", name], check=True, env=dated)
    data = root / "data"
    key = flowconf.repo_key(repo)
    (data / "records" / key).mkdir(parents=True)
    costs = json.dumps({"by_type": {}, "total": {"usd": 20.0}})
    (data / "records" / key / "run1.md").write_text(
        f"<!-- agent-costs {costs} -->\n# Run: run1\n\n- Plan: docs/plan1.md (1 stories)\n- Spec: docs/x-design.md\n"
        f"- Part: 1 of 2 of docs/x-design.md\n- Manager: claude-opus-5-5 {MANAGER1}\n")
    cfg = root / "cfg"
    top = subprocess.run(["git", "-C", str(repo), "rev-parse", "--show-toplevel"], capture_output=True, text=True).stdout.strip()
    project = cfg / "projects" / re.sub(r"[^A-Za-z0-9]", "-", top)
    project.mkdir(parents=True)
    write = {"type": "tool_use", "name": "Edit", "input": {"file_path": str(spec), "old_string": "a", "new_string": "b"}}
    for sid, when in ((DESIGN1, "11:00"), (DRAFT1, "13:00"), (MANAGER1, "14:00"), (DESIGN2, "16:00")):
        (project / f"{sid}.jsonl").write_text(
            reply(sid[:2], "claude-opus-5-5", f"2026-11-09T{when}:00Z", write, input_tokens=1000, output_tokens=100))
    return repo, {**env, "CLAUDE_CONFIG_DIR": str(cfg), "FLOW_DATA": str(data)}


class EpicTest(unittest.TestCase):
    def costs(self, *args):
        with tempfile.TemporaryDirectory() as tmp:
            repo, env = epic_fixture(Path(tmp))
            return subprocess.run([sys.executable, str(SCRIPT), *args], capture_output=True, text=True, cwd=repo, env=env)

    def test_a_later_parts_design_leaves_out_earlier_parts_sessions(self):
        result = self.costs("design", "docs/x-design.md", "--plan", "docs/plan2.md", "--json")
        self.assertEqual(result.returncode, 0, result.stderr)
        out = json.loads(result.stdout)
        self.assertEqual([s["session"] for s in out["sessions"]], [DESIGN2])
        self.assertEqual(out["since"], "2026-11-09T12:00:00+00:00")
        self.assertEqual(out["excluded"], sorted([DRAFT1, MANAGER1]))

    def test_the_first_part_keeps_todays_rule(self):
        out = json.loads(self.costs("design", "docs/x-design.md", "--plan", "docs/plan1.md", "--json").stdout)
        self.assertEqual([s["session"] for s in out["sessions"]], [DESIGN1])
        self.assertIsNone(out["since"])

    def test_epic_gives_a_row_per_part_and_a_total(self):
        result = self.costs("epic", "docs/x-design.md")
        self.assertEqual(result.returncode, 0, result.stderr)
        lines = result.stdout.splitlines()
        part1 = next(l for l in lines if l.startswith("| 1 |"))
        part2 = next(l for l in lines if l.startswith("| 2 |"))
        self.assertIn("| $10.00 | $20.00 | 5,000 |", part1)
        self.assertIn("| — |", part2, "part 2 has no run record yet")
        self.assertTrue(any(l.startswith("| **Total** |") for l in lines))

    def test_epic_refuses_a_spec_that_is_no_epic(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, env = epic_fixture(Path(tmp))
            (repo / "docs" / "y-design.md").write_text("# Y\n")
            result = subprocess.run([sys.executable, str(SCRIPT), "epic", "docs/y-design.md"], capture_output=True, text=True, cwd=repo, env=env)
            self.assertEqual(result.returncode, 1)
            self.assertIn("is no epic", result.stderr)
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "agent_costs epic tests (red)" -- sh -c 'cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q agent_costs_test.EpicTest'`
Expected: failures: no `since` or `excluded` key, DESIGN1 priced for part 2, and `epic` an invalid choice.

- [ ] **Step 3: Implement**

In `flow/lib/agent_costs.py`, import `from flowconf import flow_data, repo_key  # noqa: E402`, and after `design_json`'s helpers add:

```python
PLAN_SPEC_RE = re.compile(r"^\*\*(?:Spec|Design):\*\*\s*.*?`([^`]+)`", re.M)
PLAN_PART_RE = re.compile(r"^\*\*Part:\*\*\s*(\d+) of (\d+)\.?\s*$", re.M)
PLAN_STAGE_RE = re.compile(r"^\*\*Design stage:\*\*\s*(.*)$", re.M)
SESSION_ID_RE = re.compile(r"\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b")
LOGGED_RE = re.compile(r"\$([0-9][0-9,]*(?:\.[0-9]+)?) logged")
PLAN_LINES_RE = re.compile(r"([0-9][0-9,]*) plan lines")
RECORD_SPEC_RE = re.compile(r"^- Spec: (.*)$", re.M)
RECORD_PART_RE = re.compile(r"^- Part: (\d+) of (\d+)", re.M)
RECORD_MANAGER_RE = re.compile(r"^- Manager: (.*)$", re.M)
MANAGERS_RE = re.compile(r"<!-- agent-costs-managers (\{.*?\}) -->", re.S)
DELIVERY_ITEM_RE = re.compile(r"^(\d+)\.\s+\*\*Part\s+(\d+):", re.M)


def toplevel() -> Path:
    return Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True).stdout.strip() or os.getcwd())


def plan_head(text: str) -> str:
    """A plan's text before its first story."""
    cut = re.search(r"^## Story\s", text, re.M)
    return text[: cut.start()] if cut else text


def read_text(path: Path) -> str:
    try:
        return path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""


def epic_plans(top: Path, spec: str) -> dict[int, tuple[str, int]]:
    """{N: (plan path, M)} for each tracked plan whose **Spec:** line names spec and that has a **Part:** line."""
    listed = subprocess.run(["git", "-C", str(top), "grep", "-l", "-F", "**Part:**", "--", "*.md"],
                            capture_output=True, text=True).stdout.split()
    plans: dict[int, tuple[str, int]] = {}
    for rel in listed:
        head = plan_head(read_text(top / rel))
        named, part = PLAN_SPEC_RE.search(head), PLAN_PART_RE.search(head)
        if named and part and named.group(1) == spec:
            plans[int(part.group(1))] = (rel, int(part.group(2)))
    return plans


def epic_records(top: Path, spec: str) -> dict[int, list[Path]]:
    """{N: run records} of this repo's records whose Spec line is spec and that carry a Part line, by name."""
    found: dict[int, list[Path]] = {}
    for path in sorted((flow_data() / "records" / repo_key(top)).glob("*.md")):
        text = read_text(path)
        named, part = RECORD_SPEC_RE.search(text), RECORD_PART_RE.search(text)
        if named and part and named.group(1).strip() == spec:
            found.setdefault(int(part.group(1)), []).append(path)
    return found


def record_managers(path: Path) -> set[str]:
    """The manager session ids a run record names: its Manager line and its agent-costs-managers comment."""
    text = read_text(path)
    ids = set()
    line = RECORD_MANAGER_RE.search(text)
    if line:
        ids |= set(SESSION_ID_RE.findall(line.group(1)))
    for comment in MANAGERS_RE.finditer(text):
        ids |= set(json.loads(comment.group(1)))
    return ids


def record_usd(path: Path) -> float | None:
    """A run record's total USD from its agent-costs comments, None without one."""
    comments = [json.loads(m.group(1)) for m in COMMENT_RE.finditer(read_text(path))]
    return merge_sessions(comments)["total"].get("usd", 0.0) if comments else None


def design_window(top: Path, spec: str, plan: str) -> tuple[datetime | None, set[str], str | None]:
    """For a part after the first: the time its design window opens (the previous part's last commit to its plan),
    the sessions earlier parts' Design stage lines and run records name, and a note when the previous part has no
    plan; (None, set(), None) for anything else."""
    part = PLAN_PART_RE.search(plan_head(read_text(top / plan)))
    if not part or int(part.group(1)) < 2:
        return None, set(), None
    n = int(part.group(1))
    plans = epic_plans(top, spec)
    if n - 1 not in plans:
        return None, set(), f"part {n - 1} has no plan in the repo: priced as a single plan's design"
    last = subprocess.run(["git", "-C", str(top), "log", "-1", "--format=%cI", "--", plans[n - 1][0]],
                          capture_output=True, text=True).stdout.strip()
    excluded: set[str] = set()
    for k, (rel, _) in plans.items():
        stage = PLAN_STAGE_RE.search(plan_head(read_text(top / rel)))
        if k < n and stage:
            excluded |= set(SESSION_ID_RE.findall(stage.group(1)))
    for k, records in epic_records(top, spec).items():
        if k < n:
            for path in records:
                excluded |= record_managers(path)
    return (parse_time(last) if last else None), excluded, None
```

`design_json` becomes:

```python
def design_json(spec: str, plan: str | None) -> dict:
    """The design session's cost (flow visualisation spec §3): the transcripts in the worktree's project dir that
    wrote the spec, each priced from its first request up to the commit that added the plan (now while it isn't
    committed). For a later part of an epic (the spec-splitting spec, §7) the window opens at the previous part's
    last commit to its plan, and the sessions earlier parts name are left out."""
    top = toplevel()
    target = (top / spec).resolve()
    until = None
    since, excluded, note = (design_window(top, spec, plan) if plan else (None, set(), None))
    if plan:
        added = subprocess.run(["git", "-C", str(top), "log", "-1", "--diff-filter=A", "--format=%cI", "--", plan],
                               capture_output=True, text=True).stdout.strip()
        until = parse_time(added) if added else None
    # The worktree's spellings: Claude names a project dir after the cwd it ran in, which may be a symlinked path.
    spellings = {str(top), os.getcwd(), os.path.realpath(top), os.path.realpath(os.getcwd())}
    projects = sorted({projects_root() / re.sub(r"[^A-Za-z0-9]", "-", s) for s in spellings})
    sessions, total = [], Usage()
    for project in projects:
        for path in sorted(project.glob("*.jsonl")):
            if path.stem in excluded or not wrote(path, target):
                continue
            usage, _, _ = transcript_usage(path, since, until)
            total.add(usage)
            sessions.append({"session": path.stem, **usd_cells(usage)})
    return {"spec": spec, "since": since.isoformat() if since else None, "until": until.isoformat() if until else None,
            "excluded": sorted(excluded), "note": note, "sessions": sessions, "total": usd_cells(total)}
```

`epic_report`:

```python
def epic_report(spec: str) -> tuple[str, int]:
    """One row per part of the epic: design, /flow:draft and run cost, plan lines and $ per 1,000 lines; then the total."""
    top = toplevel()
    spec_text = read_text(top / spec)
    delivery = spec_text.split("\n## Delivery\n", 1)[1] if "\n## Delivery\n" in spec_text else ""
    delivery = re.split(r"^#{2,3} ", delivery, maxsplit=1, flags=re.M)[0]
    listed = [int(m.group(2)) for m in DELIVERY_ITEM_RE.finditer(delivery)]
    plans, records = epic_plans(top, spec), epic_records(top, spec)
    if not listed and not plans:
        return f"agent_costs: {spec} is no epic: no ## Delivery section and no plan with a **Part:** line", 1
    rows = ["| Part | Plan | Design | Draft | Run | Plan lines | $ per 1,000 lines |", "|---:|---|---:|---:|---:|---:|---:|"]
    sums = [0.0, 0.0, 0.0, 0]
    for n in sorted(set(listed) | set(plans)):
        if n not in plans:
            rows.append(f"| {n} | not designed | — | — | — | — | — |")
            continue
        rel = plans[n][0]
        text = read_text(top / rel)
        design = design_json(spec, rel)["total"]["usd"]
        stage = PLAN_STAGE_RE.search(plan_head(text))
        logged = LOGGED_RE.search(stage.group(1)) if stage else None
        counted = PLAN_LINES_RE.search(stage.group(1)) if stage else None
        draft = float(logged.group(1).replace(",", "")) if logged else None
        lines = int(counted.group(1).replace(",", "")) if counted else len(text.splitlines())
        run = record_usd(records[n][-1]) if records.get(n) else None
        spent = design + (draft or 0.0) + (run or 0.0)
        sums = [sums[0] + design, sums[1] + (draft or 0.0), sums[2] + (run or 0.0), sums[3] + lines]
        cell = lambda v: fmt(v, usd=True) if v is not None else "—"  # noqa: E731
        rows.append(f"| {n} | {rel} | {cell(design)} | {cell(draft)} | {cell(run)} | {lines:,} | {fmt(spent * 1000 / lines, usd=True) if lines else '—'} |")
    total = sum(sums[:3])
    rows.append(f"| **Total** | | {fmt(sums[0], usd=True)} | {fmt(sums[1], usd=True)} | {fmt(sums[2], usd=True)} | {sums[3]:,} | "
                f"{fmt(total * 1000 / sums[3], usd=True) if sums[3] else '—'} |")
    return f"Epic {spec}\n\n" + "\n".join(rows), 0
```

`main`: a parser `epic = sub.add_parser("epic", help="price an epic part by part: design, /flow:draft and run"); epic.add_argument("spec", help="the epic's spec, from the top level")`, and before the records fall-through:

```python
    if args.command == "epic":
        text, code = epic_report(args.spec)
        print(text, file=sys.stderr if code else sys.stdout)
        return code
```

The `design` text output gains the note: after its `print(...)`, `if data["note"]: print(f"note: {data['note']}")`. The module docstring's usage gains `agent_costs.py epic SPEC`.

- [ ] **Step 4: Run every flow test**

Run: `loom job run -d "flow lib tests" -- bash flow/lib/test.sh`
Expected: `OK`.

- [ ] **Step 5: Commit**

```bash
git add flow/lib/agent_costs.py flow/lib/agent_costs_test.py
git commit -m "feat(flow): costs price a later part's design alone and an epic part by part" -- flow/lib/agent_costs.py flow/lib/agent_costs_test.py
```

---

## Task 3: The record, the flow view and the PWA

**Files:**
- Modify: `internal/flowrun/state.go`, `internal/flowrun/record.go`, `internal/cli/flow_run.go`, `internal/flowview/plan.go`, `internal/flowview/find.go`, `internal/web/flow.go`, `internal/web/static/js/flow.mjs`, `internal/web/static/js/flows.mjs`, `internal/web/README.md`, `docs/specs/protocol.md` (generated)
- Test: `internal/flowrun/record_test.go` (+ golden), `internal/cli/flow_run_test.go`, `internal/flowview/find_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/flowrun/record_test.go`, in `TestRecord` after `st.DesignStage = …`: `st.Part = "2 of 3"`.

`internal/cli/flow_run_test.go`, append:

```go
func TestPartLine(t *testing.T) {
	assert.Equal(t, "2 of 3", partLine("# P\n\n**Spec:** `s.md`\n**Part:** 2 of 3\n\n## Stories\n"))
	assert.Equal(t, "2 of 3", partLine("**Part:** 2 of 3.\n"))
	assert.Empty(t, partLine("# P\n\n**Spec:** `s.md`\n"))
	assert.Empty(t, partLine("# P\n\n## Story 1: x\n\n**Part:** 2 of 3\n"), "a line inside a story is not the plan's")
}
```

`internal/flowview/find_test.go`, append:

```go
func TestFind_APartOfAnEpicAndASpecThatSplitsAreLabelled(t *testing.T) {
	repo, base, records := branchRepo(t)
	writeFile(t, repo, specP, "# P design\n\n## Delivery\n\n1. **Part 1: a.** A.\n2. **Part 2: b.** B.\n3. **Part 3: c.** C.\n\n### Part 2 settlements (2026-11-14)\n\n1. **Part 9: no.** No.\n")
	got, err := Find(repo, base, records)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "epic, 3 parts", got[0].Part, "a spec in design that splits")

	writeFile(t, repo, planP, strings.Replace(skeleton(specP, "1"), "**Spec:**", "**Part:** 2 of 3\n**Spec:**", 1))
	got, err = Find(repo, base, records)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "part 2 of 3", got[0].Part)
}

func TestFind_APlanOfNoEpicHasNoPartLabel(t *testing.T) {
	repo, base, records := branchRepo(t)
	writeFile(t, repo, specP, "# P design\n")
	writeFile(t, repo, planP, skeleton(specP, "1"))
	got, err := Find(repo, base, records)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, got[0].Part)
}
```

(`find_test.go` imports `strings` if it doesn't already.)

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "flowrun cli flowview tests (red)" -- sh -c 'CGO_ENABLED=0 go test ./internal/flowrun/ ./internal/flowview/ -count=1 && CGO_ENABLED=0 go test ./internal/cli/ -run TestPartLine -count=1'`
Expected: build failures: `st.Part`, `partLine` and `Summary.Part` undefined.

- [ ] **Step 3: Implement**

`internal/flowrun/state.go`, after `DesignStage`:

```go
	Part        string                     `json:"part,omitempty"`         // the plan's **Part:** line, "N of M", "" for none
```

`internal/flowrun/record.go`, after the Design stage block:

```go
	if st.Part != "" {
		fmt.Fprintf(&b, "- Part: %s of %s\n", st.Part, st.Spec)
	}
```

`internal/cli/flow_run.go`, beside `designStage`:

```go
// partLine is the plan's **Part:** line before its first story ("2 of 3"),
// "" when it has none: the record copies it (the spec-splitting spec, §7).
func partLine(plan string) string {
	for _, l := range strings.Split(plan, "\n") {
		if strings.HasPrefix(l, "## Story") {
			return ""
		}
		if v, ok := strings.CutPrefix(l, "**Part:**"); ok {
			return strings.TrimSuffix(strings.TrimSpace(v), ".")
		}
	}
	return ""
}
```

and where the state is built (`DesignStage: designStage(string(text))`), add `Part: partLine(string(text)),`.

`internal/flowview/plan.go`: beside `stageRE`,

```go
	partRE     = regexp.MustCompile(`^\*\*Part:\*\*\s*(\d+ of \d+)\.?\s*$`) // a part's line (plan_skeleton.PART_RE)
	deliveryRE = regexp.MustCompile(`^\d+\.\s+\*\*Part\s+\d+:`)             // a Delivery list's part (plan_skeleton.DELIVERY_ITEM_RE)
```

and `const deliveryHeading = "## Delivery"`; `planHeader` gains

```go
	Part  string // the **Part:** line's "N of M" before the first story, "" for none
	Parts int    // the parts a spec's ## Delivery list names, up to its first ### heading
```

In `parsePlan`'s loop, before the `h2RE` check:

```go
		if strings.TrimRight(line, " \t\r") == deliveryHeading {
			inDelivery = true
			continue
		}
		if inDelivery && (h2RE.MatchString(line) || strings.HasPrefix(line, "### ")) {
			inDelivery = false
		}
		if inDelivery && deliveryRE.MatchString(line) {
			h.Parts++
		}
```

(declare `inDelivery := false` beside `inStories`), and in the `len(h.Stories) == 0` block:

```go
			if m := partRE.FindStringSubmatch(line); m != nil && h.Part == "" {
				h.Part = m[1]
			}
```

`internal/flowview/find.go`: `Summary` gains, after `Title`,

```go
	Part       string    // "part N of M" for a plan of an epic, "epic, M parts" for a spec in design that splits, "" otherwise
```

in the summary builder, after the `s.Title` lines: `s.Part = partLabel(f)`; and

```go
// partLabel is a flow's place in an epic (the spec-splitting spec, §7):
// a plan's **Part:** line, or the parts a spec in design delivers.
func partLabel(f *flow) string {
	if f.design {
		if f.header.Parts > 0 {
			return fmt.Sprintf("epic, %d parts", f.header.Parts)
		}
		return ""
	}
	if f.header.Part != "" {
		return "part " + f.header.Part
	}
	return ""
}
```

`internal/web/flow.go`: `flowSummaryJSON` gains `Part string \`json:"part"\`` after `Title`, and `flowSummaryOf` sets `Part: f.Part`.

`internal/web/static/js/flow.mjs`, the board's title (`<p class="sc-flow-title">${sum.title || plan}</p>`) becomes

```js
    <p class="sc-flow-title">${sum.title || plan}${sum.part ? html` <span class="sc-dim">${sum.part}</span>` : null}</p>
```

`internal/web/static/js/flows.mjs`, the live row's title becomes

```js
          <span class="sc-title-text">${l.title}${l.flow.part ? html` <span class="sc-dim">${l.flow.part}</span>` : null}</span>
```

`internal/web/README.md`: where it lists what each session's `flows` carries, name `part` ("part N of M", or "epic, M parts", empty otherwise). Read the README's flow paragraph first and add the field in its style.

Regenerate the record golden and the protocol reference:

```bash
CGO_ENABLED=0 go test ./internal/flowrun -run TestRecord -update
CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update
```

and check `git diff internal/flowrun/testdata/record.golden` shows only the new `- Part: 2 of 3 of …` line, and `git diff docs/specs/protocol.md` only `"Part":""` fields.

- [ ] **Step 4: Run the Go tests**

Run: `loom job run -d "go test" -- sh -c 'CGO_ENABLED=0 go vet ./internal/... ./core/... && CGO_ENABLED=0 go test ./...'`
Expected: `ok` for every package; `gofmt -l` on the changed files prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/flowrun/state.go internal/flowrun/record.go internal/flowrun/record_test.go internal/flowrun/testdata/record.golden internal/cli/flow_run.go internal/cli/flow_run_test.go internal/flowview/plan.go internal/flowview/find.go internal/flowview/find_test.go internal/web/flow.go internal/web/static/js/flow.mjs internal/web/static/js/flows.mjs internal/web/README.md docs/specs/protocol.md
git commit -m "feat(flowrun,flowview,web): the record and the flow view name an epic's part" -- <the same paths>
```

---

## Task 4: The prompts and the docs

**Files:**
- Modify: `flow/plugin/agents/drafter.md`, `flow/plugin/skills/draft/SKILL.md`, `flow/plugin/skills/run/SKILL.md`, `flow/plugin/skills/retro/SKILL.md`, `flow/plugin/docs/dispatch.md`, `flow/plugin/docs/records.md`, `session/loom_context_flow.md`, `flow/README.md`, `flow/PROVENANCE.md`

- [ ] **Step 1: The drafter**

In the question-round bullet: "four lists under these headings" becomes "five lists under these headings"; the block gains, after `## Entry lines` and its line,

```text
      ## Size
      - Story <id>: <n> subtasks: <one line on what they are>; split: <the two entries, when over 7>
```

and after the paragraph defining Entry lines, add: "Size gives, for each of your stories, the subtasks (`### Task` sections) it will need and what they are. A story over 7 subtasks gets a proposed split: two entries, `<id>` and `<id>b`, each with its **Files**, **Interfaces**, **Verify** and **Review** lines and the contract names it owns." The hand-back sentence stays ("each list's count").

- [ ] **Step 2: The draft skill**

In step 2a, "writes the file's four lists" becomes "writes the file's five lists", and "A questions file that isn't the four lists" becomes "that isn't the five lists". In **The wait**, after "amend the entries;", add: "settle each proposed split: accept it by replacing the entry with its two halves and moving each contract owner (`Story <id>`) to the half that defines the name, or reject it with its reason in the story's row of `STATE.md`;". After the wait bullet's last sentence, add: "When accepted splits put the plan over its story budget, `check-plan --skeleton` fails: take it to the owner, who gives a `**Size:**` reason or stops `/flow:draft` to cut the spec into an epic in a design session; never cut one yourself." In **2b**, add: "A drafter whose split was accepted drafts both halves; if it already held two stories, dispatch a fresh drafter with step 2's drafting brief for the new half."

- [ ] **Step 3: The run skill**

Step 5 ("When it returns ended, …") ends "Then tell the owner what was built, what failed, and what needs them." Add: "When the plan has a `**Part:** N of M` line with N below M, end with what comes next: merge this part to main, then start a design session for part N+1 from the spec's `## Delivery` section (the dispatch guide's Epics section)."

- [ ] **Step 4: The retro skill**

Where it says which records to read, add one sentence: "Records whose `- Part:` lines name one spec are the parts of one epic: compare them part by part, and total them with `loom flow costs epic <spec>`." Read the skill first and place it in the step that gathers records.

- [ ] **Step 5: The dispatch guide**

1. `### The skeleton`, the header bullet: after "a `**Parallel:**` line ([parallel pairs](#parallel-pairs));" add ", for a part of an epic a `**Part:** N of M` line ([epics](#epics)), and, for a plan over the story budget, a `**Size:**` line with its reason". After the paragraph on `loom flow check-plan --skeleton`, add: "It also fails a skeleton listing more stories than the profile's `[size] plan` (15 when unset) unless a `**Size:**` line says why it stays one plan, and a `**Part:**` line that disagrees with the spec's `## Delivery` list, or is missing when the spec has one. Story count predicts a plan's size well (r = 0.82 over 51 past plans), and plans over 15 stories mostly ran at $9 to $10 a story against $3 to $4 for the three parts of a split spec."
2. Design-session step 2 gains: "When the spec splits into an epic, it writes only the next part's skeleton ([epics](#epics))."
3. A new section after `### Fixing the plan`:

```markdown
### Epics

A spec too big for one plan is an epic: a `## Delivery` section names its parts, each a plan merged to main before
the next part is designed, so each later part is designed and drafted against the code as it landed.

    ## Delivery

    An epic of 3 parts, each a plan merged to main before the next part is designed.

    1. **Part 1: kernel and ops.** Stages 0 and 1: …
    2. **Part 2: markets, trading and ledger.** Stages 2 to 4: …

Each item is `N. **Part N: <title>.** <scope>`, numbered 1 to M in merge order. The first design session writes the
spec with this section and only part 1's skeleton, with `**Part:** 1 of M` beside its `**Spec:**` line. When a run
ends on part N of M with N below M, the owner merges it and starts part N+1's design session, on a branch cut from
main, which:

1. reads the spec, its Delivery section and the settlements so far;
2. runs a fresh recon at the new main, scoped to part N+1 and to what earlier parts landed: the names, files and
   seams it builds on, and what came out other than the spec said;
3. appends `### Part N+1 settlements (YYYY-MM-DD)` under `## Delivery`: the recon's findings and the decisions made
   with the owner, which hold where they disagree with earlier sections, and any re-cut of the parts not yet started
   (a merged part stays as it landed);
4. writes the skeleton with `**Part:** N+1 of M`, runs `loom flow check-plan --skeleton <plan>` until it passes, and
   commits the spec and the skeleton by pathspec.

A run's record copies the Part line, `loom flow costs design` prices a later part's design session without earlier
parts' sessions, and `loom flow costs epic <spec>` gives each part's design, drafting and run cost.
```

4. The question round's paragraph (`### Drafting the stories`) gains, after the entry lines that can't hold: "and the size of each story: the subtasks it will need, with a proposed split for a story over 7 (the p90 of 526 past stories), which the manager accepts or rejects in its settlement".

- [ ] **Step 6: The records format**

In `flow/plugin/docs/records.md`, after the `- Design stage:` item: "`- Part:` `N of M of <spec path>`, when the plan has a `**Part:**` line: the run is one part of an epic, and `loom flow costs epic <spec>` totals its parts."

- [ ] **Step 7: Loom's flow context, the README and provenance**

`session/loom_context_flow.md`: after "…and commits both by pathspec; it drafts nothing.", add: "When the spec has a `## Delivery` section, it is an epic: the design session writes only the next part's skeleton, and a later part's design session follows the dispatch guide's Epics section."

`flow/README.md`'s tool table: the `costs` row gains "`epic SPEC` (an epic's parts: design, drafting and run cost each)"; the `check-plan` row's `--skeleton` text gains "an epic's Part line and the story budget".

`flow/PROVENANCE.md`, first under `## drafter`: "- 2026-11-11, the question round's file gains a fifth list, `## Size`: each story's subtasks, and a proposed split for one over 7 (`agents/drafter.md`, the draft skill's step 2); no pin changed. On the owner's instruction (`docs/superpowers/specs/2026-11-11-flow-spec-splitting-design.md`), from 526 stories in 51 plans: a story's size can't be predicted from its skeleton entry (r = 0.37 for files named), but the drafter has read the code it needs before it writes."

- [ ] **Step 8: Run the gates**

Run: `loom job run -d "flow lib tests and agent lint" -- bash flow/lib/test.sh`
Run: `loom job run -d "go test" -- sh -c 'CGO_ENABLED=0 go test ./...'`
Run: `loom job run -d "CLAUDE.md lint" -- go run ./tools/claudemd`
Expected: all pass (the drafter's description is unchanged, so its 400-character cap holds).

- [ ] **Step 9: Read through against the spec**

Check the drafter's Size list, the draft skill's settling and the Epics section against spec §5 and §6, and the hand-over against §6.

- [ ] **Step 10: Commit**

```bash
git add flow/plugin/agents/drafter.md flow/plugin/skills/draft/SKILL.md flow/plugin/skills/run/SKILL.md flow/plugin/skills/retro/SKILL.md flow/plugin/docs/dispatch.md flow/plugin/docs/records.md session/loom_context_flow.md flow/README.md flow/PROVENANCE.md
git commit -m "feat(flow): epics in the prompts: the Size list, the part's hand-over and the Epics guide" -- <the same paths>
```

---

## Self-review

- Spec §3 → Task 4 (the Delivery format in the guide) and Task 1 (`delivery_parts`); §4 → Task 1; §5 → Task 4 (drafter, draft skill); §6 → Task 4 (run skill, guide, loom context); §7 → Task 2 (costs) and Task 3 (record, view, PWA) and Task 4 (records.md, retro); §8 → Task 1's and Task 2's failure messages; §9's components each have a task; §10 → each task's tests.
- Names used once each: `PART_RE`, `PART_VALUE_RE`, `part_line`, `plan_part`, `POSITIVE`, `DEFAULT_PLAN_SIZE`, `plan_size`, `SIZE_RE`, `DELIVERY_ITEM_RE`, `delivery_parts`, `part_problems`, `size_problems`, `epic_plans`, `epic_records`, `record_managers`, `record_usd`, `design_window`, `epic_report`, `partLine`, `partRE`, `deliveryRE`, `partLabel`, `Summary.Part`.

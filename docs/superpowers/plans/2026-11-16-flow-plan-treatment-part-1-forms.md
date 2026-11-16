# Flow plan treatment, part 1: the story forms — Implementation Plan

> **For agentic workers:** executed inline by the session that wrote it (owner's choice, 2026-11-16). Four work packages, each one commit; steps use checkbox (`- [ ]`) syntax for tracking. The packages give the behaviour, the names and the tests in full, and code only where its exact form is the decision: the executor holds the spec and the code in context.

**Goal:** Give every supervised-plan story a **Form** (full, mechanical or brief) that the skeleton, the join, `loom flow check-plan`, `loom flow plan-review --select`, the drafter, `/flow:draft` and the run engine's review brief all honour.

**Architecture:** `Form` joins `Review` as an optional opening line, so the existing entry parsing, join and `check-plan` field reading carry it. `check-plan` validates it, exposes it in `--json`, refuses to vouch a non-full story, warns on a mechanical story's fenced code past a cap kept in `flow/lib/estimates.toml`, and fails a non-full story holding a landmine marker its reason doesn't name. `plan-review --select` skips non-full stories and fails one whose drafter lists a risky task its reason doesn't name. The engine reads `form` and adds a move-aware paragraph to a mechanical story's first review brief, and a no-plan-code paragraph to a brief story's.

**Tech Stack:** Python 3 (`flow/lib`, unittest), Go (`internal/flowrun`, testify, golden files), Markdown prompts.

**Spec:** `docs/superpowers/specs/2026-11-16-flow-plan-treatment-design.md` §4 (part 1 of its Delivery).

**Two extensions of the spec, both fail-closed:** the risky-task FAIL of §4.1 covers mechanical stories as well as brief ones (§4.1's last rule says a mechanical story that invents logic should be full), and it and the new landmine FAIL pass when the Form line's reason names each listed task or marker, as a light story's Review reason does for a marker today.

---

## File structure

| File | Change |
|---|---|
| `flow/lib/estimates.toml` | new: `[mechanical] code_cap = 40`, with its reason; part 2 adds the priors |
| `flow/lib/check_plan.py` | `Form` in `OPTIONAL`, `FORM_VALUE_RE`, `Story.form`/`form_reason`/`task_lines`, the value FAIL, the landmine FAIL, vouching only full stories, `form` in `--json` and the text listing, the cap lint |
| `flow/lib/plan_skeleton.py` | an entry's Form value checked |
| `flow/lib/plan_join.py` | docstring: Form is an opening line the join writes |
| `flow/lib/plan_review.py` | `--select`: non-full stories skip; a risky task in a non-full story fails unless named |
| `flow/lib/*_test.py` | the tests below |
| `internal/flowrun/state.go` | `Story.Form` (`json:"form,omitempty"`) |
| `internal/flowrun/briefs.go` | `ReviewBrief` adds the form's paragraph |
| `internal/flowrun/briefs_test.go`, `testdata/*.golden` | two goldens |
| `internal/flowrun/README.md` | Form read at the start, never restoried |
| `flow/plugin/agents/drafter.md`, `skeleton-reviewer.md` | the three forms |
| `flow/plugin/skills/draft/SKILL.md`, `flow/plugin/docs/dispatch.md` | the Form line, the forms, the checks per form |
| `flow/README.md`, `flow/PROVENANCE.md` | rows and dated entries |

## WP1: the Form line in the plan tools

**Files:** `flow/lib/estimates.toml`, `flow/lib/check_plan.py`, `flow/lib/plan_skeleton.py`, `flow/lib/plan_join.py`, `flow/lib/check_plan_test.py`, `flow/lib/plan_skeleton_test.py`, `flow/lib/plan_join_test.py`.

Behaviour:
- `OPTIONAL = ("Review", "Form")`; `FORM_VALUE_RE = re.compile(r"^(full|mechanical|brief)(?:\s*\((.*)\))?$")`. `Story.form` is the value, `full` when the line is missing or doesn't parse; `Story.form_reason` its parenthesised text. `Story.task_lines` holds each `###` task heading's plan line, set in `parse` beside `tasks += 1`.
- `check`: a Form value that doesn't parse is `Story N (line n): **Form:** must be full, mechanical or brief, optionally with a reason in parentheses`. A non-full story whose fenced code holds a landmine marker its Form reason doesn't name: `Story N (line n): mechanical, but its code has <marker> (line m): make it full, or name the marker in the Form line's reason` (brief likewise).
- Vouching: `review_vouches` (line ~320) vouches a story only when it is deep and its form is full. `tier_problems`: a non-full story under `deep:` is `Story N: the Plan review line lists it under deep:, but its Form is mechanical: only a full story is vouched`; under `full:` it passes whatever its tier (a non-full light story stays under `light:`).
- `--json`: each story gains `"form"`. The text listing prints the form after the tier (`deep   full   review: full`).
- The lint: `estimates.toml`'s `[mechanical] code_cap` (read with `tomllib` beside the script, only when the plan has a mechanical story; a missing or bad file is itself a `WARN [lint]` naming it, since a lint never fails a plan) caps each task's fenced lines in a mechanical story; each task over it is `WARN [lint] Story N: Task <id> has <k> fenced lines, over the mechanical cap of <cap>: a mechanical story lists what it moves rather than copying it`. A task's lines run from its heading to the next task's heading or the story's end.
- `plan_skeleton.entry_problems`: a bad Form value fails as in `check`, worded for the entry.
- `plan_join`: no code change; the docstring's list of opening lines adds Form.

Tests (each must fail without its code):
- `check_plan_test`: `test_a_story_without_a_form_line_is_full`, `test_a_form_line_gives_the_form_and_its_reason`, `test_a_bad_form_value_fails`, `test_json_gives_each_storys_form`, `test_a_mechanical_story_is_never_vouched` (a deep mechanical story under `deep:` fails and its `review` is `full`), `test_a_non_full_story_under_full_passes`, `test_a_mechanical_story_holding_a_marker_fails_unless_its_reason_names_it`, `test_a_mechanical_task_over_the_cap_warns_and_one_at_it_does_not`, `test_the_cap_applies_only_to_mechanical_stories`, `test_an_unreadable_estimates_file_warns`.
- `plan_skeleton_test`: `test_an_entry_with_a_bad_form_value_fails`, `test_an_entry_with_a_form_line_passes`.
- `plan_join_test`: `test_the_join_writes_an_entrys_form_line` (the joined story's opening lines hold it, and `check-plan --json` reads it back).

- [ ] Write the tests; run `cd flow/lib && python3 -m unittest -q check_plan_test plan_skeleton_test plan_join_test` and watch the new ones fail.
- [ ] Implement; run the same command until it passes, then `bash flow/lib/test.sh`.
- [ ] Commit: `feat(flow): a story's Form line, full, mechanical or brief, through the skeleton, the join and check-plan` -- the files above.

## WP2: plan-review --select by form

**Files:** `flow/lib/plan_review.py`, `flow/lib/plan_review_test.py`.

Behaviour:
- A non-full story prints `Story N: skip (mechanical)` or `skip (brief)`: no plan review reads it, whatever its markers (WP1's check fails a non-full story holding an unnamed marker).
- A non-full story whose risk file lists tasks, any of which its Form reason doesn't name (`Task 3.2` appearing in the reason), is a failure: exit 1, nothing on stdout, and stderr names each such story and task: `Story 4 is brief but its drafter lists Task 4.2 (risky logic): make it full, or name the task in the Form line's reason`. A missing or unreadable risk file in a non-full story is not a failure (nothing was listed): it skips.
- Full stories are selected as now. The docstring states both rules.

Tests: `test_select_skips_a_mechanical_and_a_brief_story`, `test_select_fails_a_brief_story_whose_drafter_lists_a_risky_task`, `test_select_fails_a_mechanical_story_likewise`, `test_select_passes_a_non_full_story_whose_reason_names_every_listed_task`, `test_select_skips_a_non_full_story_with_no_risk_file`.

- [ ] Write the tests; run `cd flow/lib && python3 -m unittest -q plan_review_test` and watch them fail.
- [ ] Implement; run until it passes, then `bash flow/lib/test.sh`.
- [ ] Commit: `feat(flow): plan-review --select skips mechanical and brief stories, and fails one hiding risky logic` -- the two files.

## WP3: the run engine's review brief by form

**Files:** `internal/flowrun/state.go`, `internal/flowrun/briefs.go`, `internal/flowrun/briefs_test.go`, `internal/flowrun/testdata/review-mechanical.golden`, `internal/flowrun/testdata/review-brief.golden`, `internal/flowrun/README.md`.

Behaviour:
- `Story.Form string \`json:"form,omitempty"\``: empty (a plan or run from before forms) means full. `Restory` doesn't move it, as it doesn't move the tier or review: a form is read at the start.
- `ReviewBrief`, after the Interfaces line, for `mechanical`: "Its Form is mechanical: it moves, renames or rewires code and invents none. Read its diff with `git diff --color-moved=plain --color-moved-ws=allow-indentation-change <base>..<head>`; check that each item its tasks' tables name arrived unchanged except for the paths and visibility the table gives; review only the lines that are not moves." For `brief`: "Its Form is brief: the plan gives its behaviour and its tests, not its code, so loom flow conformance covers none of it. Judge the code against the story's text, the names contract and the spec." Nothing for full or empty, so `review.golden` is unchanged.
- The role choice (engine.go `role := …`) is unchanged: a non-full story is never vouched (WP1), so it gets `code-quality-reviewer`.

Tests: `TestBriefs` (or the file's golden test) gains `review-mechanical.golden` and `review-brief.golden`, generated with the test's `-update` flag once and read; `TestStory_FormDecodesAndDefaults` decodes check-plan JSON with and without `form`.

- [ ] Write the tests; run `loom job run -d "flowrun briefs" -- env CGO_ENABLED=0 go test ./internal/flowrun -run 'TestBriefs|TestStory_Form'` and watch them fail.
- [ ] Implement; generate the goldens, read them, run until it passes, then `gofmt -l` on the changed files.
- [ ] Commit: `feat(flowrun): a mechanical or brief story's first review brief says how to read it` -- the files above.

## WP4: the prompts, the guide and the records of the change

**Files:** `flow/plugin/agents/drafter.md`, `flow/plugin/agents/skeleton-reviewer.md`, `flow/plugin/skills/draft/SKILL.md`, `flow/plugin/docs/dispatch.md`, `flow/README.md`, `flow/PROVENANCE.md`.

- `drafter.md`: under the story format, the three forms: what a mechanical story's tasks hold (the items table: item, path now, new path, new visibility; the grep that lists the uses; the rewrite steps; the gate; fenced code only for glue such as a re-export), what a brief story's tasks hold (behaviour, the contract names, each test by name with what it asserts and its `Fails without:` line, no implementation code), and that the entry's Form line decides which; the Reading section: a mechanical drafter reads the items' signatures and use sites, never their bodies; a brief drafter the spec and the contract. A drafter that finds its mechanical or brief story invents risky logic lists the task in its risk file and reports the Form line as an entry line that can't hold.
- `skeleton-reviewer.md`: an entry whose Form is mechanical or brief but whose paragraph or Interfaces describe logic for store writes, transactions, concurrency, outside calls, bounds or auth is an `ENTRY` finding.
- `draft/SKILL.md`: step 2's briefs need nothing new (the drafter reads its entry's Form); step 4: `--select` skips non-full stories, and its risky-task FAIL goes to the owner: make the story full (amend the entry, re-draft with a fresh drafter) or accept with the task named in the Form reason.
- `dispatch.md`: the opening lines list and the skeleton entry example gain `**Form:**`; a short "Story forms" subsection under the skeleton (the table of §4, the rules of §4.1); the plan review paragraph says non-full stories skip it; the review sequence's supervised paragraph says a mechanical or brief story gets the full review with its form's paragraph.
- `flow/README.md`: `check-plan` and `plan-review` rows mention forms; `estimates.toml` in the lib listing.
- `flow/PROVENANCE.md`: dated entries under drafter (the forms), skeleton-reviewer (the ENTRY finding) and code-quality-reviewer (the form paragraphs in its brief); no pin changed.

- [ ] Edit; run `bash flow/lib/test.sh` (agent lint, description caps) and `loom job run -d "claudemd + flow go tests" -- env CGO_ENABLED=0 go test ./flow/... ./tools/claudemd/...`.
- [ ] Commit: `docs(flow): the story forms in the drafter, the draft skill and the dispatch guide` -- the files above.

## Final verification

- [ ] `bash flow/lib/test.sh`; `loom job run -- env CGO_ENABLED=0 go test ./internal/flowrun/... ./internal/cli/... ./flow/... ./tools/claudemd/...`; `CGO_ENABLED=0 go vet ./internal/flowrun/...`; `gofmt -l` on the changed Go files. golangci-lint is skipped (the dev shell's v2 rejects the repo's config).
- [ ] Run `loom flow check-plan --skeleton` and `loom flow check-plan --json` on a scratch skeleton and plan outside the worktree with one story of each form, and read the output.

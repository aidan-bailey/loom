# Auditing what the docs claim

**Symptoms:** a rule names a test that still exists but no longer asserts what the rule says; a guide's checklist sends you to a function that moved; a count ("three cases", "every event but two") is off by one; a doc says CI runs something it doesn't. Nothing failed, because `tools/claudemd` checks structure and the names it can resolve, not truth.

**Cause.** The `CLAUDE.md` files, READMEs, `docs/ARCHITECTURE.md` and these guides describe code that keeps moving. The lint fails on a missing path, test name or file-and-symbol reference, and only reports other symbols it can't find; a rule whose names all still resolve can be wrong in substance.

**Rule:** after a big refactor, a deletion, a retired concept or a long merge, and on a schedule, run this audit and fix what it finds in the same branch.

## Procedure

1. **Run the lint with the symbol report:** `go run ./tools/claudemd -idents > <scratchpad>/idents.txt`, from the repo root. Structural problems come first; `advisory:` lines are symbols found in no source file.
2. **Triage each advisory:**
   - **ELSEWHERE**: the symbol exists, but outside the package the doc names. It moved: fix the doc's package or path.
   - **Missing**: renamed or deleted. Find what happened with `git log -S'<symbol>' --oneline`, then rename it in the doc, or delete or rewrite the rule if the concept is gone.
   - **Negative claim**: the doc says the thing must not exist, or no longer does. Add it to `tools/claudemd/idents.allow` with a `# reason`; anything else there hides a stale rule.
3. **Re-check every Enforced claim you touched, and a sample of the rest.** The named test must exist (the lint checks that) and still assert what the rule says (it doesn't): open it. Check it runs where the rule implies: `e2e` runs only under `-tags e2e` and in no CI job, the real-Claude contract tests only with `LOOM_TEST_REAL_CLAUDE=1`, and `tools/` and `e2e/` are not in the Nix build.
4. **Re-count every countable and check the ends of every range.** A list ("the state events are …", "`1`–`5` select a panel tab") is a claim about both its members and its completeness: grep for the set's source (`core.EventTypes()`, the `switch` in question) and compare both ways. Prefer rewriting a count as names.
5. **Check what the docs say about CI** against `.github/workflows/`: which workflows exist, their path filters, their matrices, and which tool versions they pin. Docs should name the file that pins a version rather than restate it.
6. **For a retired concept,** grep every `CLAUDE.md`, README, guide, `USAGE.md` and `docs/specs/` for its names, including the plain-prose ones the lint can't see.
7. **When you moved text between docs,** compare backticked tokens before and after, so no fact is dropped on the way: collect each file set's code spans with `grep -o`, `sort -u` each list, and run `comm -23` on the old list against the new; every leftover is restored or deliberately dropped.

## Traps

- **Write sweeps as bash scripts in the scratchpad**, run with `bash`. zsh doesn't word-split an unquoted `$var`, so `for f in $files` loops once, and `$rev:app/…` triggers zsh's `:a` modifier (write `${rev}:path`).
- **A hook compresses git and grep output** in this environment (`rtk`): a diff can look empty or a match list short. Prefix raw commands with `rtk proxy` (`rtk proxy git show …`, `rtk proxy grep …`).
- **Batch large outputs:** write them to a file in the scratchpad and read slices, rather than printing thousands of lines into the session.
- **Keep the scratchpad outside the worktree**: loom intent-to-adds every file there ([`testing-pitfalls.md`](testing-pitfalls.md)).
- **The symbol report skips short and spaced tokens** (under four characters, or holding whitespace or most punctuation), and a test family written with ASCII dots (`TestX_...`) is never checked; `tools/CLAUDE.md` lists the lint's blind spots.

## When

- After a refactor that renames or moves packages, files or tests.
- After deleting a package or retiring a concept.
- After merging a long-lived branch, or one whose docs changed alongside `main`'s.
- On a schedule: monthly is a reasonable default.

## What the gates won't tell you

Whether a rule is still true. The lint proves names resolve; only reading the code, and the test the rule cites, proves the rule.

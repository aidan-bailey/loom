# Adding an enforce test: when a written rule is broken again

**Symptom:** a rule a CLAUDE.md states was broken again: a raw tmux call followed `$TMUX` to the user's server, a test seam ran in production, the TUI grew a reference to a model object.

**Cause:** a written rule reaches only the session that loads its file and reads it. A test runs for everyone, in `go test ./...`, in CI and in the Nix build's check phase.

**Rule:** when a rule is broken a second time, turn it into a test, and mark the rule **Enforced** by that test in its CLAUDE.md.

## Pick the kind of check

| The rule says | Check | Model |
|---|---|---|
| "never call X outside Y" | A go/parser scan of every production file | `TestNoRawGitGhExec` (`internal/exec/command_enforce_test.go`), `TestTmuxTargetsAreExact` (`session/tmux/target_enforce_test.go`), `TestNoProductionCallsOfTestSeams` (`internal/testenv/seams_enforce_test.go`) |
| "these dirs never name those objects" | The same scan, over chosen dirs, resolving each file's import names | `TestTUIHoldsNoModelObject` (`internal/testenv/instance_enforce_test.go`) |
| "package A never reaches B" | An import-graph walk | `TestCoreImportsNoUI` (`core/boundary_test.go`), `TestEveryConfigReachingPackageIsolatesLoomDirs` (`internal/testenv/testenv_test.go`) |
| "these types stay plain data" | Reflection over a production list of types | `TestCoreIsValueTyped` (`core/value_boundary_test.go`) |
| "list L covers every X" | Completeness against the source or a production table | `TestAllEventsListsEveryEvent`, `TestMethods_CoverCore`, `TestForConn_CoversEveryEventNamingARequest` |
| "file F matches its generator" | Regenerate and compare bytes | `TestGenerated_IsFresh`, `TestProtocolReference` |

A rule with no static shape (a `Job` reads no model state; nothing on the loop waits on the TUI) can't be scanned for: its guards are `-race` and review.

## Procedure for a source scan

1. **Place it** in the package that owns the rule, as `<topic>_enforce_test.go`, or in `internal/testenv` when the rule spans packages.
2. **Find the root by depth.** `filepath.Abs(filepath.Join("..", ".."))` from a package two levels down, `".."` from one (as `core/boundary_test.go` does), then `require.FileExists(t, filepath.Join(root, "go.mod"))`, so a wrong depth fails loudly instead of scanning nothing.
3. **Walk** with `filepath.WalkDir`, skipping `vendor/` and every dot-dir: `.git`, and `.loom`, whose worktrees are other checkouts of this repo, so scanning them flags other branches' code.
4. **Decide about tests deliberately.** The existing scans exempt `_test.go` files and say why in their doc comment: the hazard is the shipped binary, and test code builds fixtures.
5. **Parse** each file with `parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)`. go/parser ignores build tags, so `_windows.go` files are checked too, which `go list` would skip.
6. **Resolve the import name per file.** An aliased import (`osexec "os/exec"`) or a dot import calls the same package under another name, and `_` binds none: `internal/exec/command_enforce_test.go:rawGitGhExecs` is the model.
7. **Keep the matcher a function of one `*ast.File`** that returns positions, so a detection test can call it on source held in memory.
8. **Report positions and the fix.** Collect `fset.Position(pos).String()` per offender, and make the assertion message name the helper to use and its file ("build git/gh subprocesses with internalexec.GitCommand / GhCommand (internal/exec/command.go)").
9. **Allow-list sparingly.** Key each exemption `"<file>:<func>"` with a reason, and fail on an entry that no longer matches anything (`session/tmux/target_enforce_test.go:exactTargetAllowed`), so an exemption can't outlive its code.
10. **Pin the matcher with a detection table**: sources it must flag and sources it must not, parsed with `parser.ParseFile(token.NewFileSet(), "x.go", "package x; "+src, 0)`. `TestRawGitGhExecs_Detection` is the model for a source scan; for a reflection check, `TestPlainProblem_Bites` (`core/value_boundary_test.go`) is the equivalent, a table of values the rule must reject. Without one, a refactor that stops matching (a wrong alias lookup, say) turns the enforcer into a test that can never fail.
11. **Say what it can't see** in its doc comment, as `TestNoRawGitGhExec` does: only a literal program name is detected.
12. **Mark the rule** in its CLAUDE.md: **Enforced** by the new test, with the test's file.

## Traps

- **Fixtures exist only at runtime**: source strings in the test, or files under `t.TempDir()`. None of the walkers skip `testdata/`, so a committed fixture holding a violation fails every other enforcer, and tools/claudemd's identifier index reads it too.
- **Proving it bites against the real tree.** Never plant a violation in a loom worktree: loom runs `git add -N .` there on every diff probe, so even a deleted scratch file leaves an index entry. Copy the working tree, your uncommitted test included, to a scratch dir instead (`rsync -a --exclude .git --exclude .loom ./ "$S/copy/"`; vendor/ is committed, so the copy builds offline), plant the violation there and run the test in the copy.
- **`go test -overlay` is invisible**: the scan reads files from disk.
- **No type information.** A scan matches names: a same-named function elsewhere is a false positive (tighten the matcher, or exempt it with a reason), and a value routed through a variable or a constant is a false negative.

## What the gates won't tell you

- Only `TestRawGitGhExecs_Detection` and `TestPlainProblem_Bites` pin their matchers. `TestNoRawTmuxExec` (`session/tmux/command_enforce_test.go`) matches only the identifier `exec` and the interpreted literal `"tmux"`, so an aliased `os/exec` import or a raw-string literal passes it, and no detection table would notice it stop matching (found 2026-10-09).
- An enforcer covers the tree as committed, not the reasoning behind the rule: a new helper that does the forbidden thing under another name passes until someone adds it to the matcher.

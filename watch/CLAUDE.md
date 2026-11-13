# watch

The issue watcher's logic: the `loom-watch.json` config, the title template and the branch pattern derived from it, and prompt rendering. It holds no model state: `core` imports it and calls it from the loop and from a poll job, so it imports nothing from `core`. Facts in [`README.md`](README.md).

## Rules when modifying this package

- **Keep `maxTitleWidth` equal to `core.MaxTitleWidth` (32 cells).** `watch` can't import `core` (core imports it), and a title wider than core accepts is refused at the start of every session, so the issue never gets one and the status says only that the start failed. **Enforced** by `TestMaxTitleWidth_MatchesCore`.
- **Derive the branch pattern from `git.SanitizeBranchName` itself (`TitleTemplate.BranchPattern`), never from a copy of its rules.** The watcher recognises its own PRs by that pattern, and one that drifts from the sanitizer stops matching them: their issues get a second session and the PRs are never nudged or settled. **Enforced** by `TestTitleTemplate_RoundTrip`.

## Pointers

- [`README.md`](README.md) — the files and what each holds.
- [`../docs/superpowers/specs/2026-11-13-issue-watcher-design.md`](../docs/superpowers/specs/2026-11-13-issue-watcher-design.md) — the design the package implements.

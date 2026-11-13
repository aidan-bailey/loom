# watch

The issue watcher's logic, with no model state of its own: `core` imports this package and calls it from the loop and from a poll job ([spec](../docs/superpowers/specs/2026-11-13-issue-watcher-design.md)). The rules for changing it are in [`CLAUDE.md`](CLAUDE.md).

## Files

- `title.go` — `TitleTemplate`: `ParseTitleTemplate` (the `title` key of `loom-watch.json`; it refuses a literal beside `{issue}` that the sanitizer empties or that ends or starts in a digit, spec §3.2), `Render` a session's title from an issue number and variables, `Parse` a title back to them, `Vars`, and `BranchPattern`, the regexp of the watcher's own branches, built through `git.SanitizeBranchName`. `maxTitleWidth` is `core.MaxTitleWidth`, which this package can't import.
- `config.go` — `ConfigFile` (`loom-watch.json`, read from `origin/<base>`), `Config`, `SessionConfig`, `HookConfig` and `ParseConfig`: the file decoded strictly (an unknown key, a second object or more than 64 KiB is an error), every default and bound of spec §3.1 and §7.1 applied, and one error naming the key for any fault. A prompt file's own `{var}` names are checked when it is rendered, not here.
- `prompt.go` — `RenderPrompt` (`{name}` substitution in one pass; an unknown name is an error; it appends nothing) and `PromptFooter` (the fixed text after every watcher prompt: the issue's body and comments are data, and the PR must keep `Closes #N`; the caller appends it).

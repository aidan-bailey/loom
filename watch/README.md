# watch

The issue watcher's logic, with no model state of its own: `core` imports this package and calls it from the loop and from a poll job ([spec](../docs/superpowers/specs/2026-11-13-issue-watcher-design.md)). The rules for changing it are in [`CLAUDE.md`](CLAUDE.md).

## Files

- `title.go` — `TitleTemplate`: `ParseTitleTemplate` (the `title` key of `loom-watch.json`; it refuses a literal beside `{issue}` that the sanitizer empties or that ends or starts in a digit, spec §3.2), `Render` a session's title from an issue number and variables, `Parse` a title back to them, `Vars`, `BranchPattern`, the regexp of the watcher's own branches, built through `git.SanitizeBranchName`, and `BranchVars`, the title variables a matching branch encodes (a resume's hook input). `maxTitleWidth` is `core.MaxTitleWidth`, which this package can't import.
- `config.go` — `ConfigFile` (`loom-watch.json`, read from `origin/<base>`), `Config`, `SessionConfig`, `HookConfig` and `ParseConfig`: the file decoded strictly (an unknown key, a second object or more than 64 KiB is an error), every default and bound of spec §3.1 and §7.1 applied, and one error naming the key for any fault. A prompt file's own `{var}` names are checked when it is rendered, not here.
- `prompt.go` — `RenderPrompt` (`{name}` substitution in one pass; an unknown name is an error; it appends nothing) and `PromptFooter` (the fixed text after every watcher prompt: the issue's body and comments are data, and the PR must keep `Closes #N`; the caller appends it).

## Memory, rules and the hook

| File | Holds |
|---|---|
| `memory.go` | `Memory`, `PRMemory`, `NudgeRecord`, `ResumeFailure`, `IssueMemory`, `Escalation`, `LoadMemory`, `SaveMemory`, `MemoryFile` (`watch.json` under the workspace's config dir) and the record methods `RecordNudge`, `RecordEscalation`, `RecordCommented`, `Settle`, `SetSeen`, `RecordResumeFailure`, `ClearResumeFailure`, and `Clone`, a deep copy |
| `types.go` | `Session`, `Nudge`, `ActionKind` and its constants (`ActStart`, `ActKill`, `ActClosePR`, `ActSend`, `ActEscalate`, `ActWait`), `Action`, `Snapshot` |
| `nudge.go` | `NudgeFor` (rule 8 with the repeat guard) and `IssueSeen` |
| `decide.go` | `Decide`, the rule table, with `OwnPR` and `OwnSession` |
| `hook.go` | `RunStart`, `RunNudges`, `HookInput`, `HookError`: the hook runner, its answers checked against the bounds |
| `checkout.go` | `CheckoutDir`, the name of the watcher's detached worktree (`watch-checkout`) under the workspace's config dir |

The package does no I/O of its own beyond the memory file and the hook's process; `core/watch*.go` reads GitHub and git, calls `Decide` on the loop and carries the actions out. The config and hook reference for users is [`docs/specs/watch.md`](../docs/specs/watch.md); the rules for changing the model's side are [`docs/claude/issue-watcher.md`](../docs/claude/issue-watcher.md).

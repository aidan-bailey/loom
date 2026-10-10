# config

Loom's on-disk configuration, app state, profile catalog and workspace registry, and the resolution of the directories that hold them.

## Directories

- The config dir defaults to `~/.loom`; `LOOM_HOME` overrides it (absolute, `~` expanded), with `CLAUDE_SQUAD_HOME` still honoured with a deprecation warning. A registered workspace keeps its own config dir at `<repo>/.loom/` (`WorkspaceConfigDir`, `EnsureGitignore`).
- `GetGlobalConfigDir` returns `LOOM_GLOBAL_DIR` when set, else `~/.loom`, regardless of `LOOM_HOME`: it holds `workspaces.json`, the global workspace's context (`GlobalWorkspaceContext()`), the account registry and the daemon's lock, socket fallback and logs.
- `WorkspaceContext` carries the resolved config dir for one workspace through the code, instead of globals (`ResolveWorkspace`, `WorkspaceContextFor`).
- `LoadConfigFrom("")`/`LoadStateFrom("")` accept an empty string as "use the default directory".
- `config/migration.go:MigrateLegacyHome` handles the one-time `~/.claude-squad` → `~/.loom` rename.

## Files

- `settings.go` — `Settings`, the plain struct of every persisted field and its getters, and `Clone`, `FromSettings`, `Snapshot`, `ReplaceSettings`.
- `config.go` — `Config` (embeds `Settings` beside the mutex that guards a shared `Config`; JSON flattens the embedded struct), `Mutate`, the locked getters, `DefaultConfig`, `LoadConfigFrom`, `SaveConfigTo` (marshals under the read lock), and the cycle lists `ClaudePermissionModes`, `ClaudeModels` (`ClaudeModel`: an alias and its `Supports1M`), `ClaudeEfforts`.
- `state.go` — `State` (with its own mutex), `UIPrefs`, `LoadStateFrom`, `SaveStateTo`, and the interfaces `InstanceStorage`, `AppState`, `StateManager`. A save is skipped when nothing changed.
- `workspace.go` — `WorkspaceRegistry` (no mutex: its writes go through the model), `WorkspaceContext`, `GetGlobalConfigDir`.
- `atomic.go`, `quarantine.go` — `AtomicWriteFile` (temp file and rename) and the quarantine that moves a file that fails to parse aside rather than overwrite it.

## `config.json`

User configuration, written only by the model (`core.Model.SaveSettings`; the settings overlay edits a copy). The getters are the truth for defaults:

- `DefaultProgram` (`GetProgram`); `Profiles`, named program presets (`GetProfiles`).
- `BranchPrefix` (default `{username}/`; `GetBranchPrefix`). Overridable per session from the Session Launch Options modal: `launch.Options.BranchPrefix` → `Instance.SetBranchPrefix` → `git.WorktreeSpec.BranchPrefix`, an ephemeral override that is never persisted.
- `BaseBranch` (default empty, `Config.GetBaseBranch()`): the branch new worktrees are cut from; empty auto-detects via `git.ResolveBaseCommit` (`origin/HEAD` → `main` → `master` → current `HEAD`).
- `Theme` (`"afterglow"` default, `"legacy"`; empty means default, `Config.GetTheme()`), cycled live from the settings overlay's Theme row.
- `ClaudeTmpArchiveDir` (`claude_tmp_archive_dir`), read from the global `config.json` only: empty keeps each workspace's Claude temp-dir archives in its own config folder; set, every workspace's go under it in a subfolder per config dir (`session.ClaudeTmpArchiveDir`). Read via `Config.ClaudeTmpArchiveRoot()`, which expands `~` and rejects a relative path.
- `ClaudeRemoteControl` (`*bool`, default on): launches Claude sessions with `--remote-control <title>`; nil is enabled (`Config.RemoteControlEnabled()`).
- `ClaudePermissionMode` (`*string`, default `"default"`): launches Claude sessions with `--permission-mode <mode>`; nil is `"default"`, which injects no flag (`Config.PermissionMode()`). Valid values are `config.ClaudePermissionModes`, cycled from the Claude Preferences overlay.
- `Claude1MContext` (`*bool`, default off): appends Claude's `[1m]` long-context suffix to the launched `--model` alias, e.g. `sonnet[1m]`; a no-op for `default`, `haiku` and any alias whose `config.ClaudeModel.Supports1M` is false (`Config.Context1MEnabled()`). The composed value is single-quoted, because tmux runs the program string through a shell and `[`/`]` are zsh glob metacharacters.
- `ClaudeSubagentTracking` (`*bool`, default on): shows the subagents and teammates loom's hooks track; every Claude launch gets the hooks regardless. Nil is enabled (`Config.SubagentTrackingEnabled()`), toggled from the Claude Preferences overlay's Track Subagents row.
- `ClaudeLoomContext` (`LoomContextEnabled`), `HeadroomProxy` (`HeadroomProxyEnabled`), `ClaudeModel` (`Model`), `ClaudeEffort` (`Effort`) and `CacheTTL1h` (`CacheTTL1hEnabled`): the other Claude launch defaults the Session Launch Options modal starts from.

## `state.json`

App state, e.g. the help screens seen (`HelpScreensSeen`), the serialized `instances` array (owned by `session`'s storage), and the `ui` prefs block (`config.UIPrefs`): `view_mode`, `rail_hidden`, `terminal_hidden`, `split_ratios` (a per-session-title map of agent/terminal split ratios, written throttled on `ctrl+up`/`ctrl+down` resizes) and `workbench_ratios` (the workbench's agent/panel split, per session).

## `workspaces.json`

In the global dir: the registered workspaces (`name`, `path`, `added_at`), `last_used`, and the open list (`open_workspaces`). Every write rereads the file first, so another process's change survives.

## Tests

Unlike every other package's, this package's `TestMain` (`config/config_test.go`) leaves the loom dir variables unset and points `HOME` at a throwaway, because the tests exercise default resolution itself.

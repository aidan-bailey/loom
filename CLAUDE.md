# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Loom is a terminal UI (TUI) for managing multiple AI coding agents (Claude Code, Aider, Codex, Amp) in parallel. Each agent runs in an isolated git worktree with its own tmux session. Built with Go using the Charmbracelet Bubble Tea framework.

Loom was forked from [smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad) at v1.0.17 (April 2026) and has diverged substantially since — see [NOTICE.md](NOTICE.md).

## Build & Development Commands

```bash
# Build
CGO_ENABLED=0 go build -o loom

# Build & run via Nix (no dev shell needed)
nix run .

# Run tests
go test -v ./...

# Run a single package's tests
go test -v ./config
go test -v ./session/git

# Race detector — the default CGO_ENABLED=0 build disables it, so enable CGO
# (needs a C compiler; use CC=clang if gcc is absent). CI runs this as a job.
CGO_ENABLED=1 go test -race ./...

# Format code (CI enforces this)
gofmt -w .

# Lint (CI uses golangci-lint v1.60.1)
golangci-lint run --timeout=3m --fast

# Cleanup scripts
./clean.sh        # Kill tmux server, remove worktrees and ~/.loom/
./clean_hard.sh   # Same as clean.sh + git worktree prune

# Install (adds ~/.local/bin to PATH)
./install.sh
```

CGO is disabled for builds (`CGO_ENABLED=0`). Go version is 1.23.0 (toolchain go1.24.1).

A Nix flake (`flake.nix`) provides a dev shell with Go, golangci-lint, tmux, git, and gh.

## CLI Usage

```bash
# Run with default settings
loom

# Specify agent program
loom --program "aider --model ollama_chat/gemma3:1b"

# Subcommands
loom reset    # Reset all instances, cleanup tmux sessions and worktrees
loom debug    # Print config paths and debug info
loom version  # Print version

# Workspace management
loom workspace add [path]    # Register a git repo as a workspace
loom workspace list          # List registered workspaces
loom workspace remove <name> # Unregister a workspace
loom workspace use <name>    # Set default workspace
loom workspace rename <old> <new>  # Rename a workspace
loom workspace status [name] # Show instance counts
loom workspace migrate       # Migrate instances to workspaces

# Select workspace explicitly
loom --workspace <name>
```

## TUI Keybindings

| Key | Action |
|-----|--------|
| `n` | New instance |
| `N` | New instance with prompt |
| `i` | Interact with the focused pane (inline attach to agent) |
| `ctrl+a` | Interact with agent pane (inline attach) |
| `ctrl+t` | Interact with terminal pane (inline attach) |
| `alt+a` | Full-screen attach (agent pane) |
| `alt+t` | Full-screen attach (terminal pane) |
| `ctrl+q` / double-`esc` | Detach/exit interact (inline attach) |
| `r` | Resume paused instance |
| `D` | Kill instance |
| `p` | Push branch |
| `c` | Checkout branch |
| `m` | Merge another session's branch into the current one |
| `a` | Quick input bar (send to agent) |
| `t` | Quick input bar (send to terminal) |
| `d` | Toggle diff overlay |
| `up`/`k`, `down`/`j` | Navigate sessions |
| `W` | Workspace picker |
| `S` | Open settings |
| `l`/`[`, `;`/`]` | Previous/next workspace tab |
| `?` | Help |
| `q` | Quit |

## Environment Variables

- `LOOM_HOME` — Override config directory (default: `~/.loom`). Must be absolute path; supports `~` expansion. Used as a backward-compatible fallback; internal code uses explicit `WorkspaceContext` threading.
- `LOOM_LOG_FORMAT` — Set to `json` to emit structured log records from `log.InfoKV/WarnKV/ErrorKV` as JSON lines; otherwise plain text. Legacy `log.Infof`/`Warnf`/`Errorf` callers are unaffected.
- `LOOM_LOG_LEVEL` — `debug|info|warn|error` (default `info`). Gates both the Structured logger and the legacy `InfoLog`/`WarningLog`/`ErrorLog` writers (legacy records below the gate are dropped at the writer layer). The `--log-level` CLI flag (persistent on all subcommands) takes precedence over the env var.
- `LOOM_PANE_RENDERER` — Set to `snapshot` to disable the embedded VT emulator and fall back to the legacy `tmux capture-pane` snapshot path for pane rendering (also the implicit path on Windows). Unset (default) renders panes from the emulator, enabling live-scroll and mouse forwarding.

Legacy fallbacks (`CLAUDE_SQUAD_HOME`, `CLAUDE_SQUAD_LOG_FORMAT`, `CLAUDE_SQUAD_LOG_LEVEL`) are still honored with a one-time deprecation warning to stderr; remove them from your shell init once you've migrated.

## Migration from claude-squad

On first launch, Loom renames `~/.claude-squad/` → `~/.loom/` atomically so in-flight instances, worktrees, and user scripts continue to work. Live tmux sessions with the legacy `claudesquad_` prefix are renamed to `loom_` before reconcile runs, so running agents keep their panes. The orphan sweep in `session/reconcile.go` recognizes both prefixes to clean up stragglers.

Auto-commit tags flipped from `[claudesquad]` → `[loom]` at the v0.1.0 cutover. Historic worktree commits retain the old tag — that is expected and not rewritten.

## Debugging

- Log file: `{configDir}/logs/loom.log` (rotated once to `.log.1` at startup when >5 MB). Run `loom debug` to print the exact path plus the effective log level and format.
- To enable verbose output, set `LOOM_LOG_LEVEL=debug` or pass `--log-level=debug`. Debug logs are routed exclusively through the Structured logger (`log.Debugf` / `log.DebugKV`); they never appear via the legacy `*log.Logger` vars.
- New code should prefer `log.For("subsystem", ...)` to get a pre-tagged `*slog.Logger`, or call `log.InfoKV/WarnKV/ErrorKV/DebugKV` directly. The resulting records carry `subsystem=...` so a single `grep subsystem=tmux loom.log` scopes output to one component.

## Documentation

- [USAGE.md](USAGE.md) — comprehensive TUI guide and CLI reference
- [CONTRIBUTING.md](CONTRIBUTING.md) — contribution guidelines
- [NOTICE.md](NOTICE.md) — fork attribution and AGPL §5 notice
- [docs/specs/workspaces.md](docs/specs/workspaces.md) — workspace registration, isolation via `WorkspaceContext`, switching, and migration
- [docs/specs/scripting.md](docs/specs/scripting.md) — Lua scripting sandbox, dispatch flow, and `cs`/`ctx`/`instance`/`worktree` API reference

## Architecture

### Core Flow

`main.go` (Cobra CLI) → `app/app.go` (Bubble Tea Model) → manages `session/instance.go` instances

The app follows Bubble Tea's Model-View-Update pattern. `app/app.go` owns the `home` model and its `Update`/`View`. Keyboard input is routed in two stages: `handleKeyPress` (`app.go`) dispatches by `m.state` to a per-state handler in `app/state_*.go`; within the default state, keys flow through the Lua engine via `app/app_scripts.go:dispatchScript`, which consults `script.Engine.HasAction` and returns a `tea.Cmd` that drains the resulting `scriptDoneMsg`. The canonical keymap lives in `script/defaults.lua` (embedded at build time); user scripts in `~/.loom/scripts/*.lua` can rebind or add keys. On startup, the app detects the current workspace or prompts the user to select one via the workspace picker overlay.

### Key Packages

- **`app/`** — Bubble Tea application model. Handles all keyboard input dispatch, instance lifecycle management, and UI composition. This is the "controller" layer.
- **`session/`** — Core domain. `Instance` represents a running agent session with status lifecycle (Ready → Loading → Running → Paused). `storage.go` handles JSON serialization to `~/.loom/instances.json` and retains raw `InstanceData` for records that fail `ReconcileAndRestore` (the unrecovered cache) so a transient failure does not silently drop the entry on the next save. `orphan.go` discovers worktree directories on disk not referenced in state.json and classifies them (`Disposition`): stale leftovers (dead tmux + clean) are auto-cleaned, ones with unsaved work or a live agent surface inline as `Recoverable` session entries. `reconcileOrphans` (`app/app.go`, run on every workspace activation) drives this — there is no blocking recovery modal.
- **`session/agent/`** — `Adapter` interface and per-program implementations (claude, aider, gemini, default fallback). Centralizes trust-prompt keys, recovery flags, and `Supports(program)` checks. Look here when adding a new agent program rather than touching `tmux.go` or `agent_restart.go` directly.
- **`session/git/`** — Git worktree operations. Each session gets an isolated worktree in `~/.loom/worktrees/`. Branches are named `{username}/{session_title}`. Handles setup, diff stats, push, and cleanup.
- **`session/tmux/`** — Tmux session management. Creates/attaches terminal sessions, captures pane content, detects prompts (surfaces a `Prompting` status so the user knows an instance needs attention), sends keystrokes. Also pumps `ptmx` output into an embedded VT emulator (`emulator_unix.go`/`emulator_windows.go`, gated by `LOOM_PANE_RENDERER`) so panes render from the emulator with a capture-pane fallback; `RenderWindow`/`ScrollbackLen` back live-scroll, and `ForwardMouse`/bracketed paste back interact mode. `TmuxSession.stateMu` guards the `ptmx`/`monitor`/emulator fields against the metadata-fan-out vs attach-lifecycle race. Prefix is `loom_`; `LegacyTmuxPrefix` (`claudesquad_`) is still recognized by the orphan sweep and the startup rename pass.
- **`session/vt/`** — Embedded terminal emulator backing pane display. `vt.go` defines the `Emulator` interface; `xvt.go` is the `charm.land/x/vt`-backed implementation. Decouples on-screen rendering and scrollback from `tmux capture-pane` so panes can live-scroll and forward mouse/paste input.
- **`session/files/`** — Stateless filesystem enumeration helpers backing the file-explorer overlay. Separate from `session/git/` because callers may operate on non-git roots (workspace terminals pointed at bare directories).
- **`config/`** — Configuration (`config.json`), state (`state.json`), profiles, and workspace registry (`workspace.go`). Key types: `WorkspaceContext` (carries resolved config dir through the app), `InstanceStorage`, `AppState`, `StateManager`. `LoadConfigFrom("")`/`LoadStateFrom("")` accept empty string as "use default directory". `config/migration.go:MigrateLegacyHome` handles the one-time `~/.claude-squad` → `~/.loom` rename.
- **`ui/`** — Bubble Tea view components. Left panel (`list.go`, 20% width), right panel (`split_pane.go`, 80% width) with agent and terminal panes stacked vertically (70/30 split) and a hotkey-toggled diff overlay. `terminal.go` renders a pane from the embedded VT emulator (with live-scroll offset, jump-to-bottom footer, and mouse drag-select/copy). `quick_input.go` provides an inline input bar for sending text to tmux. `workspace_tab_bar.go` renders workspace tabs. `ui/overlay/` has modal dialogs (text input, confirmation, branch picker, profile picker, workspace picker, file explorer).
- **`keys/`** — Keybinding definitions. Enum-based `KeyName` with global maps for lookup.
- **`cmd/`** — `Executor` interface wrapping `os/exec` for testability.
- **`log/`** — Centralized logging to `{configDir}/logs/loom.log` with Info/Warning/Error loggers and rate limiting.
- **`script/`** — Lua scripting engine (`github.com/yuin/gopher-lua`). The full built-in keymap lives in `script/defaults.lua`, embedded via `go:embed` and loaded at engine init before any user script. Users extend or override bindings from `~/.loom/scripts/*.lua` (global, not per-workspace). Dispatch is driven from `state_default.go` through `app/app_scripts.go`'s `scriptHost` adapter. Hard-sandboxed: only `base`/`string`/`table`/`math`/`coroutine`; `dofile`/`loadfile`/`load`/`loadstring`/`require`/`string.dump`/`collectgarbage` stripped. Exposed API: `cs.bind`/`cs.unbind`/`cs.register_action`, `cs.actions.*` (sync primitives + deferred intent factories), `cs.await`, `cs.log`, `cs.notify`, `cs.now`, `cs.sprintf`, plus userdata wrappers for `session.Instance`, `git.GitWorktree`, and a per-dispatch `ctx`.
- **`web/`** — Next.js marketing site (no CI deployment; build locally with `cd web && npm run build`).

### Session Lifecycle

Statuses: `Ready` (initial), `Loading` (setup in progress), `Running` (agent active), `Paused` (worktree removed, branch preserved), `Recoverable` (an orphaned worktree found on disk, surfaced inline for recover/discard; never persisted).

1. **New**: User presses `n`/`N` → overlay collects title and optional prompt → status: Ready
2. **Start**: Creates git worktree + tmux session, records base commit → status: Loading → Running
3. **Running**: Agent works in isolated worktree; UI shows live terminal output + diff stats
4. **Pause**: Commits changes, kills tmux session, removes worktree (branch preserved) → status: Paused
5. **Resume**: Recreates worktree from branch, starts new tmux session → status: Running
6. **Kill**: Cleans up worktree, tmux session, and branch; instance removed from storage

**Workspace Terminals**: A special instance type (`IsWorkspaceTerminal: true`) that runs directly in the root repo without a worktree. Cannot be paused/resumed. Diff tracking shows uncommitted changes in the root repo.

### Gotchas

- **Instance data schema changes.** `session.InstanceData` has a `SchemaVersion` field and `session.CurrentSchemaVersion` constant. When adding/removing/renaming fields: bump `CurrentSchemaVersion`, add an upgrade step to the switch in `session/storage_migrate.go:Migrate`, and update the JSON fixture in `cmd/workspace_migrate_shape_test.go` (drift guard for the `workspace migrate` CLI's typed mirror struct).
- **`FromInstanceData` is decoupled from PTY attach.** It's a pure constructor — it does not spawn a tmux session. Callers that need a live PTY must call `inst.EnsureRunning()` explicitly (see `session/reconcile.go`).
- **Inline orphan recovery (no modal).** `reconcileOrphans` (`app/app.go`, called from `activateWorkspace` and classic startup — so it runs on every workspace-load path) auto-cleans stale worktrees and surfaces unsaved/live ones as `Recoverable` list entries. `Recoverable` is **ephemeral**: filtered out of `persistableInstances`, re-derived from disk each load, and inert (`EnsureRunning` no-ops on it). `r` recovers (adopt via `ReconcileAndRestore`), `D` discards (worktree removed, branch kept via `IsExistingBranch`). When adding a per-instance loop, treat `Recoverable` like `Paused`/not-started so it never drives a PTY.
- **Lua LState is not goroutine-safe.** All `script.Engine` dispatch runs under `engine.mu`; the Bubble Tea main loop invokes scripts via a `tea.Cmd` goroutine and awaits `scriptDoneMsg`. Pending instances created by scripts are queued on the `scriptHost` adapter and finalized on the main goroutine in `handleScriptDone` — never call `h.list.AddInstance` from inside the engine.
- **No model mutation from `tea.Cmd` goroutines.** Bubble Tea runs every returned `tea.Cmd` in its own goroutine, concurrent with `Update`/`View` — so a Cmd body must not mutate shared state. `session.Storage`, `config.State`, and `tmux.TmuxSession` (its `ptmx`/`monitor`, via `stateMu` — snapshot under the lock, never hold it across PTY/subprocess I/O) each carry a mutex. Script nav/scroll/diff/workspace primitives record a `func(*home)` via `scriptHost.deferModelMutation`, drained into `scriptDoneMsg` and applied on the main goroutine in `handleScriptDone`; they do **not** touch `m.list`/`m.splitPane`/`m.slots` directly. Verify concurrent code with `go test -race` (see Build & Development Commands).
- **Script key collisions.** `cs.bind` / `cs.register_action` overwrite each other — last-write-wins across all scripts and `defaults.lua`. `ctrl+c` is hard-reserved in the default state (app-level) so user scripts cannot steal the interrupt; `keys.KeyForString` is a reverse lookup of the built-in binding table used only for menu-bar highlighting, not for dispatch gating. Duplicate load-order: `defaults.lua` loads first, then `~/.loom/scripts/*.lua` in filename order, so user bindings for the same key win.
- **Tmux prefix transition.** `tmux.TmuxPrefix = "loom_"`, `tmux.LegacyTmuxPrefix = "claudesquad_"`. `tmux.RenameLegacySessions` is centralized in `Storage.LoadAndReconcile`, running before per-record reconcile on every load path, so live sessions survive the flip. The orphan sweep accepts both prefixes.

### Persistent State

All stored in `~/.loom/`:
- `config.json` — user configuration: `DefaultProgram`, `DaemonPollInterval` (ms, default 1000; retained for backward compatibility only — its consumer, the background daemon, was removed), `BranchPrefix` (default: `{username}/`), `Profiles` (named program presets), `ClaudeRemoteControl` (`*bool`, default on — launches Claude sessions with `--remote-control <title>`; nil is treated as enabled, read via `Config.RemoteControlEnabled()`)
- `state.json` — app state (e.g. help screens seen)
- `instances.json` — serialized session data
- `workspaces.json` — registered workspaces with name, path, and last-used tracking
- `worktrees/` — git worktree directories
- `scripts/` — user-supplied `*.lua` files loaded at startup (global, shared across workspaces)

## Testing Patterns

- Tests use `testify/assert` for assertions
- Dependency injection via interfaces: `cmd.Executor`, `tmux.PtyFactory`
- Constructor variants for testing: `NewTmuxSessionWithDeps()` accepts mock dependencies
- Test setup pattern: `TestMain` initializes logging, runs tests, calls `os.Exit`
- Tests use temp directories for file I/O isolation

## Code Conventions

- Error wrapping: `fmt.Errorf("context: %w", err)`
- Module path is `github.com/aidan-bailey/loom`
- Platform-specific code in `_unix.go` / `_windows.go` suffixed files
- Private struct fields, public methods (PascalCase)
- Minimal goroutine usage; concurrency mainly in tmux monitoring

## CI/CD

GitHub Actions workflows in `.github/workflows/`:
- **build.yml** — Build and test on push/PR to main (triggered by Go file changes)
- **lint.yml** — golangci-lint on Go code changes
- **release.yml** — Auto-triggers on Build success on main (or `workflow_dispatch`). Reads `version` from `main.go`; skips if `v$VERSION` already exists on GitHub; otherwise tags, generates release notes from conventional commits via `git-cliff` (see `cliff.toml`), and runs GoReleaser to build/publish artifacts. To cut a release: bump the `version` string in `main.go`, regenerate `CHANGELOG.md` with `git cliff -o CHANGELOG.md --tag v$VERSION`, commit, and merge to main.

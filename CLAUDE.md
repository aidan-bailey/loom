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
| `R` | Resume paused instance with different launch options |
| `D` | Kill instance |
| `p` | Push branch |
| `s` | Stash & pause |
| `m` | Merge another session's branch into the current one |
| `a` | Quick input bar (send to agent) |
| `t` | Quick input bar (send to terminal) |
| `d` | Toggle diff overlay |
| `up`/`k`, `down`/`j` | Navigate sessions |
| `tab` | Toggle overview (fleet card grid) / focus mode |
| `]` / `[` | Jump to next/prev agent waiting for input (Prompting or bell; wraps) |
| `\` | Toggle the session rail |
| `T` | Show/hide the terminal pane |
| `ctrl+up` / `ctrl+down` | Resize the agent/terminal split (persisted per session title) |
| `z` | (overview) Collapse/expand the active workspace group |
| `enter` / `esc` | (overview) Return to focus mode |
| `W` | Workspace picker |
| `S` | Open settings |
| `l`/`{`, `;`/`}` | Previous/next workspace tab |
| `?` | Help |
| `q` | Quit |

## Environment Variables

- `LOOM_HOME` — Override config directory (default: `~/.loom`). Must be absolute path; supports `~` expansion. Used as a backward-compatible fallback; internal code uses explicit `WorkspaceContext` threading.
- `LOOM_LOG_FORMAT` — Set to `json` to emit structured log records from `log.InfoKV/WarnKV/ErrorKV` as JSON lines; otherwise plain text. Legacy `log.Infof`/`Warnf`/`Errorf` callers are unaffected.
- `LOOM_LOG_LEVEL` — `debug|info|warn|error` (default `info`). Gates both the Structured logger and the legacy `InfoLog`/`WarningLog`/`ErrorLog` writers (legacy records below the gate are dropped at the writer layer). The `--log-level` CLI flag (persistent on all subcommands) takes precedence over the env var.
- `LOOM_PANE_RENDERER` — Set to `snapshot` to disable the embedded VT emulator and fall back to the legacy `tmux capture-pane` snapshot path for pane rendering (also the implicit path on Windows). Unset (default) renders panes from the emulator, enabling mouse forwarding, event-driven updates (no render/status polling), the native hardware cursor, and title/bell/focus pass-through. Scroll-back is emulator-owned on this path: windows render in-process from x/vt scrollback (`vt.Emulator.RenderWindow`), seeded once per attach from `tmux capture-pane -S - -E -1`; `tmux capture-pane` windowing survives only on the snapshot path.

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

The default state renders in one of two **view modes** (`m.viewMode`), toggled with `tab`: **focus** (session rail + agent/terminal split — the classic layout) and **overview** (`ui/overview.go`, a fleet-triage card grid: the active workspace's instances as attention-sorted cards with live tails under a collapsible group header, peer workspaces as dimmed count lines). `enter`/`esc` return to focus; `n`/`N` drop to focus first, then run the create flow; `z` collapses the active group; mouse input is dropped in overview (v1). The mode persists per workspace in state.json's `ui` block — switching workspaces applies the target workspace's persisted mode.

### Key Packages

- **`app/`** — Bubble Tea application model. Handles all keyboard input dispatch, instance lifecycle management, and UI composition. This is the "controller" layer.
- **`session/`** — Core domain. `Instance` represents a running agent session with status lifecycle (Ready → Loading → Running → Paused). `storage.go` handles JSON serialization to `~/.loom/instances.json` and retains raw `InstanceData` for records that fail `ReconcileAndRestore` (the unrecovered cache) so a transient failure does not silently drop the entry on the next save. `orphan.go` discovers worktree directories on disk not referenced in state.json and classifies them (`Disposition`): stale leftovers (dead tmux + clean) are auto-cleaned, ones with unsaved work or a live agent surface inline as `Recoverable` session entries. `reconcileOrphans` (`app/app.go`, run on every workspace activation) drives this — there is no blocking recovery modal.
- **`session/agent/`** — `Adapter` interface and per-program implementations (claude, aider, gemini, default fallback). Centralizes trust-prompt keys, recovery flags, and `Supports(program)` checks. Look here when adding a new agent program rather than touching `tmux.go` or `agent_restart.go` directly.
- **`session/git/`** — Git worktree operations. Each session gets an isolated worktree in `~/.loom/worktrees/`. Branches are named `{username}/{session_title}`. Handles setup, diff stats, push, and cleanup. `Setup` writes a `.loom-title` sidecar file next to the worktree directory (a sibling path, not inside the work tree, so it never pollutes git status) recording the original instance title; `Cleanup` removes it. Orphan discovery (`session/orphan.go`) reads it to recover the exact title/tmux-session-name pair — branch-name-derived titles are lossy (lowercased, dash-collapsed) and would otherwise miss the live session. Worktrees predating the sidecar degrade gracefully to the humanized branch leaf.
- **`session/tmux/`** — Tmux session management. Creates/attaches terminal sessions, captures pane content, detects prompts (surfaces a `Prompting` status so the user knows an instance needs attention), sends keystrokes. Also pumps `ptmx` output into an embedded VT emulator (`emulator_unix.go`/`emulator_windows.go`, gated by `LOOM_PANE_RENDERER`) so panes render from the emulator with a capture-pane fallback, and `ForwardMouse`/bracketed paste back interact mode. The pump writes through `vt.NewAltScreenFilter` before it reaches the emulator: tmux clients enter the alternate screen at attach and never leave it, and x/vt only accumulates scrollback on the primary screen, so the filter strips alt-screen mode switches (1047/1049/47) from the client stream to keep it on the primary screen where scrollback can accrue. `Restore` seeds pre-attach history once per attach (`capture-pane -S - -E -1`, stored as `SeedHistory`); `ui.ScrollModel` (one shared state machine used by both panes) windows seed + emulator scrollback + screen via `RenderWindow`/`ScrollbackLen`. `TmuxSession.stateMu` guards the `ptmx`/`monitor`/emulator fields against the metadata-fan-out vs attach-lifecycle race. Prefix is `loom_`; `LegacyTmuxPrefix` (`claudesquad_`) is still recognized by the orphan sweep and the startup rename pass. The output pump also drives the event-based UI: a per-session coalescer (`notify.go`) emits dirty (≤ ~60/s), quiet (500ms settled), bell, and dead notifications through the package-level `tmux.SetNotifier` hook, which `app.Run` wires to `tea.Program.Send`. Status detection (`statusContent`) reads the emulator in-process; capture-pane remains only as the snapshot-path fallback.
- **`session/vt/`** — Embedded terminal emulator backing pane display. `vt.go` defines the `Emulator` interface; `xvt.go` is the `charm.land/x/vt`-backed implementation. Decouples on-screen rendering and scrollback from `tmux capture-pane` so panes can live-scroll and forward mouse/paste input.
- **`session/files/`** — Stateless filesystem enumeration helpers backing the file-explorer overlay. Separate from `session/git/` because callers may operate on non-git roots (workspace terminals pointed at bare directories).
- **`config/`** — Configuration (`config.json`), state (`state.json`), profiles, and workspace registry (`workspace.go`). Key types: `WorkspaceContext` (carries resolved config dir through the app), `InstanceStorage`, `AppState`, `StateManager`. `LoadConfigFrom("")`/`LoadStateFrom("")` accept empty string as "use default directory". `config/migration.go:MigrateLegacyHome` handles the one-time `~/.claude-squad` → `~/.loom` rename.
- **`ui/`** — Bubble Tea view components. Left session rail (`list.go`, 20% width, hidden with `\`) renders live mini-cards built by `card.go` (title + output tail, left accent bar: gold = needs input, blue = selected, green = running, purple = workspace terminal) with dimmed peer-workspace summaries at the bottom; branch + diff stats live in the agent pane title and overview cards, not the rail. Right panel (`split_pane.go`, 80% width) has agent and terminal panes stacked vertically (70/30 default split, resizable with `ctrl+up`/`ctrl+down`, terminal hideable with `T`) and a hotkey-toggled diff overlay. `overview.go` renders the fleet card grid for overview mode. `theme.go` defines the color-role vars, `ApplyTheme`, and `RegisterThemeHook`. `scroll.go` defines `ScrollModel`, the shared scroll state machine (offset/anchoring/wheel-damping/alt-screen routing) that both `PreviewPane` (agent pane) and `terminal.go`'s `TerminalPane` delegate to on the emulator path; each pane falls back to legacy capture-pane windowing when the instance has no emulator (snapshot mode / Windows). `terminal.go` renders a pane from the embedded VT emulator (with jump-to-bottom footer and mouse drag-select/copy). `quick_input.go` provides an inline input bar for sending text to tmux. `workspace_tab_bar.go` renders workspace tabs. `ui/overlay/` has modal dialogs (text input, confirmation, branch picker, profile picker, workspace picker, file explorer).
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
- **Pane updates are event-driven, not polled.** With the emulator enabled there is NO preview tick: panes re-render on `paneDirtyMsg` from the output pump, status transitions ride `paneQuietMsg`, and the 3s health tick only does liveness/ptmx-repair/diff stats. The legacy 100ms preview tick and 500ms full metadata scan only survive under `LOOM_PANE_RENDERER=snapshot` (and Windows). When adding per-instance periodic work, put it on the health tick; when reacting to output, handle the event messages in `app/events.go` — and never `Send` from the Update goroutine's own handlers (the pump/timer goroutines own that). Status derivation on this path must carry its own re-evaluation guarantee: quiet fires once per burst and the health tick runs no status ladder for emulator instances, so an inconclusive detection (`updated=true`, or a quiet dropped while `Loading`) re-arms via `maybeRedetect`/`redetectMsg` until a sample sees unchanged content — a rule that assumes "the next poll will correct it" silently latches (the old `updated→Running` latch; regression tests in `app/status_redetect_test.go`).
- **x/vt callbacks run under the xvt wrapper's write lock.** `session/vt/xvt.go` registers `Callbacks` that fire inside `emu.Write`; they may only assign wrapper fields — re-locking `e.mu` deadlocks (RWMutex is not reentrant) and calling app code from them violates the no-model-mutation rule. New emulator-sourced state follows the same pattern: callback writes the field, a read-locked accessor exposes it. Events that must *notify* (not just store) follow the bell pattern: the callback sets a pending flag, and `Write`/`Resize` invoke the handler after releasing the lock — invoking under the lock deadlocked the whole UI once (bellFunc → `tea.Program.Send` blocks on the Update goroutine, which was blocked on `e.mu`; `TestBell_FiresOutsideWriteLock` pins this). One known vendored-library bug: the OSC-title parser truncates titles containing non-ASCII bytes (terminates on the lead byte of a multi-byte UTF-8 sequence) — `TmuxSession.PaneTitle` guards this by rejecting invalid UTF-8 rather than forwarding mangled bytes to the host terminal.
- **Inline orphan recovery (no modal).** `reconcileOrphans` (`app/app.go`, called from `activateWorkspace` and classic startup — so it runs on every workspace-load path) auto-cleans stale worktrees and surfaces unsaved/live ones as `Recoverable` list entries. `Recoverable` is **ephemeral**: filtered out of `persistableInstances`, re-derived from disk each load, and inert (`EnsureRunning` no-ops on it). `r` recovers (adopt via `ReconcileAndRestore`), `D` discards (worktree removed, branch kept via `IsExistingBranch`). When adding a per-instance loop, treat `Recoverable` like `Paused`/not-started so it never drives a PTY.
- **Lua LState is not goroutine-safe.** All `script.Engine` dispatch runs under `engine.mu`; the Bubble Tea main loop invokes scripts via a `tea.Cmd` goroutine and awaits `scriptDoneMsg`. Pending instances created by scripts are queued on the `scriptHost` adapter and finalized on the main goroutine in `handleScriptDone` — never call `h.list.AddInstance` from inside the engine.
- **No model mutation from `tea.Cmd` goroutines.** Bubble Tea runs every returned `tea.Cmd` in its own goroutine, concurrent with `Update`/`View` — so a Cmd body must not mutate shared state. `session.Storage`, `config.State`, and `tmux.TmuxSession` (its `ptmx`/`monitor`, via `stateMu` — snapshot under the lock, never hold it across PTY/subprocess I/O) each carry a mutex. Script nav/scroll/diff/workspace primitives record a `func(*home)` via `scriptHost.deferModelMutation`, drained into `scriptDoneMsg` and applied on the main goroutine in `handleScriptDone`; they do **not** touch `m.list`/`m.splitPane`/`m.slots` directly. Verify concurrent code with `go test -race` (see Build & Development Commands).
- **Script key collisions.** `cs.bind` / `cs.register_action` overwrite each other — last-write-wins across all scripts and `defaults.lua`. `ctrl+c` is hard-reserved in the default state (app-level) so user scripts cannot steal the interrupt; `keys.KeyForString` is a reverse lookup of the built-in binding table used only for menu-bar highlighting, not for dispatch gating. Duplicate load-order: `defaults.lua` loads first, then `~/.loom/scripts/*.lua` in filename order, so user bindings for the same key win.
- **Pane scroll-back is emulator-owned; the alt-screen filter is ARCHITECTURAL.** tmux client streams live permanently on the alt screen; x/vt only exposes primary-screen scrollback; `vt.NewAltScreenFilter` between pump and emulator is what makes scrollback accumulate at all — removing it silently kills scroll-back (the real-tmux integration test `TestScrollbackAccumulation_RealTmux` pins this). `ScrollModel.AdvanceAndRender` mutates anchor state — exactly once per render pass. TerminalPane's `updateContentSnapshotLocked` releases `t.mu` around CaptureHistory — never add an early return between its Unlock/Lock.
- **Tmux prefix transition.** `tmux.TmuxPrefix = "loom_"`, `tmux.LegacyTmuxPrefix = "claudesquad_"`. `tmux.RenameLegacySessions` is centralized in `Storage.LoadAndReconcile`, running before per-record reconcile on every load path, so live sessions survive the flip. The orphan sweep accepts both prefixes.
- **Theme-derived styles must be hook-built.** Any package-level `lipgloss.Style` using a `ui` color role must be constructed inside a `ui.RegisterThemeHook` callback, not in a var initializer — init-time styles capture pre-`ApplyTheme` colors and go stale when the settings overlay switches themes live. Roles only, no literal colors (see `ui/theme.go`).
- **Overview mode is a `viewMode`, orthogonal to the state machine.** `m.viewMode` (focus/overview) is not an `m.state` value — overlays and per-state handlers work unchanged on top of it. In overview, `state_default.go` gates script dispatch through the `overviewKeyAllowed` whitelist (everything else no-ops rather than acting on an invisible pane), mouse events are dropped, and bell/attention badges clear only on entering focus so the attention-sorted grid stays stable while you look at it. New key work must respect both: add grid-safe keys to the whitelist explicitly, and don't clear attention state from overview handlers.

### Persistent State

All stored in `~/.loom/`:
- `config.json` — user configuration: `DefaultProgram`, `BranchPrefix` (default: `{username}/`), `Profiles` (named program presets), `Theme` (`"afterglow"` default, `"legacy"`; empty means default, read via `Config.GetTheme()`; cycled live from the settings overlay's Theme row), `ClaudeRemoteControl` (`*bool`, default on — launches Claude sessions with `--remote-control <title>`; nil is treated as enabled, read via `Config.RemoteControlEnabled()`), `ClaudePermissionMode` (`*string`, default `"default"` — launches Claude sessions with `--permission-mode <mode>`; nil is treated as `"default"` (no flag injected), read via `Config.PermissionMode()`; valid values enumerated in `config.ClaudePermissionModes` and cycled from the Claude Preferences overlay)
- `state.json` — app state (e.g. help screens seen) plus the `ui` prefs block (`config.UIPrefs`: `view_mode`, `rail_hidden`, `terminal_hidden`, and `split_ratios` — a per-session-title map of agent/terminal split ratios, written throttled on `ctrl+up`/`ctrl+down` resizes)
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

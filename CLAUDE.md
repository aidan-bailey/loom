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

# Cleanup scripts (refuse to run inside a loom-managed tmux session)
./clean.sh        # Kill tmux server, remove worktrees and ~/.loom/
./clean_hard.sh   # Same as clean.sh + git worktree prune

# Dev sandbox — run a dev build safely from inside loom (.claude/skills/loom-dev)
go run ./tools/loomdev up                 # create + build (named after the branch leaf)
go run ./tools/loomdev run                # interactive, in this terminal
go run ./tools/loomdev start              # headless, then: wait --text toy / keys … / shot
go run ./tools/loomdev down               # delete the sandbox and its tmux server
go test -tags e2e ./e2e/...               # end-to-end smoke suite (needs tmux)

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
| `I` | New instance from a GitHub issue (picker; needs `gh` auth). In the `N` prompt, a leading `#123` expands to that issue |
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
| `tab` | Toggle overview (fleet card grid) / focus mode (from the workbench, tab exits straight to overview) |
| `]` / `[` | Jump to next/prev agent waiting for input (Prompting or bell; spans **all open workspaces** — in focus mode crosses workspaces, switching the focused tab; in overview it moves only the cursor, no focus switch; in the workbench, same-slot jumps retarget the panel while cross-workspace jumps exit to focus; wraps). **Not** waiting-jump on the workbench's review tab: there they are prev/next comment |
| `\` | Toggle the session rail |
| `T` | Show/hide the terminal pane |
| `ctrl+up` / `ctrl+down` | Resize the agent/terminal split (persisted per session title) |
| `z` | (overview) Collapse/expand the active workspace group |
| `enter` | (overview) Focus the selected card's workspace + instance (crosses open workspaces); (focus) Open the workbench for the selected session |
| `esc` | (overview) Return to focus mode; (workbench) Return to focus mode |
| `1`–`5` | (workbench) Select panel tab (markdown / diff / files / terminal / review). On an open **doc** review `1`–`4` and `tab` still act as normal workbench keys; on an open **code** review the pane owns digits `1`–`9` (file tabs) and `tab`/`shift+tab` (cycle files), so leave with `q` first to switch panels |
| `c` | (workbench, markdown tab) Review the shown doc with inline comments; (focus) open the workbench code review for the selected session |
| `S` | (workbench, review tab) Send review comments to the agent (with confirmation) |
| `q` | (workbench, review tab) Save and close the review, returning to the panel tab it was opened from |
| `e` / `f` | (workbench) Edit the shown markdown / resume follow mode |
| `ctrl+left` / `ctrl+right` | (workbench) Resize the agent/panel split (persisted per session) |
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
- `LOOM_TMUX_SOCKET` — Private tmux socket name: every tmux invocation gets `-L <name>` (via `tmux.Command`), overriding `$TMUX`. Used by the dev sandbox.
- `LOOM_GLOBAL_DIR` — Relocates `GetGlobalConfigDir()` (workspace registry + global context), which ignores `LOOM_HOME` by design. Absolute; supports `~`. Also disables the legacy-home migration.
- `LOOM_ALLOW_NESTED` — Set to `1` to bypass the nesting guard (see Gotchas).

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

The default state renders in one of two **view modes** (`m.viewMode`), toggled with `tab`: **focus** (session rail + agent/terminal split — the classic layout) and **overview** (`ui/overview.go`, a fleet-triage card grid). `enter`/`esc` return to focus; `n`/`N` drop to focus first, then run the create flow; `z` collapses a group; mouse input is dropped in overview (v1). The mode persists per workspace in state.json's `ui` block — switching workspaces applies the target workspace's persisted mode.

**Cross-workspace overview.** The overview spans the **open (tab-bar) workspaces only** — each open workspace renders as its own card group (focused first, then alphabetical; per-group states loaded/empty). Workspaces registered but not open do not appear; open more via the picker (`W`). The overview carries a **global domain cursor** `home.overviewCursor {slot,inst}` (translated to render coords `ui.OverviewCursor {Group,Item}` in `overviewData`); `j/k` walk `fleetOrder` across all groups. Cursor-committing keys route through **`focusCursorSlot()`** (save current slot → `loadSlot` → select), so `enter`/`D`/`r`/`n` reuse the existing focus-mode intents on the right workspace. Focus-mode `]`/`[` (`jumpWaiting`) also crosses open workspaces, switching the focused slot as needed.

### Key Packages

- **`app/`** — Bubble Tea application model. Handles all keyboard input dispatch, instance lifecycle management, and UI composition. This is the "controller" layer.
- **`session/`** — Core domain. `Instance` represents a running agent session with status lifecycle (Ready → Loading → Running → Paused). `storage.go` handles JSON serialization of the `instances` array in `state.json` and retains raw `InstanceData` for records that fail `ReconcileAndRestore` (the unrecovered cache) so a transient failure does not silently drop the entry on the next save. Records that fail to *decode* (corrupt, or a `schema_version` newer than this binary after a downgrade) are skipped by `MigrateAll`, held as raw bytes (`UndecodableCount`), and appended verbatim to every write — `SaveInstances`, `DeleteInstance`, `UpdateInstance` all go through `writeLocked`, which also loads first on a never-loaded `Storage`. A payload that is not a JSON array at all fails the load and latches every write shut (`ErrStorageLoadFailed`, state untouched) until a later load succeeds; `DeleteAllInstances` (`loom reset`) is the one write allowed through, and clears the latch. Both kinds of preserved record are claimed by worktree path (`PreservedWorktreePaths`) so orphan discovery doesn't offer them as `Recoverable`, and by title (`PreservedTitles`, folded in by `app`'s `claimTitles` at every sweep site) so the server-wide orphan tmux sweep and the hooks sweep spare their sessions, workspace-terminal auto-create doesn't kill and duplicate a preserved terminal, and a new session can't take a preserved title (`preservedTitleErr`); both are counted in the recovery summary. `orphan.go` discovers worktree directories on disk not referenced in state.json and classifies them (`Disposition`): stale leftovers (dead tmux + clean) are auto-cleaned, ones with unsaved work or a live agent surface inline as `Recoverable` session entries. `reconcileOrphans` (`app/app.go`, run on every workspace activation) drives this — there is no blocking recovery modal.
- **`session/agent/`** — `Adapter` interface and per-program implementations (claude, aider, gemini, default fallback). Centralizes trust-prompt keys, recovery flags, and `Supports(program)` checks. Look here when adding a new agent program rather than touching `tmux.go` or `agent_restart.go` directly.
- **`session/subagent/`** — Tracks the subagents and agent-team teammates a Claude session spawns, from Claude Code hook events. `hooks.go` writes the per-launch hooks folder (`settings.json`, `launch-id`, `events/`); `scan.go` turns new event files into compact `.ev` files and replays them when needed; `tracker.go` is the state machine; `event.go`/`meta.go` parse payloads and the `agent-<id>.meta.json` sidecars. No tmux, UI or app dependency. `session/subagent_hooks.go` wires it into `Instance`.
- **`session/git/`** — Git worktree operations. Each session gets an isolated worktree in `~/.loom/worktrees/`. Branches are named `{username}/{session_title}`. Handles setup, diff stats, push, and cleanup. `Setup` writes a `.loom-title` sidecar file next to the worktree directory (a sibling path, not inside the work tree, so it never pollutes git status) recording the original instance title; `Cleanup` removes it. Orphan discovery (`session/orphan.go`) reads it to recover the exact title/tmux-session-name pair — branch-name-derived titles are lossy (lowercased, dash-collapsed) and would otherwise miss the live session. Worktrees predating the sidecar degrade gracefully to the humanized branch leaf.
- **`session/tmux/`** — Tmux session management. Creates/attaches terminal sessions, captures pane content, detects prompts (surfaces a `Prompting` status so the user knows an instance needs attention), sends keystrokes. Also pumps `ptmx` output into an embedded VT emulator (`emulator_unix.go`/`emulator_windows.go`, gated by `LOOM_PANE_RENDERER`) so panes render from the emulator with a capture-pane fallback, and `ForwardMouse`/bracketed paste back interact mode. The pump writes through `vt.NewAltScreenFilter` before it reaches the emulator: tmux clients enter the alternate screen at attach and never leave it, and x/vt only accumulates scrollback on the primary screen, so the filter strips alt-screen mode switches (1047/1049/47) from the client stream to keep it on the primary screen where scrollback can accrue. `Restore` seeds pre-attach history once per attach (`capture-pane -S - -E -1`, stored as `SeedHistory`); `ui.ScrollModel` (one shared state machine used by both panes) windows seed + emulator scrollback + screen via `RenderWindow`/`ScrollbackLen`. `TmuxSession.stateMu` guards the `ptmx`/`monitor`/emulator fields against the metadata-fan-out vs attach-lifecycle race. Prefix is `loom_`; `LegacyTmuxPrefix` (`claudesquad_`) is still recognized by the orphan sweep and the startup rename pass. The output pump also drives the event-based UI: a per-session coalescer (`notify.go`) emits dirty (≤ ~60/s), quiet (500ms settled), bell, and dead notifications through the package-level `tmux.SetNotifier` hook, which `app.Run` wires to `tea.Program.Send`. Status detection (`statusContent`) reads the emulator in-process; capture-pane remains only as the snapshot-path fallback.
- **`session/vt/`** — Embedded terminal emulator backing pane display. `vt.go` defines the `Emulator` interface; `xvt.go` is the `charm.land/x/vt`-backed implementation. Decouples on-screen rendering and scrollback from `tmux capture-pane` so panes can live-scroll and forward mouse/paste input.
- **`session/files/`** — Stateless filesystem enumeration helpers backing the file-explorer overlay. Separate from `session/git/` because callers may operate on non-git roots (workspace terminals pointed at bare directories).
- **`session/github/`** — Reads GitHub state through the `gh` CLI: `Query` (PRs by head branch + open issues, one snapshot per repo), `View` (one issue with body), `CheckCLI`, `StateFor` (pure per-session join), `SeedPrompt`/`SlugTitle`/`ParseShorthand`. `SlugTitle`'s output becomes a git branch name (loom names branches `{username}/{session_title}`), so it whitelists lowercase ASCII alphanumerics only — a fully non-ASCII title yields a bare `gh-<n>`. No app, ui or session imports; injected executor. `app/github.go` drives it.
- **`review/`** — Comment model, YAML store (`.crit/` in the worktree, self-gitignored), code-review session manifest, and agent-prompt composition. Vendored from kevindutra/crit (MIT, see NOTICE.md) with paths threaded through an explicit worktree root. `review/gitdiff/` parses per-file changed-line maps via go-gitdiff (`git -C <dir>`), separate from `session/git`'s worktree lifecycle ops.
- **`config/`** — Configuration (`config.json`), state (`state.json`), profiles, and workspace registry (`workspace.go`). Key types: `WorkspaceContext` (carries resolved config dir through the app), `InstanceStorage`, `AppState`, `StateManager`. `LoadConfigFrom("")`/`LoadStateFrom("")` accept empty string as "use default directory". `config/migration.go:MigrateLegacyHome` handles the one-time `~/.claude-squad` → `~/.loom` rename.
- **`ui/`** — Bubble Tea view components. Left session rail (`list.go`, 20% width, hidden with `\`) renders live mini-cards built by `card.go` (title + output tail, left accent bar: gold = needs input, blue = selected, green = running, purple = workspace terminal) with dimmed peer-workspace summaries at the bottom; branch + diff stats live in the agent pane title and overview cards, not the rail. Right panel (`split_pane.go`, 80% width) has agent and terminal panes stacked vertically (70/30 default split, resizable with `ctrl+up`/`ctrl+down`, terminal hideable with `T`) and a hotkey-toggled diff overlay. `overview.go` renders the fleet card grid for overview mode. `theme.go` defines the color-role vars, `ApplyTheme`, and `RegisterThemeHook`. `scroll.go` defines `ScrollModel`, the shared scroll state machine (offset/anchoring/wheel-damping/alt-screen routing) that both `PreviewPane` (agent pane) and `terminal.go`'s `TerminalPane` delegate to on the emulator path; each pane falls back to legacy capture-pane windowing when the instance has no emulator (snapshot mode / Windows). `terminal.go` renders a pane from the embedded VT emulator (with jump-to-bottom footer and mouse drag-select/copy). `quick_input.go` provides an inline input bar for sending text to tmux. `workspace_tab_bar.go` renders workspace tabs. `ui/overlay/` has modal dialogs (text input, confirmation, branch picker, profile picker, workspace picker, file explorer). `ui/review/` (package `reviewui`) embeds the vendored crit review pane — theme-hooked styles, doc-review and code-review modes behind a shared `Pane` type; package `ui` holds only the `ReviewPane` interface (implemented by `reviewui.Pane`) so the workbench can reference it without an import cycle.
- **`keys/`** — Keybinding definitions. Enum-based `KeyName` with global maps for lookup.
- **`cmd/`** — `Executor` interface wrapping `os/exec` for testability.
- **`log/`** — Centralized logging to `{configDir}/logs/loom.log` with Info/Warning/Error loggers and rate limiting.
- **`internal/devsandbox/`** — Dev sandboxes for developing loom inside loom: `Up` (toy repo + bare origin, sandbox config/state seeded into both `global/` and `repo/.loom/`, registry entry), `Build`, and a headless driver (`Start`/`SendKeys`/`Screen`/`WaitFor`) on a private tmux socket. CLI: `tools/loomdev`; deterministic agent stand-in: `tools/fakeagent` (persona from `argv[0]` via the adapter registry). `tools/` is excluded from the Nix package.
- **`script/`** — Lua scripting engine (`github.com/yuin/gopher-lua`). The full built-in keymap lives in `script/defaults.lua`, embedded via `go:embed` and loaded at engine init before any user script. Users extend or override bindings from `~/.loom/scripts/*.lua` (global, not per-workspace). Dispatch is driven from `state_default.go` through `app/app_scripts.go`'s `scriptHost` adapter. Hard-sandboxed: only `base`/`string`/`table`/`math`/`coroutine`; `dofile`/`loadfile`/`load`/`loadstring`/`require`/`string.dump`/`collectgarbage` stripped. Exposed API: `cs.bind`/`cs.unbind`/`cs.register_action`, `cs.actions.*` (sync primitives + deferred intent factories), `cs.await`, `cs.log`, `cs.notify`, `cs.now`, `cs.sprintf`, plus userdata wrappers for `session.Instance`, `git.GitWorktree`, and a per-dispatch `ctx`.

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

- **Instance data schema changes.** `session.InstanceData` has a `SchemaVersion` field and `session.CurrentSchemaVersion` constant. When adding/removing/renaming fields: bump `CurrentSchemaVersion`, add an upgrade step to the switch in `session/storage_migrate.go:Migrate`, and update the JSON fixture in `cmd/workspace_migrate_shape_test.go` (drift guard for the `workspace migrate` CLI's typed mirror struct). A record `Migrate` rejects is never fatal to the load and never dropped: `Storage` skips it and writes it back unchanged, so a downgraded loom leaves a newer binary's records intact for when it returns. Only a top-level payload that isn't an array fails the load. Then classic startup, `activateWorkspace` and `enterGlobalMode` abort rather than continue with an empty list, `restoreSavedWorkspaces` skips the server-wide orphan sweep (the failed workspace's titles are unknown), and `Storage` refuses writes with `ErrStorageLoadFailed`. When *every* restored workspace fails, `loadStartupStorageFallback` loads the startup context's storage with classic-startup semantics (shared `loadStartupStorage`, minus the orphan sweep) so the global list shows its real sessions; if that load fails too, the error is shown instead of exiting, the latch refuses every save, `applyWorkspaceToggle` skips its pre-transition global save on `ErrStorageLoadFailed` so a workspace can still be opened, and `handleQuit` quits without saving (keeping the failed workspaces in the registry's open list for the next launch). Those skips are lossless because new sessions are refused while the focused storage `WritesRefused()` (`latchedStorageErr`), so a latched list stays empty. Don't add a write path that bypasses `Storage.writeLocked`; one already does — `loom workspace migrate` (`cmd/workspace_migrate.go`) decodes records into its own typed mirror and writes via `config.SaveStateTo`, so a newer-schema record passing through it loses every field this binary doesn't know.
- **`FromInstanceData` is decoupled from PTY attach.** It's a pure constructor — it does not spawn a tmux session. Callers that need a live PTY must call `inst.EnsureRunning()` explicitly (see `session/reconcile.go`).
- **Pane updates are event-driven, not polled.** With the emulator enabled there is NO preview tick: panes re-render on `paneDirtyMsg` from the output pump, status transitions ride `paneQuietMsg`, and the 3s health tick only does liveness/ptmx-repair/diff stats. The legacy 100ms preview tick and 500ms full metadata scan only survive under `LOOM_PANE_RENDERER=snapshot` (and Windows). When adding per-instance periodic work, put it on the health tick; when reacting to output, handle the event messages in `app/events.go` — and never `Send` from the Update goroutine's own handlers (the pump/timer goroutines own that). Status derivation on this path must carry its own re-evaluation guarantee: quiet fires once per burst and the health tick runs no status ladder for emulator instances, so an inconclusive detection (`updated=true`, or a quiet dropped while `Loading`) re-arms via `maybeRedetect`/`redetectMsg` until a sample sees unchanged content — a rule that assumes "the next poll will correct it" silently latches (the old `updated→Running` latch; regression tests in `app/status_redetect_test.go`). For Claude sessions this ladder is now the **fallback** — see the Claude roster gotcha below.
- **Claude's roster outranks the pane scraper.** `session/claude_roster.go` shells out to `claude agents --json` (`rosterQueryCmd`, ~380ms, off the Update goroutine) on its **own 3s cadence** — `maybeRosterQuery` throttles by `rosterInterval` and guards against overlap through its `pollGate` (`gateRoster`), because the health tick it rides fires every 500ms on the snapshot path and an unthrottled ~380ms subprocess there would keep a `claude` process alive ~76% of the time (and stack concurrent ones if the CLI hung). Every throttled job (roster query, subagent scan, GitHub poll, and the split-ratio flush tick) is gated this way (`app/pollgate.go`): `dispatchGated` arms the gate only when its builder returns a Cmd (no Cmd means no result message to disarm it), and wraps the result in a `gatedMsg` that `Update` disarms **before** routing the inner message to its usual handler — so every delivery, errors included, re-arms the job and no handler touches in-flight state (a missed disarm would latch the job off for the session). A gated builder must return a single-message Cmd: a `tea.Tick` is fine, a `tea.Batch`/`tea.Sequence` is not. The query returns every live Claude session keyed by **cwd** — the roster covers ordinary interactive sessions, not just `claude --bg` ones, so Loom's own tmux-hosted agents appear in it. `home.roster` holds the result; `rosterStatusFor` joins on `Instance.GetWorktreePath()` and maps `busy`→`Running`, `waiting`→`Prompting`, `idle`→`Ready`. Only **interactive** entries are joined: background (`claude --bg`) sessions publish `state` where interactive ones publish `status`, and a `--bg` session started inside a worktree would otherwise collide with Loom's own tmux session and blind the join for an instance whose identity is not in doubt. An entry with no `kind` counts as interactive (older CLI builds omit the field). Both status paths (`statusDetectedMsg` on the event path, `metadataReadyMsg` on the snapshot path) consult it **before** the content ladder and must stay in lockstep — they do so by both calling **`adoptRosterStatus`**, the single choke point that applies the status *and* records `Instance.WaitReason`; never re-implement that pairing at a call site. An authoritative answer also **suppresses `maybeRedetect`** — the ladder re-samples only because one content hash cannot separate "still working" from "just finished", and the roster says which it is. It **fails closed** in the same sense as `DetectClaudeRemoteControlAuth` — on any uncertainty it expresses *no opinion* rather than guessing, and the scraper ladder decides: non-Claude agents, an empty/failed query, no entry for the worktree, an ambiguous cwd (two *interactive* Claude sessions in one directory — deliberately dropped in `QueryClaudeRoster`, with a `session.roster.ambiguous_cwd` debug line), a cwd holding only background sessions, and unrecognized status strings all return `false` from `rosterStatusFor`. The Claude CLI is therefore never a hard dependency; a machine without it behaves exactly as before. A failed query **clears** the roster rather than retaining it — stale statuses are worse than none. `RosterEntry.WaitingFor` (Claude's own reason for blocking: `"sandbox request"`, `"dialog open"`, `"input needed"`, `"goal proposal"`, …) rides through `adoptRosterStatus` onto the transient `Instance.waitReason` and replaces the generic "awaiting input" phrase in `CardData.statusLabel`. It is treated as an **opaque string** — displayed verbatim and truncated, never mapped to a Loom enum — so a reason string this build has never seen renders fine instead of degrading to Unknown. It lives exactly as long as the roster-driven wait: any non-Prompting or non-authoritative outcome clears it, so a dismissed dialog cannot leave a label behind. Never serialized (absent from `InstanceData`, so it needs no `SchemaVersion` bump). Tests: `session/claude_roster_test.go`, `app/roster_status_test.go`, `ui/card_test.go`.
- **Subagent rows come from hooks, not transcripts.** Transcripts can't tell you whether an agent is running (each content block is its own record, and `stop_reason` is missing from ~60% of responses), so Claude is launched with `--settings {ConfigDir}/hooks/<folder>/settings.json`, where `<folder>` is the path-escaped tmux session name (`hooksFolderName`: `url.PathEscape(tmux.ToLoomTmuxName(title))`, always a single path segment, so a `/` in a title can't nest one instance's folder inside another's, where launching, killing or sweeping the outer one would delete it), registering `SubagentStart`/`SubagentStop`/`TeammateIdle`/`Stop`/`SessionEnd`. Each hook writes a file to `events/`; `maybeSubagentScan` (gated like `maybeRosterQuery`, via `gateSubagent`) collects them into `Instance`'s `subagent.Tracker`. Rules that are easy to break: hooks are prepared **only on real launches** (`launchProgram(…, true)`: `Start(true)`, `startFreshWithRecovery`, `CrashRestart`, `Restart`), never on `Restore`, because a reattached Claude is still writing to its folder. `Restart()` (the workspace-terminal auto-restart) is a real launch too: it closes the dead session and rebuilds it as `TmuxSession.WithProgram(launchProgram(Program, true))` — reusing the old object would relaunch its stale command (for a restored instance, the bare `Program`: no hooks, no loom context) while the tracker kept the dead process's rows. Every real launch resets the previous launch's state first, even when hooks end up being skipped: `resetSubagentLaunch` (`session/subagent_hooks.go`) clears the tracker and warm flag, sets the launch ID to the `noHooksLaunchID` sentinel so no scan result matches, and removes the old hooks folder; a successful `Prepare` then sets the real launch ID. The folder lives outside `worktrees/` because `DiscoverOrphans` would walk it. Only the **parent's `Stop`** reconciles against `background_tasks` (a `SubagentStop` list can omit a parallel foreground agent), and a missing or malformed list must never be read as empty. Teammates can't be matched by ID in that list, so shutdown is inferred by count. Read events are kept as compact `.ev` files so a restarted loom replays them (`Request.Cold`); a restored instance adopts the folder's `launch-id`, and results for another launch ID are dropped. A scan returning `ErrNoHooks` for a launch with a real ID means its folder vanished mid-run, and `ForgetSubagentsWithoutHooks` drops its rows. Everything fails closed: an agent without its metadata sidecar is hidden. Windows, non-Claude programs, a user-supplied `--settings`, or a `'` in the config dir path launch without hooks (the folder name itself escapes `'`). `claude_subagent_tracking` only decides whether a launch gets hooks; sessions launched with hooks keep being scanned only until their next launch. Opt-in contract test: `LOOM_TEST_REAL_CLAUDE=1 go test -timeout 15m ./session/subagent -run TestRealClaude` (costs money).
- **The GitHub poller is roster-shaped and fails closed.** `maybeGHQuery` (`app/github.go`) rides the health tick on its own `ghInterval` (60s) cadence through the same `pollGate` as the roster (`gateGH`); the `ghAvailable` backoff below is a pre-check ahead of `dispatchGated`, and no open repo means no dispatch, so nothing is armed. Each poll resolves + fetches the base ref per open repo (`git.FetchRef`, even without `gh`) and, when `gh` is available, runs `github.Query`; `ghState` is replaced wholesale, so an errored repo is dropped rather than kept — its cards render `Known=false` (nothing) rather than a stale badge, and `Known=true, HasPR=false` is a real "no PR", not "not yet checked". Results join onto `Instance.SetGitHubState` (transient; `ui.BuildCardData` copies it via `GitHubState()`); ahead/behind (`Instance.UpdateParity`, one local `rev-list`) rides the 3s metadata fan-out keyed on `home.ghBases`. Call `m.gate(gateGH).expedite()` (or return `ghRefreshMsg`) to force the next tick to poll immediately (an in-flight poll still lands first) — push (`pushActionFor`), an issue-born session (`handleIssuePicked`/`handleIssueExpanded`), and workspace activation all do this. GitHub state never sets `NeedsAttention`. `ghAvailable.ok` is a **one-way latch to true**: `maybeGHQuery`'s `req.check` is `!checked || !ok`, so once `CheckCLI` succeeds it is never re-run for the rest of the session — gh breaking mid-session (revoked token, network loss) surfaces only as per-repo query errors, never as an availability flip. An *unavailable* result is not latched the same way: it is re-probed every `ghRecheckInterval` (5 min) rather than forever, so a user who runs `gh auth login` after starting loom recovers without a restart. `ghPollRequest.configured` is a `map[string]string` keyed by repo path, built by `baseBranchByRepo()`, because `m.appConfig` is whichever slot is *focused*, not a shared primary — one string applied across the batch would resolve every non-focused repo against the wrong workspace's `BaseBranch`. `ghPollBudget` (45s) bounds the **entire** poll, not each repo: `github.Query` budgets each `gh` subprocess separately (2 list calls plus one `issue view` per linked closed issue), so without an overall ceiling one repo with many linked closed issues could stall every other workspace's refresh; it is deliberately kept under `ghInterval` so a poll can't outlive its own cadence. `UpdateParity` is called in `gatherMetadataCmd` **before** the `wantFull`/`ShouldRefreshDiff` early return — that gate exists to skip a git subprocess for an idle session with no tmux output, but the base branch moves with zero session activity, and "you are now N behind main" is exactly the case where `tmuxUpdated` is false. `issuePickedMsg` and `issueExpandedMsg` (`app/state_issue_picker.go`) can land up to ~20s after the user acted, into whatever is on screen by then; both handlers drop the result — with an explanation, not silently — when `msg.repo != m.repoPath()` (a different workspace is now focused) or `m.state != stateDefault` (another flow already owns the overlay and pending launch-options closure), because applying anyway would let `openLaunchOptionsForNew` silently replace it, stranding an instance unstarted and unreachable. `handleIssuePicked` returns on a fetch error before either guard; `handleIssueExpanded` degrades and continues on error (falls back to the literal, unlinked prompt), which is why its guards run **before** the `err != nil` branch instead of after.
- **x/vt callbacks run under the xvt wrapper's write lock.** `session/vt/xvt.go` registers `Callbacks` that fire inside `emu.Write`; they may only assign wrapper fields — re-locking `e.mu` deadlocks (RWMutex is not reentrant) and calling app code from them violates the no-model-mutation rule. New emulator-sourced state follows the same pattern: callback writes the field, a read-locked accessor exposes it. Events that must *notify* (not just store) follow the bell pattern: the callback sets a pending flag, and `Write`/`Resize` invoke the handler after releasing the lock — invoking under the lock deadlocked the whole UI once (bellFunc → `tea.Program.Send` blocks on the Update goroutine, which was blocked on `e.mu`; `TestBell_FiresOutsideWriteLock` pins this). One known vendored-library bug: the OSC-title parser truncates titles containing non-ASCII bytes (terminates on the lead byte of a multi-byte UTF-8 sequence) — `TmuxSession.PaneTitle` guards this by rejecting invalid UTF-8 rather than forwarding mangled bytes to the host terminal.
- **Inline orphan recovery (no modal).** `reconcileOrphans` (`app/app.go`, called from `activateWorkspace` and classic startup — so it runs on every workspace-load path) auto-cleans stale worktrees and surfaces unsaved/live ones as `Recoverable` list entries. `Recoverable` is **ephemeral**: filtered out of `persistableInstances`, re-derived from disk each load, and inert (`EnsureRunning` no-ops on it). `r` recovers (adopt via `ReconcileAndRestore`), `D` discards (worktree removed, branch kept via `IsExistingBranch`). When adding a per-instance loop, treat `Recoverable` like `Paused`/not-started so it never drives a PTY.
- **Lua LState is not goroutine-safe.** All `script.Engine` dispatch runs under `engine.mu`; the Bubble Tea main loop invokes scripts via a `tea.Cmd` goroutine and awaits `scriptDoneMsg`. Pending instances created by scripts are queued on the `scriptHost` adapter and finalized on the main goroutine in `handleScriptDone` — never call `h.list.AddInstance` from inside the engine.
- **No model mutation from `tea.Cmd` goroutines.** Bubble Tea runs every returned `tea.Cmd` in its own goroutine, concurrent with `Update`/`View` — so a Cmd body must not mutate shared state, nor read unlocked model state (`m.list`, `m.splitPane`, …; `loadSlot` reassigns them). `session.Storage`, `config.State`, and `tmux.TmuxSession` (its `ptmx`/`monitor`, via `stateMu` — snapshot under the lock, never hold it across PTY/subprocess I/O) each carry a mutex. `scriptHost` holds no `*home`: script nav/scroll/diff/workspace primitives record a `func(*home)` via `deferModelMutation`, drained into `scriptDoneMsg` and applied on the main goroutine in `handleScriptDone`, and reads (`ctx:selected()`, `ctx:instances()`, `ctx:config_dir()`, …) come from a snapshot `newScriptHost` takes on the Update goroutine when the dispatch or resume begins. Verify concurrent code with `go test -race` (see Build & Development Commands).
- **Script key collisions.** `cs.bind` / `cs.register_action` overwrite each other — last-write-wins across all scripts and `defaults.lua`. `ctrl+c` is hard-reserved in the default state (app-level) so user scripts cannot steal the interrupt; `keys.KeyForString` is a reverse lookup of the built-in binding table used only for menu-bar highlighting, not for dispatch gating. Duplicate load-order: `defaults.lua` loads first, then `~/.loom/scripts/*.lua` in filename order, so user bindings for the same key win.
- **Pane scroll-back is emulator-owned; the alt-screen filter is ARCHITECTURAL.** tmux client streams live permanently on the alt screen; x/vt only exposes primary-screen scrollback; `vt.NewAltScreenFilter` between pump and emulator is what makes scrollback accumulate at all — removing it silently kills scroll-back (the real-tmux integration test `TestScrollbackAccumulation_RealTmux` pins this). `ScrollModel.AdvanceAndRender` mutates anchor state — exactly once per render pass. TerminalPane's `updateContentSnapshotLocked` releases `t.mu` around CaptureHistory — never add an early return between its Unlock/Lock.
- **Synchronized output defeats emulator scroll-back — hence Claude is forced fullscreen.** Output wrapped in DEC 2026 brackets still scrolls tmux's own history, but tmux relays it to the attach client as a repaint, not scroll sequences, so the emulator's scrollback stays empty and a wheel scroll only shows the footer over an unmoved screen (`TestSyncOutputDefeatsEmulatorScrollback_RealTmux`). Claude's classic renderer brackets every frame this way, so `session.ClaudeFullscreenEnv` (folded into `InstanceEnv`) launches every Claude session with `CLAUDE_CODE_NO_FLICKER=1`; on the alt screen the wheel is forwarded into Claude instead. Always-on, no config: the env var (not `--settings`) keeps `/tui default` working per session, because Claude drops it on renderer relaunch. Any other sync-output app in either pane still can't scroll; the general fix would be sourcing history from tmux instead of the emulator.
- **Tmux prefix transition.** `tmux.TmuxPrefix = "loom_"`, `tmux.LegacyTmuxPrefix = "claudesquad_"`. `tmux.RenameLegacySessions` is centralized in `Storage.LoadAndReconcile`, running before per-record reconcile on every load path, so live sessions survive the flip. The orphan sweep accepts both prefixes.
- **Theme-derived styles must be hook-built.** Any package-level `lipgloss.Style` using a `ui` color role must be constructed inside a `ui.RegisterThemeHook` callback, not in a var initializer — init-time styles capture pre-`ApplyTheme` colors and go stale when the settings overlay switches themes live. Roles only, no literal colors (see `ui/theme.go`).
- **Overview and workbench are `viewMode`s, orthogonal to the state machine.** `m.viewMode` (focus/overview/workbench) is not an `m.state` value — overlays and per-state handlers work unchanged on top of it. In overview, `state_default.go` gates script dispatch through the `overviewKeyAllowed` whitelist (everything else no-ops rather than acting on an invisible pane), mouse events are dropped, and bell/attention badges clear only on entering focus so the attention-sorted grid stays stable while you look at it. New key work must respect both: add grid-safe keys to the whitelist explicitly, and don't clear attention state from overview handlers.
- **Overview cursor and nav share one classic-vs-slots split.** `overviewData`'s cursor translation, `moveCursor`/`fleetOrder`, and `jumpWaiting` all key on the same `len(m.slots)==0` classic-vs-workspace-mode split — keep them in sync or nav and render diverge. A stale overview cursor (killed instance, collapsed group) is healed by `normalizeOverviewCursor` at render (`overviewData`) and nav (`moveCursor`) — don't add per-event cursor bookkeeping. The overview only ever renders open workspace slots; there is no background/lazy fleet loading (removed 2026-07-21).
- **Workbench mode reuses the focus split for its left half.** `viewWorkbench` force-hides the split's terminal (restored from `wbPrevTerminalHidden` on exit — `cleanupWorkbench` is the single teardown choke point, called from `saveCurrentSlot`/`loadSlot` so implicit workspace switches can't leave a half-cleaned workbench) and shares its `TerminalPane` with the right panel via `SplitPane.Terminal()` — `SplitPane.SetSize` must run before `Workbench.SetSize`. The markdown follow scan rides the 3s health tick as a `tea.Cmd`; scan/load/save results are applied only in `Update` handlers, gated on session title to drop stale deliveries. Workbench is never persisted: restart lands in focus. Glamour styles rebuild in a theme hook (`ui/markdown_style.go`); `MarkdownPane` re-renders lazily off the `mdStyleGen` counter. The review tab (`5`/`c`) freezes markdown follow-mode on entry and resumes it on exit (`q`)/teardown, and `q` returns to the panel tab the review was opened from (`home.wbReviewPrevTab`); review persistence runs as Cmds delivering `reviewui.SavedMsg` (non-fatal, footer-surfaced). The pane claims only the keys it acts on (`reviewui.claimsIdleKey`) and declines the rest — idle `esc`, session ops, workspace nav, and (in doc mode) the panel-tab digits all fall through to the workbench. The app-layer `home.wbReview` concrete pane and `Workbench.SetReview` interface field must stay in lockstep (nil iff nil): `Workbench.SetSession` drops its half on a title change, so **every** retarget path must go through `home.dropReviewPane` — `instanceChanged`'s workbench retarget does, and a miss silently routes keys (and `S`'s composed comments) into the previous session's pane.
- **Every tmux exec goes through `tmux.Command`/`tmux.CommandOnSocket`.** They honor `LOOM_TMUX_SOCKET`; a raw `exec.Command("tmux", …)` would follow `$TMUX` to whatever server encloses the process. `TestNoRawTmuxExec` fails the build on any raw tmux exec outside `session/tmux/command.go` (whose `EnclosingSessionName` is the one deliberate exception).
- **The nesting guard exists because the orphan sweep is server-wide.** `CleanupOrphanedSessions` kills every `loom_*` session the process didn't load, on the server it talks to. `main.go`'s `nestingCheck` refuses the TUI and `reset` inside a `loom_*`/`claudesquad_*` session unless `LOOM_TMUX_SOCKET` names a server other than the enclosing one (or `LOOM_ALLOW_NESTED=1`) — a tmux server copies the environment of the client that started it, so every pane of a server started with `LOOM_TMUX_SOCKET` set inherits that same variable, and trusting it unconditionally would wave through a bare run inside its own sandbox server. Run dev builds through `tools/loomdev`, never directly in a pane. The guard only recognizes loom-managed enclosing sessions (`loom_*`/`claudesquad_*`); a dev loom started from a non-loom tmux session (e.g. a plain shell pane) on a server that also hosts loom sessions is not refused, so use `loomdev` (or `LOOM_TMUX_SOCKET`) there too.

### Persistent State

All stored in `~/.loom/`:
- `config.json` — user configuration: `DefaultProgram`, `BranchPrefix` (default: `{username}/`; overridable per session via the Session Launch Options modal — see `overlay.LaunchOptions.BranchPrefix` → `Instance.SetBranchPrefix` → `git.WorktreeSpec.BranchPrefix`, an ephemeral override that is never persisted), `BaseBranch` (default empty, read via `Config.GetBaseBranch()` — the branch new worktrees are cut from; empty auto-detects via `git.ResolveBaseCommit`: `origin/HEAD` → `main` → `master` → current `HEAD`), `Profiles` (named program presets), `Theme` (`"afterglow"` default, `"legacy"`; empty means default, read via `Config.GetTheme()`; cycled live from the settings overlay's Theme row), `ClaudeRemoteControl` (`*bool`, default on — launches Claude sessions with `--remote-control <title>`; nil is treated as enabled, read via `Config.RemoteControlEnabled()`), `ClaudePermissionMode` (`*string`, default `"default"` — launches Claude sessions with `--permission-mode <mode>`; nil is treated as `"default"` (no flag injected), read via `Config.PermissionMode()`; valid values enumerated in `config.ClaudePermissionModes` and cycled from the Claude Preferences overlay), `Claude1MContext` (`*bool`, default off — appends Claude's `[1m]` long-context suffix to the launched `--model` alias, e.g. `sonnet[1m]`; a no-op for `default`, `haiku`, and any alias whose `config.ClaudeModel.Supports1M` is false, read via `Config.Context1MEnabled()`; the composed value is single-quoted because tmux runs the program string through a shell and `[`/`]` are zsh glob metacharacters), `ClaudeSubagentTracking` (`*bool`, default on — launches Claude sessions with loom's subagent hooks; nil is treated as enabled, read via `Config.SubagentTrackingEnabled()`, toggled from the Claude Preferences overlay's Track Subagents row)
- `state.json` — app state (e.g. help screens seen) plus the `ui` prefs block (`config.UIPrefs`: `view_mode`, `rail_hidden`, `terminal_hidden`, and `split_ratios` — a per-session-title map of agent/terminal split ratios, written throttled on `ctrl+up`/`ctrl+down` resizes)
- `instances` (an array inside `state.json`, not a separate file — `config.InstancesFileName` is unused) — serialized session data (`issue`, since schema v6, links a session to the GitHub issue number it was created from)
- `workspaces.json` — registered workspaces with name, path, and last-used tracking
- `worktrees/` — git worktree directories
- `hooks/` — per-instance Claude hook folders for subagent tracking (see `session/subagent`), one per path-escaped tmux session name; emptied at each launch, removed when an instance is killed, swept when unclaimed and its tmux session is gone
- `scripts/` — user-supplied `*.lua` files loaded at startup (global, shared across workspaces)

## Testing Patterns

- Tests use `testify/assert` for assertions
- Dependency injection via interfaces: `cmd.Executor`, `tmux.PtyFactory`
- Constructor variants for testing: `NewTmuxSessionWithDeps()` accepts mock dependencies
- Test setup pattern: `TestMain` initializes logging, runs tests, calls `os.Exit`
- Tests use temp directories for file I/O isolation
- `app` tests never touch the developer's default tmux server: `TestMain` (`app/app_test.go`) points `LOOM_TMUX_SOCKET` at a private server and kills it afterwards; `isolateTmux` gives a test its own fresh server, and `home.cmdExec` swaps the workspace load paths' executor for a recorder

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

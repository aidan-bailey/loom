# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Claude Squad is a terminal UI (TUI) for managing multiple AI coding agents (Claude Code, Aider, Codex, Amp) in parallel. Each agent runs in an isolated git worktree with its own tmux session. Built with Go using the Charmbracelet Bubble Tea framework.

## Build & Development Commands

```bash
# Build
CGO_ENABLED=0 go build -o claude-squad

# Build & run via Nix (no dev shell needed)
nix run .

# Run tests
go test -v ./...

# Run a single package's tests
go test -v ./config
go test -v ./session/git

# Format code (CI enforces this)
gofmt -w .

# Lint (CI uses golangci-lint v1.60.1)
golangci-lint run --timeout=3m --fast

# Version bump (updates main.go)
./bump-version.sh <version>

# Cleanup scripts
./clean.sh        # Kill tmux server, remove worktrees and ~/.claude-squad/
./clean_hard.sh   # Same as clean.sh + git worktree prune

# Install (adds ~/.local/bin to PATH)
./install.sh
```

CGO is disabled for builds (`CGO_ENABLED=0`). Go version is 1.23.0 (toolchain go1.24.1).

A Nix flake (`flake.nix`) provides a dev shell with Go, golangci-lint, tmux, git, and gh.

## CLI Usage

```bash
# Run with default settings
claude-squad

# Specify agent program
claude-squad --program "aider --model ollama_chat/gemma3:1b"

# Enable auto-yes mode (experimental)
claude-squad --autoyes

# Subcommands
claude-squad reset    # Reset all instances, cleanup tmux sessions and worktrees
claude-squad debug    # Print config paths and debug info
claude-squad version  # Print version

# Workspace management
claude-squad workspace add [path]    # Register a git repo as a workspace
claude-squad workspace list          # List registered workspaces
claude-squad workspace remove <name> # Unregister a workspace
claude-squad workspace use <name>    # Set default workspace
claude-squad workspace rename <old> <new>  # Rename a workspace
claude-squad workspace status [name] # Show instance counts
claude-squad workspace migrate       # Migrate instances to workspaces

# Select workspace explicitly
claude-squad --workspace <name>
```

## TUI Keybindings

| Key | Action |
|-----|--------|
| `n` | New instance |
| `N` | New instance with prompt |
| `O` | Full-screen attach (agent) |
| `ctrl+a` | Inline attach to agent pane |
| `ctrl+t` | Inline attach to terminal pane |
| `ctrl+q` | Detach from inline/full-screen attach |
| `r` | Resume paused instance |
| `D` | Kill instance |
| `p` | Push branch |
| `c` | Checkout branch |
| `a` | Quick input bar (send to agent) |
| `t` | Quick input bar (send to terminal) |
| `d` | Toggle diff overlay |
| `up`/`k`, `down`/`j` | Navigate sessions |
| `W` | Workspace picker |
| `h`/`[`, `l`/`]` | Previous/next workspace tab |
| `?` | Help |
| `q` | Quit |

## Environment Variables

- `CLAUDE_SQUAD_HOME` — Override config directory (default: `~/.claude-squad`). Must be absolute path; supports `~` expansion. Used as a backward-compatible fallback; internal code uses explicit `WorkspaceContext` threading.

## Documentation

- [USAGE.md](USAGE.md) — comprehensive TUI guide and CLI reference
- [CONTRIBUTING.md](CONTRIBUTING.md) — contribution guidelines
- [docs/specs/workspaces.md](docs/specs/workspaces.md) — workspace registration, isolation via `WorkspaceContext`, switching, and migration

## Architecture

### Core Flow

`main.go` (Cobra CLI) → `app/app.go` (Bubble Tea Model) → manages `session/instance.go` instances

The app follows Bubble Tea's Model-View-Update pattern. The single-threaded event loop in `app/app.go` (~1500 lines) is the central orchestrator that handles all keyboard input, manages instance lifecycle, and coordinates UI updates. On startup, the app detects the current workspace or prompts the user to select one via the workspace picker overlay.

### Key Packages

- **`app/`** — Bubble Tea application model. Handles all keyboard input dispatch, instance lifecycle management, and UI composition. This is the "controller" layer.
- **`session/`** — Core domain. `Instance` represents a running agent session with status lifecycle (Ready → Loading → Running → Paused). `storage.go` handles JSON serialization to `~/.claude-squad/instances.json`.
- **`session/git/`** — Git worktree operations. Each session gets an isolated worktree in `~/.claude-squad/worktrees/`. Branches are named `{username}/{session_title}`. Handles setup, diff stats, push, and cleanup.
- **`session/tmux/`** — Tmux session management. Creates/attaches terminal sessions, captures pane content, detects prompts (for auto-yes), sends keystrokes. Platform-specific files: `tmux_unix.go`, `tmux_windows.go`.
- **`config/`** — Configuration (`config.json`), state (`state.json`), profiles, and workspace registry (`workspace.go`). Key types: `WorkspaceContext` (carries resolved config dir through the app), `InstanceStorage`, `AppState`, `StateManager`. `LoadConfigFrom("")`/`LoadStateFrom("")` accept empty string as "use default directory".
- **`daemon/`** — Background auto-yes mode. Polls instances, detects prompts, auto-presses Enter. Platform-specific: `daemon_unix.go`, `daemon_windows.go`.
- **`ui/`** — Bubble Tea view components. Left panel (`list.go`, 20% width), right panel (`split_pane.go`, 80% width) with agent and terminal panes stacked vertically (70/30 split) and a hotkey-toggled diff overlay. `quick_input.go` provides an inline input bar for sending text to tmux. `workspace_tab_bar.go` renders workspace tabs. `ui/overlay/` has modal dialogs (text input, confirmation, branch picker, profile picker, workspace picker).
- **`keys/`** — Keybinding definitions. Enum-based `KeyName` with global maps for lookup.
- **`cmd/`** — `Executor` interface wrapping `os/exec` for testability.
- **`log/`** — Centralized logging to `$TMPDIR/claudesquad.log` with Info/Warning/Error loggers and rate limiting.
- **`web/`** — Next.js marketing site, deployed to GitHub Pages via CI.

### Session Lifecycle

Statuses: `Ready` (initial), `Loading` (setup in progress), `Running` (agent active), `Paused` (worktree removed, branch preserved).

1. **New**: User presses `n`/`N` → overlay collects title and optional prompt → status: Ready
2. **Start**: Creates git worktree + tmux session, records base commit → status: Loading → Running
3. **Running**: Agent works in isolated worktree; UI shows live terminal output + diff stats
4. **Pause**: Commits changes, kills tmux session, removes worktree (branch preserved) → status: Paused
5. **Resume**: Recreates worktree from branch, starts new tmux session → status: Running
6. **Kill**: Cleans up worktree, tmux session, and branch; instance removed from storage

**Workspace Terminals**: A special instance type (`IsWorkspaceTerminal: true`) that runs directly in the root repo without a worktree. Cannot be paused/resumed. Diff tracking shows uncommitted changes in the root repo.

### Persistent State

All stored in `~/.claude-squad/`:
- `config.json` — user configuration: `DefaultProgram`, `AutoYes`, `DaemonPollInterval` (ms, default 1000), `BranchPrefix` (default: `{username}/`), `Profiles` (named program presets)
- `state.json` — app state (e.g. help screens seen)
- `instances.json` — serialized session data
- `workspaces.json` — registered workspaces with name, path, and last-used tracking
- `worktrees/` — git worktree directories

## Testing Patterns

- Tests use `testify/assert` for assertions
- Dependency injection via interfaces: `cmd.Executor`, `tmux.PtyFactory`
- Constructor variants for testing: `NewTmuxSessionWithDeps()` accepts mock dependencies
- Test setup pattern: `TestMain` initializes logging, runs tests, calls `os.Exit`
- Tests use temp directories for file I/O isolation

## Code Conventions

- Error wrapping: `fmt.Errorf("context: %w", err)`
- Module path is `claude-squad` (not a URL-based path)
- Platform-specific code in `_unix.go` / `_windows.go` suffixed files
- Private struct fields, public methods (PascalCase)
- Minimal goroutine usage; concurrency mainly in tmux monitoring and daemon polling

## CI/CD

GitHub Actions workflows in `.github/workflows/`:
- **build.yml** — Build and test on push/PR to main (triggered by Go file changes)
- **lint.yml** — golangci-lint on Go code changes
- **release.yml** — Build and publish artifacts on version tags (`v*`)
- **deploy-pages.yml** — Deploy Next.js marketing site when `web/` changes
- **cla.yml** — CLA enforcement for pull requests

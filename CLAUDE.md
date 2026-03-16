# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Claude Squad is a terminal UI (TUI) for managing multiple AI coding agents (Claude Code, Aider, Codex, Amp) in parallel. Each agent runs in an isolated git worktree with its own tmux session. Built with Go using the Charmbracelet Bubble Tea framework.

## Build & Development Commands

```bash
# Build
go build -o claude-squad

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
```

CGO is disabled for builds (`CGO_ENABLED=0`). Go version is 1.23.0.

## Architecture

### Core Flow

`main.go` (Cobra CLI) → `app/app.go` (Bubble Tea Model) → manages `session/instance.go` instances

The app follows Bubble Tea's Model-View-Update pattern. The single-threaded event loop in `app/app.go` (~950 lines) is the central orchestrator that handles all keyboard input, manages instance lifecycle, and coordinates UI updates.

### Key Packages

- **`app/`** — Bubble Tea application model. Handles all keyboard input dispatch, instance lifecycle management, and UI composition. This is the "controller" layer.
- **`session/`** — Core domain. `Instance` represents a running agent session with status lifecycle (Created → Running/Ready → Paused → Killed). `storage.go` handles JSON serialization to `~/.claude-squad/instances.json`.
- **`session/git/`** — Git worktree operations. Each session gets an isolated worktree in `~/.claude-squad/worktrees/`. Branches are named `{username}/{session_title}`. Handles setup, diff stats, push, and cleanup.
- **`session/tmux/`** — Tmux session management. Creates/attaches terminal sessions, captures pane content, detects prompts (for auto-yes), sends keystrokes. Platform-specific files: `tmux_unix.go`, `tmux_windows.go`.
- **`config/`** — Configuration (`config.json`), state (`state.json`), and profiles. Key interfaces: `InstanceStorage`, `AppState`, `StateManager`.
- **`daemon/`** — Background auto-yes mode. Polls instances, detects prompts, auto-presses Enter. Platform-specific: `daemon_unix.go`, `daemon_windows.go`.
- **`ui/`** — Bubble Tea view components. Left panel (`list.go`, 30% width), right panel (`tabbed_window.go`, 70% width) with preview/diff/terminal tabs. `ui/overlay/` has modal dialogs (text input, confirmation, branch picker, profile picker).
- **`keys/`** — Keybinding definitions. Enum-based `KeyName` with global maps for lookup.
- **`cmd/`** — `Executor` interface wrapping `os/exec` for testability.
- **`log/`** — Centralized logging to `$TMPDIR/claudesquad.log` with Info/Warning/Error loggers and rate limiting.

### Session Lifecycle

1. **Create**: User presses `n`/`N` → overlay collects title and optional prompt
2. **Start**: Creates git worktree + tmux session, records base commit
3. **Running**: Agent works in isolated worktree; UI shows live terminal output + diff stats
4. **Pause**: Commits changes, kills tmux session, removes worktree (branch preserved)
5. **Resume**: Recreates worktree from branch, starts new tmux session
6. **Kill**: Cleans up worktree, tmux session, and branch

### Persistent State

All stored in `~/.claude-squad/`:
- `config.json` — user configuration and profiles
- `state.json` — app state (e.g. help screens seen)
- `instances.json` — serialized session data
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

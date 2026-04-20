# USAGE.md

A comprehensive guide to using Loom — the terminal UI for managing multiple AI coding agents in parallel.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [TUI Layout](#tui-layout)
- [Session Lifecycle](#session-lifecycle)
- [Keyboard Reference](#keyboard-reference)
- [Workflows](#workflows)
- [CLI Reference](#cli-reference)
- [Configuration](#configuration)
- [Workspaces](#workspaces)
- [Auto-Yes Mode](#auto-yes-mode)

---

## Overview

Loom lets you run multiple AI coding agents (Claude Code, Aider, Codex, Amp) simultaneously, each in its own isolated git worktree and tmux session. You can create sessions, watch agents work in real time, review diffs, pause/resume sessions, and push completed work — all from a single terminal interface.

### Core Concepts

| Concept | Description |
|---------|-------------|
| **Session** | A running agent instance with its own tmux terminal, git branch, and worktree |
| **Worktree** | An isolated git checkout where the agent works without affecting your main branch |
| **Workspace** | A registered git repository with its own configuration and session storage |
| **Profile** | A named program configuration (e.g. "claude-fast", "aider-gpt4") |

---

## Quick Start

### Build & Run

```bash
# Build
CGO_ENABLED=0 go build -o loom

# Run (from any git repository)
./loom

# Or with Nix
nix run .
```

### First Session in 30 Seconds

1. Launch `loom` from a git repository
2. Press `n` to create a new session
3. Type a name and press `Enter`
4. The agent starts in an isolated worktree — watch its output in the **Agent** pane
5. Press `d` to toggle the **Diff** overlay to see what the agent has changed
6. Press `Ctrl+A` to attach to the agent pane and interact directly
7. Press `Ctrl+Q` to detach back to the TUI
8. Press `c` to checkout (pause) the session when done

---

## TUI Layout

```
┌─────────────────────────────────────────────────────────────────────┐
│  [ Global ]  [ my-project ]  [ other-repo ]    ← Workspace Tabs    │
├────────────────────┬────────────────────────────────────────────────┤
│                    │  Agent  │  Terminal              ← Pane Bar      │
│   INSTANCE LIST    ├────────────────────────────────────────────────┤
│    (30% width)     │                                                │
│                    │                                                │
│  ⟳ fix-auth       │         CONTENT AREA                           │
│    user/fix-auth   │          (70% width)                           │
│    +42 / -15       │                                                │
│                    │  Agent and Terminal panes stacked:              │
│  ⏸ add-tests      │   • Agent — live agent output (ctrl+a attach)  │
│    user/add-tests  │   • Terminal — terminal session (ctrl+t)       │
│    +120 / -8       │   • Diff overlay toggled with d                │
│                    │                                                │
│  ● refactor-api   │                                                │
│    user/refactor   │                                                │
│                    │                                                │
├────────────────────┴────────────────────────────────────────────────┤
│  n new • N prompt • c checkout • r resume • p push • ? help • q     │
│                                                   ← Context Menu    │
├─────────────────────────────────────────────────────────────────────┤
│  Error: something went wrong                      ← Error Bar      │
└─────────────────────────────────────────────────────────────────────┘
```

### Left Panel — Instance List

Each session shows its title, branch name, and diff stats (lines added/removed). Status indicators:

| Icon | Status | Meaning |
|------|--------|---------|
| `⟳` | Running | Agent is actively working |
| `●` | Ready | Waiting for user input |
| `⏳` | Loading | Session is starting up |
| `⏸` | Paused | Worktree removed, branch preserved |

The selected instance is highlighted. Navigate with `↑`/`↓` or `k`/`j`.

### Right Panel — Tabbed Content

- **Agent** — Live read-only view of the agent's tmux output. Press `Ctrl+A` to attach and interact directly; `Ctrl+Q` to detach.
- **Terminal** — Terminal pane for the session. Press `Ctrl+T` to attach; `Ctrl+Q` to detach.
- **Diff** — Toggle with `d` to see git changes since the session started.

### Bottom Menu

Context-sensitive — shows only the actions available for the current state. Keybinding hints update based on the selected instance's status.

### Workspace Tab Bar

Visible only when multiple workspaces are active. Switch between workspace tabs with `[` and `]`. Toggle which workspaces are visible with `W`.

---

## Session Lifecycle

A session moves through these states:

```
         ┌──────┐
         │ n/N  │  User creates session
         └──┬───┘
            ▼
        ┌───────┐
        │ Ready │  Instance created, not yet started
        └──┬────┘
           ▼
       ┌─────────┐
       │ Loading │  Creating worktree + tmux session
       └──┬──────┘
          ▼
      ┌─────────┐  ◄──────────────────────────────┐
      │ Running │  Agent is working                │
      └──┬──┬───┘                                  │
    c    │  │  D                                   │  r
  ┌──────┘  └──────────┐                           │
  ▼                    ▼                           │
┌────────┐       ┌──────────┐                      │
│ Paused │───────│  Killed  │                      │
└──┬─────┘       └──────────┘                      │
   │  r       Branch deleted,                      │
   │          worktree removed,                    │
   │          tmux session destroyed               │
   └───────────────────────────────────────────────┘
```

### What Happens at Each Stage

**Ready → Loading → Running** (on creation):
1. Git worktree created at `~/.loom/worktrees/{name}_{timestamp}`
2. New branch created: `{branch_prefix}{session_title}` (default prefix: `username/`)
3. Base commit SHA recorded (used as the baseline for diffs)
4. Tmux session launched running the configured program
5. Agent begins working in the isolated worktree

**Running → Paused** (on checkout, `c`):
1. Any uncommitted changes are staged and committed locally
2. Tmux session is detached
3. Worktree directory is removed (saves disk space)
4. Branch is preserved in git — all work is safe
5. Branch name is copied to your clipboard

**Paused → Running** (on resume, `r`):
1. Worktree recreated from the preserved branch
2. Tmux session restored or recreated
3. Diff baseline preserved — you see cumulative changes since session creation
4. Agent picks up where it left off

**Running/Paused → Killed** (on kill, `D`):
1. Tmux session destroyed
2. Worktree removed
3. Branch deleted (unless it was a pre-existing branch you selected at creation)
4. Instance removed from storage

### Workspace Terminals

When you launch Loom inside a registered workspace, a special instance is pinned at the top of the instance list — the **Workspace Terminal**. Unlike regular sessions, it runs directly in the root of the repository without creating a git worktree:

- **No worktree** — the agent operates on your main checkout, so changes are immediately visible to other tools working in that directory.
- **Cannot be paused or killed** — the workspace terminal is a permanent fixture while the workspace exists. `c` and `D` are no-ops when it is selected.
- **Diff stats** reflect the workspace's uncommitted changes against HEAD, not a cumulative diff against a base commit.
- **Auto-recreated** — if its tmux session is missing at startup (e.g. after a reboot), Loom recreates it in place.

Use the workspace terminal for work that needs unrestricted access to the root checkout (ad-hoc shell commands, editors, or an agent you want full repo visibility for). Create standard sessions with `n` / `N` for anything that should stay isolated in a worktree.

---

## Keyboard Reference

### Default State

| Key | Action |
|-----|--------|
| `↑` / `k` | Move selection up |
| `↓` / `j` | Move selection down |
| `n` | Create new session (name only) |
| `N` | Create new session with prompt, profile, and branch picker |
| `Ctrl+A` | Inline attach to agent pane |
| `Ctrl+T` | Inline attach to terminal pane |
| `O` | Full-screen attach (agent) |
| `a` | Quick input to agent |
| `t` | Quick input to terminal |
| `c` | Checkout — commit changes and pause session |
| `r` | Resume a paused session |
| `p` | Push branch to remote (with confirmation) |
| `D` | Kill selected session (with confirmation) |
| `d` | Toggle diff overlay |
| `W` | Open workspace picker |
| `l` / `[` | Previous workspace tab |
| `;` / `]` | Next workspace tab |
| `?` | Show help screen |
| `q` | Quit |

### Name Entry Mode (after pressing `n` or `N`)

| Key | Action |
|-----|--------|
| *Type characters* | Enter session name (max 32 chars) |
| `Enter` | Submit name and start session |
| `Backspace` | Delete last character |
| `Ctrl+C` / `Esc` | Cancel |

### Prompt Overlay (after pressing `N` and entering a name)

The overlay has four focus areas. Press `Tab` to cycle between them:

1. **Profile Picker** — `←` / `→` to select a profile (if configured)
2. **Prompt Text Area** — Type your instructions for the agent
3. **Branch Picker** — Type to filter branches, `↑` / `↓` (or `k` / `j`) to select
4. **Submit** — `Enter` to start

| Key | Action |
|-----|--------|
| `Tab` | Cycle between focus areas |
| `Enter` | Submit prompt and start session |
| `Ctrl+C` | Cancel |

### Inline Attach (after pressing `Ctrl+A` or `Ctrl+T`)

| Key | Action |
|-----|--------|
| `Ctrl+Q` | Detach from session and return to TUI |
| *All other keys* | Sent directly to the tmux session |

### Confirmation Modal (kill, push)

| Key | Action |
|-----|--------|
| `y` | Confirm action |
| `n` / `Esc` | Cancel |

### Workspace Picker

**On startup** (single-select):

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Navigate |
| `Enter` | Select workspace |
| `Esc` | Use global (default) |

**Mid-session** (multi-select toggle, `W`):

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Navigate |
| `Space` | Toggle workspace active/inactive |
| `Esc` / `q` | Apply changes |

---

## Workflows

### Create a Simple Session

```
n → type "fix-auth-bug" → Enter
```

A new session starts immediately with the default program in a fresh worktree.

### Create a Session with Prompt and Branch

```
N → type "add-validation" → Enter
  → [select profile with ←/→]
  → type prompt: "Add input validation to the /api/users endpoint"
  → Tab to branch picker → type "feat" to filter → select branch
  → Enter to submit
```

The agent starts with your prompt pre-loaded. If you selected an existing branch, the worktree is created from that branch instead of HEAD.

### Watch an Agent Work

1. Select the session with `↑`/`↓`
2. The **Agent** pane shows live terminal output (default view)
3. Press `d` to toggle the **Diff** overlay — see what files changed and how many lines were added/removed

### Interact with an Agent

```
Select session → Ctrl+A (agent) or Ctrl+T (terminal)
```

You're now inside the tmux session. Type naturally to communicate with the agent. Press `Ctrl+Q` to return to the TUI without stopping the agent.

### Pause and Resume

**Pause** — saves everything and frees disk space:
```
Select running session → c
```
Changes are committed, worktree is removed, branch name is on your clipboard. The agent's tmux session remains in the background.

**Resume** — picks up where you left off:
```
Select paused session → r
```
Worktree is recreated from the branch, tmux session is restored.

### Push to Remote

```
Select session → p → y (confirm)
```

Commits any pending changes with a timestamp message and pushes the branch to origin. You can then open a PR from the pushed branch.

### Kill a Session

```
Select session → D → y (confirm)
```

Destroys the tmux session, removes the worktree, and deletes the branch (unless it was pre-existing). This is irreversible.

### Work Across Multiple Workspaces

```
W → Space to toggle workspaces on/off → Esc
[ / ] to switch between active workspace tabs
```

Each workspace tab shows only the sessions for that repository.

---

## CLI Reference

### Usage

```
loom [flags]
loom [command]
```

### Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--program <prog>` | `-p` | Program to run in new sessions (e.g. `aider --model gpt-4`) |
| `--autoyes` | `-y` | [Experimental] Auto-accept all agent prompts |
| `--workspace <name>` | `-w` | Select workspace by name (bypasses auto-detection) |

### Commands

| Command | Description |
|---------|-------------|
| `version` | Print version number |
| `debug` | Print config paths and loaded configuration |
| `reset` | Reset all instances, cleanup tmux sessions and worktrees |
| `workspace` | Manage workspaces (see below) |

### Workspace Subcommands

| Command | Description |
|---------|-------------|
| `workspace add [path]` | Register a git repo as a workspace (defaults to current dir) |
| `workspace add --name <n> [path]` | Register with a custom name |
| `workspace list` | List all registered workspaces |
| `workspace remove <name>` | Unregister a workspace (data preserved) |
| `workspace use <name>` | Set the default workspace |
| `workspace rename <old> <new>` | Rename a workspace |
| `workspace status [name]` | Show instance counts (defaults to CWD workspace) |
| `workspace migrate` | Move global instances to their matching workspace directories |

### Examples

```bash
# Run with a specific agent
loom -p "aider --model ollama_chat/gemma3:1b"

# Run with auto-yes in a specific workspace
loom -y -w my-project

# Register current directory as a workspace
loom workspace add

# Register a specific path with a custom name
loom workspace add --name backend ~/projects/api-server

# Check how many sessions are running
loom workspace status my-project
```

---

## Configuration

Configuration is stored in `~/.loom/config.json` (or per-workspace at `<repo>/.loom/config.json`).

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `default_program` | string | `"claude"` | Program to run in new sessions. Can be a profile name. |
| `auto_yes` | bool | `false` | Auto-accept agent prompts via background daemon |
| `daemon_poll_interval` | int | `1000` | Milliseconds between prompt checks (lower = more responsive) |
| `branch_prefix` | string | `"{username}/"` | Prefix for auto-generated branch names |
| `profiles` | array | `[]` | Named program configurations |

### Example config.json

```json
{
  "default_program": "claude",
  "auto_yes": false,
  "daemon_poll_interval": 1000,
  "branch_prefix": "aidanb/",
  "profiles": [
    {
      "name": "aider-gpt4",
      "program": "aider --model gpt-4"
    },
    {
      "name": "claude-fast",
      "program": "claude --fast"
    }
  ]
}
```

### Profiles

A profile is a named shortcut for a program invocation. Profiles serve two purposes:

1. **As the default program.** If `default_program` matches a profile name, Loom resolves it to the profile's `program` string when starting new sessions. This keeps the default readable (`"claude-fast"` instead of `"claude --fast --experimental"`).
2. **As a picker in the prompt overlay.** When you press `N` to create a session with a prompt, the overlay exposes a profile picker (`←` / `→`). Pick a profile and the session launches with that profile's program.

Profiles are defined in the `profiles` array; each entry needs a unique `name` and a `program` string. There is no inheritance or templating — each profile is a flat, literal command.

### Branch Prefix

`branch_prefix` is prepended to every auto-generated branch name. The value shown as the default — `{username}/` — is a placeholder for the rendered text: when Loom creates its config, it resolves your OS username and writes the literal value (e.g. `aidanb/`) into `config.json`. There is no runtime token expansion, so editing `branch_prefix` to anything you like (e.g. `"loom/"`, `"wip-"`) works as expected.

The resulting branch for a session titled `fix-auth` with the default prefix would be `aidanb/fix-auth`.

### Environment Variables

| Variable | Description |
|----------|-------------|
| `LOOM_HOME` | Override the config directory (default: `~/.loom`). Must be an absolute path; supports `~` expansion. |

---

## Workspaces

Workspaces provide per-repository isolation. Each workspace gets its own config, state, and session storage inside the repository at `<repo>/.loom/`.

### Directory Structure

```
~/.loom/                     ← Global (fallback)
  config.json
  state.json
  workspaces.json                    ← Workspace registry
  worktrees/

~/projects/my-app/.loom/     ← Workspace-scoped
  config.json                        ← Overrides global config
  state.json                         ← This workspace's sessions
  worktrees/
```

### Auto-Detection

When you run `loom` from a directory:
1. The registry is checked for a workspace matching the current path
2. If found, that workspace's config directory is used
3. If not found, the global `~/.loom/` directory is used

Use `--workspace <name>` to explicitly select a workspace regardless of your current directory.

### Workspace Picker

On launch, if workspaces are registered, a picker appears to select which workspace to use. Press `Esc` to use the global default.

During a session, press `W` to toggle which workspaces are visible as tabs. Use `[` and `]` to switch between active tabs.

### Migration

If you have existing sessions in the global directory and want to move them to workspaces:

```bash
# First register your workspaces
loom workspace add ~/projects/frontend
loom workspace add ~/projects/backend

# Then migrate — matches instances by repo path
loom workspace migrate
```

Instances are matched to workspaces by their repository path. Unmatched instances remain in global storage.

---

## Auto-Yes Mode

Auto-yes mode uses a background daemon to automatically accept agent prompts (e.g. "Do you want to proceed?") without manual intervention.

### Enable

```bash
# Via CLI flag
loom --autoyes

# Via config
# Set "auto_yes": true in config.json
```

### How It Works

1. A background daemon process is launched when the TUI starts
2. The daemon polls all running sessions every `daemon_poll_interval` ms (default: 1000ms)
3. When it detects a program-specific prompt pattern, it sends a carriage return (`Enter`)
4. Diff stats are recalculated after each automatic acceptance

### Detected Prompt Patterns

| Agent | Prompt Pattern |
|-------|---------------|
| Claude Code | "No, and tell Claude what to do differently" |
| Aider | "(Y)es/(N)o/(D)on't ask again" |
| Gemini | "Yes, allow once" |

Trust prompts (e.g. "Do you trust the files in this folder?") are handled separately and auto-dismissed for all agents.

### Daemon Lifecycle

- **Starts** when the TUI launches with auto-yes enabled
- **Runs** in the background as a separate process (PID stored in `{configDir}/daemon.pid`)
- **Stops** when the TUI exits, or manually via `loom reset`
- Any running daemon is killed and restarted on each TUI launch to ensure a clean state

### Troubleshooting

- **Log location** — The daemon writes to the same file as the TUI (`{configDir}/logs/loom.log`). Daemon-emitted records carry `component=daemon`, so `grep component=daemon ~/.loom/logs/loom.log` narrows output to the daemon.
- **Enable debug logging** — Set `LOOM_LOG_LEVEL=debug` or pass `--log-level=debug` when launching Loom; the level is inherited by the daemon child process, so both TUI and daemon become verbose.
- **Healthy output** — Each poll tick logs its tracked instance count; when the daemon detects and dismisses a prompt, it logs the matched agent and pattern.
- **Manual stop** — `kill "$(cat ~/.loom/daemon.pid)"` stops the current daemon. Because the TUI restarts the daemon on each launch, relaunching Loom will start a fresh one automatically.
- **Force full reset** — `loom reset` removes the PID file, worktrees, and instance state. Use this if the daemon appears wedged and a simple relaunch does not recover.

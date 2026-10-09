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
| **Daemon** | The background process (`loom serve`) that owns every session. `loom` starts it when none runs, and it keeps running after you quit (see [The Loom Daemon](#the-loom-daemon)) |

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
8. Press `s` to stash (pause) the session when done

---

## TUI Layout

Loom has three view modes:

- **Focus mode** (the default, shown below) — a session rail on the left plus the selected session's agent and terminal panes on the right. Toggle to overview with `tab`.
- **Overview mode** — a full-width fleet card grid for triaging many sessions at once. See [Overview Mode](#overview-mode).
- **Session workbench** — a single-session deep-dive: the agent pane on the left, a tabbed panel (markdown / diff / files / terminal / review) on the right. Press `enter` in focus mode to open it. See [Session Workbench](#session-workbench).

```
┌─────────────────────────────────────────────────────────────────────┐
│  [ Global ]  [ my-project ]  [ other-repo ]    ← Workspace Tabs    │
├────────────────────┬────────────────────────────────────────────────┤
│                    │  fix-auth · user/fix-auth · +42/-15 ← Title    │
│   SESSION RAIL     ├────────────────────────────────────────────────┤
│    (20% width)     │                                                │
│                    │              AGENT PANE                        │
│ ▌fix-auth          │          live agent output                     │
│ ▌❯ awaiting input  │        (ctrl+a to interact)                    │
│                    │                                                │
│ ▌add-tests         ├────────────────────────────────────────────────┤
│ ▌✻ working         │             TERMINAL PANE                      │
│ ▌ …output tail…    │           (ctrl+t to interact;                 │
│                    │   hide with T, resize with ctrl+↑ / ctrl+↓)    │
│  other-repo · 3    │                                                │
├────────────────────┴────────────────────────────────────────────────┤
│  n new • N prompt • s stash • r resume • p push • ? help • q        │
│                                                   ← Context Menu    │
├─────────────────────────────────────────────────────────────────────┤
│  Error: something went wrong                      ← Error Bar      │
└─────────────────────────────────────────────────────────────────────┘
```

### Left Panel — Session Rail

Each session renders as a live mini-card: its title, a status line with wait age (e.g. `❯ awaiting input · 4m`, `✻ working`, `✓ idle`, `paused · 3d`, `⟲ recoverable`), and a tail of recent agent output. When Claude says why it is waiting, the reason replaces the generic phrase (`❯ sandbox request · 4m`). When a Claude session stops, the tail shows the end of Claude's last message instead of the screen. When a Claude session has live subagents or teammates, the status line replaces the tail and ends with a count, e.g. `✻ working · 3 agents (1 idle)`. The colored accent bar on the card's left edge encodes state at a glance:

| Accent | Meaning |
|--------|---------|
| Gold | Needs your input (prompting or bell) |
| Blue | Selected session |
| Green | Agent running |
| Purple | Workspace terminal |

Branch name and diff stats moved off the rail — they now live in the agent pane's title bar and on overview cards. Dimmed one-line summaries of your other (peer) workspaces sit at the bottom of the rail. Navigate with `↑`/`↓` or `k`/`j`; hide the rail entirely with `\` for a full-width pane view. Rail visibility persists per workspace.

### Overview Mode

Press `tab` to switch to overview: a card grid of every session in your open workspaces, one group per workspace (focused workspace first, the rest alphabetical), each group sorted so sessions needing attention come first. Each card shows the title, status with wait age, branch and diff stats, and a live output tail. While a Claude session has live subagents, the tail shows them instead: `✻` for working, `◦` for idle, with a `+N more` line when more than two are running. Only workspaces open in the tab bar appear — use `W` to open more.

- `j`/`k` (or `↑`/`↓`) walk the sorted grid across all groups; `enter` returns to focus mode on the selected session (switching workspace tabs if it lives in another group); `esc` returns to focus mode where you left it.
- `z` collapses/expands the active workspace's group.
- `]` / `[` jump to the next/previous agent waiting for input, same as in focus mode.
- `n`/`N` drop back to focus mode first, then open the create flow.
- Focus-only keys (attach, quick input, scroll, diff, file explorer) are inactive here, and mouse input is ignored in overview (v1).

The current view mode persists per workspace — switching workspaces restores whichever mode that workspace last used, and it survives restarts. Workbench mode is the exception — see below.

### Session Workbench

Press `enter` on a selected session in focus mode to open the workbench: a single-session deep-dive with the agent pane on the left and a tabbed panel on the right (default 50/50 split). It reuses the focus-mode split's terminal for its Terminal tab, so the split's own terminal pane is hidden for the duration.

```
┌─────────────────────────────────────────────────────────────────────┐
│                    │  1 Markdown  2 Diff  3 Files  4 Terminal        │
│     AGENT PANE     ├────────────────────────────────────────────────┤
│   (ctrl+a/i to     │                                                │
│     interact)      │              PANEL CONTENT                     │
│                     │      (markdown / diff / files / terminal)      │
│                     │                                                │
└─────────────────────────────────────────────────────────────────────┘
```

- **Tabs** — `1` Markdown, `2` or `d` Diff, `3` Files, `4` (or `t`/`ctrl+t`) Terminal, `5` Review (opened on demand — see [Reviewing](#reviewing)).
- **Markdown tab** — follows the most-recently-modified markdown file in the session's worktree by default (a lightweight scan rides the same 3-second health tick used elsewhere). Press `f` to resume following after you've pinned a file; opening a file from the Files tab (`enter` on a `.md` entry) pins it until you follow again. Press `e` to edit in place; `ctrl+s` saves, `esc` cancels (prompting to discard if the buffer is dirty). If the file changed on disk since it was loaded, saving prompts to overwrite rather than silently clobbering the external edit — choose overwrite or cancel (there's no separate "reload"; cancel, then `esc` out and let follow/re-open pick up the latest content). Press `c` to review the currently-shown document with inline comments — see [Reviewing](#reviewing).
- **Diff tab** — the same git-diff view as focus mode's `d` overlay, scoped to this session.
- **Files tab** — a flat listing of the session's worktree; `enter` on a markdown file opens it in the Markdown tab.
- **Terminal tab** — the shared terminal pane; `t` opens the quick-input bar, `ctrl+t` inline-attaches, `alt+t` full-screen attaches, same as focus mode. Mouse wheel and hardware cursor are unavailable on this tab in v1.
- **Resize** — `ctrl+left` / `ctrl+right` adjust the agent/panel split in 5% steps (20–80% range), remembered per session title.
- **Navigation** — `g`/`G` jump to the top/bottom of the active tab; `pgup`/`pgdown` page the markdown and diff tabs; mouse wheel scrolls the pane under the cursor (agent pane on the left, active tab on the right). `]`/`[` still jump to the next/previous agent waiting for input — within the same workspace slot this retargets the workbench to that session (closing any open review, since it belonged to the session you left), while a cross-workspace jump exits cleanly to focus mode on the target. On the Review tab `]`/`[` are prev/next comment instead.
- **Exit** — `esc` returns to focus mode; `tab` exits straight to overview. Workbench mode is never persisted across restarts, and it does not survive an implicit workspace switch (workspace nav, the picker, or a cross-workspace `]`/`[` jump lands you in the target workspace's focus mode instead).

#### Reviewing

The Review tab is a full code-review UI (vendored from the [crit](https://github.com/kevindutra/crit) project) for leaving inline comments on a session's changes, either a single document or the whole worktree diff.

There are three ways in:

- **`c` on the Markdown tab** — reviews the currently-shown document, with comments anchored to its lines.
- **`5` in the workbench** — opens a multi-file code review over the session worktree's changes against `HEAD` (or, if a review is already open, just switches to the Review tab).
- **`c` in focus mode** — jumps straight into the workbench and opens the code review for the selected session in one step.

Once inside the Review tab:

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Move |
| `Shift+↑` / `Shift+↓` | Half-page up/down |
| `g` / `G` | Top / bottom |
| `[` / `]` | Previous / next comment |
| `n` / `N` | Next / previous change (code review) |
| `Tab` / `Shift+Tab` | Next / previous file (code review) |
| `s` | Toggle the sidebar |
| `v` | Visual mode — select a range of lines |
| `Enter` | Add/edit a comment (or confirm a selection in visual mode) |
| `/` | Search file tabs (code review) |
| `Esc` | Cancel the current action |
| `q` | Save and close — returns to the panel tab you opened the review from (Markdown by default) |
| `S` | Send the review comments to the session's agent (with confirmation) |

Keys the review doesn't use fall through to the workbench, so session-lifecycle keys, attach, quick input and workspace navigation keep working while a review is open. Which keys those are depends on the mode:

- **Document review** (`c`) — the panel-tab digits `1`–`4` and `Tab` behave as usual (switch tabs / leave to the overview); the review stays open and `5` returns to it.
- **Code review** (`5` / focus-mode `c`) — the pane owns digits `1`–`9` (jump to a file tab) and `Tab`/`Shift+Tab` (cycle file tabs), so press `q` first if you want to switch panel tabs.

Note that `[` / `]` inside the Review tab are previous/next *comment*, not the waiting-agent jump.

Opening a review freezes the Markdown tab's follow mode, so line anchors can't shift under a live agent while you're commenting; `q` resumes following. `S` composes all outstanding comments into a single prompt and sends it to the agent's pane after you confirm — the session must be running: a paused or recoverable session refuses to open a review and says so in the status area.

Comments are stored as YAML under `.crit/` in the session's worktree. That directory self-gitignores, and its layout is interop-compatible with the upstream `crit` CLI, so an agent invoked to run `crit` in the same worktree sees the same comments and (for code reviews) the same file set.

### Right Panel — Agent & Terminal Panes

- **Agent** — Live view of the agent's tmux output. Its title bar shows the session's branch and diff stats. Press `Ctrl+A` (or `i`) to attach and interact directly; `Ctrl+Q` to detach.
- **Terminal** — Terminal pane for the session. Press `Ctrl+T` to attach; `Ctrl+Q` to detach. Show/hide it with `T`; resize the agent/terminal split with `Ctrl+↑` / `Ctrl+↓` (the ratio is remembered per session).
- **Diff** — Toggle with `d` to see git changes since the session started.

### Bottom Menu

Context-sensitive — shows only the actions available for the current state. Keybinding hints update based on the selected instance's status.

### Workspace Tab Bar

Visible only when multiple workspaces are active. Switch between workspace tabs with `{` and `}` (or `l` and `;`). Toggle which workspaces are visible with `W`.

### Native Terminal Behavior

The focused pane shows your terminal's **real cursor** — native blink, color,
and shape (bar/underline/block follow the app's DECSCUSR setting, and apps
that hide their cursor hide yours). The cursor only appears at the live tail;
scrolling back or opening an overlay hides it.

The host window title mirrors the selected agent's own title (e.g. Claude's
status line). A backgrounded session that rings the terminal bell gets a ●
attention badge in the session list until you select it. Apps that enable
focus reporting (like Claude Code) receive real focus in/out events when you
focus/unfocus Loom's window, switch panes, or switch sessions — so idle
notifications fire correctly.

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
    s    │  │  D                                   │  r
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
2. New branch created: `{branch_prefix}{session_title}` (default prefix: `username/`). The prefix can be changed for this one session in the Session Launch Options modal.
3. Base branch resolved (`base_branch`, or auto-detected) and its commit SHA recorded — this is both where the worktree starts and the baseline for diffs
4. Tmux session launched running the configured program
5. Agent begins working in the isolated worktree

**Running → Paused** (on stash, `s`):
1. Any uncommitted changes (tracked and untracked) are stashed via `git stash`
2. Tmux session is detached
3. Worktree directory is removed (saves disk space)
4. Branch is preserved in git — all work is safe
5. Branch name is copied to your clipboard
6. Claude's temp dir for the session (its scratchpad and task output under `/tmp/claude-<uid>/`) is zipped into the archive and removed — see [Claude Temp-Dir Archives](#claude-temp-dir-archives)

**Paused → Running** (on resume, `r`):
1. Worktree recreated from the preserved branch
2. Stashed changes are restored
3. Tmux session restored or recreated
4. Diff baseline preserved — you see cumulative changes since session creation
5. Agent picks up where it left off
6. Claude's archived scratchpad is restored before the agent starts

**Running/Paused → Killed** (on kill, `D`):
1. Tmux session destroyed
2. Worktree removed
3. Branch deleted (unless it was a pre-existing branch you selected at creation)
4. Instance removed from storage
5. Claude's temp dir is archived (a paused session's archive simply stays)

There is one more state that appears only after a crash or lost state
file: **Recoverable** (`⟲`) — a worktree found on disk that Loom isn't
tracking. It sits inertly in the list until you recover (`r`) or discard
(`D`) it; see [Session Recovery](#session-recovery-orphaned-worktrees).

### Workspace Terminals

When you launch Loom inside a registered workspace, a special instance is pinned at the top of the instance list — the **Workspace Terminal**. Unlike regular sessions, it runs directly in the root of the repository without creating a git worktree:

- **No worktree** — the agent operates on your main checkout, so changes are immediately visible to other tools working in that directory.
- **Cannot be paused or killed** — the workspace terminal is a permanent fixture while the workspace exists. `s` and `D` are no-ops when it is selected.
- **Diff stats** reflect the workspace's uncommitted changes against HEAD, not a cumulative diff against a base commit.
- **Auto-recreated** — if its tmux session is missing at startup (e.g. after a reboot), Loom recreates it in place.

Use the workspace terminal for work that needs unrestricted access to the root checkout (ad-hoc shell commands, editors, or an agent you want full repo visibility for). Create standard sessions with `n` / `N` for anything that should stay isolated in a worktree.

---

## Keyboard Reference

### Default State (focus mode)

| Key | Action |
|-----|--------|
| `↑` / `k` | Move selection up |
| `↓` / `j` | Move selection down |
| `n` | Create new session (name only) |
| `N` | Create new session with prompt, profile, and branch picker |
| `I` | Create new session from a GitHub issue (picker; needs `gh` auth) |
| `Tab` | Toggle overview mode (fleet card grid) |
| `]` / `[` | Jump to next/previous agent waiting for input (prompting or bell; wraps) |
| `i` / `Ctrl+A` | Inline attach to agent pane |
| `Ctrl+T` | Inline attach to terminal pane |
| `Alt+A` / `Alt+T` | Full-screen attach (agent / terminal) |
| `a` | Quick input to agent |
| `t` | Quick input to terminal |
| `\` | Show/hide the session rail |
| `T` | Show/hide the terminal pane |
| `Ctrl+↑` / `Ctrl+↓` | Resize the agent/terminal split (remembered per session) |
| `s` | Stash — stash changes and pause session |
| `m` | Merge another session's branch into the current one |
| `r` | Resume a paused session / recover an orphaned (`⟲`) session |
| `R` | Resume a paused session with different launch options |
| `p` | Push branch to remote (with confirmation) |
| `D` | Kill selected session (with confirmation); on an orphaned (`⟲`) session: discard its worktree, keeping the branch |
| `d` | Toggle diff overlay |
| `c` | Open the workbench code review for the selected session |
| `W` | Open workspace picker |
| `S` | Open settings (edit config.json: Default Program, Branch Prefix, Base Branch, Theme, Profiles, Claude Preferences) |
| `{` / `l` | Previous workspace tab |
| `}` / `;` | Next workspace tab |
| `?` | Show help screen |
| `q` | Quit |

### Overview Mode (after pressing `Tab`)

Session-lifecycle keys (`D`, `r`, `R`), workspace keys, and `q`/`?`/`W`/`S` keep working; keys that act on an invisible pane (attach, quick input, scroll, diff, file explorer) are inactive.

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Walk the attention-sorted card grid |
| `Enter` / `Esc` | Return to focus mode on the selected session |
| `Tab` | Return to focus mode |
| `z` | Collapse/expand the active workspace group |
| `]` / `[` | Jump to next/previous agent waiting for input |
| `n` / `N` | Drop to focus mode, then open the create flow |

### Session Workbench (after pressing `Enter` on a selected session)

Session-lifecycle keys (`D`, `r`, `R`, `p`, `s`, `m`), attach (`i`/`Ctrl+A`/`Alt+A`), quick input (`a`), workspace keys, `]`/`[`, and `q`/`?`/`W`/`S` keep working; layout keys that address the hidden focus-mode chrome (`\`, `T`, list paging) are inactive. The Review tab passes most of those keys through, with these exceptions: `S` sends the review comments to the agent instead of opening settings, `q` closes the review instead of quitting, `]`/`[` move between comments instead of jumping to a waiting agent, and `s` toggles the comment sidebar instead of stash-and-pause (in a code review, `n`/`N` jump between changes instead of creating instances). In a code review the pane also owns `1`–`9` and `Tab`/`Shift+Tab` (file tabs) — see [Reviewing](#reviewing) for the full set of review keys.

| Key | Action |
|-----|--------|
| `1` | Markdown tab |
| `2` / `d` | Diff tab |
| `3` | Files tab |
| `4` / `t` / `Ctrl+T` | Terminal tab (`t` also opens the quick-input bar, `Ctrl+T` also inline-attaches) |
| `5` | Review tab — open a code review of the worktree's changes vs `HEAD` (or reopen the active review); `q` closes it and returns to the tab you came from. See [Reviewing](#reviewing) |
| `Alt+T` | Full-screen attach to the terminal tab |
| `e` | Edit the markdown tab's document |
| `f` | Resume following the worktree's most-recently-modified markdown file |
| `c` | (Markdown tab) Review the shown document with inline comments |
| `Enter` | On the Files tab: open the file under the cursor (markdown files load into the Markdown tab) |
| `↑` / `k`, `↓` / `j` | Scroll the active tab (or move the file cursor on the Files tab) |
| `g` / `G` | Jump to top/bottom of the active tab |
| `PgUp` / `PgDn` | Page the Markdown or Diff tab |
| `Ctrl+←` / `Ctrl+→` | Resize the agent/panel split (remembered per session) |
| *(mouse wheel)* | Scroll the pane under the cursor — agent pane on the left, active tab on the right (no-op on the Terminal tab) |
| `Ctrl+S` | (editing) Save the markdown document |
| `Esc` | (editing) Cancel the edit, confirming first if there are unsaved changes; (otherwise) return to focus mode |
| `Tab` | Exit straight to overview mode |
| `n` / `N` | Drop to focus mode, then open the create flow |

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
Select running session → s
```
Changes are stashed, worktree is removed, branch name is on your clipboard. The agent's tmux session remains in the background.

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

### The Loom Daemon

Your sessions belong to a background process, the loom daemon
(`loom serve`), not to the loom you are looking at. The first `loom` you
start launches it, and it keeps running after you quit, until you stop
it. There is one daemon per global config folder (`~/.loom`, or
`LOOM_GLOBAL_DIR`). While it starts, which loads every workspace, loom
says `loom: waiting for the loom daemon (it loads every workspace as it
starts)…`.

- **Several terminals at once.** Start loom in as many terminals as you
  like, on your desktop and over SSH from a laptop, say. Each one is a
  client of the same daemon: they show the same sessions, and what one
  does, the others see. Each keeps its own tabs and selection. A tmux
  window takes the size of whichever terminal used it last.
- **Quitting** (`q`) closes only this loom. Your agents keep running, and
  the daemon keeps watching them: it pauses an agent that exits, restarts
  a workspace terminal that dies, polls GitHub, and saves.
- **Stopping the daemon**: `loom serve stop`. The daemon finishes any
  pause or kill in progress (up to 30 seconds), saves and exits. Your
  agents keep running in tmux. Every open loom then prints `loom: the
  daemon stopped (see ~/.loom/logs/serve.log); your sessions keep running.
  Run loom again.` and exits, and the next `loom` starts a new daemon,
  which picks the sessions back up. A daemon that crashes ends the same
  way. Don't run `loom serve stop` from inside one of loom's own panes
  unless you mean it: it stops the daemon every open loom uses.
- **Upgrading.** A newer loom replaces an older daemon with its own build
  (`loom: replacing the loom daemon (…) with this build…`), and a loom of
  the old build still open exits as above. An older loom refuses a newer
  daemon (`the loom daemon is …, newer than this loom (…): upgrade loom,
  or run loom serve stop`). Of two builds of one release, the one of the
  later commit is newer; a build that names no commit (a plain `go build`
  from a source tarball) keeps the daemon that runs, so run
  `loom serve stop` to switch to it. A Nix build of loom names the commit
  it was built from, so a newer one replaces the daemon as a release does.
  A loom started inside one of loom's own tmux sessions never replaces the
  daemon: run the new loom from an ordinary terminal, or stop the daemon
  first.
- **The daemon's environment** is the one it started with: that of the
  terminal or SSH login whose `loom` launched it. It decides what your
  agents get for `SSH_AUTH_SOCK`, `DISPLAY` and the like (the rest of
  their environment is the tmux server's), the Claude
  credential warning (see [Run Sessions on Several Claude
  Accounts](#run-sessions-on-several-claude-accounts)), and where Claude's
  temp directories are looked for. Starting loom later from another
  terminal changes none of it. To pick up a new environment (a new SSH
  agent, say), run `loom serve stop`, then start loom from the terminal
  you want. The shell in the terminal pane is the exception: the loom you
  are looking at starts it, with its own `SSH_AUTH_SOCK`, `DISPLAY` and
  the like.
- **Where it lives.** While it runs, the daemon holds `~/.loom/loom.lock`
  (with its pid, socket and build, which `loom debug` prints), listens on
  a private socket (in `$XDG_RUNTIME_DIR/loom/`, else `~/.loom/run/`,
  else `loom-<uid>/` in the temp dir), and logs to
  `~/.loom/logs/serve.log`, with a crash it can't log in
  `serve-crash.log` beside it. If the daemon fails to start, loom prints
  what it logged.
- `loom reset` and `loom workspace migrate` write session files
  themselves, so they refuse while the daemon runs: run
  `loom serve stop` first.
- A loom from before the daemon, still running, keeps the daemon from
  starting: quit it first.

You never need to run `loom serve` yourself. Run by hand, it runs the
daemon in that terminal until `Ctrl-C`, which stops it as `loom serve
stop` does (a second `Ctrl-C` exits at once, without saving); if a
daemon already runs, it says so and exits.

### Session Recovery (orphaned worktrees)

If Loom crashes or its state file loses track of a session, the worktree
and agent may still exist on disk. Whenever the daemon starts (and when a
workspace is registered), Loom scans each workspace's worktrees folder
(`~/.loom/worktrees/` for the global one) for directories it isn't
tracking:

- **Stale leftovers** (no live agent, no uncommitted changes) are removed
  automatically. A summary line reports the count — the branch itself is
  never deleted by this cleanup.
- **Locked worktrees** are left alone: a worktree you locked with `git worktree lock` is never auto-cleaned (or removed by pause or kill, which tell you to run `git worktree unlock`). A worktree left locked "initializing" by an interrupted `git worktree add` is unlocked and cleaned once the lock is older than six minutes.
- **Worktrees with a live agent or uncommitted work** appear in the
  session list as recoverable entries, marked with an orange `⟲` icon.

For a `⟲` entry you have two choices:

- `r` — **recover**: adopt it back as a normal session, reattaching to
  the live agent if one is still running. If the session and worktree
  turn out to be gone, it is recovered as *paused* (branch preserved);
  press `r` again to resume it.
- `D` — **discard**: remove the worktree. Uncommitted changes are lost,
  but the branch is kept, so committed work survives.

Unactioned `⟲` entries are re-derived from disk and will reappear the
next time the daemon starts until you recover or discard them.

If a stored session fails to load (e.g. its repo moved), it is **not**
deleted: the record is kept on disk and retried each time the daemon
starts, and the workspace's recovery summary reports "N sessions failed
to load (kept; see serve.log)". The reason is in the daemon's log,
`~/.loom/logs/serve.log`; the record stays in the workspace's
`state.json` until the underlying problem is fixed.

**Stashes on pause/resume**: pausing stashes uncommitted work
(`[loom] stash from '<title>' …` in `git stash list`); resuming re-applies
it. If re-apply conflicts, the session returns to paused, the stash is
preserved, and the error names the stash SHA so you can resolve it
manually in the worktree.

### Run Sessions on Several Claude Accounts

If you have more than one Claude subscription, loom can put each session
on whichever has headroom.

1. Add an account: `loom account add max-2` (or `a` in **Settings →
   Accounts**). Loom creates `~/.loom/accounts/max-2`, links your main
   Claude config dir into it (everything but credentials and runtime
   state — settings, `CLAUDE.md`, skills, plugins, transcripts and memory
   stay shared), and opens `claude auth login` for it.
2. Once a second account exists, a usage strip appears above the tabs:
   each account's 5-hour and weekly plan usage, refreshed every two
   minutes.
3. When you create a session, the **Account** row in Session Launch
   Options (Claude sessions only) picks where it runs, preselected to the
   default account; change the default with `loom account use` or
   `enter` in **Settings → Accounts**. Cards, the overview grid and the
   agent pane title show `@account`.
4. To move a session that hit its limit, pause it and press `R`: choose
   another account and the conversation resumes there under it. If its
   tmux session is still alive, `R` just reattaches to it on whichever
   account it's already running under — pick another account only after
   it has actually stopped.

Removing an account (`loom account remove`, or `x` in **Settings →
Accounts**) is refused while any session uses it, or while its config dir
holds files that aren't shared with your main setup (something written
directly into the account rather than through `sync`). `-y`/`--yes` skips
only the confirmation prompt; `--force` is what overrides either refusal,
and it prints what it overrode. User-scope MCP servers
(`claude mcp add --scope user`) live in each account's own `.claude.json`
and are not shared.

`loom account add`/`list`/`sync`, and a bare `login`, refuse when run from
a shell whose own environment already points at an account's config dir
(inside an account's own agent pane, say) — run them from an ordinary
shell instead; `loom account login <name>` for a specific account is
unaffected.

Account selection needs `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` and
`CLAUDE_CODE_OAUTH_TOKEN` unset in the loom daemon's environment (the
terminal it was started from; see [The Loom Daemon](#the-loom-daemon))
and in tmux's — Claude checks those before ever reading a config dir's
own login, so with one set every account silently runs and bills as that
credential no matter which one you pick. The usage strip and **Settings →
Accounts** both warn "`⚠ $VAR set: all accounts use it`" when the
daemon's environment has one, and `loom account add`/`login`/`list`
print the same warning for the shell you run them in. Your agents get
these variables from the tmux server's environment, which is the
daemon's only when the daemon started that tmux server: if loom runs on
a tmux server you started yourself, unset them there too
(`tmux set-environment -g -u ANTHROPIC_API_KEY`, say), since the warning
can't see that environment.

**Known limitations**: the terminal pane (the shell below the agent pane)
always runs on the default account, whichever account the agent is on; an
orphaned worktree recovered from disk (`r` on a `⟲` entry) always comes
back on the default account, since nothing on disk records which one it
used; `R` on a still-live session reattaches on its old account rather
than switching; and `loom account remove --force` on an account a session
is still actively using can lose a race with that session's own Claude
process, which may recreate a bare, unlinked directory there right after
the delete — if `loom account add` of the same name then fails with
"already exists", remove the leftover directory by hand first.

### Work Across Multiple Workspaces

```
W → Space to toggle workspaces on/off → Esc
{ / } to switch between active workspace tabs
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
| `--program <prog>` | `-p` | Program this loom's new sessions run (e.g. `aider --model gpt-4`); other open looms keep their own |
| `--workspace <name>` | `-w` | Select workspace by name (bypasses auto-detection) |

### Commands

| Command | Description |
|---------|-------------|
| `version` | Print version number |
| `debug` | Print config paths and loaded configuration, the daemon (pid, socket, build and tmux server, or "not running") and its log, and the tmux server a daemon started now would use (the last daemon's while it runs). Its Claude temp root is the one your shell's environment gives, not necessarily the daemon's |
| `serve` | Run the loom daemon in this terminal (loom starts one in the background when none runs; see [The Loom Daemon](#the-loom-daemon)) |
| `serve stop` | Stop the daemon: it finishes any pause or kill in progress, saves and exits. Sessions keep running; every open loom exits |
| `reset --force` | Delete a workspace's instances (the global one's, or `--workspace <name>`'s), kill its tmux sessions (on the tmux server the last daemon used, while it runs) — only those started in its repo or worktrees directory; other workspaces' sessions keep running — and remove its worktrees **and their branches**. Stops before removing worktrees if the tmux cleanup fails. Refused while the daemon runs (`loom serve stop` first). |
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
| `workspace migrate` | Move global instances to their matching workspace directories (refused while the daemon runs) |

### Examples

```bash
# Run with a specific agent
loom -p "aider --model ollama_chat/gemma3:1b"

# Run in a specific workspace
loom -w my-project

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
| `branch_prefix` | string | `"{username}/"` | Prefix for auto-generated branch names |
| `base_branch` | string | `""` | Branch new sessions are cut from. Empty auto-detects. |
| `theme` | string | `"afterglow"` | UI color theme (`"afterglow"` or `"legacy"`) |
| `profiles` | array | `[]` | Named program configurations |
| `claude_remote_control` | bool | `true` | Launch Claude sessions with `--remote-control`, named after the session title |
| `claude_tmp_archive_dir` | string | `""` | Where Claude temp-dir archives go, for every workspace (global `config.json` only; absolute or `~/…`). Empty keeps each workspace's in its own loom folder. See [Claude Temp-Dir Archives](#claude-temp-dir-archives) |

### Example config.json

```json
{
  "default_program": "claude",
  "branch_prefix": "aidanb/",
  "base_branch": "main",
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

### Themes

The `theme` field selects the UI color theme. Two themes ship with Loom:

- `"afterglow"` (default) — the warm dark palette introduced with the mission-control UI.
- `"legacy"` — the original pre-theme color scheme.

The easiest way to switch is the settings overlay: press `S`, move to the **Theme** row, and press `Enter`/`Space` to cycle. The change applies live — no restart — and is saved to `config.json`. An absent or empty `theme` field means the default.

### UI Layout Persistence

Layout tweaks are remembered per workspace in `state.json` (under a `ui` block): the current view mode (focus/overview), rail visibility (`\`), terminal pane visibility (`T`), the agent/terminal split ratio (`Ctrl+↑`/`Ctrl+↓`, stored per session title), and the workbench's agent/panel split ratio (`Ctrl+←`/`Ctrl+→`, also stored per session title). Everything is restored on the next launch or workspace switch; there is nothing to configure by hand. Workbench mode itself is the one exception — it's never persisted, so a restart or workspace switch always lands in focus mode.

### Claude Remote Control

When `claude_remote_control` is enabled (the default), every Claude session Loom starts is launched with Claude's `--remote-control` flag, so you can drive it from a remote-control client. The remote session is named after the Loom session title (sanitized to a shell-safe token — e.g. `fix login bug` → `fix-login-bug`), making parallel sessions easy to tell apart. The flag is only added for the `claude` program; other agents are unaffected. To turn it off globally, set `"claude_remote_control": false`. If your `default_program` already includes `--remote-control`, Loom leaves it as-is rather than adding a second flag.

**Authentication requirement.** Remote control requires a claude.ai OAuth login (Pro/Max/Team/Enterprise). API keys, Console accounts, and inference-scoped `setup-token`s cannot use it — nor can a login that's overridden by an `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` environment variable. The loom daemon probes `claude auth status` when it starts:

- **Logged in via claude.ai** → sessions launch with `--remote-control`.
- **Incompatible auth detected** → when you create a session (`n`/`N`), Loom shows a modal explaining the problem (e.g. "not logged in — run `claude auth login`") and lets you **start the session without remote control** (`y`) or **cancel** (`n`/`esc`). Auto-created workspace terminals skip the flag silently and show a brief info-bar notice.
- **Auth can't be determined** (older `claude` without `auth status`, or unexpected output) → Loom fails closed: it skips `--remote-control` silently rather than launching a session that would fail.

### Subagent Tracking

Loom shows the subagents and agent-team teammates a Claude session has spawned: a count on its rail card and rows on its overview card. It works through an extra `--settings` file loom launches every Claude session with, which registers hooks; your own hooks keep running alongside them. The same hooks report the session's status, the conversation a crash relaunch resumes, and Claude's last message.

- Toggle the rows with **Track Subagents** under `S` → Claude Preferences. It is on by default and applies at once; the hooks stay installed either way, so turning it back on shows the current agents.
- Only live agents are shown. A finished subagent disappears; a teammate stays listed as idle until it is shut down.
- Sessions whose program already passes `--settings`, and sessions on Windows, get no hooks: they show no subagent rows, and their status comes from Claude's session list and the screen.
- Restarting the daemon keeps the rows: it replays the events it already collected for sessions that are still running.
- Event files live in the `hooks/` folder inside the workspace's loom config folder: `<repo>/.loom/hooks/` for a registered workspace, otherwise `~/.loom/hooks/`. They are cleared at each launch and removed when you kill the session.

### Claude Temp-Dir Archives

Claude Code keeps a temp directory for every session, outside the worktree: `/tmp/claude-<uid>/<the session's directory, encoded>/<session id>/`, holding its scratchpad and background-task output (`$CLAUDE_CODE_TMPDIR`, `$TMPDIR`, `$TMP` or `$TEMP` move it). On many Linux systems `/tmp` is in RAM, and nothing removes these directories when a session ends, so loom archives them:

- **Pause** zips the session's temp dir and removes it; **resume** puts it back before the agent starts. If the restore fails, you see why, the archive is kept, and the session starts without it.
- **Kill** zips it and removes it.
- **At every workspace load**, loom sweeps the temp dirs of its sessions that no longer exist and haven't been touched for a day (worktrees it auto-cleaned, sessions killed before this feature) into the archive.
- Archives live in the workspace's loom folder: `<repo>/.loom/archive/claude-tmp/` for a registered workspace, otherwise `~/.loom/archive/claude-tmp/`. To keep them somewhere else (a bigger disk, say), set `claude_tmp_archive_dir` in the global `~/.loom/config.json` to an absolute path (or one starting with `~/`):

  ```json
  { "claude_tmp_archive_dir": "~/claude-archives" }
  ```

  Every workspace's archives then go under that folder, each workspace in its own subfolder named after its loom config folder (e.g. `~/claude-archives/home-you-projects-my-app--loom/`). It applies to the next archive, no restart needed. Archives already made stay where they are, and a session paused before the change still gets its scratchpad back on resume. A relative path is ignored (with a warning in the daemon's log, `serve.log`). A workspace's own `config.json` doesn't set this. `loom debug` prints the archive folder in effect, and the temp root your shell's environment gives: the daemon uses the one its own environment gives (see [The Loom Daemon](#the-loom-daemon)).
- Directories marked with a valid `CACHEDIR.TAG` (cargo's `target/`, for example) are left out; the archive's `.loom-archive.json` lists what was skipped and how big it was. Symlinks are stored as links.
- Loom never deletes an archive. Prune `archive/claude-tmp/` by hand when you no longer need them. To look inside one: `unzip -l <file>.zip`.

### Claude Fullscreen Renderer

Every Claude session Loom starts runs Claude's fullscreen renderer (Loom sets `CLAUDE_CODE_NO_FLICKER=1` in the session's environment), regardless of your `/tui` setting. Scrolling the agent pane then scrolls Claude's own transcript. Claude's classic inline renderer can't be scrolled inside Loom: it draws every frame as a synchronized update, which tmux relays to Loom as a full repaint, so no scroll-back ever builds up. Mouse-wheeling over it shows the "scrolled" footer but the content doesn't move.

- Sessions started before this change keep their renderer until Loom restarts their tmux session; run `/tui fullscreen` inside one to switch it now.
- To use the classic renderer for a single session anyway, run `/tui default` in it.
- To opt out globally, run `tmux set-environment -g CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN 1`; Claude checks it before `CLAUDE_CODE_NO_FLICKER`. It applies to sessions created afterwards, and agent-pane scrolling won't work in them. Exporting the variable in the shell you start Loom from isn't enough: the loom daemon starts the sessions, with the environment it started with, and once the tmux server is running tmux only copies its `update-environment` variables into new sessions.

### Branch Prefix

`branch_prefix` is prepended to every auto-generated branch name. The value shown as the default — `{username}/` — is a placeholder for the rendered text: when Loom creates its config, it resolves your OS username and writes the literal value (e.g. `aidanb/`) into `config.json`. There is no runtime token expansion, so editing `branch_prefix` to anything you like (e.g. `"loom/"`, `"wip-"`) works as expected.

The resulting branch for a session titled `fix-auth` with the default prefix would be `aidanb/fix-auth`.

`branch_prefix` sets the default. To use a different prefix for a single session, edit the **Branch Prefix** row in the Session Launch Options modal that appears just before the session starts — press `space` on the row to edit it, `enter` to commit. Clearing it entirely is allowed and produces a bare `fix-auth`. The override applies only to that session and is not written to `config.json`; on `R` (restart with options) the row shows the session's existing branch read-only, since a branch cannot be renamed after the fact.

### Base Branch

`base_branch` names the branch new session worktrees are cut from — and therefore the baseline their diffs are measured against.

Leave it empty (the default) to auto-detect, in this order:

1. `refs/remotes/origin/HEAD` — what the remote declares its default branch to be
2. `main`
3. `master`
4. Whatever the root repo currently has checked out

Set it explicitly (e.g. `"develop"`) to pin one branch. A configured branch is looked up locally first, then as `origin/<branch>`; if it resolves to neither, session creation fails rather than silently starting somewhere else.

Resolution reads local refs only — Loom never fetches for this, so it stays fast and works offline. If your local copy of the base branch is behind the remote, new sessions start from that older commit; `git fetch` (or the `N` flow, which fetches for the branch picker) brings it current.

Because `config.json` lives in each workspace's own `.loom/` directory, `base_branch` is naturally per-repository — a `main` repo and a `master` repo can each hold the right value.

This setting only affects sessions created on a **new** branch. Picking an existing branch in the `N` flow's branch picker starts from that branch instead, and resuming a paused session always returns to its own branch with its original diff baseline intact.

### Environment Variables

| Variable | Description |
|----------|-------------|
| `LOOM_HOME` | Override the config directory (default: `~/.loom`). Must be an absolute path; supports `~` expansion. |
| `LOOM_TMUX_SOCKET` | Run loom's sessions on a private tmux server (`tmux -L <name>`). It is read when a daemon starts (a daemon keeps the tmux server the last one used while that server runs); every loom then uses the daemon's server, whatever its own environment says. Inside one of loom's own tmux sessions it doesn't let a daemon start: for a dev build, use `go run ./tools/loomdev run`. |
| `LOOM_GLOBAL_DIR` | Override the directory holding `workspaces.json` (default: `~/.loom`). Absolute path; supports `~`. Each global directory has its own daemon. |
| `LOOM_ALLOW_NESTED` | Set to `1` to let the daemon start (or `loom reset` run) inside one of loom's own tmux sessions on the tmux server it would use anyway (normally refused, because its startup cleanup could kill the enclosing loom's sessions). A loom started inside loom still never replaces an older daemon. |

---

## Workspaces

Workspaces provide per-repository isolation. Each workspace gets its own config, state, and session storage inside the repository at `<repo>/.loom/`.

### Directory Structure

```
~/.loom/                     ← Global (fallback)
  config.json
  state.json
  workspaces.json                    ← Workspace registry
  loom.lock                          ← The daemon's lock (its pid, socket and build)
  logs/serve.log                     ← The daemon's log
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

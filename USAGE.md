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
- [Coming from claude-squad](#coming-from-claude-squad)

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

# Or install a release binary (into ~/.local/bin, which it adds to your shell's PATH)
./install.sh
```

### First Session in 30 Seconds

1. Launch `loom` from a git repository
2. Press `n` to create a new session
3. Type a name and press `Enter`, then `Enter` again in the **Session Launch Options** modal
4. The agent starts in an isolated worktree — watch its output in the **Agent** pane
5. Press `d` to toggle the **Diff** overlay to see what the agent has changed
6. Press `Ctrl+A` to attach to the agent pane and interact directly
7. Press `Ctrl+Q` to detach back to the TUI
8. Press `s` to pause the session when done

---

## TUI Layout

Loom has four view modes:

- **Focus mode** (the default, shown below) — a session rail on the left plus the selected session's agent and terminal panes on the right. Toggle to overview with `tab`.
- **Overview mode** — a full-width fleet card grid for triaging many sessions at once. See [Overview Mode](#overview-mode).
- **Session workbench** — a single-session deep-dive: the agent pane on the left, a tabbed panel (markdown / diff / files / terminal / review) on the right. Press `enter` in focus mode to open it. See [Session Workbench](#session-workbench).
- **Work view** — the host's job queue, across every workspace. Press `w` in focus or overview mode to open it; `w` or `Esc` returns to the mode it came from. It is not persisted: a restarted TUI opens in focus or overview as before. See [The Work Queue](#the-work-queue).
- **Feedback view** — the notes loom's session agents filed about loom itself, grouped into threads and ranked by how many sessions hit each. Press `F` in focus or overview mode to open it; `F` or `Esc` returns. While threads are open, the status line starts `F feedback N ·`. See [Feedback from Sessions](#feedback-from-sessions).

```
┌─────────────────────────────────────────────────────────────────────┐
│  [ webapp ]  [ my-project ]  [ other-repo ]    ← Workspace Tabs    │
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
│  n new • N prompt • s pause • r resume • p push • ? help • q        │
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

Visible whenever workspaces are open as tabs; the global workspace, and a single workspace opened without tabs, show none. Switch between workspace tabs with `{` and `}` (or `l` and `;`). Toggle which workspaces are visible with `W`.

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
1. Git worktree created at `worktrees/{branch}_{timestamp}` in the workspace's loom folder (`<repo>/.loom/`, or `~/.loom/` for the global workspace)
2. New branch created: `{branch_prefix}{session_title}` (default prefix: `username/`). The prefix can be changed for this one session in the Session Launch Options modal.
3. Base branch resolved (`base_branch`, or auto-detected) and its commit SHA recorded — this is both where the worktree starts and the baseline for diffs
4. Tmux session launched running the configured program
5. Agent begins working in the isolated worktree

**Running → Paused** (on pause, `s`):
1. The agent's tmux session and the terminal pane's shell are closed
2. The worktree stays as it is: uncommitted changes, untracked and ignored files (`.env`, `.claude/settings.local.json`, build caches) are all kept, and so is the branch
3. Claude's temp dir for the session (its scratchpad and task output under `/tmp/claude-<uid>/`) is zipped into the archive and removed, since `/tmp` may not survive a reboot — see [Claude Temp-Dir Archives](#claude-temp-dir-archives)

**Paused → Running** (on resume, `r`):
1. Claude's archived scratchpad is restored before the agent starts
2. The agent is relaunched in the worktree and picks up its conversation where it left off
3. Diff baseline preserved — you see cumulative changes since session creation

A session an older loom paused (it removed the worktree on pause) is resumed by recreating the worktree from its branch and re-applying the changes that pause stashed.

**Running/Paused → Killed** (on kill, `D`):
1. Tmux session destroyed
2. Worktree removed (moved into `worktree-trash/` beside `worktrees/` and deleted in the background, so the kill does not wait on a large tree)
3. Branch deleted (unless it existed before the session: one you selected at creation, or one an earlier session of the same title left behind)
4. Instance removed from storage
5. Claude's temp dir is archived (a paused session's archive simply stays)

A directory git no longer tracks as a worktree, or one whose repository is gone, is left on disk and named in a notice, since it may hold the only copy of the work.

There is one more state that appears only after a crash or lost state
file: **Recoverable** (`⟲`) — a worktree found on disk that Loom isn't
tracking. It sits inertly in the list until you recover (`r`) or discard
(`D`) it; see [Session Recovery](#session-recovery-orphaned-worktrees).

### Workspace Terminals

When a registered workspace is open, a special instance is pinned at the top of the instance list — the **Workspace Terminal**. Unlike regular sessions, it runs directly in the root of the repository without creating a git worktree:

- **No worktree** — the agent operates on your main checkout, so changes are immediately visible to other tools working in that directory.
- **Cannot be paused or killed** — the workspace terminal is a permanent fixture while the workspace exists. `s` and `D` are no-ops when it is selected.
- **Diff stats** reflect the workspace's uncommitted changes against HEAD, not a cumulative diff against a base commit.
- **Auto-recreated** — if its tmux session is missing when the workspace is first opened after the daemon starts (e.g. after a reboot), Loom recreates it in place.
- **Resume with `r` when it shows Paused** — a terminal whose relaunch failed (tmux could not start a session, say) or whose agent kept exiting is left Paused. `r` relaunches it in the repository, resuming its conversation; it refuses, changing nothing, when another session holds the terminal's tmux name. `R` (restart with options) stays off for it: its launch follows the workspace's config.

Use the workspace terminal for work that needs unrestricted access to the root checkout (ad-hoc shell commands, editors, or an agent you want full repo visibility for). Create standard sessions with `n` / `N` for anything that should stay isolated in a worktree.

---

## Keyboard Reference

### Default State (focus mode)

| Key | Action |
|-----|--------|
| `↑` / `k` | Move selection up |
| `↓` / `j` | Move selection down |
| `n` | Create new session (name only) |
| `N` | Create new session with prompt, profile, and branch picker. A prompt starting `#123` expands to that GitHub issue |
| `I` | Create new session from a GitHub issue (picker; needs `gh` auth) |
| `Tab` | Toggle overview mode (fleet card grid) |
| `]` / `[` | Jump to next/previous agent waiting for input (prompting or bell; wraps), across every open workspace tab, switching tabs as needed |
| `i` / `Ctrl+A` | Inline attach to agent pane |
| `Ctrl+T` | Inline attach to terminal pane |
| `Alt+A` / `Alt+T` | Full-screen attach (agent / terminal) |
| `a` | Quick input to agent |
| `t` | Quick input to terminal |
| `\` | Show/hide the session rail |
| `T` | Show/hide the terminal pane |
| `Ctrl+↑` / `Ctrl+↓` | Resize the agent/terminal split (remembered per session) |
| `s` | Pause — stop the session's agent, keeping its worktree |
| `m` | Merge another session's branch into the current one |
| `r` | Resume a paused session / recover an orphaned (`⟲`) session |
| `R` | Resume a paused session with different launch options |
| `p` | Push branch to remote (with confirmation) |
| `D` | Kill selected session (with confirmation); on an orphaned (`⟲`) session: discard its worktree, keeping the branch |
| `d` | Toggle diff overlay |
| `f` | File explorer: fuzzy-find a file in the selected session's worktree and open it in `$VISUAL`/`$EDITOR` (else `vi`) |
| `PgUp` / `PgDn` (or `Ctrl+U` / `Ctrl+D`), `Home` / `End` | Scroll the active pane (the diff when shown, else the agent pane or the inline-attached terminal) by a page / to the top or bottom |
| `Shift+↑` / `Shift+↓` | Scroll the active pane by a line |
| `Alt+PgUp` / `Alt+PgDn` | Scroll the terminal pane by a page |
| `K` / `J`, `g` / `G` | Page the session list up/down; jump to its top/bottom |
| `Esc` | Close the diff overlay, or leave scroll-back |
| `Enter` | Open the session workbench for the selected session |
| `c` | Open the workbench code review for the selected session |
| `W` | Open workspace picker |
| `w` | Open the work view: the host's job queue (see [The Work Queue](#the-work-queue)) |
| `F` | Open the feedback view: the notes sessions filed about loom (see [Feedback from Sessions](#feedback-from-sessions)) |
| `S` | Open settings (edit the workspace's config.json: Default Program, Branch Prefix, Base Branch, Profiles, Claude Preferences, Theme; Build Sweep and Job Borrowing, which apply to all workspaces; Accounts) |
| `{` / `l` | Previous workspace tab |
| `}` / `;` | Next workspace tab |
| `?` | Show help screen |
| `Ctrl+R` | While the daemon is away: join a daemon now, starting one (see [The Loom Daemon](#the-loom-daemon)). Otherwise unbound |
| `q` | Quit |
| `Ctrl+C` | Quit at once, without saving the open tabs (works even when a script rebinds every other key) |

### Overview Mode (after pressing `Tab`)

Session-lifecycle keys (`D`, `r`, `R`), workspace keys, and `q`/`?`/`W`/`S` keep working; keys that act on an invisible pane (attach, quick input, scroll, diff, file explorer) are inactive.

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Walk the attention-sorted card grid |
| `Enter` | Return to focus mode on the selected session, switching to its workspace tab |
| `Esc` / `Tab` | Return to focus mode where you left it |
| `w` | Open the work view; `w` or `Esc` there returns to the overview |
| `F` | Open the feedback view; `F` or `Esc` there returns to the overview |
| `z` | Collapse/expand the active workspace group |
| `]` / `[` | Move the cursor to the next/previous agent waiting for input, in any open workspace |
| `n` / `N` / `I` | Drop to focus mode, then open the create flow |

### Session Workbench (after pressing `Enter` on a selected session)

Session-lifecycle keys (`D`, `r`, `R`, `p`, `s`, `m`), attach (`i`/`Ctrl+A`/`Alt+A`), quick input (`a`), workspace keys, `]`/`[`, and `q`/`?`/`W`/`S` keep working; layout keys that address the hidden focus-mode chrome (`\`, `T`, list paging) are inactive. `]`/`[` move the panel to a waiting agent in the same workspace, and return to focus mode for one in another workspace. The Review tab passes most of those keys through, with these exceptions: `S` sends the review comments to the agent instead of opening settings, `q` closes the review instead of quitting, `]`/`[` move between comments instead of jumping to a waiting agent, and `s` toggles the comment sidebar instead of pausing (in a code review, `n`/`N` jump between changes instead of creating instances). In a code review the pane also owns `1`–`9` and `Tab`/`Shift+Tab` (file tabs) — see [Reviewing](#reviewing) for the full set of review keys.

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

### Work View (after pressing `w`)

The work view covers the whole host, across every workspace. Mouse input is dropped. Offline (daemon away), it shows the last state it had and its actions are refused like every other model key. Each running or queued job shows its claim and where the claim came from (`learned·5`, `family·3`, `agent`, `default`, `yours`; `—` and `sizing` while it waits for the agent); a running job shows how long it has run against its usual duration (`1m10s/~2m`, marked `⚠` once it is overdue), and a queued job when it is expected to start (`starts ~3m`).

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Move the selection |
| `Enter` | Open the selected job's whole log in a scrolling pager, whose title shows the job's command and folder (`Esc` back) |
| `b` | Open what the scheduler has learned of the selected job's command in the pager: the claim a job of it gets, its usual duration, how it scales, the agent's prior and notes, and its runs (`Esc` back) |
| `x` | Cancel the selected job, after a confirmation (a queued job at once, a running one with SIGTERM and then SIGKILL) |
| `t` | Move a queued job to the front of the queue |
| `o` | Focus the job's owner session in focus mode, opening its workspace's tab if it is not open. A job whose owner is in the global workspace, which no open tab shows, is left alone: `o` changes nothing and says so on the info line |
| `e` | Open the caps form: the cores, memory and I/O caps, the default claim, the starvation wait and how many finished jobs to keep. It shows each value in effect, whether it is the host's default, and the host's detected cores and RAM; saving applies it at once, and a refused value is shown in the form |
| `w` / `Esc` | Return to the mode the view came from |

### Feedback View (after pressing `F`)

The feedback view covers the whole host. Its rows are the threads that aren't closed and the notes no thread holds yet (`n12`), most-reported first; the header says when the grouper is off and why. Mouse input is dropped, and offline `x` and `c` are refused.

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Move the selection |
| `Enter` | Show or hide the selected row's notes: who filed each, on what model and effort, the loom build, its title and its text's first lines |
| `x` | Start a fix session for the selected thread in `feedback.workspace`, after a confirmation; the view leaves for focus, where the new session attaches |
| `c` | Close the selected thread, after a confirmation |
| `F` / `Esc` | Return to the mode the view came from |

### Name Entry Mode (after pressing `n` or `N`)

| Key | Action |
|-----|--------|
| *Type characters* | Enter session name (max 32 chars) |
| `Enter` | Submit the name: `n` opens the Session Launch Options modal, `N` the prompt overlay |
| `Backspace` | Delete last character |
| `Ctrl+C` / `Esc` | Cancel |

### Prompt Overlay (after pressing `N` and entering a name)

The overlay has up to four focus areas. Press `Tab` to cycle between them:

1. **Profile Picker** — `←` / `→` to select a profile (shown when more than one profile is configured)
2. **Prompt Text Area** — Type your instructions for the agent (`Enter` starts a new line)
3. **Branch Picker** — Type to filter branches, `↑` / `↓` to select; `Enter` moves to Submit
4. **Submit** — `Enter` to continue to the Session Launch Options modal

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Cycle between focus areas |
| `Enter` (on Submit) | Submit the prompt and open the Session Launch Options modal |
| `Ctrl+C` / `Esc` | Cancel |

### Session Launch Options (before every new session starts)

Rows: Remote Control, Permission Mode, Model, 1M Context, Headroom Proxy, Effort, Cache TTL (1h), Branch Prefix, and Account (once a second Claude account exists), pre-filled from the workspace's config.

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Move between rows |
| `Space` | Toggle, cycle or edit the row under the cursor |
| `Enter` | Start the session with these options |
| `Esc` / `q` | Cancel |

### Inline Attach (after pressing `Ctrl+A` or `Ctrl+T`)

| Key | Action |
|-----|--------|
| `Ctrl+Q`, or `Esc` twice within half a second | Detach from session and return to TUI (a single `Esc` still reaches the agent) |
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
| `Space` / `Enter` | Toggle workspace active/inactive; on the Global row, close every tab and switch to the global workspace |
| `Esc` / `q` | Apply changes |

---

## Workflows

### Create a Simple Session

```
n → type "fix-auth-bug" → Enter → Enter (Session Launch Options)
```

The session starts with the default program in a fresh worktree.

### Create a Session with Prompt and Branch

```
N → type "add-validation" → Enter
  → [select profile with ←/→]
  → type prompt: "Add input validation to the /api/users endpoint"
  → Tab to branch picker → type "feat" to filter → select branch
  → Enter to submit → Enter (Session Launch Options)
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

**Pause** — stops the agent and keeps everything:
```
Select running session → s
```
The agent's tmux session ends; the worktree stays exactly as it is, uncommitted and ignored files included, and so does its branch.

**Resume** — picks up where you left off:
```
Select paused session → r
```
The agent is relaunched in the worktree and continues its conversation.

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
  a workspace terminal that dies, polls GitHub, reads Claude's status
  from its hook events, and saves. It also answers an agent's trust
  prompt ("Do you trust the files in this folder?") for up to 30 seconds
  after each launch, so an agent it relaunches at boot with no loom open
  gets past the prompt. A loom you have open never answers it too. It
  leaves Claude's MCP server dialogs ("New MCP server found in this
  project") to you, since approving one starts a server the repository's
  `.mcp.json` names. A prompt it leaves to you is never typed into: a
  new session's first prompt waits up to two minutes for you to answer
  it, or loom tells you it was not sent, with its text; a prompt loom
  would type in (the quick bar, the prompt overlay, a script's send to a
  session without the loom mod) is refused while it is open.
- **Stopping the daemon**: `loom serve stop`. The daemon tells every open
  loom it is stopping, finishes any pause or kill in progress (up to 30
  seconds; the loom that asked still gets the result), saves and exits. Your
  agents keep running in tmux. The next `loom`, or `Ctrl+R` in a loom
  that is still open, starts a new daemon, which picks the sessions back
  up. Don't run `loom serve stop` from inside one of loom's own panes
  unless you mean it: it stops the daemon every open loom uses.
- **When the daemon goes away**, an open loom stays up. A banner on its top
  row says what happened, the panes keep rendering and you can still
  attach to them, and it joins a daemon again as soon as one answers,
  keeping your tabs, selection and any prompt you were typing.
  - After `loom serve stop` (or a newer loom replacing the daemon): `⚠ the
    loom daemon stopped: waiting for one to start (ctrl+r starts it)`. The
    loom looks for a daemon once a second and never starts one itself, so
    a stop stays a stop: press `Ctrl+R` to start one now. Right after you
    run the stop the banner reads `the loom daemon is stopping: finishing
    its jobs in flight`, and `Ctrl+R` waits until that ends.
  - After a crash (the daemon was killed, or failed): `⚠ lost the loom
    daemon: reconnecting (attempt N)`. The loom retries with growing
    pauses (1s up to 30s) and starts a daemon when none runs. If three
    daemons fail to start, or die within 30 seconds of loom rejoining
    them, before one stays up, it stops trying and waits as above, naming
    `serve.log`; `Ctrl+R` tries again.
  - Offline, the keys that only talk to tmux or stay in loom still work:
    moving the selection, tabs, the overview and workbench views, attach
    (`i`, `Ctrl+A`, `Ctrl+T`, `Alt+A`, `Alt+T`), quick input to the
    terminal (`t`), the diff overlay (`d`), the file explorer (`f`), the
    rail and terminal-pane layout keys, scrolling, help and `q`. Every
    other key (`n`, `N`, `I`, `D`, `r`, `R`, `p`, `s`, `m`, `a`, `W`, `S`
    among them) shows `the loom daemon is stopped: n needs it` and does
    nothing. This goes by the key, not by what it is bound to: a key your
    scripts bound is refused unless it is one of the keys above, and a
    script bound to one of those still runs, though whatever it asks of
    the daemon fails with `the loom daemon is unavailable`. A command
    already waiting for an answer when the daemon went (a kill, a pause,
    a prompt you sent) ends with `the loom daemon is unavailable`; check
    the session once you are back. Statuses stay as last shown until a
    daemon is back.
  - Changes to the layout while offline (the rail, the terminal pane, the
    view mode, the split sizes) and the tab you focus take effect at
    once and are saved when a daemon is back.
  - On rejoining, the banner clears and `reconnected to the loom daemon`
    appears. A tab whose workspace was unregistered meanwhile closes, and
    the message says so.
  - `q` while offline quits without saving the open tabs, the split sizes
    or the layout changes above, since only the daemon can write them
    (`loom.log` records `quit.offline_unsaved`); the next `loom` opens
    the tabs of the last time they were saved.
- **Upgrading.** A newer loom replaces an older daemon with its own build
  (`loom: replacing the loom daemon (…) with this build…`). A loom of the
  old build still open waits for a daemon as above, finds the newer one
  and exits with `loom: the loom daemon was replaced by a newer loom (…);
  run loom again`. An older loom refuses a newer
  daemon (`the loom daemon is …, newer than this loom (…): upgrade loom,
  or run loom serve stop`). Of two builds of one release, the one of the
  later commit is newer; a build that names no commit (a plain `go build`
  from a source tarball) keeps the daemon that runs, so run
  `loom serve stop` to switch to it. A Nix build of loom names the commit
  it was built from, so a newer one replaces the daemon as a release does.
  A loom started inside one of loom's own tmux sessions never replaces the
  daemon. Its subcommands (`loom job`, `loom session`, …) use an older
  daemon of the same wire protocol as it is (`loom: using the loom daemon
  as it is (…)` on stderr), so an agent's commands keep working after a
  rebuild, and a command only the newer build knows fails saying the daemon
  is older; the TUI, and any loom facing a daemon of another protocol, is
  refused. To upgrade the daemon, run the new loom from an ordinary
  terminal, or stop the daemon first.
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
- `loom workspace`, `loom account`, `loom session` and `loom web` are
  clients of the daemon, as a loom is: they start one when none runs,
  make their request and exit.
  `loom reset` is the daemon's to make while it runs (it drops the
  workspace's sessions at once); with no daemon it works directly and
  holds the daemon's lock meanwhile, so none starts under it.
  `loom workspace migrate` writes session files itself, so it refuses
  while the daemon runs: run `loom serve stop` first.
- A loom from before the daemon, still running, keeps the daemon from
  starting: quit it first.

You never need to run `loom serve` yourself. Run by hand, it runs the
daemon in that terminal until `Ctrl-C`, which stops it as `loom serve
stop` does (a second `Ctrl-C` exits at once, without saving); if a
daemon already runs, it says so and exits.

### Use Loom from Your Phone

The daemon can serve a web app, an installable page for an iPhone, that
shows your fleet across every workspace, why a session waits and what it
said last. From it you reply to a session, start, pause, resume, kill,
push and merge, and get a notification when a session starts waiting on
you or finishes. It is off until you enable it, listens on loopback only,
and answers only phones you have paired.

- **The header and the status row.** Every screen sits between a pinned
  header and a pinned footer. The header is the screen's own row (the way
  back, its title, its actions) and, on a session, its tabs. The footer is
  the status row: whether the phone is live (`LIVE`, `CONNECTING`,
  `RESTARTING`, or `OFFLINE` with when it last heard from loom), which
  machine it shows (`loom@<host>`), and how many sessions need you (●), are
  your turn (◆) and are working (◐) in the workspaces it shows. Tap a count
  to open that group's next session, and again for the one after. The
  footer hides while the reply box has focus.
- **The fleet.** Sessions are listed by group: **Needs you** (longest wait
  first, with the reason and, for a permission request, the command, file
  or address), **Your turn** (marked NEW when the turn ended after this
  phone last opened the session), **Working**, and **Paused** (folded
  away). Each Claude session's row shows its account's plan usage beside
  its age (the 5-hour share, or the weekly one for an account without a
  5-hour limit), amber from 80%; the counts are in the status row. The
  workspace chips are the workspaces open as tabs in loom, in tab order,
  plus any with a session that isn't paused (all of them while no tab is
  open); the phone remembers the chip. A Claude session opens on its
  conversation (below); a session that doesn't run Claude opens on its
  info tab.
- **The conversation.** A session's screen has tabs under its title:
  **conversation** on a Claude session, **changes** while the session
  runs (with its line counts), **info**, and **mockup** while the
  brainstorming skill's visual companion runs in the session's worktree
  (marked when its screen changed in the latest turn): the page the
  companion shows, sandboxed, with a full-screen view. A strip under the
  tabs shows the session's state and age, its line counts, its pull
  request and its account's usage; tap it for the info tab. The
  conversation is its recent turns: each prompt (yours, a background
  task's, or a subagent's report), Claude's replies as it formatted them,
  its thinking as a folded line (tap to read it), and its tool calls as
  one-line steps folded under a count (tap to open; the newest turn's run
  is open while Claude works, its running step marked). A subagent's step
  is a link to that subagent's screen. **↑ earlier turns** loads more. A
  session that waits on you ends its conversation in a box that says why
  and lists what it asks, each answered there, above the reply box. Every
  tab but a subagent's ends in the reply box, and a reply you started
  stays as you switch tabs. Going back from a subagent or another tab
  returns you to where you were, then refreshes.
- **The info tab.** Tiles for the session's changes, its pull request
  (which opens on GitHub) and its account's usage (both windows, and when
  each resets), its subagents by name (a row opens that subagent's
  screen), the actions, and the reply box.
- **A subagent's screen.** Its description is the title, with the session's
  tabs under it and a strip by its state: running with its age, finished
  (idle for a teammate) or unknown, then its type, and **session needs
  you ›** while the session waits. It shows the subagent's own turns, its
  brief first, and refreshes while it runs. It has no reply box. It reads
  **Starting…** until the subagent's record is written, and **This
  subagent's record is gone.** when it never is.
- **The changes.** The worktree's diff against its base commit, file by
  file: tap a file for its hunks. It refreshes while the session works and
  shows no tool output.
- **Replying.** The reply goes to Claude through the loom mod, as
  `loom session send` does, queued behind the running turn. It is refused,
  with the reason, for a session with no live mod or another agent than
  Claude. A reply whose first word is a slash command runs it: `/compact
  [instructions]`, `/clear`, `/model <name>`, or any skill, plugin or user
  command the session's Claude lists (see [Session
  Subcommands](#session-subcommands) for what is refused). Its result
  arrives as a notice.
- **Quick replies.** While the reply box is empty, chips above it fill it
  with a reply you use often; Send sends it. They start as `yes`,
  `continue`, `/compact` and `/clear`; edit them in Settings, one a line,
  at most 12. Every paired phone shares the list.
- **Questions and permission prompts.** Loom answers none itself. The
  dialogs a waiting session's Claude holds open through the loom mod are
  listed in its needs-you box, and you answer each there: a question's
  options, Other, a note, or Chat about this; a permission's Allow, one of
  its suggested rules (one that persists asks first), or Deny with an
  optional message. One already answered in Claude is refused, with the
  reason. **Open in Claude**, below them, links to the session's Remote
  Control page on claude.ai, where you read the whole conversation; it
  shows only while Remote Control is connected for that session, and
  otherwise the card says Remote Control is off for it.
- **Actions.** Kill, push and merge ask first, and the sheet names what
  happens (kill ends the tmux session, force-removes the worktree and
  discards its uncommitted changes, so push first to keep them, and deletes
  the branch unless the session checked out an existing one; for a session
  an older loom paused, which has no worktree, the stash it holds is dropped
  too; push commits first; merge asks which session's branch to merge in).
  Push and merge work on a paused session as well.
  Pause and resume do not ask. An action the daemon refuses shows the
  daemon's reason. A stash that kill or resume reports is always shown: it
  can be the only record of your work.
- **Notifications.** Turn them on from the app's settings with a tap (iOS
  requires the tap, iOS 16.4 or later, and only for an app installed to the
  Home Screen); the phone can take minutes to finish. Each device
  chooses *waiting*, *finished* or both, and can pause them for an hour. A
  notification is tagged by session, so a session shows at most one, and
  opens it (on its mockup tab while that is new and no dialog is open); the
  Home Screen badge counts the sessions that need you, and the app keeps
  it in step with the stream. Loom cannot clear a notification that went
  stale: tapping it shows the current state. If a push service stops
  accepting a device, or the app finds when it opens that the phone has
  dropped its subscription (Apple goes on accepting pushes for one),
  `loom web devices` shows `expired` or `failing: <status>` and the app's
  Settings says so ("Notifications stopped; turn them on again." or
  "Notifications are failing (<status>); turn them on again."). When the
  app finds the phone's own subscription gone it also says "Notifications
  stopped on this phone. Turn them on again in Settings."

#### Set it up

1. **Reach the daemon.** The daemon listens on a loopback port, and a
   Cloudflare tunnel run by a `cloudflared` service on the same machine
   carries your host name to it. With a tunnel named `loom` (made once
   with `cloudflared tunnel login`, `cloudflared tunnel create loom` and
   `cloudflared tunnel route dns loom loom.example.com`), the tunnel's
   `config.yml` routes the host name to the port:

   ```yaml
   tunnel: <tunnel id>
   credentials-file: /home/<you>/.cloudflared/<tunnel id>.json
   ingress:
     - hostname: loom.example.com
       service: http://127.0.0.1:7420
     - service: http_status:404
   ```

   Run `cloudflared tunnel run loom` as a service under its own user. Leave
   the Host header as the browser sent it: loom answers 421 to a request
   whose Host is not the origin's host and port.
2. **Turn it on.**

   ```bash
   loom web enable --listen 127.0.0.1:7420 --origin https://loom.example.com
   ```

   `--listen` is a loopback address (`127.0.0.1:<port>` or `[::1]:<port>`),
   not a host name, and its port is 1 to 65535 (0 is refused); `--origin`
   is `https://<host>[:<port>]` with no path
   (for development only, `http://127.0.0.1:<port>` or
   `http://localhost:<port>`; the app works on one, but no notification is
   sent, since push needs an `https` origin and `loom web enable` says so).
   The daemon applies them at once, and the
   command waits a few seconds to say whether it serves or why not (the
   port is taken, say). The settings are kept either way. Running
   `loom web enable` again with the same values while the web serves changes
   nothing and says so; after a bind that failed, it retries the bind: free
   the port, then run it again.
3. **Install the app.** Open the origin in Safari on the iPhone and use
   Share, then Add to Home Screen. Opened in Safari, the app shows these
   steps instead of pairing; the installed app keeps its storage apart from
   Safari's.
4. **Pair.** On the machine, run `loom web pair --name iPhone`; it prints a
   code such as `K7QM-2XPA`, when it expires and the origin. Enter the code
   in the installed app. A code works once and for ten minutes; a second
   `pair` for the same name replaces its code; at most four codes wait at a
   time; five wrong codes void every waiting one (run `loom web pair`
   again); sixteen devices at most. A daemon restart voids the codes.
5. **Notifications.** In the app's settings, tap to turn them on.

`loom web devices` lists each device, when it paired and was last seen, its
notifications and its push state; `loom web revoke <name>` (or `--all`)
unpairs one: its stream ends, its push subscription is dropped and the app
returns to pairing. `loom web disable` turns the web off and keeps the
devices and the origin, voids the pending pairing codes, and sends no push
until it is on again (the phones' subscriptions stay, so turning it on
resumes pushes without them subscribing again); `loom web status` shows
the settings, whether it serves and why not, and the device count. The
settings live in `web.json` in the global dir, which only the daemon
writes; a `web.json` that fails to load (unreadable, a newer version than
this build, or a stored address, origin, name or key that is not valid) is
left alone: the web stays off, `enable` and `revoke <name>` refuse
(`disable` says the web is already off), `pair` says the web is off, and a
notice says why; fix or move the file by hand.

**What the tunnel means.** Cloudflare ends TLS at its edge and reaches loom
over plain HTTP on loopback, so Cloudflare can read the traffic, the device
token included. A token is revocable per device, and Cloudflare Access can be
put in front of the host name with no change to loom.

#### Keep the daemon up (a systemd user unit)

The app answers only while the daemon runs. To have it after a reboot, run
`loom serve` as a systemd user unit, `~/.config/systemd/user/loom.service`:

```ini
[Unit]
Description=loom daemon

[Service]
Type=simple
ExecStart=%h/.local/bin/loom serve
Restart=on-failure
RestartSec=5
# Leave the jobs the work queue runs alone when the unit stops or restarts:
# each runs in a session of its own and outlives the daemon.
KillMode=process
# The login environment, which a user service does not inherit (below).
EnvironmentFile=-%h/.config/loom/serve.env

[Install]
WantedBy=default.target
```

Enable it with `systemctl --user enable --now loom`, and
`loginctl enable-linger $USER` so it starts at boot without a login.

**The unit needs your session's environment.** The daemon passes its own
environment to every agent it launches, and a user service starts with
almost none: `PATH` must reach `claude`, `git`, `tmux` and your tools, and
`SSH_AUTH_SOCK` (and `DISPLAY`, and the rest of what your agents use) must
be set, or every session the daemon starts runs without them. Either import
them from a login shell before the unit starts, with
`systemctl --user import-environment PATH SSH_AUTH_SOCK`, or write them to
the file the unit reads, `~/.config/loom/serve.env`, as `NAME=value` lines
(`PATH=/home/you/.local/bin:/run/current-system/sw/bin`, for example). Either
way, restart the unit after changing them (`systemctl --user restart loom`):
a daemon keeps the environment it started with.

After installing a new loom build, restart the unit too. A `loom` you start by
hand that finds an older daemon replaces it with a detached daemon of its own,
outside the unit.

**`KillMode=process` is what lets jobs survive a restart.** The work queue's
running jobs belong to the unit's cgroup. Under systemd's default
(`control-group`), `systemctl --user restart loom` kills every one of them with
the daemon; with `KillMode=process` only the daemon's own process is stopped,
and the restarted daemon adopts the jobs that are still running (see
[The Work Queue](#the-work-queue)).

#### When something is wrong

| You see | Cause |
|---|---|
| The app says "Can't reach loom" and when it last updated; the browser shows 502 | The daemon is not running, or the tunnel cannot reach the port |
| 421 | The Host header is not the origin's host and port: the tunnel changed it, or `--origin` is not the address the phone opens |
| 403 on an action | The request's `Origin` is not the configured origin |
| "loom is restarting" | The daemon stopped; the app reconnects by itself |
| Pairing says the code is wrong, used or expired | Run `loom web pair` again; a code works once, for ten minutes |
| `loom web enable` says it is not serving | `loom web status` shows the reason, usually a port that is taken; the settings are kept. Free the port and run the same `loom web enable` again: it retries the bind |
| The fleet is greyed with every action off | The app shows its last snapshot until the stream is back |
| No notifications | The app is not installed to the Home Screen, notifications are off or paused for the device, the origin is a development `http://127.0.0.1:<port>` or `http://localhost:<port>` (push needs `https`), or `loom web devices` shows `expired` or `failing` |
| `loom web enable` says the daemon did not say whether it serves | The web layer may have stopped (a panic, or it failed to start with the daemon, which is only logged), and `loom web status` then shows `Serving: no` with no reason. Restart the daemon to start it again (`systemctl --user restart loom`, or see [The Loom Daemon](#the-loom-daemon)); sessions keep running; the daemon's `serve.log` says what happened |

### The Work Queue

Builds, test suites and benchmarks from several sessions at once can starve a host. The daemon keeps a per-host queue: a session (or you) submits a command with `loom job`, the daemon starts it when its claim fits what the host has left, runs it detached from the daemon, and tells the submitting session's Claude when it ends. Loom's Claude context tells agents to use it. Nothing rewrites an agent's commands, so work not submitted through `loom job` competes as before. The scheduler is not available on Windows.

```
loom job run -d "tests" -- cargo test       # queue it, follow it, exit with its exit code
loom job submit -d "tests" -- cargo test    # queue it and return; a message arrives when it ends
loom job submit -d "tests" --after 14 -- cargo test   # held until job 14 ends done, then queued at its place
loom job submit -d "e2e" --lock port:4300 -- pnpm exec playwright test   # waits while another job holding port:4300 runs
loom job wait 14                            # follow a job again
loom job logs 14 -f                         # its output, following
loom job list --all                         # running, queued and the finished ones kept
loom job cancel 14
loom job beliefs                            # what the scheduler learned of each command, and how well it parallelises (--repo NAME for one repo)
loom job beliefs show 3f9a1c2b7d10          # one command in full: claim, usual duration, how it scales, the agent's prior and notes, runs
loom job beliefs forget 3f9a1c2b7d10        # drop a command's history (a new toolchain, a rewritten build)
loom job agent                              # the scheduler's agent: its state, today's calls and cost, its recent calls (--json, --calls N)
```

- **No shell.** The command runs as given, in the folder you ran `loom job` from, with your environment. Write a pipeline as `-- sh -c '…'`. A command given as one word with spaces in it that names no program (`loom job submit -- "cargo build --release"`) is refused, saying how to split it.
- **Sizing.** The scheduler sizes each job from what the same command measured before: the same repo (every worktree of it counts as one), the same folder in it, and the same command once wrappers such as `env A=1`, `direnv exec .` and `nix develop -c` are set aside. Memory is the largest peak of its last 10 done runs plus 15%; cores are the busy cores those runs used, plus a quarter, never more than the default claim; an I/O slot goes to a command whose runs write 2 GiB or more. A command whose runs keep well over their cores busy (one that ignores the hint variables, such as a test runner that sizes its workers to the machine) claims the cores it takes, up to the cap. A command with no done run yet borrows from its family: the runs of commands in the same folder that run the same tools on the same crates or packages, whatever their test filters, output trims (`| tail -40`), environment or flag order. With no family run either it takes the agent's estimate or the default claim (and a failed run's peak only raises it). `submit` says how it sized the job (`sized 8c/16G io (learned from 5 runs)`), and `loom job list` and the work view show where each claim came from (`learned·5`, `family·3`, `default`, `yours`); `loom job beliefs show` names a belief's family. What it learned is in `~/.loom/jobs/beliefs.json`.
- **Spare cores.** The scheduler also learns how each command scales: from the CPU time, wall time and cores of its done runs, the share of its work that runs in parallel and the most cores it uses at better than 50% efficiency (at most twice the most cores it has had). On an idle host, when no other job waits, a job that scales starts with spare cores up to that width, always leaving one default claim free for the next job, and with memory in proportion; its claim reads `4+2c/3G` (4 cores sized, 2 spare), the hint variables carry all 6, and `run` and `wait` say `job 21 started after 6s with 2 spare cores (4+2c/3G)`. `submit` says how far a job could go (`up to 6 cores when idle`). An explicit `--cpus` or `--mem` forgoes spare cores, and usual durations are given at the cores a job gets.
- **Claims.** `--cpus N` cores and `--mem N` GB override the sizing, part by part; `--cpus` with `--mem` makes the whole claim yours, `--io` takes an I/O slot, and `--exclusive` takes the whole host (it cannot combine with the others). `submit` warns when `--mem` is below what the command has used. A job starts when its claim fits within the caps beside the running jobs' claims; otherwise `loom job list` and the work view say why it waits ("needs 8 cores (4 free)"). A claim is used for admission only: nothing stops a job from using more.
- **How long.** Once a command has run, `submit` and `run` say how long it usually takes (`usually 2m`; `about 5m` for the agent's estimate), `loom job list`'s `USUAL` column shows it, and a queued job's wait says when it is expected to start (`starts ~3m`, from running admission forward over those durations). A job that has run past max(2 × its P90, P90 + 5 minutes), once its command has three runs, is overdue: `list` marks it, the work view shows `⚠`, and `run` and `wait` say so once (`loom: job 14 is overdue: 9m so far, usually 2m`). Nothing is sent to the owner.
- **Holds.** A job reserves its whole claim while it runs, unless its command's past runs say it no longer needs it. The scheduler records how each done run used memory and cores as it went (measured in CPU-seconds of progress, so a busy host doesn't skew it) and keeps that for a command's last 5 runs. Once a running job has reached its usual peak and gone past the point where those runs peaked, and its last two samples sit within what they used from there on, it holds only that, plus 15% for memory and 25% for cores: other jobs start in what it gave back. A job without 3 such runs, one adopted by a restarted daemon, one with an `--exclusive` or a full `--cpus`/`--mem` claim, or one that outlasts every past run holds its claim. A job that uses more than it holds goes back to its claim for the rest of its run, and its command holds its claim for its next 10 runs. The work view shows a lowered hold beside the job's peak (`holds 1c/2G`), its header reads `8/14 held · 12 claimed`, and `loom job list` adds the same text after the claim. Turn it off with `release_past_peaks` ([Scheduler Caps](#scheduler-caps)).
- **Order.** First fit, so a small job can pass a big one that waits for room, until the big one has waited past `starve_after_minutes` ([Scheduler Caps](#scheduler-caps)); then nothing else starts before it.
- **After another job.** `--after N` holds a job until job N ends. N must be the same session's (or, from a plain shell, a plain shell's) and not have failed. When N succeeds, the job joins the queue at N's place in line, ahead of anything submitted after N; when N fails, is cancelled or is lost, the job is cancelled, and so is every job held after it (`loom: job 15 was cancelled because job 14 failed (exit 101)`). Use it to free a build's memory while its tests run: submit the build, then, `--after` it, the step that runs what the build made (the test binaries, or an archive of them), since a step that rebuilds does so on its own, smaller claim, and its message says when it peaked at over twice that. A held job shows as `held` in `loom job list` and the work view, with what it waits on (`after job 14 (running)`), and the work view's `x` on its prerequisite names it. One message reports a chain: a build that succeeds sends none, and its dependent's message names it (`(2s in the queue, after job 14 "build tests", done in 3m10s)`); a build that fails sends its own, naming the jobs it cancelled. Each step is sized on its own: a command run after another learns apart from the same command run cold.
- **Locks.** `--lock NAME` (repeatable, up to 8) holds a named lock while the job runs: a job naming a lock another running job holds waits its turn (`waits for lock port:4300 (job 884)`), whatever the caps leave. Use it for jobs that bind one fixed port, or share any other resource the scheduler can't see, across sessions and worktrees: `--lock port:4300 --lock port:4310`. A lock is a name of 1–64 letters, digits and `. _ : / -`; it is host-wide, and freed when its holder ends, however it ended, so unlike `--after` a failed holder cancels nothing. A job waiting for a lock never holds the queue back. When a job fails and its log says a port was already in use, its message suggests the lock to take. A daemon older than this build ignores locks, so `submit` and `run` cancel the job and say so.
- **Parallelism.** The CPU claim is the job's parallelism: loom sets `CARGO_BUILD_JOBS`, `RUST_TEST_THREADS`, `NEXTEST_TEST_THREADS`, `MAKEFLAGS=-jN`, `NIX_BUILD_CORES`, `CMAKE_BUILD_PARALLEL_LEVEL` and `GOFLAGS=-p=N` unless your environment already sets them, and `LOOM_JOB=<id>` always. It also sets `SCCACHE_CLIENT_SIDE=1`, so sccache runs each compile inside the job, where it is measured and held to the claim, rather than in its shared daemon (set it to `0` to keep a job server-side; sccache ignores it when `SCCACHE_LOG` is set). On Linux a running job's cores and memory sum every process in its session, short-lived ones included.
- **Confinement.** On Linux each job runs in a systemd scope of its own (`loom-<tag>-job-<id>-<attempt>.scope`), created by your user's systemd before the command starts, in a slice of the daemon's (`loom_<tag>.slice`). Its CPU share is weighted by its claim, and the slice is weighted below your agents' panes (`jobs_cpu_weight`), so a busy queue yields to an agent that needs a core. The queue reads each job's memory (its working set: what it uses less file cache the kernel can drop) and CPU time from its cgroup, every process it started included, and a cancel's SIGKILL reaches the whole scope. When systemd can't be reached, or `contain` is off, jobs run as before, sampled from their sessions, and the work view says `uncontained: <why>`; the daemon tries systemd again every minute.
- **Greedy admission.** While jobs are contained, the queue admits by forecast rather than by summed claims. It packs jobs into a memory budget: what the running jobs use now plus the memory the host has available, less a reserve for everything outside the queue (`interactive_reserve_gb`), up to `memory_gb`. Each command whose last runs left 3 usage curves (memory over its progress, rise and fall) is predicted from them: a running job from where it is now, a waiting one from its start, each step given 15% and widened in time; a job starts if every job's prediction summed stays inside the budget until its expected end, so two jobs whose peaks come at different times run together though their claims would not fit. A command without curves counts at its claim. Cores are shared by weight, not capped: a job waits only while the jobs' CPU pressure is at `cpu_pressure_pct` or more, or while the claimed cores would pass `cpu_overcommit` × `cpus`. The work view shows `budget 19 GB · forecast peak 16.0 GB`, and a waiting job says why: `needs ~11 GB at its peak (forecast leaves 6)`, `CPU busy (pressure 34%)` or `CPU overcommitted (30/28 claimed)`.
- **When memory runs short.** Greed can lose: two jobs' peaks can meet after all, or your agents can grow. The daemon keeps the jobs' slice's `MemoryHigh` at the budget, so the kernel slows and reclaims the jobs together first. If the host's available memory still falls below the reserve, or the jobs stall on memory 10% of the time, the most recently started job (a job the starvation guard started goes last) is **squeezed** (its `MemoryHigh` set to what it uses: it slows, and its cold pages go to swap); 15 s later, if memory is still short, it is **paused** (frozen); 30 s after that it is killed and **requeued**: it keeps its ID and its place in line, its earlier output moves to `<job dir>/attempt-1/`, and its owner is told (`[loom] job 14 "tests" was stopped and requeued: memory ran short (1.2 GB free); it keeps its place in line`). A job submitted with `--no-requeue` stops at the pause, however long; an `--exclusive` job is never touched; a job requeued twice runs at its full claim from then on. Nothing new starts while a job is squeezed or paused (`memory is short (job 14 paused)`). Once memory has been calm for 30 s (2 GB above the reserve, pressure under 2%), the oldest laddered job is restored first, one every 15 s. A kernel out-of-memory kill takes a whole job and requeues it the same way. The work view and `loom job list` show `squeezed` or `paused (memory)` after the claim, and `run`/`wait` print `job 14 squeezed: memory is short`, `job 14 paused: memory is short`, `job 14 resumed`, and on a requeue `job 14 requeued: …; following its next attempt`, then follow the new attempt's log. A squeezed or paused run teaches only memory floors.
- **Borrowing.** With `borrow` on (it is off by default), a job greedy admission holds back for memory alone may start as a *borrower*: it runs on memory the firm jobs claim but don't use now, counted at their measured use plus 15%, with every borrower counted at its own forecast, so borrowers never lend to each other. A job submitted with `--no-requeue`, an exclusive or agent job, one requeued twice, and the attempt after a give-back never borrow. When the host's available memory falls below the reserve plus 2 GB, the most recently started borrower gives its memory back, one every 10 s: one that has run under 2 minutes is restarted (it keeps its ID and place in line, and its next attempt runs only if it fits without borrowing); an older one is frozen and its memory pushed into swap (`memory.reclaim`), and thawed once it fits again and 30 s have passed. Nothing new borrows while a job is lent. A borrower that comes to fit with every job at its forecast becomes an ordinary job. The work view shows `borrowing`, `lent (memory) 3m` and `· borrowed 6.0 GB` in its header; `run` and `wait` say `started … on borrowed memory`, `lent its memory back: paused`, `resumed` and `restarted to lend memory back`; give-backs send no message, and the end message says how often a job was restarted or how long it was lent. Turn it on or off in Settings (`S`) → Job Borrowing, one setting for every workspace (`borrow` in the global `~/.loom/config.json`).
- **Owner and messages.** A job submitted from a loom session belongs to it (loom reads `$LOOM_LAUNCH`); one from a plain shell has no owner. `submit` messages the owner's Claude when the job ends, through the loom mod: `[loom] job 14 "tests" finished: exit 0 after 3m10s`, the log's path and its last lines (`--tail N`, default 20; `--no-notify` sends none). `run` sends none while it follows, but if it is interrupted (a Bash timeout, say) and the job has an owner, it asks for one and leaves the job running. A message the session cannot take (it is not running, is busy, or has no live loom mod) is never typed into its pane: the work view marks the job and says why.
- **Following.** `run` and `wait` print the log on stdout and status lines (`loom: job 14 started after 1m42s`; `loom: job 15 held: runs after job 14 (running)` while a job waits on another, and `loom: job 15 started 2s after job 14 ended` once it is released) on stderr, and exit with the job's exit code, `128+N` for a job killed by signal N, or `125` for one that was lost, could not start, or was cancelled. `125` also means the follower itself could not go on: it lost the daemon, the daemon does not know the job or never reported it, or the job's `output.log` could not be read. Its stderr line says which and, when the job may still be running, how to follow it again (`loom: lost the daemon; job 14 may still be running (loom job wait 14 follows it again)`). `run` also exits `125` when it submitted nothing (a bad command line, no daemon, a refused submission such as a claim over a cap), with a line saying so; `submit` exits `1` for a refusal. An interrupt of the follower exits `130` (SIGINT), `143` (SIGTERM) or `129` (SIGHUP) and leaves the job running. `1` is left to the job's own exit code; only a usage error rejected before the command does anything exits `1` too.
- **Cancelling.** A queued job is cancelled at once; a running job gets SIGTERM to its whole process group, and SIGKILL if it is still there 10 s later. Pausing, killing or resetting a session when you ask (or a script does) cancels its jobs first, with no message. When loom pauses a session on its own because its tmux session died, its jobs keep their places and run on.
- **Daemon restarts.** Running jobs live in sessions of their own, so stopping or upgrading the daemon leaves them running, and the next daemon adopts and settles them before it admits anything new. Under systemd this needs `KillMode=process` in the unit ([Keep the daemon up](#keep-the-daemon-up-a-systemd-user-unit)). A job whose runner vanished without a result (a reboot) is marked `lost`.
- **The work view.** `w` shows the host's caps, what the running jobs claim and use, the running, queued and recent jobs, and the selected job's log tail (keys: [Work View](#work-view-after-pressing-w)). A session's card in the rail and the overview shows `⚙ job 14` (with `+N` for more running) or `⚙ queued`.
- **Files.** Everything is under `~/.loom/jobs/`: `jobs.json`, `beliefs.json` (what each command measured, at most 1,000 commands and 20 runs each) and, per job, `<id>/output.log` (no size cap; `keep_finished` bounds how many finished jobs are kept). A `jobs.json` the daemon cannot read is left untouched and the queue refuses submissions, saying why; a `beliefs.json` it cannot read is left untouched too, and jobs take the default claim until it is fixed (`learning off` in `loom job list` and the work view).

### Feedback from Sessions

Loom's session agents use loom's own tooling (`loom session`, `loom job`, the daemon) and are the first to hit its rough edges. Loom's Claude context asks every agent, subagents included, to file a note when loom's tooling fails, surprises it, contradicts what the context says, or lacks something it needed:

```
loom feedback add --kind bug --title "loom job run: --after refuses another session's job" \
  --text "Ran loom job run --after 14 -- go test ./...; it said no such job 14, though loom job list shows it"
```

The daemon records which session filed it (title, workspace, branch, subagent), the session's model and effort, its loom build and where its transcript is; none of that comes from the note's text. A headless `claude -p` (Haiku at low effort, with the scheduler agent's lockdown) files each note into the open thread about the same problem, or opens a new one, so recurrence shows: `loom feedback list` ranks threads by how many sessions hit each. A closed thread is never reopened: a problem that comes back opens a new thread.

```
loom feedback list [--all] [--json]   # threads and loose notes, most-reported first
loom feedback show 3                  # a thread with every note; show n12 for one note
loom feedback fix 3 [-w loom]         # start a session that fixes it, prompted with every note
loom feedback close 3
```

`fix` starts a session titled `fb-<thread>-<slug>` in the workspace you name, else `feedback.workspace`, with the workspace's default launch options, as `loom session new` would. Its prompt holds every note and asks it to check first whether the base branch already fixes the problem, since a daemon often runs an older build than your branch. The thread shows `fixing` with the session until you close it. Agents may run `add`, `list` and `show`; `fix` and `close` are yours. Everything is under `~/.loom/feedback/`: `feedback.json`, and `grouper.jsonl`, one line per grouping call (the model, tokens, cost, the answer or the error). A `feedback.json` the daemon cannot read is left untouched and notes are refused. Set the block in the global `config.json`:

```json
{
  "feedback": {
    "workspace": "loom",
    "grouper": { "enabled": true, "model": "haiku", "effort": "low", "account": "" }
  }
}
```

`grouper` takes the scheduler agent's fields and bounds (`wait_seconds` aside). A failed call leaves the note ungrouped, shown as its own row and retried 10 minutes later; three failed calls in a row pause the grouper until the daemon restarts or the block changes. With the grouper off, notes stay ungrouped rows.

### Supervised Plans (the Agent Flow)

Loom ships the agent flow: a pinned roster of subagents that runs an implementation plan story by story. Every Claude session loom launches gets it as the `flow` plugin, beside the loom mod, unless the `flow` setting is off:

- `/flow:init` drafts the repo's profile, `.claude/flow.toml` (gates, landmines, review idioms), for you to commit;
- `/flow:draft <plan>` has drafters write a plan's stories from its spec and skeleton;
- `/flow:supervise <plan>` runs them: a supervisor dispatches a developer per story, a verifier for the gates and reviewers for each story and the whole, and asks you what the spec doesn't settle;
- `/flow:run <plan>` runs them with loom's run engine instead: a `run` job that dispatches each step as a headless agent job, runs the gates itself, parks on every question for the session to answer from the spec or ask you, and writes the run's record. Pausing the session spares the run; killing it ends the run;
- `/flow:retro` reads the run records and proposes changes to the roster or a profile.

The five are yours to type; agents never start them. The agents are `flow:<name>` (`flow:verifier`, `flow:intermediate-dev`, …). A PreToolUse hook loom adds to each session, `loom flow guard`, blocks the git commands that would sweep or rewrite the shared worktree for the developers and the drafter. Each launch gets its own copy of the plugin and its tools in its hooks folder, so a session keeps the version it launched with. Run records and retros live in `~/.loom/flow/records/`. The tools the agents call are `loom flow <tool>` (see [Flow Subcommands](#flow-subcommands)); they need `python3` 3.11 or later on `PATH`.

Coming from the standalone flow (`~/.claude/flow`, linked by its `install.sh`)? Once no supervised run uses the old copy, run its `install.sh --uninstall` (and again with `CLAUDE_CONFIG_DIR` set to each loom account's config dir), then `loom flow import ~/.claude/flow`: it copies the run records and prints where each repo's profile belongs.

### Session Recovery (orphaned worktrees)

If Loom crashes or its state file loses track of a session, the worktree
and agent may still exist on disk. Whenever the daemon starts (and when a
workspace is registered), Loom scans each workspace's worktrees folder
(`~/.loom/worktrees/` for the global one) for directories it isn't
tracking:

- **Stale leftovers** (no live agent, no uncommitted changes) are removed
  automatically. A summary line reports the count — the branch itself is
  never deleted by this cleanup.
- **Locked worktrees** are left alone: a worktree you locked with `git worktree lock` is never auto-cleaned (or removed by kill, which tells you to run `git worktree unlock`; pause leaves the tree and its lock alone). A worktree left locked "initializing" by an interrupted `git worktree add` is unlocked and cleaned once the lock is older than six minutes.
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

**Stashes from older pauses**: a loom from before pause kept the worktree
stashed uncommitted work on pause (`[loom] stash from '<title>' …` in
`git stash list`); resuming such a session re-applies it. If re-apply
conflicts, the session returns to paused, the stash is preserved, and the
error names the stash SHA so you can resolve it manually in the worktree.

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

`loom account add`/`list`/`sync`, and `login default`, refuse when run from
a shell whose own environment already points at an account's config dir
(inside an account's own agent pane, say) — run them from an ordinary
shell instead; `loom account login <name>` for an extra account is
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

Registering and unregistering take effect in a running loom. `loom workspace remove <name>` closes the tab of an open loom ("X is no longer registered; closed its tab"); its agents keep running, and adding the directory again brings them back. `loom workspace rename <old> <new>` keeps the tab and its sessions under the new name. Both are refused while one of the workspace's sessions is loading or being killed.

---

## CLI Reference

### Usage

```
loom [flags] [directory]
loom [command]
```

`loom <directory>` opens the git repository at that path: its workspace when it is registered, otherwise the global workspace, with a prompt to register it.

### Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--program <prog>` | `-p` | Program this loom's new sessions run (e.g. `aider --model gpt-4`); other open looms keep their own |
| `--workspace <name>` | `-w` | Start on this registered workspace |
| `--no-scripts` | | Skip loading `~/.loom/scripts` (the embedded default keymap still loads), to recover from a broken user script |
| `--version` | | Print the version, as `version` does |
| `--log-level <level>` | | `debug`, `info`, `warn` or `error`; accepted by every subcommand and wins over `LOOM_LOG_LEVEL` |

### Commands

| Command | Description |
|---------|-------------|
| `version` | Print version number |
| `debug` | Print config paths and loaded configuration, the daemon (pid, socket, build and tmux server, or "not running") and its log, and the tmux server a daemon started now would use (the last daemon's while it runs). Its Claude temp root is the one your shell's environment gives, not necessarily the daemon's |
| `serve` | Run the loom daemon in this terminal (loom starts one in the background when none runs; see [The Loom Daemon](#the-loom-daemon)) |
| `serve stop` | Stop the daemon: it finishes any pause or kill in progress, saves and exits. Sessions keep running; every open loom waits for a daemon (`Ctrl+R` starts one) |
| `reset --force` | Delete a workspace's instances (the global one's, or `--workspace <name>`'s), kill its tmux sessions (on the tmux server the last daemon used, while it runs) — only those started in its repo or worktrees directory; other workspaces' sessions keep running — and remove its worktrees **and their branches**, with any uncommitted work in them (paused sessions' included). Stops before removing worktrees if the tmux cleanup fails. While the daemon runs it makes the reset, waits for it and refuses while a session of the workspace is loading or being killed; with none running it works directly. |
| `workspace` | Manage workspaces (see below); a client of the daemon, which it starts when none runs |
| `account` | Manage extra Claude accounts (see below, and [Run Sessions on Several Claude Accounts](#run-sessions-on-several-claude-accounts)); a client of the daemon too |
| `session` | Create, list, message, pause, resume and kill a registered workspace's sessions from a shell (see below); a client of the daemon too |
| `web` | Serve the fleet to a paired phone: enable, disable, pair, devices, revoke and status (see below, and [Use Loom from Your Phone](#use-loom-from-your-phone)); a client of the daemon too |
| `sweep` | Run the build sweep now: delete idle and paused sessions' unused Rust binaries (`--dry-run` lists them, `--after <hours>` sets the threshold, `-v` lists each file; see [Build Sweep](#build-sweep)); a client of the daemon too |
| `job` | Submit, follow, list and cancel jobs in the host's work queue (see below, and [The Work Queue](#the-work-queue)); a client of the daemon too |
| `feedback` | File, list, show, fix and close notes about loom that its session agents write (see below, and [Feedback from Sessions](#feedback-from-sessions)); a client of the daemon too |
| `flow` | The agent flow's tools: plans, profiles, gates and the git guard (see below, and [Supervised Plans](#supervised-plans-the-agent-flow)); runs without the daemon |

### Workspace Subcommands

| Command | Description |
|---------|-------------|
| `workspace add [path]` | Register a git repo as a workspace (defaults to current dir) |
| `workspace add --name <n> [path]` | Register with a custom name |
| `workspace list` | List all registered workspaces |
| `workspace remove <name>` | Unregister a workspace (data preserved): the daemon stops serving it and open looms close its tab, and its agents keep running |
| `workspace use <name>` | Set the default workspace |
| `workspace rename <old> <new>` | Rename a workspace; open looms keep its tab under the new name |
| `workspace status [name]` | Show instance counts as the daemon serves them (defaults to CWD workspace) |
| `workspace migrate [--dry-run] [--backup=false]` | Move global instances to their matching workspace directories (refused while the daemon runs). `--dry-run` prints the plan and changes nothing; each `state.json` it overwrites is first copied to `state.json.bak` unless `--backup=false` |

### Account Subcommands

| Command | Description |
|---------|-------------|
| `account add <name> [--no-login]` | Create `~/.loom/accounts/<name>`, link your Claude setup into it, and run `claude auth login` for it (skipped with `--no-login`) |
| `account login <name>` | Log an account in again; `default` is your main login |
| `account list` | Each account's email, plan and 5-hour/7-day usage. The daemon probes usage only once an extra account exists, so with only the default account the usage columns are empty |
| `account use <name>` | The account new sessions preselect |
| `account sync` | Share entries added to your main Claude config dir since the accounts were made |
| `account remove <name> [-y] [--force]` | Remove an account; refused while a session uses it or its dir holds unshared files. `-y` skips the confirmation, `--force` overrides both refusals |

### Session Subcommands

Each acts on a registered workspace: `-w <name>`, else the one whose repository holds the current directory. Sessions are named by exact title; when two share a title, `--id` (from `session list --json`) picks one.

| Command | Description |
|---------|-------------|
| `session new <title> [flags]` | Create and start a session with the workspace's default program and the launch options the Session Launch Options screen would pre-fill, and return once it runs. Flags: `--prompt <text>` or `--prompt-file <file>` (sent once the agent is up: through the loom mod, as `session send` delivers, or typed into the pane when the session runs without the mod), `--branch <existing>`, `--issue <n>` (links it), `--account`, `--model`, `--permission-mode` (`default` passes no flag and leaves the mode to Claude, whose own default is auto; `manual` is its ask-before-acting mode), `--effort`, and `--no-remote-control`, which a session needs when remote control is on and its account's login can't use it. Refuses a title the workspace already has. Ctrl-C stops waiting; the daemon still starts the session |
| `session send <title> [--text <t> \| --file <f>] [--from <name>] [--wait] [--id <n>]` | Hand a message to the session's Claude through the loom mod, never typed into the pane: it waits in Claude's prompt queue behind any running turn or open dialog, and messages that wait through the same turn are answered together, as one turn. The text comes from `--text`, `--file` or stdin; `--from` names the sender on a line before it. Returns once Claude has queued it; with `--wait`, once the turn that carries it ends, printing Claude's reply (for merged messages, the one answer they share) alone on stdout, and failing when the turn ended without an answer. A message whose first word is `/name` runs that slash command instead of being sent as text: `/compact [instructions]`, `/clear`, `/model <name>` (a bare `/model` opens a menu), or any skill, plugin or user command the session's Claude listed when its loom mod said hello; with `--wait` it returns once the command ran, printing its output or `Ran /name.` (a skill's run ends as its turn begins). Every other built-in (it may open a menu or end the session), an alias, a name the session doesn't list, `--from`, and a session whose mod predates commands (pause and resume it) are refused; a first word holding a second `/` (a path) is plain text. Refuses an empty message, a session that is busy (starting, resuming or pausing), another agent than Claude, and a session with no live mod, saying why: the daemon serves none (on Windows), the session was launched before this daemon started and its mod has not said hello (common right after an upgrade: `session pause` then `session resume` relaunches it with the mod), its launch has no hooks, or its Claude never loaded the mod. Ctrl-C stops waiting; a queued message stays queued |
| `session list [--json]` | The workspace's sessions: title, status, branch, account and time in that status. `--json` prints `id`, `title`, `status`, `wait_reason`, `status_since`, `branch`, `worktree`, `repo`, `program`, `account`, `issue` and `pr` |
| `session kill <title> [-y] [--id <n>]` | Kill a session (its agent, tmux session and worktree) after asking; `-y` skips the question |
| `session pause <title> [--id <n>]` | Pause a running session: its agent stops; its worktree and branch stay as they are |
| `session resume <title> [--id <n>]` | Resume a paused session |

### Job Subcommands

A job's command follows `--`. The claim flags are `--cpus <n>`, `--mem <gb>`, `--io` and `--exclusive`; `-d`/`--desc` names the job (default: the command line, cut to 60 characters).

| Command | Description |
|---------|-------------|
| `job submit [-d <desc>] [claim flags] [--tail <n>] [--no-notify] -- <cmd…>` | Queue the command and return: `job 14 queued (3rd in line, 2 running)` or `job 14 started`. A refusal (a claim over a cap, admission paused, a bound exceeded) is printed with exit 1. The owner's Claude is messaged when the job ends; `--tail` (0–200, default 20) sets how many log lines the message carries |
| `job run [-d <desc>] [claim flags] -- <cmd…>` | Queue the command and follow it: the log on stdout, status lines on stderr, the job's exit code (see [The Work Queue](#the-work-queue)) |
| `job wait <id>` | Follow an existing job the same way |
| `job logs <id> [--tail <n>] [-f]` | Print the job's output, all of it or the last `n` lines; `-f` follows until the job ends |
| `job list [--all]` | The running and queued jobs, with why each waits; `--all` adds the finished ones kept |
| `job cancel <id>` | Cancel a queued or running job; refused for a finished one |

### Feedback Subcommands

| Command | Description |
|---------|-------------|
| `feedback add --kind <bug\|friction\|missing\|docs> --title <t> (--text <x> \| --file <f>)` | File a note from inside a session loom launched (it reads `$LOOM_LAUNCH`, and the effort from `$CLAUDE_EFFORT`); prints `[loom] feedback n12 filed`. Refused outside a session, and for a title over 120 characters or a text over 20,000 bytes |
| `feedback list [--all] [--json]` | The threads that aren't closed and the notes no thread holds, by how many sessions reported each, then the latest note; `--all` adds closed threads |
| `feedback show <thread \| nN>` | A thread with every note in full, or one note |
| `feedback fix <thread> [-w <workspace>]` | Start the thread's fix session and return once it runs (refused while its last fix session is still there) |
| `feedback close <thread>` | Close a thread |

### Flow Subcommands

Inside a session loom launched, each tool runs from the session's own copy (`$LOOM_FLOW_ROOT`); outside one, from a temp copy of this binary's. A tool exits with its script's code, and 125 when it could not run (no `python3`, or a `$LOOM_FLOW_ROOT` that is not a flow root).

| Command | Description |
|---------|-------------|
| `flow conf <key\|status\|show --for ROLE\|state\|drafts STEM\|vacuous FILE\|roster\|gates …\|doctor>` | The repo's flow key, profile and role blocks, its state and drafts directories, the gates to run, and the roster ID |
| `flow check-plan <plan>` | Check that a plan is ready for the supervised option |
| `flow packet`, `flow join`, `flow premises`, `flow conformance` | The drafting and review tools `/flow:draft` and the supervisor run |
| `flow costs session [<id>] [--since T] [--until T]`, `flow costs records [FILE …]`, `flow costs run <run>` | Token usage and API-price cost of a session's agents, totals over run records, or a `/flow:run` run's manager sessions and headless agent runs |
| `flow doctor` | Check that a supervised run can start here (`flow conf doctor`) |
| `flow check-agents` | Lint the flow plugin's agents and skills |
| `flow wait start <name> -- <cmd>`, `flow wait wait (<name> \| --job <N>) [secs]` | Queue a long step for an agent as a job and print its ID; wait for it inside a turn (75: still running, 70: lost); refuses polls |
| `flow agent run <role> [--effort E] [--name N] < brief`, `flow agent wait [<id>] [secs]`, `flow agent answer <id> < answer`, `flow agent show\|list\|cancel [<id>]` | Run a flow agent headless as an agent job (inside a session: its account, flow root and permission mode, the git guard, your plugins off), wait for its report inside a turn (0: a report, 1: failed or cancelled, 75: running), and resume it with an answer |
| `flow run start <plan>`, `flow run wait [<run>]`, `flow run answer <run> --from spec\|owner < answer`, `flow run show\|list\|resume\|cancel [<run>]` | The run engine behind `/flow:run`: start a run (its engine is a `run` job; it refuses unless `claude_permission_mode` is `auto` or `bypassPermissions`, since its agents run headless), wait with no cap until it parks on a question or ends (0: parked or ended, 1: cancelled or the engine lost, with its cause; a restarted daemon is joined again), answer the question in the form it takes (a decision, or `stop`, `accept`, `retry`, `continue`; an answer that carries an edited plan re-reads its stories' line ranges) and say where the answer came from (`spec`: settled without you; `owner`: you were asked), which the record counts, queue a lost engine again, or cancel the run and write its record |
| `flow gates start (--story <commits>… \| --final)`, `flow gates wait [<run>] [secs]`, `flow gates cancel [<run>]` | Queue a story's gates or the final gates as a chain of jobs, wait for the run inside a turn and print each step's verdict (0: passed, 1: failed, 75: not ended, or the daemon still away after a restart), or cancel it; a start or wait that loses the daemon mid-submit joins it again and queues no gate twice |
| `flow guard` | Loom's PreToolUse hook: the git guard for flow's developers and drafter (reads the hook's JSON on stdin) |
| `flow path <file>` | The absolute path of a file in this session's copy of the plugin (`docs/dispatch.md`, …); inside a session only |
| `flow import <checkout>` | Copy a standalone flow checkout's run records into `~/.loom/flow/records` (an identical one is skipped, a differing one refused and listed: exit 1) and print where each of its profiles belongs |

### Web Subcommands

| Command | Description |
|---------|-------------|
| `web enable --listen <addr> --origin <url>` | Turn the web app on, or change it: `--listen` is a loopback `127.0.0.1:<port>` or `[::1]:<port>`, `--origin` the `https://<host>` the phone opens. Both are required and checked by the daemon, which applies them at once; the command waits up to 5 s and says whether it serves or why not. Running it again with the same values changes nothing while the web serves, and retries a bind that failed (the port was taken) |
| `web disable` | Turn the web app off; paired devices and the origin are kept, pending pairing codes are voided, and no push is sent until it is on again |
| `web pair [--name <device>]` | Print a one-time code (`XXXX-XXXX`, default name `iPhone`), when it expires and the origin to enter it at. Refused while the web is off, for a name already paired, and at sixteen devices |
| `web devices [--json]` | Each device: name, when paired, last seen, notifications, push state (`ok`, `expired`, `failing: <status>`, or none) |
| `web revoke <name>` or `web revoke --all` | Unpair a device, or every device: its app returns to pairing and its push subscription is dropped |
| `web status` | The settings, whether the web app serves or why not, and the number of devices |

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

# Start a session from a shell and hand it a task
loom session new fix-login -w my-project --prompt "Fix the login redirect"

# List a workspace's sessions for a script
loom session list -w my-project --json

# Ask a session a question once its current turn is done, and print the reply
loom session send fix-login -w my-project --from po --wait --text "Status?"

# Serve the fleet to a phone, then pair it
loom web enable --listen 127.0.0.1:7420 --origin https://loom.example.com
loom web pair --name iPhone
```

---

## Configuration

Configuration is stored in `~/.loom/config.json` for the global workspace, and in `<repo>/.loom/config.json` for a registered one. A workspace's file stands alone: nothing is merged in from the global one, and a missing file or field takes the built-in default. Two settings, `claude_tmp_archive_dir` and `build_sweep_after_hours`, are read from the global file only.

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `default_program` | string | `"claude"` | Program to run in new sessions. Can be a profile name. |
| `branch_prefix` | string | `"{username}/"` | Prefix for auto-generated branch names |
| `base_branch` | string | `""` | Branch new sessions are cut from. Empty auto-detects. |
| `theme` | string | `"afterglow"` | UI color theme (`"afterglow"` or `"legacy"`) |
| `profiles` | array | `[]` | Named program configurations |
| `claude_remote_control` | bool | `true` | Launch Claude sessions with `--remote-control`, named after the session title |
| `claude_permission_mode` | string | `"default"` | `--permission-mode` for new Claude sessions: `default` (passes none, leaving it to Claude), `manual`, `acceptEdits`, `plan`, `auto`, `dontAsk` or `bypassPermissions` |
| `claude_model` | string | `"default"` | `--model` alias for new Claude sessions: `default` (passes none), `sonnet`, `opus`, `fable` or `haiku` |
| `claude_1m_context` | bool | `false` | Append `[1m]` (long context) to the model alias, for the aliases that take it (`sonnet`, `opus`, `fable`) |
| `claude_effort` | string | `"default"` | `--effort` for new Claude sessions: `default` (passes none), `low`, `medium`, `high`, `xhigh` or `max` |
| `cache_ttl_1h` | bool | `false` | Launch Claude sessions with `ENABLE_PROMPT_CACHING_1H=1` (a one-hour prompt cache) |
| `headroom_proxy` | bool | `false` | Point Claude sessions' `ANTHROPIC_BASE_URL` at a Headroom proxy on `http://127.0.0.1:8787`, which you run yourself; turning it on turns `claude_remote_control` off, and the reverse |
| `claude_loom_context` | bool | `true` | Give Claude sessions loom's context prompt (`--append-system-prompt-file`), which tells the agent about its worktree and `loom session`; their subagents, forks aside, get a cut of it at their start |
| `claude_subagent_tracking` | bool | `true` | Show the subagents and teammates a Claude session runs (see [Subagent Tracking](#subagent-tracking)) |
| `flow` | bool | `true` | Give Claude sessions the agent flow: the `flow` plugin, its git guard hook and its paragraph in loom's context (see [Supervised Plans](#supervised-plans-the-agent-flow)). Takes effect at each session's next launch |
| `claude_tmp_archive_dir` | string | `""` | Where Claude temp-dir and chat archives go, for every workspace (global `config.json` only; absolute or `~/…`). Empty keeps each workspace's in its own loom folder. See [Claude Temp-Dir Archives](#claude-temp-dir-archives) and [Claude Chat Archives](#claude-chat-archives) |
| `build_sweep_after_hours` | int | `0` | Turns the build sweep on: every 30 minutes the daemon deletes the Rust executables in idle and paused sessions' cargo target dirs that have gone unused this many hours (global `config.json` only; `0` is off, at most `8760`, a year). Set it in Settings (`S`) → Build Sweep. See [Build Sweep](#build-sweep) |
| `scheduler` | object | host defaults | The work queue's caps and default claim (global `config.json` only). See [Scheduler Caps](#scheduler-caps) |

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

### Claude Chat Archives

Claude Code keeps each session's conversation in `~/.claude/projects/<the session's directory, encoded>/` (under `$CLAUDE_CONFIG_DIR` when that is set; an extra account's `projects/` links to the main one), and deletes a transcript once it has gone unwritten for `cleanupPeriodDays` (30 by default). A loom session's directory is its worktree, which nothing reuses, so a session's conversation would vanish a month after it ended, or a month into a pause. Loom keeps a copy:

- **Pause** and **kill** copy the session's conversation (its transcripts, its subagents' and its tool results) into a zip. Claude's own files are never moved or changed.
- **Resume** puts a paused session's transcript back if Claude deleted it during the pause, so Claude picks the conversation up again (`--resume`) instead of starting over. If that fails, you see why and the session starts with `--continue`.
- **At every workspace load**, loom copies the conversations of its sessions that aren't running (paused, killed, or ended before loom archived chats) when they changed since their last copy.
- A new copy replaces the old zip but keeps everything in it that Claude has since deleted, so nothing once archived is lost.
- Archives live beside the temp-dir archives: `<repo>/.loom/archive/chats/` (or `~/.loom/archive/chats/`), or `<claude_tmp_archive_dir>/<workspace folder>/chats/` when that is set. `loom debug` prints the folder.
- Loom never deletes them. To read one:

  ```sh
  unzip -l <file>.zip                     # list the sessions in it
  unzip -p <file>.zip <session id>.jsonl  # print a transcript
  zipgrep 'some phrase' <file>.zip        # search every file in it
  ```

- Only loom's worktree sessions are covered, not Claude sessions run in a repository root or outside loom.

### Build Sweep

Cargo builds into each session's own `<worktree>/target/` and never deletes a test binary it has replaced: a new feature set, profile or flag writes a new copy under a new hash. Over a few days of agents running `cargo test` that reaches tens of gigabytes per session. Set a threshold in Settings (`S`) → Build Sweep, a whole number of hours from 0 (off) to 8760 (a year), and the daemon deletes those copies once they have gone unused that long. The row sets one threshold for every workspace, whichever is focused; it is `build_sweep_after_hours` in the global `~/.loom/config.json`, which you can also edit by hand:

```json
{ "build_sweep_after_hours": 12 }
```

- **Which sessions:** only sessions whose agent is idle (Ready) or paused. The main checkout and workspace terminals are never swept.
- **Which dir:** only the worktree's own `target/`, once cargo has built into it. A `CARGO_TARGET_DIR` elsewhere is never touched, and other files you keep in `target/` stay.
- **What goes:** executables in a cargo profile dir's `deps/` and `examples/` (`<name>-<16 hex digits>`, no extension), with the hard links cargo makes to them under their plain names. Libraries, proc-macros, build scripts and the incremental cache stay. Cargo rebuilds anything deleted the next time it's needed.
- **"Unused" means the file's last access time,** so the disk must record access times. Linux only. A disk mounted `noatime` is skipped. One mounted `relatime` records an access at most once a day, so it is swept only with a threshold of 24 hours or more. For hour thresholds, mount it `strictatime,lazytime`: every access is recorded, and `lazytime` writes the timestamps out in batches. A skipped disk shows a notice once each time the daemon starts.
- **Never mid-build:** a session is skipped while cargo holds its target dir's locks, or while any of your `cargo` processes runs inside the worktree. That second check covers `cargo test`, which drops its locks before it runs the tests.
- **On demand:** `loom sweep` runs the sweep now. `loom sweep --dry-run --after 12 -v` lists what would go, file by file, and deletes nothing; `--after` works even while the setting is off. Each sweep is logged as `build_sweep.swept` and `build_sweep.skipped` in the daemon's log.

### Scheduler Caps

The work queue admits jobs against caps on the host's cores, memory and I/O-heavy jobs. They live in a `scheduler` block of the global `~/.loom/config.json` (per host; a workspace's `config.json` never holds them, and the `S` settings overlay does not show them). Edit them in the work view (`w`, then `e`) or by hand; the daemon rereads the block at every admission pass, so a hand edit applies without a restart.

```json
{
  "scheduler": {
    "cpus": 14,
    "memory_gb": 24,
    "io_jobs": 2,
    "default_claim": { "cpus": 4, "memory_gb": 6 },
    "starve_after_minutes": 10,
    "keep_finished": 50,
    "agent_jobs": 4,
    "release_past_peaks": true,
    "contain": true,
    "jobs_cpu_weight": 50,
    "interactive_reserve_gb": 4,
    "cpu_pressure_pct": 25,
    "cpu_overcommit": 2,
    "borrow": false
  }
}
```

An absent or `0` field takes the host's default:

| Field | Default | Bounds |
|-------|---------|--------|
| `cpus` | cores − 2 (at least 1) | 1–1024 |
| `memory_gb` | ¾ of the RAM in GB (at least 1; a GB is 2^30 bytes) | 1–65536 |
| `io_jobs` | `2` | 1–64 |
| `default_claim.cpus` | the lesser of `4` and `cpus` | 1 to `cpus` |
| `default_claim.memory_gb` | the lesser of `6` and `memory_gb` | 1 to `memory_gb` |
| `starve_after_minutes` | `10` | 1–1440 |
| `keep_finished` | `50` | 1–10000 |
| `agent_jobs` | `4` | 1–16: how many agent jobs (`loom flow agent`) of one account run at once; they claim nothing and are admitted apart from the caps |
| `release_past_peaks` | `true` | `true` or `false`: let a running job reserve less than its claim once its own past runs show its peaks are behind it ([Holds](#the-work-queue)) |
| `contain` | `true` | `true` or `false`: run each job in its own systemd scope ([Confinement](#the-work-queue)); Linux only |
| `jobs_cpu_weight` | `50` | 1–10000: the jobs' slice's CPU weight; an agent's pane has 100 |
| `interactive_reserve_gb` | `4` | 1–1024: the available memory greedy admission leaves to everything outside the queue ([Greedy admission](#the-work-queue)) |
| `cpu_pressure_pct` | `25` | 1–100: the jobs' CPU pressure (the share of time a job waited for a core, over 10 s) at which no job starts |
| `cpu_overcommit` | `2` | 1–8: the claimed cores greedy admission allows, as a multiple of `cpus` |
| `borrow` | `false` | `true` or `false`: start a job greedy admission holds back on memory the running jobs claim but don't use, giving it back when memory gets tight ([Borrowing](#the-work-queue)); also Settings (`S`) → Job Borrowing |

A value outside its bounds, or a default claim larger than a cap, makes the block invalid: admission pauses, running jobs are left alone, `loom job submit` is refused, and the work view says why. No cap is guessed. If the RAM cannot be read, `memory_gb` has no default and must be set. Lowering a cap never touches running jobs; it changes only what starts next.

#### The scheduler's agent

A command the queue has never measured is sized by an agent: a headless `claude -p` the daemon runs in the job's folder, with read-only file tools and none of your settings, hooks, plugins, MCP servers or CLAUDE.md files. It reads the command, its description, the job's build variables (never a variable whose name holds `TOKEN`, `SECRET`, `PASSW`, `KEY`, `CRED` or `AUTH`, and no others) and what the queue has learned of similar commands, and answers with an estimate, which the command's first done run replaces. A job waits for it at most `wait_seconds`, then takes the default claim. When a run surprises what was learned (a peak above its claim, a SIGKILL nobody asked for, a duration far off the usual), the agent writes a note on it, shown with the command's history. It is called one job at a time, not again for 10 minutes for a command whose call failed, not at all for 30 minutes after three failed calls in a row, and not while its account has used 90% or more of its 5-hour window. It is on by default whenever loom has a Claude program; set it in the `scheduler` block's `agent` object (the caps form leaves it alone):

```json
{
  "scheduler": {
    "agent": { "enabled": true, "model": "haiku", "effort": "low", "account": "", "wait_seconds": 60 }
  }
}
```

| Field | Default | Bounds |
|-------|---------|--------|
| `enabled` | `true` | |
| `model` | `haiku` | any `--model` value, 1–100 characters, no spaces |
| `effort` | `low` | `low`, `medium`, `high`, `xhigh`, `max` |
| `account` | `""` (the default account) | a registered account's name |
| `wait_seconds` | `60` | 1–600 |

A block outside its bounds turns the agent off (the work view's header and `loom job list` say why); jobs are still sized from what was measured.

To see what the agent does, run `loom job agent`: whether it is on, the call it is making, the calls waiting, failures in a row, today's calls and cost, and a table of its recent calls (when, kind, job, command, model, how long, tokens in and out, cost, and the estimate or note it gave, or why it failed or was skipped). `--json` prints the same as JSON, and `--calls N` lists more. Every call, and every call dropped before it ran, is kept in `~/.loom/jobs/advisor.jsonl` (the last 500, one JSON line each, with no request: a request holds the job's command line, description and build variables). The work view's header shows the call in flight and today's use (`agent: sizing job 17 (12s) · 2 waiting · today 5 calls, $0.06`). The cost is what the CLI reports, at API prices, also on a subscription plan.

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
| `LOOM_LOG_LEVEL` | `debug`, `info` (default), `warn` or `error`: what goes into the log files. `--log-level` wins over it. |
| `LOOM_LOG_FORMAT` | Set to `json` to write structured log records as JSON lines (older plain lines stay plain text). |
| `LOOM_PANE_RENDERER` | Set to `snapshot` to turn off the embedded terminal emulator and render panes from periodic `tmux capture-pane` snapshots instead (always the case on Windows). You lose mouse forwarding, live scroll-back, the native cursor and title/bell pass-through, and panes refresh on a timer rather than as output arrives. The daemon and each loom read their own: start loom with it to change your panes, and stop the daemon (`loom serve stop`) to change the daemon's refresh rate. |
| `LOOM_ALLOW_NESTED` | Set to `1` to let the daemon start (or `loom reset` run) inside one of loom's own tmux sessions on the tmux server it would use anyway (normally refused, because its startup cleanup could kill the enclosing loom's sessions). A loom started inside loom still never replaces an older daemon. |

The names Loom inherited from claude-squad still work, with a one-time deprecation warning on stderr: `CLAUDE_SQUAD_HOME`, `CLAUDE_SQUAD_LOG_FORMAT` and `CLAUDE_SQUAD_LOG_LEVEL`. Move to the `LOOM_*` names in your shell init.

### Logs

Each loom writes `logs/loom.log` in its config folder (`~/.loom/logs/loom.log` by default), moving it to `loom.log.1` at startup once it passes 5 MB. The daemon writes `~/.loom/logs/serve.log`, and a crash it can't log goes to `serve-crash.log` beside it. `loom debug` prints the paths. For more detail run loom with `--log-level=debug`; a daemon already running keeps its own level, so `loom serve stop` first to raise the daemon's. Every record names its component, so `grep subsystem=tmux loom.log` narrows a log to one part of loom.

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
  jobs/                              ← The work queue (jobs.json, beliefs.json, advisor.jsonl, one folder per job)
  feedback/                          ← The feedback inbox (feedback.json, grouper.jsonl)
  worktrees/

~/projects/my-app/.loom/     ← Workspace-scoped
  config.json                        ← This workspace's config (used instead of the global one)
  state.json                         ← This workspace's sessions
  worktrees/
```

### Choosing the Workspace at Startup

The TUI does not pick a workspace from the current directory:
1. `loom <directory>` opens the workspace registered for that path (beside the tabs you had open when you last quit), or the global workspace with a prompt to register the directory
2. `loom --workspace <name>` (`-w`) opens that workspace by name, beside those tabs
3. Plain `loom` reopens those tabs; with none saved and workspaces registered, the startup picker appears; otherwise it opens the global `~/.loom/` workspace (and then must be run inside a git repository when no workspace is registered)

The `loom session` and `loom workspace status` commands do go by the current directory: they act on the workspace whose repository holds it unless you pass a name.

### Workspace Picker

On launch, if workspaces are registered and no saved tabs are reopened, a picker appears to select which workspace to use. Press `Esc` to use the global default.

During a session, press `W` to toggle which workspaces are visible as tabs. Use `{` and `}` (or `l` and `;`) to switch between active tabs.

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

## Coming from claude-squad

Loom is a fork of [claude-squad](https://github.com/smtg-ai/claude-squad). The first time it runs, Loom renames `~/.claude-squad/` to `~/.loom/` in one step, so sessions, worktrees and your scripts carry over. Running agents keep running: their tmux sessions, named `claudesquad_…`, are renamed to `loom_…` before Loom reconciles them. Auto-commits are tagged `[loom]` rather than `[claudesquad]`; commits made before the switch keep the old tag. The old `CLAUDE_SQUAD_*` environment variables still work (see [Environment Variables](#environment-variables)).

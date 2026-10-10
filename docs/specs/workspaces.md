# Workspaces

Workspaces let users manage multiple git repositories as separate environments within Loom. Each workspace gets its own isolated set of instances, worktrees, config, and state — all stored in a `.loom/` directory inside the repo itself.

## Concepts

### Workspace

A registered git repository. Stored as a name + absolute path in the global registry.

```go
// config/workspace.go
type Workspace struct {
    Name    string    `json:"name"`
    Path    string    `json:"path"`
    AddedAt time.Time `json:"added_at"`
}
```

### Workspace Registry

The global index of all registered workspaces. Always stored at `~/.loom/workspaces.json` (or `$LOOM_GLOBAL_DIR/workspaces.json`), regardless of `LOOM_HOME`. Tracks which workspace was last used and which were open as tabs when the TUI last saved them (the [open list](#the-open-list)).

```go
// config/workspace.go
type WorkspaceRegistry struct {
    Workspaces     []Workspace `json:"workspaces"`
    LastUsed       string      `json:"last_used"`
    OpenWorkspaces []string    `json:"open_workspaces,omitempty"`
}
```

Example file:

```json
{
  "workspaces": [
    {
      "name": "myproject",
      "path": "/home/alice/repos/myproject",
      "added_at": "2025-06-15T10:30:00Z"
    }
  ],
  "last_used": "myproject",
  "open_workspaces": ["myproject"]
}
```

## Directory Layout

### Global (no workspaces, or "Global" mode)

All state lives in `~/.loom/`:

```
~/.loom/
├── config.json            # User configuration
├── state.json             # App state (instances, help-seen flags)
├── workspaces.json        # Workspace registry (always here)
└── worktrees/             # Git worktrees for sessions
```

### Per-Workspace

A registered workspace's state lives in `{repo}/.loom/`:

```
/home/alice/repos/myproject/
├── .loom/         # Workspace-local data (gitignored)
│   ├── config.json        # Workspace-specific config
│   ├── state.json         # Workspace-specific state & instances
│   └── worktrees/         # Worktrees for this workspace's sessions
├── .gitignore             # Contains ".loom/" entry
└── ... (repo files)
```

The `.loom/` directory is automatically added to the repo's `.gitignore` on registration (`EnsureGitignore()`).

## Isolation Mechanism

Workspaces achieve isolation through explicit `WorkspaceContext` propagation.

1. On startup, `main.go` resolves the workspace the TUI starts on (see [Startup Behavior](#startup-behavior)) as a `WorkspaceContext` (`config.WorkspaceContextFor`, or `config.GlobalWorkspaceContext()` for the global one).
2. The session model (`core.Model`) builds one `WorkspaceContext` per workspace it serves: the global one and every registered one (`core/load.go`: `boot`, `ensureLoaded`, `globalWS`). Each workspace's storage, config, state and worktrees are read and written through its own context.
3. All state reads/writes use the context's `ConfigDir` directly via `LoadConfigFrom(dir)` / `LoadStateFrom(dir)`.
4. `GetConfigDir()` honors `LOOM_HOME` (with `CLAUDE_SQUAD_HOME` as a deprecated fallback) for external tooling, but internal code passes config directories explicitly.

This means there is no explicit instance filtering — each workspace simply loads from its own state file. A workspace is one config dir, compared canonically (symlinks resolved, `session.CanonicalPath`): one directory registered under two names (through a symlink, say) is served once, under the name it was first served by, and the other name is its **twin**. Since daemon stage 3A the model loads every workspace once, at startup, and never drops one, so switching workspaces changes only which of them the TUI shows (see [Tabs and Switching](#tabs-and-switching)).

The workspace registry (`workspaces.json`) is the one exception: it always reads from `~/.loom/` via `GetGlobalConfigDir()`, since it needs to be accessible regardless of which workspace is shown.

## CLI Commands

All under `loom workspace`:

| Command | Description |
|---------|-------------|
| `workspace add [path]` | Register a git repo as a workspace. Defaults to `.`. Flag `--name` overrides the auto-derived name (directory basename). |
| `workspace list` | List registered workspaces with name, path, and status (`[last used]` or `[missing]`). |
| `workspace remove <name>` | Unregister a workspace by name. Does not delete the `.loom/` directory. The loom daemon keeps serving it until the daemon restarts. |
| `workspace use <name>` | Set the default workspace (`LastUsed`) for future invocations. |
| `workspace rename <old> <new>` | Rename a workspace in the registry. The loom daemon keeps serving it under the old name, and loom refuses to open it by the new one until the daemon restarts (`loom serve stop`, then loom). |
| `workspace status [name]` | Show instance counts for a workspace (defaults to cwd-matched workspace). |
| `workspace migrate` | Move global instances to their matching workspaces (see [Migration](#migration)). Refused while the loom daemon runs. |

The root command also accepts `--workspace <name>` (`-w`) to select a workspace by name, bypassing cwd auto-detection.

Source: `cmd/workspace.go`, `main.go`.

### `workspace add` Details

1. Resolves path to absolute.
2. Validates it's a git repo (checks for `.git`).
3. Ensures name and path are both unique in the registry.
4. Calls `EnsureGitignore()` to add `.loom/` to the repo's `.gitignore`.
5. Saves to `~/.loom/workspaces.json`.

The loom daemon serves the new workspace from the next time it rereads the registry (`core.Model.ReloadRegistry`, which every loom runs as it starts and the workspace picker runs whenever it opens).

### `workspace remove` Details

1. Finds workspace by name.
2. Removes from registry slice.
3. Clears `LastUsed` if this was the last-used workspace.
4. Saves registry. Does **not** delete on-disk data.

The loom daemon keeps serving the workspace (its sessions are still watched) until it restarts; unloading one is a later stage's work (daemon stage 3C).

## Startup Behavior

Source: `main.go`, `daemon_client.go` (`joinDaemon`), `app/app_init.go` (`startHome`, `restoreSavedWorkspaces`), `internal/daemon` (`Serve`), `core/load.go` (`Boot`).

1. **Resolve the startup workspace** (`main.go`):
   - a directory argument (`loom <dir>`) must be a git repository: if it is a registered workspace (`FindByPath()`), that workspace; otherwise the global context, and the TUI asks whether to register the directory;
   - else `--workspace <name>` (`-w`): that workspace, by name (a twin's name starts on the workspace served for its directory, with an info note: "X is the same directory as Y; showing Y"; a workspace registered at `$HOME` shares the global config dir, so it starts on the global workspace: "X shares its config dir with the global workspace; showing global");
   - else the global context. With no workspace registered, the current directory must be a git repository.

   Then loom joins the loom daemon (`joinDaemon`), starting one when none runs, and the TUI starts as its client (`startHome`), which first rereads the registry (`ReloadRegistry`), so a workspace registered since the daemon started is served, and records a named startup workspace as `LastUsed` (`SetLastUsed`).
2. **Boot the model** (`core.Model.Boot`), which the daemon does once, when it starts (`loom serve`, before it listens): the account registry, the remote-control auth detection, then every workspace it serves: the global one and each registered one, loaded once (reconcile, crash-restart, inline orphan recovery). Then one orphan tmux sweep covers them all. A workspace whose state fails to load is kept, empty and latched (see [Failed Loads](#failed-loads)). A workspace terminal does not start yet (see [Opening](#opening)). A loom joining a running daemon finds all of this done.
3. **Show a workspace** (`startHome`):
   - with a saved open list and no directory awaiting registration, `restoreSavedWorkspaces` opens each saved workspace as a tab, plus the startup workspace when it is registered and not among them, and focuses the startup workspace's tab, else `LastUsed`'s, else the first. One workspace is one tab: a name listed twice opens once, and a twin opens its served workspace's tab, with the same info note (a workspace registered at `$HOME` opens nothing, since the global workspace is no tab, with a note); a saved tab that fails to open is kept in the open list (`failedOpen`), and when none opens the startup workspace is shown in the classic slot instead (if it fails to load too, the error is shown rather than exiting);
   - otherwise the startup workspace is opened in the classic (no-tab) slot. If it fails to load, loom exits with the error.
4. **Startup overlays**: the registration prompt for a directory awaiting it, else, when starting in global mode with workspaces registered and no tabs restored, the startup workspace picker (one workspace, or Global).

Path matching uses `FindByPath()`, which matches exact paths or parent directories (with separator check to avoid `/repo` matching `/repo-fork`).

## Tabs and Switching

Source: `app/workspaces.go`, `app/state_workspace_picker.go`, `ui/overlay/workspacePicker.go`, `ui/workspace_tab_bar.go`, `core/workspaces.go`, `core/workspace_requests.go`.

The TUI shows either one workspace with no tab bar (the **classic** slot: the startup workspace, or the global workspace in global mode) or a set of **tabs**, one per open workspace, with one focused. `l`/`{` and `;`/`}` move between tabs; the overview (`tab`) shows every open tab's sessions. Which workspaces are open is the TUI's own state: the model serves every workspace whether or not a tab shows it.

### Picker UI

Users press `W` (shift+w) to open the workspace picker overlay. Opening it rereads the registry first, so a workspace another process registered appears (and is served from then on).

- Lists all registered workspaces with names and paths.
- Pre-checks the open tabs, plus the saved tabs that failed to open at startup, labelled "(failed to load)".
- Includes a "Global (no workspace)" row at the bottom.
- Shows a footer while any listed workspace failed to load: closing one (unchecking it, or Global) drops it from the open list, so loom stops retrying it at start. Its live sessions are spared either way (see [Failed Loads](#failed-loads)).
- Navigation: `j`/`k` or arrow keys. `Space` or `Enter` toggles a workspace; on the Global row it unchecks everything and commits. `Esc` or `q` commits the checked set.

Opening a workspace by a name the model serves it under another is refused, because the TUI keys its tabs, the picker and the open list on names: a twin with "X is the same directory as Y, which loom serves: open Y", a workspace registered at `$HOME` with "X shares its config dir with the global workspace, which loom serves: pick Global", and a workspace renamed while the daemon runs with "... stop the daemon (`loom serve stop`), then start loom, to open it as X".

The startup picker (step 4 above) is single-select: `Enter` opens the workspace under the cursor as a tab, or stays global on the Global row.

### Commit

`applyWorkspaceToggle` diffs the checked set against the open tabs:

1. **Open** each newly checked workspace first (`openTab`), so a failure leaves the current tabs in place. The first tab opened from the classic slot takes focus and replaces it.
2. **Close** each unchecked tab (`deactivateWorkspace`), unless no checked workspace opened, in which case the open tabs are kept and the failures reported. Closing the focused tab focuses the one that slides into its place. The last tab is never closed this way: leaving every workspace means global mode.
3. Persist the [open list](#the-open-list) and show the focused workspace's recovery summary.

Nothing is saved or dropped in the model by a commit: a closed tab's workspace stays served, its agents still probed by the health tick and spared by the orphan sweep, and reopening it shows the same sessions under the same IDs.

### Opening

Every path that shows a workspace (a tab, the classic slot, global mode, the startup restore, a just-registered workspace) calls `core.Core.Open` (`core/workspace_requests.go`), which:

- retries a workspace whose load failed, rereading its state from disk, and returns the error while it still fails (the TUI then shows nothing of it);
- on the workspace's **first open** since the daemon started, starts its workspace terminal (creates it, or relaunches one that died while nobody had the workspace open, or one that is Paused) and adds its repository to the GitHub poll (with its own base branch; the global workspace stands for the repositories its sessions run in, unless one is an opened workspace's repository), then saves the workspace so the terminal's record survives a crash. Until then the terminal is dormant: not probed, and not relaunched or paused by the health tick. A relaunch first asks whether another session holds the terminal's tmux name (`session.HeldElsewhere`): tmux names are per server, and another workspace's agent, or another loom's terminal, can carry it. One that does is left running and the terminal stays Paused; a check tmux leaves unanswered changes nothing, and the health tick asks again until it answers.

A later open, by another tab or another client, shows the same workspace and starts nothing.

The same name check guards the session actions, since a record can share its tmux name with another workspace's live session: a kill or discard leaves that session (and its terminal-pane shell) running and cleans up only what is the record's own, a resume refuses, and both refuse, changing nothing, when tmux can't say whose the session is.

### Global Mode

Picking Global (or unchecking every workspace) from a tab set, or from a named workspace shown in the classic slot, opens the global workspace (`Open`) in the classic slot and drops what was shown; if the global workspace cannot be opened, nothing changes and the error is shown. It also forgets the saved tabs that failed to open and clears the open list, so the next launch starts in global mode. From global mode itself such a commit rebuilds nothing (`stayInGlobalMode`): it only forgets the failed tabs and persists the empty open list.

### Quit

`q` flushes the split ratios, persists the open list (when tabs are open, or the registry holds one) and quits. While the daemon is away it writes neither (only the daemon can), and logs `quit.offline_unsaved` with what was lost; the UI prefs and the focused tab changed meanwhile are otherwise kept and sent when a daemon is joined again. It saves no session: the daemon saves as sessions change, whether a request or the daemon itself changed them, and when it stops (`loom serve stop`: `SaveForQuit`, which saves every served workspace). There, a failed save of a workspace opened since the daemon started is logged, except a workspace whose storage is latched, which is skipped. A workspace nobody opened is saved too (its agents are crash-restarted at boot and paused by the tick), but its failure is only logged, and when its config dir is gone (a deleted or unmounted repository) it is skipped rather than recreated.

## The Open List

The registry's `open_workspaces` is the tabs the TUI last persisted, in tab order, followed by the saved tabs it failed to open (`failedOpen`), so a workspace that failed to load is retried at the next launch rather than closed. The TUI writes it (`persistOpenList` → `core.Core.PersistOpenList`) after a picker commit, on entering or staying in global mode, after the startup restore, and on quit. The model writes the list it is handed and never acts on it: opening, closing and restoring tabs are the TUI's. `LastUsed` names the workspace a restore focuses.

Several looms can be clients of one daemon at once (daemon stage 3B, `loom serve`): each keeps its own tabs, and the open list is one registry value, written by whichever persists last.

## Failed Loads

A workspace whose `state.json` holds a payload that is not a JSON array (see the instance-data-schema gotcha in CLAUDE.md) fails to load:

- the model keeps serving it, empty, with its storage's write latch engaged so nothing overwrites the unreadable file, and publishes the error (`WorkspaceView.LoadErr`). The boot only logs it (`workspace.load_failed` in the daemon's `serve.log`): no notice reaches the TUI, which reads its own failed opens (`failedOpen`) rather than `LoadErr`, so a failed workspace that isn't in the open list shows nowhere until it is opened;
- the orphan tmux sweep leaves its roots out of the ones it owns (its sessions' titles are unknown), so its live sessions are spared, while every other workspace is still swept;
- each `Open` rereads it from disk, so fixing the file and reopening the workspace (or restarting the daemon) recovers it;
- a saved tab that fails at startup stays checked in the picker, labelled "(failed to load)", until it opens or is unchecked; unchecking it drops it from the open list, so loom stops retrying it at start;
- the daemon's stop skips its save.

## Migration

`workspace migrate` moves instances from the global `~/.loom/state.json` to workspace-specific state files.

Source: `cmd/workspace.go`.

### Process

1. Load all instances from the global state file.
2. For each instance, match its `worktree.repo_path` to a registered workspace via `FindByPath()`.
3. Group matched instances by workspace.
4. For each workspace:
   - Load existing workspace state (if any).
   - Skip instances that already exist (by title) to avoid duplicates.
   - Update worktree paths: `~/.loom/worktrees/{name}` becomes `{workspace_path}/.loom/worktrees/{name}`.
   - Move the worktree directories on the filesystem.
   - Merge and save to workspace state.
5. Update global state to contain only unmatched (orphan) instances.
6. Print a summary of what was migrated.

### Path Rewriting

The migration rewrites the `worktree_path` field on each instance:

```
Before: ~/.loom/worktrees/alice/my_feature_abc123
After:  /home/alice/repos/myproject/.loom/worktrees/alice/my_feature_abc123
```

The actual directories are moved on disk via `os.Rename()`.

## Key Source Files

| File | Role |
|------|------|
| `config/workspace.go` | `Workspace`, `WorkspaceRegistry`, CRUD operations, `EnsureGitignore` |
| `cmd/workspace.go` | CLI commands: `add`, `list`, `remove`, `migrate` |
| `ui/overlay/workspacePicker.go` | Workspace picker overlay (Bubble Tea component) |
| `ui/workspace_tab_bar.go` | Workspace tab bar |
| `app/app_init.go` | Startup: the startup workspace, the saved-tab restore, the startup overlays |
| `app/workspaces.go` | Slots, tabs (`openTab`, `deactivateWorkspace`), global mode, the picker's commit, the open list |
| `core/load.go` | The model's boot, the one load path (`loadWS`), first open (`open`, `ensureTerminal`), the orphan sweep |
| `core/workspaces.go`, `core/workspace_requests.go` | `Workspaces`, `Open`, `Register`, `ReloadRegistry`, `PersistOpenList`, `SetLastUsed` |
| `config/config.go` | `GetConfigDir()` — respects `LOOM_HOME` |
| `config/state.go` | State loading from config directory |
| `session/git/worktree.go` | `getWorktreeDirectory()` — uses config directory |
| `main.go` | Startup workspace resolution |
| `keys/keys.go` | `KeyWorkspace` binding (`W`) |

## Design Decisions

**Isolation via explicit context, not filtering.** Rather than loading all instances globally and filtering by workspace, each workspace has its own state file. A `WorkspaceContext` value object carries the config directory and is threaded through all function calls. `LOOM_HOME` remains the user-facing override (with `CLAUDE_SQUAD_HOME` as a deprecated fallback) for external tooling.

**Every workspace served; the tabs are the client's.** Since daemon stage 3A the session model loads every registered workspace and the global one at startup and never drops one, so session lifecycle (crash restarts, the health tick, the orphan sweep) covers every workspace whether or not it is open. Which workspaces a TUI shows, and the open list it persists, are its own state; a workspace's terminal and GitHub polling wait for its first open, so a workspace nobody looks at gets no terminal and no GitHub polling, but still costs its load and reconcile at startup, the health tick's probe and the roster queries and hook scans of its agents, and its save when the daemon stops. Since stage 3B the model runs in the daemon (`loom serve`), and several looms each keep their own tabs over it. See the [daemon spec](../superpowers/specs/2026-10-03-loom-daemon-design.md).

**Registry always global.** The workspace registry must be accessible before any workspace is selected, so it lives at `~/.loom/workspaces.json` regardless of `LOOM_HOME`.

**`.loom/` is gitignored.** Workspace data (worktrees, state, config) lives inside the repo but is excluded from version control via an automatic `.gitignore` entry.

**No auto-migration.** Migration from global to workspace-scoped instances is a manual `workspace migrate` command. This avoids surprising users who haven't opted into workspaces yet.

**`workspace remove` is non-destructive.** Removing a workspace from the registry does not delete its `.loom/` directory or any sessions. The data remains on disk.

# Permanent Workspace Terminal

## Summary

Add a permanent, always-running terminal instance pinned at the top of the left panel for each workspace. It runs the configured agent program (claude, aider, etc.) directly in the root repository — no git worktree isolation. It persists and restores across app restarts.

## Approach

Extend the existing `Instance` struct with an `IsWorkspaceTerminal bool` flag. This reuses all existing infrastructure (tmux, Preview/Diff/Terminal tabs, storage) with minimal branching in lifecycle methods.

## Instance Model Changes

- Add `IsWorkspaceTerminal bool` to `Instance` struct
- Add `IsWorkspaceTerminal bool` JSON field to `InstanceData`

### Lifecycle behavior when `IsWorkspaceTerminal == true`

| Method | Normal Instance | Workspace Terminal |
|---|---|---|
| `Start()` | Creates worktree + tmux session | Skips worktree; creates tmux session in `Instance.Path` (root repo) |
| `Pause()` | Commits, kills tmux, removes worktree | Returns error (not pausable) |
| `Kill()` | Cleans up worktree + tmux + branch | Only closes tmux session |
| `GetWorktreePath()` | Returns worktree path | Returns `Instance.Path` (root repo) |
| `UpdateDiffStats()` | Diffs worktree branch vs base | Diffs root repo working tree |
| `Resume()` | Recreates worktree from branch | N/A (never paused) |

## List Rendering

- Workspace terminal is always index 0 in `l.items`
- Distinct icon (e.g. `◆`) instead of `●`
- Title: workspace name or "Workspace Terminal"
- Branch line shows current branch of root repo
- Regular instances numbered starting from 1

## Storage & Persistence

- Serialized to `instances.json` with `"is_workspace_terminal": true`
- On load: if no workspace terminal in storage, auto-create one
- On load: if one exists, restore it (reconnect tmux session)
- Kill/delete operations skip workspace terminal

## App Initialization

1. Load instances from storage
2. Check if any loaded instance has `IsWorkspaceTerminal == true`
3. If not, create one with `Title: "Workspace Terminal"`, `Path: wsCtx.RepoPath`, `Program: program`
4. Insert at index 0 in the list
5. Start it (creates tmux session in root repo)

## UI Guardrails

- Pause keybinding: no-op when workspace terminal selected
- Kill keybinding: no-op when workspace terminal selected
- Right panel: works identically (Preview/Diff/Terminal tabs)
- Exactly one per workspace, auto-created

## Decisions

- **One per workspace**: Auto-created, not user-created
- **Always running**: Cannot be paused
- **Runs agent program**: Same program as configured (not just a shell)
- **No worktree**: Operates directly in root repo
- **Persisted**: Saved/restored across restarts like other instances

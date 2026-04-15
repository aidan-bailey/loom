# Immediate UI Feedback on Session Deletion

## Problem

When a user confirms session deletion, the instance lingers in the list for 1-2+ seconds while synchronous cleanup runs (tmux close, git worktree remove, branch delete, prune, storage I/O). This feels sluggish.

## Design

### New Status: `StatusDeleting`

Add `StatusDeleting` to the Instance status enum. Rendered as "Deleting..." in the list with a distinct style.

### Modified Kill Flow

1. User presses `D`, confirms via modal
2. **Main goroutine (synchronous):** save `previousStatus`, set `instance.Status = StatusDeleting`, update UI immediately
3. **Background Cmd:** runs cleanup — git checks, tmux close, worktree cleanup, storage delete
4. **On success:** `killInstanceMsg{title}` removes instance from the list (same as today)
5. **On failure:** new `killFailedMsg{title, previousStatus, err}` reverts status to `previousStatus`, logs error

### UI Behavior

- Instances with `StatusDeleting` are **inert/unselectable**
- All interactive keybindings skip them (attach, kill, push, checkout, input, diff, resume)
- Cursor movement (`j`/`k`) skips them so they cannot be selected
- Rendered with a "Deleting..." label and muted/distinct style

### Error Recovery

- On cleanup failure: status reverts to previous, error is logged
- On app exit mid-delete: any instance with `StatusDeleting` on next startup should be retried for cleanup

### Messages

- `killInstanceMsg{title}` — existing, sent on successful cleanup
- `killFailedMsg{title, previousStatus, err}` — new, sent on failed cleanup

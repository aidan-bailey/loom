# Git Merge Hotkey Design

*Date: 2026-07-01 · Branch: `aidanb/git-merge-hotkey`*

## Problem

Sessions in loom work in isolated git worktrees on independent branches. Bringing one session's work into another today requires manually shelling into a worktree and running `git merge <branch>` by hand — there's no in-TUI way to combine two agents' work. This adds a keybinding, `m`, that lets the user pick another session by its existing list index and merge that session's branch into the currently-focused session's branch, without leaving the session list.

## Goals

- Press `m` while not attached to any pane (default state) to merge another session's branch into the currently-focused session's worktree.
- Select the source session using the index numbers already shown in the session list (`1.`, `2.`, `3.`, ...).
- Follow the existing Lua scripting dispatch pattern (`cs.bind` / deferred `cs.actions.*` / Intent) rather than a Go-only special case, so the binding stays user-rebindable like every other default key.
- Guard against merging into a dirty worktree, and never silently discard a conflicted merge.

## Non-goals

- No support for merging into or from a session that is a workspace terminal (`IsWorkspaceTerminal`) — it runs in the root repo without an isolated branch/worktree to act on.
- No custom merge-commit message, no rebase option, no squash option — plain `git merge <branch>`, matching git's own defaults.
- No automatic conflict resolution or `merge --abort` on failure — a conflicted merge is left in place for the user (or the agent) to resolve, matching existing "don't paper over failures" conventions in this codebase.
- No fetch/remote sync before merging — source and target worktrees share one local repository, so the source branch's commits are already reachable locally.
- No warning about uncommitted changes in the *source* session — `git merge` only pulls in committed history, so this is standard, unsurprising git behavior.

## Design

### Interaction flow

```
User presses `m` in stateDefault (not attached to a pane)
  → script dispatch (defaults.lua "m" binding) → cs.actions.merge_selected()
  → MergeSessionsIntent enqueued, coroutine yields
  → handleScriptIntent (app_scripts.go) → runMergeSelected(m)
      → selectedNotBusyNotWorkspace gate fails?
          → cs.notify() explains why (not eligible), resume coroutine, done
      → target.Worktree().IsDirty() == true?
          → cs.notify("commit or stash first"), resume coroutine, done
      → build eligible source list: instances in the current workspace
        passing the same not-Loading/not-Deleting/not-workspace-terminal
        filter, excluding the target itself
      → list empty?
          → cs.notify("no other sessions to merge"), resume coroutine, done
      → open mergePicker overlay, m.state = stateMergePicker
  (user interacts with the overlay)
  → Esc → close overlay, m.state = stateDefault, resume coroutine ("cancelled")
  → Enter on a highlighted row → target.Worktree().Merge(source.Branch())
      → close overlay, m.state = stateDefault
      → resume coroutine with {ok, source, target, err}
  → Lua: cs.notify("Merged 'source' into 'target'") on success,
         cs.notify("Merge failed: <git output>") on failure/conflict
```

There is no separate yes/no confirmation dialog after the picker — the picker itself shows exactly which branch is about to be merged into the target before Enter is pressed, so a second confirmation would be redundant friction.

### Picker overlay

New `ui/overlay/mergePicker.go`, modeled on the existing `WorkspacePicker`:

- Header names the merge target: `"Merge into '<target title>'"`.
- Rows show each eligible source session labeled with its **original index from the main session list** — not renumbered sequentially. If session `2` (say, a workspace terminal) is filtered out, the picker's rows read `1.`, `3.`, `4.`, ... with a gap at `2`. This preserves the muscle memory of "I saw session 3 in the list, so I type 3" — renumbering would silently break that mapping.
- Navigation: `up`/`down` moves the cursor. Typing digits accumulates into a short-lived buffer (cleared on `enter`/`esc`/an idle timeout, or immediately once the buffered number can no longer match any remaining eligible index) and jumps the cursor to the row whose original index matches, supporting two-digit indices the same way the main list already does (`ui/list.go`'s `idx >= 10` padding case). `enter` commits the highlighted row; `esc` cancels.
- A new `stateMergePicker` app state and `app/state_mergepicker.go` handler route these keys to the overlay, mirroring `stateWorkspace`.

### Eligibility rules

**Target (the currently-focused/selected session)** — gated by the existing `selectedNotBusyNotWorkspace(m *home) bool` predicate (`app/intents.go:41`), the same gate `push_selected`/`kill_selected`/`checkout_selected` already use: selected instance exists, is not a workspace terminal, and is not `Loading`/`Deleting`. This correctly includes `Running`, `Ready`, `Prompting`, `Paused`, and `Recoverable` — all of these carry a resolvable `GetGitWorktree()` (verified: `Ready`/`Prompting` are always post-`Start()` states in every reachable selection context, and `Recoverable` placeholders are reconstructed with real worktree/branch paths in `session/orphan.go`'s `InstanceDataFromOrphan`). No new predicate is introduced — reusing the established gate keeps merge's eligibility consistent with its siblings rather than inventing bespoke rules.

The worktree-dirty check is *not* part of this static gate — it requires a live `git status` call, so it happens inside `runMergeSelected` right before the picker would open, not as a synchronous precondition used for menu/help-text enabling.

**Sources (rows in the picker)** — same filter as the target (not `Loading`/`Deleting`, not a workspace terminal), same workspace as the target, excluding the target itself.

### Git operation

New method on `git.GitWorktree` (`session/git/worktree_git.go`):

```go
func (gw *GitWorktree) Merge(sourceBranch string) error
```

Runs `git merge <sourceBranch>` in the target worktree's directory via the existing `cmd.Executor`, following the same conventions as `PushChanges`/`CommitChanges`. On a non-zero exit it returns the git error/output as-is — it does not run `git merge --abort`. A conflicted merge leaves `MERGE_HEAD` and conflict markers in the worktree exactly as git's default behavior would, ready for the user (via `ctrl+t`/`i` into the terminal pane) or the agent to resolve and commit.

### Wiring

- `script/defaults.lua`: new `m` binding calling `cs.actions.merge_selected()`, with `help = "Merge session into current"`.
- `script/api_actions.go`: register `merge_selected` as a deferred action, enqueuing `MergeSessionsIntent`.
- `script/intent.go`: new `MergeSessionsIntent` type, following `CheckoutIntent`'s shape.
- `app/app_scripts.go`: `handleScriptIntent` dispatches `MergeSessionsIntent` to `runMergeSelected`.
- `keys/keys.go`: mirror the `m` binding for the help-overlay listing (this map is read-only display metadata, not dispatch).
- `USAGE.md` / `CLAUDE.md` keybindings table: add the `m` row.

## Error handling

| Situation | Behavior |
|---|---|
| Target fails `selectedNotBusyNotWorkspace` (no selection, workspace terminal, or mid-transition) | `cs.notify()` explaining why; no overlay opens. |
| Target worktree is dirty | `cs.notify("commit or stash first")`; no overlay opens; no git command runs. |
| No eligible source sessions | `cs.notify("no other sessions to merge")`; no overlay opens. |
| User cancels the picker (`esc`) | No git command runs; back to `stateDefault`. |
| Merge succeeds (fast-forward or merge commit) | `cs.notify("Merged '<source>' into '<target>'")`. |
| Merge conflicts | Left in place (no auto-abort); `cs.notify()` surfaces git's raw stderr so the user knows to resolve it. Treated the same code path as any other non-zero-exit failure — no special-case conflict detection needed since git's own default behavior (leave `MERGE_HEAD` + markers, non-zero exit) already matches the desired UX. |
| Other merge failure (e.g. unrelated histories, missing ref) | Same generic error surface as conflicts — git's message is passed through rather than reinterpreted. |

## Testing

- `session/git`: table-driven tests for `Merge()` covering a clean fast-forward, a merge producing a merge commit, and a conflicting merge (assert `MERGE_HEAD` exists afterward, error surfaces the conflict, and no `merge --abort` is triggered).
- `app/intents_test.go`-style tests for `runMergeSelected`'s short-circuit paths (dirty target, empty source list, ineligible target via `selectedNotBusyNotWorkspace`), using the existing mock `cmd.Executor` pattern.
- `ui/overlay`: unit tests for `mergePicker`'s cursor navigation, digit-jump, and selection commit, mirroring `workspacePicker_test.go`.
- `script` package: a test asserting `m` is bound by default and dispatches `MergeSessionsIntent`, mirroring how `checkout_selected` is tested.
- Since `runMergeSelected` mutates `m.state` and opens an overlay, it must run on the main goroutine inside `handleScriptIntent` (already guaranteed by the existing intent-handling flow) — no new goroutine-safety concerns are introduced, but a `-race` run is worth including given the new state + overlay field (per `CLAUDE.md`'s concurrency gotchas).

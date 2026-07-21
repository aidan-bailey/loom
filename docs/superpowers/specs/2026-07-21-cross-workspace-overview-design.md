# Cross-Workspace Overview (Mission Control Phase 4) — Design

**Date:** 2026-07-21
**Status:** Approved (brainstorm 2026-07-21)
**Supersedes:** the concept-level Phase 4 sketch in
`docs/superpowers/specs/2026-07-20-mission-control-gui-design.md` §Phasing.4,
which explicitly deferred cross-workspace to its own spec.

## Problem

Phases 1–3 shipped a two-mode UI (focus + overview) and an attention-sorted
card grid, but the overview is **scoped to the focused workspace**. Peer
workspaces render only as dimmed count headers, `]`/`[` jumps stop at the
workspace boundary, and `enter` never crosses workspaces. At the user's scale
(5–10 agents across several workspaces) the fleet view still can't show the
fleet: everything outside the active workspace is a number, not a live card.

## What is already true (and shrinks this phase)

The heavy machinery the 2026-07-20 doc feared — "storage, reconcile, orphan
sweep, monitor fan-out, and the coalescer's session registry" — is **already
global across *open* slots**, because it was built per-instance from the start:

- `activateWorkspace` reconciles and PTY-attaches every slot it opens,
  regardless of focus (`app/app.go:2255`, via `session/reconcile.go`
  `EnsureRunning`). Non-focused slots are already fully live.
- The tmux notify coalescer is per-session and process-wide
  (`tmux.SetNotifier`, `session/tmux/notify.go`); `instanceForSession`
  (`app/events.go:42`) already resolves a session to its instance across **all**
  slots. A peer-slot agent going Prompting already fires status detection and
  bell today.

So the two real gaps are narrow:

1. **Load scope.** Startup activates only *previously-opened* workspaces
   (`registry.GetOpenWorkspaces()`, `app/app.go:430`), never the full registry.
2. **Render + nav scope.** `overviewData` (`app/app.go:2714`), `jumpWaiting`
   (`app/app.go:768`), and the overview `enter`/cursor path
   (`app/state_default.go:48`, `app/app.go:2736`) are hard-scoped to the focused
   slot's `m.list`.

This design closes exactly those two gaps by **extending the existing slots
mechanism**, not by adding a new subsystem.

## Goals

- A **live** global overview: every registered workspace as its own group of
  live cards, grouped and attention-sorted.
- Cross-workspace navigation: overview `j/k/enter` span all groups; focus-mode
  `]`/`[` jump fleet-wide, switching the focused workspace as needed.
- Zero cost for single-workspace users until they open the overview.
- No new data path, no storage-schema bump, no change to agent lifecycle.

## Non-goals

- Eager loading of all workspaces at startup (explicitly rejected — see
  Decisions).
- Auto-spawning a workspace terminal in every registered repo.
- Mouse interaction in the overview (still keyboard-only, v1 rule unchanged).
- Cross-workspace *merge/move* of sessions, or any new agent lifecycle.

## Decisions (settled in brainstorm)

- **Lazy on first overview entry, live for the session** — over eager-startup or
  metadata-only peers. Single-workspace focus users pay nothing; opening the
  fleet view once brings every workspace live and it stays live. Rejected eager
  because it would attach every repo's PTYs and spawn an agent in each on every
  launch; rejected metadata-only because stale tails defeat the "see the fleet"
  goal.
- **Background-loaded slots are live but not tabs; focusing promotes** — over
  making every workspace an open tab. Keeps "open tabs" meaning "workspaces I'm
  actively working in," keeps next-session startup lazy, and keeps the tab bar
  uncluttered.
- **Suppress workspace-terminal auto-spawn for background slots** — so opening
  the overview never launches an agent session in a repo you didn't open.
- **Cross-workspace jumps in *both* modes** — focus-mode `]`/`[` retarget
  fleet-wide (switching the focused slot), not just the overview. Chosen for
  reach ("never stuck in one workspace") over predictability.
- **Progressive load, not synchronous** — enter the overview immediately and
  fill groups in as they finish reconciling, rather than freezing input on the
  first `tab` while several repos reconcile. Fits the app's event-driven
  architecture and isolates per-workspace load failures.

## Design

### 1. Slot model — foreground vs. background

`workspaceSlot` (`app/app.go:170`) gains one field:

```go
// background marks a slot loaded solely to feed the live global
// overview: fully reconciled and PTY-attached like any slot, but hidden
// from the tab bar and never persisted to OpenWorkspaces. Cleared
// (promoted) when the slot becomes focused.
background bool
```

**Invariant: the focused slot is never background.** Focusing a background slot
promotes it first.

- `slotNames()` (`app/app.go:2796`) is overloaded today: it feeds *both* the tab
  bar (`app/app.go:2530`) and `SetOpenWorkspaces` (`app/app.go:2775`). Add
  `foregroundSlotNames()` for both of those; keep whole-`m.slots` iteration for
  liveness and overview.
- Add `foregroundSlotsAndSelected() ([]string, int)`: the foreground names plus
  `focusedSlot` remapped to its index *within* that subset (safe because the
  focused slot is always foreground). Every `tabBar.SetWorkspaces(...)` call
  uses this.
- `promoteSlot(idx int)`: clears `background`, calls `foregroundSlotsAndSelected`
  → `tabBar.SetWorkspaces`, and `saveOpenWorkspaces()`. Called whenever a
  background slot becomes focused (overview `enter`, fleet `]` landing there,
  workspace picker selecting it).

### 2. Progressive fleet loading

**Trigger.** The first transition into `viewOverview`. `ToggleOverview`
(`app/app_scripts.go:332`) — and any other path that sets `viewMode =
viewOverview` — calls `m.ensureFleetLoaded()`, which returns a `tea.Cmd` (batched
into the dispatch result).

**`ensureFleetLoaded() tea.Cmd`.** Computes `registry.Workspaces` minus
workspaces already present as a slot or already in the loading set. For each
remaining workspace it adds the name to `m.fleetLoading` (a `map[string]bool`)
and produces a background `tea.Cmd`. There is **no `fleetLoaded` boolean**: the
"already a slot or loading" guard means re-entering the overview automatically
retries any workspace that previously failed or was registered since.

**The background Cmd** runs the heavy body of `activateWorkspace` **off the
Update goroutine**:

- `config.LoadStateFrom` / `LoadConfigFrom`, `session.NewStorage`,
  `storage.LoadAndReconcile` (git reconcile + `EnsureRunning` PTY attach),
  crash-restart loop, and `reconcileOrphans` — all disk/git/tmux work that
  touches only *this workspace's fresh instances*, never shared model state.
- **Skips** the workspace-terminal auto-spawn block (`app/app.go:2304-2343`).
- **Skips** the `m.slots` append **and all UI-component construction** — no
  `ui.List`/`ui.SplitPane` is built off the Update goroutine.
- Returns `workspaceActivatedMsg{ name, wsCtx, storage, appConfig, appState,
  instances []*session.Instance, recovery, err }` — reconciled domain data only.

**Update handler `workspaceActivatedMsg`** (mirrors the existing
`workspaceRegisteredMsg` handler, `app/app.go:1498`): on success it builds the
`ui.List` (adding the reconciled instances, pre-sized from `m.lastWidth/Height`)
and `ui.SplitPane` on the main goroutine, appends the slot to `m.slots` with
`background: true`, deletes the name from `m.fleetLoading`, and refreshes the
overview. On error, set `m.fleetLoadErrors[name] = err`, delete from
`m.fleetLoading`, and log — **one workspace's failure never breaks the fleet.**
Duplicate-guard: if a slot for this ConfigDir already exists when the message
arrives (e.g. the user opened it via the picker mid-load), discard the message.

**Concurrency.** The Cmd body follows the CLAUDE.md tea.Cmd rule exactly: no
`home` mutation off the Update goroutine; the sole mutation (slot append) is in
the message handler. *Flagged risk for the plan:* firing N activations at once
spawns N× git+tmux subprocesses. Acceptable for "several" workspaces; the plan
notes a small worker cap as a measured-if-needed follow-up, not a v1 requirement.

### 3. Global overview rendering + global cursor

**`OverviewData` becomes multi-group.** Replace `{ActiveName, Items, Order,
SelectedIdx, Peers}` (`ui/overview.go:27`) with:

```go
type OverviewGroup struct {
    Name   string
    Items  []*session.Instance
    Order  []int              // SortForOverview(Items)
    State  GroupState         // loaded | loading | error | empty
    Err    string             // populated when State == error
}

type OverviewData struct {
    Groups   []OverviewGroup
    Cursor   OverviewCursor    // render coords: {Group, Item} into Groups / that group's Order
    Spinner  string
}
```

Two coordinate spaces, deliberately distinct: `home.overviewCursor` is
**domain** coords `{slot, inst}` (index into `m.slots`, index into that slot's
list); `ui.OverviewData.Cursor` is **render** coords `{Group, Item}` (index into
`Groups`, position in that group's `Order`). `overviewData()` translates domain →
render while assembling `Groups`; nav handlers move the domain cursor. The UI
never sees slot indices.

- **Group ordering:** focused workspace first, then all others **alphabetical by
  name** — stable regardless of async arrival order.
- The overview's peer-count footer is removed (every workspace is now a real
  group). The **rail** keeps `PeerSection`/`refreshPeerSections` unchanged.
- **Group render states:** `loaded` → header `▾ NAME · N` + card grid (today's
  `renderGrid`, per group); `loading` → header + dim `loading…`; `error` →
  header + dim-error `failed to load — <err>`; `empty` (loaded, zero instances)
  → header `▾ NAME · 0` + dim `no sessions`. `z` collapse still keys on the name
  (already case-insensitive).

**Global cursor.** `home` gains `overviewCursor struct{ slot, inst int }` (slot
index into `m.slots`, inst index into that slot's list). It replaces
`m.list.SelectedIdx()` as the overview highlight source.

- `j/k/h/l` walk the flattened global order: display-ordered groups → each
  group's `SortForOverview` order, **skipping** `Deleting` instances, collapsed
  groups' contents, and non-`loaded` groups (loading/error/empty have no
  selectable cards).
- A card highlights only when it matches the cursor's `slot`+`inst`.
- Focus mode still uses `m.list` selection. Entering overview seeds the cursor
  from the focused slot + its current selection; `enter` commits the cursor back
  to focus.

**Windowing (the one genuinely new layout piece).** Today `renderGrid`
(`ui/overview.go:122`) windows a single group by its selected row. Across stacked
groups the overview becomes one combined vertical scroll that must keep the
**cursor's** card row visible. The plan isolates this as its own task: a combined
window over all groups' rendered rows (headers + card rows + state lines),
scrolled so the cursor card stays on screen — the multi-group analogue of the
existing single-group `rowOffset` logic, preserving the "only visible rows'
cards are built" property.

### 4. Cross-workspace navigation

**The unifying primitive `focusCursorSlot()`.** Because the focused slot can
never be background, *any* action that operates on the cursor's card must first
make that card's slot the focused one. One helper does this: `saveCurrentSlot()`
→ `promoteSlot(cursor.slot)` if background → `loadSlot(cursor.slot)` → set
`m.list` selection to `cursor.inst`. It is a no-op fast path when the cursor is
already in the focused slot. Every cursor-committing overview action routes
through it, which lets each reuse the *existing* focus-mode intent code unchanged
(they all read `m.list`). Consequence, made explicit: `D`/`r`/`n` on a
background-workspace card **promote** that workspace (it is now one you're acting
in) — the promotion-trigger set is therefore `enter`, fleet-jump landing, picker
selection, **and** a committed `D`/`r`/`n`. Bare cursor movement (`j/k/h/l`) does
**not** focus or promote — browsing stays cheap.

- **Overview `enter`** (`app/state_default.go:48`): `focusCursorSlot()`, then
  `viewMode = viewFocus`.
- **Overview `j/k/h/l`**: move `overviewCursor` across the global order (§3);
  non-selectable groups are skipped. No focus switch.
- **Focus-mode `]`/`[` — `jumpWaiting` rewrite** (`app/app.go:768`): build a
  fleet-wide attention list — every slot's instances that are `Prompting` or
  bell-pending, in display order (focused slot first, then alphabetical, matching
  the overview). Step next/prev with fleet-wide wrap. When the target is in
  another slot: `saveCurrentSlot` → promote-if-background → `loadSlot` → select
  there. A lone remote waiter is reachable with a single `]`.
- **Overview `D` (kill) / `r`/`R` (recover/resume)**: `focusCursorSlot()` (which
  selects the cursor's instance in the now-focused slot), then dispatch the
  existing kill/recover intent — it now naturally mutates the correct slot's
  list + storage. After the mutation, re-clamp `overviewCursor` to a valid
  position.
- **Overview `n`/`N` (create)**: `focusCursorSlot()`, `viewMode = viewFocus`,
  then run the existing create flow (create in the cursor's workspace).

### 5. Persistence, teardown, tab bar, edge cases

- Tab bar and `saveOpenWorkspaces` use foreground slots only; background slots
  stay invisible there and never persist as open → **next session stays lazy.**
- **Quit** (`handleQuit`): `SaveInstances` for *all* slots (foreground +
  background) so no session data is lost; `OpenWorkspaces` already holds only
  foreground names.
- **Workspace picker / `applyWorkspaceToggle`** (`app/app.go:2462`): if the
  chosen workspace is already a background slot, **promote + focus** it rather
  than double-activating (which would try to open a second slot for the same
  ConfigDir).
- **Closing a tab while the fleet is loaded:** **demote to background**
  (`background = true`, drop from tab bar + `OpenWorkspaces`) so its live agents
  stay in the overview, instead of full teardown. If the fleet was never loaded,
  today's `deactivateWorkspace` full teardown still applies. Two invariant
  guards: (a) if the closed tab is the *focused* slot, focus first shifts to an
  adjacent foreground slot (as `deactivateWorkspace` already moves `focusedSlot`)
  *before* the demote, so the focused slot is never background; (b) there must
  always be ≥1 foreground slot — closing the last foreground tab promotes the
  next slot in display order (a background one if that's all that remains) to
  foreground and focuses it before demoting the closed one. If no other slot
  exists at all, it is a full teardown (today's behavior), returning to the
  classic single-`m.list` path.
- **Newly registered workspace** (`workspaceRegisteredMsg`): unchanged — a user
  action, so it activates as a focused foreground tab and simply also appears as
  its own overview group.
- **Retry:** because `ensureFleetLoaded` re-scans every overview entry, an
  errored or newly-registered workspace loads on the next `tab` into the
  overview.

### 6. Concurrency & data flow

No new data path. Cards read emulator content through the existing read-locked
accessors, exactly as phase 3. The only new goroutine work is the fleet-load
Cmds (§2), which obey the tea.Cmd rule (no model mutation off the Update
goroutine). Cross-slot instance resolution (`instanceForSession`) and notify
fan-out are unchanged — already global.

## Testing

- **Slot model:** background flag excluded from tab bar + `OpenWorkspaces`;
  `promoteSlot` flips + persists; focused-slot-never-background invariant holds
  across promote/demote/close.
- **Progressive load:** `ensureFleetLoaded` emits Cmds only for missing
  workspaces (not already-open or already-loading); `workspaceActivatedMsg`
  appends a background slot and clears the loading entry; an error message
  records `fleetLoadErrors` without crashing; re-entry retries errored/missing.
- **Global cursor:** `j/k` crosses groups skipping Deleting/collapsed/non-loaded;
  `enter` switches focused slot + promotes + selects the right instance; `D`/`r`
  mutate the **cursor slot's** storage, and the cursor re-clamps after.
- **jumpWaiting fleet-wide:** crosses slots, switches focus, wraps; the
  single-remote-waiter case lands correctly.
- **Overview render:** multi-group ordering stable under out-of-order async
  arrival; loading/error/empty group states; combined vertical windowing keeps
  the cursor card visible across groups (golden-string height/visibility
  assertions, per phase-3 pattern).
- **Regression:** existing single-workspace overview tests (fleet not yet
  loaded) stay green; rail `PeerSection` path unchanged; scroll / status-redetect
  / pane-border suites unaffected.
- **`-race`** (`CC=clang CGO_ENABLED=1`): fleet-load Cmds attaching PTYs
  concurrently while a pump writes; overview reading every slot's emulator
  through read-locked accessors only.
- **Manual smoke:** 3+ registered workspaces, several agents; open overview →
  all groups fill in progressively; a prompt-waiting agent in a non-focused
  workspace shows gold; `enter` on it switches workspace into focus; `]` in focus
  mode reaches it; closing its tab demotes (agent stays live in overview);
  restart is still lazy (only the workspace you focused eager-loads).

## Deferred

- Worker cap / throttle on concurrent fleet activation (add by measurement).
- Cross-workspace session merge/move.
- Per-group scroll independence (v1 uses one combined overview window).

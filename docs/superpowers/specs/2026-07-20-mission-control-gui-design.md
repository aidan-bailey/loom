# Mission Control GUI — Design

**Date:** 2026-07-20
**Status:** Approved (brainstorm 2026-07-20; mockups in `.superpowers/brainstorm/`, not committed)
**Scope:** Phases 1–3 of this document feed one implementation plan. Phase 4
(cross-workspace) is designed here at concept level only and requires its own
spec before implementation.

## Problem

Loom's single layout — 20% session list, 80% split pane — was designed around
babysitting a few agents. At the user's actual scale (5–10 concurrent agents,
several workspaces) it fails on three fronts:

1. **Situational awareness.** There is no way to see the fleet at once. Which
   agents are blocked on a prompt, which are running, which went idle — all of
   it hides behind one-at-a-time list navigation, and everything outside the
   active workspace is invisible entirely.
2. **Layout rigidity.** The 20/80 and 70/30 splits are hardcoded. The list
   wastes width when you're deep in one agent; the agent pane is cramped when
   the terminal pane is dead weight.
3. **Visual identity.** Styling is scattered hardcoded lipgloss colors with no
   theme system and no deliberate design language.

## Goals

- A two-mode application: **Overview** (fleet triage) and **Focus** (work with
  one agent), toggled with `tab`, last mode remembered.
- Triage primitives: attention-priority sorting, `]` jump-to-next-waiting,
  "needs you" as the only loud signal on screen.
- Workspaces first-class in both modes (section grouping), building toward a
  cross-workspace overview in phase 4.
- Layout control in focus mode: toggleable rail, adjustable and hideable
  terminal split, remembered per session.
- A theme system with semantic color roles; default theme **Afterglow** in a
  "quiet precision" design language.
- All new keys routed through the Lua keymap so user scripts can rebind.

## Non-goals

- Changing agent lifecycle, storage schema, or the tmux/emulator data path
  (the overview reads what the emulator already produces).
- Cross-workspace *implementation* (phase 4 spec-to-come; see Phasing).
- Mouse-driven pane resizing (keyboard only for now; drag can follow).
- Light themes (theme system permits them; none ship in this effort).
- Web/GUI frontends. This is and remains a TUI.

## Design

### 1. View modes

`home` gains a `viewMode` enum (`modeFocus`, `modeOverview`) orthogonal to the
existing `state` machine. `tab` toggles in the default state; overlays and
attach states are unaffected. Key dispatch adds a `state_overview.go` handler
alongside the existing per-state handlers; focus mode keeps today's dispatch
path. Last mode persists in `state.json` and restores on startup.

### 2. Overview mode (fleet triage)

A responsive grid (1–3 columns by terminal width) of **status cards** grouped
under collapsible workspace section headers.

Each card shows:

- Title, with a status accent on the card border/title (gold = needs input,
  accent-blue = focused/selected, default rule color otherwise).
- Status label with wait-duration for prompts (`input · 4m`, `trust · 40s`,
  `running`, `idle 22m`, `paused · 3d`).
- Branch and diff stats (`+n −n`), agent program, session age.
- A 2–3 line **live output tail** rendered from the instance's emulator
  (bottom lines of `RenderWindow`); snapshot-path instances degrade to their
  latest capture content, `Paused`/`Recoverable` instances show a static line
  (no PTY is ever spawned for a card — same rule as the list today).

Sorting within a group: needs-input → running → idle → paused/recoverable,
stable by title within a tier. Groups: active workspace first, then others
alphabetically. `z` collapses/expands a group; `enter` focuses the selected
card (switching mode, and in phase 4 possibly workspace); `]`/`[` cycle
through agents currently waiting on input; `j/k/h/l` move selection in the
grid. Until phase 4, non-active workspaces render as collapsed dimmed headers
with instance counts from persisted state (not live).

### 3. Focus mode (work with one agent)

Today's layout, reimagined:

- **Rail.** The session list becomes a rail of **mini-cards**: workspace
  section labels, then per-instance cards with a one-line live tail and a
  left-edge status accent bar. `\` toggles the rail away for full-bleed work.
  Width stays ~25% when visible.
- **Split pane.** Agent/terminal stack remains persistent, but the ratio is
  adjustable in steps via `ctrl+up`/`ctrl+down` (persisted in `state.json`
  keyed by instance title — see Decisions) and the terminal pane can be
  hidden/restored with a dedicated key, giving the agent pane full height.
- `]`/`[` jump-to-waiting works here too, retargeting the focused instance.
- Existing overlays (diff, quick input, pickers, settings) are restyled to the
  theme but functionally unchanged.

### 4. Shared card component

`ui/card.go` renders an instance at three densities from one implementation:

- `DensityCard` — overview card (chrome + metadata + tail).
- `DensityRail` — rail mini-card (title + one-line tail + accent bar).
- `DensityLine` — one-line degenerate (height-starved fallback; also the
  eventual replacement for today's list row rendering).

Inputs are a narrow view-model struct (title, status, wait-age, branch, diff
stats, tail lines, accents) built by the caller — the component does no
instance/emulator access itself, keeping it trivially table-testable.

### 5. Theme system & visual language

`ui/theme.go` becomes a real theme registry. A `Theme` holds semantic roles:

```
Ground, Panel, Text, Dim, Faint, Rule,
Accent, Attention, OK, Error, Workspace
```

plus derived lipgloss styles built once at load. All hardcoded colors in
`ui/`, `ui/overlay/`, and app-level chrome migrate to roles. Config gains a
`Theme` string field (default `"afterglow"`); the settings overlay lists
registered themes. Shipped themes: `afterglow` (default) and `legacy` (the
current look, preserved for continuity).

Afterglow role mapping (from `Afterglow.tmTheme`):

| Role | Hex | | Role | Hex |
|---|---|---|---|---|
| Ground | `#2E2E2E` | | Accent | `#6c99bb` |
| Panel | `#333435` | | Attention | `#e5b567` |
| Text | `#d6d6d6` | | OK / added | `#b4c973` |
| Dim | `#797979` | | Error / removed | `#c45330` |
| Faint | `#505050` | | Workspace | `#a1617a` |
| Rule | `#404040` | | | |

Design language ("quiet precision"): thin rules instead of heavy boxes,
status via small glyphs and left-edge accent bars, dim-by-default text,
`Attention` gold reserved exclusively for agents waiting on the user — when
the screen is calm, nothing is loud.

### 6. Keybindings & scripting

New Lua actions registered in `script/defaults.lua` (rebindable, last-write-
wins as usual): `toggle_overview` (`tab`), `next_waiting` (`]`),
`prev_waiting` (`[`), `toggle_rail` (`\`), `toggle_terminal_pane`,
`resize_split_up`/`resize_split_down` (`ctrl+up`/`ctrl+down`). Overview-mode
navigation (`j/k/h/l`, `enter`, `z`, `D`, `r`) dispatches through the same
engine. `tab` currently has no default-state binding conflict; if a user
script binds it, their binding wins per existing collision rules.

### 7. Concurrency & data flow

No new data paths. Cards read emulator content via the existing read-locked
accessors (`RenderWindow`/`ScrollbackLen`); the per-instance dirty/quiet
coalescer already drives re-renders, and overview mode simply re-renders the
visible cards on `paneDirtyMsg` for any visible instance (coalesced ≤ ~60/s
per session; if full-grid renders prove hot at 10 sessions, throttle
non-selected card tails to quiet-events only — decide by measurement).
All model mutation stays on the Update goroutine; the xvt callback and
tea.Cmd rules in CLAUDE.md are unchanged by this design.

## Phasing

1. **Theme system + restyle.** `Theme` registry, role migration, `afterglow`
   + `legacy` themes, config/settings plumbing. Ships alone; no layout change.
2. **Focus rail + split controls.** `ui/card.go` (rail + line densities),
   rail toggle, resizable/hideable terminal split, `]`/`[` jumps.
3. **Overview mode.** `modeOverview`, card density, grid layout + grouping +
   sorting, `state_overview.go` dispatch, mode persistence. Scoped to the
   active workspace; other workspaces shown as dimmed collapsed headers.
4. **Cross-workspace (follow-up spec required).** Keep all registered
   workspaces' instances loaded and reconciled concurrently; live global
   overview; `enter`/`]` across workspaces. Touches storage, reconcile,
   orphan sweep, monitor fan-out, and the coalescer's session registry —
   deliberately out of scope for the phase 1–3 plan.

## Testing

- Table-driven unit tests: card rendering at all three densities (golden
  strings per theme), attention sort ordering, grid column math, mode-switch
  key dispatch, theme resolution/fallback (unknown theme name → default).
- Regression suites that must stay green through the restyle: scroll
  (`ui/scroll_test.go`), status redetect (`app/status_redetect_test.go`),
  pane border sizing (`ui/pane_border_test.go`), selection hit-tests.
- `-race` (CGO_ENABLED=1, CC=clang locally) over overview rendering while a
  pump writes — pins that card tails only use read-locked accessors.
- Manual smoke: 6+ sessions across 2 workspaces, prompt-waiting agent surfaces
  gold in both modes, `]` cycles correctly, rail/terminal toggles and split
  resize persist across restart.

## Decisions

- **Hybrid cards over pure live-grid or pure-metadata dashboard** — chosen in
  brainstorm; tails give "what is it saying", chrome gives "what does it need".
- **Global grouped overview over per-workspace tabs** — the tab bar's
  one-workspace-at-a-time model is the thing being replaced; phase 4 makes it
  real, phases 1–3 fake the inactive groups from persisted state.
- **Persistent resizable terminal split with hide** (not toggle-on-demand or
  swap) — user preference.
- **Afterglow as default personality** with a `legacy` escape hatch rather
  than restyling in place — keeps the restyle reviewable and reversible.
- **Split ratio persisted in `state.json`, not `instances.json`** — avoids an
  instance schema bump (and the migration ceremony) for a UI preference.

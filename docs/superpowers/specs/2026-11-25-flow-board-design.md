# Web: the flow board

Status: design approved 2026-11-25.

Builds on the [flow visualisation](2026-11-09-flow-visualisation-design.md) screens and the [web desktop](2026-11-21-web-desktop-design.md) shell. The owner's ask: "a flow based dashboard in the web application: see all currently executing flows and in what stage they're in".

## 1. Problem

The Flows screen (`#/flows`) lists each session's flow as one row with a chip (`flow 3/7 · review · $4.20`), above the past runs. To learn which stage each flow is in, the owner reads every chip. The list reads once per visit and again only on ↻, so it does not follow a run.

Success: one screen shows every flow in the shown workspaces placed by its stage, follows the stream without a reload, and leads to a flow's own screen in one click.

## 2. Decisions

The owner's choices in the brainstorm, with what was rejected:

- **A stage board:** one column per stage, each flow a card in its stage's column. Rejected: a card per flow with its own stage strip; a dense table.
- **A new screen,** beside the Flows screen, which stays as it is. Rejected: replacing the Flows screen's live box; a desktop-only board.
- **Flows in progress in their stage's column, finished ones in a sixth, dimmed column.** Rejected: hiding finished flows; leaving every flow in its stage's column whatever its status.
- **Drawn from the stream** (`session.flows` in the fleet store). Rejected: `GET /api/v1/flows`, which is the same summaries read once and would need polling; a new route with per-story detail, which nothing on a card needs.

Made while writing this spec, open to planning: the names (`flowBoard`, `#/flow-board`, "Flow board"); the sort within a column (§4.2); empty columns hidden on a phone and kept on a desktop (§4.4).

## 3. Scope

In: the new app (`internal/web/ui`) and its committed bundle (`internal/web/dist`).

Out: any Go change, route, stream event or `api.fields` line; the classic app (`internal/web/static`); the TUI; drag and drop; filters; per-story detail on a card; the past runs, which stay on the Flows screen.

## 4. The board

### 4.1 Which flows

Every flow of every session of a shown workspace (`shownWorkspaces`), the same set `hasShownFlows` tests. A session with two flows has two cards.

### 4.2 Columns

Six, in this order: `design`, `spec`, `draft`, `run`, `record`, `finished`.

- A flow whose `status` is `done`, `stopped` or `cancelled` is in `finished`.
- Every other flow is in the column named by its `stage`. That includes `parked`, `lost`, an asking flow (`asks !== ""`) and an unreadable one (`unreadable !== ""`): each still needs the owner.
- A `stage` the app does not know (a newer daemon's) gets a column of its own name, placed before `finished`, so no flow is dropped.

Within a column: asking flows first, then by `updated`, newest first; ties keep the snapshot's order.

Each column's head shows its name and its count. `finished` is drawn dimmed.

### 4.3 A card

- The session's title, and the flow's `part` when it has one.
- The state line, from the chip's logic without its `flow` prefix and without the stage the column already says: `draft 2/5` while drafting, `drafted` or `designed`, `3/7 · review` in a run (done/total, then asks, lost, the running phase or the status), nothing for a design or spec flow with no more to say. `flowChip` is split so the chip and the card share this text.
- The question kind when it asks (`blocked-decision`), `unreadable` when it is.
- The cost (`usd(spent)`) when priced, and `ago(updated)`.
- The tone is `flowTone`'s: attention when it asks, error when lost or unreadable, else plain.

The card is a link to the session's flow screen: `hrefFor("flow", { id }, { plan })`, as the Flows screen's live row.

### 4.4 Layouts

On a desktop layout (`desk2`, `desk3`) the columns sit side by side, equal width, each scrolling with the page; every column is drawn, empty ones included, so the board keeps its shape.

On the phone layout the columns stack as sections in the same order, and an empty one is left out.

### 4.5 States

- No snapshot yet: "Loading…" (`role="status"`).
- A snapshot and no flow: "No session's worktree holds a flow.", the Flows screen's sentence.
- A stale link: the board is dimmed (`opacity-50`), as the fleet header dims what it cannot trust; the cards stay links.

Ages move with the clock: the board redraws each minute as the fleet list does.

## 5. Getting there

- The palette gains the place "Flow board", under the same rule as "Flows" (`hasShownFlows`), after it.
- The Flows screen's head gains a link to the board beside its ↻; the board's head links back to the Flows screen and to the fleet (its back).
- The address is `#/flow-board`.

## 6. Code

| File | Holds |
|---|---|
| `src/fleet/board.ts` | pure: `boardOf(snapshot)` gives the columns, each a name and its cards (session id, title, flow), by §4.1 and §4.2 |
| `src/fleet/board.test.ts` | each status and stage; both sides of the finished rule; asking and lost flows stay in their stage; an unknown stage; the sort; hidden workspaces left out |
| `src/panels/flowBoard/index.tsx` | the panel and its head, in the entry; imports nothing the body chunk owns |
| `src/panels/flowBoard/FlowBoardBody.tsx` | the board, a lazy chunk |
| `src/panels/flowBoard/board.test.tsx` | the columns and cards drawn, a card's link, the phone's hidden empty columns, loading, empty and stale |
| `src/app/panels.ts`, `src/app/routes.tsx`, `src/panels/index.ts` | the `flowBoard` id, its route and its registration |
| `src/desktop/palette.ts` | the place |
| `src/panels/flows/index.tsx` | the head's link |
| `src/panels/fleet/SessionRow.tsx` | `flowChip` split into the state text and its prefix |
| `internal/web/dist/` | the rebuilt bundle |

## 7. Tests and gates

- Vitest as §6 lists, and the existing registry, route and palette tests extended for the new id.
- `pnpm run test`, the app's typecheck and lint, then the bundle rebuilt.
- Go: `CGO_ENABLED=0 go test ./internal/web/...`, for `TestStatic_NextTreeBuilds` and `TestDist_FirstLoadIsWithinBudget`: the body is a lazy chunk so the first load grows only by the panel's head and `board.ts`.
- A look at the real screen through the `loom-dev` sandbox, desktop and phone widths.

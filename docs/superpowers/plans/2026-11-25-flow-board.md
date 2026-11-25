# Flow Board Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A "Flow board" screen in the new web app that places every flow of the shown workspaces in a column for its stage, live from the stream.

**Architecture:** Frontend only. A pure module (`src/fleet/board.ts`) turns the fleet store's snapshot into columns; a lazy panel (`src/panels/flowBoard/`) draws them. No route, stream event or `api.fields` line is added. Spec: [`docs/superpowers/specs/2026-11-25-flow-board-design.md`](../specs/2026-11-25-flow-board-design.md).

**Tech Stack:** React 19, TypeScript, Zustand (`useFleet`), Tailwind, Vitest and Testing Library; pnpm in `internal/web/ui`.

**Execution:** inline, three work packages, one commit each. Heavy commands go through `loom job run`.

All paths below are from the repo root; the app is `internal/web/ui` (`ui/` below).

---

## Package 1: the board's logic

**Files:**
- Create: `ui/src/fleet/board.ts`, `ui/src/fleet/board.test.ts`
- Modify: `ui/src/panels/fleet/SessionRow.tsx` (`flowChip` takes its state word from `flowState`)

- [ ] **Step 1: write `ui/src/fleet/board.test.ts`** (fails: no module)

Cases, each a named `it`:

1. `the columns are design, spec, draft, run, record and finished, in that order, empty ones included` — `boardOf` of a snapshot with no flows gives six columns with those names and no cards.
2. `a flow is in its stage's column` — one flow per stage, status `running`.
3. `a done, stopped or cancelled flow is in finished, whatever its stage` — and `running`, `parked`, `lost`, `designed`, `drafted`, `supervised`, `""` stay in the stage (both sides of the rule, each status its own assertion).
4. `an asking, lost or unreadable flow stays in its stage's column`.
5. `a stage this build does not know gets a column of its own name before finished`.
6. `within a column asking flows come first, then the newest update; ties keep the snapshot's order`.
7. `a session with two flows has two cards, each with the session's id and title`.
8. `a hidden workspace's flows are left out` — workspace 2 with `shown: false`.
9. `the fixture's board: flow-run asks in run, its designed flow in spec`.
10. `flowState: asks, then lost, then a running flow's phase, else its status`.
11. `cardLine` — `designing` for a design flow, `designed` for a spec one, `draft 2/5` while drafting, `drafted`, `3/7 · review` in a run, `2/5 · asks` when it asks, `0/4` with an empty status.

Fixtures: `fixtureSnapshot(NOW)` from `@/fixtures`; a `flow(over)` helper spreading over session 6's first flow; a `withFlows(flows)` helper that sets session 6's flows and clears every other session's.

- [ ] **Step 2: run it red** — `cd internal/web/ui && pnpm exec vitest run src/fleet/board.test.ts` → fails to resolve `./board`.

- [ ] **Step 3: write `ui/src/fleet/board.ts`**

```ts
// The flow board's columns (flow board spec §4): every flow of the shown workspaces' sessions,
// placed by its stage, the ended ones in a column of their own.
import type { FlowSummary, Snapshot } from "@/api/gen";

import { allSessions, shownWorkspaces } from "./groups";

/** The stage columns, in the order the board draws them. */
export const STAGES: readonly string[] = ["design", "spec", "draft", "run", "record"];

/** The last column: the flows whose status says they ended. */
export const FINISHED = "finished";

const ENDED: ReadonlySet<string> = new Set(["done", "stopped", "cancelled"]);

export interface BoardCard {
  session: number;
  title: string;
  flow: FlowSummary;
}

export interface BoardColumn {
  name: string;
  cards: BoardCard[];
}

/** The column a flow is in: finished once it ended, else its stage's. */
export function columnOf(flow: FlowSummary): string {
  return ENDED.has(flow.status) ? FINISHED : flow.stage;
}

/**
 * A run's state word, as the fleet chip and a card say it: "asks" while it asks, "lost" when its
 * engine has ended, a running flow's phase, else its status.
 */
export function flowState(flow: FlowSummary): string {
  if (flow.asks !== "") return "asks";
  if (flow.status === "lost") return "lost";
  if (flow.status === "running" && flow.phase !== "") return flow.phase;
  return flow.status;
}

/** A card's state line: what the flow is doing, without the stage its column already names. */
export function cardLine(flow: FlowSummary): string {
  if (flow.stage === "design" || flow.stage === "spec") return flow.status;
  if (flow.status === "drafting") return `draft ${flow.drafted}/${flow.total}`;
  if (flow.status === "drafted" || flow.status === "designed") return flow.status;
  return [`${flow.done}/${flow.total}`, flowState(flow)].filter((s) => s !== "").join(" · ");
}

const updated = (c: BoardCard): number => Date.parse(c.flow.updated) || 0;

/**
 * The board: the five stage columns, a column for each stage this build does not know, then
 * finished. A column's asking flows come first, then the newest update; the sort is stable, so
 * ties keep the snapshot's order.
 */
export function boardOf(snapshot: Snapshot): BoardColumn[] {
  const shown = new Set(shownWorkspaces(snapshot).map((w) => w.id));
  const cards = new Map<string, BoardCard[]>(STAGES.map((s) => [s, []]));
  const finished: BoardCard[] = [];
  for (const session of allSessions(snapshot)) {
    if (!shown.has(session.workspace)) continue;
    for (const flow of session.flows ?? []) {
      const name = columnOf(flow);
      const card = { session: session.id, title: session.title, flow };
      if (name === FINISHED) finished.push(card);
      else cards.set(name, [...(cards.get(name) ?? []), card]);
    }
  }
  const columns = [...cards].map(([name, list]) => ({ name, cards: list }));
  columns.push({ name: FINISHED, cards: finished });
  for (const column of columns) {
    column.cards.sort(
      (a, b) =>
        Number(b.flow.asks !== "") - Number(a.flow.asks !== "") || updated(b) - updated(a),
    );
  }
  return columns;
}
```

- [ ] **Step 4: `flowChip` uses `flowState`** in `ui/src/panels/fleet/SessionRow.tsx`: import `flowState` from `@/fleet/board` and replace the nested conditional (`const state = flow.asks !== "" ? …`) with `const state = flowState(flow);`. Its output is unchanged; `flows.test.tsx` and `Sidebar.test.tsx` pin it.

- [ ] **Step 5: green** — `pnpm exec vitest run src/fleet src/panels/fleet src/panels/flows src/desktop/Sidebar.test.tsx`.

- [ ] **Step 6: commit** — `feat(web): the flow board's columns` with the three paths.

## Package 2: the screen and its wiring

**Files:**
- Create: `ui/src/panels/flowBoard/index.tsx`, `ui/src/panels/flowBoard/FlowBoardBody.tsx`, `ui/src/panels/flowBoard/board.test.tsx`
- Modify: `ui/src/app/panels.ts` (PanelId `flowBoard`), `ui/src/app/routes.tsx` (`routeOf`, `hrefFor`, `PATHS`: `/flow-board`), `ui/src/panels/index.ts`, `ui/src/desktop/palette.ts`, `ui/src/panels/flows/index.tsx`
- Tests: `ui/src/app/panels.test.ts`, `ui/src/app/routes.test.ts`, `ui/src/desktop/palette.test.ts`, `ui/src/panels/flows/flows.test.tsx`

- [ ] **Step 1: write `ui/src/panels/flowBoard/board.test.tsx`** (fails: no module). The store is set with `useFleet.setState({ snapshot, stale, link })`; the layout by mocking `@/app/viewport`'s `useLayout`. Cases:

1. `draws Loading… until a snapshot arrives` (`role="status"`).
2. `with no flow says so` — "No session's worktree holds a flow.", no column.
3. `on desktop draws all six columns with their counts, empty ones included`.
4. `on the phone leaves the empty columns out`.
5. `a card: the session's title, its part, its state line, its cost and age, and a link to its flow` — href `hrefFor("flow", { id: "6" }, { plan })`, text `2/5 · asks`, `blocked-decision`, `$4.71`, `4m`.
6. `a card takes the chip's tone` — `data-tone` attention, error, docs.
7. `an unreadable flow says so`.
8. `finished is dimmed` — the column has `opacity-60`.
9. `a stale link dims the board and keeps the cards links`.
10. `the head: a way back to the fleet, the title and a link to the Flows screen`.
11. `the panel is registered under flowBoard with its head, a lazy body and no tab`, and `draws its body through PanelHost once its chunk loads`.

- [ ] **Step 2: red** — `pnpm exec vitest run src/panels/flowBoard`.

- [ ] **Step 3: the id and route.** `panels.ts`: add `| "flowBoard"` after `"flows"`. `routes.tsx`: in `routeOf`'s switch add `case "flow-board": return plain("flowBoard");`; add `flowBoard: "/flow-board"` to `hrefFor`'s `paths` and to `PATHS`. `panels/index.ts`: `import "./flowBoard";` after `./flows`.

- [ ] **Step 4: `ui/src/panels/flowBoard/index.tsx`**

```tsx
// The flow board's panel: the head (a way back to the fleet, the title and a link to the Flows
// screen) is in the entry; the body is a lazy chunk, so a first launch never loads it.
import { lazy, type ReactElement } from "react";

import { definePanel } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { ContextRow } from "@/app/shell";

export function FlowBoardHead(): ReactElement {
  return (
    <ContextRow
      back={hrefFor("fleet")}
      title="Flow board"
      actions={
        <a
          href={hrefFor("flows")}
          aria-label="Flows"
          className="flex min-h-[44px] min-w-[44px] flex-none items-center justify-center text-sm text-muted-foreground"
        >
          flows
        </a>
      }
    />
  );
}

const FlowBoardBody = lazy(() => import("./FlowBoardBody"));

export const flowBoardPanel = definePanel({
  id: "flowBoard",
  title: () => "Flow board",
  Head: FlowBoardHead,
  Body: FlowBoardBody,
});
```

- [ ] **Step 5: `ui/src/panels/flowBoard/FlowBoardBody.tsx`**

```tsx
// The flow board (a chunk of its own): every flow of the shown workspaces in its stage's column,
// drawn from the fleet store, so it follows the stream.
import { useEffect, useMemo, useState, type ReactElement } from "react";

import { hrefFor } from "@/app/routes";
import { useLayout } from "@/app/viewport";
import { boardOf, cardLine, FINISHED, type BoardCard, type BoardColumn } from "@/fleet/board";
import { cn } from "@/lib/utils";
import { useFleet, useLink } from "@/store/fleet";

import { ago, flowTone, usd } from "../fleet/SessionRow";

const LEGEND = "text-[11px] tracking-wide text-muted-foreground uppercase";
const BORDER = { attention: "border-attention", error: "border-error", docs: "border-border" };

function Card({ card }: { card: BoardCard }): ReactElement {
  const flow = card.flow;
  const tone = flowTone(flow);
  const line = cardLine(flow);
  const facts = [typeof flow.spent === "number" ? usd(flow.spent) : "", ago(flow.updated)];
  return (
    <a
      href={hrefFor("flow", { id: String(card.session) }, { plan: flow.plan })}
      data-tone={tone}
      className={cn(
        "flex min-h-[44px] flex-col gap-0.5 rounded-lg border bg-background p-2",
        BORDER[tone],
      )}
    >
      <span className="block truncate font-semibold">{card.title}</span>
      {flow.part !== "" && (
        <span className="block truncate text-xs text-muted-foreground">{flow.part}</span>
      )}
      {line !== "" && <span className="block truncate text-sm">{line}</span>}
      {flow.asks !== "" && (
        <span className="block truncate text-xs text-attention">{flow.asks}</span>
      )}
      {flow.unreadable !== "" && <span className="block text-xs text-error">unreadable</span>}
      <span className="block truncate text-xs text-muted-foreground">
        {facts.filter((f) => f !== "").join(" · ")}
      </span>
    </a>
  );
}

function Column({ column }: { column: BoardColumn }): ReactElement {
  return (
    <div
      role="group"
      aria-label={column.name}
      className={cn(
        "flex min-w-0 flex-col gap-2 rounded-xl border border-border bg-card p-2",
        column.name === FINISHED && "opacity-60",
      )}
    >
      <p className={LEGEND}>
        {column.name} · {column.cards.length}
      </p>
      {column.cards.map((card) => (
        <Card key={`${card.session}:${card.flow.plan}`} card={card} />
      ))}
    </div>
  );
}

export default function FlowBoardBody(): ReactElement {
  const snapshot = useFleet((s) => s.snapshot);
  const { stale } = useLink();
  const phone = useLayout() === "phone";
  const [, setTick] = useState(0);

  // Ages move with the clock, not only with the stream.
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30_000);
    return () => clearInterval(t);
  }, []);

  const columns = useMemo(() => (snapshot === null ? null : boardOf(snapshot)), [snapshot]);
  if (columns === null) {
    return (
      <p role="status" className="px-4 py-6 text-center text-muted-foreground">
        Loading…
      </p>
    );
  }
  if (columns.every((c) => c.cards.length === 0)) {
    return (
      <p className="px-4 py-6 text-center text-sm text-faint">
        No session&apos;s worktree holds a flow.
      </p>
    );
  }
  const drawn = phone ? columns.filter((c) => c.cards.length > 0) : columns;
  return (
    <section
      aria-label="Flow board"
      className={cn(
        "px-4 pb-6",
        phone
          ? "flex flex-col gap-4"
          : "grid auto-cols-[minmax(9rem,1fr)] grid-flow-col items-start gap-3 overflow-x-auto",
        stale && "opacity-50",
      )}
    >
      {drawn.map((column) => (
        <Column key={column.name} column={column} />
      ))}
    </section>
  );
}
```

- [ ] **Step 6: the palette and the Flows head.** `desktop/palette.ts`: inside the `hasShownFlows` branch push both `plain("flows", "Flows")` and `plain("flowBoard", "Flow board")`. `panels/flows/index.tsx`: the head's `actions` become a fragment of a link (`href={hrefFor("flowBoard")}`, `aria-label="Flow board"`, text `board`, the same classes as the board head's link) and the existing ↻ button.

- [ ] **Step 7: extend the existing tests.** `panels.test.ts`: `flowBoard: true` in `ids`. `routes.test.ts`: `["#flow-board", "#/flow-board"]` in the normalise table, `["#flow-board", route("flowBoard")]` and `["#/flow-board", route("flowBoard")]` in the `routeOf` table, `["flowBoard", {}, {}, "#/flow-board"]` in the `hrefFor` table and `["flowBoard", {}, {}]` in the round-trip list. `palette.test.ts`: `"place:flowBoard"` after each `"place:flows"` in the key lists, its title, hint and href beside Flows', and its absence in the no-flows case. `flows.test.tsx`: the head has the link `Flow board` to `#/flow-board`.

- [ ] **Step 8: green** — `loom job run -d "web ui check" -- sh -c 'cd internal/web/ui && pnpm run check'` (typecheck, lint, prettier, every Vitest, `sw.js`).

- [ ] **Step 9: commit** — `feat(web): the flow board screen` with the paths above.

## Package 3: the bundle, the lists that name it, and the gates

**Files:**
- Modify: `internal/web/static_budget_test.go` (`checkLazyViews`' list, its comment and `TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne`'s `all`: add `FlowBoardBody`, last), `tools/webshot/main.go` (`{"flow-board", "#flow-board"}` after `flows`, and its comment), `internal/web/ui/CLAUDE.md` (the lazy-view rule's list of chunks), `internal/web/dist/` (rebuilt)

- [ ] **Step 1:** add `"FlowBoardBody"` to the Go list first, and run `CGO_ENABLED=0 go test ./internal/web -run 'TestDist_LazyViewsAreChunksOfTheirOwn'` through `loom job run`: it fails with "the bundle has no lazy chunk for FlowBoardBody" (the guard is red before the bundle holds the chunk).
- [ ] **Step 2:** `loom job run -d "web ui build" -- sh -c 'cd internal/web/ui && pnpm run build && pnpm run dist:check'`.
- [ ] **Step 3:** the webshot line and the CLAUDE.md list; `go run ./tools/claudemd -v` for the budget's headroom.
- [ ] **Step 4:** gates — `CGO_ENABLED=0 go test ./internal/web/... ./tools/...` and `CGO_ENABLED=0 go vet ./internal/web/... ./tools/webshot/...` through `loom job run`; `gofmt -l` on the two Go files.
- [ ] **Step 5:** look at the screen: webshot's `flow-board` shot at phone and desktop widths, read as images.
- [ ] **Step 6: commit** — `build(web): the flow board's bundle, lazy-chunk guard and webshot screen`, by path (`internal/web/dist` wholly, since the build renames its hashed files).

## Self-review

- Spec §4.1–4.2 → package 1; §4.3–4.5 and §5 → package 2; §6's bundle and §7's gates → package 3.
- The spec says "no Go change": the two Go edits are a test's list and a dev tool's screen list, neither the daemon's behaviour.
- Names used across packages: `boardOf`, `columnOf`, `cardLine`, `flowState`, `STAGES`, `FINISHED`, `BoardCard`, `BoardColumn`, `flowBoardPanel`, `FlowBoardHead`, `FlowBoardBody`, panel id `flowBoard`, address `#/flow-board`.

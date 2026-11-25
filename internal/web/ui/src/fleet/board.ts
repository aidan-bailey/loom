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
      (a, b) => Number(b.flow.asks !== "") - Number(a.flow.asks !== "") || updated(b) - updated(a),
    );
  }
  return columns;
}

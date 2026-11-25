import { describe, expect, it } from "vitest";

import type { FlowSummary, Snapshot } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";

import {
  boardOf,
  cardLine,
  columnOf,
  FINISHED,
  flowState,
  STAGES,
  type BoardColumn,
} from "./board";
import { allSessions } from "./groups";

const NOW = Date.parse("2026-11-18T12:00:00Z");
const iso = (minutesAgo: number): string => new Date(NOW - minutesAgo * 60_000).toISOString();

// The canned fleet's session 6 (flow-run, workspace 2) holds two flows: a run parked on a
// question, and a designed spec.
function base(): FlowSummary {
  const flow = allSessions(fixtureSnapshot(NOW)).find((s) => s.id === 6)?.flows?.[0];
  if (flow === undefined) throw new Error("fixture has no flow");
  return flow;
}

// flow is a plain running flow, with `over` on top.
function flow(over: Partial<FlowSummary> = {}): FlowSummary {
  return { ...base(), stage: "run", status: "running", asks: "", phase: "", ...over };
}

// withFlows is the canned fleet with only session 6 holding flows: these.
function withFlows(flows: FlowSummary[]): Snapshot {
  const snapshot = fixtureSnapshot(NOW);
  for (const s of allSessions(snapshot)) s.flows = s.id === 6 ? flows : null;
  return snapshot;
}

const names = (columns: BoardColumn[]): string[] => columns.map((c) => c.name);
const plans = (columns: BoardColumn[], name: string): string[] =>
  (columns.find((c) => c.name === name)?.cards ?? []).map((c) => c.flow.plan);

describe("boardOf", () => {
  it("the columns are design, spec, draft, run, record and finished, in that order, empty ones included", () => {
    const columns = boardOf(withFlows([]));
    expect(names(columns)).toEqual(["design", "spec", "draft", "run", "record", "finished"]);
    expect(names(columns)).toEqual([...STAGES, FINISHED]);
    expect(columns.every((c) => c.cards.length === 0)).toBe(true);
  });

  it("a flow is in its stage's column", () => {
    const stages = ["design", "spec", "draft", "run", "record"] as const;
    const columns = boardOf(withFlows(stages.map((stage) => flow({ stage, plan: `${stage}.md` }))));
    for (const stage of stages) expect(plans(columns, stage)).toEqual([`${stage}.md`]);
    expect(plans(columns, FINISHED)).toEqual([]);
  });

  it("a done, stopped or cancelled flow is in finished, whatever its stage", () => {
    for (const status of ["done", "stopped", "cancelled"] as const) {
      expect(columnOf(flow({ status, stage: "run" }))).toBe(FINISHED);
      expect(columnOf(flow({ status, stage: "draft" }))).toBe(FINISHED);
    }
    for (const status of [
      "designing",
      "designed",
      "drafting",
      "drafted",
      "supervised",
      "running",
      "parked",
      "lost",
      "",
    ] as const) {
      expect(columnOf(flow({ status, stage: "run" }))).toBe("run");
    }
    const columns = boardOf(
      withFlows([flow({ status: "done", plan: "done.md" }), flow({ plan: "live.md" })]),
    );
    expect(plans(columns, FINISHED)).toEqual(["done.md"]);
    expect(plans(columns, "run")).toEqual(["live.md"]);
  });

  it("an asking, lost or unreadable flow stays in its stage's column", () => {
    const columns = boardOf(
      withFlows([
        flow({ plan: "asks.md", status: "parked", asks: "blocked-decision", updated: iso(1) }),
        flow({ plan: "lost.md", status: "lost", updated: iso(2) }),
        flow({ plan: "bad.md", unreadable: "bad plan", updated: iso(3) }),
      ]),
    );
    expect(plans(columns, "run")).toEqual(["asks.md", "lost.md", "bad.md"]);
    expect(plans(columns, FINISHED)).toEqual([]);
  });

  it("a stage this build does not know gets a column of its own name before finished", () => {
    const later = flow({ plan: "later.md" });
    (later as { stage: string }).stage = "verify";
    const columns = boardOf(withFlows([later, flow({ status: "done", plan: "done.md" })]));
    expect(names(columns)).toEqual([...STAGES, "verify", FINISHED]);
    expect(plans(columns, "verify")).toEqual(["later.md"]);
  });

  it("within a column asking flows come first, then the newest update; ties keep the snapshot's order", () => {
    const columns = boardOf(
      withFlows([
        flow({ plan: "old.md", updated: iso(30) }),
        flow({ plan: "tie-a.md", updated: iso(10) }),
        flow({ plan: "tie-b.md", updated: iso(10) }),
        flow({ plan: "new.md", updated: iso(1) }),
        flow({ plan: "asks-old.md", asks: "gate-failure", updated: iso(90) }),
        flow({ plan: "asks-new.md", asks: "gate-failure", updated: iso(5) }),
        flow({ plan: "undated.md", updated: "" }),
      ]),
    );
    expect(plans(columns, "run")).toEqual([
      "asks-new.md",
      "asks-old.md",
      "new.md",
      "tie-a.md",
      "tie-b.md",
      "old.md",
      "undated.md",
    ]);
  });

  it("a session with two flows has two cards, each with the session's id and title", () => {
    const columns = boardOf(
      withFlows([flow({ plan: "a.md" }), flow({ plan: "b.md", stage: "draft" })]),
    );
    const cards = columns.flatMap((c) => c.cards);
    expect(cards).toHaveLength(2);
    for (const card of cards) expect(card).toMatchObject({ session: 6, title: "flow-run" });
  });

  it("a hidden workspace's flows are left out", () => {
    const snapshot = withFlows([flow()]);
    const hidden: Snapshot = {
      ...snapshot,
      workspaces: (snapshot.workspaces ?? []).map((w) => (w.id === 2 ? { ...w, shown: false } : w)),
    };
    expect(boardOf(snapshot).flatMap((c) => c.cards)).toHaveLength(1);
    expect(boardOf(hidden).flatMap((c) => c.cards)).toHaveLength(0);
  });

  it("the fixture's board: flow-run asks in run, its designed flow in spec", () => {
    const columns = boardOf(fixtureSnapshot(NOW));
    expect(plans(columns, "run")).toEqual([
      "docs/superpowers/plans/2026-11-09-flow-visualisation.md",
    ]);
    expect(plans(columns, "spec")).toEqual([
      "docs/superpowers/plans/2026-11-16-flow-plan-treatment-part-2-estimate.md",
    ]);
    expect(columns.flatMap((c) => c.cards)).toHaveLength(2);
  });
});

describe("flowState", () => {
  it("asks, then lost, then a running flow's phase, else its status", () => {
    expect(flowState(flow({ asks: "blocked-decision", status: "lost" }))).toBe("asks");
    expect(flowState(flow({ status: "lost", phase: "review" }))).toBe("lost");
    expect(flowState(flow({ status: "running", phase: "review" }))).toBe("review");
    expect(flowState(flow({ status: "running", phase: "" }))).toBe("running");
    expect(flowState(flow({ status: "parked", phase: "story 3" }))).toBe("parked");
    expect(flowState(flow({ status: "" }))).toBe("");
  });
});

describe("cardLine", () => {
  it("says what the flow is doing without the stage its column names", () => {
    expect(cardLine(flow({ stage: "design", status: "designing" }))).toBe("designing");
    expect(cardLine(flow({ stage: "spec", status: "designed" }))).toBe("designed");
    expect(cardLine(flow({ stage: "draft", status: "drafting", drafted: 2, total: 5 }))).toBe(
      "draft 2/5",
    );
    expect(cardLine(flow({ stage: "draft", status: "drafted" }))).toBe("drafted");
    expect(cardLine(flow({ done: 3, total: 7, phase: "review" }))).toBe("3/7 · review");
    expect(cardLine(flow({ done: 2, total: 5, status: "parked", asks: "blocked-decision" }))).toBe(
      "2/5 · asks",
    );
    expect(cardLine(flow({ done: 0, total: 4, status: "" }))).toBe("0/4");
  });
});

import { describe, expect, it } from "vitest";

import type { FlowSummary, Session } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { flowChip, flowTone } from "@/panels/fleet/SessionRow";

import { sidebarLine } from "./Sidebar";

const NOW = Date.parse("2026-11-18T12:00:00Z");
const snapshot = fixtureSnapshot(NOW);
const byId = (id: number): Session => allSessions(snapshot).find((s) => s.id === id) as Session;
const flowOf = (session: Session, i: number): FlowSummary => (session.flows as FlowSummary[])[i];

describe("sidebarLine", () => {
  it("words a waiting session's why: the reason, then the detail", () => {
    expect(sidebarLine(byId(2))).toEqual({
      text: "Allow Bash: go test ./ui/... -run TestPane",
      tone: "attention",
    });
    expect(sidebarLine(byId(4))).toEqual({ text: "Plan approval", tone: "attention" });
    const detailOnly = { ...byId(4), wait: { reason: "", detail: "go test ./..." } };
    expect(sidebarLine(detailOnly).text).toBe("go test ./...");
  });

  it("a waiting session with no reason and no detail falls through to what it said", () => {
    const s = { ...byId(2), wait: { reason: "", detail: "" }, last_message: "thinking" };
    expect(sidebarLine(s)).toEqual({ text: "thinking", tone: "muted" });
  });

  it("a waiting session shows why it waits ahead of its flow", () => {
    const s = { ...byId(2), flows: byId(6).flows };
    expect(sidebarLine(s)).toEqual({
      text: "Allow Bash: go test ./ui/... -run TestPane",
      tone: "attention",
    });
  });

  it("a session with flows shows its newest flow's chip in the flow's tone", () => {
    const s6 = byId(6);
    const want = { text: flowChip(flowOf(s6, 0)), tone: flowTone(flowOf(s6, 0)) };
    expect(want.tone).toBe("attention");
    expect(want.text).not.toBe(flowChip(flowOf(s6, 1)));
    expect(sidebarLine(s6)).toEqual(want);
    expect(sidebarLine({ ...s6, flows: [...(s6.flows as FlowSummary[])].reverse() })).toEqual(want);

    const designed = flowOf(s6, 1);
    expect(sidebarLine({ ...s6, flows: [designed] })).toEqual({
      text: flowChip(designed),
      tone: "docs",
    });
    const lost: FlowSummary = { ...designed, status: "lost" };
    expect(sidebarLine({ ...s6, flows: [lost] }).tone).toBe("error");
  });

  it("equal or unreadable flow times keep the first flow", () => {
    const s6 = byId(6);
    const [a, b] = s6.flows as FlowSummary[];
    for (const updated of ["", "2026-11-18T11:00:00Z"]) {
      const s = {
        ...s6,
        flows: [
          { ...a, updated },
          { ...b, updated },
        ],
      };
      expect(sidebarLine(s).text).toBe(flowChip(s.flows[0]));
    }
  });

  it("a session with no flow and no wait shows its last message, muted", () => {
    const s1 = byId(1);
    expect(s1.last_message).not.toBe("");
    expect(sidebarLine(s1)).toEqual({ text: s1.last_message, tone: "muted" });
    expect(sidebarLine(byId(3))).toEqual({ text: "", tone: "muted" });
    expect(sidebarLine({ ...s1, flows: [] })).toEqual(sidebarLine({ ...s1, flows: null }));
  });
});

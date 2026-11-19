import { describe, expect, it } from "vitest";
import { fixtureTurns } from "@/fixtures";
import type { AgentPage, JobLog, Turn, Turns } from "./gen";
import {
  appendLog,
  mergeTurns,
  MAX_LOG_CHARS,
  type HeldLog,
  type HeldTurns,
  type TurnsLoad,
} from "./reads";

function turn(id: number): Turn {
  return {
    id,
    at: "2026-11-19T10:00:00Z",
    from: "you",
    name: "",
    text: `turn ${id}`,
    cut: false,
    command: false,
    partial: false,
    items: null,
  };
}

function page(ids: number[], extra: Partial<Turns> = {}): Turns {
  return {
    conversation: "c1",
    turns: ids.map(turn),
    earlier: null,
    reset: false,
    skipped: 0,
    ...extra,
  };
}

function heldOf(ids: number[], extra: Partial<HeldTurns> = {}): HeldTurns {
  return {
    conversation: "c1",
    turns: ids.map(turn),
    earlier: null,
    skipped: 0,
    agent: undefined,
    ...extra,
  };
}

const idsOf = (held: HeldTurns): number[] => held.turns.map((t) => t.id);

function logPage(text: string, end: number, extra: Partial<JobLog> = {}): JobLog {
  return { text, start: 0, end, cut: false, reset: false, ...extra };
}

describe("mergeTurns", () => {
  it.each<{ when: string; held: HeldTurns | undefined; next: Turns; load: TurnsLoad }>([
    {
      when: "no pages are held",
      held: undefined,
      next: page([5, 6], { earlier: 4 }),
      load: "refresh",
    },
    {
      when: "the load is newest",
      held: heldOf([1, 2]),
      next: page([5, 6], { earlier: 4 }),
      load: "newest",
    },
    {
      when: "the page is a reset",
      held: heldOf([1, 2]),
      next: page([5, 6], { earlier: 4, reset: true }),
      load: "refresh",
    },
  ])("replaces what is held when $when", ({ held, next, load }) => {
    const merged = mergeTurns(held, next, load);
    expect(idsOf(merged)).toEqual([5, 6]);
    expect(merged.conversation).toBe("c1");
    expect(merged.earlier).toBe(4);
  });

  it("puts an earlier page before the held turns, dropping a turn the held ones start with", () => {
    const held = heldOf([4, 5, 6], { earlier: 4 });
    const merged = mergeTurns(held, page([2, 3, 4], { earlier: 2 }), "earlier");
    expect(idsOf(merged)).toEqual([2, 3, 4, 5, 6]);
    expect(merged.earlier).toBe(2);
    const start = mergeTurns(held, page([1, 2, 3], { earlier: null }), "earlier");
    expect(start.earlier).toBeNull();
  });

  it("replaces every held turn from the refresh page's first on, and keeps `earlier`", () => {
    const held = heldOf([1, 2, 3], { earlier: 1 });
    const grown: Turn = { ...turn(3), text: "grown", partial: false };
    const merged = mergeTurns(
      held,
      { ...page([4]), turns: [grown, turn(4)], earlier: null },
      "refresh",
    );
    expect(idsOf(merged)).toEqual([1, 2, 3, 4]);
    expect(merged.turns[2]?.text).toBe("grown");
    expect(merged.earlier).toBe(1);
  });

  it("keeps the held turns when a refresh page holds none", () => {
    const held = heldOf([1, 2], { earlier: 1 });
    const merged = mergeTurns(held, { ...page([]), turns: null }, "refresh");
    expect(idsOf(merged)).toEqual([1, 2]);
    expect(merged.earlier).toBe(1);
  });

  it("keeps `skipped` once a page reported a record it could not read", () => {
    expect(mergeTurns(heldOf([1], { skipped: 2 }), page([2]), "refresh").skipped).toBe(2);
    expect(mergeTurns(heldOf([1]), page([2], { skipped: 1 }), "refresh").skipped).toBe(1);
    expect(
      mergeTurns(heldOf([1], { skipped: 2 }), page([2], { skipped: 3 }), "refresh").skipped,
    ).toBe(3);
    expect(mergeTurns(undefined, page([2], { skipped: 1 }), "newest").skipped).toBe(1);
  });

  it("takes the subagent from the latest page", () => {
    const agent: AgentPage = {
      id: "a1f00dcafe0001",
      type: "Explore",
      description: "look around",
      teammate: false,
      state: "running",
      started: "2026-11-19T10:00:00Z",
      missing: false,
    };
    expect(mergeTurns(heldOf([1]), page([2], { agent }), "refresh").agent).toEqual(agent);
    expect(mergeTurns(heldOf([4], { earlier: 4 }), page([3], { agent }), "earlier").agent).toEqual(
      agent,
    );
    expect(mergeTurns(undefined, page([1]), "newest").agent).toBeUndefined();
  });

  it("holds the fixture's page as it came", () => {
    const fixture = fixtureTurns();
    const merged = mergeTurns(undefined, fixture, "newest");
    expect(merged.turns).toEqual(fixture.turns ?? []);
    expect(merged.conversation).toBe(fixture.conversation);
  });
});

describe("appendLog", () => {
  it("holds a first page as it came", () => {
    expect(appendLog(undefined, logPage("one\n", 4, { cut: true }))).toEqual({
      text: "one\n",
      end: 4,
      cut: true,
    });
  });

  it("replaces the held log with a reset page", () => {
    const held: HeldLog = { text: "old\n", end: 4, cut: false };
    expect(appendLog(held, logPage("new\n", 9, { reset: true, cut: true }))).toEqual({
      text: "new\n",
      end: 9,
      cut: true,
    });
  });

  it("appends a page, moves `end` and keeps the held `cut`", () => {
    const held: HeldLog = { text: "one\n", end: 4, cut: true };
    expect(appendLog(held, logPage("two\n", 8, { cut: false }))).toEqual({
      text: "one\ntwo\n",
      end: 8,
      cut: true,
    });
  });

  it("trims past the cap at the next newline and marks the log cut", () => {
    const held: HeldLog = { text: "AAAA\n" + "b".repeat(MAX_LOG_CHARS - 5), end: 1, cut: false };
    const next = appendLog(held, logPage("\ncc", 2));
    expect(next.cut).toBe(true);
    expect(next.text.startsWith("b")).toBe(true);
    expect(next.text.endsWith("\ncc")).toBe(true);
    expect(next.text.length).toBe(MAX_LOG_CHARS - 2);
  });

  it("trims past the cap in mid-line when there is no newline to trim at", () => {
    const held: HeldLog = { text: "x".repeat(MAX_LOG_CHARS), end: 1, cut: false };
    const next = appendLog(held, logPage("yyy", 2));
    expect(next.cut).toBe(true);
    expect(next.text.length).toBe(MAX_LOG_CHARS);
    expect(next.text.endsWith("xxxyyy")).toBe(true);
  });

  it("leaves a log of exactly the cap whole", () => {
    const held: HeldLog = { text: "x".repeat(MAX_LOG_CHARS - 3), end: 1, cut: false };
    const next = appendLog(held, logPage("yyy", 2));
    expect(next.cut).toBe(false);
    expect(next.text.length).toBe(MAX_LOG_CHARS);
  });
});

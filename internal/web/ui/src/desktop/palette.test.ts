import { describe, expect, it } from "vitest";

import type { Session, Snapshot } from "@/api/gen";
import { context, makeSession } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";

import {
  compareCodePoints,
  fuzzyScore,
  paletteGroups,
  type PaletteGroup,
  type PaletteInput,
  type PaletteItem,
} from "./palette";

const NOW = Date.parse("2026-11-18T12:00:00Z");

function input(over: Partial<PaletteInput> = {}): PaletteInput {
  return {
    query: "",
    snapshot: fixtureSnapshot(NOW),
    commands: [],
    ctx: context(undefined),
    tabs: [],
    ...over,
  };
}

const keys = (items: PaletteItem[]): string[] => items.map((i) => i.key);

describe("fuzzyScore", () => {
  it('scores "pa" against "Pause" as 7', () => {
    expect(fuzzyScore("pa", "Pause")).toBe(7);
  });

  it('scores "pa" against "update panel" as 7, from the alignment at "panel"', () => {
    expect(fuzzyScore("pa", "update panel")).toBe(7);
  });

  it('scores "ps" against "Pause" as 5', () => {
    expect(fuzzyScore("ps", "Pause")).toBe(5);
  });

  it('scores "sp" against "session.push" as 8', () => {
    expect(fuzzyScore("sp", "session.push")).toBe(8);
  });

  it("gives null when the query's code points do not all match in order", () => {
    expect(fuzzyScore("xyz", "Pause")).toBeNull();
    expect(fuzzyScore("ba", "ab")).toBeNull();
  });

  it("takes the best start, not the first or the last", () => {
    expect(fuzzyScore("ab", "a ab")).toBe(7);
    expect(fuzzyScore("ab", "axbab")).toBe(5);
  });

  it("scores an empty or blank query as 0, and trims the query", () => {
    expect(fuzzyScore("", "Pause")).toBe(0);
    expect(fuzzyScore("   ", "Pause")).toBe(0);
    expect(fuzzyScore("  pa ", "Pause")).toBe(7);
    expect(fuzzyScore("", "")).toBe(0);
  });

  it("lowercases both sides", () => {
    expect(fuzzyScore("PA", "pause")).toBe(7);
    expect(fuzzyScore("pa", "PAUSE")).toBe(7);
  });

  it("starts a word at the text's start or after a code point that is neither a letter nor a digit", () => {
    expect(fuzzyScore("b", "b")).toBe(4);
    expect(fuzzyScore("b", " b")).toBe(4);
    expect(fuzzyScore("b", "-b")).toBe(4);
    expect(fuzzyScore("b", "ab")).toBe(1);
    expect(fuzzyScore("b", "1b")).toBe(1);
  });

  it("walks code points, not UTF-16 units", () => {
    expect(fuzzyScore("\u{1F600}", "a\u{1F600}")).toBe(1);
    // 4 for the first code point at the text's start, then 1 + 2 + 3 for the b: it follows the
    // emoji, which is neither a letter nor a digit, so it also starts a word.
    expect(fuzzyScore("\u{1F600}b", "\u{1F600}b")).toBe(10);
  });
});

describe("compareCodePoints", () => {
  it("orders by code point", () => {
    expect(compareCodePoints("a", "b")).toBeLessThan(0);
    expect(compareCodePoints("b", "a")).toBeGreaterThan(0);
    expect(compareCodePoints("a", "a")).toBe(0);
    expect(compareCodePoints("ab", "abc")).toBeLessThan(0);
    expect(compareCodePoints("Z", "a")).toBeLessThan(0);
  });

  it("puts an astral code point after a BMP one, where < on the strings does the opposite", () => {
    expect(compareCodePoints("\u{FF61}", "\u{1F600}")).toBeLessThan(0);
    expect(compareCodePoints("\u{1F600}", "\u{FF61}")).toBeGreaterThan(0);
    expect("\u{FF61}" < "\u{1F600}").toBe(false);
  });
});

describe("paletteGroups: sessions", () => {
  const sessionItems = (groups: PaletteGroup[]): PaletteItem[] =>
    groups.find((g) => g.label === "Sessions")?.items ?? [];
  const snapshotOf = (sessions: Session[]): Snapshot => ({
    ...fixtureSnapshot(NOW),
    sessions: { "1": sessions },
  });
  const plain = (id: number, title: string): Session => makeSession({ id, title, branch: "" });
  const codePointSorted = (list: string[]): string[] => [...list].sort(compareCodePoints);

  it("lists no sessions for a null snapshot", () => {
    const groups = paletteGroups(input({ snapshot: null, query: "flow" }));
    expect(groups.some((g) => g.label === "Sessions")).toBe(false);
  });

  it("with an empty query lists only the sessions that need you, the longest wait first", () => {
    expect(keys(sessionItems(paletteGroups(input())))).toEqual(["session:4", "session:2"]);
  });

  it("with an empty query leaves the Sessions group out when nothing needs you", () => {
    const groups = paletteGroups(input({ snapshot: snapshotOf([plain(1, "solo")]) }));
    expect(groups.some((g) => g.label === "Sessions")).toBe(false);
  });

  it("draws a session as its title, a hint of workspace and branch, and its link", () => {
    const items = sessionItems(paletteGroups(input({ query: "plain-shell" })));
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({
      kind: "session",
      key: "session:3",
      title: "plain-shell",
      hint: "loom · aidanb/plain-shell",
      href: "#/s/3",
    });
  });

  it("opens a session whose mockup is fresh at its mockup", () => {
    const items = sessionItems(paletteGroups(input({ query: "a-very-long" })));
    expect(items.find((i) => i.key === "session:7")).toMatchObject({ href: "#/s/7/mockup" });
  });

  it("leaves an empty branch or an unnamed workspace out of the hint", () => {
    const solo = plain(1, "solo");
    const bare = sessionItems(
      paletteGroups(input({ snapshot: snapshotOf([solo]), query: "solo" })),
    );
    expect(bare[0]).toMatchObject({ hint: "loom" });
    const orphan = { ...solo, workspace: 99, branch: "aidanb/solo" };
    const lost = sessionItems(
      paletteGroups(input({ snapshot: snapshotOf([orphan]), query: "solo" })),
    );
    expect(lost[0]).toMatchObject({ hint: "aidanb/solo" });
  });

  it("matches a session on its branch", () => {
    const items = sessionItems(paletteGroups(input({ query: "aidanb/plain" })));
    expect(keys(items)).toEqual(["session:3"]);
  });

  it("matches a session on its workspace's label, or its name when the label is empty", () => {
    const relabel = (id: number, label: string): Snapshot => {
      const snapshot = fixtureSnapshot(NOW);
      return {
        ...snapshot,
        workspaces: (snapshot.workspaces ?? []).map((w) => (w.id === id ? { ...w, label } : w)),
      };
    };
    const scratch = sessionItems(
      paletteGroups(input({ snapshot: relabel(2, "Scratch"), query: "scratch" })),
    );
    expect(codePointSorted(keys(scratch))).toEqual(["session:4", "session:5", "session:6"]);
    const loom = sessionItems(paletteGroups(input({ snapshot: relabel(1, ""), query: "loom" })));
    expect(codePointSorted(keys(loom))).toEqual([
      "session:1",
      "session:2",
      "session:3",
      "session:7",
    ]);
  });

  it("lists paused sessions, and those of a hidden workspace", () => {
    const listed = sessionItems(paletteGroups(input({ query: "flow-old" })));
    expect(keys(listed)).toEqual(["session:5"]);
    const snapshot = fixtureSnapshot(NOW);
    const hidden: Snapshot = {
      ...snapshot,
      workspaces: (snapshot.workspaces ?? []).map((w) => (w.id === 2 ? { ...w, shown: false } : w)),
    };
    const still = sessionItems(paletteGroups(input({ snapshot: hidden, query: "flow-old" })));
    expect(keys(still)).toEqual(["session:5"]);
  });

  it("drops what does not match", () => {
    const groups = paletteGroups(input({ query: "zzzz" }));
    expect(groups.some((g) => g.label === "Sessions")).toBe(false);
    expect(groups).toEqual([]);
  });

  it("orders by score, higher first", () => {
    const snapshot = snapshotOf([plain(10, "xpa"), plain(11, "update panel"), plain(12, "Pause")]);
    const items = sessionItems(paletteGroups(input({ snapshot, query: "pa" })));
    expect(keys(items)).toEqual(["session:12", "session:11", "session:10"]);
  });

  it("orders equal scores by title by code point, not locale", () => {
    const snapshot = snapshotOf([plain(10, "xb"), plain(11, "Xc")]);
    const items = sessionItems(paletteGroups(input({ snapshot, query: "x" })));
    expect(keys(items)).toEqual(["session:11", "session:10"]);
  });

  it("orders equal scores by title across BMP and astral code points", () => {
    const snapshot = snapshotOf([plain(10, "x\u{1F600}"), plain(11, "x\u{FF61}")]);
    const items = sessionItems(paletteGroups(input({ snapshot, query: "x" })));
    expect(keys(items)).toEqual(["session:11", "session:10"]);
  });

  it("orders equal scores and titles by key by code point", () => {
    const snapshot = snapshotOf([plain(3, "same"), plain(12, "same")]);
    const items = sessionItems(paletteGroups(input({ snapshot, query: "same" })));
    expect(keys(items)).toEqual(["session:12", "session:3"]);
  });

  it("scores a session by the best of its title, branch and workspace name", () => {
    const a = makeSession({ id: 10, title: "zzz", branch: "pa" });
    const b = plain(11, "xpa");
    const items = sessionItems(paletteGroups(input({ snapshot: snapshotOf([a, b]), query: "pa" })));
    expect(keys(items)).toEqual(["session:10", "session:11"]);
  });
});

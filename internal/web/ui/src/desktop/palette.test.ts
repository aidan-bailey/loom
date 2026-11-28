import { describe, expect, it } from "vitest";

import type { Session, Snapshot } from "@/api/gen";
import type { Command } from "@/app/commands";
import { context, makeSession } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";

import {
  compareCodePoints,
  fuzzyScore,
  paletteGroups,
  type PaletteCommand,
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

describe("paletteGroups: commands and places", () => {
  const cmd = (
    id: string,
    title: Command<unknown>["title"],
    over: Partial<Command<unknown>> = {},
  ): Command<unknown> => ({
    id,
    title,
    palette: true,
    when: () => true,
    run: () => Promise.resolve(),
    ...over,
  });
  const items = (groups: PaletteGroup[], label: PaletteGroup["label"]): PaletteItem[] =>
    groups.find((g) => g.label === label)?.items ?? [];
  const current = allSessions(fixtureSnapshot(NOW)).find((s) => s.id === 3);
  if (current === undefined) throw new Error("the canned snapshot holds no session 3");
  const tabs = [
    { id: "turns" as const, label: "conversation" },
    { id: "info" as const, label: "info" },
  ];
  const fourCommands = (): Command<unknown>[] => [
    cmd("c.beta", "beta"),
    cmd("c.zed", "Zed"),
    cmd("c.alpha", "alpha"),
    cmd("c.hidden", "hidden", { when: () => false }),
  ];
  const noFlows = (): Snapshot => {
    const snapshot = fixtureSnapshot(NOW);
    for (const s of allSessions(snapshot)) s.flows = null;
    return snapshot;
  };

  it("lists only the commands that carry the palette mark", () => {
    const commands = [
      cmd("a.one", "One"),
      cmd("a.two", "Two", { palette: false }),
      { ...cmd("a.three", "Three"), palette: undefined },
    ];
    expect(keys(items(paletteGroups(input({ commands })), "Commands"))).toEqual(["command:a.one"]);
  });

  it("draws a command as its key, its title and itself", () => {
    const one = cmd("a.one", "One");
    const [item] = items(paletteGroups(input({ commands: [one] })), "Commands");
    expect(item).toMatchObject({
      kind: "command",
      key: "command:a.one",
      title: "One",
      enabled: true,
    });
    expect((item as PaletteCommand).command).toBe(one);
  });

  it("calls a function title with the context", () => {
    const titled = cmd("a.title", (ctx) => `Pause ${ctx.session?.title ?? "nothing"}`);
    const withSession = items(
      paletteGroups(input({ commands: [titled], ctx: context(current) })),
      "Commands",
    );
    expect(withSession[0]).toMatchObject({ title: "Pause plain-shell" });
    const without = items(paletteGroups(input({ commands: [titled] })), "Commands");
    expect(without[0]).toMatchObject({ title: "Pause nothing" });
  });

  it("says enabled as commandEnabled does", () => {
    const enabled = (command: Command<unknown>, ctx = context(current)): boolean | undefined =>
      // a query, so a command whose `when` fails is listed (disabled) too
      (
        items(
          paletteGroups(input({ commands: [command], ctx, query: "a" })),
          "Commands",
        )[0] as PaletteCommand
      )?.enabled;
    const stale = context(current, { stale: true });
    expect(enabled(cmd("a", "A"), stale)).toBe(false);
    expect(enabled(cmd("a", "A", { offline: true }), stale)).toBe(true);
    expect(enabled(cmd("a", "A", { when: () => false }), context(current, { stale: false }))).toBe(
      false,
    );
    expect(enabled(cmd("a", "A"))).toBe(true);
  });

  it("with an empty query lists the commands whose when holds, by title by code point", () => {
    const groups = paletteGroups(input({ commands: fourCommands() }));
    expect(keys(items(groups, "Commands"))).toEqual([
      "command:c.zed",
      "command:c.alpha",
      "command:c.beta",
    ]);
  });

  it("with an empty query lists a command whose when holds even while stale, not enabled", () => {
    const groups = paletteGroups(
      input({ commands: [cmd("a.one", "One")], ctx: context(current, { stale: true }) }),
    );
    expect(items(groups, "Commands")[0]).toMatchObject({ key: "command:a.one", enabled: false });
  });

  it("with a query matches on the title and lists a command whose when fails, not enabled", () => {
    const hid = items(paletteGroups(input({ commands: fourCommands(), query: "hid" })), "Commands");
    expect(hid).toHaveLength(1);
    expect(hid[0]).toMatchObject({ key: "command:c.hidden", enabled: false });
    const a = items(paletteGroups(input({ commands: fourCommands(), query: "a" })), "Commands");
    expect(keys(a)).toEqual(["command:c.alpha", "command:c.beta"]);
  });

  it("matches a command on its title, never its id", () => {
    const groups = paletteGroups(input({ commands: [cmd("zzz.id", "One")], query: "zzz" }));
    expect(groups.some((g) => g.label === "Commands")).toBe(false);
  });

  it("with an empty query lists the session's tabs, then Fleet, New session, Flow board, Flows and Settings", () => {
    const groups = paletteGroups(input({ ctx: context(current), tabs }));
    expect(keys(items(groups, "Places"))).toEqual([
      "place:turns",
      "place:info",
      "place:fleet",
      "place:new",
      "place:flowBoard",
      "place:flows",
      "place:settings",
    ]);
  });

  it("draws a tab as its label, the session's title and its tab's link", () => {
    const places = items(paletteGroups(input({ ctx: context(current), tabs })), "Places");
    const byKey = (key: string): PaletteItem | undefined => places.find((i) => i.key === key);
    expect(byKey("place:turns")).toMatchObject({
      kind: "place",
      title: "conversation",
      hint: "plain-shell",
      href: "#/s/3",
    });
    expect(byKey("place:info")).toMatchObject({ href: "#/s/3/info" });
  });

  it("draws Fleet, New session, Flows and Settings with their titles, no hint and their links", () => {
    const places = items(paletteGroups(input()), "Places");
    const byKey = (key: string): PaletteItem | undefined => places.find((i) => i.key === key);
    expect(byKey("place:fleet")).toMatchObject({ title: "Fleet", hint: "", href: "#/" });
    expect(byKey("place:new")).toMatchObject({ title: "New session", hint: "", href: "#/new" });
    expect(byKey("place:flows")).toMatchObject({ title: "Flows", hint: "", href: "#/flows" });
    expect(byKey("place:flowBoard")).toMatchObject({
      title: "Flow board",
      hint: "",
      href: "#/flow-board",
    });
    expect(byKey("place:settings")).toMatchObject({
      title: "Settings",
      hint: "",
      href: "#/settings",
    });
  });

  it("leaves the Flows place out when no shown workspace's session has a flow", () => {
    const none = items(paletteGroups(input({ snapshot: noFlows() })), "Places");
    expect(keys(none)).not.toContain("place:flows");
    expect(keys(none)).not.toContain("place:flowBoard");
    const snapshot = fixtureSnapshot(NOW);
    const hidden: Snapshot = {
      ...snapshot,
      workspaces: (snapshot.workspaces ?? []).map((w) => (w.id === 2 ? { ...w, shown: false } : w)),
    };
    expect(keys(items(paletteGroups(input({ snapshot: hidden })), "Places"))).not.toContain(
      "place:flows",
    );
    expect(keys(items(paletteGroups(input({ snapshot: null })), "Places"))).toEqual([
      "place:fleet",
      "place:new",
      "place:settings",
    ]);
  });

  it("has no tab places without a session", () => {
    expect(keys(items(paletteGroups(input()), "Places"))).toEqual([
      "place:fleet",
      "place:new",
      "place:flowBoard",
      "place:flows",
      "place:settings",
    ]);
    expect(keys(items(paletteGroups(input({ tabs })), "Places"))).toEqual([
      "place:fleet",
      "place:new",
      "place:flowBoard",
      "place:flows",
      "place:settings",
    ]);
  });

  it("with a query matches a place on its title, not its hint, ordered by score, title, key", () => {
    const ctx = context(current);
    const s = items(paletteGroups(input({ query: "s", ctx, tabs })), "Places");
    expect(keys(s)).toEqual(["place:new", "place:settings", "place:flows", "place:turns"]);
    const plain = paletteGroups(input({ query: "plain", ctx, tabs }));
    expect(plain.some((g) => g.label === "Places")).toBe(false);
  });

  it("a query of only whitespace gives the empty query's groups", () => {
    const commands = [cmd("a.one", "One")];
    const ctx = context(current);
    expect(paletteGroups(input({ query: "  ", commands, ctx, tabs }))).toEqual(
      paletteGroups(input({ query: "", commands, ctx, tabs })),
    );
  });

  it("orders the groups Sessions, Commands, Places, and leaves out an empty one", () => {
    const commands = [cmd("a.one", "One")];
    const labels = (groups: PaletteGroup[]): string[] => groups.map((g) => g.label);
    expect(labels(paletteGroups(input({ commands })))).toEqual(["Sessions", "Commands", "Places"]);
    const quiet: Snapshot = {
      ...fixtureSnapshot(NOW),
      sessions: { "1": [makeSession({ id: 1, title: "solo", branch: "" })] },
    };
    expect(labels(paletteGroups(input({ commands, snapshot: quiet })))).toEqual([
      "Commands",
      "Places",
    ]);
    expect(labels(paletteGroups(input({ snapshot: quiet })))).toEqual(["Places"]);
    expect(paletteGroups(input({ commands, query: "zzzz" }))).toEqual([]);
  });

  it("matches across the three kinds in one query", () => {
    const groups = paletteGroups(
      input({
        query: "pa",
        commands: [cmd("p.pause", "Pause")],
        ctx: context(current),
        tabs: [{ id: "turns", label: "conversation" }],
      }),
    );
    expect(keys(items(groups, "Sessions"))).toEqual(["session:1", "session:3"]);
    expect(keys(items(groups, "Commands"))).toEqual(["command:p.pause"]);
    expect(groups.some((g) => g.label === "Places")).toBe(false);
  });
});

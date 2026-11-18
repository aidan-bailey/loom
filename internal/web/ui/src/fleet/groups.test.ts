import { describe, expect, it } from "vitest";
import type { Session, SessionGroup, Snapshot } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import {
  allSessions,
  emptyGroups,
  fleetGroups,
  GROUPS,
  groupSessions,
  nextInGroup,
  shownWorkspaces,
} from "./groups";

const NOW = Date.parse("2026-11-18T12:00:00Z");
const ids = (list: Session[]) => list.map((s) => s.id);

// The canned fleet: workspace 1 (loom) holds 1 working (2m), 2 needs you (18m), 3 working (9m) and
// 7 your turn (41m); workspace 2 (flow) holds 4 needs you (52m), 5 paused and 6 working (4m).
describe("GROUPS", () => {
  it("lists Needs you, Your turn, Working and Paused, in that order", () => {
    expect(GROUPS).toEqual([
      { key: "needs_you", title: "Needs you" },
      { key: "your_turn", title: "Your turn" },
      { key: "working", title: "Working" },
      { key: "paused", title: "Paused" },
    ]);
  });
});

describe("allSessions and shownWorkspaces", () => {
  it("flattens the sessions of every workspace", () => {
    expect(ids(allSessions(fixtureSnapshot(NOW))).sort()).toEqual([1, 2, 3, 4, 5, 6, 7]);
  });

  it("copes with no sessions, and a workspace whose list is null", () => {
    const snapshot = fixtureSnapshot(NOW);
    expect(allSessions({ ...snapshot, sessions: null })).toEqual([]);
    const second = snapshot.sessions?.["2"] ?? null;
    const partial: Snapshot = { ...snapshot, sessions: { "1": null, "2": second } };
    expect(ids(allSessions(partial)).sort()).toEqual([4, 5, 6]);
  });

  it("lists the workspaces the daemon shows, and one without the flag", () => {
    const snapshot = fixtureSnapshot(NOW);
    const [first, second] = snapshot.workspaces ?? [];
    if (!first || !second) throw new Error("the fixture has two workspaces");
    const hidden = { ...snapshot, workspaces: [first, { ...second, shown: false }] };
    expect(shownWorkspaces(hidden).map((w) => w.id)).toEqual([1]);
    const older: Partial<typeof second> = { ...second };
    delete older.shown;
    const flagless = { ...snapshot, workspaces: [first, older as typeof second] };
    expect(shownWorkspaces(flagless).map((w) => w.id)).toEqual([1, 2]);
    expect(shownWorkspaces({ ...snapshot, workspaces: null })).toEqual([]);
  });
});

describe("groupSessions", () => {
  const sessions = allSessions(fixtureSnapshot(NOW));

  it("orders Needs you by the longest wait and the other groups newest first", () => {
    const groups = groupSessions(sessions, null);
    expect(ids(groups.needs_you)).toEqual([4, 2]);
    expect(ids(groups.your_turn)).toEqual([7]);
    expect(ids(groups.working)).toEqual([1, 6, 3]);
    expect(ids(groups.paused)).toEqual([5]);
  });

  it("keeps one workspace's sessions when given its id", () => {
    const groups = groupSessions(sessions, 1);
    expect(ids(groups.needs_you)).toEqual([2]);
    expect(ids(groups.working)).toEqual([1, 3]);
    expect(ids(groups.paused)).toEqual([]);
  });

  it("lists a group this build does not know under Paused, never drops it", () => {
    const stranger = { ...sessions[0], id: 99, group: "mystery" as SessionGroup };
    const groups = groupSessions([...sessions, stranger], null);
    expect(ids(groups.paused)).toContain(99);
  });

  it("sorts a session with no time last among the newest-first groups", () => {
    const first = sessions.find((s) => s.id === 1) as Session;
    const timeless = { ...first, id: 50, since: "" };
    expect(ids(groupSessions([timeless, first], null).working)).toEqual([1, 50]);
  });
});

describe("fleetGroups", () => {
  it("groups the sessions of the shown workspaces, whatever the fleet's filter", () => {
    const groups = fleetGroups(fixtureSnapshot(NOW));
    expect(ids(groups.needs_you)).toEqual([4, 2]);
    expect(ids(groups.working)).toEqual([1, 6, 3]);
  });

  it("leaves out the sessions of a workspace the fleet does not show", () => {
    const snapshot = fixtureSnapshot(NOW);
    const [first, second] = snapshot.workspaces ?? [];
    if (!first || !second) throw new Error("the fixture has two workspaces");
    const hidden: Snapshot = { ...snapshot, workspaces: [first, { ...second, shown: false }] };
    const groups = fleetGroups(hidden);
    expect(ids(groups.needs_you)).toEqual([2]);
    expect(ids(groups.paused)).toEqual([]);
  });
});

describe("nextInGroup", () => {
  const groups = fleetGroups(fixtureSnapshot(NOW));

  it("is the session after the current one, wrapping to the first", () => {
    expect(nextInGroup(groups, "working", 1)?.id).toBe(6);
    expect(nextInGroup(groups, "working", 3)?.id).toBe(1);
  });

  it("is the first when the current session is not in the group or there is none", () => {
    expect(nextInGroup(groups, "needs_you", 7)?.id).toBe(4);
    expect(nextInGroup(groups, "needs_you", null)?.id).toBe(4);
  });

  it("is undefined for an empty group", () => {
    expect(nextInGroup(emptyGroups(), "working", 1)).toBeUndefined();
  });
});

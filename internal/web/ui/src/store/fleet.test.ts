import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Notice, Session } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { MAX_NOTICES, useFleet, useFleetGroups, useLink, useSession } from "./fleet";
import { clearSnapshot } from "./persist";

vi.mock("./persist");

const NOW = Date.parse("2026-11-18T12:00:00Z");
const RESET = { snapshot: null, stale: true, link: "connecting", updated: 0 } as const;
const store = () => useFleet.getState();
const sessionsOf = (workspace: number): Session[] =>
  fixtureSnapshot(NOW).sessions?.[String(workspace)] ?? [];
const notice = (text: string): Notice => ({ text, error: false, at: "2026-11-18T12:00:00Z" });

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  vi.setSystemTime(NOW);
  useFleet.setState(RESET);
  vi.mocked(clearSnapshot).mockResolvedValue(undefined);
});

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("the store", () => {
  it("starts empty, stale and connecting", () => {
    expect(store()).toMatchObject({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  });

  it("installs a snapshot, goes live and stamps the time", () => {
    const snapshot = fixtureSnapshot(NOW);
    store().apply("snapshot", snapshot);
    expect(store().snapshot).toBe(snapshot);
    expect(store()).toMatchObject({ stale: false, link: "live", updated: NOW });
  });

  it("replaces one workspace's sessions on a sessions event and keeps the others", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    const second = store().snapshot?.sessions?.["2"];
    const only = sessionsOf(1).slice(0, 1);
    vi.advanceTimersByTime(500);
    store().apply("sessions", { workspace: 1, sessions: only, badge: 1 });
    expect(store().snapshot?.sessions?.["1"]).toEqual(only);
    expect(store().snapshot?.sessions?.["2"]).toBe(second);
    expect(store().updated).toBe(NOW + 500);
  });

  it("ignores a sessions event that arrives before any snapshot", () => {
    store().apply("sessions", { workspace: 1, sessions: sessionsOf(1), badge: 0 });
    expect(store()).toMatchObject({ snapshot: null, updated: 0 });
  });

  it("drops the sessions of a workspace that closed on a workspaces event", () => {
    const snapshot = fixtureSnapshot(NOW);
    store().apply("snapshot", snapshot);
    const open = (snapshot.workspaces ?? []).slice(0, 1);
    store().apply("workspaces", { workspaces: open, badge: 0 });
    expect(Object.keys(store().snapshot?.sessions ?? {})).toEqual(["1"]);
    expect(store().snapshot?.workspaces).toEqual(open);
  });

  it("replaces usage, quick replies and the device on their events", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    store().apply("usage", { usage: [] });
    store().apply("quick_replies", { quick_replies: ["ok"] });
    const device = {
      name: "iPad",
      notify: { waiting: false, finished: true, paused_until: "" },
      push: "",
    };
    store().apply("device", device);
    expect(store().snapshot?.usage).toEqual([]);
    expect(store().snapshot?.quick_replies).toEqual(["ok"]);
    expect(store().snapshot?.device).toEqual(device);
  });

  it("keeps the last MAX_NOTICES notices it is given, the oldest dropped", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    for (let i = 0; i < MAX_NOTICES + 5; i++) store().addNotices([notice(`n${i}`)]);
    const texts = (store().snapshot?.notices ?? []).map((n) => n.text);
    expect(MAX_NOTICES).toBe(20);
    expect(texts).toHaveLength(MAX_NOTICES);
    expect(texts[0]).toBe("n5");
    expect(texts.at(-1)).toBe(`n${MAX_NOTICES + 4}`);
  });

  it("appends a batch in order after the notices already held", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    const held = store().snapshot?.notices ?? [];
    store().addNotices([notice("a"), notice("b")]);
    expect(store().snapshot?.notices).toEqual([...held, notice("a"), notice("b")]);
  });

  it("keeps no notice while there is no snapshot", () => {
    store().addNotices([notice("a")]);
    expect(store().snapshot).toBeNull();
  });

  it("does not stamp the time for notices it is given: they are not news from the daemon", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    vi.advanceTimersByTime(7000);
    store().addNotices([notice("a")]);
    expect(store().updated).toBe(NOW);
  });

  it("keeps nothing for a stream notice event and stamps the time: the onNotices listener keeps it", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    const held = store().snapshot?.notices;
    vi.advanceTimersByTime(7000);
    store().apply("notice", notice("x"));
    expect(store().snapshot?.notices).toEqual(held);
    expect(store().updated).toBe(NOW + 7000);
  });

  it("stamps the time on a ping and changes nothing on a bye", () => {
    const snapshot = fixtureSnapshot(NOW);
    store().apply("snapshot", snapshot);
    vi.advanceTimersByTime(7000);
    store().apply("ping", {});
    expect(store().updated).toBe(NOW + 7000);
    expect(store().snapshot).toBe(snapshot);
    vi.advanceTimersByTime(7000);
    store().apply("bye", {});
    expect(store().updated).toBe(NOW + 7000);
    expect(store().snapshot).toBe(snapshot);
  });

  it.each([
    ["connecting", true],
    ["live", false],
    ["restarting", true],
    ["offline", true],
  ] as const)("setLink(%s) leaves stale %s", (state, stale) => {
    store().setLink(state);
    expect(store()).toMatchObject({ link: state, stale });
  });

  it("loads a saved snapshot with its time and keeps it stale", () => {
    const saved = fixtureSnapshot(NOW);
    store().load(saved, 1234);
    expect(store()).toMatchObject({
      snapshot: saved,
      updated: 1234,
      stale: true,
      link: "connecting",
    });
  });

  it("does not load a saved snapshot over a live one", () => {
    const live = fixtureSnapshot(NOW);
    store().apply("snapshot", live);
    store().load(fixtureSnapshot(NOW - 1000), 1);
    expect(store().snapshot).toBe(live);
  });

  it("clears the store and the saved snapshot", async () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    await store().clear();
    expect(store()).toMatchObject({ snapshot: null, stale: true, link: "connecting", updated: 0 });
    expect(clearSnapshot).toHaveBeenCalledOnce();
  });
});

describe("the selectors", () => {
  it("useSession finds a session by id, and nothing for an unknown or undefined id", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    expect(renderHook(() => useSession(2)).result.current?.title).toBe("sandbox-ask");
    expect(renderHook(() => useSession(999)).result.current).toBeUndefined();
    expect(renderHook(() => useSession(undefined)).result.current).toBeUndefined();
  });

  it("useSession follows a change to its session and ignores the rest", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    const { result } = renderHook(() => useSession(2));
    const before = result.current;
    act(() => {
      store().apply("sessions", { workspace: 2, sessions: sessionsOf(2), badge: 1 });
    });
    expect(result.current).toBe(before);
    const renamed = sessionsOf(1).map((s) => (s.id === 2 ? { ...s, title: "renamed" } : s));
    act(() => {
      store().apply("sessions", { workspace: 1, sessions: renamed, badge: 1 });
    });
    expect(result.current?.title).toBe("renamed");
  });

  it("useFleetGroups is empty before a snapshot and groups the fleet after one", () => {
    const { result } = renderHook(() => useFleetGroups());
    expect(result.current.needs_you).toEqual([]);
    act(() => {
      store().apply("snapshot", fixtureSnapshot(NOW));
    });
    expect(result.current.needs_you.map((s) => s.id)).toEqual([4, 2]);
    expect(result.current.working.map((s) => s.id)).toEqual([1, 6, 3]);
  });

  it("useFleetGroups gives the same object again while the snapshot is the same", () => {
    store().apply("snapshot", fixtureSnapshot(NOW));
    const { result, rerender } = renderHook(() => useFleetGroups());
    const before = result.current;
    rerender();
    expect(result.current).toBe(before);
    act(() => {
      store().apply("ping", {});
    });
    expect(result.current).toBe(before);
  });

  it("useLink gives the link and stale, and the same object while neither changes", () => {
    const { result } = renderHook(() => useLink());
    expect(result.current).toEqual({ state: "connecting", stale: true });
    const before = result.current;
    act(() => {
      store().apply("ping", {});
    });
    expect(result.current).toBe(before);
    act(() => {
      store().setLink("live");
    });
    expect(result.current).toEqual({ state: "live", stale: false });
  });
});

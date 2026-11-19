import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Session } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";

import { allSessions } from "./groups";
import { hasNewTurn, markSeen, pruneSeen, readSeen, SEEN_KEY, useMarkSeen, useSeen } from "./seen";

const NOW = Date.parse("2026-11-18T12:00:00Z");
// What the classic pref("seen", JSON.stringify({ "5": 1700000000000 })) stores.
const CLASSIC = '"{\\"5\\":1700000000000}"';

const encode = (seen: Record<string, number>): string => JSON.stringify(JSON.stringify(seen));

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
});

afterEach(() => {
  // Unmounting marks a session seen, so it goes before the storage is cleared.
  cleanup();
  vi.useRealTimers();
  window.localStorage.clear();
  vi.restoreAllMocks();
});

function yourTurn(id: number, since: string, group: Session["group"] = "your_turn"): Session {
  const base = allSessions(fixtureSnapshot())[0];
  if (base === undefined) throw new Error("the fixture holds no session");
  return { ...base, id, group, since };
}

describe("the seen times", () => {
  it("SEEN_KEY is the classic key", () => {
    expect(SEEN_KEY).toBe("loom.seen");
  });

  it("readSeen reads what the classic pref wrote", () => {
    window.localStorage.setItem(SEEN_KEY, CLASSIC);
    expect(readSeen()).toEqual({ "5": 1700000000000 });
  });

  it("readSeen is empty for nothing stored and for every value it cannot use", () => {
    expect(readSeen()).toEqual({});
    for (const raw of [
      "not json",
      JSON.stringify("[1,2]"),
      JSON.stringify("null"),
      JSON.stringify({ "5": 1 }),
    ]) {
      window.localStorage.setItem(SEEN_KEY, raw);
      expect(readSeen()).toEqual({});
    }
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(readSeen()).toEqual({});
  });

  it("readSeen keeps only the entries that are numbers", () => {
    window.localStorage.setItem(
      SEEN_KEY,
      JSON.stringify(JSON.stringify({ "5": 10, "6": "x", "7": null })),
    );
    expect(readSeen()).toEqual({ "5": 10 });
  });

  it("markSeen writes the classic double encoding and keeps the other times", () => {
    window.localStorage.setItem(SEEN_KEY, CLASSIC);
    markSeen(6);
    expect(window.localStorage.getItem(SEEN_KEY)).toBe(encode({ "5": 1700000000000, "6": NOW }));
  });

  it("markSeen survives a refused write", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("full");
    });
    expect(() => {
      markSeen(5);
    }).not.toThrow();
  });

  it("pruneSeen drops the times of sessions no longer listed, in the classic encoding", () => {
    window.localStorage.setItem(SEEN_KEY, encode({ "5": 1, "6": 2, "7": 3 }));
    pruneSeen([5, 7]);
    expect(window.localStorage.getItem(SEEN_KEY)).toBe(encode({ "5": 1, "7": 3 }));
  });

  it("pruneSeen of an empty list prunes nothing", () => {
    window.localStorage.setItem(SEEN_KEY, CLASSIC);
    pruneSeen([]);
    expect(window.localStorage.getItem(SEEN_KEY)).toBe(CLASSIC);
  });

  it("pruneSeen writes nothing when nothing is dropped", () => {
    window.localStorage.setItem(SEEN_KEY, CLASSIC);
    const set = vi.spyOn(Storage.prototype, "setItem");
    pruneSeen([5]);
    expect(set).not.toHaveBeenCalled();
  });
});

describe("hasNewTurn", () => {
  const since = "2026-11-18T11:00:00Z";
  const T = Date.parse(since);

  it("is true for a Your turn session never opened, or whose turn ended after it was opened", () => {
    const s = yourTurn(5, since);
    expect(hasNewTurn(s, {})).toBe(true);
    expect(hasNewTurn(s, { "5": T - 1000 })).toBe(true);
    expect(hasNewTurn(s, { "5": T + 1000 })).toBe(false);
    expect(hasNewTurn(s, { "5": T })).toBe(false);
  });

  it("is false for every other group, opened or not", () => {
    for (const group of ["needs_you", "working", "paused"] as const) {
      expect(hasNewTurn(yourTurn(5, since, group), {})).toBe(false);
    }
  });
});

describe("useMarkSeen and useSeen", () => {
  it("useMarkSeen marks on mount and again on unmount", () => {
    const { unmount } = renderHook(() => {
      useMarkSeen(5);
    });
    expect(readSeen()).toEqual({ "5": NOW });

    vi.setSystemTime(NOW + 60_000);
    unmount();
    expect(readSeen()).toEqual({ "5": NOW + 60_000 });
  });

  it("useMarkSeen marks the old session closed and the new one opened when the id changes, and nothing without one", () => {
    const { rerender } = renderHook<void, { id: number | undefined }>(
      ({ id }) => {
        useMarkSeen(id);
      },
      { initialProps: { id: 5 } },
    );
    vi.setSystemTime(NOW + 60_000);
    rerender({ id: 6 });
    expect(readSeen()).toEqual({ "5": NOW + 60_000, "6": NOW + 60_000 });

    window.localStorage.clear();
    renderHook(() => {
      useMarkSeen(undefined);
    });
    expect(window.localStorage.getItem(SEEN_KEY)).toBeNull();
  });

  it("useSeen follows the writes of markSeen and pruneSeen", () => {
    const { result } = renderHook(() => useSeen());
    expect(result.current).toEqual({});

    act(() => {
      markSeen(5);
    });
    expect(result.current["5"]).toBe(NOW);

    act(() => {
      pruneSeen([9]);
    });
    expect(result.current).toEqual({});
  });
});

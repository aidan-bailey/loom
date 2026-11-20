import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { emitUnpaired } from "@/api/request";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import { PENDING_MS, useIsPending, usePending } from "./pending";

const NOW = Date.parse("2026-11-18T12:00:00Z");
const NEW_ID = 4242;
const pending = () => usePending.getState();

// list is the stream's sessions event for workspace 1 with a session of this id added to the ones
// the snapshot holds.
function list(id: number): void {
  const base = allSessions(fixtureSnapshot(NOW))[0];
  if (base === undefined) throw new Error("the canned snapshot holds no session");
  const held = useFleet.getState().snapshot?.sessions?.["1"] ?? [];
  useFleet.getState().apply("sessions", {
    workspace: 1,
    sessions: [...held, { ...base, id, workspace: 1 }],
    badge: 0,
  });
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  vi.setSystemTime(NOW);
  useFleet.setState({ snapshot: fixtureSnapshot(NOW), stale: false, link: "live", updated: NOW });
});

afterEach(() => {
  pending().clear();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  vi.useRealTimers();
});

describe("the pending session", () => {
  it("starts with no session pending", () => {
    expect(pending().id).toBeNull();
  });

  it("holds a marked id for PENDING_MS and then lets it go", () => {
    pending().mark(NEW_ID);
    expect(pending().id).toBe(NEW_ID);
    vi.advanceTimersByTime(PENDING_MS - 1);
    expect(pending().id).toBe(NEW_ID);
    vi.advanceTimersByTime(1);
    expect(pending().id).toBeNull();
  });

  it("replaces an earlier id and restarts the wait, so the earlier wait never ends the later id", () => {
    pending().mark(NEW_ID);
    vi.advanceTimersByTime(3000);
    pending().mark(NEW_ID + 1);
    vi.advanceTimersByTime(3000);
    expect(pending().id).toBe(NEW_ID + 1);
    vi.advanceTimersByTime(2000);
    expect(pending().id).toBeNull();
  });

  it("lets go once the snapshot lists the session, and not for another session's listing", () => {
    pending().mark(NEW_ID);
    list(NEW_ID + 1);
    expect(pending().id).toBe(NEW_ID);
    list(NEW_ID);
    expect(pending().id).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("lets go at once for a session the snapshot already lists", () => {
    list(NEW_ID);
    pending().mark(NEW_ID);
    expect(pending().id).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("clear cancels the wait and closes the watch of the snapshot", () => {
    pending().mark(NEW_ID);
    pending().clear();
    expect(pending().id).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
    // A watch that outlived clear would let go of this id when NEW_ID is listed.
    usePending.setState({ id: 7 });
    list(NEW_ID);
    expect(pending().id).toBe(7);
  });

  it("lets go when the device is unpaired", () => {
    pending().mark(NEW_ID);
    emitUnpaired();
    expect(pending().id).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe("useIsPending", () => {
  it("is true only for the marked id, and false for an undefined one", () => {
    const marked = renderHook(() => useIsPending(NEW_ID));
    const other = renderHook(() => useIsPending(NEW_ID + 1));
    const none = renderHook(() => useIsPending(undefined));
    expect(marked.result.current).toBe(false);

    act(() => {
      pending().mark(NEW_ID);
    });
    expect(marked.result.current).toBe(true);
    expect(other.result.current).toBe(false);
    expect(none.result.current).toBe(false);

    act(() => {
      pending().clear();
    });
    expect(marked.result.current).toBe(false);
  });
});

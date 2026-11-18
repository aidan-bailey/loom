import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Notice, Snapshot } from "@/api/gen";
import { queryClient } from "@/api/queries";
import { emitStopping, emitUnpaired, onNotices } from "@/api/request";
import { connectStream, RESTART_GRACE, type StreamHandlers } from "@/api/stream";
import { fixtureSnapshot } from "@/fixtures";
import { setBadge } from "./badge";
import { startFleet, useFleet } from "./fleet";
import { clearSnapshot, loadSnapshot, saveSnapshot } from "./persist";

vi.mock("@/api/stream");
vi.mock("./badge");
vi.mock("./persist");

type Saved = { snapshot: Snapshot; updated: number };

const NOW = Date.parse("2026-11-18T12:00:00Z");
const closeStream = vi.fn();
const stops: (() => void)[] = [];
let handlers: StreamHandlers;

const store = () => useFleet.getState();
const settle = () => vi.advanceTimersByTimeAsync(0);
const notice = (text: string): Notice => ({ text, error: false, at: "2026-11-18T12:00:00Z" });

function start(): () => void {
  const stop = startFleet();
  stops.push(stop);
  return stop;
}

// A load that stays pending until the test resolves it.
function pendingLoad(): (saved: Saved) => void {
  let resolveLoad: (saved: Saved) => void = () => undefined;
  vi.mocked(loadSnapshot).mockReturnValue(
    new Promise<Saved>((resolve) => {
      resolveLoad = resolve;
    }),
  );
  return (saved) => {
    resolveLoad(saved);
  };
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  vi.setSystemTime(NOW);
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  vi.mocked(loadSnapshot).mockResolvedValue(null);
  vi.mocked(clearSnapshot).mockResolvedValue(undefined);
  vi.mocked(connectStream).mockImplementation((h) => {
    handlers = h;
    return closeStream;
  });
});

afterEach(() => {
  for (const stop of stops.splice(0)) stop();
  queryClient.clear();
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("startFleet", () => {
  it("loads the saved snapshot into a stale store", async () => {
    const saved = fixtureSnapshot(NOW);
    vi.mocked(loadSnapshot).mockResolvedValue({ snapshot: saved, updated: 1234 });
    start();
    await settle();
    expect(store()).toMatchObject({ snapshot: saved, updated: 1234, stale: true });
  });

  it("does not load the saved snapshot over a live one", async () => {
    const resolveLoad = pendingLoad();
    start();
    const live = fixtureSnapshot(NOW);
    handlers.onEvent("snapshot", live);
    resolveLoad({ snapshot: fixtureSnapshot(NOW - 5000), updated: 1 });
    await settle();
    expect(store().snapshot).toBe(live);
  });

  it("drops a saved snapshot that was still loading when the device was unpaired", async () => {
    const resolveLoad = pendingLoad();
    start();
    emitUnpaired();
    resolveLoad({ snapshot: fixtureSnapshot(NOW), updated: 1 });
    await settle();
    expect(store().snapshot).toBeNull();
  });

  it("connects the stream into the store", () => {
    start();
    expect(connectStream).toHaveBeenCalledOnce();
    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    handlers.onLink("live");
    expect(store()).toMatchObject({ link: "live", stale: false });
    handlers.onLink("restarting");
    expect(store()).toMatchObject({ link: "restarting", stale: true });
  });

  it("sets the badge from the snapshot, sessions and workspaces events only", () => {
    start();
    handlers.onEvent("snapshot", { ...fixtureSnapshot(NOW), badge: 3 });
    expect(setBadge).toHaveBeenLastCalledWith(3);
    handlers.onEvent("sessions", { workspace: 1, sessions: [], badge: 1 });
    expect(setBadge).toHaveBeenLastCalledWith(1);
    handlers.onEvent("workspaces", { workspaces: [], badge: 0 });
    expect(setBadge).toHaveBeenLastCalledWith(0);
    handlers.onEvent("device", {
      name: "x",
      notify: { waiting: true, finished: true, paused_until: "" },
      push: "",
    });
    handlers.onEvent("ping", {});
    expect(setBadge).toHaveBeenCalledTimes(3);
  });

  it("saves at most once a second, the latest state", async () => {
    start();
    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    await vi.advanceTimersByTimeAsync(300);
    handlers.onEvent("sessions", { workspace: 1, sessions: [], badge: 0 });
    await vi.advanceTimersByTimeAsync(300);
    handlers.onEvent("sessions", { workspace: 2, sessions: [], badge: 0 });
    await vi.advanceTimersByTimeAsync(399);
    expect(saveSnapshot).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(saveSnapshot).toHaveBeenCalledOnce();
    expect(saveSnapshot).toHaveBeenLastCalledWith(store().snapshot, NOW + 600);
    await vi.advanceTimersByTimeAsync(200);
    handlers.onEvent("usage", { usage: [] });
    await vi.advanceTimersByTimeAsync(999);
    expect(saveSnapshot).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(1);
    expect(saveSnapshot).toHaveBeenCalledTimes(2);
  });

  it("saves nothing for a ping, a bye or a change of link", async () => {
    start();
    handlers.onEvent("ping", {});
    handlers.onEvent("bye", {});
    handlers.onLink("offline");
    await vi.advanceTimersByTimeAsync(5000);
    expect(saveSnapshot).not.toHaveBeenCalled();
  });

  it("hands each stream notice to the onNotices listeners and keeps it", () => {
    const heard: Notice[][] = [];
    const off = onNotices((notices) => {
      heard.push(notices);
    });
    start();
    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    const n = notice("stash kept: abc123");
    handlers.onEvent("notice", n);
    expect(heard).toEqual([[n]]);
    expect(store().snapshot?.notices?.at(-1)).toEqual(n);
    off();
  });

  it("clears the store, the saved snapshot, the cached queries and the badge, and closes the stream, when unpaired", () => {
    start();
    handlers.onEvent("snapshot", { ...fixtureSnapshot(NOW), badge: 4 });
    queryClient.setQueryData(["turns", 7], { turns: [] });
    emitUnpaired();
    expect(store()).toMatchObject({ snapshot: null, stale: true, updated: 0 });
    expect(clearSnapshot).toHaveBeenCalledOnce();
    expect(closeStream).toHaveBeenCalledOnce();
    expect(queryClient.getQueryData(["turns", 7])).toBeUndefined();
    expect(setBadge).toHaveBeenLastCalledWith(0);
  });

  it("marks the link restarting when a request is answered with the stopping text, until the stream is live", () => {
    start();
    handlers.onLink("live");
    handlers.onLink("offline");
    emitStopping();
    expect(store()).toMatchObject({ link: "restarting", stale: true });
    handlers.onLink("offline");
    expect(store().link).toBe("restarting");
    handlers.onLink("live");
    expect(store().link).toBe("live");
    handlers.onLink("offline");
    expect(store().link).toBe("offline");
  });

  it("says restarting for the grace after a stopping 503, then follows the stream, and a later one restarts the grace", async () => {
    start();
    handlers.onLink("offline");
    emitStopping();
    await vi.advanceTimersByTimeAsync(RESTART_GRACE - 1);
    expect(store().link).toBe("restarting");
    handlers.onLink("offline");
    expect(store().link).toBe("restarting");
    await vi.advanceTimersByTimeAsync(1);
    expect(store().link).toBe("offline");
    handlers.onLink("offline");
    expect(store().link).toBe("offline");
    emitStopping();
    expect(store().link).toBe("restarting");
    await vi.advanceTimersByTimeAsync(RESTART_GRACE - 1);
    emitStopping();
    await vi.advanceTimersByTimeAsync(RESTART_GRACE - 1);
    expect(store().link).toBe("restarting");
    await vi.advanceTimersByTimeAsync(1);
    expect(store().link).toBe("offline");
  });

  it("follows the stream's connecting state once the grace is over", async () => {
    start();
    handlers.onLink("connecting");
    emitStopping();
    await vi.advanceTimersByTimeAsync(RESTART_GRACE);
    expect(store().link).toBe("connecting");
  });

  it("leaves a live link alone when a request is answered with the stopping text", () => {
    start();
    handlers.onLink("live");
    emitStopping();
    expect(store().link).toBe("live");
    handlers.onLink("offline");
    expect(store().link).toBe("offline");
  });

  it("lets a clear cancel the save that was waiting", async () => {
    start();
    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    expect(vi.getTimerCount()).toBe(1);
    await store().clear();
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(5000);
    expect(saveSnapshot).not.toHaveBeenCalled();
  });

  it("writes nothing while the store holds no snapshot", async () => {
    start();
    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    useFleet.setState({ snapshot: null });
    await vi.advanceTimersByTimeAsync(1000);
    expect(saveSnapshot).not.toHaveBeenCalled();
  });

  it("stops the stream, the unpaired listener and the pending save", async () => {
    const stop = start();
    handlers.onEvent("snapshot", fixtureSnapshot(NOW));
    stop();
    expect(closeStream).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(5000);
    expect(saveSnapshot).not.toHaveBeenCalled();
    queryClient.setQueryData(["kept"], 1);
    emitUnpaired();
    expect(clearSnapshot).not.toHaveBeenCalled();
    expect(queryClient.getQueryData(["kept"])).toBe(1);
    useFleet.setState({ link: "offline" });
    emitStopping();
    expect(store().link).toBe("offline");
  });

  it("stops the previous start when started again", () => {
    start();
    start();
    expect(connectStream).toHaveBeenCalledTimes(2);
    expect(closeStream).toHaveBeenCalledOnce();
  });
});

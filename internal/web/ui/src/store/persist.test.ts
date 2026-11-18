import "fake-indexeddb/auto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Snapshot } from "@/api/gen";
import { clearSnapshot, loadSnapshot, saveSnapshot } from "./persist";

const snapshotOf = (build: string) => ({ build, host: "fresco" }) as unknown as Snapshot;

// putRaw writes value under the saved snapshot's key, as an older or foreign build might have.
function putRaw(value: unknown): Promise<void> {
  return new Promise((resolve, reject) => {
    const open = indexedDB.open("loom-next", 1);
    open.onupgradeneeded = () => {
      open.result.createObjectStore("snapshot");
    };
    open.onsuccess = () => {
      const db = open.result;
      const tx = db.transaction("snapshot", "readwrite");
      tx.objectStore("snapshot").put(value, "latest");
      tx.oncomplete = () => {
        db.close();
        resolve();
      };
      tx.onerror = () => {
        reject(tx.error ?? new Error("put failed"));
      };
    };
    open.onerror = () => {
      reject(open.error ?? new Error("open failed"));
    };
  });
}

beforeEach(async () => {
  await clearSnapshot();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

// OP_TIMEOUT is persist.ts's time limit for an open and for a transaction (ms).
const OP_TIMEOUT = 5000;

// settle lets the queue start its next operation.
const settle = () => vi.advanceTimersByTimeAsync(0);

// hangOpen makes the next indexedDB.open never answer (WebKit's first-open hang in a Home Screen
// app), and gives back the request it returned, for a test that answers late. Only the timers the
// limit uses are faked: fake-indexeddb schedules its own work with setImmediate.
function hangOpen() {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  const close = vi.fn();
  const request = { result: { close } } as unknown as IDBOpenDBRequest;
  const open = vi.spyOn(indexedDB, "open").mockImplementationOnce(() => request);
  return { open, request, close };
}

describe("the saved snapshot", () => {
  it("loads as null when nothing was saved", async () => {
    expect(await loadSnapshot()).toBeNull();
  });

  it("loads what was saved, with the time it was last heard from the daemon", async () => {
    const snapshot = snapshotOf("b1");
    saveSnapshot(snapshot, 1234);
    expect(await loadSnapshot()).toEqual({ snapshot, updated: 1234 });
  });

  it("keeps the last of two saves, written in one transaction", async () => {
    const open = vi.spyOn(indexedDB, "open");
    saveSnapshot(snapshotOf("first"), 1);
    saveSnapshot(snapshotOf("second"), 2);
    expect(await loadSnapshot()).toEqual({ snapshot: snapshotOf("second"), updated: 2 });
    expect(open).toHaveBeenCalledTimes(2);
  });

  it("forgets what was saved once cleared", async () => {
    saveSnapshot(snapshotOf("b1"), 1);
    await clearSnapshot();
    expect(await loadSnapshot()).toBeNull();
  });

  it("never lets a save started before a clear land after it", async () => {
    const open = vi.spyOn(indexedDB, "open");
    const close = vi.spyOn(IDBDatabase.prototype, "close");
    saveSnapshot(snapshotOf("b1"), 1);
    await clearSnapshot();
    expect(await loadSnapshot()).toBeNull();
    // The clear opens its connection only once the save's is closed: without the queue both open
    // at once and the save can land last.
    expect(open).toHaveBeenCalledTimes(3);
    expect(open.mock.invocationCallOrder[1]).toBeGreaterThan(
      close.mock.invocationCallOrder[0] ?? Number.POSITIVE_INFINITY,
    );
  });

  it("writes a save asked after a clear behind the clear, not into the save before it", async () => {
    saveSnapshot(snapshotOf("before"), 1);
    const cleared = clearSnapshot();
    saveSnapshot(snapshotOf("after"), 2);
    await cleared;
    expect(await loadSnapshot()).toEqual({ snapshot: snapshotOf("after"), updated: 2 });
  });

  it("loads a record of the wrong shape as null", async () => {
    await putRaw({ build: "classic", host: "fresco" });
    expect(await loadSnapshot()).toBeNull();
  });

  it("never rejects where IndexedDB is missing", async () => {
    vi.stubGlobal("indexedDB", undefined);
    expect(await loadSnapshot()).toBeNull();
    await expect(clearSnapshot()).resolves.toBeUndefined();
    expect(() => {
      saveSnapshot(snapshotOf("b1"), 1);
    }).not.toThrow();
  });
});

describe("an IndexedDB that stops answering", () => {
  it("lets a later clear and load finish when an open never settles", async () => {
    hangOpen();
    saveSnapshot(snapshotOf("lost"), 1);
    const cleared = clearSnapshot();
    const loaded = loadSnapshot();
    await settle();
    await vi.advanceTimersByTimeAsync(OP_TIMEOUT);
    await expect(cleared).resolves.toBeUndefined();
    expect(await loaded).toBeNull();
  });

  it("closes a connection that opens after its time limit", async () => {
    const { request, close } = hangOpen();
    saveSnapshot(snapshotOf("late"), 1);
    await settle();
    await vi.advanceTimersByTimeAsync(OP_TIMEOUT);
    expect(close).not.toHaveBeenCalled();
    request.onsuccess?.(new Event("success"));
    expect(close).toHaveBeenCalledOnce();
  });

  it("lets a later clear and load finish when a transaction never completes", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const transaction = vi
      .spyOn(IDBDatabase.prototype, "transaction")
      .mockImplementationOnce(
        () => ({ objectStore: () => ({ put: () => ({}) }) }) as unknown as IDBTransaction,
      );
    const close = vi.spyOn(IDBDatabase.prototype, "close");
    saveSnapshot(snapshotOf("stuck"), 1);
    const cleared = clearSnapshot();
    const loaded = loadSnapshot();
    // The save's real open finishes on its own clock; its transaction is the one that hangs.
    await vi.waitFor(() => {
      expect(transaction).toHaveBeenCalledOnce();
    });
    await vi.advanceTimersByTimeAsync(OP_TIMEOUT);
    await expect(cleared).resolves.toBeUndefined();
    expect(await loaded).toBeNull();
    expect(close).toHaveBeenCalledTimes(3);
  });

  it("keeps one save waiting behind a stuck queue, the newest", async () => {
    const { open } = hangOpen();
    saveSnapshot(snapshotOf("lost"), 1);
    await settle();
    saveSnapshot(snapshotOf("b"), 2);
    saveSnapshot(snapshotOf("c"), 3);
    saveSnapshot(snapshotOf("d"), 4);
    await vi.advanceTimersByTimeAsync(OP_TIMEOUT);
    expect(await loadSnapshot()).toEqual({ snapshot: snapshotOf("d"), updated: 4 });
    // The open that hung, one write for b, c and d, and the load.
    expect(open).toHaveBeenCalledTimes(3);
  });
});

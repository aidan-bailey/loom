// The phone's copy of the last snapshot, in IndexedDB `loom-next` (spec §24, §6: the classic app
// keeps its own `loom` database, so switching back finds its snapshot untouched). Every access can
// fail (a private window, a full disk, a blocked database) or never answer (WebKit's first open in
// a Home Screen app can hang), and none may break the app, so each is guarded and a failure reads as
// "nothing stored".
import type { Snapshot } from "@/api/gen";

const DB_NAME = "loom-next";
const STORE = "snapshot";
const KEY = "latest";

// How long an open or a transaction may take before it is given up (ms). Every access is queued
// behind the one before it, so one that never settled would stop every later save, load and clear.
const OP_TIMEOUT = 5000;

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (typeof indexedDB === "undefined") {
      reject(new Error("no indexedDB"));
      return;
    }
    const req = indexedDB.open(DB_NAME, 1);
    let settled = false;
    const fail = (err: Error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(err);
    };
    const timer = setTimeout(() => {
      fail(new Error("indexedDB open timed out"));
    }, OP_TIMEOUT);
    req.onupgradeneeded = () => {
      req.result.createObjectStore(STORE);
    };
    req.onsuccess = () => {
      // An open that answers after it was given up has no one waiting for it: close it, or it
      // would hold the database open (and block a version change) for as long as the app runs.
      if (settled) {
        req.result.close();
        return;
      }
      settled = true;
      clearTimeout(timer);
      resolve(req.result);
    };
    req.onerror = () => {
      fail(req.error ?? new Error("indexedDB open failed"));
    };
    req.onblocked = () => {
      fail(new Error("indexedDB is blocked"));
    };
  });
}

// withStore runs one request in a transaction and resolves its result when the transaction
// completes; one that does not complete within OP_TIMEOUT is given up.
async function withStore<T>(
  mode: IDBTransactionMode,
  run: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  const db = await openDB();
  try {
    return await new Promise<T>((resolve, reject) => {
      const tx = db.transaction(STORE, mode);
      const request = run(tx.objectStore(STORE));
      const timer = setTimeout(() => {
        reject(new Error("indexedDB transaction timed out"));
      }, OP_TIMEOUT);
      tx.oncomplete = () => {
        clearTimeout(timer);
        resolve(request.result);
      };
      tx.onerror = () => {
        clearTimeout(timer);
        reject(tx.error ?? new Error("indexedDB transaction failed"));
      };
      tx.onabort = () => {
        clearTimeout(timer);
        reject(tx.error ?? new Error("indexedDB transaction aborted"));
      };
    });
  } finally {
    db.close();
  }
}

// Every access goes through one queue, so they run in the order they were asked: a save that
// started before a clear can never open its connection later and write what the clear removed. An
// access that outlasts OP_TIMEOUT rejects, which moves the queue on.
let tail: Promise<void> = Promise.resolve();

function enqueue<T>(op: () => Promise<T>): Promise<T> {
  const result = tail.then(op);
  tail = result.then(
    () => undefined,
    () => undefined,
  );
  return result;
}

interface Saved {
  snapshot: Snapshot;
  updated: number;
}

function isSaved(value: unknown): value is Saved {
  return (
    typeof value === "object" &&
    value !== null &&
    "snapshot" in value &&
    typeof value.snapshot === "object" &&
    value.snapshot !== null &&
    "updated" in value &&
    typeof value.updated === "number"
  );
}

/** The last saved snapshot and when it was last heard from the daemon (ms), or null. Never rejects. */
export async function loadSnapshot(): Promise<Saved | null> {
  try {
    const record: unknown = await enqueue(() => withStore("readonly", (store) => store.get(KEY)));
    return isSaved(record) ? record : null;
  } catch {
    return null;
  }
}

// The save waiting for its turn in the queue, if there is one. A save asked while one waits
// replaces its snapshot, so a queue held up by a slow access holds one snapshot, not one per event.
// A clear empties the slot (the save stays queued ahead of the clear), so a save asked after the
// clear gets a place behind it and is not folded into the save the clear removes.
let waiting: { saved: Saved } | null = null;

/**
 * Stores the snapshot with the time it was last heard from the daemon, replacing a save that has
 * not started yet. Never throws or rejects.
 */
export function saveSnapshot(snapshot: Snapshot, updated: number): void {
  if (waiting !== null) {
    waiting.saved = { snapshot, updated };
    return;
  }
  const slot = { saved: { snapshot, updated } };
  waiting = slot;
  enqueue(() => {
    if (waiting === slot) waiting = null;
    return withStore("readwrite", (store) => store.put(slot.saved, KEY));
  }).catch(() => undefined);
}

/** Forgets the stored snapshot (on a 401 and on unpairing). Never rejects. */
export async function clearSnapshot(): Promise<void> {
  waiting = null;
  try {
    await enqueue(() => withStore("readwrite", (store) => store.delete(KEY)));
  } catch {
    // nothing stored, or no way to store it
  }
}

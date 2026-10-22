// The phone's own storage: the last snapshot in IndexedDB, small preferences
// in localStorage. Every access can fail (private mode, a full disk, a
// blocked database), and none may break the app, so each is guarded and a
// failure reads as "nothing stored".
const DB_NAME = "loom";
const STORE = "snapshot";
const KEY = "latest";

function openDB() {
  return new Promise((resolve, reject) => {
    if (!("indexedDB" in window)) {
      reject(new Error("no indexedDB"));
      return;
    }
    const req = indexedDB.open(DB_NAME, 1);
    req.onupgradeneeded = () => {
      req.result.createObjectStore(STORE);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
    req.onblocked = () => reject(new Error("indexedDB is blocked"));
  });
}

// withStore runs one request in a transaction and resolves its result when
// the transaction completes.
async function withStore(mode, run) {
  const db = await openDB();
  try {
    return await new Promise((resolve, reject) => {
      const tx = db.transaction(STORE, mode);
      const request = run(tx.objectStore(STORE));
      tx.oncomplete = () => resolve(request.result);
      tx.onerror = () => reject(tx.error);
      tx.onabort = () => reject(tx.error);
    });
  } finally {
    db.close();
  }
}

// loadSnapshot, saveSnapshot and clearSnapshot never reject.
// loadSnapshot resolves the last saved snapshot, or null.
export async function loadSnapshot() {
  try {
    const snap = await withStore("readonly", (store) => store.get(KEY));
    return snap === undefined ? null : snap;
  } catch (err) {
    return null;
  }
}

// saveSnapshot stores the snapshot and resolves whether it worked.
export async function saveSnapshot(snap) {
  try {
    await withStore("readwrite", (store) => store.put(snap, KEY));
    return true;
  } catch (err) {
    return false;
  }
}

// clearSnapshot forgets the stored snapshot (on a 401 and on unpairing).
export async function clearSnapshot() {
  try {
    await withStore("readwrite", (store) => store.delete(KEY));
    return true;
  } catch (err) {
    return false;
  }
}

// pref reads (value undefined) or writes (value given) the localStorage key
// "loom.<key>". Values are stored as JSON, so strings, numbers and booleans
// round-trip; a null value removes the key. A read of a key that is not set
// or cannot be read, and any failure, gives null.
export function pref(key, value) {
  const name = "loom." + key;
  try {
    if (value === undefined) {
      const raw = localStorage.getItem(name);
      return raw === null ? null : JSON.parse(raw);
    }
    if (value === null) {
      localStorage.removeItem(name);
      return null;
    }
    localStorage.setItem(name, JSON.stringify(value));
    return value;
  } catch (err) {
    return null;
  }
}

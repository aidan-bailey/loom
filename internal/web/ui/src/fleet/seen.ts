// The times a session was last opened, for the fleet's NEW mark. The stored value is the classic
// app's double encoding (its `pref` stores JSON text, and the value is itself JSON text), so a
// device that switches apps keeps its times.
import { useEffect, useMemo, useSyncExternalStore } from "react";

import type { Session } from "@/api/gen";

export const SEEN_KEY = "loom.seen";

const listeners = new Set<() => void>();

function notify(): void {
  listeners.forEach((l) => {
    l();
  });
}

function rawSeen(): string | null {
  try {
    return window.localStorage.getItem(SEEN_KEY);
  } catch {
    return null;
  }
}

function parseSeen(raw: string | null): Record<string, number> {
  try {
    const outer: unknown = JSON.parse(raw ?? "null");
    const inner: unknown = JSON.parse(typeof outer === "string" ? outer || "{}" : "{}");
    if (typeof inner !== "object" || inner === null || Array.isArray(inner)) return {};
    const seen: Record<string, number> = {};
    for (const [id, at] of Object.entries(inner)) {
      if (typeof at === "number" && Number.isFinite(at)) seen[id] = at;
    }
    return seen;
  } catch {
    return {};
  }
}

/** The stored times by session id, or {} for nothing stored or anything unusable. */
export function readSeen(): Record<string, number> {
  return parseSeen(rawSeen());
}

function writeSeen(seen: Record<string, number>): void {
  try {
    window.localStorage.setItem(SEEN_KEY, JSON.stringify(JSON.stringify(seen)));
  } catch {
    // Full or blocked storage: the mark is only a convenience.
  }
  notify();
}

/** Records now as the time the session was seen. */
export function markSeen(id: number): void {
  const seen = readSeen();
  seen[String(id)] = Date.now();
  writeSeen(seen);
}

/**
 * Drops the times of every session not in `ids`. An empty list drops nothing: it is more likely a
 * fleet not loaded than a fleet of none.
 */
export function pruneSeen(ids: number[]): void {
  if (ids.length === 0) return;
  const keep = new Set(ids.map(String));
  const seen = readSeen();
  let dropped = false;
  for (const id of Object.keys(seen)) {
    if (!keep.has(id)) {
      delete seen[id];
      dropped = true;
    }
  }
  if (dropped) writeSeen(seen);
}

/** Whether a Your turn session's turn ended after it was last opened (or it never was). */
export function hasNewTurn(session: Session, seen: Record<string, number>): boolean {
  if (session.group !== "your_turn") return false;
  const at = seen[String(session.id)];
  return at === undefined || (Date.parse(session.since) || 0) > at;
}

/** Marks the session seen on mount and again on unmount, and for the old one when `id` changes. */
export function useMarkSeen(id: number | undefined): void {
  useEffect(() => {
    if (id === undefined) return;
    markSeen(id);
    return () => {
      markSeen(id);
    };
  }, [id]);
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  window.addEventListener("storage", onChange);
  return () => {
    listeners.delete(onChange);
    window.removeEventListener("storage", onChange);
  };
}

/** The stored times, read again whenever markSeen or pruneSeen writes and on a storage event. */
export function useSeen(): Record<string, number> {
  const raw = useSyncExternalStore(subscribe, rawSeen);
  return useMemo(() => parseSeen(raw), [raw]);
}

// What a fleet row shows of its account's usage, ported from the classic app's usage.mjs.
import type { Session, Usage, Window } from "@/api/gen";

/** The share at which a fleet row's figure turns amber. */
export const HOT_PCT = 80;

// The usage entry of a session's account: "" is the default account, which the daemon names
// "default".
function usageFor(usage: Usage[], account: string): Usage | undefined {
  const name = account === "" ? "default" : account;
  return usage.find((u) => u.account === name);
}

// The window a row shows: the 5-hour one, else the 7-day one; null for neither.
function shownWindow(entry: Usage): Window | null {
  return entry.five_hour ?? entry.seven_day ?? null;
}

// The window reset already, so its share describes a window that no longer exists. An empty or
// unparsable time has not passed.
function resetPassed(w: Window, now: number): boolean {
  const t = Date.parse(w.resets_at);
  return !Number.isNaN(t) && t <= now;
}

// The sample is at least the daemon's bound old, by this phone's clock.
function isStale(at: string, staleAfterS: number | undefined, now: number): boolean {
  if (staleAfterS === undefined || staleAfterS <= 0) return false;
  const t = Date.parse(at);
  return !Number.isNaN(t) && now - t >= staleAfterS * 1000;
}

/**
 * The share a fleet row shows, or null. Only a Claude session has one: another program records no
 * account, and the entry of "" would read as the default account's. The text is the window's whole
 * percent, or "reset" once it has reset; amber is a raw share of HOT_PCT or more of a window that
 * has not reset, from a sample that is not stale.
 */
export function rowShare(
  session: Session,
  usage: Usage[],
  staleAfterS?: number,
  now: number = Date.now(),
): { text: string; amber: boolean } | null {
  if (!session.claude) return null;
  const entry = usageFor(usage, session.account);
  if (entry === undefined) return null;
  const w = shownWindow(entry);
  if (w === null) return null;
  const reset = resetPassed(w, now);
  return {
    text: reset ? "reset" : `${Math.round(w.pct)}%`,
    amber: !reset && !isStale(entry.at, staleAfterS, now) && w.pct >= HOT_PCT,
  };
}

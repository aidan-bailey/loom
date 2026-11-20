// What a fleet row shows of its account's usage, ported from the classic app's usage.mjs.
import type { Session, Usage, Window } from "@/api/gen";

/** The share at which a fleet row's figure turns amber. */
export const HOT_PCT = 80;

// The usage entry of a session's account: "" is the default account, which the daemon names
// "default".
export function usageFor(usage: Usage[], account: string): Usage | undefined {
  const name = account === "" ? "default" : account;
  return usage.find((u) => u.account === name);
}

/** The window an entry shows, its label, and the other window beside it (null when it has none). */
export interface ShownWindow {
  w: Window;
  label: string;
  other: Window | null;
  otherLabel: string;
}

// The window a row shows: the 5-hour one, else the 7-day one; null for neither. It also names the
// window and the other one, for the info view's Account tile.
export function shownWindow(entry: Usage): ShownWindow | null {
  if (entry.five_hour !== null) {
    return { w: entry.five_hour, label: "5h", other: entry.seven_day, otherLabel: "7d" };
  }
  if (entry.seven_day !== null) {
    return { w: entry.seven_day, label: "7d", other: null, otherLabel: "" };
  }
  return null;
}

// The window reset already, so its share describes a window that no longer exists. An empty or
// unparsable time has not passed.
export function resetPassed(w: Window, now: number): boolean {
  const t = Date.parse(w.resets_at);
  return !Number.isNaN(t) && t <= now;
}

// The sample is at least the daemon's bound old, by this phone's clock.
export function isStale(at: string, staleAfterS: number | undefined, now: number): boolean {
  if (staleAfterS === undefined || staleAfterS <= 0) return false;
  const t = Date.parse(at);
  return !Number.isNaN(t) && now - t >= staleAfterS * 1000;
}

/** A window's share as text: its whole percent, or "reset" once it has reset. */
export function shareText(w: Window, now: number): string {
  return resetPassed(w, now) ? "reset" : `${Math.round(w.pct)}%`;
}

/**
 * When a window resets, as text: "1h40m" or "35m" under a day, else the weekday; "" for a time that
 * does not parse.
 */
export function resetText(iso: string, now: number): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const left = Math.max(0, t - now);
  if (left >= 24 * 3_600_000) return new Date(t).toLocaleDateString([], { weekday: "short" });
  const mins = Math.ceil(left / 60_000);
  const hours = Math.floor(mins / 60);
  return hours > 0 ? `${hours}h${String(mins % 60).padStart(2, "0")}m` : `${mins}m`;
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
  const shown = shownWindow(entry);
  if (shown === null) return null;
  const w = shown.w;
  const reset = resetPassed(w, now);
  return {
    text: shareText(w, now),
    amber: !reset && !isStale(entry.at, staleAfterS, now) && w.pct >= HOT_PCT,
  };
}

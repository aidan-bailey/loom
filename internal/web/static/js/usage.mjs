// Subscription usage as the daemon sends it (the snapshot's `usage` and the
// `usage` event): helpers that pick a session's entry and format a window.
// The fleet's rows and the info tab's Account tile read them.

const HOUR = 3600000;

// usageFor is the usage entry of a session's account: "" is the default
// account, which the daemon names "default".
export function usageFor(snap, account) {
  const name = account || "default";
  return ((snap && snap.usage) || []).find((u) => u.account === name);
}

// cells is how many of the bar's 8 cells a share of 0 to 100 fills.
export function cells(pct) {
  return Math.min(8, Math.max(0, Math.round((Number(pct) || 0) / 12.5)));
}

// resetPassed: the window reset already, so its share describes a window
// that no longer exists.
export function resetPassed(w, now) {
  const t = Date.parse(w && w.resets_at);
  return !Number.isNaN(t) && t <= now;
}

// shareText is a window's share as the app shows it: "62%", or "reset" once
// the window has reset.
export function shareText(w, now) {
  return resetPassed(w, now) ? "reset" : Math.round(Number(w.pct) || 0) + "%";
}

// resetText is when a window resets: "1h40m" or "35m" within a day, else the
// weekday ("Thu"), and "" when the server gave no time.
export function resetText(iso, now) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const left = Math.max(0, t - now);
  if (left < 24 * HOUR) {
    const mins = Math.ceil(left / 60000);
    const hours = Math.floor(mins / 60);
    return hours > 0 ? hours + "h" + String(mins % 60).padStart(2, "0") + "m" : mins + "m";
  }
  return new Date(t).toLocaleDateString([], { weekday: "short" });
}

// isStale: the sample is at least the daemon's bound old (staleAfterS
// seconds, core.UsageStaleAfter), by this phone's clock.
export function isStale(at, staleAfterS, now) {
  const t = Date.parse(at);
  return !Number.isNaN(t) && staleAfterS > 0 && now - t >= staleAfterS * 1000;
}

// planName is a plan with a capital first letter: "max" is "Max".
export function planName(plan) {
  return plan ? plan.charAt(0).toUpperCase() + plan.slice(1) : "";
}

// HOT_PCT is the share at which a fleet row's figure turns amber.
export const HOT_PCT = 80;

// shownWindow is the window a session's share shows, with its label: the 5h
// one, or the 7d one when the account has no 5h; and the other window, if
// any. null for no entry or an entry with neither.
export function shownWindow(u) {
  if (!u) return null;
  if (u.five_hour) return { w: u.five_hour, label: "5h", other: u.seven_day || null, otherLabel: "7d" };
  if (u.seven_day) return { w: u.seven_day, label: "7d", other: null, otherLabel: "" };
  return null;
}

// rowShare is what a fleet row shows of its account's usage: {text, hot}, or
// null. Only a Claude session has one: another program records no account,
// and usageFor would read its "" as the default account's. hot is a share at
// HOT_PCT or more of a window that has not reset, from a sample that is not
// stale.
export function rowShare(snap, s, now) {
  if (!s.claude) return null;
  const u = usageFor(snap, s.account);
  const sw = shownWindow(u);
  if (!sw) return null;
  const stale = isStale(u.at, snap.usage_stale_after_s, now);
  const hot = !stale && !resetPassed(sw.w, now) && (Number(sw.w.pct) || 0) >= HOT_PCT;
  return { text: shareText(sw.w, now), hot };
}

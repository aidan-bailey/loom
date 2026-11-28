// The palette's pure part: no React and no store. The score, the code-point order, and the items
// and their order from a snapshot, the registered commands and a command context.
import type { Session, Snapshot } from "@/api/gen";
import { commandEnabled, type Command, type CommandContext } from "@/app/commands";
import type { PanelId } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { allSessions, groupSessions, hasShownFlows } from "@/fleet/groups";
import { sessionHref } from "@/fleet/links";

/** A tab panel of the current session. */
export interface PaletteTab {
  id: PanelId;
  label: string;
}

export interface PaletteSession {
  kind: "session";
  key: string;
  title: string;
  hint: string;
  href: string;
}

export interface PaletteCommand {
  kind: "command";
  key: string;
  title: string;
  command: Command<unknown>;
  enabled: boolean;
}

export interface PalettePlace {
  kind: "place";
  key: string;
  title: string;
  hint: string;
  href: string;
}

export type PaletteItem = PaletteSession | PaletteCommand | PalettePlace;

export interface PaletteGroup {
  label: "Sessions" | "Commands" | "Places";
  items: PaletteItem[];
}

export interface PaletteInput {
  query: string;
  snapshot: Snapshot | null;
  commands: Command<unknown>[];
  ctx: CommandContext;
  tabs: PaletteTab[];
}

/**
 * Orders two strings by code point: the first difference decides, and a string that is a prefix of
 * the other comes first. Never `localeCompare` (locale order) or `<`, which compares UTF-16 units,
 * where a surrogate (an astral code point) sorts below U+E000 to U+FFFF.
 */
export function compareCodePoints(a: string, b: string): number {
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    const x = a.codePointAt(i) ?? 0;
    const y = b.codePointAt(j) ?? 0;
    if (x !== y) return x - y;
    i += x > 0xffff ? 2 : 1;
    j += y > 0xffff ? 2 : 1;
  }
  if (i < a.length) return 1;
  if (j < b.length) return -1;
  return 0;
}

// startsWord: the code point at i begins a word, at the text's start or after one that is neither
// a letter nor a digit.
function startsWord(points: string[], i: number): boolean {
  if (i === 0) return true;
  const before = points[i - 1] ?? "";
  return !/\p{L}/u.test(before) && !/\p{N}/u.test(before);
}

/**
 * Scores a query against a text, or null when the query's code points do not all occur in the
 * text in order. Both are lowercased and the query trimmed; an empty query scores 0. An alignment
 * starts at a place in the text holding the query's first code point and takes each later one at
 * its next occurrence; each matched code point scores 1, plus 2 when it directly follows the
 * previous match, plus 3 when it starts a word. The result is the best over every start. The work
 * is quadratic only in the text's length, which is a session's title, branch or workspace name, a
 * command's title or a place's title, so no length bound is needed.
 */
export function fuzzyScore(query: string, text: string): number | null {
  const q = [...query.trim().toLowerCase()];
  if (q.length === 0) return 0;
  const t = [...text.toLowerCase()];
  let best: number | null = null;
  for (let start = 0; start < t.length; start++) {
    if (t[start] !== q[0]) continue;
    let score = 0;
    let prev = -2;
    let at = start;
    let ok = true;
    for (const point of q) {
      while (at < t.length && t[at] !== point) at++;
      if (at >= t.length) {
        ok = false;
        break;
      }
      score += 1 + (at === prev + 1 ? 2 : 0) + (startsWord(t, at) ? 3 : 0);
      prev = at;
      at++;
    }
    if (ok && (best === null || score > best)) best = score;
  }
  return best;
}

interface Ranked<T> {
  item: T;
  score: number;
  title: string;
  key: string;
}

// rank orders by score (higher first), then title by code point, then key by code point: the one
// order of all three groups.
function rank<T>(entries: Ranked<T>[]): T[] {
  return [...entries]
    .sort(
      (a, b) =>
        b.score - a.score || compareCodePoints(a.title, b.title) || compareCodePoints(a.key, b.key),
    )
    .map((e) => e.item);
}

function paletteSession(session: Session, workspaceName: string): PaletteSession {
  return {
    kind: "session",
    key: `session:${session.id}`,
    title: session.title,
    hint: [workspaceName, session.branch].filter((part) => part !== "").join(" · "),
    href: sessionHref(session),
  };
}

// sessionItems: with a query, every session (paused and hidden workspaces included) matched on its
// title, branch or workspace name and ranked; with none, the sessions that need you, the longest
// wait first.
function sessionItems(snapshot: Snapshot | null, query: string): PaletteSession[] {
  if (snapshot === null) return [];
  const names = new Map((snapshot.workspaces ?? []).map((w) => [w.id, w.label || w.name] as const));
  const nameOf = (s: Session): string => names.get(s.workspace) ?? "";
  const sessions = allSessions(snapshot);
  if (query === "") {
    return groupSessions(sessions, null).needs_you.map((s) => paletteSession(s, nameOf(s)));
  }
  const entries: Ranked<PaletteSession>[] = [];
  for (const s of sessions) {
    const name = nameOf(s);
    let best: number | null = null;
    for (const text of [s.title, s.branch, name]) {
      const score = fuzzyScore(query, text);
      if (score !== null && (best === null || score > best)) best = score;
    }
    if (best === null) continue;
    const item = paletteSession(s, name);
    entries.push({ item, score: best, title: item.title, key: item.key });
  }
  return rank(entries);
}

// commandItems: the commands that carry the palette mark. With a query, those whose title matches,
// ranked, a command whose `when` fails among them (drawn disabled); with none, those whose `when`
// holds (a stale snapshot still lists them, dimmed), by title by code point.
function commandItems(input: PaletteInput, query: string): PaletteCommand[] {
  const entries: Ranked<PaletteCommand>[] = [];
  for (const command of input.commands) {
    if (command.palette !== true) continue;
    const title = typeof command.title === "function" ? command.title(input.ctx) : command.title;
    let score = 0;
    if (query === "") {
      if (!command.when(input.ctx)) continue;
    } else {
      const matched = fuzzyScore(query, title);
      if (matched === null) continue;
      score = matched;
    }
    const item: PaletteCommand = {
      kind: "command",
      key: `command:${command.id}`,
      title,
      command,
      enabled: commandEnabled(command, input.ctx),
    };
    entries.push({ item, score, title, key: item.key });
  }
  return rank(entries);
}

// placeItems: the current session's tabs, then Fleet, New session, Flows (only when a shown
// workspace's session has a flow) and Settings. With a query, those whose title (never the hint)
// matches, ranked; with none, every one in that order.
function placeItems(input: PaletteInput, query: string): PalettePlace[] {
  const { session } = input.ctx;
  const places: PalettePlace[] = [];
  if (session !== undefined) {
    for (const tab of input.tabs) {
      places.push({
        kind: "place",
        key: `place:${tab.id}`,
        title: tab.label,
        hint: session.title,
        href: hrefFor(tab.id, { id: String(session.id) }),
      });
    }
  }
  const plain = (id: PanelId, title: string): PalettePlace => ({
    kind: "place",
    key: `place:${id}`,
    title,
    hint: "",
    href: hrefFor(id),
  });
  places.push(plain("fleet", "Fleet"), plain("new", "New session"));
  if (input.snapshot !== null && hasShownFlows(input.snapshot))
    places.push(plain("flowBoard", "Flow board"), plain("flows", "Flows"));
  places.push(plain("settings", "Settings"));
  if (query === "") return places;
  const entries: Ranked<PalettePlace>[] = [];
  for (const place of places) {
    const score = fuzzyScore(query, place.title);
    if (score !== null) entries.push({ item: place, score, title: place.title, key: place.key });
  }
  return rank(entries);
}

/**
 * The palette's groups, in the order Sessions, Commands, Places, a group with no items left out.
 * With an empty query the Sessions group lists the sessions that need you, the longest wait first,
 * the Commands group the commands whose `when` holds, and the Places group every place.
 */
export function paletteGroups(input: PaletteInput): PaletteGroup[] {
  const query = input.query.trim();
  const groups: PaletteGroup[] = [];
  const sessions = sessionItems(input.snapshot, query);
  if (sessions.length > 0) groups.push({ label: "Sessions", items: sessions });
  const commands = commandItems(input, query);
  if (commands.length > 0) groups.push({ label: "Commands", items: commands });
  const places = placeItems(input, query);
  if (places.length > 0) groups.push({ label: "Places", items: places });
  return groups;
}

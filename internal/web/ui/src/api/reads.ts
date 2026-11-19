// The reads of the session views (spec §26, Part 2 settlements 11 and 15): a conversation's or a
// subagent's turn pages, a session's diff and a job's log, each held in the query cache and read
// again by the classic app's rules (internal/web/static/js/turns.mjs, changes.mjs and job.mjs). A
// view reads through these hooks and never fetches on its own: one request is in flight per key,
// and a read asked for meanwhile runs once when it lands.
import type { AgentPage, JobLog, Turn, Turns } from "./gen";

/** How many conversations keep their pages and scroll: a ninth evicts the least recently opened. */
export const KEEP_SESSIONS = 8;
/** How many jobs keep their log: a sixth evicts the least recently opened. */
export const KEEP_JOBS = 5;
/** The most characters (UTF-16 units, as classic counts them) of one job's log that are held. */
export const MAX_LOG_CHARS = 524288;

/** Which page a turn read asks for: the newest, the newest turn refreshed, or the one before. */
export type TurnsLoad = "newest" | "refresh" | "earlier";

/** The turns held for a conversation or a subagent, oldest first. */
export interface HeldTurns {
  conversation: string;
  turns: Turn[];
  /** The `before` of the page ahead of the first held turn; null when there is none. */
  earlier: number | null;
  /** The most records any page reported unreadable: once set it stays set. */
  skipped: number;
  /** The subagent the latest page described; undefined on a conversation. */
  agent: AgentPage | undefined;
}

/**
 * Folds a page into the turns held, as the classic `mergeTurns` does: the first page, a newest
 * load and a reset replace them, an earlier page goes before them, and a refresh replaces every
 * held turn from its own first turn on.
 */
export function mergeTurns(held: HeldTurns | undefined, page: Turns, load: TurnsLoad): HeldTurns {
  const skipped = Math.max(held?.skipped ?? 0, page.skipped);
  const turns = page.turns ?? [];
  const agent = page.agent;
  if (held === undefined || page.reset || load === "newest") {
    return {
      conversation: page.conversation,
      turns,
      earlier: page.earlier || null,
      skipped,
      agent,
    };
  }
  if (load === "earlier") {
    const first = held.turns.length > 0 ? held.turns[0].id : Infinity;
    return {
      ...held,
      turns: turns.filter((t) => t.id < first).concat(held.turns),
      earlier: page.earlier || null,
      skipped,
      agent,
    };
  }
  if (turns.length === 0) return { ...held, skipped, agent };
  const from = turns[0].id;
  return { ...held, turns: held.turns.filter((t) => t.id < from).concat(turns), skipped, agent };
}

/** A job's log as held: its text, the offset the next read continues from, and whether it is cut. */
export interface HeldLog {
  text: string;
  end: number;
  cut: boolean;
}

/**
 * Adds a log page to the log held, as the classic `appended` does: a reset page, or no log yet,
 * replaces it; the text is then trimmed to `MAX_LOG_CHARS` at the next newline, and marked cut.
 */
export function appendLog(held: HeldLog | undefined, page: JobLog): HeldLog {
  let next: HeldLog =
    held === undefined || page.reset
      ? { text: page.text, end: page.end, cut: page.cut }
      : { text: held.text + page.text, end: page.end, cut: held.cut };
  if (next.text.length > MAX_LOG_CHARS) {
    const from = next.text.length - MAX_LOG_CHARS;
    const newline = next.text.indexOf("\n", from);
    next = { ...next, text: next.text.slice(newline >= 0 ? newline + 1 : from), cut: true };
  }
  return next;
}

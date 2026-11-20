// The reads of the session views (spec §26, Part 2 settlements 11 and 15): a conversation's or a
// subagent's turn pages, a session's diff and a job's log, each held in the query cache and read
// again by the classic app's rules (internal/web/static/js/turns.mjs, changes.mjs and job.mjs). A
// view reads through these hooks and never fetches on its own: one request is in flight per key,
// and a read asked for meanwhile runs once when it lands.
import { useQuery, type QueryKey } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";
import type {
  AgentPage,
  Flow,
  FlowDoc,
  Flows,
  JobLog,
  LaunchDefaults,
  Patch,
  Session,
  Turn,
  Turns,
  TurnsParams,
} from "./gen";
import { OutcomeError, queryClient, unwrap } from "./queries";
import { api } from "./request";

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

// The query cache's own default for a query nobody reads for a while (5 minutes), which a diff
// keeps: only turn pages and job logs are kept longer.
const DEFAULT_GC = 5 * 60 * 1000;

// How often a live conversation is refreshed, as the classic app does.
const TURNS_POLL = 5000;

// again holds the reads asked for while one of the same key was in flight: each runs once when
// that one lands. A key is its query key's JSON.
const again = new Set<string>();

function hashOf(key: QueryKey): string {
  return JSON.stringify(key);
}

function inFlight(key: QueryKey): boolean {
  return queryClient.getQueryState(key)?.fetchStatus === "fetching";
}

// startRead runs read as the query of key. A read is never retried here: the next poll, or the
// view's own retry, is the next attempt, as classic. The promise's failure is the query's error.
function startRead<T>(key: QueryKey, read: () => Promise<T>, gcTime: number): void {
  void queryClient
    .fetchQuery<T>({ queryKey: key, queryFn: read, gcTime, retry: false })
    .catch(() => undefined);
}

// askedAgain runs rerun once, after the read that was in flight has landed, when a read of key
// was asked for meanwhile. The zero delay lets that read's result reach the cache first. The
// rerun does nothing when the query is gone (an unpair cleared it, keepRecent evicted it) or no
// component reads it any more: it would read for nobody, or create a query outside the kept
// lists.
function askedAgain(key: QueryKey, rerun: () => void): void {
  if (!again.delete(hashOf(key))) return;
  setTimeout(() => {
    const query = queryClient.getQueryCache().find({ queryKey: key, exact: true });
    if (query !== undefined && query.getObserversCount() > 0) rerun();
  }, 0);
}

function errorText(error: Error | null, fallback: string): string {
  if (error === null) return "";
  return error.message !== "" ? error.message : fallback;
}

// keepRecent notes key as the most recently opened of order, dropping the keys whose query has
// left the cache (an unpair clears it), and removes the queries of the oldest past limit. A query
// a view still observes is not removed: it stays in the order and goes once it is closed and
// another is opened. It returns the new order.
function keepRecent(order: QueryKey[], key: QueryKey, limit: number): QueryKey[] {
  const hash = hashOf(key);
  const cache = queryClient.getQueryCache();
  const next = order.filter((k) => hashOf(k) !== hash && cache.find({ queryKey: k }) !== undefined);
  next.push(key);
  const idle = (k: QueryKey): boolean =>
    (cache.find({ queryKey: k, exact: true })?.getObserversCount() ?? 0) === 0;
  while (next.length > limit) {
    const at = next.findIndex((k) => hashOf(k) !== hash && idle(k));
    if (at < 0) break;
    for (const oldest of next.splice(at, 1)) {
      queryClient.removeQueries({ queryKey: oldest, exact: true });
    }
  }
  return next;
}

// What the cache holds for a conversation or a subagent: the pages, and the kind of load whose
// page produced them, written together.
interface TurnsEntry {
  held: HeldTurns;
  landed: TurnsLoad;
}

const turnsKey = (session: number, agent: string | undefined): QueryKey => [
  "turns",
  session,
  agent ?? "",
];

// A conversation's pages stay in the cache until KEEP_SESSIONS evicts them or an unpair clears
// it; a subagent's leave with their last reader, so a reopened subagent view loads the newest.
const turnsRetention = (agent: string | undefined): number => (agent === undefined ? Infinity : 0);

// asked holds the kind of the load a `load` call asked for, for the read that runs next; a read
// nobody asked for (a mount, a poll) is a refresh of what is held, or the newest.
const asked = new Map<string, TurnsLoad>();

async function fetchTurns(session: number, agent: string | undefined): Promise<TurnsEntry> {
  const key = turnsKey(session, agent);
  const entry = queryClient.getQueryData<TurnsEntry>(key);
  const held = entry?.held;
  let kind = asked.get(hashOf(key)) ?? "refresh";
  asked.delete(hashOf(key));
  if (held === undefined || (kind === "refresh" && held.turns.length === 0)) kind = "newest";
  const query: Partial<TurnsParams> = {};
  if (agent !== undefined) query.agent = agent;
  if (held !== undefined) {
    query.conversation = held.conversation;
    if (kind === "earlier" && held.earlier !== null) query.before = String(held.earlier);
    if (kind === "refresh") query.after = String(held.turns[held.turns.length - 1].id);
  }
  try {
    const page = unwrap(await api.turns(session, query));
    return { held: mergeTurns(held, page, kind), landed: page.reset ? "newest" : kind };
  } finally {
    askedAgain(key, () => requestTurns(session, agent, "refresh"));
  }
}

// requestTurns asks for a load of the pages. One read is in flight at a time: a refresh asked
// meanwhile runs once when it lands, and any other kind asked meanwhile is dropped, as classic.
// An earlier page is not asked for when none is left.
function requestTurns(session: number, agent: string | undefined, kind: TurnsLoad): void {
  const key = turnsKey(session, agent);
  if (inFlight(key)) {
    if (kind === "refresh") again.add(hashOf(key));
    return;
  }
  const held = queryClient.getQueryData<TurnsEntry>(key)?.held;
  if (kind === "earlier" && (held === undefined || held.earlier === null)) return;
  asked.set(hashOf(key), kind);
  startRead(key, () => fetchTurns(session, agent), turnsRetention(agent));
}

// The conversations kept, least recently opened first, and the page scroll kept with each one's
// query: a scroll lives and dies with its query object, so evicting or clearing the pages drops
// it, and a conversation read again after a clear starts with none.
let keptSessions: QueryKey[] = [];
const scrolls = new WeakMap<object, number>();

function openConversation(session: number): void {
  keptSessions = keepRecent(keptSessions, turnsKey(session, undefined), KEEP_SESSIONS);
}

/** The page scroll kept with a conversation's pages, undefined when none is kept. */
export function keptScroll(session: number): number | undefined {
  const query = queryClient.getQueryCache().find({ queryKey: turnsKey(session, undefined) });
  return query === undefined ? undefined : scrolls.get(query);
}

/** Keeps the page scroll with a conversation's pages; nothing is kept while no pages are held. */
export function keepScroll(session: number, y: number): void {
  const query = queryClient.getQueryCache().find({ queryKey: turnsKey(session, undefined) });
  if (query !== undefined && query.state.data !== undefined) scrolls.set(query, y);
}

export interface TurnPagesOptions {
  session: number;
  /** A subagent's id: its turns are read in place of the conversation's. */
  agent: string | undefined;
  /** The session works: its newest turn is refreshed every 5 s. */
  live: boolean;
}

export interface TurnPages {
  held: HeldTurns | undefined;
  /** The kind of the load whose page produced `held`; a reset page counts as "newest". */
  landed: TurnsLoad | undefined;
  /** Why the last read failed, "" for none; the pages held stay. */
  error: string;
  load: (kind: TurnsLoad) => void;
}

/**
 * Reads a conversation's pages, or a subagent's when `agent` is set: on mount a refresh when
 * pages are held, else the newest page; a refresh every 5 s while `live`; `load` asks for an
 * earlier page or a refresh. Components with the same session and agent share one held state.
 */
export function useTurnPages({ session, agent, live }: TurnPagesOptions): TurnPages {
  const query = useQuery<TurnsEntry>({
    queryKey: turnsKey(session, agent),
    queryFn: () => fetchTurns(session, agent),
    gcTime: turnsRetention(agent),
    retry: false,
    refetchInterval: live ? TURNS_POLL : false,
  });
  useEffect(() => {
    if (agent === undefined) openConversation(session);
  }, [session, agent]);
  const load = useCallback(
    (kind: TurnsLoad) => {
      requestTurns(session, agent, kind);
    },
    [session, agent],
  );
  return {
    held: query.data?.held,
    landed: query.data?.landed,
    error: errorText(query.error, "Could not read the conversation."),
    load,
  };
}

/**
 * The pages held for a conversation or a subagent, read without fetching them. It reads the
 * cache directly rather than through a query observer: an observer writes its own options
 * (`queryFn`, `retry`) onto the shared query on every render, and a later refetch with no options
 * of its own would then fail with "Missing queryFn". No turns key is refetched without options.
 */
export function useHeldTurns(session: number, agent: string | undefined): HeldTurns | undefined {
  return useSyncExternalStore(
    (onChange) => queryClient.getQueryCache().subscribe(onChange),
    () => queryClient.getQueryData<TurnsEntry>(turnsKey(session, agent))?.held,
  );
}

// How often a live session's diff is read again, as the classic app does.
const DIFF_POLL = 10000;

const diffKey = (session: number): QueryKey => ["diff", session];

async function readDiff(session: number): Promise<Patch> {
  try {
    return unwrap(await api.diff(session));
  } finally {
    askedAgain(diffKey(session), () => reloadDiff(session));
  }
}

// reloadDiff reads the diff: one read is in flight at a time, and a read asked for meanwhile
// runs once when it lands.
function reloadDiff(session: number): void {
  const key = diffKey(session);
  if (inFlight(key)) {
    again.add(hashOf(key));
    return;
  }
  startRead(key, () => readDiff(session), DEFAULT_GC);
}

export interface PatchOptions {
  session: number;
  /** What the caller reads the diff's change from (the session's status and line counts). */
  signature: string;
  /** The session works: the diff is read every 10 s. */
  live: boolean;
}

export interface PatchView {
  patch: Patch | undefined;
  /** Why the last read failed, "" for none; the patch held stays. */
  error: string;
  /** Reads the diff now. */
  reload: () => void;
}

/**
 * Reads a session's diff on mount, again when `signature` changes once a patch is held, and every
 * 10 s while `live`.
 */
export function usePatch({ session, signature, live }: PatchOptions): PatchView {
  const query = useQuery<Patch>({
    queryKey: diffKey(session),
    queryFn: () => readDiff(session),
    retry: false,
    refetchInterval: live ? DIFF_POLL : false,
  });
  // seen follows the key: a change of session starts from the new session's own signature, and a
  // mount is no change, so the observer's own read is the only one either asks for.
  const seen = useRef({ session, signature });
  useEffect(() => {
    const was = seen.current;
    seen.current = { session, signature };
    if (was.session !== session || was.signature === signature) return;
    if (queryClient.getQueryData(diffKey(session)) !== undefined) reloadDiff(session);
  }, [session, signature]);
  const reload = useCallback(() => {
    reloadDiff(session);
  }, [session]);
  return {
    patch: query.data,
    error: errorText(query.error, "Could not read the diff."),
    reload,
  };
}

// How often a live job's log is read again, as the classic app does.
const LOG_POLL = 2000;

const logKey = (session: number, job: number): QueryKey => ["job-log", session, job];

// The jobs kept, least recently opened first.
let keptJobs: QueryKey[] = [];

function openJob(session: number, job: number): void {
  keptJobs = keepRecent(keptJobs, logKey(session, job), KEEP_JOBS);
}

// goneText is the text of a 409 (the job has left the queue), "" for any other error.
function goneText(error: Error | null | undefined): string {
  if (!(error instanceof OutcomeError)) return "";
  if (error.outcome.kind !== "refused" || error.outcome.status !== 409) return "";
  return error.message !== "" ? error.message : "This job is no longer in the queue.";
}

// readLog reads what the log holds past the end held, or its tail when none is held, and appends
// it. A held log is always read `after`: a tail answer never says it is a reset, so a tail
// appended to a held log would repeat it.
async function readLog(session: number, job: number): Promise<HeldLog> {
  const key = logKey(session, job);
  try {
    const held = queryClient.getQueryData<HeldLog>(key);
    const page = unwrap(
      await api.jobLog(session, job, held === undefined ? undefined : { after: String(held.end) }),
    );
    return appendLog(held, page);
  } finally {
    askedAgain(key, () => requestLog(session, job));
  }
}

// requestLog reads the log now: one read is in flight at a time, and a read asked for meanwhile
// runs once when it lands.
function requestLog(session: number, job: number): void {
  const key = logKey(session, job);
  if (inFlight(key)) {
    again.add(hashOf(key));
    return;
  }
  startRead(key, () => readLog(session, job), Infinity);
}

export interface JobLogOptions {
  session: number;
  job: number;
  /** The job has yet to end: its log is read every 2 s, and once more when this turns false. */
  live: boolean;
}

export interface JobLogView {
  log: HeldLog | undefined;
  /** The text of a 409: the job has left the queue and nothing more is read; "" for none. */
  gone: string;
  /** Why the last read failed, other than a 409, "" for none; the log held stays. */
  error: string;
}

/**
 * Reads a job's log: on mount the kept log drawn first and a read after its end (or the tail when
 * none is kept), then after the end every 2 s while `live` and not gone, and once more when
 * `live` turns false. A 409 sets `gone` and stops; a timeout or a network failure waits for the
 * next tick. A job's log stays in the cache until KEEP_JOBS evicts it or an unpair clears it.
 */
export function useJobLog({ session, job, live }: JobLogOptions): JobLogView {
  const query = useQuery<HeldLog>({
    queryKey: logKey(session, job),
    queryFn: () => readLog(session, job),
    gcTime: Infinity,
    retry: false,
    refetchInterval: (q) => (live && goneText(q.state.error) === "" ? LOG_POLL : false),
  });
  useEffect(() => {
    openJob(session, job);
  }, [session, job]);
  // was follows the key: a change of job is no end of the old one, so only the observer's own
  // read is made for the new key.
  const was = useRef({ session, job, live });
  useEffect(() => {
    const before = was.current;
    was.current = { session, job, live };
    const ended = before.session === session && before.job === job && before.live && !live;
    if (ended && goneText(queryClient.getQueryState(logKey(session, job))?.error) === "") {
      requestLog(session, job);
    }
  }, [live, session, job]);
  const gone = goneText(query.error);
  return {
    log: query.data,
    gone,
    error: gone === "" ? errorText(query.error, "Could not read the job's log.") : "",
  };
}

// The reads of part 3's views (spec §26, Part 3 settlements 2 and 7): the flow board and
// document, the host's flows and launch defaults, each read by the classic app's triggers
// (internal/web/static/js/flow.mjs, flowdoc.mjs, flows.mjs and newsession.mjs) and none polled.

/** How many boards keep their place: a ninth evicts the least recently opened. */
export const KEEP_FLOWS = 8;

const flowQueryKey = (session: number, plan: string): QueryKey => ["flow", session, plan];

// The boards kept, least recently opened first.
let keptFlows: QueryKey[] = [];

function openFlow(session: number, plan: string): void {
  keptFlows = keepRecent(keptFlows, flowQueryKey(session, plan), KEEP_FLOWS);
}

/** What a board is read again on: the JSON of the session's flow summaries, as classic keys it. */
export function flowKey(session: Session): string {
  return JSON.stringify(session.flows);
}

async function readFlow(session: number, plan: string): Promise<Flow> {
  try {
    return unwrap(await api.flow(session, { plan }));
  } finally {
    askedAgain(flowQueryKey(session, plan), () => reloadFlow(session, plan));
  }
}

// reloadFlow reads the board: one read is in flight at a time, and a read asked for meanwhile
// runs once when it lands.
function reloadFlow(session: number, plan: string): void {
  const key = flowQueryKey(session, plan);
  if (inFlight(key)) {
    again.add(hashOf(key));
    return;
  }
  startRead(key, () => readFlow(session, plan), Infinity);
}

export interface FlowOptions {
  session: number;
  /** The flow's plan path; empty reads nothing. */
  plan: string;
  /** What `flowKey` answers for the session: a new one reads a held board again. */
  key: string;
}

export interface FlowView {
  flow: Flow | undefined;
  /** Why the last read failed, "" for none; the board held stays. */
  error: string;
  /** No board is held and the read has neither landed nor failed. */
  loading: boolean;
}

/**
 * Reads a session's flow board on mount (a held board drawn first), and again whenever `key`
 * changes (a change asked while a read is out runs once when it lands). Boards are held per session and plan, the
 * last KEEP_FLOWS opened, and the query key is the session and plan only, so a re-priced run
 * (a new `key`) never adds an entry. A plan that is not the one drawn never shows its board.
 */
export function useFlow({ session, plan, key }: FlowOptions): FlowView {
  const query = useQuery<Flow>({
    queryKey: flowQueryKey(session, plan),
    queryFn: () => readFlow(session, plan),
    enabled: plan !== "",
    // A board is held past its observers; the disabled entry of an empty plan holds nothing and
    // leaves with its observer.
    gcTime: plan !== "" ? Infinity : 0,
    retry: false,
  });
  useEffect(() => {
    if (plan !== "") openFlow(session, plan);
  }, [session, plan]);
  // seen follows the options: a change of session or plan is a new query whose own observer
  // reads it, and a mount is no change, so only a new key asks for a read of its own. A key
  // that changes while the first read is out is asked all the same: reloadFlow queues it, and it
  // runs once when that read lands, so a board opened mid-run is never left one step stale.
  const seen = useRef({ session, plan, key });
  useEffect(() => {
    const was = seen.current;
    seen.current = { session, plan, key };
    if (plan === "" || was.session !== session || was.plan !== plan || was.key === key) return;
    if (queryClient.getQueryState(flowQueryKey(session, plan)) !== undefined) {
      reloadFlow(session, plan);
    }
  }, [session, plan, key]);
  return {
    flow: query.data,
    error: errorText(query.error, "Could not read the flow."),
    loading: plan !== "" && query.data === undefined && query.error === null,
  };
}

export interface FlowDocOptions {
  session: number;
  /** The flow's plan path; empty reads nothing. */
  plan: string;
  /** The document's name (`spec`, `plan`, `report:<id>`); empty reads nothing. */
  doc: string;
}

export interface FlowDocView {
  doc: FlowDoc | undefined;
  /** Why the read failed, "" for none. */
  error: string;
  /** The read has neither landed nor failed. */
  loading: boolean;
}

/**
 * Reads a flow document on mount and whenever the session, plan or document changes, never
 * polled. The query is keyed by all three and leaves the cache with its last reader, so a view
 * draws the document it asked for or loading, never the previous one.
 */
export function useFlowDoc({ session, plan, doc }: FlowDocOptions): FlowDocView {
  const enabled = plan !== "" && doc !== "";
  const query = useQuery<FlowDoc>({
    queryKey: ["flow-doc", session, plan, doc],
    queryFn: async () => unwrap(await api.flowDoc(session, { plan, doc })),
    enabled,
    gcTime: 0,
    retry: false,
  });
  return {
    doc: query.data,
    error: errorText(query.error, "Could not read the document."),
    loading: enabled && query.data === undefined && query.error === null,
  };
}

const flowsKey: QueryKey = ["flows"];

// How many components read the host's flows now. The query's observers only watch (they are
// disabled): the first caller to mount asks for the read, and a caller that mounts while another
// is mounted (FlowsHead beside FlowsBody) sends none, so a screen with two callers reads once.
let flowsReaders = 0;

async function readFlows(): Promise<Flows> {
  try {
    return unwrap(await api.flows());
  } finally {
    askedAgain(flowsKey, reloadFlows);
  }
}

// reloadFlows reads the host's flows: one read is in flight at a time, and a read asked for
// meanwhile runs once when it lands.
function reloadFlows(): void {
  if (inFlight(flowsKey)) {
    again.add(hashOf(flowsKey));
    return;
  }
  startRead(flowsKey, readFlows, DEFAULT_GC);
}

export interface FlowsView {
  flows: Flows | undefined;
  /** Why the last read failed, "" for none; the answer held stays. */
  error: string;
  /** No answer is held and the read has neither landed nor failed. */
  loading: boolean;
  /** Reads the host's flows again, for every caller. */
  reload: () => void;
}

/**
 * Reads the host's flows when the first caller mounts and on `reload`, never polled. Every
 * caller shares one query, so FlowsHead's reload redraws FlowsBody, and the answer of the last
 * visit, when it is within the 5 minutes the cache keeps it, is drawn at once while the next
 * read runs.
 */
export function useFlows(): FlowsView {
  const query = useQuery<Flows>({
    queryKey: flowsKey,
    queryFn: readFlows,
    // The observer only watches: every read goes through startRead, which is never retried.
    enabled: false,
  });
  useEffect(() => {
    const first = flowsReaders === 0;
    flowsReaders++;
    if (first && !inFlight(flowsKey)) reloadFlows();
    return () => {
      flowsReaders--;
    };
  }, []);
  return {
    flows: query.data,
    error: errorText(query.error, "Could not read the host's flows."),
    loading: query.data === undefined && query.error === null,
    reload: reloadFlows,
  };
}

const defaultsKey = (workspace: number | undefined): QueryKey => [
  "launch-defaults",
  workspace ?? null,
];

async function readDefaults(workspace: number): Promise<LaunchDefaults> {
  return unwrap(await api.launchDefaults({ workspace: String(workspace) }));
}

export interface LaunchDefaultsView {
  defaults: LaunchDefaults | undefined;
  /** Why the read failed, "" for none. */
  error: string;
  /** A workspace is given and its defaults have neither landed nor failed (the stream may be down). */
  loading: boolean;
  /** Reads the defaults again after a failure; startRead joins a read already out rather than repeating it. */
  retry: () => void;
}

/**
 * Reads a workspace's launch defaults once the stream is `live`, and holds the answer for the
 * page's lifetime: it is never read again on its own, so a stream that comes back or a remount
 * keeps the picks made on it (`retry` reads again after a failure). A failed read is read again
 * by the observer only when the stream comes back or the screen is opened again. No workspace
 * reads nothing.
 */
export function useLaunchDefaults(
  workspace: number | undefined,
  live: boolean,
): LaunchDefaultsView {
  const query = useQuery<LaunchDefaults>({
    queryKey: defaultsKey(workspace),
    queryFn: async () => {
      if (workspace === undefined) throw new Error("no workspace to read the defaults of");
      return readDefaults(workspace);
    },
    enabled: workspace !== undefined && live,
    staleTime: Infinity,
    // An answer is held for the page's lifetime; the disabled entry of no workspace holds nothing
    // and leaves with its observer.
    gcTime: workspace !== undefined ? Infinity : 0,
    retry: false,
  });
  const retry = useCallback(() => {
    if (workspace === undefined) return;
    startRead(defaultsKey(workspace), () => readDefaults(workspace), Infinity);
  }, [workspace]);
  return {
    defaults: query.data,
    error: errorText(query.error, "Could not load the launch defaults."),
    loading: workspace !== undefined && query.data === undefined && query.error === null,
    retry,
  };
}

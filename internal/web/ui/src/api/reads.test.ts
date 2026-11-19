import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { createElement, type ReactElement, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fixtureJobLog, fixturePatch, fixtureTurns } from "@/fixtures";
import type { AgentPage, JobLog, Outcome, Patch, Turn, Turns } from "./gen";
import { queryClient } from "./queries";
import {
  appendLog,
  keepScroll,
  keptScroll,
  KEEP_JOBS,
  KEEP_SESSIONS,
  mergeTurns,
  MAX_LOG_CHARS,
  useHeldTurns,
  useJobLog,
  usePatch,
  useTurnPages,
  type HeldLog,
  type HeldTurns,
  type JobLogOptions,
  type PatchOptions,
  type TurnPagesOptions,
  type TurnsLoad,
} from "./reads";
import { api } from "./request";

// Timers are faked so a poll's cadence is exact: every wait is a settle, which also lets the
// query client's own zero-delay notifications run.
const spyOnTurns = () =>
  vi.spyOn(api, "turns").mockRejectedValue(new Error("unexpected turns read"));
const spyOnDiff = () => vi.spyOn(api, "diff").mockRejectedValue(new Error("unexpected diff read"));
let turnsRead: ReturnType<typeof spyOnTurns>;
const spyOnLog = () =>
  vi.spyOn(api, "jobLog").mockRejectedValue(new Error("unexpected job log read"));
let diffRead: ReturnType<typeof spyOnDiff>;
let logRead: ReturnType<typeof spyOnLog>;

beforeEach(() => {
  vi.useFakeTimers();
  queryClient.clear();
  turnsRead = spyOnTurns();
  diffRead = spyOnDiff();
  logRead = spyOnLog();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function settle(ms = 1): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

function wrapper({ children }: { children: ReactNode }): ReactElement {
  return createElement(QueryClientProvider, { client: queryClient }, children);
}

const ok = <T>(value: T): Outcome<T> => ({ kind: "ok", status: 200, value, notices: [] });
const refused = (status: number, message: string): Outcome<never> => ({
  kind: "refused",
  status,
  message,
});

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function turn(id: number): Turn {
  return {
    id,
    at: "2026-11-19T10:00:00Z",
    from: "you",
    name: "",
    text: `turn ${id}`,
    cut: false,
    command: false,
    partial: false,
    items: null,
  };
}

function page(ids: number[], extra: Partial<Turns> = {}): Turns {
  return {
    conversation: "c1",
    turns: ids.map(turn),
    earlier: null,
    reset: false,
    skipped: 0,
    ...extra,
  };
}

function heldOf(ids: number[], extra: Partial<HeldTurns> = {}): HeldTurns {
  return {
    conversation: "c1",
    turns: ids.map(turn),
    earlier: null,
    skipped: 0,
    agent: undefined,
    ...extra,
  };
}

const idsOf = (held: HeldTurns): number[] => held.turns.map((t) => t.id);

function logPage(text: string, end: number, extra: Partial<JobLog> = {}): JobLog {
  return { text, start: 0, end, cut: false, reset: false, ...extra };
}

describe("mergeTurns", () => {
  it.each<{ when: string; held: HeldTurns | undefined; next: Turns; load: TurnsLoad }>([
    {
      when: "no pages are held",
      held: undefined,
      next: page([5, 6], { earlier: 4 }),
      load: "refresh",
    },
    {
      when: "the load is newest",
      held: heldOf([1, 2]),
      next: page([5, 6], { earlier: 4 }),
      load: "newest",
    },
    {
      when: "the page is a reset",
      held: heldOf([1, 2]),
      next: page([5, 6], { earlier: 4, reset: true }),
      load: "refresh",
    },
  ])("replaces what is held when $when", ({ held, next, load }) => {
    const merged = mergeTurns(held, next, load);
    expect(idsOf(merged)).toEqual([5, 6]);
    expect(merged.conversation).toBe("c1");
    expect(merged.earlier).toBe(4);
  });

  it("puts an earlier page before the held turns, dropping a turn the held ones start with", () => {
    const held = heldOf([4, 5, 6], { earlier: 4 });
    const merged = mergeTurns(held, page([2, 3, 4], { earlier: 2 }), "earlier");
    expect(idsOf(merged)).toEqual([2, 3, 4, 5, 6]);
    expect(merged.earlier).toBe(2);
    const start = mergeTurns(held, page([1, 2, 3], { earlier: null }), "earlier");
    expect(start.earlier).toBeNull();
  });

  it("replaces every held turn from the refresh page's first on, and keeps `earlier`", () => {
    const held = heldOf([1, 2, 3], { earlier: 1 });
    const grown: Turn = { ...turn(3), text: "grown", partial: false };
    const merged = mergeTurns(
      held,
      { ...page([4]), turns: [grown, turn(4)], earlier: null },
      "refresh",
    );
    expect(idsOf(merged)).toEqual([1, 2, 3, 4]);
    expect(merged.turns[2]?.text).toBe("grown");
    expect(merged.earlier).toBe(1);
  });

  it("keeps the held turns when a refresh page holds none", () => {
    const held = heldOf([1, 2], { earlier: 1 });
    const merged = mergeTurns(held, { ...page([]), turns: null }, "refresh");
    expect(idsOf(merged)).toEqual([1, 2]);
    expect(merged.earlier).toBe(1);
  });

  it("keeps `skipped` once a page reported a record it could not read", () => {
    expect(mergeTurns(heldOf([1], { skipped: 2 }), page([2]), "refresh").skipped).toBe(2);
    expect(mergeTurns(heldOf([1]), page([2], { skipped: 1 }), "refresh").skipped).toBe(1);
    expect(
      mergeTurns(heldOf([1], { skipped: 2 }), page([2], { skipped: 3 }), "refresh").skipped,
    ).toBe(3);
    expect(mergeTurns(undefined, page([2], { skipped: 1 }), "newest").skipped).toBe(1);
  });

  it("takes the subagent from the latest page", () => {
    const agent: AgentPage = {
      id: "a1f00dcafe0001",
      type: "Explore",
      description: "look around",
      teammate: false,
      state: "running",
      started: "2026-11-19T10:00:00Z",
      missing: false,
    };
    expect(mergeTurns(heldOf([1]), page([2], { agent }), "refresh").agent).toEqual(agent);
    expect(mergeTurns(heldOf([4], { earlier: 4 }), page([3], { agent }), "earlier").agent).toEqual(
      agent,
    );
    expect(mergeTurns(undefined, page([1]), "newest").agent).toBeUndefined();
  });

  it("holds the fixture's page as it came", () => {
    const fixture = fixtureTurns();
    const merged = mergeTurns(undefined, fixture, "newest");
    expect(merged.turns).toEqual(fixture.turns ?? []);
    expect(merged.conversation).toBe(fixture.conversation);
  });
});

describe("appendLog", () => {
  it("holds a first page as it came", () => {
    expect(appendLog(undefined, logPage("one\n", 4, { cut: true }))).toEqual({
      text: "one\n",
      end: 4,
      cut: true,
    });
  });

  it("replaces the held log with a reset page", () => {
    const held: HeldLog = { text: "old\n", end: 4, cut: false };
    expect(appendLog(held, logPage("new\n", 9, { reset: true, cut: true }))).toEqual({
      text: "new\n",
      end: 9,
      cut: true,
    });
  });

  it("appends a page, moves `end` and keeps the held `cut`", () => {
    const held: HeldLog = { text: "one\n", end: 4, cut: true };
    expect(appendLog(held, logPage("two\n", 8, { cut: false }))).toEqual({
      text: "one\ntwo\n",
      end: 8,
      cut: true,
    });
  });

  it("trims past the cap at the next newline and marks the log cut", () => {
    const held: HeldLog = { text: "AAAA\n" + "b".repeat(MAX_LOG_CHARS - 5), end: 1, cut: false };
    const next = appendLog(held, logPage("\ncc", 2));
    expect(next.cut).toBe(true);
    expect(next.text.startsWith("b")).toBe(true);
    expect(next.text.endsWith("\ncc")).toBe(true);
    expect(next.text.length).toBe(MAX_LOG_CHARS - 2);
  });

  it("trims past the cap in mid-line when there is no newline to trim at", () => {
    const held: HeldLog = { text: "x".repeat(MAX_LOG_CHARS), end: 1, cut: false };
    const next = appendLog(held, logPage("yyy", 2));
    expect(next.cut).toBe(true);
    expect(next.text.length).toBe(MAX_LOG_CHARS);
    expect(next.text.endsWith("xxxyyy")).toBe(true);
  });

  it("leaves a log of exactly the cap whole", () => {
    const held: HeldLog = { text: "x".repeat(MAX_LOG_CHARS - 3), end: 1, cut: false };
    const next = appendLog(held, logPage("yyy", 2));
    expect(next.cut).toBe(false);
    expect(next.text.length).toBe(MAX_LOG_CHARS);
  });
});
const options = (extra: Partial<TurnPagesOptions> = {}): TurnPagesOptions => ({
  session: 7,
  agent: undefined,
  live: false,
  ...extra,
});

function mountTurns(initial: TurnPagesOptions) {
  return renderHook((props: TurnPagesOptions) => useTurnPages(props), {
    initialProps: initial,
    wrapper,
  });
}

// open mounts a conversation, lets its page land and closes it again: it stays in the cache.
async function open(session: number): Promise<void> {
  turnsRead.mockResolvedValueOnce(ok(page([1])));
  const view = mountTurns(options({ session }));
  await settle();
  view.unmount();
}

describe("useTurnPages", () => {
  it("opens at the newest page and says so", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3], { earlier: 40 })));
    const { result } = mountTurns(options());
    expect(result.current.held).toBeUndefined();
    expect(result.current.landed).toBeUndefined();
    expect(result.current.loading).toBe(true);
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    expect(turnsRead).toHaveBeenCalledWith(7, {});
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3]);
    expect(result.current.held?.earlier).toBe(40);
    expect(result.current.landed).toBe("newest");
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBe("");
  });

  it("holds the fixture's page as mergeTurns would", async () => {
    const fixture = fixtureTurns();
    turnsRead.mockResolvedValueOnce(ok(fixture));
    const { result } = mountTurns(options());
    await settle();
    expect(result.current.held).toEqual(mergeTurns(undefined, fixture, "newest"));
  });

  it("refreshes from the newest turn every 5 s while live, and not otherwise", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const view = mountTurns(options({ live: true }));
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    await settle(5000);
    expect(turnsRead).toHaveBeenCalledTimes(2);
    expect(turnsRead).toHaveBeenLastCalledWith(7, { conversation: "c1", after: "3" });
    expect(view.result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3, 4]);
    expect(view.result.current.landed).toBe("refresh");
    view.rerender(options({ live: false }));
    await settle(20_000);
    expect(turnsRead).toHaveBeenCalledTimes(2);
  });

  it("pages back by `before` and asks nothing once the start is reached", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([5, 6], { earlier: 5 })));
    const { result } = mountTurns(options());
    await settle();
    turnsRead.mockResolvedValueOnce(ok(page([3, 4], { earlier: null })));
    act(() => {
      result.current.load("earlier");
    });
    await settle();
    expect(turnsRead).toHaveBeenLastCalledWith(7, { conversation: "c1", before: "5" });
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([3, 4, 5, 6]);
    expect(result.current.held?.earlier).toBeNull();
    expect(result.current.landed).toBe("earlier");
    act(() => {
      result.current.load("earlier");
    });
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(2);
  });

  it("calls a reset page newest, and replaces the turns", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const { result } = mountTurns(options());
    await settle();
    turnsRead.mockResolvedValueOnce(ok(page([8, 9], { conversation: "c2", reset: true })));
    act(() => {
      result.current.load("refresh");
    });
    await settle();
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([8, 9]);
    expect(result.current.held?.conversation).toBe("c2");
    expect(result.current.landed).toBe("newest");
  });

  it("keeps one request in flight and runs a refresh asked meanwhile once, when it lands", async () => {
    const first = deferred<Outcome<Turns>>();
    turnsRead.mockReturnValueOnce(first.promise);
    const { result } = mountTurns(options());
    await settle();
    act(() => {
      result.current.load("refresh");
      result.current.load("refresh");
      result.current.load("earlier");
    });
    expect(turnsRead).toHaveBeenCalledTimes(1);
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    first.resolve(ok(page([1, 2, 3], { earlier: 9 })));
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(2);
    expect(turnsRead).toHaveBeenLastCalledWith(7, { conversation: "c1", after: "3" });
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3, 4]);
    await settle(10_000);
    expect(turnsRead).toHaveBeenCalledTimes(2);
  });

  it("says why a read failed, keeps the turns held, and clears the error on the next page", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    const { result } = mountTurns(options());
    await settle();
    turnsRead.mockResolvedValueOnce(refused(409, "session is busy"));
    act(() => {
      result.current.load("refresh");
    });
    await settle();
    expect(result.current.error).toBe("session is busy");
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([1, 2]);
    expect(result.current.loading).toBe(false);
    turnsRead.mockResolvedValueOnce(ok(page([2, 3])));
    act(() => {
      result.current.load("refresh");
    });
    await settle();
    expect(result.current.error).toBe("");
    expect(result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3]);
  });

  it("gives two components with the same session and agent one request and one held state", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    const { result } = renderHook(
      () => ({ a: useTurnPages(options()), b: useTurnPages(options()) }),
      {
        wrapper,
      },
    );
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    expect(result.current.a.held).toBeDefined();
    expect(result.current.a.held).toBe(result.current.b.held);
  });

  it("draws a conversation's pages first when it is opened again, and refreshes them", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const view = mountTurns(options());
    await settle();
    view.unmount();
    await settle(10 * 60_000);
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    const again = mountTurns(options());
    expect(again.result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3]);
    await settle();
    expect(turnsRead).toHaveBeenLastCalledWith(7, { conversation: "c1", after: "3" });
    expect(again.result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3, 4]);
  });

  it("does not keep a subagent's pages: a reopened view loads the newest", async () => {
    const agent = "a1f00dcafe0001";
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    const view = renderHook(
      () => ({
        pages: useTurnPages(options({ agent })),
        head: useHeldTurns(7, agent),
      }),
      { wrapper },
    );
    await settle();
    expect(turnsRead).toHaveBeenCalledWith(7, { agent });
    expect(view.result.current.head).toBeDefined();
    expect(view.result.current.head).toBe(view.result.current.pages.held);
    view.unmount();
    await settle();
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const again = mountTurns(options({ agent }));
    expect(again.result.current.held).toBeUndefined();
    await settle();
    expect(turnsRead).toHaveBeenLastCalledWith(7, { agent });
    expect(again.result.current.held?.turns.map((t) => t.id)).toEqual([1, 2, 3]);
  });
});

describe("the kept scroll and the eight conversations kept", () => {
  it("keeps a page scroll with a conversation's pages", async () => {
    await open(3);
    expect(keptScroll(3)).toBeUndefined();
    keepScroll(3, 340);
    expect(keptScroll(3)).toBe(340);
    keepScroll(3, 120);
    expect(keptScroll(3)).toBe(120);
  });

  it("keeps no scroll for a conversation whose pages are not held", () => {
    keepScroll(99, 5);
    expect(keptScroll(99)).toBeUndefined();
  });

  it("keeps no scroll while a mounted conversation's first read is still out", async () => {
    const first = deferred<Outcome<Turns>>();
    turnsRead.mockReturnValueOnce(first.promise);
    const view = mountTurns(options({ session: 4 }));
    await settle();
    keepScroll(4, 77);
    expect(keptScroll(4)).toBeUndefined();
    first.resolve(ok(page([1])));
    await settle();
    expect(keptScroll(4)).toBeUndefined();
    keepScroll(4, 77);
    expect(keptScroll(4)).toBe(77);
    view.unmount();
  });

  it("evicts the least recently opened conversation's pages and scroll for a ninth", async () => {
    for (let session = 1; session <= KEEP_SESSIONS; session++) {
      await open(session);
      keepScroll(session, session * 100);
    }
    expect(keptScroll(1)).toBe(100);
    await open(KEEP_SESSIONS + 1);
    expect(keptScroll(1)).toBeUndefined();
    expect(
      renderHook(() => useHeldTurns(1, undefined), { wrapper }).result.current,
    ).toBeUndefined();
    expect(keptScroll(2)).toBe(200);
    expect(renderHook(() => useHeldTurns(2, undefined), { wrapper }).result.current).toBeDefined();
  });

  it("never evicts a conversation a view still shows", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1])));
    const shown = mountTurns(options({ session: 1 }));
    await settle();
    keepScroll(1, 100);
    for (let session = 2; session <= KEEP_SESSIONS + 1; session++) {
      await open(session);
      keepScroll(session, session * 100);
    }
    expect(keptScroll(1)).toBe(100);
    expect(shown.result.current.held).toBeDefined();
    expect(keptScroll(2)).toBeUndefined();
    expect(keptScroll(3)).toBe(300);
    shown.unmount();
  });

  it("neither reads nor brings back a conversation evicted while a refresh was queued", async () => {
    const first = deferred<Outcome<Turns>>();
    turnsRead.mockReturnValueOnce(first.promise);
    const view = mountTurns(options({ session: 1 }));
    await settle();
    act(() => {
      view.result.current.load("refresh");
    });
    view.unmount();
    for (let session = 2; session <= KEEP_SESSIONS + 1; session++) await open(session);
    first.resolve(ok(page([1])));
    await settle(100);
    expect(turnsRead).toHaveBeenCalledTimes(KEEP_SESSIONS + 1);
    expect(
      renderHook(() => useHeldTurns(1, undefined), { wrapper }).result.current,
    ).toBeUndefined();
  });

  it("counts opening a conversation again as using it", async () => {
    for (let session = 1; session <= KEEP_SESSIONS; session++) {
      await open(session);
      keepScroll(session, session * 100);
    }
    await open(1);
    await open(KEEP_SESSIONS + 1);
    expect(keptScroll(2)).toBeUndefined();
    expect(keptScroll(1)).toBe(100);
  });

  it("drops the scroll with the cache when the pairing is revoked", async () => {
    await open(3);
    keepScroll(3, 50);
    queryClient.clear();
    expect(keptScroll(3)).toBeUndefined();
    await open(3);
    expect(keptScroll(3)).toBeUndefined();
  });
});
const patchOptions = (extra: Partial<PatchOptions> = {}): PatchOptions => ({
  session: 7,
  signature: "Running:1:0",
  live: false,
  ...extra,
});

function mountPatch(initial: PatchOptions) {
  return renderHook((props: PatchOptions) => usePatch(props), { initialProps: initial, wrapper });
}

describe("usePatch", () => {
  it("reads the diff on mount", async () => {
    const patch = fixturePatch();
    diffRead.mockResolvedValueOnce(ok(patch));
    const { result } = mountPatch(patchOptions());
    expect(result.current.patch).toBeUndefined();
    expect(result.current.loading).toBe(true);
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(1);
    expect(diffRead).toHaveBeenCalledWith(7);
    expect(result.current.patch).toEqual(patch);
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBe("");
  });

  it("reads again when the signature changes once a patch is held, and only then", async () => {
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    const view = mountPatch(patchOptions());
    await settle();
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    view.rerender(patchOptions({ signature: "Running:3:1" }));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(2);
    view.rerender(patchOptions({ signature: "Running:3:1" }));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(2);
  });

  it("reads once on a reopened diff with the same signature", async () => {
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    const first = mountPatch(patchOptions());
    await settle();
    first.unmount();
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    const again = mountPatch(patchOptions());
    expect(again.result.current.patch).toBeDefined();
    await settle(100);
    expect(diffRead).toHaveBeenCalledTimes(2);
  });

  it("treats a change of session as a new key, with no read beyond the observer's own", async () => {
    diffRead.mockResolvedValue(ok(fixturePatch()));
    const other = mountPatch(patchOptions({ session: 8, signature: "Idle:0:0" }));
    await settle();
    other.unmount();
    const view = mountPatch(patchOptions());
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(2);
    view.rerender(patchOptions({ session: 8, signature: "Running:3:1" }));
    await settle(100);
    expect(diffRead).toHaveBeenCalledTimes(3);
    expect(diffRead).toHaveBeenLastCalledWith(8);
  });

  it("does not read again on a signature change while the first read is still out", async () => {
    const first = deferred<Outcome<Patch>>();
    diffRead.mockReturnValueOnce(first.promise);
    const view = mountPatch(patchOptions());
    await settle();
    view.rerender(patchOptions({ signature: "Running:3:1" }));
    first.resolve(ok(fixturePatch()));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(1);
  });

  it("keeps one read in flight and runs a read asked meanwhile once, when it lands", async () => {
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    const view = mountPatch(patchOptions());
    await settle();
    const second = deferred<Outcome<Patch>>();
    diffRead.mockReturnValueOnce(second.promise);
    view.rerender(patchOptions({ signature: "Running:3:1" }));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(2);
    view.rerender(patchOptions({ signature: "Running:4:1" }));
    act(() => {
      view.result.current.reload();
    });
    expect(diffRead).toHaveBeenCalledTimes(2);
    diffRead.mockResolvedValueOnce(ok(fixturePatch()));
    second.resolve(ok(fixturePatch()));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(3);
    await settle(5000);
    expect(diffRead).toHaveBeenCalledTimes(3);
  });

  it("reads every 10 s while live, and not otherwise", async () => {
    diffRead.mockResolvedValue(ok(fixturePatch()));
    const view = mountPatch(patchOptions({ live: true }));
    await settle();
    expect(diffRead).toHaveBeenCalledTimes(1);
    await settle(10_000);
    expect(diffRead).toHaveBeenCalledTimes(2);
    view.rerender(patchOptions({ live: false }));
    await settle(30_000);
    expect(diffRead).toHaveBeenCalledTimes(2);
  });

  it("says why a read failed, keeps the patch held, and reads again on reload", async () => {
    const patch = fixturePatch();
    diffRead.mockResolvedValueOnce(ok(patch));
    const { result } = mountPatch(patchOptions());
    await settle();
    diffRead.mockResolvedValueOnce(refused(409, "session is busy"));
    act(() => {
      result.current.reload();
    });
    await settle();
    expect(result.current.error).toBe("session is busy");
    expect(result.current.patch).toEqual(patch);
    diffRead.mockResolvedValueOnce(ok(patch));
    act(() => {
      result.current.reload();
    });
    await settle();
    expect(result.current.error).toBe("");
  });
});
const logOptions = (extra: Partial<JobLogOptions> = {}): JobLogOptions => ({
  session: 7,
  job: 214,
  live: false,
  ...extra,
});

function mountLog(initial: JobLogOptions) {
  return renderHook((props: JobLogOptions) => useJobLog(props), {
    initialProps: initial,
    wrapper,
  });
}

describe("useJobLog", () => {
  it("reads the tail on mount and holds the fixture's page", async () => {
    const tail = fixtureJobLog();
    logRead.mockResolvedValueOnce(ok(tail));
    const { result } = mountLog(logOptions());
    expect(result.current.log).toBeUndefined();
    expect(result.current.loading).toBe(true);
    await settle();
    expect(logRead).toHaveBeenCalledTimes(1);
    expect(logRead).toHaveBeenCalledWith(7, 214, undefined);
    expect(result.current.log).toEqual({ text: tail.text, end: tail.end, cut: tail.cut });
    expect(result.current.gone).toBe("");
    expect(result.current.error).toBe("");
    expect(result.current.loading).toBe(false);
  });

  it("draws the kept log first when a job is opened again, and reads after its end", async () => {
    const tail = fixtureJobLog();
    const more = logPage("and more\n", tail.end + 9, { start: tail.end });
    logRead.mockResolvedValueOnce(ok(tail));
    const view = mountLog(logOptions());
    await settle();
    view.unmount();
    await settle(10 * 60_000);
    logRead.mockResolvedValueOnce(ok(more));
    const again = mountLog(logOptions());
    expect(again.result.current.log?.text).toBe(tail.text);
    await settle();
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: String(tail.end) });
    expect(again.result.current.log?.text).toBe(tail.text + "and more\n");
    expect(again.result.current.log?.end).toBe(more.end);
  });

  it("reads after the end every 2 s while live and appends what it gets", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const { result } = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce(ok(logPage("b\n", 4)));
    await settle(2000);
    expect(logRead).toHaveBeenCalledTimes(2);
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: "2" });
    expect(result.current.log).toEqual({ text: "a\nb\n", end: 4, cut: false });
  });

  it("stops polling when the job ends, after one more read", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const view = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce(ok(logPage("last\n", 7)));
    view.rerender(logOptions({ live: false }));
    await settle();
    expect(logRead).toHaveBeenCalledTimes(2);
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: "2" });
    expect(view.result.current.log?.text).toBe("a\nlast\n");
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(2);
  });

  it("does not read once more when it was never live", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const view = mountLog(logOptions({ live: false }));
    await settle();
    view.rerender(logOptions({ live: false }));
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(1);
  });

  it("says a 409 means the job left the queue, keeps the log, and stops reading", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const { result } = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce(refused(409, "job 214 has left the queue"));
    await settle(2000);
    expect(result.current.gone).toBe("job 214 has left the queue");
    expect(result.current.error).toBe("");
    expect(result.current.log?.text).toBe("a\n");
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(2);
  });

  it("waits for the next tick after a read that timed out", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const { result } = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce({ kind: "timeout", message: "timed out" });
    await settle(2000);
    expect(result.current.error).toBe("timed out");
    expect(result.current.gone).toBe("");
    logRead.mockResolvedValueOnce(ok(logPage("b\n", 4)));
    await settle(2000);
    expect(logRead).toHaveBeenCalledTimes(3);
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: "2" });
    expect(result.current.error).toBe("");
    expect(result.current.log?.text).toBe("a\nb\n");
  });

  it("reads once more at the job's end, once, when a read is still out then", async () => {
    const first = deferred<Outcome<JobLog>>();
    logRead.mockReturnValueOnce(first.promise);
    const view = mountLog(logOptions({ live: true }));
    await settle();
    view.rerender(logOptions({ live: false }));
    await settle();
    expect(logRead).toHaveBeenCalledTimes(1);
    logRead.mockResolvedValueOnce(ok(logPage("last\n", 7)));
    first.resolve(ok(logPage("a\n", 2)));
    await settle();
    expect(logRead).toHaveBeenCalledTimes(2);
    expect(logRead).toHaveBeenLastCalledWith(7, 214, { after: "2" });
    expect(view.result.current.log?.text).toBe("a\nlast\n");
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(2);
  });

  it("treats a change of job as a new key, with no read beyond the observer's own", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const view = mountLog(logOptions({ live: true }));
    await settle();
    logRead.mockResolvedValueOnce(ok(logPage("b\n", 3)));
    view.rerender(logOptions({ job: 215, live: false }));
    await settle(100);
    expect(logRead).toHaveBeenCalledTimes(2);
    expect(logRead).toHaveBeenLastCalledWith(7, 215, undefined);
    await settle(20_000);
    expect(logRead).toHaveBeenCalledTimes(2);
  });

  it("evicts the least recently opened job's log for a sixth", async () => {
    const openJob = async (job: number): Promise<void> => {
      logRead.mockResolvedValueOnce(ok(logPage("j", 1)));
      const view = mountLog(logOptions({ session: 5, job }));
      await settle();
      view.unmount();
    };
    for (let job = 1; job <= KEEP_JOBS + 1; job++) await openJob(job);
    await openJob(2);
    expect(logRead).toHaveBeenLastCalledWith(5, 2, { after: "1" });
    await openJob(1);
    expect(logRead).toHaveBeenLastCalledWith(5, 1, undefined);
  });

  it("drops the logs with the cache when the pairing is revoked", async () => {
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const view = mountLog(logOptions());
    await settle();
    view.unmount();
    queryClient.clear();
    logRead.mockResolvedValueOnce(ok(logPage("a\n", 2)));
    const again = mountLog(logOptions());
    expect(again.result.current.log).toBeUndefined();
    await settle();
    expect(logRead).toHaveBeenLastCalledWith(7, 214, undefined);
  });
});

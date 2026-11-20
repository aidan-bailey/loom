import { QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";

import type { Outcome, Session, Turn, Turns } from "@/api/gen";
import { queryClient } from "@/api/queries";
import { keptScroll } from "@/api/reads";
import { api } from "@/api/request";
import "@/commands";
import { deferred, makeSession, resetFleet, seed } from "@/commands/testkit";
import { fixtureTurns } from "@/fixtures";
import { readSeen } from "@/fleet/seen";
import { useSession } from "@/store/fleet";

import { Conversation } from "./Conversation";

const NOW = Date.parse("2026-11-19T12:00:00Z");

const spyOnTurns = () =>
  vi.spyOn(api, "turns").mockRejectedValue(new Error("unexpected turns read"));
let turnsRead: ReturnType<typeof spyOnTurns>;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  queryClient.clear();
  turnsRead = spyOnTurns();
  window.history.replaceState(null, "", "#/s/1");
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  resetFleet();
  window.localStorage.clear();
  window.history.replaceState(null, "", window.location.pathname);
});

async function settle(ms = 1): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

const ok = <T,>(value: T): Outcome<T> => ({ kind: "ok", status: 200, value, notices: [] });
const refused = (status: number, message: string): Outcome<never> => ({
  kind: "refused",
  status,
  message,
});

function turn(id: number): Turn {
  return {
    id,
    at: "2026-11-19T10:00:00Z",
    from: "you",
    name: "",
    text: `prompt ${id}`,
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

// The session on screen is the store's, so a test changes it as the stream would.
let current: Partial<Session> = {};

function sessionNow(): Session {
  return makeSession({
    id: 1,
    claude: true,
    remote_url: "https://claude.ai/code/webshot",
    ...current,
  });
}

function change(over: Partial<Session>): void {
  current = { ...current, ...over };
  act(() => {
    seed([sessionNow()]);
  });
}

function Screen(): ReactElement | null {
  const session = useSession(1);
  return session === undefined ? null : <Conversation session={session} />;
}

function show(over: Partial<Session> = {}) {
  current = over;
  seed([sessionNow()]);
  return render(
    <QueryClientProvider client={queryClient}>
      <Screen />
    </QueryClientProvider>,
  );
}

describe("Conversation's drawn parts", () => {
  it("draws the turns, and marks only the newest working while the session runs", async () => {
    turnsRead.mockResolvedValueOnce(ok(fixtureTurns()));
    const first = show({ status: "Running", group: "working" });
    await settle();
    expect(screen.getByText("Its start is earlier.")).toBeTruthy();
    expect(screen.getAllByText(/WORKING/)).toHaveLength(1);
    first.unmount();
    queryClient.clear();

    turnsRead.mockResolvedValueOnce(ok(fixtureTurns()));
    show({ status: "Ready", group: "your_turn" });
    await settle();
    expect(screen.queryByText(/WORKING/)).toBeNull();
  });

  it("says the records it could not read", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1], { skipped: 2 })));
    const first = show();
    await settle();
    expect(screen.getByText("Some records could not be read.")).toBeTruthy();
    first.unmount();
    queryClient.clear();

    turnsRead.mockResolvedValueOnce(ok(page([1], { skipped: 0 })));
    show();
    await settle();
    expect(screen.queryByText("Some records could not be read.")).toBeNull();
  });

  it("offers earlier turns only while there are some, and asks for them by before", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([5, 6], { earlier: 5 })));
    const first = show();
    await settle();
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    fireEvent.click(screen.getByRole("button", { name: "↑ earlier turns" }));
    await settle();
    expect(turnsRead).toHaveBeenLastCalledWith(1, { conversation: "c1", before: "5" });
    expect(screen.getByText("prompt 3")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "↑ earlier turns" })).toBeNull();
    first.unmount();
    queryClient.clear();

    turnsRead.mockResolvedValueOnce(ok(page([5, 6], { earlier: null })));
    show();
    await settle();
    expect(screen.queryByRole("button", { name: "↑ earlier turns" })).toBeNull();
  });

  it("says Loading… until the first page lands", async () => {
    const pending = deferred<Outcome<Turns>>();
    turnsRead.mockReturnValueOnce(pending.promise);
    show();
    await settle();
    expect(screen.getByText("Loading…")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "retry" })).toBeNull();
    expect(screen.queryByText("No turns yet.")).toBeNull();
    pending.resolve(ok(page([1])));
    await settle();
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.getByText("prompt 1")).toBeTruthy();
  });

  it("says why the first read failed and retries it", async () => {
    turnsRead.mockResolvedValueOnce(refused(409, "session is busy"));
    show();
    await settle();
    expect(screen.getByText("session is busy")).toBeTruthy();
    expect(screen.queryByText("Loading…")).toBeNull();
    turnsRead.mockResolvedValueOnce(ok(page([1])));
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await settle();
    expect(screen.getByText("prompt 1")).toBeTruthy();
    expect(turnsRead).toHaveBeenCalledTimes(2);
    expect(turnsRead).toHaveBeenLastCalledWith(1, {});
  });

  it("says there are no turns yet for an empty page", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([])));
    const first = show();
    await settle();
    expect(screen.getByText("No turns yet.")).toBeTruthy();
    first.unmount();
    queryClient.clear();

    turnsRead.mockResolvedValueOnce(ok({ ...page([]), turns: null }));
    const second = show();
    await settle();
    expect(screen.getByText("No turns yet.")).toBeTruthy();
    second.unmount();
    queryClient.clear();

    turnsRead.mockResolvedValueOnce(ok(page([1])));
    show();
    await settle();
    expect(screen.queryByText("No turns yet.")).toBeNull();
  });

  it("keeps the turns and says why a refresh failed, with its retry", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    show();
    await settle();
    turnsRead.mockResolvedValueOnce(refused(504, "timed out"));
    change({ last_message: "x" });
    await settle();
    expect(screen.getByText(/Could not refresh: timed out/)).toBeTruthy();
    expect(screen.getByText("prompt 2")).toBeTruthy();
    turnsRead.mockResolvedValueOnce(ok(page([2, 3])));
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await settle();
    expect(screen.getByText("prompt 3")).toBeTruthy();
    expect(screen.queryByText(/Could not refresh/)).toBeNull();
  });

  it("draws the needs-you box only while the group is needs_you", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1])));
    const first = show({ status: "Prompting", group: "needs_you" });
    await settle();
    expect(screen.getByRole("link", { name: "Open in Claude" })).toBeTruthy();
    first.unmount();
    queryClient.clear();

    turnsRead.mockResolvedValueOnce(ok(page([1])));
    show({ status: "Ready", group: "your_turn" });
    await settle();
    expect(screen.queryByRole("link", { name: "Open in Claude" })).toBeNull();
  });

  it("ends in the reply box, or says to resume while the session is paused", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1])));
    const first = show();
    await settle();
    expect(screen.getByRole("textbox", { name: "Reply" })).toBeTruthy();
    first.unmount();
    queryClient.clear();

    turnsRead.mockResolvedValueOnce(ok(page([1])));
    const second = show({ status: "Paused", group: "paused" });
    await settle();
    expect(screen.getByText("Resume the session to reply.")).toBeTruthy();
    expect(screen.queryByRole("textbox")).toBeNull();
    second.unmount();
    queryClient.clear();

    turnsRead.mockResolvedValueOnce(ok(page([1])));
    show({ status: "Loading", group: "working" });
    await settle();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByText("Resume the session to reply.")).toBeNull();
  });
});

let pageHeight = 2000;
let pageY = 0;
let scrollTo: Mock<(x: number, y: number) => void>;

function stubPage(): void {
  pageHeight = 2000;
  pageY = 0;
  Object.defineProperty(document.documentElement, "scrollHeight", {
    configurable: true,
    get: () => pageHeight,
  });
  Object.defineProperty(window, "scrollY", { configurable: true, get: () => pageY });
  Object.defineProperty(window, "innerHeight", { configurable: true, value: 800 });
  scrollTo = vi.fn<(x: number, y: number) => void>((...args: [number, number]) => {
    pageY = args[1];
  });
  window.scrollTo = scrollTo as unknown as typeof window.scrollTo;
}

// readerAt puts the reader at y and tells the page, as the browser's scroll event does.
function readerAt(y: number): void {
  pageY = y;
  fireEvent.scroll(window);
}

describe("Conversation's refreshes", () => {
  it("a change of the last message or of the status refreshes the newest turn, an unrelated change does not", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    show();
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);

    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    change({ last_message: "next" });
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(2);
    expect(turnsRead).toHaveBeenLastCalledWith(1, { conversation: "c1", after: "3" });
    expect(screen.getByText("prompt 4")).toBeTruthy();

    turnsRead.mockResolvedValueOnce(ok(page([4, 5])));
    change({ status: "Prompting", group: "needs_you" });
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(3);

    change({ title: "renamed" });
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(3);
  });

  it("a change while the first page is in flight asks for nothing", async () => {
    const pending = deferred<Outcome<Turns>>();
    turnsRead.mockReturnValueOnce(pending.promise);
    show();
    await settle();
    change({ last_message: "next" });
    pending.resolve(ok(page([1, 2])));
    await settle(10);
    expect(turnsRead).toHaveBeenCalledTimes(1);
  });

  it("a running session is refreshed every 5 s and a ready one is not", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1])));
    const first = show({ status: "Running", group: "working" });
    await settle();
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    await settle(5000);
    expect(turnsRead).toHaveBeenCalledTimes(2);
    expect(turnsRead).toHaveBeenLastCalledWith(1, { conversation: "c1", after: "1" });
    first.unmount();
    queryClient.clear();
    turnsRead.mockClear();

    turnsRead.mockResolvedValueOnce(ok(page([1])));
    show({ status: "Ready", group: "your_turn" });
    await settle();
    await settle(20_000);
    expect(turnsRead).toHaveBeenCalledTimes(1);
  });
});

describe("Conversation's scroll", () => {
  beforeEach(stubPage);

  it("the first page lands at the bottom", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    show();
    await settle();
    expect(scrollTo).toHaveBeenCalledWith(0, 2000);
  });

  it("an earlier page keeps the reader's place", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([5, 6], { earlier: 5 })));
    show();
    await settle();
    readerAt(300);
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    pageHeight = 3200;
    fireEvent.click(screen.getByRole("button", { name: "↑ earlier turns" }));
    await settle();
    expect(scrollTo).toHaveBeenLastCalledWith(0, 1500);
    expect(scrollTo).not.toHaveBeenCalledWith(0, 3200);
  });

  it("a refresh lands at the bottom only when the reader is within 48 px of it", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    const first = show();
    await settle();
    readerAt(1152);
    scrollTo.mockClear();
    pageHeight = 2400;
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    change({ last_message: "a" });
    await settle();
    expect(scrollTo).toHaveBeenCalledWith(0, 2400);
    first.unmount();
    queryClient.clear();

    stubPage();
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    show();
    await settle();
    readerAt(1151);
    scrollTo.mockClear();
    pageHeight = 2400;
    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    change({ last_message: "b" });
    await settle();
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("a reset page lands at the bottom wherever the reader is", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2])));
    show();
    await settle();
    readerAt(0);
    scrollTo.mockClear();
    pageHeight = 2600;
    turnsRead.mockResolvedValueOnce(ok(page([9], { conversation: "c2", reset: true })));
    change({ last_message: "a" });
    await settle();
    expect(scrollTo).toHaveBeenCalledWith(0, 2600);
  });

  it("a conversation kept in the cache comes back where it was left", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const first = show();
    await settle();
    readerAt(640);
    first.unmount();
    scrollTo.mockClear();

    turnsRead.mockResolvedValueOnce(ok(page([3, 4])));
    show();
    expect(scrollTo).toHaveBeenCalledWith(0, 640);
    await settle();
    expect(scrollTo).not.toHaveBeenCalledWith(0, 2000);
  });

  it("leaving the conversation stops recording its scroll", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1, 2, 3])));
    const first = show();
    await settle();
    readerAt(640);
    first.unmount();
    readerAt(5);
    expect(keptScroll(1)).toBe(640);
  });
});

describe("Conversation's seen time", () => {
  it("marks the session seen on arrival and again on leaving", async () => {
    turnsRead.mockResolvedValueOnce(ok(page([1])));
    const view = show();
    await settle();
    expect(readSeen()["1"]).toBe(NOW);
    vi.setSystemTime(NOW + 60_000);
    view.unmount();
    expect(readSeen()["1"]).toBe(NOW + 60_000);
  });
});

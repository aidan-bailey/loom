import { act, render, renderHook, screen, type RenderResult } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Flow, Session } from "@/api/gen";
import { flowKey, useFlow } from "@/api/reads";
import { hrefFor } from "@/app/routes";
import { resetFleet, seed } from "@/commands/testkit";
import { fixtureFlow, fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";

import { usd as rowUsd } from "../fleet/SessionRow";
import FlowBody from "./FlowBody";
import { Bars, median, planOf, usd, useClock } from "./parts";

vi.mock("@/api/reads", async (original) => ({
  ...(await original<typeof import("@/api/reads")>()),
  useFlow: vi.fn(),
}));

const NOW = Date.parse("2026-11-19T12:00:00Z");

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  vi.mocked(useFlow).mockReturnValue({ flow: undefined, error: "", loading: false });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  resetFleet();
  window.history.replaceState(null, "", window.location.pathname);
});

function session6(over: Partial<Session> = {}): Session {
  const found = allSessions(fixtureSnapshot()).find((s) => s.id === 6);
  if (found === undefined) throw new Error("the canned snapshot holds no session 6");
  return { ...found, ...over };
}

// show seeds the session (session 6 unless given; null seeds none), makes the read answer with
// the board and error, and draws the board on its route.
function show(
  opts: {
    session?: Session | null;
    flow?: Flow;
    error?: string;
    search?: Record<string, string>;
  } = {},
): RenderResult {
  const session = opts.session === undefined ? session6() : opts.session;
  seed(session === null ? [] : [session]);
  vi.mocked(useFlow).mockReturnValue({ flow: opts.flow, error: opts.error ?? "", loading: false });
  window.history.replaceState(null, "", "#/s/6/flow");
  return render(<FlowBody sessionId={6} params={{ id: "6" }} search={opts.search ?? {}} />);
}

// board is the canned board; a test changes what it needs.
function board(over: (flow: Flow) => void = () => undefined): Flow {
  const flow = fixtureFlow();
  over(flow);
  return flow;
}

describe("flow parts", () => {
  it("planOf: the given plan, else the newest flow's, else none", () => {
    const session = session6();
    expect(session.flows).toHaveLength(2);
    expect(planOf(session, "x.md")).toBe("x.md");
    expect(planOf(session, "")).toBe(session.flows?.[0]?.plan);
    expect(planOf(session6({ flows: [] }), "")).toBe("");
    expect(planOf(session6({ flows: null }), "")).toBe("");
  });

  it("usd: words a figure as the fleet row does, and nothing for no number", () => {
    expect(usd(4.71)).toBe("$4.71");
    expect(usd(0.0031)).toBe("$0.0031");
    expect(usd(0.0312)).toBe("$0.03");
    expect(usd(0)).toBe("$0.00");
    expect(usd(0.01)).toBe("$0.01");
    expect(usd(undefined)).toBe("");
    expect(usd(null)).toBe("");
    expect(usd(0.0031)).toBe(rowUsd(0.0031));
  });

  it("median: the middle value, the mean of the middle two, and none for none", () => {
    const values = [3, 1, 2];
    expect(median(values)).toBe(2);
    expect(values).toEqual([3, 1, 2]);
    expect(median([4, 1, 3, 2])).toBe(2.5);
    expect(median([])).toBe(0);
  });

  it("Bars: one bar a value, the current one after them in the accent colour, each height an exact share of the tallest", () => {
    const heights = (container: HTMLElement): string[] =>
      Array.from(container.querySelectorAll<HTMLElement>('[data-slot="bar"]')).map(
        (el) => el.style.height,
      );
    const view = render(<Bars values={[1, 2, 4]} current={2} />);
    const bars = view.container.querySelectorAll<HTMLElement>('[data-slot="bar"]');
    expect(bars).toHaveLength(4);
    expect(bars[3]?.getAttribute("data-current")).toBe("true");
    expect(bars[3]?.classList.contains("bg-turn")).toBe(true);
    expect(bars[0]?.classList.contains("bg-turn")).toBe(false);
    expect(bars[0]?.hasAttribute("data-current")).toBe(false);
    expect(heights(view.container)).toEqual(["25%", "50%", "100%", "50%"]);
    expect(view.container.firstElementChild?.getAttribute("aria-hidden")).toBe("true");
    view.unmount();

    const none = render(<Bars values={[1, 2, 4]} current={null} />);
    expect(none.container.querySelectorAll('[data-slot="bar"]')).toHaveLength(3);
    none.unmount();

    const floor = render(<Bars values={[0.001, 4]} current={null} />);
    expect(heights(floor.container)[0]).toBe("5%");
    floor.unmount();

    const tall = render(<Bars values={[2]} current={8} />);
    expect(heights(tall.container)).toEqual(["25%", "100%"]);
    tall.unmount();

    const share = render(<Bars values={[1, 3]} current={null} />);
    expect(parseFloat(heights(share.container)[0] ?? "")).toBeCloseTo(33.333, 2);
  });

  it("useClock: the time now, moving every ms and no sooner, and stopped on unmount", () => {
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    const start = Date.parse("2026-11-19T12:00:00Z");
    vi.setSystemTime(start);
    const slow = renderHook(() => useClock(30_000));
    expect(slow.result.current).toBe(start);
    act(() => {
      vi.advanceTimersByTime(29_000);
    });
    expect(slow.result.current).toBe(start);
    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(slow.result.current).toBe(start + 30_000);
    slow.unmount();

    const now = Date.now();
    const fast = renderHook(() => useClock(1_000));
    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(fast.result.current).toBe(now + 1_000);
    fast.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe("FlowBody", () => {
  it("a session the snapshot does not list says it is gone and reads nothing", () => {
    show({ session: null });
    expect(screen.getByText("This session is gone.")).toBeInTheDocument();
    expect(vi.mocked(useFlow)).toHaveBeenCalledWith(expect.objectContaining({ plan: "" }));
  });

  it("a session with no flow and no plan says so and reads nothing", () => {
    for (const flows of [[], null]) {
      vi.mocked(useFlow).mockClear();
      const view = show({ session: session6({ flows }) });
      expect(screen.getByText("No flow in this session's worktree.")).toBeInTheDocument();
      expect(vi.mocked(useFlow)).toHaveBeenCalledWith(expect.objectContaining({ plan: "" }));
      view.unmount();
    }
  });

  it("reads the route's plan, else the newest flow's, keyed by the session's flows", () => {
    const session = session6();
    show({ flow: board() });
    expect(vi.mocked(useFlow)).toHaveBeenLastCalledWith({
      session: 6,
      plan: session.flows?.[0]?.plan,
      key: flowKey(session),
    });
    const second = session.flows?.[1]?.plan ?? "";
    show({ flow: board(), search: { plan: second } });
    expect(vi.mocked(useFlow)).toHaveBeenLastCalledWith({
      session: 6,
      plan: second,
      key: flowKey(session),
    });
  });

  it("draws Loading… until the board arrives, and the read's error when none is held", () => {
    const view = show();
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    view.unmount();
    show({ error: "loom answered 500" });
    expect(screen.getByRole("alert")).toHaveTextContent("loom answered 500");
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it("draws the title with its part, the plan path without a title, and the error over a held board", () => {
    const flow = board();
    const view = show({ flow });
    const title = screen.getByText(flow.summary.title, { selector: "p" });
    expect(title.tagName).toBe("P");
    expect(title).toHaveClass("font-semibold");
    view.unmount();

    const parted = show({ flow: board((f) => (f.summary.part = "part 2 of 3")) });
    expect(screen.getByText("part 2 of 3")).toBeInTheDocument();
    parted.unmount();

    const untitled = board((f) => (f.summary.title = ""));
    const bare = show({ flow: untitled });
    expect(screen.getByText(untitled.summary.plan, { selector: "p" })).toBeInTheDocument();
    bare.unmount();

    show({ flow, error: "loom answered 500" });
    expect(screen.getByText("loom answered 500")).toBeInTheDocument();
    expect(screen.getByText(flow.summary.title, { selector: "p" })).toBeInTheDocument();
  });

  it("draws the unreadable line and the lost line", () => {
    const view = show({ flow: board() });
    expect(screen.queryByText(/^unreadable:/)).toBeNull();
    expect(screen.queryByText(/engine has ended/)).toBeNull();
    view.unmount();

    const bad = show({ flow: board((f) => (f.summary.unreadable = "bad plan")) });
    expect(screen.getByText("unreadable: bad plan")).toBeInTheDocument();
    bad.unmount();

    show({
      flow: board((f) => {
        f.summary.status = "lost";
        f.summary.run = "r1";
      }),
    });
    expect(
      screen.getByText("The run's engine has ended without it: loom flow run resume r1"),
    ).toBeInTheDocument();
  });

  it("draws the phase line with the time since the run started, until a question is open, and the time moves with the clock", () => {
    const running = (end: string): Flow =>
      board((f) => {
        f.question = null;
        f.summary.phase = "story 3";
        f.summary.status = "running";
        f.start = new Date(NOW - 100_000).toISOString();
        f.end = end;
      });
    const view = show({ flow: running("") });
    expect(screen.getByText("running · story 3 · 1m 40s")).toBeInTheDocument();
    view.unmount();

    const withQuestion = show({ flow: board((f) => (f.summary.phase = "story 3")) });
    expect(screen.queryByText(/ · story 3/)).toBeNull();
    withQuestion.unmount();

    const ended = show({ flow: running(new Date(NOW - 60_000).toISOString()) });
    expect(screen.getByText(/running · story 3 · 40s$/)).toBeInTheDocument();
    ended.unmount();

    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    vi.setSystemTime(NOW);
    show({ flow: running("") });
    expect(screen.getByText("running · story 3 · 1m 40s")).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(30_000);
    });
    expect(screen.getByText("running · story 3 · 2m 10s")).toBeInTheDocument();
  });

  it("the picker shows for two flows or more, and picking one opens its board", async () => {
    const session = session6();
    const flows = session.flows ?? [];
    const flow = board();
    const view = show({ flow });
    const picker = screen.getByRole("combobox", { name: "flow" });
    const options = Array.from(picker.querySelectorAll("option"));
    expect(options.map((o) => o.textContent)).toEqual(flows.map((f) => f.title));
    expect(options.map((o) => o.value)).toEqual(flows.map((f) => f.plan));
    expect(picker).toHaveValue(flows[0]?.plan);

    const second = flows[1]?.plan ?? "";
    await userEvent.setup().selectOptions(picker, second);
    expect(window.location.hash).toBe(hrefFor("flow", { id: "6" }, { plan: second }));
    view.unmount();

    show({ session: session6({ flows: flows.slice(0, 1) }), flow });
    expect(screen.queryByRole("combobox")).toBeNull();
  });
});

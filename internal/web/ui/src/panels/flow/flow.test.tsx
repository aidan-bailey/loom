import { act, render, renderHook, screen, within, type RenderResult } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Flow, Session } from "@/api/gen";
import { flowKey, useFlow } from "@/api/reads";
import { panelFor, type PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { PanelHost, SessionTabs } from "@/app/shell";
import { resetFleet, seed } from "@/commands/testkit";
import { fixtureFlow, fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import "@/panels";
import { SessionHead } from "@/session/SessionHead";

import { usd as rowUsd } from "../fleet/SessionRow";
import FlowBody from "./FlowBody";
import { FlowMark, flowPanel } from "./index";
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

    const withQuestion = show({
      flow: board((f) => {
        f.summary.phase = "story 3";
        f.summary.status = "running";
      }),
    });
    expect(screen.queryByText(/^running · story 3/)).toBeNull();
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

describe("FlowBody boxes", () => {
  const plan = (): string => session6().flows?.[0]?.plan ?? "";
  const stageLinks = (): HTMLElement[] =>
    Array.from(screen.getByRole("navigation", { name: "Stages" }).children) as HTMLElement[];
  const rows = (): HTMLElement[] =>
    Array.from(document.querySelectorAll<HTMLElement>('a[href^="#/s/6/flow/"]')).filter(
      (a) => !(a.getAttribute("href") ?? "").startsWith("#/s/6/flow/doc/"),
    );
  const row = (id: string): HTMLElement => {
    const found = rows().find((a) => a.getAttribute("href")?.startsWith(`#/s/6/flow/${id}?`));
    if (found === undefined) throw new Error(`no row ${id}`);
    return found;
  };
  const meta = (id: string): string => row(id).querySelector(".ml-auto")?.textContent ?? "";
  const cells = (id: string): HTMLElement[] =>
    Array.from(row(id).querySelectorAll<HTMLElement>("[aria-label]"));

  it("the stage strip: five stages in order, each with its document where it has one", () => {
    show({ flow: board() });
    const items = stageLinks();
    expect(items.map((el) => el.textContent)).toEqual([
      "design ✓",
      "spec ✓",
      "draft ✓",
      "run 2/5",
      "record",
    ]);
    const flow = board();
    ["spec", "skeleton", "plan"].forEach((doc, i) => {
      expect(items[i]?.tagName).toBe("A");
      expect(items[i]).toHaveAttribute(
        "href",
        hrefFor("flowDoc", { id: "6", doc }, { plan: plan() }),
      );
      expect(items[i]).toHaveAttribute("title", flow.stages?.[i]?.detail);
    });
    expect(items[3]?.tagName).toBe("SPAN");
    expect(items[4]?.tagName).toBe("SPAN");
  });

  it("the stage strip: draws the five stages in order whatever order the board lists them", () => {
    show({ flow: board((f) => f.stages?.reverse()) });
    expect(stageLinks().map((el) => el.textContent)).toEqual([
      "design ✓",
      "spec ✓",
      "draft ✓",
      "run 2/5",
      "record",
    ]);
  });

  it("the stage strip: a stage the board lacks is to come, and an unknown state draws its raw state in neutral styling", () => {
    const view = show({ flow: board((f) => (f.stages = null)) });
    const items = stageLinks();
    expect(items.map((el) => el.textContent)).toEqual([
      "design",
      "spec",
      "draft",
      "run 2/5",
      "record",
    ]);
    for (const el of items) {
      expect(el.tagName).toBe("SPAN");
      expect(el).toHaveClass("text-faint");
    }
    view.unmount();

    show({
      flow: board((f) => {
        const first = f.stages?.[0];
        if (first !== undefined) (first as { state: string }).state = "paused";
      }),
    });
    const first = stageLinks()[0];
    expect(first).toHaveTextContent("design (paused)");
    expect(first).toHaveClass("text-muted-foreground");
    expect(first).not.toHaveClass("text-working");
  });

  it("the question box: the kind and story, the text, and the way to the manager's conversation", () => {
    const flow = board();
    const view = show({ flow });
    expect(screen.getByText("parked · blocked-decision · story 3")).toBeInTheDocument();
    expect(screen.getByText(flow.question?.text ?? "")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "open manager ›" })).toHaveAttribute("href", "#/s/6");
    view.unmount();

    const noStory = show({
      flow: board((f) => {
        if (f.question !== null) f.question.story = "";
      }),
    });
    expect(screen.getByText("parked · blocked-decision")).toBeInTheDocument();
    noStory.unmount();

    show({ flow: board((f) => (f.question = null)) });
    expect(screen.queryByText(/^parked · blocked-decision/)).toBeNull();
    expect(screen.queryByRole("link", { name: "open manager ›" })).toBeNull();
  });

  it("the stories box: the premise row, the stories and the final row, each a link to its row", () => {
    const view = show({ flow: board() });
    expect(screen.getByText("stories")).toBeInTheDocument();
    const ids = ["P", "1", "2", "3", "4", "5", "F"];
    expect(rows().map((a) => a.getAttribute("href"))).toEqual(
      ids.map((story) => hrefFor("flowStory", { id: "6", story }, { plan: plan() })),
    );
    view.unmount();

    show({
      flow: board((f) => {
        f.premise = null;
        f.final = null;
      }),
    });
    expect(rows()).toHaveLength(5);
  });

  it("the stories' right-hand text: commits and spend once started, tier and review before, premises and final", () => {
    const view = show({ flow: board() });
    expect(meta("1")).toBe("7c · $1.41");
    expect(meta("2")).toBe("4c · $1.10");
    expect(meta("3")).toBe("$0.62");
    expect(meta("4")).toBe("");
    expect(meta("5")).toBe("deep · full");
    expect(meta("P")).toBe("premises");
    expect(meta("F")).toBe("final");
    view.unmount();

    show({
      flow: board((f) => {
        const one = f.stories?.[0];
        if (one !== undefined) one.commits = 0;
        if (f.costs.by_story !== null) delete f.costs.by_story["1"];
        const five = f.stories?.[4];
        if (five !== undefined) five.review = "";
      }),
    });
    expect(meta("1")).toBe("");
    expect(meta("5")).toBe("deep");
  });

  it("the cells: each kind's word, ? for asks, a dash for skipped, todo for a missing cell, by row kind", () => {
    const view = show({ flow: board() });
    const labelled = (id: string): string[] =>
      cells(id).map((el) => el.getAttribute("aria-label") ?? "");
    expect(cells("3")[0]).toHaveTextContent("dev?");
    expect(cells("3")[0]).toHaveAttribute("aria-label", "dev: asks");
    expect(cells("2")[3]).toHaveTextContent("—");
    expect(cells("2")[3]).toHaveAttribute("aria-label", "fix: skipped");
    expect(cells("1").map((el) => el.textContent)).toEqual(["dev", "gate", "rev", "fix", "doc"]);
    expect(cells("P").map((el) => el.textContent)).toEqual(["prem"]);
    expect(labelled("P")).toEqual(["premise: passed"]);
    expect(cells("F").map((el) => el.textContent)).toEqual(["gate", "rev", "fix"]);
    expect(labelled("F")).toEqual(["gates: passed", "review: running", "fix: todo"]);
    view.unmount();

    show({
      flow: board((f) => {
        const one = f.stories?.[0];
        if (one !== undefined) one.cells = null;
      }),
    });
    expect(labelled("1")).toEqual([
      "dev: todo",
      "gates: todo",
      "review: todo",
      "fix: todo",
      "docs: todo",
    ]);
  });

  it("a cell with an unknown state draws its raw state in neutral styling", () => {
    show({
      flow: board((f) => {
        const cell = f.stories?.[0]?.cells;
        if (cell != null) (cell as Record<string, string>).dev = "weird";
      }),
    });
    const dev = cells("1")[0];
    expect(dev).toHaveTextContent("weird");
    expect(dev).toHaveAttribute("aria-label", "dev: weird");
    expect(dev).toHaveClass("text-muted-foreground");
    expect(dev).not.toHaveClass("text-working");
  });
});

describe("FlowBody cost box and other runs", () => {
  // costs changes the canned board's costs and draws it.
  const withCosts = (over: (costs: Flow["costs"], flow: Flow) => void): RenderResult =>
    show({ flow: board((f) => over(f.costs, f)) });
  const box = (): HTMLElement => {
    const legend = screen.getByText(/^cost/);
    if (legend.parentElement === null) throw new Error("the legend has no box");
    return legend.parentElement;
  };
  const segments = (): HTMLElement[] =>
    Array.from(document.querySelectorAll<HTMLElement>('[data-slot="role-segment"]'));
  const bars = (): HTMLElement[] =>
    Array.from(document.querySelectorAll<HTMLElement>('[data-slot="bar"]'));
  const caption = (): string | undefined =>
    Array.from(document.querySelectorAll("p"))
      .find((p) => p.textContent?.startsWith("per story"))
      ?.textContent?.trim();

  it("the cost box: the run's spend, the whole flow, the projection and the list-price legend", () => {
    show({ flow: board() });
    expect(screen.getByText("cost · list prices")).toBeInTheDocument();
    expect(box()).toHaveTextContent("run $4.71");
    expect(box()).toHaveTextContent("flow $19.90");
    expect(box()).toHaveTextContent("~$9.80 projected");
    expect(box()).not.toHaveTextContent("of 3 parts");
  });

  it("the cost box: a run still to come, a flow priced from fewer parts, a projection that is missing and why", () => {
    const view = show({ flow: board((f) => (f.summary.run = "")) });
    expect(box()).toHaveTextContent("no run yet");
    expect(box()).not.toHaveTextContent("list pricesrun");
    view.unmount();

    const parts = withCosts((c) => (c.flow_parts = 2));
    expect(box()).toHaveTextContent("flow $19.90 (2 of 3 parts)");
    parts.unmount();

    const none = withCosts((c) => (c.flow_parts = 0));
    expect(box()).not.toHaveTextContent("flow $");
    none.unmount();

    const why = withCosts((c) => {
      c.projection = null;
      c.why = "no history";
    });
    expect(box()).toHaveTextContent("· no history");
    expect(box()).not.toHaveTextContent("projected");
    why.unmount();

    show({
      flow: board((f) => {
        f.summary.run = "";
        f.costs.projection = null;
        f.costs.why = "no history";
      }),
    });
    expect(box()).not.toHaveTextContent("no history");
  });

  it("the cost box: a role bar of exact shares, a segment for each role class and a legend in order", () => {
    const view = show({ flow: board() });
    expect(segments().map((el) => el.getAttribute("data-role"))).toEqual([
      "dev",
      "review",
      "manager",
      "other",
    ]);
    [2.1, 1.5, 0.99, 0.12].forEach((sum, i) => {
      expect(parseFloat(segments()[i]?.style.width ?? "")).toBeCloseTo((sum / 4.71) * 100, 3);
    });
    const text = box().textContent ?? "";
    const keys = ["■ dev $2.10", "■ review $1.50", "■ manager $0.99", "■ other $0.12"];
    expect(keys.map((k) => text.indexOf(k))).toEqual(
      keys.map((k) => text.indexOf(k)).sort((a, b) => a - b),
    );
    for (const k of keys) expect(screen.getByText(k)).toBeInTheDocument();
    view.unmount();

    withCosts((c) => {
      if (c.by_role !== null) c.by_role["mystery-agent"] = 1;
    });
    expect(screen.getByText("■ other $1.12")).toBeInTheDocument();
  });

  it("the cost box: no bar for no spend, and zero roles left out", () => {
    const view = withCosts((c) => (c.by_role = null));
    expect(segments()).toHaveLength(0);
    expect(screen.queryByText(/^■/)).toBeNull();
    expect(box()).toHaveTextContent("run $4.71");
    view.unmount();

    withCosts((c) => (c.by_role = { manager: 0 }));
    expect(segments()).toHaveLength(0);
    expect(screen.queryByText(/^■/)).toBeNull();
    expect(box()).toHaveTextContent("run $4.71");
  });

  it("the cost box: the unpriced line counts the agents still running", () => {
    const view = show({ flow: board() });
    expect(screen.getByText("not priced yet: 1 running")).toBeInTheDocument();
    view.unmount();
    for (const unpriced of [[], null]) {
      const empty = withCosts((c) => (c.unpriced = unpriced));
      expect(screen.queryByText(/not priced yet/)).toBeNull();
      empty.unmount();
    }
    withCosts((c) => (c.unpriced = ["a", "b", "c"]));
    expect(screen.getByText("not priced yet: 3 running")).toBeInTheDocument();
  });

  it("the cost box: the per-story comparison draws its bars and its caption", () => {
    const view = show({ flow: board() });
    expect(bars()).toHaveLength(6);
    expect(bars()[5]).toHaveAttribute("data-current", "true");
    expect(caption()).toBe("per story: last 5 runs, median $2.52 · this run $2.36");
    view.unmount();

    const noMedian = withCosts((c) => (c.median = null));
    expect(caption()).toBe("per story: last 5 runs · this run $2.36");
    noMedian.unmount();

    withCosts((c) => (c.this = null));
    expect(caption()).toBe("per story: last 5 runs, median $2.52");
    expect(bars()).toHaveLength(5);
  });

  it("the cost box: under two values no comparison draws", () => {
    const cases: [number[] | null, number | null, number][] = [
      [[], null, 0],
      [[1.5], null, 0],
      [[1.5], 2.36, 2],
      [null, 2.36, 0],
    ];
    for (const [past, now, count] of cases) {
      const view = withCosts((c) => {
        c.past = past;
        c.this = now;
      });
      expect(bars()).toHaveLength(count);
      if (count === 0) expect(caption()).toBeUndefined();
      view.unmount();
    }
  });

  it("the cost box: a cost error is all it says", () => {
    withCosts((c) => (c.error = "no cost records"));
    expect(screen.getByText("cost")).toBeInTheDocument();
    expect(screen.getByText("no cost records")).toBeInTheDocument();
    expect(box()).not.toHaveTextContent("run");
    expect(box()).not.toHaveTextContent("flow");
    expect(segments()).toHaveLength(0);
    expect(bars()).toHaveLength(0);
    expect(screen.queryByText(/^■/)).toBeNull();
  });

  it("the other runs: a box of each other run's id and status, unreadable when it is, none without", () => {
    const view = show({ flow: board() });
    expect(screen.getByText("other runs")).toBeInTheDocument();
    expect(screen.getByText("2026-11-08-flow-visualisation")).toBeInTheDocument();
    expect(screen.getByText("stopped")).toBeInTheDocument();
    view.unmount();

    const bad = show({
      flow: board((f) => {
        const run = f.others?.[0];
        if (run !== undefined) run.unreadable = "bad";
      }),
    });
    expect(screen.getByText("unreadable")).toBeInTheDocument();
    expect(screen.queryByText("stopped")).toBeNull();
    bad.unmount();

    for (const others of [[], null]) {
      const empty = show({ flow: board((f) => (f.others = others)) });
      expect(screen.queryByText("other runs")).toBeNull();
      empty.unmount();
    }

    show({
      flow: board((f) => {
        const run = f.others?.[0];
        if (run !== undefined) run.status = "weird";
      }),
    });
    expect(screen.getByText("weird")).toBeInTheDocument();
  });
});

describe("the flow panel", () => {
  // askingOnly is session 6 with only the flow at `index` asking (none for undefined).
  const askingOnly = (index?: number): Session => {
    const session = session6();
    return {
      ...session,
      flows: (session.flows ?? []).map((f, i) => ({
        ...f,
        asks: i === index ? "blocked-decision" : "",
      })),
    };
  };

  it("FlowMark: an amber dot while any flow asks, nothing otherwise", () => {
    const view = render(<FlowMark session={session6()} />);
    const dot = screen.getByRole("img", { name: "asks" });
    expect(dot).toHaveClass("bg-attention");
    view.unmount();

    const quiet = render(<FlowMark session={askingOnly()} />);
    expect(quiet.container).toBeEmptyDOMElement();
    quiet.unmount();

    const none = render(<FlowMark session={session6({ flows: null })} />);
    expect(none.container).toBeEmptyDOMElement();
    none.unmount();

    render(<FlowMark session={askingOnly(1)} />);
    expect(screen.getByRole("img", { name: "asks" })).toBeInTheDocument();
  });

  it("the flow panel: the sixth tab for a session with flows, under the session's head, its body a lazy chunk", () => {
    expect(panelFor("flow")).toBe(flowPanel);
    expect(flowPanel.tab).toEqual({ order: 6, label: "flow", Mark: FlowMark });
    expect(flowPanel.Head).toBe(SessionHead);
    expect(flowPanel.appliesTo?.(session6(), { terminal: false })).toBe(true);
    expect(flowPanel.appliesTo?.(session6({ flows: [] }), { terminal: false })).toBe(false);
    expect(flowPanel.appliesTo?.(session6({ flows: null }), { terminal: false })).toBe(false);
  });

  it("the session's tabs: the flow tab is last, links to the board with no plan and lights on the board", () => {
    const session = session6();
    seed([session]);
    window.history.replaceState(null, "", "#/s/6/flow");
    const view = render(<SessionTabs session={session} />);
    const links = within(screen.getByRole("navigation", { name: "Session views" })).getAllByRole(
      "link",
    );
    const last = links[links.length - 1];
    expect(last).toHaveTextContent("flow");
    expect(last).toHaveAttribute("href", "#/s/6/flow");
    expect(last).toHaveAttribute("aria-current", "page");
    expect(within(last).getByRole("img", { name: "asks" })).toBeInTheDocument();
    view.unmount();

    window.history.replaceState(null, "", "#/s/6/info");
    const info = render(<SessionTabs session={session} />);
    const tabs = within(screen.getByRole("navigation", { name: "Session views" })).getAllByRole(
      "link",
    );
    expect(tabs[tabs.length - 1]).not.toHaveAttribute("aria-current");
    info.unmount();

    const flowless = render(<SessionTabs session={session6({ flows: [] })} />);
    expect(screen.queryByRole("link", { name: /^flow/ })).toBeNull();
    flowless.unmount();
  });

  it("the flow panel draws the board through PanelHost once its chunk loads", async () => {
    const flow = board();
    seed([session6()]);
    vi.mocked(useFlow).mockReturnValue({ flow, error: "", loading: false });
    window.history.replaceState(null, "", "#/s/6/flow");
    const props: PanelProps = { sessionId: 6, params: { id: "6" }, search: {} };
    render(<PanelHost panel="flow" props={props} />);
    expect(await screen.findByText(flow.summary.title, { selector: "p" })).toBeInTheDocument();
    expect(screen.getByText("stories")).toBeInTheDocument();
  });
});

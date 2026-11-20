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

// board is the canned board; a test changes what it needs. The fixture's run stage is in state
// "now", which fillTimes reads as a time and fills in, so it is put back here.
function board(over: (flow: Flow) => void = () => undefined): Flow {
  const flow = fixtureFlow();
  const run = flow.stages?.find((s) => s.name === "run");
  if (run !== undefined) run.state = "now";
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

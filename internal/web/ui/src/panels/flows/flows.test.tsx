import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Flows, FlowSummary, PastRun } from "@/api/gen";
import { useFlows } from "@/api/reads";
import { panelFor } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { PanelHost } from "@/app/shell";
import { fixtureFlows } from "@/fixtures";

import "@/panels";
import { flowTone } from "../fleet/SessionRow";
import FlowsBody from "./FlowsBody";
import { FlowsHead, flowsPanel } from "./index";

vi.mock("@/api/reads", async (original) => ({
  ...(await original<typeof import("@/api/reads")>()),
  useFlows: vi.fn(),
}));

const NOW = Date.parse("2026-11-19T12:00:00Z");

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function show(opts: { flows?: Flows; error?: string; loading?: boolean }) {
  vi.mocked(useFlows).mockReturnValue({
    flows: opts.flows,
    error: opts.error ?? "",
    loading: opts.loading ?? false,
    reload: vi.fn(),
  });
  return render(<FlowsBody />);
}

describe("FlowsBody", () => {
  it("draws Loading… until the flows arrive, and the read's error when none is held", () => {
    const view = show({ loading: true });
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    view.unmount();
    show({ error: "loom answered 500" });
    expect(screen.getByRole("alert")).toHaveTextContent("loom answered 500");
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it("keeps the held flows drawn under a failed read's error", () => {
    show({ flows: fixtureFlows(), error: "loom answered 500" });
    expect(screen.getByRole("alert")).toHaveTextContent("loom answered 500");
    expect(screen.getByText("flow-run")).toBeInTheDocument();
  });

  it("the live box: each flow a link to its board with its title, part and chip", () => {
    const flows = fixtureFlows();
    const first = flows.live?.[0];
    if (first === undefined) throw new Error("fixture has no live flow");
    first.flow.part = "part 2 of 3";
    show({ flows });
    expect(screen.getByText("live")).toBeInTheDocument();
    const links = screen
      .getAllByRole("link")
      .filter((a) => (a.getAttribute("href") ?? "").includes("/flow"));
    expect(links).toHaveLength(2);
    expect(links[0]).toHaveAttribute(
      "href",
      hrefFor(
        "flow",
        { id: "6" },
        { plan: "docs/superpowers/plans/2026-11-09-flow-visualisation.md" },
      ),
    );
    expect(within(links[0] as HTMLElement).getByText("flow-run")).toBeInTheDocument();
    expect(links[0]).toHaveTextContent("part 2 of 3");
    expect(links[0]).toHaveTextContent("flow 2/5 · asks · $4.71");
    expect(links[1]).toHaveAttribute(
      "href",
      hrefFor(
        "flow",
        { id: "4" },
        { plan: "docs/superpowers/plans/2026-11-08-job-exploration.md" },
      ),
    );
    expect(links[1]).toHaveTextContent("flow-docs");
    expect(links[1]).toHaveTextContent("flow · draft 3/4");
    expect(links[1]).not.toHaveTextContent("part 2 of 3");
  });

  it("the live chips take the fleet row's tones", () => {
    const flows = fixtureFlows();
    const base = flows.live?.[0];
    if (base === undefined) throw new Error("fixture has no live flow");
    flows.live = [
      ...(flows.live ?? []),
      { ...base, flow: { ...base.flow, asks: "", status: "lost" } },
      { ...base, flow: { ...base.flow, asks: "", unreadable: "bad plan" } },
    ];
    show({ flows });
    const tones = Array.from(document.querySelectorAll("[data-tone]")).map((e) =>
      e.getAttribute("data-tone"),
    );
    expect(tones).toEqual(["attention", "docs", "error", "error"]);
  });

  it("flowTone: attention while it asks, error when lost or unreadable, else docs", () => {
    const base: FlowSummary = (() => {
      const f = fixtureFlows().live?.[0]?.flow;
      if (f === undefined) throw new Error("fixture has no live flow");
      return f;
    })();
    expect(flowTone({ ...base, asks: "blocked-decision" })).toBe("attention");
    expect(flowTone({ ...base, asks: "", status: "lost" })).toBe("error");
    expect(flowTone({ ...base, asks: "", unreadable: "bad" })).toBe("error");
    expect(flowTone({ ...base, asks: "", status: "running", unreadable: "" })).toBe("docs");
    expect(flowTone({ ...base, asks: "blocked-decision", status: "lost" })).toBe("attention");
  });

  it("with no live flow the box says so", () => {
    for (const live of [[], null]) {
      const flows = { ...fixtureFlows(), live };
      const view = show({ flows });
      expect(screen.getByText("live")).toBeInTheDocument();
      expect(screen.getByText("No session's worktree holds a flow.")).toBeInTheDocument();
      expect(
        screen
          .queryAllByRole("link")
          .filter((a) => (a.getAttribute("href") ?? "").includes("/flow")),
      ).toHaveLength(0);
      view.unmount();
    }
  });
});

const KEY = "-tb-Source-Personal-loom";
const iso = (hoursAgo: number): string => new Date(NOW - hoursAgo * 3600_000).toISOString();

function run(over: Partial<PastRun> = {}): PastRun {
  return {
    key: KEY,
    file: "r.md",
    stem: "2026-01-01-r",
    plan: "p.md",
    stories: 2,
    ended: "done",
    end: iso(1),
    usd: 4,
    unreadable: "",
    ...over,
  };
}

// runs makes n runs of one repo, newest first, named r0, r1, …
function runs(n: number, over: (i: number) => Partial<PastRun> = () => ({})): PastRun[] {
  return Array.from({ length: n }, (_, i) =>
    run({ stem: `2026-01-01-r${i}`, end: iso(i + 1), ...over(i) }),
  );
}

const legends = (): string[] => screen.queryAllByText(/^past · /).map((e) => e.textContent ?? "");

function box(name: string): HTMLElement {
  const legend = screen.getByText(`past · ${name}`);
  const el = legend.parentElement;
  if (el === null) throw new Error("no box");
  return el;
}

const heights = (el: HTMLElement): number[] =>
  Array.from(el.querySelectorAll<HTMLElement>('[data-slot="bar"]')).map((b) =>
    parseFloat(b.style.height),
  );

describe("the past runs", () => {
  it("one past box for each repo, in first-seen order, named by the part after -Source-", () => {
    const flows = fixtureFlows();
    const view = show({ flows });
    expect(legends()).toEqual(["past · Personal-loom", "past · Academia-kermit"]);
    view.unmount();
    show({ flows: { ...flows, past: [...(flows.past ?? [])].reverse() } });
    expect(legends()).toEqual(["past · Academia-kermit", "past · Personal-loom"]);
    cleanup();
    show({ flows: { live: null, past: [run({ key: "-tb-elsewhere" })] } });
    expect(legends()).toEqual(["past · tb-elsewhere"]);
  });

  it("a run's row: its stem without the date, then stories, cost, how it ended and its age", () => {
    show({ flows: fixtureFlows() });
    const rows = (el: HTMLElement): string[] =>
      Array.from(el.querySelectorAll("div.min-h-11")).map((r) => r.textContent ?? "");
    expect(rows(box("Personal-loom"))).toEqual([
      "pwa-conversation-first4 st · $10.08 · stopped · 3d",
      "work-scheduler6 st · $14.20 · done · 5d",
      "loom-pwa9 st · $31.77 · supervised · 17d",
    ]);
    expect(rows(box("Academia-kermit"))).toEqual([
      "per-query-index-build23 st · $58.40 · supervised · 18d",
    ]);
  });

  it("a run with no cost or no end leaves that part out, and an unreadable run says only that", () => {
    show({
      flows: {
        live: null,
        past: [
          run({ stem: "2026-01-01-a", usd: null, end: "" }),
          run({ stem: "2026-01-01-b", unreadable: "bad record" }),
        ],
      },
    });
    expect(screen.getByText("2 st · done")).toBeInTheDocument();
    const bad = screen.getByText("unreadable");
    expect(bad).toHaveClass("text-error");
    expect(bad.parentElement).not.toHaveTextContent("st");
    expect(bad.parentElement).not.toHaveTextContent("$");
  });

  it("8 runs are shown and the rest are counted", () => {
    const rowsOf = (): number => document.querySelectorAll("div.min-h-11").length;
    show({ flows: { live: null, past: runs(10) } });
    expect(rowsOf()).toBe(8);
    expect(screen.getByText("and 2 more")).toBeInTheDocument();
    cleanup();
    show({ flows: { live: null, past: runs(8) } });
    expect(rowsOf()).toBe(8);
    expect(screen.queryByText(/^and /)).toBeNull();
    cleanup();
    show({ flows: { live: null, past: runs(9) } });
    expect(screen.getByText("and 1 more")).toBeInTheDocument();
  });

  it("the bars: the last 5 priced runs, oldest first, with their median", () => {
    show({ flows: fixtureFlows() });
    const h = heights(box("Personal-loom"));
    expect(h).toHaveLength(3);
    expect(h[0]).toBeCloseTo(100, 1);
    expect(h[1]).toBeCloseTo(67.04, 1);
    expect(h[2]).toBeCloseTo(71.39, 1);
    expect(heights(box("Academia-kermit"))).toHaveLength(0);
    expect(screen.getByText("run cost per story, last 3 runs · median $2.52")).toBeInTheDocument();
  });

  it("the bars take the newest 5 priced runs, and skip a run with no price or no stories", () => {
    show({ flows: { live: null, past: runs(7) } });
    expect(heights(box("Personal-loom"))).toHaveLength(5);
    expect(screen.getByText(/last 5 runs/)).toBeInTheDocument();
    cleanup();
    show({
      flows: {
        live: null,
        past: runs(4, (i) => (i === 0 ? { usd: null } : i === 1 ? { stories: 0 } : {})),
      },
    });
    expect(heights(box("Personal-loom"))).toHaveLength(2);
    expect(screen.getByText(/last 2 runs/)).toBeInTheDocument();
    cleanup();
    show({ flows: { live: null, past: runs(3, (i) => (i === 0 ? {} : { usd: null })) } });
    expect(heights(box("Personal-loom"))).toHaveLength(0);
    expect(screen.queryByText(/run cost per story/)).toBeNull();
  });

  it("the bars are drawn from every run, not only the 8 shown", () => {
    show({
      flows: {
        live: null,
        past: runs(10, (i) => (i < 8 ? { usd: null } : { usd: i === 8 ? 2 : 6, stories: 2 })),
      },
    });
    expect(heights(box("Personal-loom"))).toHaveLength(2);
    expect(screen.getByText("run cost per story, last 2 runs · median $2.00")).toBeInTheDocument();
  });

  it("no run records says so", () => {
    for (const past of [[], null]) {
      const view = show({ flows: { ...fixtureFlows(), past } });
      expect(screen.getByText("No run records yet.")).toBeInTheDocument();
      expect(legends()).toEqual([]);
      view.unmount();
    }
    show({ flows: fixtureFlows() });
    expect(screen.queryByText("No run records yet.")).toBeNull();
  });
});

describe("the flows panel", () => {
  it("FlowsHead: a way back to the fleet, the title, and a button that reads the flows again", async () => {
    const reload = vi.fn();
    vi.mocked(useFlows).mockReturnValue({ flows: undefined, error: "", loading: true, reload });
    render(<FlowsHead />);
    expect(screen.getByRole("link", { name: "‹ fleet" })).toHaveAttribute("href", hrefFor("fleet"));
    expect(screen.getByRole("heading", { name: "Flows" })).toBeInTheDocument();
    const button = screen.getByRole("button", { name: "Read the flows again" });
    expect(button).toHaveClass("min-h-11");
    expect(button).toHaveClass("min-w-11");
    await userEvent.setup({ advanceTimers: vi.advanceTimersByTime }).click(button);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("FlowsHead and FlowsBody both read through useFlows", () => {
    const reload = vi.fn();
    vi.mocked(useFlows).mockReturnValue({
      flows: fixtureFlows(),
      error: "",
      loading: false,
      reload,
    });
    render(
      <>
        <FlowsHead />
        <FlowsBody />
      </>,
    );
    expect(vi.mocked(useFlows).mock.calls.length).toBeGreaterThanOrEqual(2);
    expect(vi.mocked(useFlows).mock.results.every((r) => r.value.reload === reload)).toBe(true);
  });

  it("the panel is registered under flows with its head, a lazy body and no tab", () => {
    expect(panelFor("flows")).toBe(flowsPanel);
    expect(flowsPanel.Head).toBe(FlowsHead);
    expect(flowsPanel.tab).toBeUndefined();
    expect(flowsPanel.appliesTo).toBeUndefined();
  });

  it("the flows panel draws its body through PanelHost once its chunk loads", async () => {
    vi.mocked(useFlows).mockReturnValue({
      flows: fixtureFlows(),
      error: "",
      loading: false,
      reload: vi.fn(),
    });
    render(<PanelHost panel="flows" props={{ sessionId: undefined, params: {}, search: {} }} />);
    await screen.findByText("flow-run");
    expect(screen.getByText("live")).toBeInTheDocument();
  });
});

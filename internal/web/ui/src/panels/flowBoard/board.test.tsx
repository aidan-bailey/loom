import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { FlowSummary, Snapshot } from "@/api/gen";
import { panelFor } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { PanelHost } from "@/app/shell";
import { useLayout, type Layout } from "@/app/viewport";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import "@/panels";
import FlowBoardBody from "./FlowBoardBody";
import { FlowBoardHead, flowBoardPanel } from "./index";

vi.mock("@/app/viewport", async (original) => ({
  ...(await original<typeof import("@/app/viewport")>()),
  useLayout: vi.fn(),
}));

const NOW = Date.parse("2026-11-19T12:00:00Z");
const PLAN = "docs/superpowers/plans/2026-11-09-flow-visualisation.md";

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
});

// show draws the board over a snapshot (the canned fleet by default) in a layout.
function show(
  opts: { snapshot?: Snapshot | null; layout?: Layout; stale?: boolean } = {},
): ReturnType<typeof render> {
  const snapshot = opts.snapshot === undefined ? fixtureSnapshot(NOW) : opts.snapshot;
  const stale = opts.stale ?? false;
  vi.mocked(useLayout).mockReturnValue(opts.layout ?? "desk3");
  useFleet.setState({ snapshot, stale, link: stale ? "offline" : "live", updated: NOW });
  return render(<FlowBoardBody />);
}

// withFlows is the canned fleet with only session 6 holding flows: its first one with each of
// `overs` on top.
function withFlows(overs: Partial<FlowSummary>[]): Snapshot {
  const snapshot = fixtureSnapshot(NOW);
  const base = allSessions(snapshot).find((s) => s.id === 6)?.flows?.[0];
  if (base === undefined) throw new Error("fixture has no flow");
  for (const s of allSessions(snapshot)) {
    s.flows = s.id === 6 ? overs.map((over) => ({ ...base, ...over })) : null;
  }
  return snapshot;
}

const columns = (): string[] =>
  screen.queryAllByRole("group").map((g) => g.getAttribute("aria-label") ?? "");
const legend = (name: string): string =>
  screen.getByRole("group", { name }).querySelector("p")?.textContent ?? "";

describe("FlowBoardBody", () => {
  it("draws Loading… until a snapshot arrives", () => {
    show({ snapshot: null });
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    expect(columns()).toEqual([]);
  });

  it("with no flow says so", () => {
    show({ snapshot: withFlows([]) });
    expect(screen.getByText("No session's worktree holds a flow.")).toBeInTheDocument();
    expect(columns()).toEqual([]);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("on desktop draws all six columns with their counts, empty ones included", () => {
    for (const layout of ["desk2", "desk3"] as const) {
      const view = show({ layout });
      expect(columns()).toEqual(["design", "spec", "draft", "run", "record", "finished"]);
      expect(legend("design")).toBe("design · 0");
      expect(legend("spec")).toBe("spec · 1");
      expect(legend("run")).toBe("run · 1");
      expect(legend("finished")).toBe("finished · 0");
      view.unmount();
    }
  });

  it("on the phone leaves the empty columns out", () => {
    show({ layout: "phone" });
    expect(columns()).toEqual(["spec", "run"]);
  });

  it("a card: the session's title, its repo, its part, its state line, its cost and age, and a link to its flow", () => {
    show({ snapshot: withFlows([{ part: "part 2 of 3" }]) });
    const card = within(screen.getByRole("group", { name: "run" })).getByRole("link");
    expect(card).toHaveAttribute("href", hrefFor("flow", { id: "6" }, { plan: PLAN }));
    const lines = Array.from(card.children).map((c) => c.textContent);
    expect(lines).toEqual([
      "flow-run",
      "flow",
      "part 2 of 3",
      "2/5 · asks",
      "blocked-decision",
      "$4.71 · 4m",
    ]);
    expect(card).toHaveClass("min-h-[44px]");
  });

  it("a card with no part, question or cost leaves each out", () => {
    show({ snapshot: withFlows([{ asks: "", status: "running", phase: "review", spent: null }]) });
    const card = within(screen.getByRole("group", { name: "run" })).getByRole("link");
    expect(Array.from(card.children).map((c) => c.textContent)).toEqual([
      "flow-run",
      "flow",
      "2/5 · review",
      "4m",
    ]);
  });

  it("a card takes the chip's tone", () => {
    show({
      snapshot: withFlows([
        { plan: "asks.md" },
        { plan: "lost.md", asks: "", status: "lost" },
        { plan: "plain.md", asks: "", status: "running" },
      ]),
    });
    const tone = (plan: string): string | null | undefined =>
      screen
        .getAllByRole("link")
        .find((a) => a.getAttribute("href") === hrefFor("flow", { id: "6" }, { plan }))
        ?.getAttribute("data-tone");
    expect(tone("asks.md")).toBe("attention");
    expect(tone("lost.md")).toBe("error");
    expect(tone("plain.md")).toBe("docs");
  });

  it("an unreadable flow says so", () => {
    show({ snapshot: withFlows([{ asks: "", unreadable: "bad plan" }]) });
    const card = screen.getByRole("link");
    expect(within(card).getByText("unreadable")).toHaveClass("text-error");
    expect(card).toHaveAttribute("data-tone", "error");
  });

  it("finished holds the ended flows, dimmed; the stage columns are not", () => {
    show({ snapshot: withFlows([{ plan: "done.md", asks: "", status: "done" }, {}]) });
    const finished = screen.getByRole("group", { name: "finished" });
    expect(within(finished).getAllByRole("link")).toHaveLength(1);
    expect(finished).toHaveClass("opacity-60");
    expect(screen.getByRole("group", { name: "run" })).not.toHaveClass("opacity-60");
    expect(within(screen.getByRole("group", { name: "run" })).getAllByRole("link")).toHaveLength(1);
  });

  it("a stale link dims the board and keeps the cards links", () => {
    const view = show({ stale: true });
    expect(screen.getByRole("region", { name: "Flow board" })).toHaveClass("opacity-50");
    expect(screen.getAllByRole("link")).toHaveLength(2);
    view.unmount();
    show({ stale: false });
    expect(screen.getByRole("region", { name: "Flow board" })).not.toHaveClass("opacity-50");
  });
});

describe("the flow board panel", () => {
  it("the head: a way back to the fleet, the title and a link to the Flows screen", () => {
    render(<FlowBoardHead />);
    expect(screen.getByRole("link", { name: "‹ fleet" })).toHaveAttribute("href", hrefFor("fleet"));
    expect(screen.getByRole("heading", { name: "Flow board" })).toBeInTheDocument();
    const flows = screen.getByRole("link", { name: "Flows" });
    expect(flows).toHaveAttribute("href", hrefFor("flows"));
    expect(flows).toHaveClass("min-h-[44px]");
  });

  it("is registered under flowBoard with its head, a lazy body and no tab", () => {
    expect(panelFor("flowBoard")).toBe(flowBoardPanel);
    expect(flowBoardPanel.Head).toBe(FlowBoardHead);
    expect(flowBoardPanel.tab).toBeUndefined();
    expect(flowBoardPanel.appliesTo).toBeUndefined();
  });

  it("draws its body through PanelHost once its chunk loads", async () => {
    vi.mocked(useLayout).mockReturnValue("desk3");
    useFleet.setState({ snapshot: fixtureSnapshot(NOW), stale: false, link: "live", updated: NOW });
    render(
      <PanelHost panel="flowBoard" props={{ sessionId: undefined, params: {}, search: {} }} />,
    );
    await screen.findByRole("region", { name: "Flow board" });
    expect(columns()).toHaveLength(6);
  });
});

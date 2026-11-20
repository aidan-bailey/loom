import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Flows, FlowSummary } from "@/api/gen";
import { useFlows } from "@/api/reads";
import { hrefFor } from "@/app/routes";
import { fixtureFlows } from "@/fixtures";

import { flowTone } from "../fleet/SessionRow";
import FlowsBody from "./FlowsBody";

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

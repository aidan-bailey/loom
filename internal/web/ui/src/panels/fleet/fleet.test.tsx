import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { FlowSummary, Pr, Session, Snapshot, Usage } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import { ago, flowChip, SessionRow } from "./SessionRow";

const NOW = Date.parse("2026-11-18T12:00:00Z");

function resetStore(): void {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
}

function load(snapshot: Snapshot): void {
  useFleet.getState().apply("snapshot", snapshot);
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  resetStore();
});

afterEach(() => {
  vi.useRealTimers();
  resetStore();
  window.localStorage.clear();
});

describe("SessionRow", () => {
  const snapshot = fixtureSnapshot(NOW);
  const usage = snapshot.usage ?? [];
  const byId = (id: number): Session => allSessions(snapshot).find((s) => s.id === id) as Session;
  const row = (session: Session, props: { usage?: Usage[]; chip?: string } = {}) => {
    load(snapshot);
    return render(<SessionRow session={session} usage={props.usage ?? usage} chip={props.chip} />)
      .container;
  };
  const link = () => screen.getByRole("link");

  const flow = (over: Partial<FlowSummary> = {}): FlowSummary => ({
    ...(byId(6).flows as FlowSummary[])[0],
    ...over,
  });

  it("ago words a time as now, minutes, hours and days", () => {
    const at = (ms: number) => new Date(NOW - ms).toISOString();
    expect(ago(at(30_000), NOW)).toBe("now");
    expect(ago(at(59_000), NOW)).toBe("now");
    expect(ago(at(60_000), NOW)).toBe("1m");
    expect(ago(at(59 * 60_000), NOW)).toBe("59m");
    expect(ago(at(60 * 60_000), NOW)).toBe("1h");
    expect(ago(at(47 * 3_600_000), NOW)).toBe("47h");
    expect(ago(at(48 * 3_600_000), NOW)).toBe("2d");
    expect(ago(at(100 * 3_600_000), NOW)).toBe("4d");
  });

  it("ago is empty for a time it cannot read", () => {
    expect(ago("", NOW)).toBe("");
    expect(ago("soon", NOW)).toBe("");
  });

  it("flowChip words a flow by its stage and status", () => {
    const run = {
      stage: "run",
      total: 5,
      done: 2,
      asks: "",
      phase: "reviewing",
      spent: 4.71,
    } as const;
    expect(flowChip(flow({ ...run, stage: "design" }))).toBe("flow · design");
    expect(flowChip(flow({ ...run, stage: "spec" }))).toBe("flow · design");
    expect(
      flowChip(flow({ ...run, stage: "draft", status: "drafting", drafted: 3, total: 4 })),
    ).toBe("flow · draft 3/4");
    expect(flowChip(flow({ ...run, stage: "draft", status: "drafted" }))).toBe("flow · drafted");
    expect(flowChip(flow({ ...run, stage: "draft", status: "designed" }))).toBe("flow · designed");
    expect(flowChip(flow({ ...run, status: "running" }))).toBe("flow 2/5 · reviewing · $4.71");
    expect(flowChip(flow({ ...run, status: "parked", asks: "blocked-decision" }))).toBe(
      "flow 2/5 · asks · $4.71",
    );
    expect(flowChip(flow({ ...run, status: "lost", spent: null }))).toBe("flow 2/5 · lost");
    expect(flowChip(flow({ ...run, status: "running", phase: "", spent: null }))).toBe(
      "flow 2/5 · running",
    );
  });

  it("flowChip leaves out an empty status and a null spend, and writes a small spend with four decimals", () => {
    const run = { stage: "run", total: 5, done: 2, asks: "", phase: "" } as const;
    expect(flowChip(flow({ ...run, status: "", spent: null }))).toBe("flow 2/5");
    expect(flowChip(flow({ ...run, status: "done", spent: 0.004 }))).toBe(
      "flow 2/5 · done · $0.0040",
    );
  });

  it("shows the title, wait reason and no last message on a needs-you row", () => {
    const s = { ...byId(4), last_message: "Something said last" };
    row(s);
    expect(within(link()).getByText("flow-docs")).toBeInTheDocument();
    expect(within(link()).getByText("Plan approval")).toBeInTheDocument();
    expect(screen.queryByText("Something said last")).not.toBeInTheDocument();
  });

  it("shows the wait detail under a needs-you row's reason", () => {
    row(byId(2));
    expect(screen.getByText("Allow Bash")).toBeInTheDocument();
    expect(screen.getByText("go test ./ui/... -run TestPane")).toBeInTheDocument();
  });

  it("shows every part of a working row", () => {
    row(byId(1));
    expect(screen.getByText("fix-pane-cursor")).toBeInTheDocument();
    expect(screen.getByText("62%")).toBeInTheDocument();
    expect(screen.getByText("2m")).toBeInTheDocument();
    expect(screen.getByText(/^Running the ui tests now\./)).toBeInTheDocument();
    expect(screen.getByText("+212")).toBeInTheDocument();
    expect(screen.getByText("−40")).toBeInTheDocument();
    expect(screen.getByText("1 agent")).toBeInTheDocument();
    expect(screen.getByText("PR #482")).toBeInTheDocument();
    expect(screen.getByText("…")).toBeInTheDocument();
    expect(screen.getByText("⚙ job 214")).toBeInTheDocument();
    expect(screen.queryByText("Allow Bash")).not.toBeInTheDocument();
  });

  it("shows another account's share, a merged PR and the flow chip", () => {
    row(byId(6));
    expect(screen.getByText("18%")).toBeInTheDocument();
    expect(screen.getByText("PR #31")).toBeInTheDocument();
    expect(screen.getByText("merged")).toBeInTheDocument();
    expect(screen.queryByText("✓")).not.toBeInTheDocument();
    expect(screen.getByText("flow 2/5 · asks · $4.71")).toHaveAttribute("data-tone", "attention");
  });

  it("shows no share for a session that is not Claude", () => {
    row(byId(3));
    expect(link().textContent).not.toContain("%");
  });

  it("tints the share amber from 80 percent", () => {
    const hot = usage.map((u) =>
      u.account === "default" ? { ...u, five_hour: { ...u.five_hour!, pct: 85 } } : u,
    );
    const { unmount } = render(<SessionRow session={byId(1)} usage={hot} />);
    expect(screen.getByText("85%")).toHaveAttribute("data-amber", "true");
    unmount();
    render(<SessionRow session={byId(1)} usage={usage} />);
    expect(screen.getByText("62%")).not.toHaveAttribute("data-amber");
  });

  it("marks an open PR's checks", () => {
    const withPr = (pr: Pr): Session => ({ ...byId(1), pr });
    const checks = (c: Pr["checks"]) => withPr({ number: 9, state: "open", checks: c });
    const cases: [Session, string | null][] = [
      [checks("passing"), "✓"],
      [checks("failing"), "✗"],
      [checks("pending"), "…"],
      [checks("none"), null],
    ];
    for (const [session, mark] of cases) {
      const { unmount } = render(<SessionRow session={session} usage={usage} />);
      for (const m of ["✓", "✗", "…"]) {
        if (m === mark) expect(screen.getByText(m)).toBeInTheDocument();
        else expect(screen.queryByText(m)).not.toBeInTheDocument();
      }
      unmount();
    }
    render(
      <SessionRow session={withPr({ number: 9, state: "draft", checks: "none" })} usage={usage} />,
    );
    expect(screen.getByText("draft")).toBeInTheDocument();
  });

  it("shows the label only when it differs from the group's title", () => {
    const { unmount } = render(<SessionRow session={byId(1)} usage={usage} />);
    expect(screen.queryByText("Working")).not.toBeInTheDocument();
    unmount();
    render(<SessionRow session={{ ...byId(5), label: "Not started" }} usage={usage} />);
    expect(screen.getByText("Not started")).toBeInTheDocument();
  });

  it("shows an unknown label as raw text", () => {
    render(<SessionRow session={{ ...byId(1), label: "Mystery" }} usage={usage} />);
    expect(screen.getByText("Mystery")).toBeInTheDocument();
  });

  it("counts several running agents", () => {
    const agent = (id: string, idle: boolean) => ({ id, name: id, description: "", idle });
    const { unmount } = render(
      <SessionRow
        session={{ ...byId(1), agents: [agent("a", false), agent("b", false), agent("c", true)] }}
        usage={usage}
      />,
    );
    expect(screen.getByText("2 agents")).toBeInTheDocument();
    unmount();
    render(<SessionRow session={{ ...byId(1), agents: null }} usage={usage} />);
    expect(screen.queryByText(/agent/)).not.toBeInTheDocument();
  });

  it("puts the workspace on the reason line of a needs-you row and on the meta line of any other", () => {
    const { unmount } = render(<SessionRow session={byId(2)} usage={usage} chip="loom" />);
    const badge = screen.getByText("loom");
    expect(badge.previousElementSibling).toHaveTextContent("Allow Bash");
    unmount();
    const second = render(<SessionRow session={byId(1)} usage={usage} chip="loom" />);
    const meta = screen.getByText("loom").parentElement as HTMLElement;
    expect(meta).toHaveTextContent("PR #482");
    second.unmount();
    render(<SessionRow session={byId(2)} usage={usage} />);
    expect(screen.queryByText("loom")).not.toBeInTheDocument();
  });

  it("tints a flow chip by its state", () => {
    const tone = (f: FlowSummary) => {
      const { unmount } = render(<SessionRow session={{ ...byId(1), flows: [f] }} usage={usage} />);
      const t = screen.getByText(flowChip(f)).getAttribute("data-tone");
      unmount();
      return t;
    };
    expect(tone(flow({ asks: "", status: "running" }))).toBe("docs");
    expect(tone(flow({ asks: "", status: "lost" }))).toBe("error");
    expect(tone(flow({ asks: "", status: "running", unreadable: "bad plan file" }))).toBe("error");
  });

  it("links to the session, or to the mockup while it is fresh", () => {
    const { unmount } = render(<SessionRow session={byId(1)} usage={usage} />);
    expect(link()).toHaveAttribute("href", "#/s/1");
    unmount();
    render(<SessionRow session={byId(7)} usage={usage} />);
    expect(link()).toHaveAttribute("href", "#/s/7/mockup");
  });

  it("shows a paused row with no meta line", () => {
    const container = row(byId(5));
    expect(screen.getByText("flow-old")).toBeInTheDocument();
    expect(screen.getByText("3d")).toBeInTheDocument();
    const text = container.textContent ?? "";
    expect(text).toBe("flow-old3d");
  });
});

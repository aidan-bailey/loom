import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { FlowSummary, Session } from "@/api/gen";
import { hrefFor } from "@/app/routes";
import { fixtureSnapshot } from "@/fixtures";
import { readFilter, useFilter } from "@/fleet/filter";
import { allSessions } from "@/fleet/groups";
import { sessionHref } from "@/fleet/links";
import { flowChip, flowTone } from "@/panels/fleet/SessionRow";
import { useFleet } from "@/store/fleet";

import { Sidebar, sidebarLine } from "./Sidebar";

const NOW = Date.parse("2026-11-18T12:00:00Z");
const snapshot = fixtureSnapshot(NOW);
const byId = (id: number): Session => allSessions(snapshot).find((s) => s.id === id) as Session;
const flowOf = (session: Session, i: number): FlowSummary => (session.flows as FlowSummary[])[i];

describe("sidebarLine", () => {
  it("words a waiting session's why: the reason, then the detail", () => {
    expect(sidebarLine(byId(2))).toEqual({
      text: "Allow Bash: go test ./ui/... -run TestPane",
      tone: "attention",
    });
    expect(sidebarLine(byId(4))).toEqual({ text: "Plan approval", tone: "attention" });
    const detailOnly = { ...byId(4), wait: { reason: "", detail: "go test ./..." } };
    expect(sidebarLine(detailOnly).text).toBe("go test ./...");
  });

  it("a waiting session with no reason and no detail falls through to what it said", () => {
    const s = { ...byId(2), wait: { reason: "", detail: "" }, last_message: "thinking" };
    expect(sidebarLine(s)).toEqual({ text: "thinking", tone: "muted" });
  });

  it("a waiting session shows why it waits ahead of its flow", () => {
    const s = { ...byId(2), flows: byId(6).flows };
    expect(sidebarLine(s)).toEqual({
      text: "Allow Bash: go test ./ui/... -run TestPane",
      tone: "attention",
    });
  });

  it("a session with flows shows its newest flow's chip in the flow's tone", () => {
    const s6 = byId(6);
    const want = { text: flowChip(flowOf(s6, 0)), tone: flowTone(flowOf(s6, 0)) };
    expect(want.tone).toBe("attention");
    expect(want.text).not.toBe(flowChip(flowOf(s6, 1)));
    expect(sidebarLine(s6)).toEqual(want);
    expect(sidebarLine({ ...s6, flows: [...(s6.flows as FlowSummary[])].reverse() })).toEqual(want);

    const designed = flowOf(s6, 1);
    expect(sidebarLine({ ...s6, flows: [designed] })).toEqual({
      text: flowChip(designed),
      tone: "docs",
    });
    const lost: FlowSummary = { ...designed, status: "lost" };
    expect(sidebarLine({ ...s6, flows: [lost] }).tone).toBe("error");
  });

  it("equal or unreadable flow times keep the first flow", () => {
    const s6 = byId(6);
    const [a, b] = s6.flows as FlowSummary[];
    for (const updated of ["", "2026-11-18T11:00:00Z"]) {
      const s = {
        ...s6,
        flows: [
          { ...a, updated },
          { ...b, updated },
        ],
      };
      expect(sidebarLine(s).text).toBe(flowChip(s.flows[0]));
    }
  });

  it("a session with no flow and no wait shows its last message, muted", () => {
    const s1 = byId(1);
    expect(s1.last_message).not.toBe("");
    expect(sidebarLine(s1)).toEqual({ text: s1.last_message, tone: "muted" });
    expect(sidebarLine(byId(3))).toEqual({ text: "", tone: "muted" });
    expect(sidebarLine({ ...s1, flows: [] })).toEqual(sidebarLine({ ...s1, flows: null }));
  });
});

function resetStore(): void {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
}

describe("Sidebar", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(NOW);
    useFleet.setState({ snapshot: fixtureSnapshot(NOW), stale: false, link: "live", updated: NOW });
  });
  afterEach(() => {
    vi.useRealTimers();
    resetStore();
    window.localStorage.clear();
    useFilter.setState({ stored: readFilter() });
  });

  const group = (key: string): Element | null =>
    document.querySelector(`[data-slot="sidebar-group"][data-group="${key}"]`);
  const titles = (key: string): string[] =>
    [...(group(key)?.querySelectorAll('[data-slot="sidebar-row"]') ?? [])].map(
      (a) => a.querySelector("span.truncate")?.textContent ?? "",
    );
  const row = (title: string): HTMLElement => {
    const found = [...document.querySelectorAll<HTMLElement>('[data-slot="sidebar-row"]')].find(
      (a) => (a.textContent ?? "").startsWith(title),
    );
    if (found === undefined) throw new Error(`no row ${title}`);
    return found;
  };
  const load = (edit: (s: ReturnType<typeof fixtureSnapshot>) => void): void => {
    const snap = fixtureSnapshot(NOW);
    edit(snap);
    useFleet.setState({ snapshot: snap });
  };

  it("draws the groups in the fleet's order, each with its count and its sessions", () => {
    render(<Sidebar current={undefined} />);
    const keys = [...document.querySelectorAll('[data-slot="sidebar-group"]')].map((g) =>
      g.getAttribute("data-group"),
    );
    expect(keys).toEqual(["needs_you", "your_turn", "working", "paused"]);
    expect(titles("needs_you")).toEqual(["flow-docs", "sandbox-ask"]);
    expect(titles("your_turn")).toEqual([
      "a-very-long-session-title-that-will-not-fit-on-one-line",
    ]);
    expect(titles("working")).toEqual(["fix-pane-cursor", "flow-run", "plain-shell"]);
    const counts = ["needs_you", "your_turn", "working", "paused"].map(
      (k) => group(k)?.querySelector('[data-slot="sidebar-heading"] span')?.textContent,
    );
    expect(counts).toEqual(["2", "1", "3", "1"]);
  });

  it("draws a row as the dot, the title, the age and the line", () => {
    render(<Sidebar current={undefined} />);
    const ask = row("sandbox-ask");
    expect(ask.textContent).toContain("18m");
    expect(ask.querySelector('[data-slot="sidebar-dot"]')?.className).toContain("bg-attention");
    const line = ask.querySelector('[data-slot="sidebar-line"]');
    expect(line?.getAttribute("data-tone")).toBe("attention");
    expect(line?.textContent).toBe("Allow Bash: go test ./ui/... -run TestPane");

    const run = row("flow-run");
    expect(run.querySelector('[data-slot="sidebar-dot"]')?.className).toContain("bg-working");
    const runLine = run.querySelector('[data-slot="sidebar-line"]');
    expect(runLine?.textContent).toBe(flowChip((byId(6).flows as FlowSummary[])[0]));
    expect(runLine?.getAttribute("data-tone")).toBe("attention");

    const fix = row("fix-pane-cursor").querySelector('[data-slot="sidebar-line"]');
    expect(fix?.textContent).toBe(byId(1).last_message);
    expect(fix?.getAttribute("data-tone")).toBe("muted");

    expect(row("plain-shell").querySelector('[data-slot="sidebar-line"]')).toBeNull();
  });

  it("a row is dense: no 44px minimum, and its line is clipped on a block element", () => {
    render(<Sidebar current={undefined} />);
    const nav = document.querySelector('[data-slot="sidebar"]') as HTMLElement;
    for (const el of [nav, ...nav.querySelectorAll("*")]) {
      expect(el.getAttribute("class") ?? "").not.toContain("min-h-[44px]");
    }
    const line = row("sandbox-ask").querySelector('[data-slot="sidebar-line"]') as HTMLElement;
    expect(line.classList.contains("block")).toBe(true);
    expect(line.classList.contains("truncate")).toBe(true);
  });

  it("a row opens the session's conversation, or its fresh mockup", () => {
    render(<Sidebar current={undefined} />);
    expect(row("fix-pane-cursor").getAttribute("href")).toBe(sessionHref(byId(1)));
    expect(row("fix-pane-cursor").getAttribute("href")).toBe(hrefFor("turns", { id: "1" }));
    const long = row("a-very-long-session-title");
    expect(long.getAttribute("href")).toBe(hrefFor("mockup", { id: "7" }));
    expect(long.getAttribute("href")).not.toBe(hrefFor("turns", { id: "7" }));
  });

  it("highlights the session on screen", () => {
    const { rerender } = render(<Sidebar current={2} />);
    const marked = (): HTMLElement[] => [
      ...document.querySelectorAll<HTMLElement>("[aria-current]"),
    ];
    expect(marked()).toEqual([row("sandbox-ask")]);
    expect(row("sandbox-ask").getAttribute("aria-current")).toBe("page");
    expect(row("sandbox-ask").className).toContain("bg-secondary");
    rerender(<Sidebar current={1} />);
    expect(marked()).toEqual([row("fix-pane-cursor")]);
    rerender(<Sidebar current={undefined} />);
    expect(marked()).toEqual([]);
    rerender(<Sidebar current={999} />);
    expect(marked()).toEqual([]);
  });

  it("folds Paused until it is opened", async () => {
    vi.useRealTimers();
    render(<Sidebar current={undefined} />);
    expect(screen.queryByText("flow-old")).not.toBeInTheDocument();
    const heading = screen.getByRole("button", { name: /^Paused/ });
    expect(heading.textContent).toContain("1");
    await userEvent.click(heading);
    const old = await screen.findByText("flow-old");
    expect(group("paused")?.contains(old)).toBe(true);
    expect(group("paused")?.querySelector('[data-slot="sidebar-dot"]')?.className).toContain(
      "bg-muted-foreground",
    );
    await userEvent.click(screen.getByRole("button", { name: /^Paused/ }));
    await waitFor(() => expect(screen.queryByText("flow-old")).not.toBeInTheDocument());
  });

  it("shows only the chosen workspace's sessions", () => {
    act(() => useFilter.getState().pick("2"));
    render(<Sidebar current={undefined} />);
    expect(titles("needs_you")).toEqual(["flow-docs"]);
    expect(titles("working")).toEqual(["flow-run"]);
    expect(group("your_turn")).toBeNull();
    expect(screen.queryByText("sandbox-ask")).not.toBeInTheDocument();
    act(() => useFilter.getState().pick(""));
    expect(titles("needs_you")).toEqual(["flow-docs", "sandbox-ask"]);
    expect(titles("working")).toEqual(["fix-pane-cursor", "flow-run", "plain-shell"]);
    expect(group("your_turn")).not.toBeNull();
  });

  it("reads a stored workspace that no shown workspace has as all, and leaves it stored", () => {
    window.localStorage.setItem("loom.filter", JSON.stringify("9"));
    useFilter.setState({ stored: readFilter() });
    render(<Sidebar current={undefined} />);
    expect(screen.getByText("sandbox-ask")).toBeInTheDocument();
    expect(screen.getByText("flow-docs")).toBeInTheDocument();
    expect(window.localStorage.getItem("loom.filter")).toBe('"9"');
  });

  it("lists no session of a hidden workspace", () => {
    load((s) => {
      (s.workspaces ?? [])[1].shown = false;
    });
    render(<Sidebar current={undefined} />);
    expect(screen.queryByText("flow-docs")).not.toBeInTheDocument();
    expect(screen.queryByText("flow-run")).not.toBeInTheDocument();
    expect(screen.getByText("sandbox-ask")).toBeInTheDocument();
    // flow-old was the only paused session, so there is no Paused group to open.
    expect(group("paused")).toBeNull();
    expect(screen.queryByText("flow-old")).not.toBeInTheDocument();
  });

  it("says so when nothing is listed", () => {
    load((s) => {
      s.sessions = {};
    });
    const { unmount } = render(<Sidebar current={undefined} />);
    expect(screen.getByText("No sessions.")).toBeInTheDocument();
    unmount();
    act(() => useFilter.getState().pick("2"));
    load((s) => {
      s.sessions = { "1": s.sessions?.["1"] ?? [] };
    });
    render(<Sidebar current={undefined} />);
    expect(screen.getByText("No sessions in this workspace.")).toBeInTheDocument();
  });

  it("draws nothing but the nav with no snapshot", () => {
    resetStore();
    render(<Sidebar current={undefined} />);
    const nav = document.querySelector('[data-slot="sidebar"]');
    expect(nav).not.toBeNull();
    expect(nav?.querySelector('[data-slot="sidebar-row"], [data-slot="sidebar-group"]')).toBeNull();
  });

  it("ages move with the clock", () => {
    vi.useRealTimers();
    vi.useFakeTimers({ now: NOW });
    const { unmount } = render(<Sidebar current={undefined} />);
    expect(within(row("sandbox-ask")).getByText("18m")).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(5 * 60_000 + 30_000);
    });
    expect(within(row("sandbox-ask")).getByText("23m")).toBeInTheDocument();
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { FlowSummary, Pr, Session, Snapshot, Usage } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { readFilter, useFilter } from "@/fleet/filter";
import { allSessions } from "@/fleet/groups";
import { markSeen, readSeen, SEEN_KEY } from "@/fleet/seen";
import { WorkspaceFilter } from "@/fleet/WorkspaceFilter";
import { panelFor } from "@/app/panels";
import "@/panels";
import { useFleet } from "@/store/fleet";
import { setViewportWidth } from "@/test-layout";

import { FleetHead } from "./FleetHead";
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
  useFilter.setState({ stored: readFilter() });
  window.history.replaceState(null, "", window.location.pathname);
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

  it("draws NEW after the title only when fresh", () => {
    load(snapshot);
    const session = byId(7);
    const { rerender } = render(<SessionRow session={session} usage={[]} fresh />);
    const mark = screen.getByText("NEW");
    const title = screen.getByText(session.title);
    expect(title.compareDocumentPosition(mark) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    rerender(<SessionRow session={session} usage={[]} />);
    expect(screen.queryByText("NEW")).toBeNull();
  });
});

describe("the fleet panel", () => {
  const props = { sessionId: undefined, params: {}, search: {} };
  const Body = (): ReactElement => {
    const Panel = panelFor("fleet")!.Body;
    return <Panel {...props} />;
  };
  const withSnapshot = (edit: (s: Snapshot) => void = () => {}): Snapshot => {
    const snapshot = fixtureSnapshot(NOW);
    edit(snapshot);
    load(snapshot);
    return snapshot;
  };
  const group = (title: string): HTMLElement =>
    screen.getByRole("heading", { name: new RegExp(`^${title}\\b`) }).closest("section")!;
  const titles = (section: HTMLElement): (string | null)[] =>
    within(section)
      .getAllByRole("link")
      .map((a) => a.querySelector("span.truncate")?.textContent ?? null);
  const chips = (): string[] =>
    [...document.querySelectorAll('a [data-slot="badge"]')].map((b) => b.textContent ?? "");
  const choose = (name: string) => userEvent.click(screen.getByRole("button", { name }));

  it("lists Needs you, Your turn and Working with their counts, each in its group's order", () => {
    withSnapshot();
    render(<Body />);
    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings).toEqual(["Needs you2", "Your turn1", "Working3"]);
    expect(titles(group("Needs you"))).toEqual(["flow-docs", "sandbox-ask"]);
    expect(titles(group("Your turn"))).toEqual([
      "a-very-long-session-title-that-will-not-fit-on-one-line",
    ]);
    expect(titles(group("Working"))).toEqual(["fix-pane-cursor", "flow-run", "plain-shell"]);
  });

  it("folds Paused until it is opened", async () => {
    withSnapshot();
    render(<Body />);
    expect(screen.queryByText("flow-old")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /^Paused/ }));
    expect(await screen.findByText("flow-old")).toBeInTheDocument();
  });

  it("shows the workspace filter only with more than one shown workspace", () => {
    withSnapshot();
    const { unmount } = render(<Body />);
    const filter = screen.getByRole("group", { name: "Workspaces" });
    expect(
      within(filter)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["All", "loom", "flow"]);
    unmount();
    resetStore();
    withSnapshot((s) => {
      s.workspaces![1].shown = false;
    });
    render(<Body />);
    expect(screen.queryByRole("group", { name: "Workspaces" })).not.toBeInTheDocument();
  });

  it("narrows to a workspace, stores the choice and restores it on the next launch", async () => {
    withSnapshot();
    const { unmount } = render(<Body />);
    await choose("flow");
    const narrowed = () => {
      expect(screen.getByText("flow-docs")).toBeInTheDocument();
      expect(screen.getByText("flow-run")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /^Paused/ })).toBeInTheDocument();
      expect(screen.queryByText("fix-pane-cursor")).not.toBeInTheDocument();
      expect(screen.queryByText("sandbox-ask")).not.toBeInTheDocument();
    };
    narrowed();
    expect(window.localStorage.getItem("loom.filter")).toBe('"2"');
    unmount();
    render(<Body />);
    narrowed();
    expect(screen.getByRole("button", { name: "flow" })).toHaveAttribute("aria-pressed", "true");
  });

  it("honours a filter the classic app stored", () => {
    window.localStorage.setItem("loom.filter", JSON.stringify("1"));
    useFilter.setState({ stored: readFilter() });
    withSnapshot();
    render(<Body />);
    expect(screen.getByText("fix-pane-cursor")).toBeInTheDocument();
    expect(screen.queryByText("flow-docs")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "loom" })).toHaveAttribute("aria-pressed", "true");
  });

  it("reads a stored filter for a workspace that is not listed as all, and leaves it stored", () => {
    window.localStorage.setItem("loom.filter", JSON.stringify("9"));
    useFilter.setState({ stored: readFilter() });
    withSnapshot();
    render(<Body />);
    expect(screen.getByText("fix-pane-cursor")).toBeInTheDocument();
    expect(screen.getByText("flow-docs")).toBeInTheDocument();
    expect(window.localStorage.getItem("loom.filter")).toBe('"9"');
  });

  it("selects all again when the chosen workspace is chosen twice", async () => {
    withSnapshot();
    render(<Body />);
    await choose("flow");
    await choose("flow");
    expect(screen.getByText("fix-pane-cursor")).toBeInTheDocument();
    expect(screen.getByText("flow-docs")).toBeInTheDocument();
    expect(window.localStorage.getItem("loom.filter")).toBe('""');
    expect(screen.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true");
  });

  it("says there are no sessions", () => {
    const empty = (s: Snapshot) => {
      s.sessions = { "1": [], "2": [] };
    };
    withSnapshot(empty);
    const { unmount } = render(<Body />);
    expect(screen.getByText("No sessions.")).toBeInTheDocument();
    unmount();
    window.localStorage.setItem("loom.filter", JSON.stringify("2"));
    useFilter.setState({ stored: readFilter() });
    render(<Body />);
    expect(screen.getByText("No sessions in this workspace.")).toBeInTheDocument();
  });

  it("names the workspace on each row only while the fleet shows all of them", async () => {
    withSnapshot();
    render(<Body />);
    expect(chips()).toEqual(expect.arrayContaining(["loom", "flow"]));
    await choose("flow");
    expect(chips()).not.toContain("loom");
    expect(chips()).not.toContain("flow");
  });

  it("redraws every 30 seconds", () => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
    withSnapshot();
    render(<Body />);
    expect(screen.getByText("2m")).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(90_000);
    });
    expect(screen.getByText("3m")).toBeInTheDocument();
  });

  it("lists a group this build does not know under Paused", async () => {
    withSnapshot((s) => {
      const one = s.sessions!["1"]![0];
      s.sessions!["1"]![0] = { ...one, group: "mystery" as Session["group"] };
    });
    render(<Body />);
    expect(screen.queryByText("fix-pane-cursor")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /^Paused/ }));
    expect(await screen.findByText("fix-pane-cursor")).toBeInTheDocument();
  });

  it("says Loading before the first snapshot", () => {
    render(<Body />);
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });

  it("FleetHead shows the flows link only when a listed session has flows", () => {
    withSnapshot();
    const { unmount } = render(<FleetHead />);
    expect(screen.getByRole("link", { name: "Flows" })).toHaveAttribute("href", "#/flows");
    unmount();
    for (const none of [[], null]) {
      resetStore();
      withSnapshot((s) => {
        for (const list of Object.values(s.sessions!)) {
          for (const x of list!) x.flows = none;
        }
      });
      const r = render(<FleetHead />);
      expect(screen.queryByRole("link", { name: "Flows" })).not.toBeInTheDocument();
      r.unmount();
    }
  });

  it("FleetHead ignores the flows of a session in a hidden workspace", () => {
    withSnapshot((s) => {
      const flows = Object.values(s.sessions!)
        .flatMap((list) => list!)
        .find((x) => (x.flows ?? []).length > 0)?.flows;
      if (flows === undefined || flows === null) throw new Error("the fixture holds no flows");
      const hidden = s.workspaces!.find((w) => w.shown !== false)!;
      for (const list of Object.values(s.sessions!)) {
        for (const x of list!) x.flows = x.workspace === hidden.id ? flows : null;
      }
      hidden.shown = false;
    });
    render(<FleetHead />);
    expect(screen.queryByRole("link", { name: "Flows" })).not.toBeInTheDocument();
  });

  it("FleetHead links settings and new to their panels", () => {
    withSnapshot();
    render(<FleetHead />);
    expect(screen.getByRole("heading", { name: "Fleet" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute("href", "#/settings");
    expect(screen.getByRole("link", { name: "+ New" })).toHaveAttribute("href", "#/new");
  });

  it("+ New is dimmed and does nothing while the view is stale", () => {
    withSnapshot();
    render(<FleetHead />);
    const live = screen.getByRole("link", { name: "+ New" });
    expect(live).not.toHaveAttribute("aria-disabled");
    expect(fireEvent.click(live)).toBe(true);

    act(() => {
      useFleet.getState().setLink("offline");
    });
    const stale = screen.getByRole("link", { name: "+ New" });
    expect(stale).toHaveAttribute("aria-disabled", "true");
    expect(fireEvent.click(stale)).toBe(false);
  });

  describe("the seen times", () => {
    const yourTurn = (snapshot: Snapshot): Session => {
      const s = allSessions(snapshot).find((x) => x.group === "your_turn");
      expect(s).toBeDefined();
      return s as Session;
    };
    const stored = (seen: Record<string, number>): void => {
      window.localStorage.setItem(SEEN_KEY, JSON.stringify(JSON.stringify(seen)));
    };

    it("marks a Your turn session NEW until it is opened", () => {
      const session = yourTurn(withSnapshot());
      render(<Body />);
      expect(screen.getByText("NEW")).toBeInTheDocument();

      act(() => {
        markSeen(session.id);
      });
      expect(screen.queryByText("NEW")).toBeNull();
    });

    it("marks a session NEW only for a turn that ended after the last time it was opened", () => {
      const session = yourTurn(withSnapshot());
      const sinceMs = Date.parse(session.since);
      stored({ [session.id]: sinceMs - 1000 });
      const { unmount } = render(<Body />);
      expect(screen.getByText("NEW")).toBeInTheDocument();
      unmount();

      stored({ [session.id]: sinceMs + 1000 });
      render(<Body />);
      expect(screen.queryByText("NEW")).toBeNull();
    });

    it("marks no needs-you, working or paused row NEW", async () => {
      const snapshot = withSnapshot();
      render(<Body />);
      await userEvent.click(screen.getByRole("button", { name: /^Paused/ }));
      const yours = allSessions(snapshot).filter((s) => s.group === "your_turn").length;
      expect(screen.getAllByText("NEW")).toHaveLength(yours);
    });

    it("prunes the seen times of sessions the daemon no longer lists", () => {
      const session = yourTurn(withSnapshot());
      stored({ [session.id]: 5, 999: 6 });
      render(<Body />);

      expect(readSeen()).toEqual({ [session.id]: 5 });
      expect(window.localStorage.getItem(SEEN_KEY)).toBe(
        JSON.stringify(JSON.stringify({ [session.id]: 5 })),
      );
    });

    it("prunes by every session of every workspace, shown or not", () => {
      const snapshot = withSnapshot((s) => {
        const hidden = s.workspaces?.find((w) => w.id === 2);
        if (hidden !== undefined) hidden.shown = false;
      });
      const inHidden = allSessions(snapshot).find((x) => x.workspace === 2);
      if (inHidden === undefined) throw new Error("the fixture holds no session in workspace 2");
      stored({ [inHidden.id]: 5, 999: 6 });
      render(<Body />);

      expect(readSeen()).toEqual({ [inHidden.id]: 5 });
    });

    it("never prunes on an empty fleet", () => {
      withSnapshot((s) => {
        s.sessions = {};
      });
      stored({ 5: 1, 6: 2 });
      render(<Body />);

      expect(readSeen()).toEqual({ "5": 1, "6": 2 });
    });
  });

  describe("WorkspaceFilter", () => {
    it("draws no filter of its own in the fleet panel on desktop, and one on the phone", () => {
      withSnapshot();
      setViewportWidth(1100);
      const desk = render(<Body />);
      expect(screen.queryByLabelText("Workspaces")).toBeNull();
      desk.unmount();

      setViewportWidth(1400);
      const wide = render(<Body />);
      expect(screen.queryByLabelText("Workspaces")).toBeNull();
      wide.unmount();

      setViewportWidth(390);
      render(<Body />);
      expect(screen.getByLabelText("Workspaces")).toBeInTheDocument();
    });

    it("a pick in the filter shows in the fleet and in what the new-session form reads", async () => {
      withSnapshot();
      setViewportWidth(1100);
      render(
        <>
          <WorkspaceFilter />
          <Body />
        </>,
      );
      await userEvent.click(screen.getByRole("button", { name: "flow" }));
      expect(screen.getByText("flow-docs")).toBeInTheDocument();
      expect(screen.queryByText("fix-pane-cursor")).not.toBeInTheDocument();
      expect(window.localStorage.getItem("loom.filter")).toBe('"2"');
      expect(readFilter()).toBe("2");

      await userEvent.click(screen.getByRole("button", { name: "All" }));
      expect(screen.getByText("flow-docs")).toBeInTheDocument();
      expect(screen.getByText("fix-pane-cursor")).toBeInTheDocument();
      expect(window.localStorage.getItem("loom.filter")).toBe('""');
    });

    it("draws nothing with one shown workspace", () => {
      withSnapshot((s) => {
        s.workspaces![1].shown = false;
      });
      const { container } = render(<WorkspaceFilter />);
      expect(container).toBeEmptyDOMElement();
    });

    it("reads a stored workspace that is not listed as all, and leaves it stored", () => {
      window.localStorage.setItem("loom.filter", JSON.stringify("9"));
      useFilter.setState({ stored: readFilter() });
      withSnapshot();
      render(<WorkspaceFilter />);
      expect(screen.getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true");
      expect(window.localStorage.getItem("loom.filter")).toBe('"9"');
    });

    it("drops the 44px touch minimum when dense", () => {
      withSnapshot();
      const plain = render(<WorkspaceFilter />);
      expect(screen.getByRole("button", { name: "All" }).className).toContain("after:min-h-[44px]");
      plain.unmount();

      render(<WorkspaceFilter dense />);
      expect(screen.getByRole("button", { name: "All" }).className).not.toContain(
        "after:min-h-[44px]",
      );
    });
  });

  it("registers the fleet panel", () => {
    const panel = panelFor("fleet");
    expect(panel).toBeDefined();
    expect(panel?.Head).toBeDefined();
    expect(panel?.Body).toBeDefined();
    expect(panel?.title(props)).toBe("Fleet");
  });
});

import { act, render, screen, type RenderResult } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Flow, Session } from "@/api/gen";
import { flowKey, useFlow } from "@/api/reads";
import { hrefFor } from "@/app/routes";
import { fixtureFlow, fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { resetFleet, seed } from "@/commands/testkit";

import FlowStoryBody from "./FlowStoryBody";

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

// board is the canned board; a test changes what it needs.
function board(over: (flow: Flow) => void = () => undefined): Flow {
  const flow = fixtureFlow();
  over(flow);
  return flow;
}

// show seeds the session (session 6 unless given; null seeds none), makes the read answer with
// the board and error, and draws the story's view on its route.
function show(
  opts: {
    session?: Session | null;
    flow?: Flow;
    error?: string;
    story?: string;
    search?: Record<string, string>;
  } = {},
): RenderResult {
  const session = opts.session === undefined ? session6() : opts.session;
  const story = opts.story ?? "1";
  seed(session === null ? [] : [session]);
  vi.mocked(useFlow).mockReturnValue({
    flow: opts.flow ?? fixtureFlow(),
    error: opts.error ?? "",
    loading: false,
  });
  window.history.replaceState(null, "", `#/s/6/flow/${story}`);
  return render(
    <FlowStoryBody sessionId={6} params={{ id: "6", story }} search={opts.search ?? {}} />,
  );
}

function planOfSession(index = 0): string {
  const plan = session6().flows?.[index]?.plan;
  if (plan === undefined) throw new Error("the canned session 6 lacks that flow");
  return plan;
}

function facts(view: RenderResult): string | null {
  return view.container.querySelector('[data-slot="facts"]')?.textContent ?? null;
}

describe("FlowStoryBody", () => {
  it("a session the snapshot does not list says it is gone and reads nothing", () => {
    show({ session: null });
    expect(screen.getByText("This session is gone.")).toBeInTheDocument();
    expect(useFlow).toHaveBeenCalledWith(expect.objectContaining({ plan: "" }));
    expect(vi.mocked(useFlow).mock.calls.every(([o]) => o.plan === "")).toBe(true);
  });

  it("a session with no flow and no plan says so and reads nothing", () => {
    for (const flows of [[], null]) {
      const view = show({ session: session6({ flows }) });
      expect(screen.getByText("No flow in this session's worktree.")).toBeInTheDocument();
      expect(vi.mocked(useFlow).mock.calls.every(([o]) => o.plan === "")).toBe(true);
      view.unmount();
      vi.mocked(useFlow).mockClear();
    }
  });

  it("reads the route's plan, else the newest flow's, keyed by the session's flows", () => {
    const session = session6();
    show({});
    expect(useFlow).toHaveBeenLastCalledWith({
      session: 6,
      plan: planOfSession(0),
      key: flowKey(session),
    });
    vi.mocked(useFlow).mockClear();
    show({ search: { plan: planOfSession(1) } });
    expect(useFlow).toHaveBeenLastCalledWith({
      session: 6,
      plan: planOfSession(1),
      key: flowKey(session),
    });
  });

  it("draws Loading… until the board arrives, and the read's error when none is held", () => {
    seed([session6()]);
    vi.mocked(useFlow).mockReturnValue({ flow: undefined, error: "", loading: true });
    const loading = render(
      <FlowStoryBody sessionId={6} params={{ id: "6", story: "1" }} search={{}} />,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    loading.unmount();

    vi.mocked(useFlow).mockReturnValue({
      flow: undefined,
      error: "loom answered 500",
      loading: false,
    });
    render(<FlowStoryBody sessionId={6} params={{ id: "6", story: "1" }} search={{}} />);
    expect(screen.getByRole("alert")).toHaveTextContent("loom answered 500");
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it("says the board has no row for an id it lacks", () => {
    const missing = show({ story: "9" });
    expect(screen.getByText("The board has no row 9.")).toBeInTheDocument();
    expect(screen.queryByText("steps")).toBeNull();
    missing.unmount();

    const final = show({ story: "F", flow: board((f) => (f.final = null)) });
    expect(screen.getByText("The board has no row F.")).toBeInTheDocument();
    final.unmount();

    show({ story: "P", flow: board((f) => (f.premise = null)) });
    expect(screen.getByText("The board has no row P.")).toBeInTheDocument();
  });

  it("the head line: a way back to the board with its plan, and the row's name", () => {
    const first = show({});
    expect(screen.getByRole("link", { name: "‹ board" })).toHaveAttribute(
      "href",
      hrefFor("flow", { id: "6" }, { plan: planOfSession(0) }),
    );
    expect(screen.getByText("Story 1")).toBeInTheDocument();
    first.unmount();

    const premise = show({ story: "P" });
    expect(screen.getByText("Premises")).toBeInTheDocument();
    premise.unmount();

    const final = show({ story: "F" });
    expect(screen.getByText("Final round")).toBeInTheDocument();
    final.unmount();

    show({ search: { plan: planOfSession(1) } });
    expect(screen.getByRole("link", { name: "‹ board" })).toHaveAttribute(
      "href",
      hrefFor("flow", { id: "6" }, { plan: planOfSession(1) }),
    );
  });

  it("keeps the held board drawn under a failed read's error", () => {
    const view = show({ error: "loom answered 500" });
    expect(screen.getByRole("alert")).toHaveTextContent("loom answered 500");
    expect(facts(view)).not.toBeNull();
  });

  it("the facts: developer, tier and review, commits, how long it took and what it cost", () => {
    const line = (story: string, flow?: Flow): string | null => {
      const view = show({ story, flow });
      const text = facts(view);
      view.unmount();
      return text;
    };
    expect(line("1")).toBe("intermediate-dev · deep · full · 7 commits · 37m 0s · $1.41");
    expect(line("2")).toBe("intermediate-dev · deep · full · 4 commits · 21m 0s · $1.10");
    expect(line("3")).toBe("intermediate-dev · deep · full · 8m 0s · $0.62");
    expect(line("5")).toBe("intermediate-dev · deep · full");
    expect(line("P")).toBe("2m 0s · $0.12");
    expect(
      line(
        "5",
        board((f) => {
          const story = f.stories?.find((s) => s.id === "5");
          if (story !== undefined) story.review = "";
        }),
      ),
    ).toBe("intermediate-dev · deep");
  });

  it("a running row's time runs to now and moves with the clock", () => {
    const four = show({ story: "4" });
    expect(facts(four)).toBe("junior-dev · light · vouched · 2m 0s");
    four.unmount();
    const final = show({ story: "F" });
    expect(facts(final)).toBe("20m 0s");
    final.unmount();

    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    vi.setSystemTime(NOW);
    const ticking = show({ story: "4" });
    expect(facts(ticking)).toBe("junior-dev · light · vouched · 2m 0s");
    act(() => {
      vi.advanceTimersByTime(30_000);
    });
    expect(facts(ticking)).toBe("junior-dev · light · vouched · 2m 30s");
  });
});

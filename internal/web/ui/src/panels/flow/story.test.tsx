import { act, render, screen, within, type RenderResult } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Flow, FlowStep, FlowStory, Session } from "@/api/gen";
import { flowKey, useFlow } from "@/api/reads";
import { panelFor } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { PanelHost } from "@/app/shell";
import { fixtureFlow, fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { resetFleet, seed } from "@/commands/testkit";
import "@/panels";
import { SessionHead } from "@/session/SessionHead";

import { flowStoryPanel } from "./index";
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

// stepsOf is the step items the view draws, in order.
function stepsOf(view: RenderResult): HTMLElement[] {
  return Array.from(view.container.querySelectorAll<HTMLElement>('[data-slot="step"]'));
}

function story(flow: Flow, id: string): FlowStory {
  const found = id === "F" ? flow.final : (flow.stories ?? []).find((s) => s.id === id);
  if (found === null || found === undefined) throw new Error(`the canned board lacks row ${id}`);
  return found;
}

function stepOf(flow: Flow, id: string, index: number): FlowStep {
  const found = story(flow, id).steps?.[index];
  if (found === undefined) throw new Error(`the canned row ${id} lacks step ${index}`);
  return found;
}

describe("FlowStoryBody steps", () => {
  it("each in order with its word, role, status and elapsed time, and the cost where the agent is priced", () => {
    const flow = board((f) => {
      f.costs.by_agent = { "2026-11-09-flow-visualisation-s1-dev1": 0.4 };
    });
    const view = show({ flow });
    const items = stepsOf(view);
    expect(items.map((li) => li.textContent?.match(/^(dev|gates|fix|review)\d/)?.[0])).toEqual([
      "dev1",
      "gates1",
      "fix1",
      "gates2",
      "review1",
    ]);
    expect(items[0]).toHaveTextContent("intermediate-dev · DONE");
    expect(items[0]).toHaveTextContent("14m 0s $0.40");
    expect(items[1]).toHaveTextContent("gates · failed");
    expect(items[1]).toHaveTextContent("3m 0s");
    expect(items[1]?.textContent).not.toContain("$");
    view.unmount();

    const bare = show({
      flow: board((f) => {
        const step = stepOf(f, "1", 0);
        step.started = "";
        step.ended = "";
      }),
    });
    expect(stepsOf(bare)[0]).toHaveTextContent(/^dev1 intermediate-dev · DONE$/);
  });

  it("the outcome of each decides its dot, and an unknown status is neutral and raw", () => {
    const outcomes = (id: string, flow?: Flow): (string | null)[] => {
      const view = show({ story: id, flow });
      const found = stepsOf(view).map((li) => li.getAttribute("data-outcome"));
      view.unmount();
      return found;
    };
    expect(outcomes("1")).toEqual(["passed", "failed", "passed", "passed", "passed"]);
    expect(outcomes("3")).toEqual(["failed"]);
    expect(outcomes("4")).toEqual(["running"]);
    const four = show({ story: "4" });
    expect(stepsOf(four)[0]).toHaveTextContent("2m 0s");
    four.unmount();

    const withStatus = (status: string): Flow =>
      board((f) => {
        stepOf(f, "4", 0).status = status;
      });
    expect(outcomes("4", withStatus("starting"))).toEqual(["running"]);

    const todo = show({ story: "4", flow: withStatus("") });
    expect(stepsOf(todo)[0]?.getAttribute("data-outcome")).toBe("todo");
    expect(stepsOf(todo)[0]).toHaveTextContent("unreadable");
    todo.unmount();

    const weird = show({ story: "4", flow: withStatus("weird") });
    const item = stepsOf(weird)[0];
    expect(item?.getAttribute("data-outcome")).toBe("unknown");
    expect(item).toHaveTextContent("weird");
    const dot = item?.querySelector("span[aria-hidden]");
    expect(dot).toHaveClass("bg-muted-foreground");
    expect(dot).not.toHaveClass("bg-error");
    expect(dot).not.toHaveClass("bg-working");
  });

  it("a step with a report links to it, and one without is not a link", () => {
    const view = show({});
    const [dev, gates] = stepsOf(view);
    expect(within(dev).getByRole("link")).toHaveAttribute(
      "href",
      hrefFor(
        "flowDoc",
        { id: "6", doc: "report:2026-11-09-flow-visualisation-s1-dev1" },
        { plan: planOfSession(0) },
      ),
    );
    expect(
      within(gates)
        .queryAllByRole("link")
        .map((a) => a.getAttribute("href")?.includes("flow/doc")),
    ).not.toContain(true);
  });

  it("the unreadable and waiting lines", () => {
    const four = show({ story: "4" });
    expect(screen.getByText("waiting for an agent slot")).toHaveClass("text-muted-foreground");
    four.unmount();

    const bad = show({
      story: "4",
      flow: board((f) => {
        stepOf(f, "4", 0).unreadable = "bad report";
      }),
    });
    expect(screen.getByText("bad report")).toHaveClass("text-error");
    bad.unmount();

    show({});
    expect(screen.queryByText("bad report")).toBeNull();
    expect(screen.queryByText("waiting for an agent slot")).toBeNull();
  });

  it("the gate lines: the verdict, the text and a link to the job", () => {
    const view = show({});
    const gates = Array.from(
      stepsOf(view)[1]?.querySelectorAll<HTMLElement>('[data-slot="gate"]') ?? [],
    );
    expect(gates.map((g) => g.textContent)).toEqual([
      "PASS gofmt check",
      "PASS go vet ./...",
      "FAIL go test ./...",
    ]);
    expect(within(gates[0]).getByText("PASS")).toHaveClass("text-working");
    expect(within(gates[2]).getByText("FAIL")).toHaveClass("text-error");
    expect(gates[2]).toHaveAttribute("href", hrefFor("job", { id: "6", job: "34" }));
    view.unmount();

    const empty = show({
      flow: board((f) => {
        const gate = stepOf(f, "1", 1).gates?.[0];
        if (gate !== undefined) Object.assign(gate, { verdict: "", job: 0 });
      }),
    });
    const first = stepsOf(empty)[1]?.querySelector('[data-slot="gate"]');
    expect(first?.textContent).toBe("… gofmt check");
    expect(first?.querySelector("span")).toHaveClass("text-muted-foreground");
    expect(first?.tagName).not.toBe("A");
  });

  it("a row with no step says so", () => {
    const view = show({ story: "5" });
    expect(screen.getByText("steps")).toBeInTheDocument();
    expect(screen.getByText("no step yet")).toBeInTheDocument();
    expect(stepsOf(view)).toHaveLength(0);
  });
});

describe("FlowStoryBody findings and reads", () => {
  it("the findings box: the counts, the kept lines of the final row, none for the premise row", () => {
    const two = show({ story: "2" });
    const line = screen.getByText("Critical 0 · Important 0 · Minor 1");
    expect(line).not.toHaveClass("text-muted-foreground");
    two.unmount();

    const one = show({});
    expect(screen.getByText("Critical 0 · Important 0 · Minor 0")).toHaveClass(
      "text-muted-foreground",
    );
    expect(screen.queryByText(/name the cache key/)).toBeNull();
    one.unmount();

    const short = show({
      flow: board((f) => {
        story(f, "1").findings = [];
      }),
    });
    expect(screen.getByText("none yet")).toBeInTheDocument();
    short.unmount();

    const final = show({ story: "F" });
    expect(screen.getByText(/^Critical \d+ · Important \d+ · Minor \d+$/)).toBeInTheDocument();
    expect(
      screen.getByText("Minor internal/flowview/detail.go:88 name the cache key"),
    ).toBeInTheDocument();
    final.unmount();

    show({ story: "P" });
    expect(screen.queryByText("findings")).toBeNull();
  });

  it("the reads box: the plan's lines, each spec section once and the changes", () => {
    const plan = planOfSession(0);
    const view = show({});
    const link = (name: string): HTMLElement => screen.getByRole("link", { name });
    expect(link("plan · Story 1 (L212–604)")).toHaveAttribute(
      "href",
      hrefFor("flowDoc", { id: "6", doc: "plan" }, { plan, at: "212" }),
    );
    expect(link("spec §1")).toHaveAttribute(
      "href",
      hrefFor("flowDoc", { id: "6", doc: "spec" }, { plan, at: "§1" }),
    );
    expect(link("spec §2")).toHaveAttribute(
      "href",
      hrefFor("flowDoc", { id: "6", doc: "spec" }, { plan, at: "§2" }),
    );
    expect(link("7 commits → changes")).toHaveAttribute("href", hrefFor("changes", { id: "6" }));
    view.unmount();

    const four = show({
      story: "4",
      flow: board((f) => {
        story(f, "4").line = 0;
      }),
    });
    const bare = screen.getByRole("link", { name: "plan · Story 4" });
    expect(bare).toHaveAttribute("href", hrefFor("flowDoc", { id: "6", doc: "plan" }, { plan }));
    expect(bare.getAttribute("href")).not.toContain("at=");
    expect(screen.queryByRole("link", { name: /changes/ })).toBeNull();
    four.unmount();
  });

  it("a section the reading names twice is one link, and spaces in it do not matter", () => {
    const withReading = (reading: string): Flow =>
      board((f) => {
        story(f, "1").reading = reading;
      });
    const specLinks = (): string[] =>
      screen.queryAllByRole("link", { name: /^spec §/ }).map((a) => a.textContent ?? "");
    const plan = planOfSession(0);

    const twice = show({ flow: withReading("spec §1, §2, §1 and § 1") });
    expect(specLinks()).toEqual(["spec §1", "spec §2"]);
    twice.unmount();

    const none = show({ flow: withReading("none") });
    expect(specLinks()).toEqual([]);
    none.unmount();

    show({ flow: withReading("spec §3.2") });
    expect(screen.getByRole("link", { name: "spec §3.2" })).toHaveAttribute(
      "href",
      hrefFor("flowDoc", { id: "6", doc: "spec" }, { plan, at: "§3.2" }),
    );
  });

  it("the premise and final rows draw no reads box", () => {
    const premise = show({ story: "P" });
    expect(screen.queryByText("reads")).toBeNull();
    premise.unmount();

    const final = show({ story: "F" });
    expect(screen.queryByText("reads")).toBeNull();
    expect(screen.queryByRole("link", { name: /^plan · Story/ })).toBeNull();
    const items = stepsOf(final);
    expect(items[0]).toHaveTextContent("gates1");
    expect(items[1]).toHaveTextContent("review1");
    expect(items[1]?.getAttribute("data-outcome")).toBe("running");
  });
});

describe("the flowStory panel", () => {
  it("the panel is registered under its id, with the session's head and no tab", () => {
    expect(panelFor("flowStory")).toBe(flowStoryPanel);
    expect(flowStoryPanel.Head).toBe(SessionHead);
    expect(flowStoryPanel.tab).toBeUndefined();
    expect(flowStoryPanel.appliesTo).toBeUndefined();
  });

  it("the panel draws its view through PanelHost once its chunk loads", async () => {
    seed([session6()]);
    vi.mocked(useFlow).mockReturnValue({ flow: fixtureFlow(), error: "", loading: false });
    window.history.replaceState(null, "", "#/s/6/flow/1");
    render(
      <PanelHost
        panel="flowStory"
        props={{ sessionId: 6, params: { id: "6", story: "1" }, search: {} }}
      />,
    );
    expect(await screen.findByText("Story 1")).toBeInTheDocument();
    expect(screen.getByText("steps")).toBeInTheDocument();
  });
});

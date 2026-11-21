import { render, screen, type RenderResult } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import type { FlowDoc, MdNode, Session } from "@/api/gen";
import { useFlowDoc } from "@/api/reads";
import { panelFor } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { elementPane, ScrollPaneProvider, type ScrollPane } from "@/app/scroll";
import { stubScroller, type Scroller } from "@/app/scrollkit";
import { PanelHost } from "@/app/shell";
import { resetFleet, seed } from "@/commands/testkit";
import { fixtureFlowDoc, fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import "@/panels";
import { SessionHead } from "@/session/SessionHead";

import { flowDocPanel } from "./index";
import FlowDocBody from "./FlowDocBody";

vi.mock("@/api/reads", async (original) => ({
  ...(await original<typeof import("@/api/reads")>()),
  useFlowDoc: vi.fn(),
}));

const NOW = Date.parse("2026-11-19T12:00:00Z");

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  vi.mocked(useFlowDoc).mockReturnValue({ doc: undefined, error: "", loading: false });
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

function planOfSession(index = 0): string {
  const plan = session6().flows?.[index]?.plan;
  if (plan === undefined) throw new Error("the canned session 6 lacks that flow");
  return plan;
}

// heading is a heading node of the tree: its text, level and source line.
function heading(text: string, level: number, line = 0): MdNode {
  return { t: "h", level, line, c: [{ t: "text", v: text }] };
}

// show seeds the session (session 6 unless given; null seeds none), makes the read answer with
// the document and error, and draws the document's view on its route.
function show(
  opts: {
    session?: Session | null;
    doc?: FlowDoc;
    error?: string;
    name?: string;
    search?: Record<string, string>;
    pane?: ScrollPane;
  } = {},
): RenderResult {
  const session = opts.session === undefined ? session6() : opts.session;
  const name = opts.name ?? "spec";
  seed(session === null ? [] : [session]);
  vi.mocked(useFlowDoc).mockReturnValue({
    doc: opts.doc ?? fixtureFlowDoc(),
    error: opts.error ?? "",
    loading: false,
  });
  window.history.replaceState(null, "", `#/s/6/flow/doc/${name}`);
  const body = (
    <FlowDocBody sessionId={6} params={{ id: "6", doc: name }} search={opts.search ?? {}} />
  );
  return render(
    opts.pane === undefined ? (
      body
    ) : (
      <ScrollPaneProvider pane={opts.pane}>{body}</ScrollPaneProvider>
    ),
  );
}

// docOf is a document of the headings given (and a paragraph between them).
function docOf(...blocks: MdNode[]): FlowDoc {
  return { path: "docs/x.md", blocks, cut: false };
}

describe("FlowDocBody", () => {
  it("a session the snapshot does not list says it is gone and reads nothing", () => {
    show({ session: null });
    expect(screen.getByText("This session is gone.")).toBeInTheDocument();
    expect(vi.mocked(useFlowDoc).mock.calls.every(([o]) => o.plan === "")).toBe(true);
  });

  it("a session with no flow and no plan says so and reads nothing", () => {
    for (const flows of [[], null]) {
      const view = show({ session: session6({ flows }) });
      expect(screen.getByText("No flow in this session's worktree.")).toBeInTheDocument();
      expect(vi.mocked(useFlowDoc).mock.calls.every(([o]) => o.plan === "")).toBe(true);
      view.unmount();
      vi.mocked(useFlowDoc).mockClear();
    }
  });

  it("reads the route's plan, else the newest flow's, and the document the route names", () => {
    const view = show({});
    expect(useFlowDoc).toHaveBeenLastCalledWith({
      session: 6,
      plan: planOfSession(0),
      doc: "spec",
    });
    view.unmount();
    show({ search: { plan: planOfSession(1) }, name: "report:abc-dev1" });
    expect(useFlowDoc).toHaveBeenLastCalledWith({
      session: 6,
      plan: planOfSession(1),
      doc: "report:abc-dev1",
    });
  });

  it("draws Loading… until the document arrives, and the read's error when none is held", () => {
    seed([session6()]);
    const draw = () =>
      render(<FlowDocBody sessionId={6} params={{ id: "6", doc: "spec" }} search={{}} />);
    vi.mocked(useFlowDoc).mockReturnValue({ doc: undefined, error: "", loading: true });
    const loading = draw();
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    expect(screen.getByRole("link", { name: "‹ board" })).toBeInTheDocument();
    loading.unmount();

    vi.mocked(useFlowDoc).mockReturnValue({
      doc: undefined,
      error: "loom answered 500",
      loading: false,
    });
    draw();
    expect(screen.getByRole("alert")).toHaveTextContent("loom answered 500");
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.getByRole("link", { name: "‹ board" })).toBeInTheDocument();
  });

  it("the head line: a way back to the board with its plan, and the document's name", () => {
    const names: [string, string][] = [
      ["spec", "spec"],
      ["skeleton", "skeleton"],
      ["plan", "plan"],
      ["record", "record"],
      ["drafts-state", "drafts' state"],
      [
        "report:2026-11-09-flow-visualisation-s1-dev1",
        "report · 2026-11-09-flow-visualisation-s1-dev1",
      ],
      ["whatever", "whatever"],
    ];
    for (const [name, text] of names) {
      const view = show({ name, doc: docOf(heading("Other", 1)) });
      expect(screen.getByRole("link", { name: "‹ board" })).toHaveAttribute(
        "href",
        hrefFor("flow", { id: "6" }, { plan: planOfSession(0) }),
      );
      expect(screen.getByText(text, { selector: ".font-semibold" })).toBeInTheDocument();
      view.unmount();
    }
  });

  it("draws the path, and the document's text through Markdown", () => {
    const doc = fixtureFlowDoc();
    show({ doc });
    expect(screen.getByText(doc.path)).toBeInTheDocument();
    expect(
      screen.getByRole("heading", {
        name: "Loom PWA: a flow's design, spec, progress, story states and costs",
      }),
    ).toBeInTheDocument();
  });

  it("the sections index: a details box of the headings of level 3 or less, only when there are more than 2", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const first = show({});
    await user.click(screen.getByText("sections"));
    for (const text of [
      "Loom PWA: a flow's design, spec, progress, story states and costs",
      "Problem",
      "Design",
      "1. Flows and their stages",
      "2. The reader (internal/flowview)",
      "3. Costs",
    ]) {
      expect(screen.getByRole("button", { name: text })).toBeInTheDocument();
    }
    first.unmount();

    const draws = (doc: FlowDoc): boolean => {
      const view = show({ doc });
      const found = screen.queryByText("sections") !== null;
      view.unmount();
      return found;
    };
    expect(draws(docOf(heading("A", 1), heading("B", 2)))).toBe(false);
    expect(draws(docOf(heading("A", 1), heading("B", 3), heading("C", 4)))).toBe(false);
    expect(draws(docOf(heading("A", 1), heading("B", 2), heading("C", 3)))).toBe(true);
    expect(
      draws(docOf(heading("A", 1), heading("B", 2), { t: "quote", c: [heading("C", 3)] })),
    ).toBe(true);
    expect(
      draws(docOf(heading("A", 1), heading("B", 2), { t: "code", c: [heading("C", 3)] })),
    ).toBe(false);
  });

  it("the cut note: drawn only for a cut document", () => {
    const cut = show({ doc: { ...fixtureFlowDoc(), cut: true } });
    expect(screen.getByText("The document was cut short.")).toBeInTheDocument();
    cut.unmount();
    show({});
    expect(screen.queryByText("The document was cut short.")).toBeNull();
    expect(screen.queryByText(/cut at 1 MiB/)).toBeNull();
  });

  it("a held document draws under a failed read's error", () => {
    const doc = fixtureFlowDoc();
    show({ doc, error: "loom answered 500" });
    expect(screen.getByRole("alert")).toHaveTextContent("loom answered 500");
    expect(screen.getByText(doc.path)).toBeInTheDocument();
  });
});

describe("the jump", () => {
  let scrollTo: MockInstance<typeof window.scrollTo>;
  let headerEl: HTMLElement;
  let headingTop: (index: number) => number;
  let scroller: Scroller;
  let paneHead: HTMLElement;

  beforeEach(() => {
    scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
    Object.defineProperty(window, "scrollY", { value: 100, configurable: true });
    headerEl = document.createElement("header");
    headerEl.setAttribute("data-slot", "header");
    document.body.appendChild(headerEl);
    headingTop = (index) => 1000 + 100 * index;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
      this: HTMLElement,
    ) {
      const rect = { top: 0, height: 0 };
      if (this === headerEl) rect.height = 120;
      if (this === scroller.el) rect.top = 200;
      if (this === paneHead) rect.height = 70;
      const headings = Array.from(document.querySelectorAll("h3, h4"));
      const at = headings.indexOf(this);
      if (at >= 0) rect.top = headingTop(at);
      return rect as DOMRect;
    });
  });

  beforeEach(() => {
    scroller = stubScroller({ height: 4000, viewport: 600, top: 300 });
    paneHead = document.createElement("div");
  });

  afterEach(() => {
    headerEl.remove();
    Reflect.deleteProperty(window, "scrollY");
  });

  it("at §2 lands the second numbered section just below the header", () => {
    show({ search: { at: "§2" } });
    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenCalledWith(0, 1000 + 100 * 4 + 100 - 120);
  });

  it("a section number is not a prefix of another", () => {
    const view = show({ search: { at: "§1" } });
    expect(scrollTo).toHaveBeenCalledWith(0, 1000 + 100 * 3 + 100 - 120);
    view.unmount();
    scrollTo.mockClear();

    show({
      doc: docOf(heading("10. Ten", 3, 5), heading("1. One", 3, 9), heading("Last", 3, 12)),
      search: { at: "§1" },
    });
    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenCalledWith(0, 1000 + 100 * 1 + 100 - 120);
  });

  it("at a source line lands the last heading at or above it", () => {
    const cases: [string, number][] = [
      ["130", 4],
      ["62", 3],
      ["5", 0],
      ["190", 5],
    ];
    for (const [at, index] of cases) {
      scrollTo.mockClear();
      const view = show({ search: { at } });
      expect(scrollTo).toHaveBeenCalledTimes(1);
      expect(scrollTo).toHaveBeenCalledWith(0, 1000 + 100 * index + 100 - 120);
      view.unmount();
    }
  });

  it("an at that matches no heading leaves the page where it is", () => {
    for (const at of ["§9", "abc", "0", ""]) {
      const view = show({ search: { at } });
      view.unmount();
    }
    const bare = show({
      doc: docOf({ t: "p", c: [{ t: "text", v: "no headings" }] }),
      search: { at: "§1" },
    });
    bare.unmount();
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("the jump runs once per document and at, and again for a new at", () => {
    const view = show({ search: { at: "§2" } });
    expect(scrollTo).toHaveBeenCalledTimes(1);
    const draw = (at: string) => (
      <FlowDocBody sessionId={6} params={{ id: "6", doc: "spec" }} search={{ at }} />
    );
    view.rerender(draw("§2"));
    expect(scrollTo).toHaveBeenCalledTimes(1);
    view.rerender(draw("§3"));
    expect(scrollTo).toHaveBeenCalledTimes(2);
    expect(scrollTo).toHaveBeenLastCalledWith(0, 1000 + 100 * 5 + 100 - 120);
  });

  it("a heading never lands above the top of the page", () => {
    Object.defineProperty(window, "scrollY", { value: 0, configurable: true });
    headingTop = () => 10;
    show({ search: { at: "§2" } });
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });

  it("inside a pane the jump scrolls the pane just below its head, not the window", () => {
    show({ search: { at: "§2" }, pane: elementPane(scroller.el, paneHead) });
    expect(scroller.scrolls).toEqual([1000 + 100 * 4 - 200 + 300 - 70]);
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("a pane with no head subtracts nothing", () => {
    show({ search: { at: "§2" }, pane: elementPane(scroller.el, null) });
    expect(scroller.scrolls).toEqual([1500]);
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("a heading never lands above the top of the pane", () => {
    headingTop = () => 10;
    scroller.set({ top: 0 });
    show({ search: { at: "§2" }, pane: elementPane(scroller.el, paneHead) });
    expect(scroller.scrolls).toEqual([0]);
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("the sections index's buttons jump inside the pane", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    show({ pane: elementPane(scroller.el, null) });
    await user.click(screen.getByText("sections"));
    await user.click(screen.getByRole("button", { name: "3. Costs" }));
    expect(scroller.scrolls).toEqual([1000 + 100 * 5 - 200 + 300]);
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("the sections index's buttons jump to their own heading", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const first = show({});
    expect(scrollTo).not.toHaveBeenCalled();
    await user.click(screen.getByText("sections"));
    await user.click(screen.getByRole("button", { name: "3. Costs" }));
    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenLastCalledWith(0, 1000 + 100 * 5 + 100 - 120);
    await user.click(screen.getByRole("button", { name: "Problem" }));
    expect(scrollTo).toHaveBeenLastCalledWith(0, 1000 + 100 * 1 + 100 - 120);
    first.unmount();
    scrollTo.mockClear();

    const at = show({ search: { at: "§2" } });
    expect(scrollTo).toHaveBeenCalledTimes(1);
    scrollTo.mockClear();
    await user.click(screen.getByText("sections"));
    await user.click(screen.getByRole("button", { name: "3. Costs" }));
    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenLastCalledWith(0, 1000 + 100 * 5 + 100 - 120);
    at.unmount();
    scrollTo.mockClear();

    // A level-4 heading shifts the position among the buttons, not among the headings.
    show({
      doc: docOf(heading("A", 1, 1), heading("B", 2, 2), heading("C", 4, 3), heading("D", 3, 4)),
    });
    await user.click(screen.getByText("sections"));
    await user.click(screen.getByRole("button", { name: "D" }));
    expect(scrollTo).toHaveBeenLastCalledWith(0, 1000 + 100 * 3 + 100 - 120);
  });
});

describe("the flowDoc panel", () => {
  it("the panel is registered under its id, with the session's head and no tab", () => {
    expect(panelFor("flowDoc")).toBe(flowDocPanel);
    expect(flowDocPanel.Head).toBe(SessionHead);
    expect(flowDocPanel.tab).toBeUndefined();
    expect(flowDocPanel.appliesTo).toBeUndefined();
  });

  it("the panel draws its view through PanelHost once its chunk loads", async () => {
    const doc = fixtureFlowDoc();
    seed([session6()]);
    vi.mocked(useFlowDoc).mockReturnValue({ doc, error: "", loading: false });
    window.history.replaceState(null, "", "#/s/6/flow/doc/spec");
    render(
      <PanelHost
        panel="flowDoc"
        props={{ sessionId: 6, params: { id: "6", doc: "spec" }, search: {} }}
      />,
    );
    expect(await screen.findByText("spec")).toBeInTheDocument();
    expect(screen.getByText(doc.path)).toBeInTheDocument();
  });
});

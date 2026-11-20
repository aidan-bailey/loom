import { render, screen, type RenderResult } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { FlowDoc, MdNode, Session } from "@/api/gen";
import { useFlowDoc } from "@/api/reads";
import { hrefFor } from "@/app/routes";
import { resetFleet, seed } from "@/commands/testkit";
import { fixtureFlowDoc, fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";

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
  return render(
    <FlowDocBody sessionId={6} params={{ id: "6", doc: name }} search={opts.search ?? {}} />,
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

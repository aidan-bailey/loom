import { render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Pr, Session, Snapshot, Usage } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions, shownWorkspaces } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";
import { usePending } from "@/store/pending";

import { checksText, groupGlyph, parityText } from "./facts";
import { FactStrip } from "./FactStrip";
import { NoSession } from "./NoSession";
import { SessionHead } from "./SessionHead";
import { SubLine } from "./SubLine";

const NOW = Date.parse("2026-11-18T12:00:00Z");

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

// load stores a snapshot holding only one session (the fixture's first, changed by `over`) in the
// first shown workspace, and returns it.
function load(over: Partial<Session> = {}, usage?: Usage[]): Session {
  const snapshot = fixtureSnapshot(NOW);
  const workspace = shownWorkspaces(snapshot)[0];
  const base = allSessions(snapshot)[0];
  if (workspace === undefined || base === undefined) throw new Error("the fixture holds none");
  const session: Session = {
    ...base,
    id: 7,
    claude: true,
    account: "",
    since: new Date(NOW - 5 * 60_000).toISOString(),
    ...over,
    workspace: workspace.id,
  };
  useFleet.setState({
    snapshot: {
      ...snapshot,
      usage: usage ?? snapshot.usage,
      sessions: { [String(workspace.id)]: [session] },
    },
    stale: false,
    link: "live",
  });
  return session;
}

describe("facts", () => {
  it("parityText words the TUI's parity token", () => {
    const s = load();
    expect(parityText({ ...s, parity_known: false, ahead: 3, behind: 0 })).toBe("");
    expect(parityText({ ...s, parity_known: true, ahead: 0, behind: 0 })).toBe("✓ even");
    expect(parityText({ ...s, parity_known: true, ahead: 2, behind: 0 })).toBe("↑2 ↓0");
    expect(parityText({ ...s, parity_known: true, ahead: 0, behind: 4 })).toBe("↑0 ↓4");
  });

  it("checksText words an open PR's checks and any other PR by its state", () => {
    const pr = (state: Pr["state"], checks: string): Pr => ({ number: 9, state, checks }) as Pr;
    expect(checksText(pr("open", "passing"))).toBe("✓ checks pass");
    expect(checksText(pr("open", "pending"))).toBe("checks pending");
    expect(checksText(pr("open", "failing"))).toBe("✗ checks fail");
    expect(checksText(pr("open", "none"))).toBe("no checks");
    expect(checksText(pr("open", "mystery"))).toBe("");
    expect(checksText(pr("merged", "passing"))).toBe("merged");
    expect(checksText(pr("closed", "failing"))).toBe("closed");
    expect(checksText(pr("draft", "none"))).toBe("draft");
  });

  it("groupGlyph is each group's glyph, and Paused's for one it does not know", () => {
    expect(groupGlyph("needs_you")).toBe("●");
    expect(groupGlyph("your_turn")).toBe("◆");
    expect(groupGlyph("working")).toBe("◐");
    expect(groupGlyph("paused")).toBe("○");
    expect(groupGlyph("somethingnew")).toBe("○");
  });
});

describe("SubLine and NoSession", () => {
  // rename sets the workspace's label and name in the loaded snapshot.
  function rename(label: string, name: string): void {
    const snapshot = useFleet.getState().snapshot as Snapshot;
    const first = shownWorkspaces(snapshot)[0];
    useFleet.setState({
      snapshot: {
        ...snapshot,
        workspaces: (snapshot.workspaces ?? []).map((w) =>
          w.id === first?.id ? { ...w, label, name } : w,
        ),
      },
    });
  }

  it("SubLine words the status, the age, the workspace and the branch", () => {
    const session = load({ group: "working", label: "Running", branch: "feat/x" });
    rename("", "loom");

    const { container } = render(<SubLine session={session} />);

    expect(container.firstElementChild?.children[0]).toHaveTextContent("◐ running · 5m");
    expect(screen.getByText("loom")).toBeInTheDocument();
    expect(screen.getByText("feat/x")).toBeInTheDocument();
  });

  it("SubLine prefers the workspace's label, and leaves out what the session lacks", () => {
    const session = load({ group: "working", label: "Running", branch: "", since: "" });
    rename("Main", "loom");

    const { container } = render(<SubLine session={session} />);

    const row = container.firstElementChild as HTMLElement;
    expect(row.children).toHaveLength(2);
    expect(row.children[0]?.textContent).toBe("◐ running");
    expect(row.children[1]?.textContent).toBe("Main");
  });

  it("NoSession says the session is gone", () => {
    render(<NoSession sessionId={undefined} />);
    expect(screen.getByRole("status")).toHaveTextContent("This session is gone.");
  });
});

const hashAt = (hash: string): void => {
  window.history.replaceState(null, "", hash);
};

const headProps = { sessionId: 7, params: { id: "7" }, search: {} };

const usageAt = (pct: number): Usage[] => [
  {
    account: "default",
    plan: "",
    five_hour: { pct, resets_at: new Date(NOW + 3_600_000).toISOString() },
    seven_day: null,
    at: new Date(NOW).toISOString(),
  },
];

describe("SessionHead", () => {
  const claudeLink = () => screen.queryByRole("link", { name: /^claude/ });

  it("draws the title as the h1 and a way back to the fleet", () => {
    const session = load({ title: "Fix the thing" });
    hashAt("#/s/7");
    render(<SessionHead {...headProps} />);

    expect(screen.getByRole("heading", { level: 1, name: session.title })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /fleet/ })).toHaveAttribute("href", "#/");
  });

  it("links to claude on the session's remote_url, in a new window, with no referrer or opener", () => {
    load({ group: "working", remote_url: "https://claude.ai/code/abc" });
    hashAt("#/s/7");
    render(<SessionHead {...headProps} />);

    const link = claudeLink();
    expect(link).toHaveAttribute("href", "https://claude.ai/code/abc");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link?.getAttribute("rel")).toContain("noopener");
    expect(link?.getAttribute("rel")).toContain("noreferrer");
  });

  it("leaves the claude link to the needs-you box on the conversation of a waiting Claude session", () => {
    load({ group: "needs_you", claude: true, remote_url: "https://claude.ai/code/abc" });
    hashAt("#/s/7");
    render(<SessionHead {...headProps} />);

    expect(claudeLink()).toBeNull();
  });

  it.each([
    ["off the conversation", { group: "needs_you", claude: true }, "#/s/7/info"],
    ["for a session not waiting", { group: "working", claude: true }, "#/s/7"],
    ["for a session that is not Claude's", { group: "needs_you", claude: false }, "#/s/7"],
  ] as const)(
    "keeps the claude link wherever the needs-you box is not on screen: %s",
    (_n, over, hash) => {
      load({ ...over, remote_url: "https://claude.ai/code/abc" });
      hashAt(hash);
      render(<SessionHead {...headProps} />);

      expect(claudeLink()).not.toBeNull();
    },
  );

  it("draws no claude link while Remote Control is off", () => {
    load({ group: "working", remote_url: "" });
    hashAt("#/s/7");
    render(<SessionHead {...headProps} />);

    expect(claudeLink()).toBeNull();
  });

  it("draws only the way back for a session the snapshot does not list", () => {
    load();
    hashAt("#/s/8");
    render(<SessionHead sessionId={8} params={{ id: "8" }} search={{}} />);

    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("");
    expect(screen.getByRole("link", { name: /fleet/ })).toBeInTheDocument();
    expect(claudeLink()).toBeNull();
  });
});

describe("FactStrip", () => {
  const working = (over: Partial<Session> = {}, usage: Usage[] = usageAt(42)): Session =>
    load(
      {
        group: "working",
        label: "Running",
        diff: { added: 0, removed: 0 },
        pr: null,
        jobs_label: "",
        jobs_focus: 0,
        ...over,
      },
      usage,
    );
  const strip = (container: HTMLElement) => container.firstElementChild as HTMLElement;

  it("words the status, the changes, the PR and the share on one line", () => {
    working({
      diff: { added: 12, removed: 3 },
      pr: { number: 9, state: "open", checks: "passing" },
    });
    hashAt("#/s/7");
    const { container } = render(<FactStrip {...headProps} />);

    expect(strip(container)).toHaveTextContent("◐ running 5m · +12 −3 · PR #9 ✓ · 42%");
  });

  it("leaves out the facts the session lacks", () => {
    working({}, []);
    hashAt("#/s/7");
    const { container } = render(<FactStrip {...headProps} />);

    expect(strip(container).textContent?.trim()).toBe("◐ running 5m");
  });

  it("tints the share amber from 80 percent", () => {
    working({}, usageAt(85));
    hashAt("#/s/7");
    const hot = render(<FactStrip {...headProps} />);
    expect(screen.getByText("85%")).toHaveClass("text-attention");
    hot.unmount();

    working({}, usageAt(42));
    render(<FactStrip {...headProps} />);
    expect(screen.getByText("42%")).not.toHaveClass("text-attention");
  });

  it("is one link to the info tab with no jobs", () => {
    working();
    hashAt("#/s/7");
    const { container } = render(<FactStrip {...headProps} />);

    const links = within(strip(container)).getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAttribute("href", "#/s/7/info");
    expect(links[0]).toHaveTextContent("◐ running 5m");
  });

  it("splits off the jobs badge, linking to the job it names or to info", () => {
    working({ jobs_label: "⚙ 1 running", jobs_focus: 216 });
    hashAt("#/s/7");
    const first = render(<FactStrip {...headProps} />);

    let links = within(strip(first.container)).getAllByRole("link");
    expect(links).toHaveLength(2);
    expect(links[0]).toHaveAttribute("href", "#/s/7/info");
    expect(links[1]).toHaveAttribute("href", "#/s/7/job/216");
    expect(links[1]).toHaveTextContent("⚙ 1 running");
    first.unmount();

    working({ jobs_label: "⚙ queued", jobs_focus: 0 });
    const second = render(<FactStrip {...headProps} />);
    links = within(strip(second.container)).getAllByRole("link");
    expect(links[1]).toHaveAttribute("href", "#/s/7/info");
    expect(links[1]).toHaveTextContent("⚙ queued");
  });

  it("draws nothing off the conversation, for a non-Claude session, or for a session the snapshot does not list", () => {
    working();
    for (const hash of ["#/s/7/info", "#/s/7/diff"]) {
      hashAt(hash);
      const { container, unmount } = render(<FactStrip {...headProps} />);
      expect(container).toBeEmptyDOMElement();
      unmount();
    }

    working({ claude: false });
    hashAt("#/s/7");
    const plain = render(<FactStrip {...headProps} />);
    expect(plain.container).toBeEmptyDOMElement();
    plain.unmount();

    working();
    const gone = render(<FactStrip sessionId={8} params={{ id: "8" }} search={{}} />);
    expect(gone.container).toBeEmptyDOMElement();
  });
});

describe("pending session", () => {
  afterEach(() => {
    usePending.getState().clear();
  });

  const head = { sessionId: 9, params: { id: "9" }, search: {} };

  it("SessionHead titles a pending session Loading…", () => {
    load();
    usePending.setState({ id: 9 });
    render(<SessionHead sessionId={9} params={{ id: "9" }} search={{}} />);
    expect(screen.getByRole("heading").textContent).toBe("Loading…");
    expect(screen.getByRole("link").getAttribute("href")).toBe("#/");
  });

  it("SessionHead keeps an empty title for an unlisted session that is not pending, and for another session's pending id", () => {
    load();
    const first = render(<SessionHead {...head} />);
    expect(usePending.getState().id).toBeNull();
    expect(screen.getByRole("heading").textContent).toBe("");
    first.unmount();
    usePending.setState({ id: 8 });
    render(<SessionHead {...head} />);
    expect(screen.getByRole("heading").textContent).toBe("");
  });

  it("SessionHead keeps a listed session's title while its id is pending", () => {
    load({ title: "listed" });
    usePending.setState({ id: 7 });
    render(<SessionHead sessionId={7} params={{ id: "7" }} search={{}} />);
    expect(screen.getByRole("heading").textContent).toBe("listed");
  });

  it("NoSession says Loading… for the pending session", () => {
    load();
    usePending.setState({ id: 9 });
    render(<NoSession sessionId={9} />);
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    expect(screen.queryByText("This session is gone.")).not.toBeInTheDocument();
  });

  it("NoSession still says gone for an unpending session, another session's pending id, and no session id", () => {
    load();
    const cases: [number | null, number | undefined][] = [
      [null, 9],
      [8, 9],
      [9, undefined],
    ];
    for (const [id, sessionId] of cases) {
      usePending.setState({ id });
      const view = render(<NoSession sessionId={sessionId} />);
      expect(screen.getByRole("status")).toHaveTextContent("This session is gone.");
      view.unmount();
    }
  });

  it("NoSession reads no route", () => {
    load();
    usePending.setState({ id: 9 });
    window.history.replaceState(null, "", "#/s/9");
    const gone = render(<NoSession sessionId={undefined} />);
    expect(screen.getByRole("status")).toHaveTextContent("This session is gone.");
    gone.unmount();

    window.history.replaceState(null, "", "#/");
    render(<NoSession sessionId={9} />);
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
  });
});

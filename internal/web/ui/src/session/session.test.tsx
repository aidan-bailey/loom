import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Pr, Session, Snapshot, Usage } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions, shownWorkspaces } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import { checksText, groupGlyph, parityText } from "./facts";
import { NoSession } from "./NoSession";
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
    render(<NoSession />);
    expect(screen.getByRole("status")).toHaveTextContent("This session is gone.");
  });
});

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";

import type { Session, SessionGroup, Snapshot } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions, shownWorkspaces } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import { GroupCounts, HostLabel, LinkPill } from "./status";

afterEach(() => {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

// withGroups is the fixture's snapshot cut down to one session per given group in the first shown
// workspace, `since` ascending in that order, all running Claude.
function withGroups(groups: SessionGroup[]): Snapshot {
  const snapshot = fixtureSnapshot();
  const workspace = shownWorkspaces(snapshot)[0];
  const base = allSessions(snapshot)[0];
  if (workspace === undefined || base === undefined) throw new Error("the fixture holds none");
  const sessions: Session[] = groups.map((group, i) => ({
    ...base,
    id: 100 + i,
    workspace: workspace.id,
    group,
    claude: true,
    since: new Date(Date.UTC(2026, 10, 18, 12, i)).toISOString(),
  }));
  return { ...snapshot, host: "devbox", sessions: { [String(workspace.id)]: sessions } };
}

function live(snapshot: Snapshot): void {
  useFleet.setState({ snapshot, stale: false, link: "live", updated: Date.now() });
}

describe("LinkPill", () => {
  it.each([
    ["live", "LIVE"],
    ["connecting", "CONNECTING"],
    ["restarting", "RESTARTING"],
    ["offline", "OFFLINE"],
  ] as const)("says each link state: %s", (link, label) => {
    useFleet.setState({ snapshot: withGroups(["working"]), stale: link !== "live", link });
    render(<LinkPill />);
    expect(screen.getByText(label)).toBeInTheDocument();
  });
});

describe("HostLabel", () => {
  it("names the host, and the time of the last update only while offline", () => {
    live(withGroups(["working"]));
    const { container, unmount } = render(<HostLabel />);
    expect(container).toHaveTextContent("loom@devbox");
    expect(container.textContent).not.toContain(" · ");
    unmount();

    const updated = Date.UTC(2026, 10, 18, 12, 5);
    useFleet.setState({ snapshot: withGroups(["working"]), stale: true, link: "offline", updated });
    render(<HostLabel />);
    // ICU puts a narrow no-break space before "PM" on Node 22, and Testing Library normalises
    // only the node's text: normalise the expected one too.
    const clock = new Date(updated)
      .toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })
      .replace(/\s+/g, " ");
    expect(screen.getByText(`devbox · ${clock}`)).toBeInTheDocument();
  });

  it("says loom with no snapshot", () => {
    const { container } = render(<HostLabel />);
    expect(container).toHaveTextContent(/^loom$/);
  });
});

describe("GroupCounts", () => {
  it("counts each group and draws a zero as no button", () => {
    live(withGroups(["needs_you", "needs_you", "your_turn"]));
    render(<GroupCounts />);

    expect(screen.getByRole("button", { name: "2 need you: open the next" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "1 your turn: open the next" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /working/ })).toBeNull();
    expect(screen.getByText("0")).toBeInTheDocument();
  });

  it("opens the next session of a group after the one on screen", async () => {
    live(withGroups(["needs_you", "needs_you"]));
    window.history.replaceState(null, "", "#/s/100");
    const first = render(<GroupCounts />);
    await userEvent.click(screen.getByRole("button", { name: "2 need you: open the next" }));
    expect(window.location.hash).toBe("#/s/101");
    first.unmount();

    window.history.replaceState(null, "", "#/s/101");
    render(<GroupCounts />);
    await userEvent.click(screen.getByRole("button", { name: "2 need you: open the next" }));
    expect(window.location.hash).toBe("#/s/100");
  });

  it("keeps its 44px minimum, and drops it when dense", () => {
    live(withGroups(["needs_you", "your_turn"]));
    const counts = (): HTMLElement[] => [
      screen.getByRole("button", { name: "1 need you: open the next" }),
      screen.getByRole("button", { name: "1 your turn: open the next" }),
      screen.getByText("0").closest("span") as HTMLElement,
    ];

    const plain = render(<GroupCounts />);
    for (const el of counts()) expect(el.className).toContain("min-h-[44px]");
    plain.unmount();

    render(<GroupCounts dense />);
    for (const el of counts()) expect(el.className).not.toContain("min-h-[44px]");
  });

  it("draws faint counts that stay buttons while the stream is down", () => {
    useFleet.setState({ snapshot: withGroups(["needs_you"]), stale: true, link: "offline" });
    render(<GroupCounts />);
    const button = screen.getByRole("button", { name: "1 need you: open the next" });
    expect(button.className).toContain("opacity-60");
    expect(button).toBeEnabled();
  });
});

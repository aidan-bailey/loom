import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { lazy, type ComponentType, type MouseEvent, type ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Session, SessionGroup, Snapshot } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions, shownWorkspaces } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import { askConfirm } from "./ConfirmSheet";
import { definePanel, type PanelProps } from "./panels";
import { router, useRoute } from "./routes";
import { AppShell, ContextRow, PanelHost, Placeholder, SessionTabs, StatusRow } from "./shell";
import { switchApp } from "./switchApp";
import { showMessage } from "./toasts";

vi.mock("./switchApp", () => ({ switchApp: vi.fn(() => Promise.resolve("switched")) }));

const props: PanelProps = { sessionId: 5, params: { id: "5" }, search: {} };

afterEach(() => {
  vi.mocked(switchApp).mockClear();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

describe("Placeholder", () => {
  it("names the screen, links back to the fleet and offers the classic app", async () => {
    render(<Placeholder panel="flows" />);

    expect(screen.getByRole("heading", { name: "Flows" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /fleet/ })).toHaveAttribute("href", "#/");

    await userEvent.click(screen.getByRole("button", { name: "Back to classic" }));
    expect(switchApp).toHaveBeenCalledWith("classic");
  });
});

describe("PanelHost", () => {
  it("draws the placeholder for a panel nothing registered", () => {
    render(<PanelHost panel="flowDoc" props={props} />);
    expect(screen.getByRole("heading", { name: "Flow document" })).toBeInTheDocument();
  });

  it("draws a registered panel's body with its props", () => {
    definePanel({
      id: "info",
      title: () => "Info",
      Body: (p: PanelProps) => <p>session {p.sessionId}</p>,
    });
    render(<PanelHost panel="info" props={props} />);
    expect(screen.getByText("session 5")).toBeInTheDocument();
  });

  it("shows what failed and a reload for a panel that throws, and recovers on another route", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      definePanel({
        id: "changes",
        title: () => "Changes",
        Body: (p: PanelProps) => {
          if (p.params.id === "1") throw new Error("boom");
          return <p>fine</p>;
        },
      });
      const { rerender } = render(
        <PanelHost panel="changes" props={{ sessionId: 1, params: { id: "1" }, search: {} }} />,
      );

      const alert = screen.getByRole("alert");
      expect(alert).toHaveTextContent("Changes failed to draw");
      expect(alert).toHaveTextContent("boom");
      expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();

      // Another session's changes is another route: the boundary starts over.
      rerender(
        <PanelHost panel="changes" props={{ sessionId: 2, params: { id: "2" }, search: {} }} />,
      );
      expect(screen.getByText("fine")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  it("recovers when only the search changes (another plan or document)", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      definePanel({
        id: "changes",
        title: () => "Changes",
        Body: (p: PanelProps) => {
          if (p.search.plan === "bad.md") throw new Error("boom");
          return <p>fine</p>;
        },
      });
      const at = (plan: string): PanelProps => ({
        sessionId: 1,
        params: { id: "1" },
        search: { plan },
      });
      const { rerender } = render(<PanelHost panel="changes" props={at("bad.md")} />);
      expect(screen.getByRole("alert")).toHaveTextContent("boom");

      rerender(<PanelHost panel="changes" props={at("good.md")} />);
      expect(screen.getByText("fine")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  it("draws a skeleton while a lazy body loads, then the body", async () => {
    let release: (m: { default: ComponentType<PanelProps> }) => void = () => {};
    const gate = new Promise<{ default: ComponentType<PanelProps> }>((resolve) => {
      release = resolve;
    });
    definePanel({ id: "new", title: () => "Lazy", Body: lazy(() => gate) });
    render(<PanelHost panel="new" props={props} />);

    expect(screen.getByRole("status", { name: "Loading" })).toBeInTheDocument();
    expect(screen.queryByText("loaded")).toBeNull();

    await act(() => {
      release({ default: () => <p>loaded</p> });
      return Promise.resolve();
    });
    expect(screen.getByText("loaded")).toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "Loading" })).toBeNull();
  });

  it("shows what failed and a reload when a lazy body's chunk does not load", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      definePanel({
        id: "new",
        title: () => "Lazy",
        Body: lazy(() => Promise.reject(new Error("chunk gone"))),
      });
      render(<PanelHost panel="new" props={props} />);

      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent("Lazy failed to draw");
      expect(alert).toHaveTextContent("chunk gone");
      expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
    } finally {
      spy.mockRestore();
    }
  });

  it("a body that is not lazy is drawn at once, with no skeleton", () => {
    definePanel({ id: "new", title: () => "Plain", Body: () => <p>plain</p> });
    render(<PanelHost panel="new" props={props} />);

    expect(screen.getByText("plain")).toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "Loading" })).toBeNull();
  });
});

// shellAt draws AppShell at the hash, over a one-route router of its own
// (memory history), whose only screen is `outlet`.
function shellAt(hash: string, outlet: ReactElement = <p>outlet</p>) {
  window.history.replaceState(null, "", hash);
  const root = createRootRoute({ component: AppShell });
  const index = createRoute({ getParentRoute: () => root, path: "/", component: () => outlet });
  const testRouter = createRouter({
    routeTree: root.addChildren([index]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return render(<RouterProvider router={testRouter} />);
}

const header = () => document.querySelector('[data-slot="header"]');
const footer = () => document.querySelector('[data-slot="footer"]');

describe("AppShell", () => {
  it.each([
    ["connecting", "Connecting…"],
    ["restarting", "loom is restarting…"],
    ["offline", "Can't reach loom. Retrying…"],
  ] as const)("while %s with no snapshot it draws the splash and no chrome", async (link, text) => {
    useFleet.setState({ link });
    shellAt("#/");

    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.queryByText("outlet")).toBeNull();
    expect(header()).toBeNull();
    expect(footer()).toBeNull();
  });

  it("offers the way back to the classic app on a route no panel is registered for, with no snapshot", async () => {
    useFleet.setState({ link: "offline" });
    shellAt("#/settings");

    expect(await screen.findByText("Can't reach loom. Retrying…")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Back to classic" }));
    expect(switchApp).toHaveBeenCalledWith("classic");
  });

  it("draws pairing without a snapshot, and without chrome", async () => {
    shellAt("#/pair");

    expect(await screen.findByText("outlet")).toBeInTheDocument();
    expect(screen.queryByText("Connecting…")).toBeNull();
    expect(header()).toBeNull();
    expect(footer()).toBeNull();
  });

  it("draws the panel's head in the header, the screen in main and the footer slot", async () => {
    definePanel({
      id: "fleet",
      title: () => "Fleet",
      Head: () => <p>fleet head</p>,
      Body: () => null,
    });
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" });
    shellAt("#/");

    expect(await screen.findByText("outlet")).toBeInTheDocument();
    expect(header()).toHaveTextContent("fleet head");
    expect(document.querySelector("main")).toHaveTextContent("outlet");
    expect(footer()).not.toBeNull();
  });

  it("draws the full mockup with neither header nor footer", async () => {
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" });
    shellAt("#/s/5/mockup/full");

    expect(await screen.findByText("outlet")).toBeInTheDocument();
    expect(header()).toBeNull();
    expect(footer()).toBeNull();
  });

  it("keeps the screen and the footer when the head throws", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      definePanel({
        id: "fleet",
        title: () => "Fleet",
        Head: () => {
          throw new Error("head boom");
        },
        Body: () => null,
      });
      useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" });
      shellAt("#/");

      expect(await screen.findByText("outlet")).toBeInTheDocument();
      expect(header()).not.toBeNull();
      expect(header()).not.toHaveTextContent("head boom");
      expect(footer()).not.toBeNull();
    } finally {
      spy.mockRestore();
    }
  });
});

describe("router", () => {
  it("draws the placeholder of a route no panel is registered for", async () => {
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" });
    render(<RouterProvider router={router} />);

    await act(async () => {
      await router.navigate({ to: "/settings" });
    });

    expect(await screen.findByRole("heading", { name: "Settings" })).toBeInTheDocument();
    expect(window.location.hash).toBe("#/settings");
  });

  it("draws the fleet for a path no route matches (spec §27)", async () => {
    definePanel({ id: "fleet", title: () => "Fleet", Body: () => <p>the fleet panel</p> });
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" });
    render(<RouterProvider router={router} />);

    await act(async () => {
      await router.navigate({ to: "/s/5/unknown" });
    });

    expect(await screen.findByText("the fleet panel")).toBeInTheDocument();
  });
});

// withGroups is the fixture with only these sessions, ids 100, 101, … in the
// first shown workspace, each of the given group, `since` ascending in that
// order (so the first needs_you has waited longest), all running Claude.
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

describe("ContextRow", () => {
  it("draws the title as the h1, the actions, and a way back named by where it goes", () => {
    definePanel({
      id: "info",
      title: () => "Info",
      tab: { order: 3, label: "info" },
      Body: () => null,
    });

    const { rerender } = render(
      <ContextRow title="Fleet" back="#/" actions={<button type="button">New</button>} />,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Fleet" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /fleet/ })).toHaveAttribute("href", "#/");

    rerender(<ContextRow title="Changes" back="#/s/5/info" />);
    expect(screen.getByRole("link", { name: /info/ })).toHaveAttribute("href", "#/s/5/info");
  });

  it("draws no way back without one", () => {
    render(<ContextRow title="Settings" />);
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("calls onBack with the click and lets it cancel the navigation", () => {
    const onBack = vi.fn<(e: MouseEvent<HTMLAnchorElement>) => void>((e) => {
      e.preventDefault();
    });
    render(<ContextRow title="Subagent" back="#/s/5" onBack={onBack} />);

    const link = screen.getByRole("link", { name: /conversation|back/ });
    expect(fireEvent.click(link)).toBe(false);
    expect(onBack).toHaveBeenCalledTimes(1);
    expect(link).toHaveAttribute("href", "#/s/5");
  });
});

describe("SessionTabs", () => {
  function tabPanels(): void {
    definePanel({
      id: "turns",
      title: () => "Conversation",
      tab: { order: 1, label: "conversation" },
      appliesTo: (s) => s.claude,
      Body: () => null,
    });
    definePanel({
      id: "changes",
      title: () => "Changes",
      tab: { order: 2, label: "changes" },
      appliesTo: (s) => s.claude,
      Body: () => null,
    });
    definePanel({
      id: "info",
      title: () => "Info",
      tab: { order: 3, label: "info" },
      Body: () => null,
    });
  }

  function aSession(claude: boolean): Session {
    const base = allSessions(fixtureSnapshot())[0];
    if (base === undefined) throw new Error("the fixture holds no session");
    return { ...base, id: 7, claude };
  }

  it("links the tabs that apply, in order, and marks the current one", () => {
    tabPanels();
    window.history.replaceState(null, "", "#/s/7/info");

    render(<SessionTabs session={aSession(true)} />);

    const links = within(screen.getByRole("navigation", { name: "Session views" })).getAllByRole(
      "link",
    );
    expect(links.map((a) => [a.textContent, a.getAttribute("href")])).toEqual([
      ["conversation", "#/s/7"],
      ["changes", "#/s/7/diff"],
      ["info", "#/s/7/info"],
    ]);
    expect(screen.getByRole("link", { name: "info" })).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: "changes" })).not.toHaveAttribute("aria-current");
  });

  it("draws no row for a lone tab", () => {
    tabPanels();
    window.history.replaceState(null, "", "#/s/7/info");

    render(<SessionTabs session={aSession(false)} />);

    expect(screen.queryByRole("navigation")).toBeNull();
  });

  it("keeps the tab under the user when it no longer applies", () => {
    tabPanels();
    window.history.replaceState(null, "", "#/s/7/diff");

    render(<SessionTabs session={aSession(false)} />);

    const links = within(screen.getByRole("navigation", { name: "Session views" })).getAllByRole(
      "link",
    );
    expect(links.map((a) => a.textContent)).toEqual(["changes", "info"]);
    expect(screen.getByRole("link", { name: "changes" })).toHaveAttribute("aria-current", "page");
  });

  it("draws a tab's Mark after its label, handed the session", () => {
    tabPanels();
    definePanel({
      id: "changes",
      title: () => "Changes",
      tab: { order: 2, label: "changes", Mark: ({ session }) => <b>+{session.id}</b> },
      appliesTo: (s) => s.claude,
      Body: () => null,
    });
    window.history.replaceState(null, "", "#/s/7/info");

    render(<SessionTabs session={aSession(true)} />);

    const links = within(screen.getByRole("navigation", { name: "Session views" })).getAllByRole(
      "link",
    );
    expect(links.map((a) => a.textContent)).toEqual(["conversation", "changes+7", "info"]);
  });

  it("keeps its tabs when a Mark throws", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      tabPanels();
      definePanel({
        id: "changes",
        title: () => "Changes",
        tab: {
          order: 2,
          label: "changes",
          Mark: () => {
            throw new Error("mark boom");
          },
        },
        appliesTo: (s) => s.claude,
        Body: () => null,
      });
      window.history.replaceState(null, "", "#/s/7/info");

      render(<SessionTabs session={aSession(true)} />);

      const links = within(screen.getByRole("navigation", { name: "Session views" })).getAllByRole(
        "link",
      );
      expect(links.map((a) => a.textContent)).toEqual(["conversation", "changes", "info"]);
    } finally {
      spy.mockRestore();
    }
  });

  it.each([
    ["a subagent route of a Claude session", "#/s/7/agent/a1f00d", true, "conversation", true],
    ["a job route of a Claude session", "#/s/7/job/216", true, "info", true],
    ["a non-Claude session's own route", "#/s/7", false, "info", false],
    ["a subagent route of a non-Claude session", "#/s/7/agent/a1f00d", false, "info", false],
  ] as const)("lights the tab its route names: %s", (_name, hash, claude, lit, hasConversation) => {
    tabPanels();
    definePanel({
      id: "changes",
      title: () => "Changes",
      tab: { order: 2, label: "changes" },
      appliesTo: () => true,
      Body: () => null,
    });
    window.history.replaceState(null, "", hash);

    render(<SessionTabs session={aSession(claude)} />);

    const nav = within(screen.getByRole("navigation", { name: "Session views" }));
    const current = nav
      .getAllByRole("link")
      .filter((a) => a.getAttribute("aria-current") === "page");
    expect(current.map((a) => a.textContent)).toEqual([lit]);
    expect(nav.queryByRole("link", { name: "conversation" }) !== null).toBe(hasConversation);
  });
});

describe("StatusRow", () => {
  it.each([
    ["live", "LIVE"],
    ["connecting", "CONNECTING"],
    ["restarting", "RESTARTING"],
    ["offline", "OFFLINE"],
  ] as const)("says the link is %s", (link, pill) => {
    useFleet.setState({ snapshot: withGroups(["working"]), stale: link !== "live", link });
    render(<StatusRow />);
    expect(screen.getByText(pill)).toBeInTheDocument();
  });

  it("names the host, and when the daemon was last heard while offline", () => {
    const updated = Date.UTC(2026, 10, 18, 12, 5);
    useFleet.setState({ snapshot: withGroups(["working"]), stale: true, link: "offline", updated });
    render(<StatusRow />);

    // ICU puts a narrow no-break space before "PM" on Node 22, and Testing
    // Library normalises only the node's text: normalise the expected one too.
    const clock = new Date(updated)
      .toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })
      .replace(/\s+/g, " ");
    expect(screen.getByText(`devbox · ${clock}`)).toBeInTheDocument();
  });

  it("counts each group, and draws a zero count as no button", () => {
    live(withGroups(["needs_you", "needs_you", "your_turn", "paused"]));
    render(<StatusRow />);

    expect(screen.getByRole("button", { name: "2 need you: open the next" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "1 your turn: open the next" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /working/ })).toBeNull();
  });

  it("opens a group's longest-waiting session, then the next one after the session on screen", async () => {
    live(withGroups(["needs_you", "needs_you"]));
    window.history.replaceState(null, "", "#/");
    const { unmount } = render(<StatusRow />);
    await userEvent.click(screen.getByRole("button", { name: "2 need you: open the next" }));
    expect(window.location.hash).toBe("#/s/100");
    unmount();

    window.history.replaceState(null, "", "#/s/100");
    const second = render(<StatusRow />);
    await userEvent.click(screen.getByRole("button", { name: "2 need you: open the next" }));
    expect(window.location.hash).toBe("#/s/101");
    second.unmount();

    window.history.replaceState(null, "", "#/s/101");
    render(<StatusRow />);
    await userEvent.click(screen.getByRole("button", { name: "2 need you: open the next" }));
    expect(window.location.hash).toBe("#/s/100");
  });

  it("keeps the counts as buttons from the cached snapshot while the stream is down", () => {
    useFleet.setState({ snapshot: withGroups(["needs_you"]), stale: true, link: "offline" });
    render(<StatusRow />);
    expect(screen.getByRole("button", { name: "1 need you: open the next" })).toBeEnabled();
  });
});

describe("AppShell's chrome", () => {
  it("draws a session's tabs under the head, and the status row in the footer", async () => {
    definePanel({
      id: "turns",
      title: () => "Conversation",
      tab: { order: 1, label: "conversation" },
      Body: () => null,
    });
    definePanel({
      id: "info",
      title: () => "Info",
      tab: { order: 3, label: "info" },
      Head: () => <p>info head</p>,
      Body: () => null,
    });
    live(withGroups(["needs_you"]));
    shellAt("#/s/100/info");

    expect(await screen.findByText("outlet")).toBeInTheDocument();
    expect(header()).toHaveTextContent("info head");
    expect(
      within(header() as HTMLElement).getByRole("navigation", { name: "Session views" }),
    ).toBeInTheDocument();
    expect(footer()).toHaveTextContent("LIVE");
    expect(footer()).toHaveTextContent("loom@devbox");
  });

  it("draws the panel's Strip under the session's tabs, in the header", async () => {
    definePanel({
      id: "turns",
      title: () => "Conversation",
      tab: { order: 1, label: "conversation" },
      Head: () => <p>turns head</p>,
      Strip: () => <p>the strip</p>,
      Body: () => null,
    });
    definePanel({
      id: "info",
      title: () => "Info",
      tab: { order: 3, label: "info" },
      Body: () => null,
    });
    live(withGroups(["working"]));
    shellAt("#/s/100");

    await screen.findByText("outlet");
    const head = header() as HTMLElement;
    const nav = within(head).getByRole("navigation", { name: "Session views" });
    const strip = within(head).getByText("the strip");
    expect(nav.compareDocumentPosition(strip) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("keeps the header and the tabs when the Strip throws", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      definePanel({
        id: "turns",
        title: () => "Conversation",
        tab: { order: 1, label: "conversation" },
        Head: () => <p>turns head</p>,
        Strip: () => {
          throw new Error("strip boom");
        },
        Body: () => null,
      });
      definePanel({
        id: "info",
        title: () => "Info",
        tab: { order: 3, label: "info" },
        Body: () => null,
      });
      live(withGroups(["working"]));
      shellAt("#/s/100");

      expect(await screen.findByText("outlet")).toBeInTheDocument();
      expect(header()).toHaveTextContent("turns head");
      expect(
        within(header() as HTMLElement).getByRole("navigation", { name: "Session views" }),
      ).toBeInTheDocument();
    } finally {
      spy.mockRestore();
    }
  });

  it("draws a session's tabs on a subagent and a job route", async () => {
    definePanel({
      id: "turns",
      title: () => "Conversation",
      tab: { order: 1, label: "conversation" },
      Body: () => null,
    });
    definePanel({
      id: "info",
      title: () => "Info",
      tab: { order: 3, label: "info" },
      Body: () => null,
    });
    definePanel({
      id: "agent",
      title: () => "Subagent",
      Head: () => <p>head</p>,
      Body: () => null,
    });
    definePanel({ id: "job", title: () => "Job", Head: () => <p>head</p>, Body: () => null });
    live(withGroups(["working"]));

    const first = shellAt("#/s/100/agent/a1f00d");
    await screen.findByText("outlet");
    let nav = within(header() as HTMLElement).getByRole("navigation", { name: "Session views" });
    expect(within(nav).getByRole("link", { name: "conversation" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    first.unmount();

    shellAt("#/s/100/job/216");
    await screen.findByText("outlet");
    nav = within(header() as HTMLElement).getByRole("navigation", { name: "Session views" });
    expect(within(nav).getByRole("link", { name: "info" })).toHaveAttribute("aria-current", "page");
  });

  it("draws the footer's top border in the error colour only while offline", async () => {
    live(withGroups(["needs_you"]));
    shellAt("#/");
    await screen.findByText("outlet");
    expect(footer()).not.toHaveClass("border-t-error");

    act(() => {
      useFleet.setState({ stale: true, link: "offline" });
    });
    expect(footer()).toHaveClass("border-t-error");
  });

  it("draws no tabs for a session the fleet does not list", async () => {
    live(withGroups(["needs_you"]));
    shellAt("#/s/999999/info");

    expect(await screen.findByText("outlet")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Session views" })).toBeNull();
  });

  it("hides the footer while a text field has focus, and brings it back after", async () => {
    live(withGroups(["needs_you"]));
    shellAt("#/", <input aria-label="reply" />);

    const field = await screen.findByLabelText("reply");
    expect(footer()).not.toBeNull();

    await userEvent.click(field);
    expect(footer()).toBeNull();

    await userEvent.click(document.body);
    expect(footer()).not.toBeNull();
  });

  it("brings the footer back when a route change removes the focused field", async () => {
    function Screen(): ReactElement {
      return useRoute().panel === "fleet" ? <input aria-label="reply" /> : <p>elsewhere</p>;
    }
    live(withGroups(["needs_you"]));
    shellAt("#/", <Screen />);

    await userEvent.click(await screen.findByLabelText("reply"));
    expect(footer()).toBeNull();

    act(() => {
      window.location.hash = "#/s/100/info";
    });
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    expect(footer()).not.toBeNull();
  });

  it("keeps the footer for a button that has focus", async () => {
    live(withGroups(["needs_you"]));
    shellAt("#/", <button type="button">go</button>);

    await userEvent.click(await screen.findByRole("button", { name: "go" }));
    expect(footer()).not.toBeNull();
  });

  it("mounts the confirmation sheet and the toaster once", async () => {
    live(withGroups(["needs_you"]));
    shellAt("#/");
    await screen.findByText("outlet");

    let answer = Promise.resolve<string | null>("");
    act(() => {
      answer = askConfirm({
        title: "Kill session",
        body: "Ends it.",
        action: "Kill",
        destructive: true,
      });
    });
    expect(await screen.findByText("Kill session")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await answer).toBeNull();

    act(() => {
      showMessage("Stashed as 1a2b3c4.");
    });
    expect(await screen.findByText("Stashed as 1a2b3c4.")).toBeInTheDocument();
  });
});

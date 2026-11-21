import { act, fireEvent, render, screen, within } from "@testing-library/react";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { useEffect, type ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Snapshot } from "@/api/gen";
import { useDesktop } from "@/app/desktopState";
import { definePanel } from "@/app/panels";
import { useScrollPane, windowPane } from "@/app/scroll";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions, shownWorkspaces } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import DesktopShell from "./DesktopShell";

afterEach(() => {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

// desktopAt draws DesktopShell at the hash over a one-route router of its own (memory history),
// whose only screen is `outlet`.
function desktopAt(hash: string, outlet: ReactElement = <p>outlet</p>) {
  window.history.replaceState(null, "", hash);
  const root = createRootRoute({ component: DesktopShell });
  const index = createRoute({ getParentRoute: () => root, path: "/", component: () => outlet });
  const testRouter = createRouter({
    routeTree: root.addChildren([index]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return render(<RouterProvider router={testRouter} />);
}

// aSnapshot is the fixture with one Claude session, numbered 100, in its first shown workspace.
function aSnapshot(): Snapshot {
  const snapshot = fixtureSnapshot();
  const workspace = shownWorkspaces(snapshot)[0];
  const base = allSessions(snapshot)[0];
  if (workspace === undefined || base === undefined) throw new Error("the fixture holds none");
  return {
    ...snapshot,
    sessions: {
      [String(workspace.id)]: [{ ...base, id: 100, workspace: workspace.id, claude: true }],
    },
  };
}

function live(): void {
  useFleet.setState({ snapshot: aSnapshot(), stale: false, link: "live", updated: Date.now() });
}

function tabbed(): void {
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
    Head: () => <p>info head</p>,
    Body: () => null,
  });
}

const slot = (name: string): HTMLElement | null =>
  document.querySelector<HTMLElement>(`[data-slot="${name}"]`);

describe("DesktopShell's middle", () => {
  it("draws the head, the session's tabs and the strip above the scroller, and the screen inside it", async () => {
    tabbed();
    live();
    desktopAt("#/s/100");

    const outlet = await screen.findByText("outlet");
    const head = slot("middle-head");
    const scroller = slot("middle-scroller");
    if (head === null || scroller === null) throw new Error("the middle drew no head or scroller");
    expect(head).toHaveTextContent("turns head");
    const nav = within(head).getByRole("navigation", { name: "Session views" });
    const strip = within(head).getByText("the strip");
    expect(nav.compareDocumentPosition(strip) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(scroller).toContainElement(outlet);
    expect(scroller).not.toContainElement(head);
    expect(head.compareDocumentPosition(scroller) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("draws neither the phone's header nor its footer", async () => {
    tabbed();
    live();
    desktopAt("#/s/100");

    await screen.findByText("outlet");
    expect(slot("header")).toBeNull();
    expect(slot("footer")).toBeNull();
  });

  it("keeps the screen and the tabs when the head throws", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      tabbed();
      definePanel({
        id: "turns",
        title: () => "Conversation",
        tab: { order: 1, label: "conversation" },
        Head: () => {
          throw new Error("head boom");
        },
        Body: () => null,
      });
      live();
      desktopAt("#/s/100");

      expect(await screen.findByText("outlet")).toBeInTheDocument();
      expect(screen.getByRole("navigation", { name: "Session views" })).toBeInTheDocument();
      expect(screen.queryByText(/head boom/)).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  it("draws no tabs on a route with no session, and no head row at all when the panel has no head", async () => {
    tabbed();
    definePanel({ id: "settings", title: () => "Settings", Body: () => null });
    live();
    desktopAt("#/settings");

    await screen.findByText("outlet");
    expect(screen.queryByRole("navigation", { name: "Session views" })).toBeNull();
    expect(slot("middle-head")).toHaveClass("empty:hidden");
    expect(slot("middle-head")).toBeEmptyDOMElement();
  });

  it.each([
    ["#/s/100", true],
    ["#/s/100/info", false],
  ])("the screen at %s has the readable measure: %s", async (hash, readable) => {
    tabbed();
    live();
    desktopAt(hash);

    await screen.findByText("outlet");
    const main = document.querySelector('[data-slot="middle"] main');
    if (main === null) throw new Error("the middle drew no main");
    if (readable) expect(main).toHaveClass("max-w-[48rem]");
    else expect(main).not.toHaveClass("max-w-[48rem]");
  });

  it("draws the full mockup alone, with no chrome", async () => {
    tabbed();
    live();
    desktopAt("#/s/100/mockup/full");

    expect(await screen.findByText("outlet")).toBeInTheDocument();
    expect(slot("desktop")).toBeNull();
    expect(slot("middle")).toBeNull();
    expect(slot("middle-head")).toBeNull();
  });
});

// PaneProbe draws what the pane it is given says, and tells onScroll when that pane scrolls.
function PaneProbe({ onScroll }: { onScroll: () => void }): ReactElement {
  const pane = useScrollPane();
  const top = pane.top();
  useEffect(() => pane.subscribe(onScroll), [pane, onScroll]);
  return (
    <p>
      probe {pane === windowPane ? "window" : "pane"} {top}
    </p>
  );
}

describe("DesktopShell's scroll pane", () => {
  it("gives the screen the scroller's pane and never reads the window's scroll", async () => {
    const before = Object.getOwnPropertyDescriptor(window, "scrollY");
    const reads = vi.fn(() => 0);
    Object.defineProperty(window, "scrollY", { configurable: true, get: reads });
    try {
      tabbed();
      live();
      const onScroll = vi.fn();
      desktopAt("#/s/100", <PaneProbe onScroll={onScroll} />);

      expect(await screen.findByText("probe pane 0")).toBeInTheDocument();
      expect(reads).not.toHaveBeenCalled();

      fireEvent.scroll(window);
      expect(onScroll).not.toHaveBeenCalled();
      const scroller = slot("middle-scroller");
      if (scroller === null) throw new Error("the middle drew no scroller");
      fireEvent.scroll(scroller);
      expect(onScroll).toHaveBeenCalledOnce();
    } finally {
      if (before !== undefined) Object.defineProperty(window, "scrollY", before);
      else Reflect.deleteProperty(window, "scrollY");
    }
  });
});

describe("DesktopShell's top bar", () => {
  it("draws the top bar above the middle, inside the frame", async () => {
    tabbed();
    live();
    desktopAt("#/s/100");

    await screen.findByText("outlet");
    const bar = slot("top-bar");
    if (bar === null) throw new Error("the frame drew no top bar");
    expect(slot("desktop")).toContainElement(bar);
    const middle = slot("middle");
    if (middle === null) throw new Error("the frame drew no middle");
    expect(bar.compareDocumentPosition(middle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(bar).getByRole("link", { name: "Settings" })).toBeInTheDocument();
  });

  it("draws the top bar on a route with no session", async () => {
    definePanel({ id: "settings", title: () => "Settings", Body: () => null });
    live();
    desktopAt("#/settings");

    await screen.findByText("outlet");
    expect(slot("top-bar")).not.toBeNull();
  });

  it("draws no head on the fleet route, and its Body in full", async () => {
    definePanel({
      id: "fleet",
      title: () => "Fleet",
      Head: () => <p>fleet head</p>,
      Body: () => null,
    });
    live();
    desktopAt("#/");

    await screen.findByText("outlet");
    expect(screen.queryByText("fleet head")).toBeNull();
    expect(slot("middle-head")).toBeEmptyDOMElement();
    expect(screen.getByRole("link", { name: "+ New" })).toBeInTheDocument();
  });

  it("still draws the head on a session route", async () => {
    tabbed();
    live();
    desktopAt("#/s/100");

    await screen.findByText("outlet");
    expect(within(slot("middle-head")!).getByText("turns head")).toBeInTheDocument();
  });

  it("draws no top bar on the full mockup", async () => {
    tabbed();
    live();
    desktopAt("#/s/100/mockup/full");

    await screen.findByText("outlet");
    expect(slot("top-bar")).toBeNull();
    expect(slot("desktop")).toBeNull();
  });
});

describe("DesktopShell's sidebar", () => {
  const reset = (): void => useDesktop.setState({ sidebar: true, inspector: true, slots: [] });
  beforeEach(reset);
  afterEach(reset);

  it("draws the sidebar before the middle, inside the frame, with the route's session highlighted", async () => {
    tabbed();
    live();
    desktopAt("#/s/100");

    await screen.findByText("outlet");
    const bar = slot("sidebar");
    if (bar === null) throw new Error("the frame drew no sidebar");
    expect(slot("desktop")).toContainElement(bar);
    const middle = slot("middle");
    if (middle === null) throw new Error("the frame drew no middle");
    expect(bar.compareDocumentPosition(middle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(bar).getByRole("link", { name: /fix-pane-cursor/ })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("draws the sidebar on a route with no session, with nothing highlighted", async () => {
    definePanel({ id: "settings", title: () => "Settings", Body: () => null });
    live();
    desktopAt("#/settings");

    await screen.findByText("outlet");
    const bar = slot("sidebar");
    if (bar === null) throw new Error("the frame drew no sidebar");
    expect(within(bar).getByRole("link", { name: /fix-pane-cursor/ })).toBeInTheDocument();
    expect(bar.querySelector("[aria-current]")).toBeNull();
  });

  it("draws no sidebar on the fleet route, and the middle in full", async () => {
    definePanel({ id: "fleet", title: () => "Fleet", Body: () => null });
    live();
    desktopAt("#/");

    await screen.findByText("outlet");
    expect(slot("sidebar")).toBeNull();
    expect(slot("middle")).not.toBeNull();
  });

  it("draws no sidebar while the preference hides it, and keeps the middle", async () => {
    useDesktop.setState({ sidebar: false });
    tabbed();
    live();
    desktopAt("#/s/100");

    await screen.findByText("outlet");
    expect(slot("sidebar")).toBeNull();
    expect(slot("middle")).not.toBeNull();
    expect(slot("top-bar")).not.toBeNull();
    act(() => useDesktop.getState().setSidebar(true));
    expect(slot("sidebar")).not.toBeNull();
  });

  it("draws no sidebar on the full mockup", async () => {
    live();
    desktopAt("#/s/100/mockup/full");

    await screen.findByText("outlet");
    expect(slot("sidebar")).toBeNull();
  });
});

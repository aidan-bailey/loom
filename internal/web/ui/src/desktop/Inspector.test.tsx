import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useEffect, type ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Session } from "@/api/gen";
import { DESKTOP_KEY, readDesktopPrefs, useDesktop } from "@/app/desktopState";
import { definePanel, type Panel, type PanelProps } from "@/app/panels";
import { useScrollPane, windowPane, type ScrollPane } from "@/app/scroll";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";

import { Inspector } from "./Inspector";

beforeEach(() => {
  localStorage.clear();
  useDesktop.setState({ sidebar: true, inspector: true, slots: [] });
});

afterEach(() => {
  localStorage.clear();
});

function claude(id = 7, isClaude = true): Session {
  const base = allSessions(fixtureSnapshot())[0];
  if (base === undefined) throw new Error("the fixture holds none");
  return { ...base, id, claude: isClaude };
}

// tabs defines the session's tab panels, in the order turns, changes, mockup, info; `changes`
// replaces fields of the changes definition.
function tabs(changes: Partial<Panel> = {}): void {
  const body = (id: string) => (): ReactElement => <p>{id} body</p>;
  definePanel({
    id: "turns",
    title: () => "Turns",
    tab: { order: 1, label: "conversation" },
    appliesTo: (s) => s.claude,
    Body: body("conversation"),
  });
  definePanel({
    id: "changes",
    title: () => "Changes",
    tab: { order: 2, label: "changes" },
    appliesTo: (s) => s.claude,
    Body: (props: PanelProps) => <p>changes body {JSON.stringify(props)}</p>,
    ...changes,
  });
  definePanel({
    id: "mockup",
    title: () => "Mockup",
    tab: { order: 3, label: "mockup" },
    appliesTo: () => false,
    Body: body("mockup"),
  });
  definePanel({
    id: "info",
    title: () => "Info",
    tab: { order: 4, label: "info" },
    Body: body("info"),
  });
}

function strip(): HTMLElement {
  return screen.getByRole("navigation", { name: "Inspector views" });
}

function lit(): string | null {
  const on = within(strip())
    .getAllByRole("button")
    .find((b) => b.getAttribute("aria-current") === "true");
  return on === undefined ? null : (on.textContent ?? null);
}

function names(): (string | null)[] {
  return within(strip())
    .getAllByRole("button")
    .map((b) => b.textContent);
}

function inspectorSlot(): HTMLElement {
  const el = document.querySelector<HTMLElement>('[data-slot="inspector"]');
  if (el === null) throw new Error("no inspector");
  return el;
}

describe("Inspector", () => {
  it("lists the session's tabs but the middle's, and draws the first applicable one", () => {
    tabs();
    render(<Inspector session={claude()} middle="turns" />);
    expect(names()).toEqual(["changes", "info"]);
    expect(lit()).toBe("changes");
    expect(screen.getByText(/changes body/)).toBeInTheDocument();
    expect(screen.queryByText("info body")).toBeNull();
  });

  it("draws the chosen panel's Body with the session's props", () => {
    tabs();
    render(<Inspector session={claude(7)} middle="turns" />);
    expect(screen.getByText(/changes body/).textContent).toContain(
      '{"sessionId":7,"params":{"id":"7"},"search":{}}',
    );
  });

  it("a click on a tab draws it, lights it and writes loom.desktop", async () => {
    tabs();
    render(<Inspector session={claude()} middle="turns" />);
    await userEvent.click(within(strip()).getByRole("button", { name: "info" }));
    expect(screen.getByText("info body")).toBeInTheDocument();
    expect(screen.queryByText(/changes body/)).toBeNull();
    expect(lit()).toBe("info");
    expect(JSON.parse(localStorage.getItem(DESKTOP_KEY) ?? "null")).toEqual({
      sidebar: true,
      inspector: true,
      slots: [{ tab: "info" }],
    });
  });

  it("the choice survives a reload", async () => {
    tabs();
    const first = render(<Inspector session={claude()} middle="turns" />);
    await userEvent.click(within(strip()).getByRole("button", { name: "info" }));
    first.unmount();
    useDesktop.setState({ sidebar: true, inspector: true, slots: [] });
    useDesktop.setState(readDesktopPrefs(localStorage.getItem(DESKTOP_KEY)));
    render(<Inspector session={claude()} middle="turns" />);
    expect(lit()).toBe("info");
    expect(screen.getByText("info body")).toBeInTheDocument();
  });

  it("the choice falls to the next applicable tab when it is the middle's", () => {
    useDesktop.setState({ slots: [{ tab: "info" }] });
    tabs();
    render(<Inspector session={claude()} middle="info" />);
    expect(names()).toEqual(["conversation", "changes"]);
    expect(lit()).toBe("conversation");
    expect(screen.getByText("conversation body")).toBeInTheDocument();
  });

  it("the choice falls past a tab that does not apply", () => {
    useDesktop.setState({ slots: [{ tab: "mockup" }] });
    tabs();
    render(<Inspector session={claude()} middle="turns" />);
    expect(lit()).toBe("info");
  });

  it("a stored tab no panel has falls to the first applicable tab", () => {
    useDesktop.setState({ slots: [{ tab: "bogus" }] });
    tabs();
    render(<Inspector session={claude()} middle="turns" />);
    expect(lit()).toBe("changes");
  });

  it("follows the middle when it changes", () => {
    useDesktop.setState({ slots: [{ tab: "changes" }] });
    tabs();
    const view = render(<Inspector session={claude()} middle="turns" />);
    expect(lit()).toBe("changes");
    view.rerender(<Inspector session={claude()} middle="changes" />);
    expect(names()).toEqual(["conversation", "info"]);
    expect(lit()).toBe("info");
    expect(screen.getByText("info body")).toBeInTheDocument();
  });

  it("draws nothing when the middle's tab is the session's only one", () => {
    tabs();
    const { container } = render(<Inspector session={claude(7, false)} middle="info" />);
    expect(container).toBeEmptyDOMElement();
    expect(document.querySelector("aside")).toBeNull();
    expect(screen.queryByRole("navigation")).toBeNull();
  });

  it("draws the strip above the scroller and outside it, with no 44px minimum", () => {
    tabs();
    render(<Inspector session={claude()} middle="turns" />);
    const scroller = document.querySelector('[data-slot="inspector-scroller"]');
    expect(scroller).not.toBeNull();
    if (scroller === null) return;
    expect(
      strip().compareDocumentPosition(scroller) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(scroller.contains(strip())).toBe(false);
    for (const el of inspectorSlot().querySelectorAll("*")) {
      const cls = el.getAttribute("class") ?? "";
      expect(cls).not.toContain("min-h-[44px]");
      expect(cls).not.toContain("min-w-[44px]");
    }
  });

  it("a panel that throws is replaced in the inspector alone, and the strip stays", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      tabs({
        Body: () => {
          throw new Error("body boom");
        },
      });
      render(
        <>
          <p>middle</p>
          <Inspector session={claude()} middle="turns" />
        </>,
      );
      expect(screen.getByText("middle")).toBeInTheDocument();
      expect(within(inspectorSlot()).getByText("body boom")).toBeInTheDocument();
      expect(names()).toEqual(["changes", "info"]);
      await userEvent.click(within(strip()).getByRole("button", { name: "info" }));
      expect(screen.getByText("info body")).toBeInTheDocument();
      expect(screen.queryByText("body boom")).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  it("a panel whose title throws is caught by the inspector's own boundary, and another tab recovers", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      tabs({
        title: () => {
          throw new Error("title boom");
        },
      });
      render(
        <>
          <p>middle</p>
          <Inspector session={claude()} middle="turns" />
        </>,
      );
      expect(screen.getByText("middle")).toBeInTheDocument();
      const alert = screen.getByRole("alert");
      expect(alert).toHaveTextContent("The inspector failed to draw.");
      expect(alert).toHaveTextContent("title boom");
      expect(names()).toEqual(["changes", "info"]);
      await userEvent.click(within(strip()).getByRole("button", { name: "info" }));
      expect(screen.getByText("info body")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  it("starts again for another session", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      tabs({
        title: (p: PanelProps) => {
          if (p.sessionId === 7) throw new Error("title boom");
          return "Changes";
        },
      });
      const view = render(<Inspector session={claude(7)} middle="turns" />);
      expect(screen.getByRole("alert")).toBeInTheDocument();
      view.rerender(<Inspector session={claude(8)} middle="turns" />);
      expect(screen.queryByRole("alert")).toBeNull();
      expect(screen.getByText(/changes body/)).toBeInTheDocument();
    } finally {
      spy.mockRestore();
    }
  });

  it("gives the panel the inspector's pane and never reads the window's scroll", () => {
    const own = Object.getOwnPropertyDescriptor(window, "scrollY");
    let reads = 0;
    Object.defineProperty(window, "scrollY", {
      configurable: true,
      get() {
        reads++;
        return 0;
      },
    });
    try {
      const onScroll = vi.fn();
      const seen: ScrollPane[] = [];
      const Probe = (): ReactElement => {
        const pane = useScrollPane();
        seen.push(pane);
        pane.top();
        useEffect(() => pane.subscribe(onScroll), [pane]);
        return <p>{pane === windowPane ? "probe window" : "probe pane"}</p>;
      };
      tabs({ Body: Probe });
      render(<Inspector session={claude()} middle="turns" />);
      expect(screen.getByText("probe pane")).toBeInTheDocument();
      expect(seen.length).toBeGreaterThan(0);
      expect(seen.every((pane) => pane !== windowPane)).toBe(true);
      expect(reads).toBe(0);
      fireEvent.scroll(window);
      expect(onScroll).not.toHaveBeenCalled();
      const scroller = document.querySelector('[data-slot="inspector-scroller"]');
      if (scroller === null) throw new Error("no scroller");
      fireEvent.scroll(scroller);
      expect(onScroll).toHaveBeenCalledTimes(1);
    } finally {
      Reflect.deleteProperty(window, "scrollY");
      if (own !== undefined) Object.defineProperty(window, "scrollY", own);
    }
  });
});

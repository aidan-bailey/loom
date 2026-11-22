import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { LinkState } from "@/api/stream";
import { hrefFor } from "@/app/routes";
import { readFilter, useFilter } from "@/fleet/filter";
import { allSessions, fleetGroups } from "@/fleet/groups";
import { fixtureSnapshot } from "@/fixtures";
import { FleetLinks } from "@/panels/fleet/FleetHead";
import { useFleet } from "@/store/fleet";

import { usePalette } from "./paletteState";
import { PaletteBox, TopBar } from "./TopBar";

function load(link: LinkState = "live"): void {
  useFleet.setState({
    snapshot: fixtureSnapshot(),
    stale: link !== "live",
    link,
    updated: Date.now(),
  });
}

afterEach(() => {
  usePalette.setState({ open: false });
  window.localStorage.clear();
  useFilter.setState({ stored: readFilter() });
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

describe("FleetLinks", () => {
  it("keeps its 44px touch minimum, and drops it when dense", () => {
    useFleet.setState({
      snapshot: fixtureSnapshot(),
      stale: false,
      link: "live",
      updated: Date.now(),
    });
    const names = ["Flows", "Settings", "+ New"];

    const plain = render(<FleetLinks />);
    for (const name of names) {
      expect(screen.getByRole("link", { name }).className).toContain("min-h-[44px]");
    }
    plain.unmount();

    render(<FleetLinks dense />);
    for (const name of names) {
      const cls = screen.getByRole("link", { name }).className;
      expect(cls).not.toContain("min-h-[44px]");
      expect(cls).not.toContain("min-w-[44px]");
    }
  });
});

const bar = (): HTMLElement => document.querySelector<HTMLElement>('[data-slot="top-bar"]')!;

describe("TopBar", () => {
  it("draws the link, the host, the filter, the counts and the fleet's links", () => {
    load();
    render(<TopBar />);

    expect(screen.getByText("LIVE")).toBeInTheDocument();
    expect(within(bar()).getByText("fresco")).toBeInTheDocument();
    const filter = within(screen.getByRole("group", { name: "Workspaces" }));
    for (const name of ["All", "loom", "flow"]) {
      expect(filter.getByRole("button", { name })).toBeInTheDocument();
    }
    for (const name of [
      "2 need you: open the next",
      "1 your turn: open the next",
      "3 working: open the next",
    ]) {
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    }
    expect(screen.getByRole("link", { name: "Flows" })).toHaveAttribute("href", "#/flows");
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute("href", "#/settings");
    expect(screen.getByRole("link", { name: "+ New" })).toHaveAttribute("href", "#/new");
  });

  it("draws every part dense", () => {
    load();
    render(<TopBar />);

    expect(bar().querySelectorAll('[class*="min-h-[44px]"]')).toHaveLength(0);
  });

  it("keeps the filter's group from taking the host label's room", () => {
    load();
    render(<TopBar />);

    const group = screen.getByRole("group", { name: "Workspaces" });
    expect(group.className).toContain("w-auto");
    expect(group.className).toContain("flex-none");
    expect(group.className).not.toMatch(/(^| )w-full( |$)/);
    for (const item of within(group).getAllByRole("button")) {
      expect(item.className).toContain("flex-none");
      expect(item.className).not.toMatch(/(^| )flex-1( |$)/);
    }
  });

  it("a count opens the group's next session, as the footer's do", async () => {
    load();
    const ids = fleetGroups(useFleet.getState().snapshot!).needs_you.map((s) => s.id);
    const name = "2 need you: open the next";
    window.history.replaceState(null, "", "#/");
    const first = render(<TopBar />);
    await userEvent.click(screen.getByRole("button", { name }));
    expect(window.location.hash).toBe(hrefFor("turns", { id: String(ids[0]) }));
    first.unmount();

    window.history.replaceState(null, "", hrefFor("turns", { id: String(ids[0]) }));
    const second = render(<TopBar />);
    await userEvent.click(screen.getByRole("button", { name }));
    expect(window.location.hash).toBe(hrefFor("turns", { id: String(ids[1]) }));
    second.unmount();

    window.history.replaceState(null, "", hrefFor("turns", { id: String(ids[1]) }));
    render(<TopBar />);
    await userEvent.click(screen.getByRole("button", { name }));
    expect(window.location.hash).toBe(hrefFor("turns", { id: String(ids[0]) }));
  });

  it("a pick in the bar's filter is the shared choice", async () => {
    load();
    render(<TopBar />);
    const filter = within(screen.getByRole("group", { name: "Workspaces" }));

    await userEvent.click(filter.getByRole("button", { name: "flow" }));
    expect(useFilter.getState().stored).toBe("2");
    expect(window.localStorage.getItem("loom.filter")).toBe('"2"');

    await userEvent.click(filter.getByRole("button", { name: "All" }));
    expect(useFilter.getState().stored).toBe("");
    expect(window.localStorage.getItem("loom.filter")).toBe('""');
  });

  it("says when the view is stale: the pill, the host's time and a dimmed New", async () => {
    const updated = Date.UTC(2026, 10, 18, 12, 5);
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: true, link: "offline", updated });
    window.history.replaceState(null, "", "#/");
    render(<TopBar />);

    const clock = new Date(updated)
      .toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })
      .replace(/\s+/g, " ");
    expect(screen.getByText("OFFLINE")).toBeInTheDocument();
    expect(screen.getByText(`fresco · ${clock}`)).toBeInTheDocument();
    const add = screen.getByRole("link", { name: "+ New" });
    expect(add).toHaveAttribute("aria-disabled", "true");
    await userEvent.click(add);
    expect(window.location.hash).toBe("#/");
  });

  it("draws the fleet's links without a Flows link when no shown session has flows", () => {
    const snapshot = fixtureSnapshot();
    for (const s of allSessions(snapshot)) s.flows = null;
    act(() => {
      useFleet.setState({ snapshot, stale: false, link: "live", updated: Date.now() });
    });
    render(<TopBar />);

    expect(screen.queryByRole("link", { name: "Flows" })).toBeNull();
    expect(screen.getByRole("link", { name: "Settings" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "+ New" })).toBeInTheDocument();
  });
});

describe("PaletteBox", () => {
  const withPlatform = (platform: string): void => {
    vi.spyOn(window.navigator, "platform", "get").mockReturnValue(platform);
  };
  const box = (): HTMLElement => document.querySelector<HTMLElement>('[data-slot="palette-box"]')!;

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("draws a button with the prompt and the Ctrl chord on a non-Mac", () => {
    withPlatform("Linux x86_64");
    render(<PaletteBox />);

    expect(box().tagName).toBe("BUTTON");
    expect(box()).toHaveTextContent("Search sessions, commands, places");
    expect(box()).toHaveTextContent("Ctrl K");
    expect(box()).not.toHaveTextContent("⌘K");
  });

  it.each(["MacIntel", "iPhone", "iPad"])("shows ⌘K on %s", (platform) => {
    withPlatform(platform);
    render(<PaletteBox />);

    expect(box()).toHaveTextContent("⌘K");
    expect(box()).not.toHaveTextContent("Ctrl K");
  });

  it("a click opens the palette", async () => {
    render(<PaletteBox />);
    await userEvent.click(box());
    expect(usePalette.getState().open).toBe(true);
  });

  it("a click on an open palette's box leaves it open", async () => {
    usePalette.setState({ open: true });
    render(<PaletteBox />);
    await userEvent.click(box());
    expect(usePalette.getState().open).toBe(true);
  });

  it("is dense and about 16rem wide", () => {
    render(<PaletteBox />);
    expect(box().className).toContain("w-64");
    expect(box().className).toContain("flex-none");
    expect(box().className).not.toContain("min-h-[44px]");
  });

  it("the top bar draws it after loom@host and before the workspace filter", () => {
    load();
    render(<TopBar />);

    const host = within(bar()).getByText("fresco");
    const filter = screen.getByRole("group", { name: "Workspaces" });
    expect(host.compareDocumentPosition(box()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(box().compareDocumentPosition(filter) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

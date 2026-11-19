import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import type { ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { definePanel, type PanelProps } from "./panels";
import { router } from "./routes";
import { AppShell, PanelHost, Placeholder } from "./shell";
import { switchApp } from "./switchApp";

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
});

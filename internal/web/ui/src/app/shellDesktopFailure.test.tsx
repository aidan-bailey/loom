import { render, screen } from "@testing-library/react";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";
import { setViewportWidth } from "@/test-layout";

import { AppShell } from "./shell";

// The desktop frame's module fails to load. A file of its own, since vi.mock holds for a whole file
// and shell.test.tsx draws the real frame.
vi.mock("@/desktop/DesktopShell", () => {
  throw new Error("chunk gone");
});

afterEach(() => {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

describe("AppShell's desktop frame", () => {
  it("shows what failed and a reload when the desktop's module does not load", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      useFleet.setState({
        snapshot: fixtureSnapshot(),
        stale: false,
        link: "live",
        updated: Date.now(),
      });
      setViewportWidth(1280);
      window.history.replaceState(null, "", "#/");
      const root = createRootRoute({ component: AppShell });
      const index = createRoute({
        getParentRoute: () => root,
        path: "/",
        component: () => <p>outlet</p>,
      });
      render(
        <RouterProvider
          router={createRouter({
            routeTree: root.addChildren([index]),
            history: createMemoryHistory({ initialEntries: ["/"] }),
          })}
        />,
      );

      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent("The desktop layout failed to draw.");
      expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
    } finally {
      spy.mockRestore();
    }
  });
});

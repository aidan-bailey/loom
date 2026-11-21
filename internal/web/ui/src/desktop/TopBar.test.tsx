import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { fixtureSnapshot } from "@/fixtures";
import { FleetLinks } from "@/panels/fleet/FleetHead";
import { useFleet } from "@/store/fleet";

afterEach(() => {
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

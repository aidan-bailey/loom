import { fireEvent, render, screen, within } from "@testing-library/react";
import { useEffect, type ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { definePanel, type PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { useScrollPane, windowPane, type ScrollPane } from "@/app/scroll";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { FlowPreview } from "./FlowPreview";

const PLAN = "docs/p.md";

// flowBody stands in for the flow panel's Body: it prints the props the pane hands it.
function flowBody(Body: (props: PanelProps) => ReactElement | null = printProps): void {
  definePanel({ id: "flow", title: () => "Flow", Body });
}

function printProps(props: PanelProps): ReactElement {
  return <p>flow body {JSON.stringify(props)}</p>;
}

beforeEach(() => {
  useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live", updated: 0 });
});

afterEach(() => {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
});

const pane = (): HTMLElement => screen.getByRole("complementary", { name: "Flow preview" });

describe("FlowPreview", () => {
  it("draws the flow panel's Body with the card's session and plan", () => {
    flowBody();
    render(<FlowPreview sessionId={6} plan={PLAN} />);
    const props = { sessionId: 6, params: { id: "6" }, search: { plan: PLAN } };
    expect(within(pane()).getByText(`flow body ${JSON.stringify(props)}`)).toBeInTheDocument();
  });

  it("heads the pane with the session's title, its sub-line, and links that open and close", () => {
    flowBody();
    render(<FlowPreview sessionId={6} plan={PLAN} />);
    const head = pane().querySelector('[data-slot="flow-preview-head"]') as HTMLElement;
    expect(within(head).getByRole("heading", { name: "flow-run" })).toBeInTheDocument();
    // The sub-line names the repo, the session's workspace, and the branch.
    expect(within(head).getByText("flow", { selector: "span" })).toBeInTheDocument();
    const flow = hrefFor("flow", { id: "6" }, { plan: PLAN });
    expect(within(head).getByRole("link", { name: "Open the flow" })).toHaveAttribute("href", flow);
    expect(within(head).getByRole("link", { name: "flow" })).toHaveAttribute("href", flow);
    expect(within(head).getByRole("link", { name: "conversation" })).toHaveAttribute(
      "href",
      hrefFor("turns", { id: "6" }),
    );
    expect(within(head).getByRole("link", { name: "Close the preview" })).toHaveAttribute(
      "href",
      hrefFor("flowBoard"),
    );
  });

  it("a session that is gone keeps the close link and names the session by its id", () => {
    flowBody();
    render(<FlowPreview sessionId={999} plan={PLAN} />);
    expect(within(pane()).getByRole("heading", { name: "Session 999" })).toBeInTheDocument();
    expect(within(pane()).getByRole("link", { name: "Close the preview" })).toBeInTheDocument();
    expect(within(pane()).queryByRole("link", { name: "conversation" })).toBeNull();
  });

  it("is dense chrome: no 44px minimum outside the panel it draws", () => {
    flowBody(() => null);
    render(<FlowPreview sessionId={6} plan={PLAN} />);
    for (const el of [pane(), ...pane().querySelectorAll("*")]) {
      expect(el.getAttribute("class") ?? "").not.toContain("min-h-[44px]");
    }
  });

  it("gives the panel the preview's pane and never reads the window's scroll", () => {
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
        const scroll = useScrollPane();
        seen.push(scroll);
        scroll.top();
        useEffect(() => scroll.subscribe(onScroll), [scroll]);
        return <p>{scroll === windowPane ? "probe window" : "probe pane"}</p>;
      };
      flowBody(Probe);
      render(<FlowPreview sessionId={6} plan={PLAN} />);
      expect(screen.getByText("probe pane")).toBeInTheDocument();
      expect(seen.every((s) => s !== windowPane)).toBe(true);
      expect(reads).toBe(0);
      fireEvent.scroll(window);
      expect(onScroll).not.toHaveBeenCalled();
      const scroller = document.querySelector('[data-slot="flow-preview-scroller"]');
      if (scroller === null) throw new Error("no scroller");
      fireEvent.scroll(scroller);
      expect(onScroll).toHaveBeenCalledTimes(1);
    } finally {
      Reflect.deleteProperty(window, "scrollY");
      if (own !== undefined) Object.defineProperty(window, "scrollY", own);
    }
  });
});

import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/commands";
import type { Mockup, Session } from "@/api/gen";
import { panelFor, type PanelProps } from "@/app/panels";
import { PanelHost } from "@/app/shell";
import { makeSession, resetFleet, seed } from "@/commands/testkit";
import { readSeen } from "@/fleet/seen";
import { SessionHead } from "@/session/SessionHead";

import { MockupMark } from "./index";
import MockupBody, { MockupFullBody } from "./MockupBody";

const NOW = Date.parse("2026-11-19T12:00:00Z");
const CHANGED = new Date(NOW - 120_000).toISOString();
const props: PanelProps = { sessionId: 7, params: { id: "7" }, search: {} };

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  resetFleet();
  window.localStorage.clear();
  window.history.replaceState(null, "", window.location.pathname);
});

function mockup(over: Partial<Mockup> = {}): Mockup {
  return { screen: "layout.html", changed: CHANGED, fresh: true, ...over };
}

function show(over: Partial<Session> = {}): Session {
  const session = makeSession({
    id: 7,
    status: "Ready",
    group: "your_turn",
    mockup: mockup(),
    ...over,
  });
  seed([session]);
  window.history.replaceState(null, "", "#/s/7/mockup");
  return session;
}

const frame = (): HTMLElement => screen.getByTitle("Mockup: layout.html");

describe("MockupBody", () => {
  it("frames the companion's page sandboxed, with the change time and no reload yet", () => {
    show();
    render(<MockupBody {...props} />);

    expect(frame()).toHaveAttribute("sandbox", "allow-scripts");
    expect(frame()).toHaveAttribute("referrerpolicy", "no-referrer");
    expect(frame()).toHaveAttribute(
      "src",
      `/api/v1/sessions/7/mockup?v=${encodeURIComponent(CHANGED)}&r=0`,
    );
  });

  it("words the strip with the screen and its age, and links the full-screen view", () => {
    show();
    render(<MockupBody {...props} />);

    expect(screen.getByText("layout.html · 2m")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Full screen" })).toHaveAttribute(
      "href",
      "#/s/7/mockup/full",
    );
  });

  it("reads the page again when the reload is tapped", async () => {
    show();
    render(<MockupBody {...props} />);
    const before = frame();

    await userEvent.click(screen.getByRole("button", { name: "Read the mockup again" }));
    expect(frame().getAttribute("src")).toMatch(/&r=1$/);
    expect(frame()).not.toBe(before);

    await userEvent.click(screen.getByRole("button", { name: "Read the mockup again" }));
    expect(frame().getAttribute("src")).toMatch(/&r=2$/);
  });

  it("loads a new screen when the change time moves", () => {
    show();
    const view = render(<MockupBody {...props} />);
    const later = new Date(NOW - 60_000).toISOString();

    act(() => {
      show({ mockup: mockup({ changed: later }) });
    });
    view.rerender(<MockupBody {...props} />);

    expect(frame()).toHaveAttribute(
      "src",
      `/api/v1/sessions/7/mockup?v=${encodeURIComponent(later)}&r=0`,
    );
  });

  it("says so when no mockup is showing", () => {
    show({ mockup: null });
    const { container } = render(<MockupBody {...props} />);

    expect(
      screen.getByText("No mockup is showing; the companion may have stopped."),
    ).toBeInTheDocument();
    expect(screen.getByText("no mockup")).toBeInTheDocument();
    expect(container.querySelector("iframe")).toBeNull();
    expect(screen.queryByRole("button", { name: "Read the mockup again" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Full screen" })).toBeNull();
  });

  it("draws the reply box, or says to resume, or nothing", () => {
    show({ status: "Running", group: "working" });
    const running = render(<MockupBody {...props} />);
    expect(screen.getByRole("textbox", { name: "Reply" })).toBeInTheDocument();
    running.unmount();

    show({ status: "Paused", group: "paused" });
    const paused = render(<MockupBody {...props} />);
    expect(screen.getByText("Resume the session to reply.")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).toBeNull();
    paused.unmount();

    show({ status: "Loading", group: "working" });
    render(<MockupBody {...props} />);
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByText("Resume the session to reply.")).toBeNull();
  });
});

describe.each([
  ["MockupBody", MockupBody],
  ["MockupFullBody", MockupFullBody],
])("%s", (_, Body) => {
  it("marks the session seen when it opens and again when it closes", () => {
    show();
    const view = render(<Body {...props} />);
    expect(readSeen()["7"]).toBe(NOW);

    vi.setSystemTime(NOW + 60_000);
    view.unmount();
    expect(readSeen()["7"]).toBe(NOW + 60_000);
  });

  it("says the session is gone", () => {
    const { container } = render(<Body {...props} />);
    expect(screen.getByText("This session is gone.")).toBeInTheDocument();
    expect(container.querySelector("iframe")).toBeNull();
  });
});

describe("MockupFullBody", () => {
  it("draws only the frame and the way back", () => {
    show();
    render(<MockupFullBody {...props} />);

    expect(frame()).toHaveAttribute("sandbox", "allow-scripts");
    expect(frame()).toHaveAttribute(
      "src",
      `/api/v1/sessions/7/mockup?v=${encodeURIComponent(CHANGED)}&r=0`,
    );
    expect(screen.getByRole("link", { name: "✕ done" })).toHaveAttribute("href", "#/s/7/mockup");
    expect(screen.queryByRole("button", { name: "Read the mockup again" })).toBeNull();
    expect(screen.queryByText(/layout\.html · /)).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("draws the empty notice beside the done link when no mockup is showing", () => {
    show({ mockup: null });
    render(<MockupFullBody {...props} />);

    expect(
      screen.getByText("No mockup is showing; the companion may have stopped."),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "✕ done" })).toBeInTheDocument();
  });
});

describe("MockupMark", () => {
  it("is a dot while the mockup is fresh", () => {
    const fresh = render(<MockupMark session={show()} />);
    expect(screen.getByRole("img", { name: "fresh" })).toBeInTheDocument();
    fresh.unmount();

    const stale = render(<MockupMark session={show({ mockup: mockup({ fresh: false }) })} />);
    expect(stale.container).toBeEmptyDOMElement();
    stale.unmount();

    const none = render(<MockupMark session={show({ mockup: null })} />);
    expect(none.container).toBeEmptyDOMElement();
  });
});

describe("the mockup panels", () => {
  it("are registered: the fourth tab for a session with a mockup, and the full view with no head", () => {
    const panel = panelFor("mockup");
    expect(panel?.tab).toEqual({ order: 4, label: "mockup", Mark: MockupMark });
    expect(panel?.Head).toBe(SessionHead);
    expect(panel?.appliesTo?.(show())).toBe(true);
    expect(panel?.appliesTo?.(show({ mockup: null }))).toBe(false);

    const full = panelFor("mockupFull");
    expect(full).toBeDefined();
    expect(full?.Head).toBeUndefined();
    expect(full?.tab).toBeUndefined();
  });

  it("draw both screens through PanelHost once their chunk loads", async () => {
    show();
    const view = render(<PanelHost panel="mockup" props={props} />);
    expect(await screen.findByTitle("Mockup: layout.html")).toBeInTheDocument();
    expect(screen.getByText("layout.html · 2m")).toBeInTheDocument();
    view.unmount();

    render(<PanelHost panel="mockupFull" props={props} />);
    expect(await screen.findByRole("link", { name: "✕ done" })).toBeInTheDocument();
    expect(screen.getByTitle("Mockup: layout.html")).toBeInTheDocument();
    expect(screen.queryByText("layout.html · 2m")).toBeNull();
  });
});

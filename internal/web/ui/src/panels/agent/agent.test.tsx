import { QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentPage, Outcome, Turns } from "@/api/gen";
import { queryClient } from "@/api/queries";
import { useTurnPages } from "@/api/reads";
import { api } from "@/api/request";
import { definePanel, panelFor } from "@/app/panels";
import { fixtureSnapshot, fixtureTurns } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import "@/panels";
import { useFleet } from "@/store/fleet";
import { usePending } from "@/store/pending";

import { Agent, AgentHead, AgentStrip } from "./Agent";

const NOW = Date.parse("2026-11-18T12:00:00Z");
const AGENT = "a1f00dcafe0001";

const ok = <T,>(value: T): Outcome<T> => ({ kind: "ok", status: 200, value, notices: [] });
const props = (id: number) => ({
  sessionId: id,
  params: { id: String(id), agent: AGENT },
  search: {},
});

function page(agent: Partial<AgentPage> = {}, over: Partial<Turns> = {}): Turns {
  const base = fixtureTurns(true);
  return { ...base, ...over, agent: { ...base.agent, ...agent } } as Turns;
}

const spyOnTurns = () => vi.spyOn(api, "turns").mockResolvedValue(ok(page()));
let turnsRead: ReturnType<typeof spyOnTurns>;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  queryClient.clear();
  turnsRead = spyOnTurns();
  useFleet.getState().apply("snapshot", fixtureSnapshot(NOW));
  definePanel({
    id: "turns",
    title: () => "Conversation",
    tab: { order: 1, label: "conversation" },
    Body: () => null,
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

async function settle(ms = 1): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

function wrapper({ children }: { children: ReactNode }): ReactElement {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

// The shell mounts the head and the strip beside the body, which reads the page; the tests read
// it with this in the body's place.
function Reader({ session }: { session: number }): null {
  useTurnPages({ session, agent: AGENT, live: false });
  return null;
}

function navigated(oldURL: string): void {
  window.dispatchEvent(Object.assign(new Event("hashchange"), { oldURL }));
}

describe("AgentHead", () => {
  it.each([
    [
      { description: "Map every caller of the cursor path", type: "recon" },
      "Map every caller of the cursor path",
    ],
    [{ description: "", type: "recon" }, "recon"],
    [{ description: "", type: "" }, "subagent"],
  ])(
    'titles the row with the agent\'s description, then its type, else "subagent" (%j)',
    async (agent, title) => {
      turnsRead.mockResolvedValue(ok(page(agent)));
      render(
        <>
          <AgentHead {...props(1)} />
          <Reader session={1} />
        </>,
        { wrapper },
      );
      expect(screen.getByRole("heading").textContent).toBe("subagent");
      await settle();
      expect(screen.getByRole("heading").textContent).toBe(title);
    },
  );

  it("has a way back named conversation, to the session's conversation", () => {
    render(<AgentHead {...props(1)} />, { wrapper });
    const link = screen.getByRole("link", { name: /conversation/ });
    expect(link.getAttribute("href")).toBe("#/s/1");
  });

  it.each(["http://localhost/#/s/1", "http://localhost/#s/1", "http://localhost/#/s/1/turns"])(
    "goes back through history when the conversation was the previous page (%s)",
    (url) => {
      const back = vi.spyOn(window.history, "back").mockImplementation(() => undefined);
      render(<AgentHead {...props(1)} />, { wrapper });
      navigated(url);
      expect(fireEvent.click(screen.getByRole("link", { name: /conversation/ }))).toBe(false);
      expect(back).toHaveBeenCalledTimes(1);
    },
  );

  it.each(["http://localhost/#/s/1/info", "http://localhost/#/s/2", "http://localhost/#/"])(
    "follows the link when the previous page was not this conversation (%s)",
    (url) => {
      const back = vi.spyOn(window.history, "back").mockImplementation(() => undefined);
      render(<AgentHead {...props(1)} />, { wrapper });
      navigated(url);
      expect(fireEvent.click(screen.getByRole("link", { name: /conversation|fleet/ }))).toBe(true);
      expect(back).not.toHaveBeenCalled();
    },
  );

  it("draws the fleet as the way back, with no title, for a session the snapshot does not list", () => {
    render(<AgentHead {...props(99)} />, { wrapper });
    expect(screen.getByRole("link").getAttribute("href")).toBe("#/");
    expect(screen.getByRole("heading").textContent).toBe("");
  });
});

describe("pending session", () => {
  afterEach(() => {
    usePending.getState().clear();
  });

  it("AgentHead titles a pending unlisted session Loading… and keeps its way back to the fleet", () => {
    usePending.setState({ id: 99 });
    render(<AgentHead {...props(99)} />, { wrapper });
    expect(screen.getByRole("heading").textContent).toBe("Loading…");
    expect(screen.getByRole("link").getAttribute("href")).toBe("#/");
  });

  it("AgentHead keeps its empty title for an unlisted session that is not pending", () => {
    expect(usePending.getState().id).toBeNull();
    render(<AgentHead {...props(99)} />, { wrapper });
    expect(screen.getByRole("heading").textContent).toBe("");
  });
});

describe("AgentStrip", () => {
  it.each([
    [{ state: "running" }, "◐ subagent running 3m · recon"],
    [{ state: "finished" }, "✓ finished · recon"],
    [{ state: "finished", teammate: true }, "○ idle · recon"],
    [{ state: "unknown" }, "subagent · recon"],
    [{ state: "unknown", type: "" }, "subagent"],
  ] as [Partial<AgentPage>, string][])(
    "says what the agent is doing by its state, then its type (%j)",
    async (agent, text) => {
      turnsRead.mockResolvedValue(ok(page(agent)));
      const { container } = render(
        <>
          <AgentStrip {...props(1)} />
          <Reader session={1} />
        </>,
        { wrapper },
      );
      await settle();
      expect(container.querySelector('[data-slot="agent-strip"]')?.textContent).toBe(text);
    },
  );

  it("links to the conversation only while the session needs you", async () => {
    const needing = render(
      <>
        <AgentStrip {...props(2)} />
        <Reader session={2} />
      </>,
      { wrapper },
    );
    await settle();
    const link = screen.getByRole("link", { name: "● session needs you ›" });
    expect(link.getAttribute("href")).toBe("#/s/2");
    expect(link.parentElement?.closest("a")).toBeNull();
    needing.unmount();

    render(
      <>
        <AgentStrip {...props(1)} />
        <Reader session={1} />
      </>,
      { wrapper },
    );
    await settle();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("draws nothing before the page lands or for a session the snapshot does not list", async () => {
    const { container } = render(
      <>
        <AgentStrip {...props(1)} />
        <Reader session={1} />
      </>,
      { wrapper },
    );
    expect(container.querySelector('[data-slot="agent-strip"]')).toBeNull();
    const other = render(
      <>
        <AgentStrip {...props(99)} />
        <Reader session={99} />
      </>,
      { wrapper },
    );
    await settle();
    expect(other.container.querySelector('[data-slot="agent-strip"]')).toBeNull();
  });
});

const refused = (status: number, message: string): Outcome<never> => ({
  kind: "refused",
  status,
  message,
});

function draw(session = 1): ReturnType<typeof render> {
  return render(<Agent {...props(session)} />, { wrapper });
}

describe("the subagent's screen", () => {
  it("says Loading… until the first page lands, then draws the brief", async () => {
    let release: (o: Outcome<Turns>) => void = () => undefined;
    turnsRead.mockImplementationOnce(
      () =>
        new Promise<Outcome<Turns>>((resolve) => {
          release = resolve;
        }),
    );
    draw();
    await settle();
    expect(screen.getByText("Loading…")).toBeTruthy();
    await act(async () => {
      release(ok(page()));
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.getByText(/You are mapping callers\./)).toBeTruthy();
    expect(screen.getAllByText(/BRIEF/).length).toBeGreaterThan(0);
  });

  it("shows a failed first read with a retry that loads the newest page", async () => {
    turnsRead.mockResolvedValueOnce(refused(500, "boom"));
    draw();
    await settle();
    expect(screen.getByText("boom")).toBeTruthy();
    turnsRead.mockResolvedValueOnce(ok(page()));
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await settle();
    expect(screen.getByText(/You are mapping callers\./)).toBeTruthy();
    expect(screen.queryByText("boom")).toBeNull();
    expect(turnsRead).toHaveBeenCalledTimes(2);
  });

  it("draws no reply box and no needs-you box, even when the session needs you", async () => {
    draw(2);
    await settle();
    expect(screen.getByText(/You are mapping callers\./)).toBeTruthy();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByText(/Open in Claude/)).toBeNull();
  });

  it("says No turns yet. for a page without turns", async () => {
    turnsRead.mockResolvedValue(ok(page({ state: "finished" }, { turns: [] })));
    draw();
    await settle();
    expect(screen.getByText("No turns yet.")).toBeTruthy();
  });

  it("says some records could not be read and pages back to the earlier turns", async () => {
    turnsRead.mockResolvedValue(ok(page({}, { skipped: 2, earlier: 5 })));
    const first = draw();
    await settle();
    expect(screen.getByText("Some records could not be read.")).toBeTruthy();
    turnsRead.mockResolvedValueOnce(ok(page({}, { skipped: 2, earlier: null })));
    fireEvent.click(screen.getByRole("button", { name: "↑ earlier turns" }));
    await settle();
    expect(turnsRead).toHaveBeenLastCalledWith(
      1,
      expect.objectContaining({ agent: AGENT, before: "5" }),
    );
    first.unmount();

    queryClient.clear();
    turnsRead.mockResolvedValue(ok(page({}, { skipped: 0, earlier: null })));
    draw();
    await settle();
    expect(screen.queryByText("Some records could not be read.")).toBeNull();
    expect(screen.queryByRole("button", { name: "↑ earlier turns" })).toBeNull();
  });

  it("says Starting… while the agent's record is missing, and gone after 30 seconds", async () => {
    turnsRead.mockResolvedValue(ok(page({ state: "running", missing: true })));
    draw();
    await settle();
    expect(screen.getByText("Starting…")).toBeTruthy();
    await settle(29_000);
    expect(screen.getByText("Starting…")).toBeTruthy();
    await settle(2_000);
    expect(screen.getByText("This subagent's record is gone.")).toBeTruthy();
  });

  it("is gone at once when the agent finished and its record is missing", async () => {
    turnsRead.mockResolvedValue(ok(page({ state: "finished", missing: true })));
    const first = draw();
    await settle();
    expect(screen.getByText("This subagent's record is gone.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "retry" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "‹ conversation" }).getAttribute("href")).toBe("#/s/1");
    first.unmount();

    queryClient.clear();
    turnsRead.mockResolvedValue(ok(page({ state: "finished", missing: false })));
    draw();
    await settle();
    expect(screen.queryByText("This subagent's record is gone.")).toBeNull();
    expect(screen.getByText(/You are mapping callers\./)).toBeTruthy();
  });

  it("retries a gone record, which restarts the 30 second wait", async () => {
    turnsRead.mockResolvedValue(ok(page({ state: "running", missing: true })));
    draw();
    await settle();
    await settle(31_000);
    expect(screen.getByText("This subagent's record is gone.")).toBeTruthy();
    const reads = turnsRead.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await settle();
    expect(screen.getByText("Starting…")).toBeTruthy();
    expect(turnsRead.mock.calls.length).toBeGreaterThan(reads);
    expect(turnsRead.mock.calls[turnsRead.mock.calls.length - 1]?.[1]).not.toHaveProperty("after");
    await settle(29_000);
    expect(screen.getByText("Starting…")).toBeTruthy();
    await settle(2_000);
    expect(screen.getByText("This subagent's record is gone.")).toBeTruthy();
  });

  it("polls every 5 seconds while the agent runs, and once more when it finishes, then stops", async () => {
    turnsRead.mockResolvedValue(ok(page({ state: "running" })));
    draw();
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    await settle(5_000);
    expect(turnsRead).toHaveBeenCalledTimes(2);
    turnsRead.mockResolvedValue(ok(page({ state: "finished" })));
    // The poll that learns of the finish is the third read, and the one more is the fourth.
    await settle(5_000);
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(4);
    await settle(30_000);
    expect(turnsRead).toHaveBeenCalledTimes(4);
  });

  it("does not poll a finished agent, nor refresh for a first page that says finished", async () => {
    turnsRead.mockResolvedValue(ok(page({ state: "finished" })));
    draw();
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    await settle(30_000);
    expect(turnsRead).toHaveBeenCalledTimes(1);
  });

  it("polls an unknown state while the session is running", async () => {
    turnsRead.mockResolvedValue(ok(page({ state: "unknown" })));
    draw();
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    await settle(5_000);
    expect(turnsRead).toHaveBeenCalledTimes(2);
  });

  it("does not poll an unknown state while the session is not running", async () => {
    const snapshot = fixtureSnapshot(NOW);
    const session = allSessions(snapshot).find((s) => s.id === 1);
    if (session === undefined) throw new Error("fixture has no session 1");
    session.status = "Ready";
    useFleet.getState().apply("snapshot", snapshot);
    turnsRead.mockResolvedValue(ok(page({ state: "unknown" })));
    draw();
    await settle();
    expect(turnsRead).toHaveBeenCalledTimes(1);
    await settle(30_000);
    expect(turnsRead).toHaveBeenCalledTimes(1);
  });

  it("keeps the turns when a refresh fails, with a retry that refreshes", async () => {
    turnsRead.mockResolvedValue(ok(page({ state: "running" })));
    draw();
    await settle();
    turnsRead.mockResolvedValueOnce(refused(502, "boom"));
    await settle(5_000);
    expect(screen.getByText(/You are mapping callers\./)).toBeTruthy();
    expect(screen.getByText(/Could not refresh: boom/)).toBeTruthy();
    turnsRead.mockResolvedValue(ok(page({ state: "finished" })));
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    await settle();
    expect(screen.queryByText(/Could not refresh/)).toBeNull();
  });

  it("draws This session is gone. for a session the snapshot does not list, and reads nothing", async () => {
    draw(99);
    await settle();
    expect(screen.getByText("This session is gone.")).toBeTruthy();
    expect(turnsRead).not.toHaveBeenCalled();
  });
});

describe("the agent panel", () => {
  it("is registered with a head, a strip and no tab", () => {
    const panel = panelFor("agent");
    expect(panel).toBeDefined();
    expect(panel?.Head).toBe(AgentHead);
    expect(panel?.Strip).toBe(AgentStrip);
    expect(panel?.Body).toBe(Agent);
    expect(panel?.tab).toBeUndefined();
    expect(panel?.appliesTo).toBeUndefined();
    expect(panel?.title(props(1))).toBe("Subagent");
  });
});

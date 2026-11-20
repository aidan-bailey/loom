import { QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentPage, Outcome, Turns } from "@/api/gen";
import { queryClient } from "@/api/queries";
import { useTurnPages } from "@/api/reads";
import { api } from "@/api/request";
import { definePanel } from "@/app/panels";
import { fixtureSnapshot, fixtureTurns } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { AgentHead, AgentStrip } from "./Agent";

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

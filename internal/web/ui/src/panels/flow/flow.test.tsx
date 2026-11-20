import { act, render, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Session } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";

import { usd as rowUsd } from "../fleet/SessionRow";
import { Bars, median, planOf, usd, useClock } from "./parts";

function session6(over: Partial<Session> = {}): Session {
  const found = allSessions(fixtureSnapshot()).find((s) => s.id === 6);
  if (found === undefined) throw new Error("the canned snapshot holds no session 6");
  return { ...found, ...over };
}

afterEach(() => {
  vi.useRealTimers();
});

describe("flow parts", () => {
  it("planOf: the given plan, else the newest flow's, else none", () => {
    const session = session6();
    expect(session.flows).toHaveLength(2);
    expect(planOf(session, "x.md")).toBe("x.md");
    expect(planOf(session, "")).toBe(session.flows?.[0]?.plan);
    expect(planOf(session6({ flows: [] }), "")).toBe("");
    expect(planOf(session6({ flows: null }), "")).toBe("");
  });

  it("usd: words a figure as the fleet row does, and nothing for no number", () => {
    expect(usd(4.71)).toBe("$4.71");
    expect(usd(0.0031)).toBe("$0.0031");
    expect(usd(0.0312)).toBe("$0.03");
    expect(usd(0)).toBe("$0.00");
    expect(usd(0.01)).toBe("$0.01");
    expect(usd(undefined)).toBe("");
    expect(usd(null)).toBe("");
    expect(usd(0.0031)).toBe(rowUsd(0.0031));
  });

  it("median: the middle value, the mean of the middle two, and none for none", () => {
    const values = [3, 1, 2];
    expect(median(values)).toBe(2);
    expect(values).toEqual([3, 1, 2]);
    expect(median([4, 1, 3, 2])).toBe(2.5);
    expect(median([])).toBe(0);
  });

  it("Bars: one bar a value, the current one after them in the accent colour, each height an exact share of the tallest", () => {
    const heights = (container: HTMLElement): string[] =>
      Array.from(container.querySelectorAll<HTMLElement>('[data-slot="bar"]')).map(
        (el) => el.style.height,
      );
    const view = render(<Bars values={[1, 2, 4]} current={2} />);
    const bars = view.container.querySelectorAll<HTMLElement>('[data-slot="bar"]');
    expect(bars).toHaveLength(4);
    expect(bars[3]?.getAttribute("data-current")).toBe("true");
    expect(bars[3]?.classList.contains("bg-turn")).toBe(true);
    expect(bars[0]?.classList.contains("bg-turn")).toBe(false);
    expect(bars[0]?.hasAttribute("data-current")).toBe(false);
    expect(heights(view.container)).toEqual(["25%", "50%", "100%", "50%"]);
    expect(view.container.firstElementChild?.getAttribute("aria-hidden")).toBe("true");
    view.unmount();

    const none = render(<Bars values={[1, 2, 4]} current={null} />);
    expect(none.container.querySelectorAll('[data-slot="bar"]')).toHaveLength(3);
    none.unmount();

    const floor = render(<Bars values={[0.001, 4]} current={null} />);
    expect(heights(floor.container)[0]).toBe("5%");
    floor.unmount();

    const tall = render(<Bars values={[2]} current={8} />);
    expect(heights(tall.container)).toEqual(["25%", "100%"]);
    tall.unmount();

    const share = render(<Bars values={[1, 3]} current={null} />);
    expect(parseFloat(heights(share.container)[0] ?? "")).toBeCloseTo(33.333, 2);
  });

  it("useClock: the time now, moving every ms and no sooner, and stopped on unmount", () => {
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    const start = Date.parse("2026-11-19T12:00:00Z");
    vi.setSystemTime(start);
    const slow = renderHook(() => useClock(30_000));
    expect(slow.result.current).toBe(start);
    act(() => vi.advanceTimersByTime(29_000));
    expect(slow.result.current).toBe(start);
    act(() => vi.advanceTimersByTime(1_000));
    expect(slow.result.current).toBe(start + 30_000);
    slow.unmount();

    const now = Date.now();
    const fast = renderHook(() => useClock(1_000));
    act(() => vi.advanceTimersByTime(1_000));
    expect(fast.result.current).toBe(now + 1_000);
    fast.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});

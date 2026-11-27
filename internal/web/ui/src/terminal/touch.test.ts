import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { bindTouchScroll, type Scroller } from "./touch";

let now = 0;
let nextId = 0;
let queue: { id: number; cb: (t: number) => void }[] = [];
let el: HTMLElement;
let calls: number[];
let cell: number;
let unbind: (() => void) | null;

function step(ms = 16): void {
  now += ms;
  const due = queue;
  queue = [];
  for (const f of due) f.cb(now);
}

function runFrames(max = 200): number {
  let n = 0;
  while (queue.length > 0 && n < max) {
    step();
    n++;
  }
  return n;
}

function touch(type: string, ys: number[]): Event {
  const ev = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "touches", { value: ys.map((y) => ({ clientY: y })) });
  el.dispatchEvent(ev);
  return ev;
}

// A touchstart at `from`, then a move for each dy, `dt` ms apart; returns the last y.
function drag(from: number, dys: number[], dt: number): number {
  touch("touchstart", [from]);
  let y = from;
  for (const dy of dys) {
    now += dt;
    y += dy;
    touch("touchmove", [y]);
  }
  return y;
}

function bind(): void {
  const scroller: Scroller = {
    scrollLines: (n) => calls.push(n),
    cellHeight: () => cell,
  };
  unbind = bindTouchScroll(el, scroller);
}

function sum(xs: number[]): number {
  return xs.reduce((a, b) => a + b, 0);
}

beforeEach(() => {
  now = 0;
  nextId = 0;
  queue = [];
  calls = [];
  cell = 10;
  unbind = null;
  vi.spyOn(performance, "now").mockImplementation(() => now);
  vi.stubGlobal("requestAnimationFrame", (cb: (t: number) => void) => {
    queue.push({ id: ++nextId, cb });
    return nextId;
  });
  vi.stubGlobal("cancelAnimationFrame", (id: number) => {
    queue = queue.filter((f) => f.id !== id);
  });
  el = document.createElement("div");
  document.body.appendChild(el);
});

afterEach(() => {
  unbind?.();
  el.remove();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("bindTouchScroll", () => {
  it("a drag down shows older lines, one line a cell height, keeping the remainder", () => {
    bind();
    touch("touchstart", [100]);
    touch("touchmove", [125]);
    expect(calls).toEqual([-2]);
    touch("touchmove", [130]);
    expect(calls).toEqual([-2, -1]);
  });

  it("a drag up shows newer lines", () => {
    bind();
    touch("touchstart", [100]);
    touch("touchmove", [70]);
    expect(calls).toEqual([3]);
  });

  it("moves under a cell height accumulate until they reach one", () => {
    bind();
    touch("touchstart", [100]);
    touch("touchmove", [104]);
    touch("touchmove", [108]);
    expect(calls).toEqual([]);
    touch("touchmove", [112]);
    expect(calls).toEqual([-1]);
  });

  it("reads the cell height at each move", () => {
    bind();
    touch("touchstart", [100]);
    touch("touchmove", [110]);
    expect(calls).toEqual([-1]);
    cell = 20;
    touch("touchmove", [120]);
    expect(calls).toEqual([-1]);
    touch("touchmove", [130]);
    expect(calls).toEqual([-1, -1]);
  });

  it("a zero or non-finite cell height scrolls nothing", () => {
    bind();
    for (const h of [0, NaN]) {
      cell = h;
      touch("touchstart", [100]);
      expect(() => touch("touchmove", [150])).not.toThrow();
      expect(calls).toEqual([]);
    }
  });

  it("cancels touchstart and touchmove, and registers them as non-passive", () => {
    const spy = vi.spyOn(el, "addEventListener");
    bind();
    for (const type of ["touchstart", "touchmove"]) {
      const call = spy.mock.calls.find((c) => c[0] === type);
      expect(call?.[2]).toEqual(expect.objectContaining({ passive: false }));
    }
    expect(touch("touchstart", [100]).defaultPrevented).toBe(true);
    expect(touch("touchmove", [110]).defaultPrevented).toBe(true);
    expect(touch("touchstart", [100, 200]).defaultPrevented).toBe(true);
  });

  it("carries a flick on at the lift's speed, decaying by 0.95 a frame until under 0.02 px/ms", () => {
    bind();
    drag(300, [-16, -16, -16], 16);
    expect(calls.every((c) => c > 0)).toBe(true);
    expect(sum(calls)).toBe(4);
    const dragCalls = calls.length;
    touch("touchend", []);
    expect(runFrames()).toBe(77);
    expect(queue).toHaveLength(0);
    const momentum = calls.slice(dragCalls);
    expect(momentum.every((c) => c > 0)).toBe(true);
    expect(sum(momentum)).toBe(32);
  });

  it("carries a flick on in the direction the finger moved", () => {
    bind();
    drag(100, [16, 16, 16], 16);
    expect(calls.every((c) => c < 0)).toBe(true);
    const dragCalls = calls.length;
    touch("touchend", []);
    runFrames();
    const momentum = calls.slice(dragCalls);
    expect(momentum.every((c) => c < 0)).toBe(true);
    expect(sum(momentum)).toBe(-32);
  });

  it("a lift at 0.3 px/ms or less carries on no further, and one above it does", () => {
    bind();
    for (const [dy, queued] of [
      [4, 0],
      [6, 0],
      [8, 1],
    ] as const) {
      drag(100, [dy], 20);
      touch("touchend", []);
      expect(queue).toHaveLength(queued);
      touch("touchstart", [100]); // stops any momentum before the next run
      touch("touchend", []);
    }
  });

  it("a finger held still before it lifts carries on no further", () => {
    bind();
    drag(100, [16], 16);
    now += 150;
    touch("touchend", []);
    expect(queue).toHaveLength(0);
    drag(100, [16], 16);
    now += 90;
    touch("touchend", []);
    expect(queue).toHaveLength(1);
  });

  it("a finger held still for exactly 100 ms before it lifts still flicks, and one more ms does not", () => {
    bind();
    drag(100, [16], 16);
    now += 100;
    touch("touchend", []);
    expect(queue).toHaveLength(1);
    touch("touchstart", [100]); // stops the momentum
    touch("touchend", []);
    drag(100, [16], 16);
    now += 101;
    touch("touchend", []);
    expect(queue).toHaveLength(0);
  });

  it("a new touch stops the momentum", () => {
    bind();
    drag(300, [-16, -16, -16], 16);
    const dragCalls = calls.length;
    touch("touchend", []);
    step();
    step();
    const before = calls.length;
    expect(before).toBeGreaterThan(dragCalls);
    touch("touchstart", [200]);
    expect(queue).toHaveLength(0);
    for (let i = 0; i < 5; i++) step();
    expect(calls).toHaveLength(before);
  });

  it("a second finger ends the drag, and the finger left does not resume it", () => {
    bind();
    touch("touchstart", [100]);
    touch("touchmove", [130]);
    expect(calls).toEqual([-3]);
    touch("touchstart", [130, 200]);
    touch("touchmove", [150, 220]);
    expect(calls).toEqual([-3]);
    touch("touchend", [150]);
    expect(queue).toHaveLength(0);
    expect(calls).toEqual([-3]);
    touch("touchmove", [190]);
    expect(calls).toEqual([-3]);
    touch("touchend", []);
    expect(queue).toHaveLength(0);
    touch("touchstart", [100]);
    touch("touchmove", [120]);
    expect(calls).toEqual([-3, -2]);
  });

  it("a second finger that lands and lifts before any move still ends the drag", () => {
    bind();
    touch("touchstart", [100]);
    touch("touchmove", [130]);
    expect(calls).toEqual([-3]);
    touch("touchstart", [130, 200]);
    touch("touchend", [130]);
    touch("touchmove", [160]);
    expect(calls).toEqual([-3]);
  });

  it("a moving drag ended by touchmove with two fingers scrolls nothing further", () => {
    bind();
    touch("touchstart", [100]);
    touch("touchmove", [120, 300]);
    expect(calls).toEqual([]);
    touch("touchmove", [140]);
    expect(calls).toEqual([]);
  });

  it("touchcancel ends the drag with no momentum", () => {
    bind();
    drag(300, [-16, -16], 16);
    const before = calls.length;
    touch("touchcancel", []);
    expect(queue).toHaveLength(0);
    touch("touchmove", [200]);
    expect(calls).toHaveLength(before);
  });

  it("the unbind stops everything", () => {
    bind();
    drag(300, [-16, -16, -16], 16);
    touch("touchend", []);
    expect(queue).toHaveLength(1);
    unbind?.();
    expect(queue).toHaveLength(0);
    const before = calls.length;
    step();
    expect(calls).toHaveLength(before);
    expect(touch("touchstart", [100]).defaultPrevented).toBe(false);
    expect(touch("touchmove", [150]).defaultPrevented).toBe(false);
    expect(touch("touchend", []).defaultPrevented).toBe(false);
    expect(calls).toHaveLength(before);
    expect(() => unbind?.()).not.toThrow();
  });
});

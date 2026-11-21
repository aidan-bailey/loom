// The test helper the panels' scroll tests share; imported only by tests.

// Scroller is a detached scrolling element over numbers a test sets.
export interface Scroller {
  el: HTMLDivElement;
  scrolls: number[];
  set: (over: { height?: number; viewport?: number; top?: number }) => void;
  place: (top: number) => void;
}

// stubScroller makes a scroller: assigning scrollTop records the value, place moves it as a reader does.
export function stubScroller(init: { height: number; viewport: number; top?: number }): Scroller {
  const el = document.createElement("div");
  let height = init.height;
  let viewport = init.viewport;
  let top = init.top ?? 0;
  const scrolls: number[] = [];
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => height });
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => viewport });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => top,
    set: (value: number) => {
      scrolls.push(value);
      top = value;
    },
  });
  return {
    el,
    scrolls,
    set: (over) => {
      if (over.height !== undefined) height = over.height;
      if (over.viewport !== undefined) viewport = over.viewport;
      if (over.top !== undefined) top = over.top;
    },
    place: (to) => {
      top = to;
      el.dispatchEvent(new Event("scroll"));
    },
  };
}

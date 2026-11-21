// Test-only: the window width the layout tests drive. happy-dom's own window is 1024px wide, which
// useLayout reads as `desk2`, so test-setup.ts sets 390 (the phone) before every test; a test
// that wants a desktop frame calls setViewportWidth itself.
import { act } from "@testing-library/react";

type Listener = (event: Event) => void;

interface Watch {
  query: string;
  listener: Listener;
}

let width = 390;
const watches: Watch[] = [];
let installed = false;

// matchesQuery answers a `(min-width: <n>px)` query from the current width; any other query
// (display-mode, prefers-reduced-motion) is false, as a headless browser answers it.
function matchesQuery(query: string): boolean {
  const min = query.trim().match(/^\(min-width:\s*(\d+)px\)$/);
  return min !== null && width >= Number(min[1]);
}

function fakeMatchMedia(query: string): MediaQueryList {
  const list = {
    media: query,
    get matches(): boolean {
      return matchesQuery(query);
    },
    onchange: null,
    addEventListener(type: string, listener: Listener): void {
      if (type === "change") watches.push({ query, listener });
    },
    removeEventListener(type: string, listener: Listener): void {
      if (type !== "change") return;
      const at = watches.findIndex((w) => w.query === query && w.listener === listener);
      if (at >= 0) watches.splice(at, 1);
    },
    addListener(): void {},
    removeListener(): void {},
    dispatchEvent(): boolean {
      return false;
    },
  };
  return list as MediaQueryList;
}

// setViewportWidth makes the window `next` pixels wide for matchMedia and useLayout, and tells
// every change listener inside act, so a test needs no act of its own around it.
export function setViewportWidth(next: number): void {
  if (!installed) {
    installed = true;
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: fakeMatchMedia,
    });
  }
  width = next;
  act(() => {
    for (const watch of [...watches]) watch.listener(new Event("change"));
  });
}

// viewportWatchers is how many change listeners are registered now.
export function viewportWatchers(): number {
  return watches.length;
}

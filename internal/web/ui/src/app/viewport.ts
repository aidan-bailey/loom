// The layout the window's width gives (spec §4): the phone's one column under 1024px, `desk2` from
// 1024px to 1279px (a sidebar and the middle) and `desk3` from 1280px (the inspector too). It is
// read from matchMedia, so a resize swaps the layout live.
import { useSyncExternalStore } from "react";

export type Layout = "phone" | "desk2" | "desk3";

export const DESK2_MIN = 1024;
export const DESK3_MIN = 1280;

// layoutOf is the layout a window `width` pixels wide has.
export function layoutOf(width: number): Layout {
  if (width >= DESK3_MIN) return "desk3";
  if (width >= DESK2_MIN) return "desk2";
  return "phone";
}

const DESK2_QUERY = `(min-width: ${DESK2_MIN}px)`;
const DESK3_QUERY = `(min-width: ${DESK3_MIN}px)`;

// read is the layout now. A page with no matchMedia is the phone.
function read(): Layout {
  if (typeof window.matchMedia !== "function") return "phone";
  if (window.matchMedia(DESK3_QUERY).matches) return layoutOf(DESK3_MIN);
  if (window.matchMedia(DESK2_QUERY).matches) return layoutOf(DESK2_MIN);
  return layoutOf(0);
}

// subscribe calls onChange when either breakpoint is crossed. A media query list that cannot
// listen is skipped: its value is read once.
function subscribe(onChange: () => void): () => void {
  if (typeof window.matchMedia !== "function") return () => {};
  const lists = [window.matchMedia(DESK2_QUERY), window.matchMedia(DESK3_QUERY)];
  for (const list of lists) {
    if (typeof list.addEventListener === "function") list.addEventListener("change", onChange);
  }
  return () => {
    for (const list of lists) {
      if (typeof list.removeEventListener === "function") {
        list.removeEventListener("change", onChange);
      }
    }
  };
}

// useLayout is the window's layout, re-read whenever it crosses 1024px or 1280px.
export function useLayout(): Layout {
  return useSyncExternalStore(subscribe, read, () => "phone");
}

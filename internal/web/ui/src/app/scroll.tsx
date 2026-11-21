import { createContext, useContext, type ReactElement, type ReactNode } from "react";

// What a panel scrolls: the window on the phone, a scrolling element on the desktop.
export interface ScrollPane {
  top: () => number;
  viewport: () => number;
  height: () => number;
  scrollTo: (top: number) => void;
  topFor: (el: Element) => number;
  subscribe: (listener: () => void) => () => void;
}

// The window as a pane, with a position just below the page's pinned header.
export const windowPane: ScrollPane = {
  top: () => window.scrollY,
  viewport: () => window.innerHeight,
  height: () => document.documentElement.scrollHeight,
  scrollTo: (top) => {
    window.scrollTo(0, top);
  },
  topFor: (el) => {
    const header = document.querySelector('[data-slot="header"]');
    const headerHeight = header === null ? 0 : header.getBoundingClientRect().height;
    return Math.max(0, el.getBoundingClientRect().top + window.scrollY - headerHeight);
  },
  subscribe: (listener) => {
    window.addEventListener("scroll", listener, { passive: true });
    return () => {
      window.removeEventListener("scroll", listener);
    };
  },
};

// A scrolling element as a pane, with a position just below its own head (null when none).
export function elementPane(scroller: HTMLElement, head: HTMLElement | null): ScrollPane {
  return {
    top: () => scroller.scrollTop,
    viewport: () => scroller.clientHeight,
    height: () => scroller.scrollHeight,
    scrollTo: (top) => {
      scroller.scrollTop = top;
    },
    topFor: (el) => {
      const headHeight = head === null ? 0 : head.getBoundingClientRect().height;
      return Math.max(
        0,
        el.getBoundingClientRect().top -
          scroller.getBoundingClientRect().top +
          scroller.scrollTop -
          headHeight,
      );
    },
    subscribe: (listener) => {
      scroller.addEventListener("scroll", listener, { passive: true });
      return () => {
        scroller.removeEventListener("scroll", listener);
      };
    },
  };
}

const PaneContext = createContext<ScrollPane>(windowPane);

// ScrollPaneProvider gives the panels below it a pane to scroll.
export function ScrollPaneProvider(props: { pane: ScrollPane; children: ReactNode }): ReactElement {
  return <PaneContext.Provider value={props.pane}>{props.children}</PaneContext.Provider>;
}

// useScrollPane is the pane the panel scrolls: the window unless a provider says otherwise.
export function useScrollPane(): ScrollPane {
  return useContext(PaneContext);
}

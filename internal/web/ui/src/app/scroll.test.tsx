import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { elementPane, ScrollPaneProvider, useScrollPane, windowPane } from "./scroll";
import { stubScroller } from "./scrollkit";

function setWindow(scrollY: number, innerHeight: number, scrollHeight: number): void {
  Object.defineProperty(window, "scrollY", { configurable: true, value: scrollY });
  Object.defineProperty(window, "innerHeight", { configurable: true, value: innerHeight });
  Object.defineProperty(document.documentElement, "scrollHeight", {
    configurable: true,
    value: scrollHeight,
  });
}

function rect(el: Element, over: { top?: number; height?: number }): void {
  vi.spyOn(el, "getBoundingClientRect").mockReturnValue({
    top: over.top ?? 0,
    height: over.height ?? 0,
  } as DOMRect);
}

afterEach(() => {
  Reflect.deleteProperty(window, "scrollY");
  Reflect.deleteProperty(window, "innerHeight");
  Reflect.deleteProperty(document.documentElement, "scrollHeight");
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("windowPane", () => {
  it("windowPane reads the window", () => {
    setWindow(120, 800, 2000);
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
    expect(windowPane.top()).toBe(120);
    expect(windowPane.viewport()).toBe(800);
    expect(windowPane.height()).toBe(2000);
    windowPane.scrollTo(300);
    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenCalledWith(0, 300);
  });

  it("windowPane.topFor puts an element just below the page's header, never above 0", () => {
    const header = document.createElement("header");
    header.setAttribute("data-slot", "header");
    rect(header, { height: 120 });
    document.body.append(header);
    const child = document.createElement("div");
    document.body.append(child);

    setWindow(100, 800, 2000);
    rect(child, { top: 500 });
    expect(windowPane.topFor(child)).toBe(480);

    setWindow(0, 800, 2000);
    rect(child, { top: 10 });
    expect(windowPane.topFor(child)).toBe(0);

    header.remove();
    setWindow(100, 800, 2000);
    rect(child, { top: 500 });
    expect(windowPane.topFor(child)).toBe(600);
  });

  it("windowPane.subscribe calls the listener on each window scroll until stopped", () => {
    const listener = vi.fn();
    const stop = windowPane.subscribe(listener);
    fireEvent.scroll(window);
    fireEvent.scroll(window);
    expect(listener).toHaveBeenCalledTimes(2);
    stop();
    fireEvent.scroll(window);
    expect(listener).toHaveBeenCalledTimes(2);
  });
});

describe("elementPane", () => {
  it("elementPane reads the scroller", () => {
    const s = stubScroller({ height: 2000, viewport: 600, top: 40 });
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
    const pane = elementPane(s.el, null);
    expect(pane.top()).toBe(40);
    expect(pane.viewport()).toBe(600);
    expect(pane.height()).toBe(2000);
    pane.scrollTo(700);
    expect(s.scrolls).toEqual([700]);
    expect(pane.top()).toBe(700);
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("elementPane.topFor measures from the scroller, below its head when it has one", () => {
    const s = stubScroller({ height: 4000, viewport: 600, top: 300 });
    rect(s.el, { top: 200 });
    const child = document.createElement("div");
    rect(child, { top: 1400 });
    const head = document.createElement("div");
    rect(head, { height: 70 });
    expect(elementPane(s.el, null).topFor(child)).toBe(1500);
    expect(elementPane(s.el, head).topFor(child)).toBe(1430);

    s.set({ top: 0 });
    rect(child, { top: 10 });
    expect(elementPane(s.el, null).topFor(child)).toBe(0);
    expect(elementPane(s.el, head).topFor(child)).toBe(0);
  });

  it("elementPane.subscribe follows the scroller, not the window", () => {
    const s = stubScroller({ height: 2000, viewport: 600 });
    const listener = vi.fn();
    const stop = elementPane(s.el, null).subscribe(listener);
    s.place(10);
    expect(listener).toHaveBeenCalledTimes(1);
    fireEvent.scroll(window);
    expect(listener).toHaveBeenCalledTimes(1);
    stop();
    s.place(20);
    expect(listener).toHaveBeenCalledTimes(1);
  });
});

function Probe(): React.ReactElement {
  return <p>{String(useScrollPane() === windowPane)}</p>;
}

function Same(props: { pane: unknown }): React.ReactElement {
  return <p>{String(useScrollPane() === props.pane)}</p>;
}

describe("useScrollPane", () => {
  it("useScrollPane is the window pane without a provider and the given pane inside one", () => {
    const bare = render(<Probe />);
    expect(screen.getByText("true")).toBeTruthy();
    bare.unmount();

    const pane = elementPane(stubScroller({ height: 2000, viewport: 600 }).el, null);
    render(
      <ScrollPaneProvider pane={pane}>
        <Probe />
        <Same pane={pane} />
      </ScrollPaneProvider>,
    );
    expect(screen.getByText("false")).toBeTruthy();
    expect(screen.getByText("true")).toBeTruthy();
  });
});

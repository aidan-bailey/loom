import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { setViewportWidth, viewportWatchers } from "@/test-layout";

import { DESK2_MIN, DESK3_MIN, layoutOf, useLayout, type Layout } from "./viewport";

describe("layoutOf", () => {
  it("the breakpoints are 1024px and 1280px", () => {
    expect(DESK2_MIN).toBe(1024);
    expect(DESK3_MIN).toBe(1280);
  });

  it.each<[number, Layout]>([
    [0, "phone"],
    [390, "phone"],
    [1023, "phone"],
    [1024, "desk2"],
    [1279, "desk2"],
    [1280, "desk3"],
    [2560, "desk3"],
  ])("layoutOf(%i) is %s", (width, want) => {
    expect(layoutOf(width)).toBe(want);
  });
});

describe("useLayout", () => {
  it.each<[number, Layout]>([
    [390, "phone"],
    [1023, "phone"],
    [1024, "desk2"],
    [1279, "desk2"],
    [1280, "desk3"],
    [1920, "desk3"],
  ])("reads %ipx as %s", (width, want) => {
    setViewportWidth(width);
    const { result } = renderHook(() => useLayout());
    expect(result.current).toBe(want);
  });

  it("follows the window as it resizes, in both directions", () => {
    const { result } = renderHook(() => useLayout());
    expect(result.current).toBe("phone");
    setViewportWidth(1100);
    expect(result.current).toBe("desk2");
    setViewportWidth(1300);
    expect(result.current).toBe("desk3");
    setViewportWidth(1000);
    expect(result.current).toBe("phone");
  });

  it("stops listening once it unmounts", () => {
    const before = viewportWatchers();
    const { unmount } = renderHook(() => useLayout());
    expect(viewportWatchers()).toBeGreaterThan(before);
    unmount();
    expect(viewportWatchers()).toBe(before);
  });

  it("reads a matchMedia that cannot listen, and throws nothing", () => {
    for (const [matches, want] of [
      [false, "phone"],
      [true, "desk3"],
    ] as const) {
      const spy = vi
        .spyOn(window, "matchMedia")
        .mockImplementation((query: string) => ({ matches, media: query }) as MediaQueryList);
      try {
        const { result, unmount } = renderHook(() => useLayout());
        expect(result.current).toBe(want);
        unmount();
      } finally {
        spy.mockRestore();
      }
    }
  });

  it("is the phone where there is no matchMedia", () => {
    const original = Object.getOwnPropertyDescriptor(window, "matchMedia");
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: undefined,
    });
    try {
      const { result } = renderHook(() => useLayout());
      expect(result.current).toBe("phone");
    } finally {
      if (original !== undefined) Object.defineProperty(window, "matchMedia", original);
    }
  });
});

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { askConfirm, testSeam } from "@/app/ConfirmSheet";

import { isPaletteChord, paletteKeyLabel, usePalette, usePaletteKey } from "./paletteState";

function chord(over: KeyboardEventInit = {}): KeyboardEvent {
  return new KeyboardEvent("keydown", {
    key: "k",
    ctrlKey: true,
    bubbles: true,
    cancelable: true,
    ...over,
  });
}

// happy-dom may ignore the init's isComposing, so the property is set here.
function withComposing(event: KeyboardEvent): KeyboardEvent {
  Object.defineProperty(event, "isComposing", { value: true });
  return event;
}

function press(target: EventTarget, event: KeyboardEvent): KeyboardEvent {
  act(() => {
    target.dispatchEvent(event);
  });
  return event;
}

const confirm = { title: "Kill", body: "b", action: "Kill", destructive: true };

beforeEach(() => {
  act(() => usePalette.getState().setOpen(false));
});

afterEach(() => {
  const id = testSeam.openId();
  if (id !== null) act(() => testSeam.answer(id, null));
  vi.restoreAllMocks();
});

describe("usePalette", () => {
  it("starts closed at load", async () => {
    vi.resetModules();
    expect((await import("./paletteState")).usePalette.getState().open).toBe(false);
  });

  it("setOpen and toggle", () => {
    const s = usePalette.getState();
    act(() => s.setOpen(true));
    expect(usePalette.getState().open).toBe(true);
    act(() => usePalette.getState().toggle());
    expect(usePalette.getState().open).toBe(false);
    act(() => usePalette.getState().toggle());
    expect(usePalette.getState().open).toBe(true);
    act(() => usePalette.getState().setOpen(false));
    expect(usePalette.getState().open).toBe(false);
  });
});

describe("isPaletteChord", () => {
  it.each([
    ["Ctrl+k", { key: "k", ctrlKey: true }],
    ["Meta+k", { key: "k", metaKey: true, ctrlKey: false }],
    ["Ctrl+K (caps lock)", { key: "K", ctrlKey: true }],
    ["Meta+K (caps lock)", { key: "K", metaKey: true, ctrlKey: false }],
  ])("accepts %s", (_name, init) => {
    expect(isPaletteChord(chord(init))).toBe(true);
  });

  it.each([
    ["k with no modifier", chord({ ctrlKey: false })],
    ["Ctrl+Alt+K", chord({ altKey: true })],
    ["Meta+Alt+K", chord({ ctrlKey: false, metaKey: true, altKey: true })],
    ["Ctrl+Shift+K", chord({ shiftKey: true })],
    ["Meta+Shift+K", chord({ ctrlKey: false, metaKey: true, shiftKey: true })],
    ["Ctrl+J", chord({ key: "j" })],
    ["Ctrl+Enter", chord({ key: "Enter" })],
    ["Ctrl+K while composing", withComposing(chord())],
  ])("refuses %s", (_name, event) => {
    expect(isPaletteChord(event)).toBe(false);
  });
});

describe("paletteKeyLabel", () => {
  it.each(["MacIntel", "Macintosh", "iPhone", "iPad"])("gives ⌘K for %s", (platform) => {
    expect(paletteKeyLabel(platform)).toBe("⌘K");
  });

  it.each(["Linux x86_64", "Win32", ""])("gives Ctrl K for %j", (platform) => {
    expect(paletteKeyLabel(platform)).toBe("Ctrl K");
  });
});

describe("usePaletteKey", () => {
  it("toggles the palette on the chord from a text field", () => {
    renderHook(() => usePaletteKey());
    const input = document.createElement("input");
    document.body.appendChild(input);
    input.focus();

    const first = press(input, chord());
    expect(usePalette.getState().open).toBe(true);
    expect(first.defaultPrevented).toBe(true);

    press(input, chord());
    expect(usePalette.getState().open).toBe(false);

    press(document.body, chord({ ctrlKey: false, metaKey: true }));
    expect(usePalette.getState().open).toBe(true);
    input.remove();
  });

  it.each([
    ["Alt", chord({ altKey: true })],
    ["Shift", chord({ shiftKey: true })],
    ["composing", withComposing(chord())],
    ["a bare k", chord({ ctrlKey: false })],
  ])("refuses what is not the chord and leaves the key alone: %s", (_name, event) => {
    renderHook(() => usePaletteKey());
    press(document.body, event);
    expect(usePalette.getState().open).toBe(false);
    expect(event.defaultPrevented).toBe(false);
  });

  it("does nothing while a confirmation is open, and still prevents the browser's default", () => {
    renderHook(() => usePaletteKey());
    act(() => {
      void askConfirm(confirm);
    });

    const event = press(document.body, chord());
    expect(usePalette.getState().open).toBe(false);
    expect(event.defaultPrevented).toBe(true);

    let id = 0;
    act(() => {
      id = testSeam.openId() as number;
      testSeam.answer(id, null);
    });
    press(document.body, chord());
    expect(usePalette.getState().open).toBe(true);
  });

  it("adds one keydown listener on the document and removes the same one on unmount", () => {
    const add = vi.spyOn(document, "addEventListener");
    const remove = vi.spyOn(document, "removeEventListener");
    const { rerender, unmount } = renderHook(() => usePaletteKey());
    rerender();
    rerender();
    unmount();

    const added = add.mock.calls.filter(([type]) => type === "keydown");
    const removed = remove.mock.calls.filter(([type]) => type === "keydown");
    expect(added).toHaveLength(1);
    expect(removed).toHaveLength(1);
    expect(removed[0]?.[1]).toBe(added[0]?.[1]);
  });

  it("stops listening after unmount", () => {
    const { unmount } = renderHook(() => usePaletteKey());
    unmount();
    press(document.body, chord());
    expect(usePalette.getState().open).toBe(false);
  });

  it("closes the palette when it unmounts", () => {
    const { unmount } = renderHook(() => usePaletteKey());
    act(() => usePalette.getState().setOpen(true));
    unmount();
    expect(usePalette.getState().open).toBe(false);
  });
});

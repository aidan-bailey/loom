import { describe, expect, it, vi } from "vitest";

import type { KeyName } from "./frame";
import { handleKey, keyAction, keyBytes, type KeyAction } from "./keys";

const ESC = 0x1b;

function bytes(name: KeyName, applicationCursor: boolean): number[] {
  return Array.from(keyBytes(name, applicationCursor));
}

// A key event, cancelable so a test can read whether the handler cancelled it.
function ev(init: KeyboardEventInit, type = "keydown"): KeyboardEvent {
  return new KeyboardEvent(type, { cancelable: true, ...init });
}

describe("keyBytes", () => {
  it("sends the arrows as CSI sequences in the normal cursor mode", () => {
    expect(bytes("up", false)).toEqual([ESC, 0x5b, 0x41]);
    expect(bytes("down", false)).toEqual([ESC, 0x5b, 0x42]);
    expect(bytes("right", false)).toEqual([ESC, 0x5b, 0x43]);
    expect(bytes("left", false)).toEqual([ESC, 0x5b, 0x44]);
  });

  it("sends the arrows as SS3 sequences while the program has set application cursor keys", () => {
    expect(bytes("up", true)).toEqual([ESC, 0x4f, 0x41]);
    expect(bytes("down", true)).toEqual([ESC, 0x4f, 0x42]);
    expect(bytes("right", true)).toEqual([ESC, 0x4f, 0x43]);
    expect(bytes("left", true)).toEqual([ESC, 0x4f, 0x44]);
  });

  it("sends tab, escape, Ctrl-C and Ctrl-D as one byte in either mode", () => {
    for (const mode of [false, true]) {
      expect(bytes("tab", mode)).toEqual([0x09]);
      expect(bytes("esc", mode)).toEqual([ESC]);
      expect(bytes("ctrl-c", mode)).toEqual([0x03]);
      expect(bytes("ctrl-d", mode)).toEqual([0x04]);
    }
  });
});

describe("keyAction", () => {
  const cases: [string, KeyboardEventInit, KeyAction][] = [
    ["⌘K", { key: "k", metaKey: true }, "chord"],
    ["⌘K with caps lock", { key: "K", metaKey: true }, "chord"],
    ["Ctrl+K, readline's kill-line", { key: "k", ctrlKey: true }, "shell"],
    ["⌘⇧K", { key: "k", metaKey: true, shiftKey: true }, "shell"],
    ["⌘⌃K", { key: "k", metaKey: true, ctrlKey: true }, "shell"],
    ["Ctrl+Shift+C", { key: "C", ctrlKey: true, shiftKey: true }, "copy"],
    ["Ctrl+C, the interrupt", { key: "c", ctrlKey: true }, "shell"],
    ["⌘C, the browser's copy", { key: "c", metaKey: true }, "shell"],
    ["Ctrl+Shift+Alt+C", { key: "C", ctrlKey: true, shiftKey: true, altKey: true }, "shell"],
    ["Ctrl+Shift+V", { key: "V", ctrlKey: true, shiftKey: true }, "paste"],
    ["Ctrl+V", { key: "v", ctrlKey: true }, "shell"],
    ["a plain letter", { key: "a" }, "shell"],
  ];

  it.each(cases)("%s", (_name, init, action) => {
    expect(keyAction(ev(init))).toBe(action);
  });
});

describe("handleKey", () => {
  function hooks() {
    return { chord: vi.fn(), copy: vi.fn() };
  }

  it("sends ⌘K to the chord, not to the shell, and cancels the browser's own", () => {
    const h = hooks();
    const event = ev({ key: "k", metaKey: true });
    expect(handleKey(event, h)).toBe(false);
    expect(h.chord).toHaveBeenCalledTimes(1);
    expect(h.copy).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(true);
  });

  it("does not repeat a held chord or a held copy", () => {
    const h = hooks();
    const chord = ev({ key: "k", metaKey: true, repeat: true });
    const copy = ev({ key: "C", ctrlKey: true, shiftKey: true, repeat: true });
    expect(handleKey(chord, h)).toBe(false);
    expect(handleKey(copy, h)).toBe(false);
    expect(chord.defaultPrevented).toBe(true);
    expect(copy.defaultPrevented).toBe(true);
    expect(h.chord).not.toHaveBeenCalled();
    expect(h.copy).not.toHaveBeenCalled();
  });

  it("keeps the other events of a chord from the shell without acting twice", () => {
    const h = hooks();
    for (const type of ["keypress", "keyup"]) {
      expect(handleKey(ev({ key: "k", metaKey: true }, type), h)).toBe(false);
      expect(handleKey(ev({ key: "C", ctrlKey: true, shiftKey: true }, type), h)).toBe(false);
    }
    expect(h.chord).not.toHaveBeenCalled();
    expect(h.copy).not.toHaveBeenCalled();
  });

  it("copies on Ctrl+Shift+C and neither sends it to the shell nor leaves it to the browser", () => {
    const h = hooks();
    const event = ev({ key: "C", ctrlKey: true, shiftKey: true });
    expect(handleKey(event, h)).toBe(false);
    expect(h.copy).toHaveBeenCalledTimes(1);
    expect(h.chord).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(true);
  });

  it("leaves Ctrl+Shift+V to the browser and keeps it from the shell", () => {
    const h = hooks();
    const event = ev({ key: "V", ctrlKey: true, shiftKey: true });
    expect(handleKey(event, h)).toBe(false);
    expect(event.defaultPrevented).toBe(false);
    expect(h.chord).not.toHaveBeenCalled();
    expect(h.copy).not.toHaveBeenCalled();
  });

  it("gives Ctrl+K, Ctrl+C and every other key to the shell", () => {
    const h = hooks();
    for (const init of [
      { key: "k", ctrlKey: true },
      { key: "c", ctrlKey: true },
      { key: "c", metaKey: true },
      { key: "a" },
      { key: "Enter" },
    ]) {
      const event = ev(init);
      expect(handleKey(event, h)).toBe(true);
      expect(event.defaultPrevented).toBe(false);
    }
    expect(h.chord).not.toHaveBeenCalled();
    expect(h.copy).not.toHaveBeenCalled();
  });
});

import { describe, expect, it, vi } from "vitest";

import { frameTerminal, startFailed, type LoomTerminal } from "./frame";

// A LoomTerminal of stubs.
function stub(): LoomTerminal {
  return {
    send: vi.fn(() => true),
    key: vi.fn(() => true),
    text: vi.fn(() => ""),
    setTouch: vi.fn(),
    setLink: vi.fn(),
    reconnect: vi.fn(),
    focus: vi.fn(),
    close: vi.fn(),
    onState: vi.fn(),
    onChord: vi.fn(),
  };
}

// A frame whose window is `win`: the accessor reads only `contentWindow`.
function frameWith(win: unknown): HTMLIFrameElement {
  return { contentWindow: win } as unknown as HTMLIFrameElement;
}

describe("frameTerminal", () => {
  it("reads the interface off the frame's window", () => {
    const term = stub();
    expect(frameTerminal(frameWith({ loomTerminal: term }))).toBe(term);
  });

  it("is null while the frame's script has not run", () => {
    expect(frameTerminal(frameWith({}))).toBeNull();
  });

  it("is null for a frame with no window", () => {
    expect(frameTerminal(frameWith(null))).toBeNull();
  });

  it("is null when the frame's window cannot be read", () => {
    const frame = {
      get contentWindow(): never {
        throw new DOMException("blocked", "SecurityError");
      },
    } as unknown as HTMLIFrameElement;
    expect(frameTerminal(frame)).toBeNull();
  });
});

describe("startFailed", () => {
  it("is a closed state with code 0 and the failure's text as its reason", () => {
    expect(startFailed(new Error("WebGL is gone"), "webgl")).toEqual({
      phase: "closed",
      closed: { code: 0, reason: "The terminal failed to start: WebGL is gone" },
      renderer: "webgl",
    });
  });

  it("says what it does not know when the failure has no text", () => {
    const reason = "The terminal failed to start: unknown error";
    expect(startFailed(new Error(""), "dom").closed).toEqual({ code: 0, reason });
    expect(startFailed("boom", "dom").closed).toEqual({ code: 0, reason });
  });

  it("is not waiting for a retry", () => {
    expect(startFailed(new Error("x"), "dom").retryAt).toBeUndefined();
  });
});

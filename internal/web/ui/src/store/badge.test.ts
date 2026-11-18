import { afterEach, describe, expect, it, vi } from "vitest";
import { setBadge } from "./badge";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("setBadge", () => {
  it("puts a positive count on the Home Screen icon", () => {
    const set = vi.fn().mockResolvedValue(undefined);
    const clear = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { setAppBadge: set, clearAppBadge: clear });
    setBadge(3);
    expect(set).toHaveBeenCalledOnce();
    expect(set).toHaveBeenCalledWith(3);
    expect(clear).not.toHaveBeenCalled();
  });

  it("clears the icon at zero, so answering every session clears what a push set", () => {
    const set = vi.fn().mockResolvedValue(undefined);
    const clear = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { setAppBadge: set, clearAppBadge: clear });
    setBadge(0);
    expect(clear).toHaveBeenCalledOnce();
    expect(set).not.toHaveBeenCalled();
  });

  it("does nothing, and does not throw, where the Badging API is missing", () => {
    vi.stubGlobal("navigator", {});
    expect(() => {
      setBadge(2);
    }).not.toThrow();
  });

  it("swallows a refusal from the browser", async () => {
    vi.stubGlobal("navigator", {
      setAppBadge: vi.fn().mockRejectedValue(new Error("not allowed")),
      clearAppBadge: vi.fn(),
    });
    setBadge(1);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

  it("swallows a throw from the browser", () => {
    vi.stubGlobal("navigator", {
      setAppBadge: () => {
        throw new Error("no badge for you");
      },
    });
    expect(() => {
      setBadge(1);
    }).not.toThrow();
  });
});

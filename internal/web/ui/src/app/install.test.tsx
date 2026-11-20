import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import { AddToHome, BROWSER_KEY, needsInstall } from "./install";

const IPHONE =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1";
const DESKTOP =
  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36";

interface Page {
  userAgent?: string;
  platform?: string;
  touchPoints?: number;
  standalone?: boolean;
  displayStandalone?: boolean;
}

// page sets the page's iOS-ness for one test: an iPhone tab, not installed, by default.
function page(over: Page = {}): MockInstance<typeof window.matchMedia> {
  vi.spyOn(navigator, "userAgent", "get").mockReturnValue(over.userAgent ?? IPHONE);
  vi.spyOn(navigator, "platform", "get").mockReturnValue(over.platform ?? "iPhone");
  vi.spyOn(navigator, "maxTouchPoints", "get").mockReturnValue(over.touchPoints ?? 5);
  Object.defineProperty(navigator, "standalone", {
    value: over.standalone,
    configurable: true,
  });
  return vi
    .spyOn(window, "matchMedia")
    .mockImplementation(
      (query: string) =>
        ({ matches: over.displayStandalone === true, media: query }) as MediaQueryList,
    );
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  Reflect.deleteProperty(navigator, "standalone");
  localStorage.clear();
});

describe("needsInstall", () => {
  it("needsInstall: an iPhone browser tab that has not chosen the browser needs the install", () => {
    page();
    expect(needsInstall()).toBe(true);
  });

  it("needsInstall: an iPad that reports itself as a Mac needs it too", () => {
    page({ userAgent: DESKTOP, platform: "MacIntel", touchPoints: 5 });
    expect(needsInstall()).toBe(true);
    vi.restoreAllMocks();
    page({ userAgent: DESKTOP, platform: "MacIntel", touchPoints: 0 });
    expect(needsInstall()).toBe(false);
    vi.restoreAllMocks();
    page({ userAgent: DESKTOP, platform: "Linux x86_64", touchPoints: 5 });
    expect(needsInstall()).toBe(false);
  });

  it("needsInstall: a desktop browser does not", () => {
    page({ userAgent: DESKTOP, platform: "Linux x86_64", touchPoints: 0 });
    expect(needsInstall()).toBe(false);
  });

  it("needsInstall: the installed app does not, by navigator.standalone or by display-mode", () => {
    page({ standalone: true });
    expect(needsInstall()).toBe(false);
    vi.restoreAllMocks();
    const media = page({ standalone: undefined, displayStandalone: true });
    expect(needsInstall()).toBe(false);
    expect(media).toHaveBeenCalledWith("(display-mode: standalone)");
  });

  it("needsInstall: a stored browser choice of true ends it, and nothing else does", () => {
    page();
    localStorage.setItem(BROWSER_KEY, "true");
    expect(needsInstall()).toBe(false);
    for (const stored of ["1", '"yes"', "false", "yes"]) {
      localStorage.setItem(BROWSER_KEY, stored);
      expect(needsInstall()).toBe(true);
    }
    localStorage.removeItem(BROWSER_KEY);
    expect(needsInstall()).toBe(true);
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("no storage");
    });
    expect(needsInstall()).toBe(true);
  });

  it("BROWSER_KEY: is the classic app's key", () => {
    expect(BROWSER_KEY).toBe("loom.browser");
  });
});

describe("AddToHome", () => {
  it("AddToHome: draws the classic words", () => {
    render(<AddToHome onContinue={() => undefined} />);
    expect(screen.getByRole("heading", { level: 1, name: "Loom" })).toBeTruthy();
    expect(
      screen.getByText(
        "Add Loom to your Home Screen to use it. The installed app keeps its own pairing, separate from this tab.",
      ),
    ).toBeTruthy();
    const steps = screen.getAllByRole("listitem");
    expect(steps.map((s) => s.textContent)).toEqual([
      "Tap the Share button in Safari's toolbar.",
      "Scroll down and tap Add to Home Screen.",
      "Tap Add, then open Loom from your Home Screen.",
    ]);
    expect(steps[1]?.querySelector("strong")?.textContent).toBe("Add to Home Screen");
    expect(steps[2]?.querySelector("strong")?.textContent).toBe("Add");
    expect(screen.getByText("The app then asks for a pairing code.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Continue in the browser anyway" })).toBeTruthy();
  });

  it("AddToHome: Continue stores the choice before it calls onContinue, once", () => {
    page();
    const seen: Array<string | null> = [];
    const onContinue = vi.fn(() => seen.push(localStorage.getItem("loom.browser")));
    render(<AddToHome onContinue={onContinue} />);
    fireEvent.click(screen.getByRole("button", { name: "Continue in the browser anyway" }));
    expect(onContinue).toHaveBeenCalledTimes(1);
    expect(seen).toEqual(["true"]);
    expect(needsInstall()).toBe(false);
  });

  it("AddToHome: a storage that refuses still continues", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    const onContinue = vi.fn();
    render(<AddToHome onContinue={onContinue} />);
    fireEvent.click(screen.getByRole("button", { name: "Continue in the browser anyway" }));
    expect(onContinue).toHaveBeenCalledTimes(1);
  });
});

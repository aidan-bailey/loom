import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { goToPairing, watchHash } from "@/app/routes";
import { watchNotices } from "@/app/toasts";
import { registerWorker } from "@/app/worker";
import { onUnpaired } from "@/api/request";
import { startFleet } from "@/store/fleet";

vi.mock("@/api/request", async (original) => ({
  ...(await original<typeof import("@/api/request")>()),
  onUnpaired: vi.fn(() => () => undefined),
}));
vi.mock("@/store/fleet", async (original) => ({
  ...(await original<typeof import("@/store/fleet")>()),
  startFleet: vi.fn(() => () => undefined),
}));
vi.mock("@/app/worker", async (original) => ({
  ...(await original<typeof import("@/app/worker")>()),
  registerWorker: vi.fn(),
}));
vi.mock("@/app/toasts", async (original) => ({
  ...(await original<typeof import("@/app/toasts")>()),
  watchNotices: vi.fn(() => () => undefined),
}));
vi.mock("@/app/routes", async (original) => ({
  ...(await original<typeof import("@/app/routes")>()),
  watchHash: vi.fn(() => () => undefined),
  goToPairing: vi.fn(),
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  RouterProvider: () => <p>router</p>,
}));

const IPHONE =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1";
const DESKTOP =
  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36";

let mount: HTMLDivElement;

// ios sets the page up as an iPhone browser tab, or as a desktop browser.
function ios(on: boolean): void {
  vi.spyOn(navigator, "userAgent", "get").mockReturnValue(on ? IPHONE : DESKTOP);
  vi.spyOn(navigator, "platform", "get").mockReturnValue(on ? "iPhone" : "Linux x86_64");
  vi.spyOn(navigator, "maxTouchPoints", "get").mockReturnValue(on ? 5 : 0);
  Object.defineProperty(navigator, "standalone", { value: false, configurable: true });
  vi.spyOn(window, "matchMedia").mockImplementation(
    (query: string) => ({ matches: false, media: query }) as MediaQueryList,
  );
}

// unpairedWatchers counts the onUnpaired listeners main itself added (goToPairing); other modules
// add theirs as they load.
function unpairedWatchers(): number {
  return vi.mocked(onUnpaired).mock.calls.filter(([listener]) => listener === goToPairing).length;
}

// launch draws main into a fresh #app.
async function launch(): Promise<void> {
  vi.resetModules();
  await act(async () => {
    await import("@/main");
  });
}

beforeEach(() => {
  mount = document.createElement("div");
  mount.id = "app";
  document.body.append(mount);
  localStorage.clear();
  vi.clearAllMocks();
});

afterEach(() => {
  mount.remove();
  vi.restoreAllMocks();
  Reflect.deleteProperty(navigator, "standalone");
  localStorage.clear();
});

describe("main", () => {
  it("main: a launch that needs no install registers the worker and starts the app", async () => {
    ios(false);
    await launch();
    expect(registerWorker).toHaveBeenCalledTimes(1);
    expect(startFleet).toHaveBeenCalledTimes(1);
    expect(watchHash).toHaveBeenCalledTimes(1);
    expect(watchNotices).toHaveBeenCalledTimes(1);
    expect(unpairedWatchers()).toBe(1);
    expect(screen.getByText("router")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Continue in the browser anyway" })).toBeNull();
  });

  it("main: an iOS browser tab draws only Add to Home and starts nothing", async () => {
    ios(true);
    await launch();
    expect(registerWorker).toHaveBeenCalledTimes(1);
    expect(startFleet).not.toHaveBeenCalled();
    expect(watchHash).not.toHaveBeenCalled();
    expect(watchNotices).not.toHaveBeenCalled();
    expect(unpairedWatchers()).toBe(0);
    expect(screen.getByRole("button", { name: "Continue in the browser anyway" })).toBeTruthy();
    expect(screen.queryByText("router")).toBeNull();
  });

  it("main: Continue in the browser anyway stores the choice and starts the app once", async () => {
    ios(true);
    await launch();
    expect(unpairedWatchers()).toBe(0);
    act(() => {
      fireEvent.click(screen.getByRole("button", { name: "Continue in the browser anyway" }));
    });
    expect(localStorage.getItem("loom.browser")).toBe("true");
    expect(startFleet).toHaveBeenCalledTimes(1);
    expect(watchHash).toHaveBeenCalledTimes(1);
    expect(unpairedWatchers()).toBe(1);
    expect(screen.getByText("router")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Continue in the browser anyway" })).toBeNull();
    expect(registerWorker).toHaveBeenCalledTimes(1);
  });

  it("main: an iOS browser tab that already chose the browser starts at once", async () => {
    ios(true);
    localStorage.setItem("loom.browser", "true");
    await launch();
    expect(startFleet).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Continue in the browser anyway" })).toBeNull();
    expect(screen.getByText("router")).toBeTruthy();
  });
});

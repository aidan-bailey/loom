import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// seen records, for each launch, the order main takes its steps in, the address and the count of
// cookie writes when the worker registers, and the router's own location when it is handed to
// the RouterProvider: the location the router read when routes.tsx was evaluated.
const seen = vi.hoisted(() => ({
  order: [] as string[],
  hashAtWorker: undefined as string | undefined,
  writes: [] as string[],
  writesAtWorker: undefined as number | undefined,
  routerAt: undefined as { pathname: string; search: string } | undefined,
}));

const NEXT_COOKIE = "__Host-loom-ui=next; Secure; Path=/; SameSite=Strict; Max-Age=31536000";

vi.mock("@/store/fleet", async (original) => ({
  ...(await original<typeof import("@/store/fleet")>()),
  startFleet: vi.fn(() => {
    seen.order.push("startFleet");
    return () => undefined;
  }),
}));
vi.mock("@/app/worker", async (original) => ({
  ...(await original<typeof import("@/app/worker")>()),
  registerWorker: vi.fn(() => {
    seen.order.push("registerWorker");
    seen.hashAtWorker = window.location.hash;
    seen.writesAtWorker = seen.writes.length;
  }),
}));
vi.mock("@/app/toasts", async (original) => ({
  ...(await original<typeof import("@/app/toasts")>()),
  watchNotices: vi.fn(() => () => undefined),
}));
// The pairing is pairLink.test.ts's; here only when main arms it matters.
vi.mock("@/app/pairLink", () => ({
  watchPairLink: vi.fn(() => {
    seen.order.push("watchPairLink");
    return () => undefined;
  }),
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  RouterProvider: ({
    router,
  }: {
    router: { history: { location: { pathname: string; search: string } } };
  }) => {
    const { pathname, search } = router.history.location;
    seen.routerAt = { pathname, search };
    return <p>router</p>;
  },
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

// launch draws main into a fresh #app, the modules evaluated afresh.
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
  seen.order.length = 0;
  seen.hashAtWorker = undefined;
  seen.writes.length = 0;
  seen.writesAtWorker = undefined;
  seen.routerAt = undefined;
  Object.defineProperty(document, "cookie", {
    configurable: true,
    get: () => "",
    set: (value: string) => {
      seen.writes.push(value);
    },
  });
});

afterEach(() => {
  mount.remove();
  vi.restoreAllMocks();
  Reflect.deleteProperty(navigator, "standalone");
  Reflect.deleteProperty(document, "cookie");
  localStorage.clear();
  window.history.replaceState(null, "", "/");
});

describe("main and the pairing link", () => {
  it("main: a pairing link is out of the address before the router is built, and before the worker and the stream start", async () => {
    ios(false);
    window.history.replaceState(null, "", "/?ui=next#/pair?code=K7QM-2XPA");
    await launch();
    // The hash history joins the page's own query, so ?ui=next is in its search; the code is not.
    expect(seen.routerAt).toEqual({ pathname: "/pair", search: "?ui=next" });
    expect(seen.order).toEqual(["registerWorker", "watchPairLink", "startFleet"]);
    expect(seen.hashAtWorker).toBe("#/pair");
    expect(seen.writes).toEqual([NEXT_COOKIE]);
    expect(seen.writesAtWorker).toBe(1);
    const { heldCode } = await import("@/app/pairHold");
    expect(heldCode()).toBe("K7QM2XPA");
  });

  it("main: an iOS browser tab takes the link at launch and watches it only once the app starts", async () => {
    ios(true);
    window.history.replaceState(null, "", "/#/pair?code=K7QM2XPA");
    await launch();
    expect(seen.order).toEqual(["registerWorker"]);
    expect(window.location.hash).toBe("#/pair");
    act(() => {
      fireEvent.click(screen.getByRole("button", { name: "Continue in the browser anyway" }));
    });
    expect(seen.order).toEqual(["registerWorker", "watchPairLink", "startFleet"]);
    expect(seen.routerAt).toEqual({ pathname: "/pair", search: "" });
  });

  it("main: a plain launch changes neither the address nor the app's cookie", async () => {
    ios(false);
    window.history.replaceState(null, "", "/#/s/5");
    await launch();
    expect(window.location.hash).toBe("#/s/5");
    expect(seen.writes).toEqual([]);
    expect(seen.hashAtWorker).toBe("#/s/5");
    expect(seen.routerAt).toEqual({ pathname: "/s/5", search: "" });
  });
});

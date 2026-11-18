import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cookieApp, page, switchApp } from "./switchApp";

interface Added {
  type: string;
  fn: () => void;
  opts: unknown;
}

let jar: string;
let writes: string[];
let registered: string[];
let added: Added[];
let removed: { type: string; fn: () => void }[];
let registerWith: () => Promise<unknown>;
let reloads: number;

beforeEach(() => {
  jar = "";
  writes = [];
  registered = [];
  added = [];
  removed = [];
  registerWith = () => Promise.resolve({});
  reloads = 0;
  Object.defineProperty(document, "cookie", {
    configurable: true,
    get: () => jar,
    set: (value: string) => {
      writes.push(value);
    },
  });
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      register: (url: string) => {
        registered.push(url);
        return registerWith();
      },
      addEventListener: (type: string, fn: () => void, opts: unknown) => {
        added.push({ type, fn, opts });
      },
      removeEventListener: (type: string, fn: () => void) => {
        removed.push({ type, fn });
      },
    },
  });
  vi.spyOn(page, "reload").mockImplementation(() => {
    reloads += 1;
  });
});

afterEach(() => {
  Reflect.deleteProperty(document, "cookie");
  Reflect.deleteProperty(navigator, "serviceWorker");
  vi.restoreAllMocks();
});

describe("cookieApp", () => {
  it.each([
    ["", "classic"],
    ["a=1", "classic"],
    ["__Host-loom-ui=classic", "classic"],
    ["__Host-loom-ui=next", "next"],
    ["a=1; __Host-loom-ui=next; b=2", "next"],
    ["__Host-loom-ui=nextish", "classic"],
    ["x__Host-loom-ui=next", "classic"],
  ])("reads %j as %s", (cookie, want) => {
    jar = cookie;
    expect(cookieApp()).toBe(want);
  });
});

describe("switchApp", () => {
  it("sets the cookie, registers the worker for the app and reloads when the worker takes over", async () => {
    await expect(switchApp("classic")).resolves.toBe("switched");
    expect(writes).toEqual([
      "__Host-loom-ui=classic; Secure; Path=/; SameSite=Strict; Max-Age=31536000",
    ]);
    expect(registered).toEqual(["/sw.js?ui=classic"]);
    expect(added).toHaveLength(1);
    expect(added[0].type).toBe("controllerchange");
    expect(added[0].opts).toEqual({ once: true });
    expect(reloads).toBe(0);
    added[0].fn();
    expect(reloads).toBe(1);
  });

  it("registers the new app's worker for next", async () => {
    await switchApp("next");
    expect(writes).toEqual([
      "__Host-loom-ui=next; Secure; Path=/; SameSite=Strict; Max-Age=31536000",
    ]);
    expect(registered).toEqual(["/sw.js?ui=next"]);
  });

  it("defers when the registration fails: the cookie stays, nothing reloads", async () => {
    registerWith = () => Promise.reject(new Error("offline"));
    await expect(switchApp("classic")).resolves.toBe("deferred");
    expect(writes).toHaveLength(1);
    expect(removed).toEqual([{ type: "controllerchange", fn: added[0].fn }]);
    expect(reloads).toBe(0);
  });

  it("reloads at once, registering nothing, where there is no service worker", async () => {
    Reflect.deleteProperty(navigator, "serviceWorker");
    await expect(switchApp("next")).resolves.toBe("switched");
    expect(writes).toHaveLength(1);
    expect(registered).toEqual([]);
    expect(reloads).toBe(1);
  });
});

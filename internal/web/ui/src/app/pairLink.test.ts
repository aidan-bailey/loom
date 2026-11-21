import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, emitUnpaired } from "@/api/request";
import { resetFleet, seed, spyOnAdd } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import "@/commands/device";
import pairHoldSource from "./pairHold.ts?raw";
import { adoptNextApp, heldCode, holdPairLink } from "./pairHold";
import { watchPairLink } from "./pairLink";

const NEXT_COOKIE = "__Host-loom-ui=next; Secure; Path=/; SameSite=Strict; Max-Age=31536000";
const REFUSED = "that code is not right";

const spyOnPair = () =>
  vi.spyOn(api, "pair").mockResolvedValue({ kind: "refused", status: 400, message: REFUSED });

let jar: string;
let writes: string[];
let stop: () => void;
let add: ReturnType<typeof spyOnAdd>;
let pair: ReturnType<typeof spyOnPair>;

beforeEach(() => {
  jar = "";
  writes = [];
  Object.defineProperty(document, "cookie", {
    configurable: true,
    get: () => jar,
    set: (value: string) => {
      writes.push(value);
    },
  });
  add = spyOnAdd();
  pair = spyOnPair();
  stop = () => undefined;
  resetFleet();
  window.history.replaceState(null, "", "/");
  holdPairLink();
});

afterEach(() => {
  stop();
  Reflect.deleteProperty(document, "cookie");
  vi.restoreAllMocks();
  resetFleet();
  window.history.replaceState(null, "", "/");
  holdPairLink();
});

// open puts the address as a browser opened on it, then reads it as pairHold's evaluation does.
function open(address: string): void {
  window.history.replaceState(null, "", address);
  holdPairLink();
}

describe("adoptNextApp", () => {
  it("sets the new app's cookie, with switchApp's attributes, for ?ui=next and no app cookie", () => {
    window.history.replaceState(null, "", "/?ui=next#/pair");
    adoptNextApp();
    expect(writes).toEqual([NEXT_COOKIE]);
  });

  it("sets it when other cookies are set but not the app's", () => {
    jar = "a=1; b=2";
    window.history.replaceState(null, "", "/?ui=next");
    adoptNextApp();
    expect(writes).toEqual([NEXT_COOKIE]);
  });

  it("leaves an explicit classic choice alone", () => {
    jar = "__Host-loom-ui=classic";
    window.history.replaceState(null, "", "/?ui=next");
    adoptNextApp();
    expect(writes).toEqual([]);
  });

  it("leaves a next cookie as it is", () => {
    jar = "a=1; __Host-loom-ui=next";
    window.history.replaceState(null, "", "/?ui=next");
    adoptNextApp();
    expect(writes).toEqual([]);
  });

  it.each(["/", "/?ui=classic", "/?x=ui%3Dnext", "/#/?ui=next", "/?ui=nextish"])(
    "does nothing at %s",
    (address) => {
      window.history.replaceState(null, "", address);
      adoptNextApp();
      expect(writes).toEqual([]);
    },
  );
});

describe("holdPairLink", () => {
  it.each(["#/pair?code=K7QM-2XPA", "#pair?code=k7qm2xpa&x=1"])(
    "takes the code out of %s in place, with replaceState, no history entry and the state kept",
    (hash) => {
      const state = { key: "router-state" };
      window.history.replaceState(state, "", `/${hash}`);
      const replace = vi.spyOn(window.history, "replaceState");
      const entries = window.history.length;
      holdPairLink();
      expect(replace).toHaveBeenCalledWith(state, "", "#/pair");
      expect(window.history.state).toEqual(state);
      expect(window.location.hash).toBe("#/pair");
      expect(window.history.length).toBe(entries);
    },
  );

  it.each(["#/", "#/pair", "#/pair?x=1", "#/s/5?code=K7QM2XPA", "#/pairs?code=K7QM2XPA"])(
    "leaves the address %s alone",
    (hash) => {
      window.history.replaceState(null, "", `/${hash}`);
      const replace = vi.spyOn(window.history, "replaceState");
      holdPairLink();
      expect(replace).not.toHaveBeenCalled();
      expect(window.location.hash).toBe(hash);
    },
  );

  it.each([
    ["K7QM-2XPA", true],
    ["k7qm2xpa", true],
    ["K7QM-2XP", false],
    ["", false],
    ["O0I1L", false],
  ])("holds the code %j only when it has 8 characters (%s)", (raw, held) => {
    open(`/#/pair?code=${raw}`);
    expect(window.location.hash).toBe("#/pair");
    expect(heldCode()).toBe(held ? "K7QM2XPA" : undefined);
    stop = watchPairLink();
    emitUnpaired();
    if (held) expect(pair).toHaveBeenCalledWith({ code: "K7QM2XPA" });
    else expect(pair).not.toHaveBeenCalled();
  });

  it("holds nothing from an earlier address once it is called again", () => {
    open("/#/pair?code=K7QM2XPA");
    open("/#/");
    expect(heldCode()).toBeUndefined();
    stop = watchPairLink();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });
});

describe("pairHold", () => {
  it("imports only leaves, nothing that reaches app/routes.tsx", () => {
    const specifiers = [...pairHoldSource.matchAll(/^import .* from "(.*)";$/gm)].map((m) => m[1]);
    expect(specifiers.sort()).toEqual(["./switchApp", "@/panels/pair/code"]);
  });

  it("runs adoptNextApp, then holdPairLink, when it is first evaluated", async () => {
    window.history.replaceState(null, "", "/?ui=next#/pair?code=K7QM-2XPA");
    vi.resetModules();
    const fresh = await import("./pairHold");
    expect(writes).toEqual([NEXT_COOKIE]);
    expect(window.location.hash).toBe("#/pair");
    expect(fresh.heldCode()).toBe("K7QM2XPA");
  });
});

describe("watchPairLink", () => {
  it("pairs with the held code on the first unpaired signal, never before", () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    expect(pair).not.toHaveBeenCalled();
    emitUnpaired();
    expect(pair).toHaveBeenCalledOnce();
    expect(pair).toHaveBeenCalledWith({ code: "K7QM2XPA" });
  });

  it("uses the code once: a second unpaired signal posts nothing, though the first was refused", async () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    emitUnpaired();
    await vi.waitFor(() => {
      expect(add).toHaveBeenCalled();
    });
    expect(heldCode()).toBeUndefined();
    emitUnpaired();
    emitUnpaired();
    expect(pair).toHaveBeenCalledOnce();
  });

  it("shows the daemon's words as the command's toast when the code is refused", async () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    emitUnpaired();
    await vi.waitFor(() => {
      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ description: REFUSED, type: "error" }),
      );
    });
  });

  it("posts nothing when no code was held", () => {
    open("/#/");
    stop = watchPairLink();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });

  it("drops the code unused when a live snapshot arrives first", () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    seed([]);
    expect(heldCode()).toBeUndefined();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });

  it("drops the code unused when a live snapshot is already held as watching starts", () => {
    open("/#/pair?code=K7QM2XPA");
    seed([]);
    stop = watchPairLink();
    expect(heldCode()).toBeUndefined();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });

  it("keeps the code through a stale snapshot, the saved one a cold start loads", () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: true, link: "offline" });
    emitUnpaired();
    expect(pair).toHaveBeenCalledOnce();
  });

  it("stops listening when its stop is called", () => {
    open("/#/pair?code=K7QM2XPA");
    stop = watchPairLink();
    stop();
    emitUnpaired();
    expect(pair).not.toHaveBeenCalled();
  });
});

import { beforeEach, describe, expect, it } from "vitest";
import source from "../../public/sw.js?raw";

const ORIGIN = "https://loom.example";
const SHELL = ["/", "/index.html", "/sw.js", "/manifest.webmanifest", "/assets/index-abc.js"];
const CACHE = "loom-next-b-test";

class FakeRequest {
  readonly url: string;
  readonly init: unknown;
  constructor(url: string, init?: unknown) {
    this.url = url;
    this.init = init;
  }
}

interface FakeWindow {
  focus: () => Promise<void>;
  navigate?: (url: string) => Promise<void>;
}

interface FakeEvent {
  request?: { method: string; url: string; mode: string };
  data?: { json: () => unknown } | null;
  notification?: { close: () => void; data?: { url?: string } };
  waitUntil: (work: Promise<unknown>) => void;
  respondWith: (answer: Promise<unknown>) => void;
}

interface Fired {
  answer: Promise<unknown> | undefined;
}

interface Harness {
  fire: (type: string, event?: Partial<FakeEvent>) => Promise<Fired>;
  stores: Map<string, Map<string, unknown>>;
  fetched: string[];
  failing: Set<string>;
  shown: { title: string; options: Record<string, unknown> }[];
  badges: (number | "cleared")[];
  opened: string[];
  windows: FakeWindow[];
  stats: { skipped: number; claimed: number };
}

// The worker's source compiles to a function of the four globals it uses.
const Compile = Function as unknown as new (...params: string[]) => (...args: unknown[]) => void;

// boot runs the worker's source against fakes and returns what they recorded.
function boot(): Harness {
  const handlers = new Map<string, (event: FakeEvent) => void>();
  const h: Harness = {
    fire: async (type, event = {}) => {
      const handler = handlers.get(type);
      if (!handler) throw new Error(`the worker has no ${type} listener`);
      const waits: Promise<unknown>[] = [];
      const out: Fired = { answer: undefined };
      handler({
        ...event,
        waitUntil: (work) => {
          waits.push(work);
        },
        respondWith: (answer) => {
          out.answer = answer;
        },
      });
      await Promise.all(waits);
      return out;
    },
    stores: new Map(),
    fetched: [],
    failing: new Set(),
    shown: [],
    badges: [],
    opened: [],
    windows: [],
    stats: { skipped: 0, claimed: 0 },
  };

  const cachesApi = {
    open: (name: string) => {
      const entries = h.stores.get(name) ?? new Map<string, unknown>();
      h.stores.set(name, entries);
      return Promise.resolve({
        put: (key: string, response: unknown) => {
          entries.set(key, response);
          return Promise.resolve();
        },
        match: (key: string) => Promise.resolve(entries.get(key)),
      });
    },
    keys: () => Promise.resolve([...h.stores.keys()]),
    delete: (name: string) => Promise.resolve(h.stores.delete(name)),
  };

  const fetchApi = (req: { url: string }) => {
    h.fetched.push(req.url);
    const ok = !h.failing.has(req.url);
    return Promise.resolve({ ok, status: ok ? 200 : 500, from: req.url });
  };

  const self = {
    location: { origin: ORIGIN },
    registration: {
      showNotification: (title: string, options: Record<string, unknown>) => {
        h.shown.push({ title, options });
        return Promise.resolve();
      },
    },
    clients: {
      claim: () => {
        h.stats.claimed += 1;
        return Promise.resolve();
      },
      matchAll: () => Promise.resolve(h.windows),
      openWindow: (url: string) => {
        h.opened.push(url);
        return Promise.resolve(null);
      },
    },
    navigator: {
      setAppBadge: (count: number) => {
        h.badges.push(count);
        return Promise.resolve();
      },
      clearAppBadge: () => {
        h.badges.push("cleared");
        return Promise.resolve();
      },
    },
    skipWaiting: () => {
      h.stats.skipped += 1;
      return Promise.resolve();
    },
    addEventListener: (type: string, handler: (event: FakeEvent) => void) => {
      handlers.set(type, handler);
    },
  };

  // The daemon fills these two placeholders when it serves the worker (buildStaticAssets).
  const code = source
    .replace('"__LOOM_BUILD__"', '"b-test"')
    .replace('["__LOOM_SHELL__"]', JSON.stringify(SHELL));
  const run = new Compile("self", "caches", "fetch", "Request", code);
  run(self, cachesApi, fetchApi, FakeRequest);
  return h;
}

const get = (url: string, mode = "cors") => ({
  request: { method: "GET", url: ORIGIN + url, mode },
});
const pushOf = (data: unknown) => ({ data: { json: () => data } });

let worker: Harness;

beforeEach(() => {
  worker = boot();
});

describe("install", () => {
  it("fetches each shell file as ?ui=next and stores every one under its plain path", async () => {
    await worker.fire("install");
    expect(worker.fetched).toEqual([
      "/?ui=next",
      "/index.html?ui=next",
      "/sw.js?ui=next",
      "/manifest.webmanifest?ui=next",
      "/assets/index-abc.js",
    ]);
    const cache = worker.stores.get(CACHE);
    expect([...(cache?.keys() ?? [])].sort()).toEqual([...SHELL].sort());
    expect(cache?.get("/")).toMatchObject({ from: "/?ui=next" });
    expect(worker.stats.skipped).toBe(1);
  });

  it("fails, and so does not install, when a shell file will not load", async () => {
    worker.failing.add("/index.html?ui=next");
    await expect(worker.fire("install")).rejects.toThrow("/index.html?ui=next: 500");
    expect(worker.stats.skipped).toBe(0);
  });
});

describe("activate", () => {
  it("deletes every other app's shell cache, keeps its own and others, and takes over", async () => {
    for (const name of ["loom-shell-b-old", "loom-next-b-old", CACHE, "unrelated"]) {
      worker.stores.set(name, new Map());
    }
    await worker.fire("activate");
    expect([...worker.stores.keys()].sort()).toEqual([CACHE, "unrelated"].sort());
    expect(worker.stats.claimed).toBe(1);
  });
});

describe("fetch", () => {
  it.each(["/api/v1/stream", "/api/v1/sessions/7/turns"])("never answers %s", async (path) => {
    const fired = await worker.fire("fetch", get(path));
    expect(fired.answer).toBeUndefined();
  });

  it("leaves a write and another origin's request alone", async () => {
    const post = await worker.fire("fetch", {
      request: { method: "POST", url: ORIGIN + "/assets/index-abc.js", mode: "cors" },
    });
    const foreign = await worker.fire("fetch", {
      request: {
        method: "GET",
        url: "https://elsewhere.example/assets/index-abc.js",
        mode: "cors",
      },
    });
    expect(post.answer).toBeUndefined();
    expect(foreign.answer).toBeUndefined();
  });

  it("answers a navigation with the cached shell, whatever its path", async () => {
    await worker.fire("install");
    const fired = await worker.fire("fetch", get("/s/7", "navigate"));
    await expect(fired.answer).resolves.toMatchObject({ from: "/?ui=next" });
  });

  it("answers a cached file from the cache and any other from the network", async () => {
    await worker.fire("install");
    worker.fetched.length = 0;
    const cached = await worker.fire("fetch", get("/assets/index-abc.js"));
    await expect(cached.answer).resolves.toMatchObject({ from: "/assets/index-abc.js" });
    expect(worker.fetched).toEqual([]);

    const other = await worker.fire("fetch", get("/assets/other.js"));
    await expect(other.answer).resolves.toMatchObject({ from: ORIGIN + "/assets/other.js" });
    expect(worker.fetched).toEqual([ORIGIN + "/assets/other.js"]);
  });
});

describe("push", () => {
  it("shows every push as a notification tagged by its session, and sets the badge", async () => {
    await worker.fire(
      "push",
      pushOf({
        kind: "waiting",
        session: 7,
        title: "api needs you",
        body: "Run the tests?",
        badge: 3,
        url: "/#s/7",
      }),
    );
    expect(worker.shown).toEqual([
      {
        title: "api needs you",
        options: {
          body: "Run the tests?",
          tag: "session-7",
          renotify: true,
          icon: "/icons/icon-192.png",
          data: { url: ORIGIN + "/#s/7" },
        },
      },
    ]);
    expect(worker.badges).toEqual([3]);
  });

  it("shows a push it cannot read, and leaves the badge alone", async () => {
    await worker.fire("push", {
      data: {
        json: () => {
          throw new SyntaxError("Unexpected end of JSON input");
        },
      },
    });
    await worker.fire("push", { data: null });
    await worker.fire("push", pushOf(null));
    await worker.fire("push", pushOf("not an object"));
    expect(worker.shown).toHaveLength(4);
    for (const shown of worker.shown) {
      expect(shown.title).toBe("loom");
      expect(shown.options).toMatchObject({
        body: "Open loom to see what changed.",
        tag: "loom",
        data: { url: ORIGIN + "/" },
      });
    }
    expect(worker.badges).toEqual([]);
  });

  it("words a waiting push with no title", async () => {
    await worker.fire("push", pushOf({ kind: "waiting", session: 2 }));
    expect(worker.shown[0]?.title).toBe("A session needs you");
  });

  it("clears the badge at 0, and leaves it alone for no count or a bad one", async () => {
    await worker.fire("push", pushOf({ session: 1, badge: 0 }));
    expect(worker.badges).toEqual(["cleared"]);
    await worker.fire("push", pushOf({ session: 1 }));
    await worker.fire("push", pushOf({ session: 1, badge: -1 }));
    await worker.fire("push", pushOf({ session: 1, badge: 1.5 }));
    expect(worker.badges).toEqual(["cleared"]);
  });

  it("opens the front page for a url on another origin", async () => {
    await worker.fire("push", pushOf({ session: 1, url: "https://evil.example/#s/1" }));
    expect(worker.shown[0]?.options).toMatchObject({ data: { url: ORIGIN + "/" } });
  });
});

describe("notificationclick", () => {
  const clicked = (url?: string) => {
    const closed = { count: 0 };
    return {
      closed,
      event: {
        notification: {
          close: () => {
            closed.count += 1;
          },
          data: url === undefined ? undefined : { url },
        },
      },
    };
  };

  it("closes the notification and opens a window on its url when none is open", async () => {
    const { closed, event } = clicked(ORIGIN + "/#s/7");
    await worker.fire("notificationclick", event);
    expect(closed.count).toBe(1);
    expect(worker.opened).toEqual([ORIGIN + "/#s/7"]);
  });

  it("brings an open window to the page instead of opening another", async () => {
    const navigated: string[] = [];
    worker.windows.push({
      focus: () => Promise.resolve(),
      navigate: (url) => {
        navigated.push(url);
        return Promise.resolve();
      },
    });
    await worker.fire("notificationclick", clicked(ORIGIN + "/#s/7").event);
    expect(navigated).toEqual([ORIGIN + "/#s/7"]);
    expect(worker.opened).toEqual([]);
  });

  it("tries the next window, then opens one, when a window will not take the page", async () => {
    worker.windows.push({ focus: () => Promise.reject(new Error("gone")) });
    await worker.fire("notificationclick", clicked(ORIGIN + "/#s/7").event);
    expect(worker.opened).toEqual([ORIGIN + "/#s/7"]);
  });

  it("opens the front page for a notification that carries no url", async () => {
    await worker.fire("notificationclick", clicked().event);
    expect(worker.opened).toEqual([ORIGIN + "/"]);
  });
});

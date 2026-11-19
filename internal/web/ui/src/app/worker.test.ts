import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Device } from "@/api/gen";
import { checkPushSubscription, registerWorker, shellBuild, upgradeShell } from "./worker";

const m = vi.hoisted(() => ({
  cookieApp: vi.fn(),
  reload: vi.fn(),
  register: vi.fn(),
  getRegistration: vi.fn(),
  postPush: vi.fn(),
  deletePush: vi.fn(),
  showMessage: vi.fn(),
}));

vi.mock("./switchApp", () => ({ cookieApp: m.cookieApp, page: { reload: m.reload } }));
vi.mock("@/api/request", () => ({ api: { postPush: m.postPush, deletePush: m.deletePush } }));
vi.mock("./toasts", () => ({ showMessage: m.showMessage }));

let listeners: { type: string; fn: () => void }[];

interface WorkersFake {
  controller?: object | null;
  ready?: Promise<unknown>;
}

function installWorkers(parts: WorkersFake = {}): void {
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      controller: parts.controller ?? null,
      ready: parts.ready ?? new Promise(() => undefined),
      register: m.register,
      getRegistration: m.getRegistration,
      addEventListener: (type: string, fn: () => void) => {
        listeners.push({ type, fn });
      },
    },
  });
}

const settle = () => vi.advanceTimersByTimeAsync(0);

beforeEach(() => {
  vi.resetAllMocks();
  vi.useFakeTimers();
  listeners = [];
  m.cookieApp.mockReturnValue("next");
  m.register.mockResolvedValue({});
  installWorkers();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, "serviceWorker");
  sessionStorage.clear();
  document.head.replaceChildren();
});

describe("registerWorker", () => {
  it.each(["classic", "next"])("registers the worker for the cookie's app (%s)", (app) => {
    m.cookieApp.mockReturnValue(app);
    registerWorker();
    expect(m.register).toHaveBeenCalledTimes(1);
    expect(m.register).toHaveBeenCalledWith(`/sw.js?ui=${app}`);
  });

  it("reloads when a new worker takes over a page that was already controlled", () => {
    installWorkers({ controller: {} });
    registerWorker();
    expect(listeners.map((l) => l.type)).toEqual(["controllerchange"]);
    listeners[0]?.fn();
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("does not reload when the first worker takes over a page that had none", () => {
    registerWorker();
    listeners[0]?.fn();
    expect(m.reload).not.toHaveBeenCalled();
  });

  it("does nothing where there is no service worker", () => {
    Reflect.deleteProperty(navigator, "serviceWorker");
    expect(() => {
      registerWorker();
    }).not.toThrow();
    expect(m.register).not.toHaveBeenCalled();
  });

  it("leaves a failed registration unremarked: the next launch tries again", async () => {
    m.register.mockRejectedValue(new Error("offline"));
    registerWorker();
    await settle();
    expect(m.reload).not.toHaveBeenCalled();
  });
});

describe("shellBuild", () => {
  it("is the loom-build meta tag's content", () => {
    const meta = document.createElement("meta");
    meta.name = "loom-build";
    meta.content = "b-9";
    document.head.append(meta);
    expect(shellBuild()).toBe("b-9");
  });

  it("is empty while the tag still holds the unfilled placeholder", () => {
    const meta = document.createElement("meta");
    meta.name = "loom-build";
    meta.content = "__LOOM_BUILD__";
    document.head.append(meta);
    expect(shellBuild()).toBe("");
  });

  it("is empty when the page has no such tag", () => {
    expect(shellBuild()).toBe("");
  });
});

describe("upgradeShell", () => {
  it("asks the registration to update, and reloads if nothing took over within 5 s", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    m.getRegistration.mockResolvedValue({ update });
    upgradeShell("b-2");
    await settle();
    expect(update).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(4999);
    expect(m.reload).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("still reloads after 5 s when the update itself fails", async () => {
    m.getRegistration.mockResolvedValue({ update: () => Promise.reject(new Error("offline")) });
    upgradeShell("b-2");
    await vi.advanceTimersByTimeAsync(5000);
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("asks once per build, and again for the next one", async () => {
    m.getRegistration.mockResolvedValue({ update: () => Promise.resolve() });
    expect(upgradeShell("b-2")).toBe(true);
    expect(upgradeShell("b-2")).toBe(false);
    await settle();
    expect(m.getRegistration).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem("loom-next.upgrade")).toBe("b-2");
    upgradeShell("b-3");
    await settle();
    expect(m.getRegistration).toHaveBeenCalledTimes(2);
    expect(sessionStorage.getItem("loom-next.upgrade")).toBe("b-3");
  });

  it("does nothing for a build this tab already asked about", async () => {
    sessionStorage.setItem("loom-next.upgrade", "b-2");
    expect(upgradeShell("b-2")).toBe(false);
    await vi.advanceTimersByTimeAsync(10000);
    expect(m.getRegistration).not.toHaveBeenCalled();
    expect(m.reload).not.toHaveBeenCalled();
  });

  it("reloads at once when there is no registration to update", async () => {
    m.getRegistration.mockResolvedValue(undefined);
    upgradeShell("b-2");
    await settle();
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("reloads when asking for the registration fails", async () => {
    m.getRegistration.mockRejectedValue(new Error("no"));
    upgradeShell("b-2");
    await settle();
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("reloads at once where there is no service worker", () => {
    Reflect.deleteProperty(navigator, "serviceWorker");
    expect(upgradeShell("b-2")).toBe(true);
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("still asks when sessionStorage is unavailable, but leaves the reload to the worker", async () => {
    vi.stubGlobal("sessionStorage", {
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("denied");
      },
    });
    const update = vi.fn().mockResolvedValue(undefined);
    m.getRegistration.mockResolvedValue({ update });
    expect(upgradeShell("b-2")).toBe(false);
    await settle();
    expect(m.getRegistration).toHaveBeenCalledTimes(1);
    expect(update).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(10000);
    expect(m.reload).not.toHaveBeenCalled();
  });
});

const deviceOf = (push: string): Device => ({
  name: "iphone",
  notify: { waiting: true, finished: true, paused_until: "" },
  push,
});

const SUBSCRIPTION_JSON = {
  endpoint: "https://web.push.apple.com/abc",
  expirationTime: null,
  keys: { p256dh: "pk", auth: "au" },
};

// phone gives the page a worker that is ready, a notification permission and push support. held is
// the subscription the phone holds (its toJSON), or null for none.
function phone(held: object | null, permission: NotificationPermission = "granted") {
  const getSubscription = vi.fn().mockResolvedValue(held === null ? null : { toJSON: () => held });
  installWorkers({ ready: Promise.resolve({ pushManager: { getSubscription } }) });
  vi.stubGlobal("Notification", { permission });
  vi.stubGlobal("PushManager", class {});
  return getSubscription;
}

const dropped = { kind: "ok", status: 204, value: undefined, notices: [] };

describe("checkPushSubscription", () => {
  beforeEach(() => {
    m.postPush.mockResolvedValue({ kind: "ok", status: 200, value: deviceOf("ok"), notices: [] });
    m.deletePush.mockResolvedValue(dropped);
  });

  it.each<[string, string, NotificationPermission, boolean, "post" | "drop" | "none"]>([
    ["posts the subscription again while loom's state is ok", "ok", "granted", true, "post"],
    ["posts it again while loom holds none", "", "granted", true, "post"],
    ["leaves an expired state for the button to replace", "expired", "granted", true, "none"],
    ["leaves a failing state for the button to replace", "failing: 410", "granted", true, "none"],
    ["has loom drop an ok subscription the phone has lost", "ok", "granted", false, "drop"],
    [
      "has loom drop a failing subscription the phone has lost",
      "failing: 500",
      "granted",
      false,
      "drop",
    ],
    [
      "has loom drop its subscription when the phone may no longer notify",
      "ok",
      "denied",
      true,
      "drop",
    ],
    ["does nothing when neither side holds one", "", "granted", false, "none"],
    ["does nothing for an expired state the phone has lost", "expired", "granted", false, "none"],
  ])("%s", async (_name, state, permission, held, want) => {
    phone(held ? SUBSCRIPTION_JSON : null, permission);
    await checkPushSubscription(deviceOf(state));
    expect(m.postPush).toHaveBeenCalledTimes(want === "post" ? 1 : 0);
    expect(m.deletePush).toHaveBeenCalledTimes(want === "drop" ? 1 : 0);
    expect(m.showMessage).toHaveBeenCalledTimes(want === "drop" ? 1 : 0);
  });

  it("posts the phone's subscription in the shape the daemon reads", async () => {
    phone(SUBSCRIPTION_JSON);
    await checkPushSubscription(deviceOf("ok"));
    expect(m.postPush).toHaveBeenCalledWith({
      endpoint: "https://web.push.apple.com/abc",
      expirationTime: null,
      keys: { p256dh: "pk", auth: "au" },
    });
  });

  it("tells the person when loom drops a subscription", async () => {
    phone(null);
    await checkPushSubscription(deviceOf("ok"));
    expect(m.showMessage).toHaveBeenCalledWith(
      "Notifications stopped on this phone. Turn them on again in Settings.",
      true,
    );
  });

  it("says nothing when loom could not drop its subscription", async () => {
    m.deletePush.mockResolvedValue({ kind: "failed", status: 0, message: "" });
    phone(null);
    await checkPushSubscription(deviceOf("ok"));
    expect(m.deletePush).toHaveBeenCalledTimes(1);
    expect(m.showMessage).not.toHaveBeenCalled();
  });

  it("posts nothing for a subscription that lacks its keys", async () => {
    phone({ endpoint: "https://web.push.apple.com/abc", expirationTime: null });
    await checkPushSubscription(deviceOf("ok"));
    expect(m.postPush).not.toHaveBeenCalled();
    expect(m.deletePush).not.toHaveBeenCalled();
  });

  it("does nothing where the browser has no push", async () => {
    const getSubscription = phone(null);
    vi.stubGlobal("PushManager", undefined);
    await checkPushSubscription(deviceOf("ok"));
    expect(getSubscription).not.toHaveBeenCalled();
    expect(m.deletePush).not.toHaveBeenCalled();

    vi.stubGlobal("PushManager", class {});
    vi.stubGlobal("Notification", undefined);
    await checkPushSubscription(deviceOf("ok"));
    expect(getSubscription).not.toHaveBeenCalled();

    Reflect.deleteProperty(navigator, "serviceWorker");
    vi.stubGlobal("Notification", { permission: "granted" });
    await checkPushSubscription(deviceOf("ok"));
    expect(m.deletePush).not.toHaveBeenCalled();
  });

  it("does nothing when no worker is active within 5 s", async () => {
    vi.stubGlobal("Notification", { permission: "granted" });
    vi.stubGlobal("PushManager", class {});
    installWorkers();
    const done = checkPushSubscription(deviceOf("ok"));
    await vi.advanceTimersByTimeAsync(5000);
    await done;
    expect(m.deletePush).not.toHaveBeenCalled();
    expect(m.postPush).not.toHaveBeenCalled();
  });

  it("does nothing when the phone cannot say what it holds", async () => {
    const getSubscription = phone(null);
    getSubscription.mockRejectedValue(new Error("no push service"));
    await checkPushSubscription(deviceOf("ok"));
    expect(m.deletePush).not.toHaveBeenCalled();
    expect(m.postPush).not.toHaveBeenCalled();
  });
});

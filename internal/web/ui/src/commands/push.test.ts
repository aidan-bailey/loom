import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import { api } from "@/api/request";
import { commandById, commandEnabled, runCommand } from "@/app/commands";
import { activeRegistration, pushRunEnded, pushSupported } from "@/app/push";

import "./index";
import { command, context, deferred, spyOnAdd } from "./testkit";

// The helpers that read the page's browser are the test's to answer; `subscriptionBody` and the rest
// of the module stay real.
vi.mock("@/app/push", async (original) => ({
  ...(await original<typeof import("@/app/push")>()),
  pushSupported: vi.fn(),
  activeRegistration: vi.fn(),
  pushRunEnded: vi.fn(),
}));

// KEY is the daemon's key as unpadded base64url: the bytes 0xfb 0xff 0xbe.
const KEY = "-_--";
const KEY_BYTES = new Uint8Array([0xfb, 0xff, 0xbe]);

const SUBSCRIPTION_JSON = {
  endpoint: "https://web.push.apple.com/abc",
  expirationTime: null,
  keys: { p256dh: "pk", auth: "au" },
};

const BLOCKED =
  "Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.";

let add: ReturnType<typeof spyOnAdd>;
let requestPermission: ReturnType<typeof vi.fn<() => Promise<NotificationPermission>>>;
let oldSubscription: { unsubscribe: ReturnType<typeof vi.fn> };
let newSubscription: { toJSON: () => object; unsubscribe: ReturnType<typeof vi.fn> };
let getSubscription: ReturnType<typeof vi.fn>;
let subscribe: ReturnType<typeof vi.fn>;
let registration: ServiceWorkerRegistration;
let pushKey: MockInstance<typeof api.pushKey>;
let postPush: MockInstance<typeof api.postPush>;

const settings = () => context(undefined, { panel: "settings" });
const enable = () => runCommand(command("push.enable"), settings(), undefined);

beforeEach(() => {
  add = spyOnAdd();
  requestPermission = vi.fn<() => Promise<NotificationPermission>>().mockResolvedValue("granted");
  vi.stubGlobal("Notification", { requestPermission });
  oldSubscription = { unsubscribe: vi.fn().mockResolvedValue(true) };
  newSubscription = {
    toJSON: () => SUBSCRIPTION_JSON,
    unsubscribe: vi.fn().mockResolvedValue(true),
  };
  getSubscription = vi.fn().mockResolvedValue(oldSubscription);
  subscribe = vi.fn().mockResolvedValue(newSubscription);
  vi.mocked(pushSupported).mockReset().mockReturnValue(true);
  registration = {
    pushManager: { getSubscription, subscribe },
  } as unknown as ServiceWorkerRegistration;
  vi.mocked(pushRunEnded).mockReset().mockResolvedValue(undefined);
  vi.mocked(activeRegistration).mockReset().mockResolvedValue(registration);
  pushKey = vi
    .spyOn(api, "pushKey")
    .mockResolvedValue({ kind: "ok", status: 200, value: { key: KEY }, notices: [] });
  postPush = vi.spyOn(api, "postPush").mockResolvedValue({
    kind: "ok",
    status: 200,
    value: {
      name: "iphone",
      notify: { waiting: true, finished: true, paused_until: "" },
      push: "ok",
    },
    notices: [],
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const toasted = (description: string, type: "error" | "info") =>
  expect(add).toHaveBeenCalledWith(expect.objectContaining({ description, type }));

describe("the push.enable command's availability", () => {
  it("is registered by the commands module, on the settings route only, and off while stale", () => {
    expect(commandById("push.enable")).toBeDefined();
    expect(commandEnabled(command("push.enable"), settings())).toBe(true);
    expect(commandEnabled(command("push.enable"), context(undefined, { panel: "fleet" }))).toBe(
      false,
    );
    expect(commandEnabled(command("push.enable"), context(undefined, { panel: "turns" }))).toBe(
      false,
    );
    expect(
      commandEnabled(
        command("push.enable"),
        context(undefined, { panel: "settings", stale: true, link: "offline" }),
      ),
    ).toBe(false);
  });
});

describe("turning push on", () => {
  it("asks for the permission inside the tap, before anything is awaited", async () => {
    const gate = deferred<NotificationPermission>();
    requestPermission.mockReturnValue(gate.promise);

    const run = enable();

    // Nothing has been awaited yet: the permission is asked, and nothing after it has been.
    expect(requestPermission).toHaveBeenCalledOnce();
    expect(activeRegistration).not.toHaveBeenCalled();
    expect(pushKey).not.toHaveBeenCalled();

    gate.resolve("granted");
    await run;
    expect(pushKey).toHaveBeenCalledOnce();
  });

  it("makes its calls in order and ends with the new subscription posted", async () => {
    await enable();

    const order = [
      requestPermission,
      vi.mocked(activeRegistration),
      pushKey,
      getSubscription,
      oldSubscription.unsubscribe,
      subscribe,
      postPush,
    ].flatMap((fn) => fn.mock.invocationCallOrder.slice(0, 1));
    expect(order).toHaveLength(7);
    expect(order).toEqual([...order].sort((a, b) => a - b));
    expect(subscribe).toHaveBeenCalledWith({
      userVisibleOnly: true,
      applicationServerKey: KEY_BYTES,
    });
    expect(postPush).toHaveBeenCalledWith(SUBSCRIPTION_JSON);
    expect(newSubscription.unsubscribe).not.toHaveBeenCalled();
    toasted("Notifications are on.", "info");
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(true, registration);
  });

  it("subscribes with the bytes of an unpadded key whose length is not a multiple of 4", async () => {
    pushKey.mockResolvedValue({ kind: "ok", status: 200, value: { key: "-_8" }, notices: [] });
    await enable();
    expect(subscribe).toHaveBeenCalledWith({
      userVisibleOnly: true,
      applicationServerKey: new Uint8Array([0xfb, 0xff]),
    });
  });

  it("makes a subscription when the phone holds none, without dropping one", async () => {
    getSubscription.mockResolvedValue(null);
    await enable();
    expect(oldSubscription.unsubscribe).not.toHaveBeenCalled();
    expect(subscribe).toHaveBeenCalledOnce();
    expect(postPush).toHaveBeenCalledOnce();
  });
});

describe("a turn-on that fails", () => {
  it("says where push is not supported, asks for nothing and ends the run", async () => {
    vi.mocked(pushSupported).mockReturnValue(false);
    await enable();
    expect(requestPermission).not.toHaveBeenCalled();
    toasted(
      "Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.",
      "error",
    );
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, null);
  });

  it.each<NotificationPermission>(["denied", "default"])(
    "says the notifications are blocked when the permission comes back %s, and goes no further",
    async (permission) => {
      requestPermission.mockResolvedValue(permission);
      await enable();
      toasted(BLOCKED, "error");
      expect(activeRegistration).not.toHaveBeenCalled();
      expect(pushKey).not.toHaveBeenCalled();
      expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, null);
    },
  );

  it("says loom is not ready when no worker is active in time, and reads no key", async () => {
    vi.mocked(activeRegistration).mockResolvedValue(null);
    await enable();
    toasted("loom isn't ready for notifications yet. Reload the app and try again.", "error");
    expect(pushKey).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, null);
  });

  it("toasts the daemon's words when the key read fails, and touches no subscription", async () => {
    pushKey.mockResolvedValue({ kind: "stopping", message: "push is not available" });
    await enable();
    toasted("push is not available", "error");
    expect(getSubscription).not.toHaveBeenCalled();
    expect(subscribe).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("says loom sent no push key for an empty key, and touches no subscription", async () => {
    pushKey.mockResolvedValue({ kind: "ok", status: 200, value: { key: "" }, notices: [] });
    await enable();
    toasted("loom sent no push key.", "error");
    expect(getSubscription).not.toHaveBeenCalled();
    expect(subscribe).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("says loom sent no push key for a key that does not decode, and drops no subscription", async () => {
    pushKey.mockResolvedValue({
      kind: "ok",
      status: 200,
      value: { key: "not a key!" },
      notices: [],
    });
    await enable();
    toasted("loom sent no push key.", "error");
    expect(getSubscription).not.toHaveBeenCalled();
    expect(oldSubscription.unsubscribe).not.toHaveBeenCalled();
    expect(subscribe).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("says the phone refused the subscription, and posts nothing", async () => {
    subscribe.mockRejectedValue(new Error("the push service is unreachable"));
    await enable();
    toasted("The phone refused the subscription: the push service is unreachable", "error");
    expect(postPush).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("says the phone refused it when dropping the old subscription throws, and makes no new one", async () => {
    oldSubscription.unsubscribe.mockRejectedValue(new Error("gone"));
    await enable();
    toasted("The phone refused the subscription: gone", "error");
    expect(subscribe).not.toHaveBeenCalled();
    expect(postPush).not.toHaveBeenCalled();
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("drops the phone's new subscription again when the daemon refuses it, with the daemon's words", async () => {
    postPush.mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "that push service is not allowed",
    });
    await enable();
    toasted("that push service is not allowed", "error");
    expect(newSubscription.unsubscribe).toHaveBeenCalledOnce();
    expect(add).not.toHaveBeenCalledWith(
      expect.objectContaining({ description: "Notifications are on." }),
    );
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("drops it again when the post cannot reach loom, and one failing to drop adds nothing", async () => {
    postPush.mockResolvedValue({ kind: "failed", status: 0, message: "Can't reach loom." });
    newSubscription.unsubscribe.mockRejectedValue(new Error("already gone"));
    await enable();
    expect(newSubscription.unsubscribe).toHaveBeenCalledOnce();
    toasted("Can't reach loom.", "error");
    expect(add).toHaveBeenCalledTimes(1);
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, registration);
  });

  it("posts nothing the daemon would not refuse for a subscription without its keys: it sends an empty body and drops the subscription", async () => {
    newSubscription.toJSON = () => ({ endpoint: "https://web.push.apple.com/abc" });
    postPush.mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "the subscription has no keys",
    });
    await enable();
    expect(postPush).toHaveBeenCalledWith({});
    toasted("the subscription has no keys", "error");
    expect(newSubscription.unsubscribe).toHaveBeenCalledOnce();
  });

  it("ends the run, and toasts the error, when a call throws", async () => {
    requestPermission.mockRejectedValue(new Error("not allowed here"));
    await enable();
    toasted("not allowed here", "error");
    expect(pushRunEnded).toHaveBeenCalledExactlyOnceWith(false, null);
  });

  it("does not end the run until the phone's new state has been read", async () => {
    const read = deferred<void>();
    vi.mocked(pushRunEnded).mockReturnValue(read.promise);
    let ended = false;
    const run = enable().then(() => {
      ended = true;
    });
    await vi.waitFor(() => {
      expect(pushRunEnded).toHaveBeenCalledOnce();
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(ended).toBe(false);

    read.resolve();
    await run;
    expect(ended).toBe(true);
  });
});

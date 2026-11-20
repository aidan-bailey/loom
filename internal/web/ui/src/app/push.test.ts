import { afterEach, describe, expect, it, vi } from "vitest";

import { activeRegistration, pushSupported, subscriptionBody } from "./push";

// installWorkers gives the page a service worker whose `ready` is the promise given.
function installWorkers(ready: Promise<unknown>): void {
  Object.defineProperty(navigator, "serviceWorker", { configurable: true, value: { ready } });
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, "serviceWorker");
});

// browser sets up a page that has push, except for the one thing named in `missing`.
function browser(missing: "serviceWorker" | "Notification" | "PushManager" | ""): void {
  if (missing !== "serviceWorker") installWorkers(new Promise(() => undefined));
  vi.stubGlobal("Notification", missing === "Notification" ? undefined : { permission: "granted" });
  vi.stubGlobal("PushManager", missing === "PushManager" ? undefined : class {});
}

describe("pushSupported", () => {
  it("is true where the page has a service worker, Notification and PushManager", () => {
    browser("");
    expect(pushSupported()).toBe(true);
  });

  it.each(["serviceWorker", "Notification", "PushManager"] as const)(
    "is false without %s",
    (missing) => {
      browser(missing);
      expect(pushSupported()).toBe(false);
    },
  );
});

describe("activeRegistration", () => {
  it("is the registration once the worker is ready", async () => {
    const registration = { pushManager: {} };
    installWorkers(Promise.resolve(registration));
    await expect(activeRegistration()).resolves.toBe(registration);
  });

  it("is null when no worker is active within 5 s, and not before", async () => {
    vi.useFakeTimers();
    installWorkers(new Promise(() => undefined));
    let answer: unknown = "waiting";
    void activeRegistration().then((registration) => {
      answer = registration;
    });
    await vi.advanceTimersByTimeAsync(4999);
    expect(answer).toBe("waiting");
    await vi.advanceTimersByTimeAsync(1);
    expect(answer).toBeNull();
  });
});

const subscriptionOf = (json: object): PushSubscription =>
  ({ toJSON: () => json }) as unknown as PushSubscription;

describe("subscriptionBody", () => {
  it("is the subscription in the shape the daemon reads", () => {
    const body = subscriptionBody(
      subscriptionOf({
        endpoint: "https://web.push.apple.com/abc",
        expirationTime: 99,
        keys: { p256dh: "pk", auth: "au" },
      }),
    );
    expect(body).toEqual({
      endpoint: "https://web.push.apple.com/abc",
      expirationTime: 99,
      keys: { p256dh: "pk", auth: "au" },
    });
  });

  it("gives a missing expirationTime as null", () => {
    const body = subscriptionBody(
      subscriptionOf({ endpoint: "https://e", keys: { p256dh: "pk", auth: "au" } }),
    );
    expect(body?.expirationTime).toBeNull();
  });

  it.each([
    ["an endpoint", { keys: { p256dh: "pk", auth: "au" } }],
    ["a p256dh key", { endpoint: "https://e", keys: { auth: "au" } }],
    ["an auth key", { endpoint: "https://e", keys: { p256dh: "pk" } }],
    ["keys", { endpoint: "https://e" }],
  ])("is null for a subscription without %s", (_name, json) => {
    expect(subscriptionBody(subscriptionOf(json))).toBeNull();
  });
});

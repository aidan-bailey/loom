import { act, renderHook, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Device } from "@/api/gen";

import {
  activeRegistration,
  pushRunEnded,
  pushSupported,
  subscriptionBody,
  usePushStatus,
  type PushStatus,
} from "./push";

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

const deviceOf = (push: string): Device => ({
  name: "iphone",
  notify: { waiting: true, finished: true, paused_until: "" },
  push,
});

// gate is a promise the test settles by hand.
function gate<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

// phone gives the page a ready worker whose push manager holds a subscription (or none), a
// notification permission and push support; it returns the `getSubscription` spy and the worker's
// registration.
function phone(held: boolean, permission: NotificationPermission = "granted") {
  const getSubscription = vi.fn().mockResolvedValue(held ? { endpoint: "https://e" } : null);
  const registration = { pushManager: { getSubscription } } as unknown as ServiceWorkerRegistration;
  installWorkers(Promise.resolve(registration));
  vi.stubGlobal("Notification", { permission });
  vi.stubGlobal("PushManager", class {});
  return { getSubscription, registration };
}

// useRecorded is usePushStatus for a device loom holds nothing for, noting each status that is
// committed (a render React throws away is not noted).
function useRecorded(statuses: PushStatus[]): PushStatus {
  const status = usePushStatus(deviceOf(""));
  useEffect(() => {
    statuses.push(status);
  });
  return status;
}

describe("usePushStatus", () => {
  it("is loading until the phone has answered, and off when no worker is active within 5 s", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("Notification", { permission: "granted" });
    vi.stubGlobal("PushManager", class {});
    installWorkers(new Promise(() => undefined));
    const { result } = renderHook(() => usePushStatus(deviceOf("")));
    expect(result.current).toBe("loading");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(result.current).toBe("off");
  });

  it("reads a push manager that cannot answer as holding no subscription", async () => {
    phone(true).getSubscription.mockRejectedValue(new Error("no push manager"));
    const { result } = renderHook(() => usePushStatus(deviceOf("ok")));
    await waitFor(() => {
      expect(result.current).toBe("off");
    });
  });

  it.each<[string, boolean, NotificationPermission, PushStatus]>([
    ["", false, "granted", "off"],
    ["", true, "granted", "off"],
    ["ok", true, "granted", "on"],
    ["ok", false, "granted", "off"],
    ["expired", true, "granted", "again"],
    ["failing: 410", false, "granted", "again"],
    ["ok", true, "denied", "denied"],
    ["expired", true, "denied", "denied"],
  ])(
    "device.push %j with a subscription held by the phone: %s, permission %s, is %s",
    async (state, held, permission, want) => {
      phone(held, permission);
      const { result } = renderHook(() => usePushStatus(deviceOf(state)));
      await waitFor(() => {
        expect(result.current).toBe(want);
      });
    },
  );

  it("is unsupported where the page has no push, whatever loom holds", async () => {
    phone(true);
    vi.stubGlobal("PushManager", undefined);
    const { result } = renderHook(() => usePushStatus(deviceOf("ok")));
    await waitFor(() => {
      expect(result.current).toBe("unsupported");
    });
  });

  it("reads the phone again when device.push changes, and not when only the object does", async () => {
    const { getSubscription } = phone(false);
    getSubscription.mockResolvedValueOnce(null).mockResolvedValue({ endpoint: "https://e" });
    const { result, rerender } = renderHook(({ device }) => usePushStatus(device), {
      initialProps: { device: deviceOf("expired") },
    });
    await waitFor(() => {
      expect(result.current).toBe("again");
    });
    expect(getSubscription).toHaveBeenCalledTimes(1);

    rerender({ device: deviceOf("expired") });
    expect(getSubscription).toHaveBeenCalledTimes(1);

    rerender({ device: deviceOf("ok") });
    await waitFor(() => {
      expect(result.current).toBe("on");
    });
    expect(getSubscription).toHaveBeenCalledTimes(2);
  });

  it("follows the newest read when two overlap, whichever settles last", async () => {
    const { getSubscription, registration } = phone(false);
    const first = gate<unknown>();
    getSubscription.mockReset().mockReturnValueOnce(first.promise).mockResolvedValue(null);
    const { result } = renderHook(() => usePushStatus(deviceOf("ok")));
    await waitFor(() => {
      expect(getSubscription).toHaveBeenCalledTimes(1);
    });

    // A turn-on that failed ends while the first read is still waiting; the phone holds none.
    await act(async () => {
      await pushRunEnded(false, registration);
    });
    await waitFor(() => {
      expect(result.current).toBe("off");
    });

    // The first read settles last, with a subscription: it is stale and changes nothing.
    await act(async () => {
      first.resolve({ endpoint: "https://e" });
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(result.current).toBe("off");
  });

  it("holds the phone's new state when a turn-on ends: on at once, with no render of off between", async () => {
    const { getSubscription, registration } = phone(false);
    const statuses: PushStatus[] = [];
    const { result } = renderHook(() => useRecorded(statuses));
    await waitFor(() => {
      expect(result.current).toBe("off");
    });

    // The turn-on made a subscription; loom's own state has not caught up.
    getSubscription.mockResolvedValue({ endpoint: "https://e" });
    const before = statuses.length;
    await act(async () => {
      await pushRunEnded(true, registration);
    });
    await waitFor(() => {
      expect(result.current).toBe("on");
    });
    expect(statuses.slice(before)).not.toContain("off");
  });

  it("reads the phone again when a turn-on ends, and says on at once while loom still says expired", async () => {
    const { getSubscription, registration } = phone(false);
    getSubscription.mockResolvedValueOnce(null).mockResolvedValue({ endpoint: "https://e" });
    const { result } = renderHook(() => usePushStatus(deviceOf("expired")));
    await waitFor(() => {
      expect(result.current).toBe("again");
    });

    await act(async () => {
      await pushRunEnded(true, registration);
    });
    await waitFor(() => {
      expect(result.current).toBe("on");
    });
    expect(getSubscription.mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it("reads again after a turn-on that failed, and does not say on", async () => {
    const { getSubscription, registration } = phone(true);
    const { result } = renderHook(() => usePushStatus(deviceOf("expired")));
    await waitFor(() => {
      expect(result.current).toBe("again");
    });
    const before = getSubscription.mock.calls.length;

    await act(async () => {
      await pushRunEnded(false, registration);
    });
    expect(getSubscription.mock.calls.length).toBeGreaterThan(before);
    expect(result.current).toBe("again");
  });

  it("reads the phone for a run that got no worker without waiting for one", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("Notification", { permission: "granted" });
    vi.stubGlobal("PushManager", class {});
    installWorkers(new Promise(() => undefined));
    let ended = false;
    void pushRunEnded(false, null).then(() => {
      ended = true;
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(ended).toBe(true);
  });

  it("forgets the turn-on's on once loom's state changes", async () => {
    const { registration } = phone(true);
    const { result, rerender } = renderHook(({ device }) => usePushStatus(device), {
      initialProps: { device: deviceOf("expired") },
    });
    await waitFor(() => {
      expect(result.current).toBe("again");
    });
    await act(async () => {
      await pushRunEnded(true, registration);
    });
    await waitFor(() => {
      expect(result.current).toBe("on");
    });

    rerender({ device: deviceOf("failing: 410") });
    await waitFor(() => {
      expect(result.current).toBe("again");
    });
  });

  it("does not count a turn-on that ended before it mounted", async () => {
    const { registration } = phone(true);
    await act(async () => {
      await pushRunEnded(true, registration);
    });
    const { result } = renderHook(() => usePushStatus(deviceOf("expired")));
    await waitFor(() => {
      expect(result.current).not.toBe("loading");
    });
    expect(result.current).toBe("again");
  });
});

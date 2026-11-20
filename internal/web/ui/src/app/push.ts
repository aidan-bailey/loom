// The phone's side of push (spec §28 and Part 3 settlement 12): whether this page can take it, the
// worker registration it needs, and the subscription in the shape the daemon reads. The launch-time
// check that uses them is in worker.ts, and the command that turns push on is commands/push.ts.
import { useEffect, useState, useSyncExternalStore } from "react";

import type { Device, SubscriptionBody } from "@/api/gen";

// READY_MS is how long to wait for an active worker before giving up.
const READY_MS = 5000;

export function pushSupported(): boolean {
  return (
    "serviceWorker" in navigator &&
    typeof Notification !== "undefined" &&
    typeof PushManager !== "undefined"
  );
}

// activeRegistration is the page's worker registration once its worker is active, or null when
// none becomes active within READY_MS.
export async function activeRegistration(): Promise<ServiceWorkerRegistration | null> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const late = new Promise<null>((resolve) => {
    timer = setTimeout(() => {
      resolve(null);
    }, READY_MS);
  });
  try {
    return await Promise.race([navigator.serviceWorker.ready, late]);
  } finally {
    clearTimeout(timer);
  }
}

// subscriptionBody is the phone's subscription in the shape POST /api/v1/device/push reads, or
// null when it lacks what the daemon would refuse it for.
export function subscriptionBody(subscription: PushSubscription): SubscriptionBody | null {
  const json = subscription.toJSON();
  const p256dh = json.keys?.p256dh;
  const auth = json.keys?.auth;
  if (!json.endpoint || !p256dh || !auth) return null;
  return {
    endpoint: json.endpoint,
    expirationTime: json.expirationTime ?? null,
    keys: { p256dh, auth },
  };
}

// PushStatus is what Settings' push control shows (classic `pushView`'s states): "loading" until
// the phone has said what it holds, "unsupported" where the page has no push, "denied" where the
// phone refuses notifications, "on", "again" where loom holds a subscription that is expired or
// failing, and "off".
export type PushStatus = "loading" | "unsupported" | "denied" | "on" | "again" | "off";

// Phone is what this phone knows: whether it can take push, the notification permission and
// whether it holds a subscription.
interface Phone {
  supported: boolean;
  permission: NotificationPermission;
  subscribed: boolean;
}

// readPhone asks the phone; a registration or subscription it cannot read is no subscription.
// `known` is the worker registration to ask, null for none; left out, it waits for the page's own.
async function readPhone(known?: ServiceWorkerRegistration | null): Promise<Phone> {
  if (!pushSupported()) return { supported: false, permission: "default", subscribed: false };
  let subscribed = false;
  try {
    const registration = known === undefined ? await activeRegistration() : known;
    if (registration !== null) {
      subscribed = (await registration.pushManager.getSubscription()) !== null;
    }
  } catch {
    subscribed = false;
  }
  return { supported: true, permission: Notification.permission, subscribed };
}

// PushRun is the last end of a turn-on: whether it worked, and what the phone held when it ended
// (null before any has ended).
interface PushRun {
  ok: boolean;
  phone: Phone | null;
}

let lastRun: PushRun = { ok: false, phone: null };
const runListeners = new Set<() => void>();

/**
 * pushRunEnded is awaited by push.enable as its run ends, whether it worked or not, with the worker
 * registration the run used (null when it got none). It reads the phone first and then tells every
 * usePushStatus, so the first render after the run already holds what the phone has now, and
 * after a run that worked says "on" until loom's own state changes, since the stream's `device`
 * event follows the run by a moment.
 */
export async function pushRunEnded(
  ok: boolean,
  registration: ServiceWorkerRegistration | null,
): Promise<void> {
  const phone = await readPhone(registration);
  lastRun = { ok, phone };
  for (const listener of [...runListeners]) listener();
}

function subscribeRuns(listener: () => void): () => void {
  runListeners.add(listener);
  return () => {
    runListeners.delete(listener);
  };
}

function currentRun(): PushRun {
  return lastRun;
}

// statusOf is classic `pushView`: loom's state for the device (`device.push` is "", "ok", "expired"
// or "failing: <status>") against what the phone knows (null while it loads) and whether the
// person just turned it on here.
function statusOf(state: string, phone: Phone | null, done: boolean): PushStatus {
  if (phone === null) return "loading";
  if (!phone.supported) return "unsupported";
  if (phone.permission === "denied") return "denied";
  if (done && phone.subscribed) return "on";
  if (state === "ok" && phone.subscribed) return "on";
  if (state === "expired" || state.startsWith("failing")) return "again";
  return "off";
}

/**
 * The state of Settings' push control: the phone's own push state (read on mount, whenever
 * `device.push` changes and whenever a push.enable run ends, the run's own read being the one the
 * render after it uses) against what loom holds for the device. A turn-on that worked reads "on"
 * until `device.push` next changes.
 */
export function usePushStatus(device: Device): PushStatus {
  const [phone, setPhone] = useState<Phone | null>(null);
  const run = useSyncExternalStore(subscribeRuns, currentRun);
  const state = device.push;

  // The "on" a turn-on gives is dropped when loom's state changes, and set with the phone's new
  // state when a run ends after this hook mounted: both adjust state during the render that sees the
  // change, so no render shows the phone's old state beside the run's end.
  const [seenState, setSeenState] = useState(state);
  const [seenRun, setSeenRun] = useState(run);
  const [done, setDone] = useState(false);
  if (seenState !== state) {
    setSeenState(state);
    setDone(false);
  }
  if (seenRun !== run) {
    setSeenRun(run);
    setDone(run.ok);
    if (run.phone !== null) setPhone(run.phone);
  }

  // A read still waiting when `state` or `run` changes is dropped, and the phone is read again.
  useEffect(() => {
    let live = true;
    void readPhone().then((read) => {
      if (live) setPhone(read);
    });
    return () => {
      live = false;
    };
  }, [state, run]);

  return statusOf(state, phone, done);
}

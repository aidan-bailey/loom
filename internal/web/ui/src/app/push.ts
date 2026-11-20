// The phone's side of push (spec §28 and Part 3 settlement 12): whether this page can take it, the
// worker registration it needs, and the subscription in the shape the daemon reads. The launch-time
// check that uses them is in worker.ts, and the command that turns push on is commands/push.ts.
import type { SubscriptionBody } from "@/api/gen";

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

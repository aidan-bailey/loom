// The app's service worker, seen from the page: registering it, asking it for a new shell, and
// the launch-time check of the phone's push subscription, which needs its registration. The worker
// itself is public/sw.js (spec §28).
import type { Device, SubscriptionBody } from "@/api/gen";
import { api } from "@/api/request";
import { cookieApp, page } from "./switchApp";
import { showMessage } from "./toasts";

const UPGRADE_KEY = "loom-next.upgrade";

// UPGRADE_RELOAD_MS is how long a page waits for the new worker to take over (which reloads it)
// before it reloads itself.
const UPGRADE_RELOAD_MS = 5000;

// READY_MS is how long the push check waits for an active worker before it gives up.
const READY_MS = 5000;

// registerWorker registers the worker at every launch, as /sw.js?ui=<the cookie's app>: the new
// app's worker while the device is on the new app, the classic one after a switch back that was
// deferred offline, so the switch completes on the next launch (spec §5). A worker that takes over
// a page that was already controlled is a new build or the other app's worker: the page reloads
// to draw from it. The first worker to take over a page that had none changes nothing the page
// has drawn.
export function registerWorker(): void {
  if (!("serviceWorker" in navigator)) return;
  const workers = navigator.serviceWorker;
  const hadController = workers.controller !== null;
  workers.addEventListener("controllerchange", () => {
    if (hadController) page.reload();
  });
  workers.register(`/sw.js?ui=${cookieApp()}`).catch(() => undefined);
}

// shellBuild is the build this page's shell was served for: the loom-build meta tag the daemon
// fills into index.html, or "" while it is unfilled.
export function shellBuild(): string {
  const meta = document.querySelector<HTMLMetaElement>('meta[name="loom-build"]');
  // An unfilled placeholder (pnpm dev serves index.html as it is) is no build.
  return meta && meta.content !== "__LOOM_BUILD__" ? meta.content : "";
}

// upgradeShell runs when the daemon's build differs from the shell's: it asks the worker for the
// new shell, which installs, takes over and reloads the page (registerWorker). Once per build per
// tab, so a shell that cannot update does not reload forever. When the worker cannot be asked, or
// does not take over within UPGRADE_RELOAD_MS, the page reloads itself, unless the ask could not be
// recorded (no sessionStorage): each reload's first snapshot would then start another timer, so
// such a page leaves the reload to the worker's takeover, and returns false so the caller goes on as
// usual. It returns whether it started a reload or a takeover: false when this tab already asked for
// the build.
export function upgradeShell(build: string): boolean {
  let remembered = true;
  try {
    if (sessionStorage.getItem(UPGRADE_KEY) === build) return false;
    sessionStorage.setItem(UPGRADE_KEY, build);
  } catch {
    // No sessionStorage: the shell is still asked, on every call.
    remembered = false;
  }
  if (!("serviceWorker" in navigator)) {
    page.reload();
    return true;
  }
  navigator.serviceWorker
    .getRegistration()
    .then((registration) => {
      if (registration === undefined) {
        page.reload();
        return;
      }
      registration.update().catch(() => undefined);
      if (!remembered) return;
      setTimeout(() => {
        page.reload();
      }, UPGRADE_RELOAD_MS);
    })
    .catch(() => {
      page.reload();
    });
  return remembered;
}

function pushSupported(): boolean {
  return (
    "serviceWorker" in navigator &&
    typeof Notification !== "undefined" &&
    typeof PushManager !== "undefined"
  );
}

// activeRegistration is the page's worker registration once its worker is active, or null when
// none becomes active within READY_MS.
async function activeRegistration(): Promise<ServiceWorkerRegistration | null> {
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
function subscriptionBody(subscription: PushSubscription): SubscriptionBody | null {
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

/**
 * The launch-time check of the phone's push subscription (once per launch, after the first
 * snapshot). Apple answers 201 to a subscription the phone has dropped, and drops the push, so
 * loom cannot learn of a lost one from the push service: the phone tells it. While loom's state
 * for the device (`push`) is "ok" or none, the phone's subscription is posted again, which changes
 * nothing when it is the same and gives loom back one it lost. When the phone holds none, or may
 * not notify, while loom holds one ("ok" or "failing: <status>"), loom drops its copy (recorded as
 * expired) and the person is told. A subscription loom knows is bad ("expired" or "failing") is
 * left for the Settings button to replace. Whatever the phone cannot answer changes nothing, a
 * failed call shows nothing, and the check never rejects.
 */
export async function checkPushSubscription(device: Device): Promise<void> {
  if (!pushSupported()) return;
  let subscription: PushSubscription | null;
  try {
    const registration = await activeRegistration();
    if (registration === null) return;
    subscription = await registration.pushManager.getSubscription();
  } catch {
    return;
  }
  const state = device.push;
  if (subscription !== null && Notification.permission === "granted") {
    if (state !== "ok" && state !== "") return;
    const body = subscriptionBody(subscription);
    if (body !== null) await api.postPush(body);
    return;
  }
  if (state !== "ok" && !state.startsWith("failing")) return;
  const outcome = await api.deletePush();
  if (outcome.kind !== "ok") return;
  showMessage("Notifications stopped on this phone. Turn them on again in Settings.", true);
}

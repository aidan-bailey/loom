// push.enable: turning notifications on from Settings, a port of the classic push.mjs's enablePush
// (spec Part 3 settlement 12). The steps run in an order iOS enforces: the permission is asked
// first, inside the tap that ran the command (runCommand reaches `run` with nothing awaited when a
// command has no sheet, and iOS asks for the permission only inside a gesture), then an active
// worker, the key, the phone's subscription made afresh, and the daemon told.
import type { PushKey } from "@/api/gen";
import { api } from "@/api/request";
import { defineCommand } from "@/app/commands";
import { activeRegistration, pushRunEnded, pushSupported, subscriptionBody } from "@/app/push";
import { showMessage, showOutcome } from "@/app/toasts";

const UNSUPPORTED =
  "Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.";
const BLOCKED =
  "Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.";
const NOT_READY = "loom isn't ready for notifications yet. Reload the app and try again.";
const WHAT = "Turning on notifications";

// keyBytes turns the unpadded base64url the daemon sends (the VAPID public key) into the bytes
// pushManager.subscribe takes (atob takes the unpadded form), or null when it is not base64url.
function keyBytes(text: string): Uint8Array<ArrayBuffer> | null {
  const b64 = text.replace(/-/g, "+").replace(/_/g, "/");
  try {
    return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
  } catch {
    return null;
  }
}

// Run is what a turn-on leaves for pushRunEnded: whether notifications are on, and the worker
// registration it used (null while it has none).
interface Run {
  ok: boolean;
  registration: ServiceWorkerRegistration | null;
}

// enable runs the steps, noting in `used` how it went. It shows every failure itself; a call that
// throws is runCommand's error toast.
async function enable(used: Run): Promise<void> {
  if (!pushSupported()) {
    showMessage(UNSUPPORTED, true);
    return;
  }
  // The first call that waits, and so the first await: the tap's gesture is still current.
  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    showMessage(BLOCKED, true);
    return;
  }
  const registration = await activeRegistration();
  if (registration === null) {
    showMessage(NOT_READY, true);
    return;
  }
  used.registration = registration;
  const key = await api.pushKey();
  if (key.kind !== "ok") {
    showOutcome(key, WHAT);
    return;
  }
  // The key is decoded before anything is dropped: a key the phone cannot use leaves the old
  // subscription as it is.
  const text: unknown = (key.value as Partial<PushKey> | undefined)?.key;
  const applicationServerKey = typeof text === "string" && text !== "" ? keyBytes(text) : null;
  if (applicationServerKey === null) {
    showMessage("loom sent no push key.", true);
    return;
  }

  let subscription: PushSubscription;
  try {
    // A fresh subscription each time: after "expired" the old one is dead.
    const old = await registration.pushManager.getSubscription();
    if (old !== null) await old.unsubscribe();
    subscription = await registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey,
    });
  } catch (err) {
    const reason = err instanceof Error && err.message !== "" ? err.message : String(err);
    showMessage(`The phone refused the subscription: ${reason}`, true);
    return;
  }

  // A subscription the daemon would refuse for what it lacks goes as an empty body, so the daemon
  // says why, as it did for classic.
  const stored = await api.postPush(subscriptionBody(subscription) ?? {});
  if (stored.kind !== "ok") {
    showOutcome(stored, WHAT);
    // The daemon never learned of it, so keep none the daemon cannot use.
    try {
      await subscription.unsubscribe();
    } catch {
      // Nothing more to undo.
    }
    return;
  }
  showMessage("Notifications are on.");
  used.ok = true;
}

defineCommand({
  id: "push.enable",
  title: "Turn on notifications",
  palette: true,
  when: (ctx) => ctx.panel === "settings",
  run: async () => {
    const used: Run = { ok: false, registration: null };
    try {
      await enable(used);
    } finally {
      // Awaited: the phone's new state is read before the run ends, so the control's first render
      // after it (when runCommand clears the in-flight key) already shows it.
      await pushRunEnded(used.ok, used.registration);
    }
  },
});

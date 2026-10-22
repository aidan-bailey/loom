// Notifications: the "Turn on notifications" button, the browser work behind
// it, and the launch's check of the phone's subscription. iOS gives web push
// only to an app installed to the Home Screen, and asks for the permission
// only inside a tap, so nothing here asks for it until the person presses the
// button. The settings screen renders PushButton with the device the stream
// sent and the app's act and toast; app.mjs's launched calls checkPush.
import { html, useEffect, useState } from "../lib/preact-htm.mjs";

// browser gathers the objects this module needs, so a check can hand in
// fakes. readyMs is how long to wait for an active service worker.
function browser() {
  const nav = globalThis.navigator;
  return {
    serviceWorker: nav ? nav.serviceWorker : undefined,
    Notification: globalThis.Notification,
    hasPush: "PushManager" in globalThis,
    readyMs: 5000,
  };
}

function pushSupported(env) {
  return Boolean(env.serviceWorker && env.Notification && env.hasPush);
}

// activeRegistration is the page's service worker registration once the
// worker is active, or null if none becomes active in time.
function activeRegistration(env) {
  let timer;
  const late = new Promise((resolve) => {
    timer = setTimeout(() => resolve(null), env.readyMs);
  });
  return Promise.race([env.serviceWorker.ready, late]).finally(() => clearTimeout(timer));
}

// keyBytes turns the unpadded base64url the daemon sends (the VAPID public
// key) into the bytes pushManager.subscribe takes.
export function keyBytes(text) {
  const b64 = text.replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4));
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}

// pushState is what this phone knows: whether it can take push at all, the
// notification permission, and whether it holds a subscription.
export async function pushState(env = browser()) {
  if (!pushSupported(env)) {
    return { supported: false, permission: "unsupported", subscribed: false };
  }
  let subscribed = false;
  try {
    const registration = await activeRegistration(env);
    subscribed = registration ? (await registration.pushManager.getSubscription()) !== null : false;
  } catch (err) {
    subscribed = false;
  }
  return { supported: true, permission: env.Notification.permission, subscribed };
}

// enablePush asks for the permission, subscribes with the daemon's VAPID key
// and hands the subscription to the daemon through act (the app's act, which
// has already shown the reason when an API call fails). It resolves
// {ok, error}: error is a sentence for the person, or "" when act showed why.
// The permission request comes before any other await, inside the tap that
// called this.
export async function enablePush(act, env = browser()) {
  if (!pushSupported(env)) {
    return {
      ok: false,
      error: "Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.",
    };
  }
  const permission = await env.Notification.requestPermission();
  if (permission !== "granted") {
    return {
      ok: false,
      error: "Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.",
    };
  }
  const registration = await activeRegistration(env);
  if (!registration) {
    return { ok: false, error: "loom isn't ready for notifications yet. Reload the app and try again." };
  }
  const key = await act("GET", "/api/v1/push-key");
  if (!key.ok) return { ok: false, error: "" };
  if (!key.data || typeof key.data.key !== "string") {
    return { ok: false, error: "loom sent no push key." };
  }

  let subscription;
  try {
    // A fresh subscription each time: after "expired" the old one is dead.
    const old = await registration.pushManager.getSubscription();
    if (old) await old.unsubscribe();
    subscription = await registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: keyBytes(key.data.key),
    });
  } catch (err) {
    return { ok: false, error: "The phone refused the subscription: " + (err && err.message ? err.message : err) };
  }

  const stored = await act("POST", "/api/v1/device/push", subscription.toJSON());
  if (!stored.ok) {
    // The daemon never learned of it, so keep none the daemon cannot use.
    try {
      await subscription.unsubscribe();
    } catch (err) {
      // Nothing more to undo.
    }
    return { ok: false, error: "" };
  }
  return { ok: true, error: "" };
}

// checkPush runs once per launch, after the first snapshot (app.mjs's
// launched). Apple answers 201 to a subscription the phone has dropped, and
// drops the push, so loom cannot learn of a lost one from the push service:
// the phone tells it. While loom's state (device.push) is "ok" or none, the
// phone's subscription is posted again, which changes nothing when it is the
// same and gives loom back one it lost. When the phone holds none, or may not
// notify, while loom holds one ("ok" or "failing: <status>"), loom drops its
// copy (DELETE /api/v1/device/push, which it records as expired) and the
// person is told. A subscription loom knows is bad ("expired" or "failing")
// is left for the button to replace. post is the app's call, which never
// throws. It resolves what it did, for the check.
export async function checkPush(device, toast, post, env = browser()) {
  if (!pushSupported(env)) return "unsupported";
  let subscription;
  try {
    const registration = await activeRegistration(env);
    if (!registration) return "unknown";
    subscription = await registration.pushManager.getSubscription();
  } catch (err) {
    return "unknown";
  }
  const state = (device && device.push) || "";
  if (subscription && env.Notification.permission === "granted") {
    if (state !== "ok" && state !== "") return "kept";
    const res = await post("POST", "/api/v1/device/push", subscription.toJSON());
    return res.status >= 200 && res.status < 300 ? "posted" : "failed";
  }
  if (state !== "ok" && !state.startsWith("failing")) return "none";
  const res = await post("DELETE", "/api/v1/device/push");
  if (res.status < 200 || res.status >= 300) return "failed";
  toast("Notifications stopped on this phone. Turn them on again in Settings.", true);
  return "dropped";
}

// pushView decides what the button area shows from the device the daemon
// has (device.push is "", "ok", "expired" or "failing: <status>"), what the
// phone knows (pushState, null while it loads) and whether the person just
// turned it on here.
export function pushView(device, local, done = false) {
  if (!local) return "loading";
  if (!local.supported) return "unsupported";
  if (local.permission === "denied") return "denied";
  if (done && local.subscribed) return "on";
  const state = (device && device.push) || "";
  if (state === "ok" && local.subscribed) return "on";
  if (state === "expired" || state.startsWith("failing")) return "again";
  return "off";
}

export function PushButton({ device, act, toast }) {
  const [local, setLocal] = useState(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const state = (device && device.push) || "";

  // Read what the phone knows when the screen opens, and again whenever the
  // daemon's idea of the subscription changes.
  useEffect(() => {
    let live = true;
    setDone(false);
    pushState().then((s) => {
      if (live) setLocal(s);
    });
    return () => {
      live = false;
    };
  }, [state]);

  async function turnOn() {
    setBusy(true);
    const result = await enablePush(act);
    if (result.error) toast(result.error, true);
    else if (result.ok) toast("Notifications are on.", false);
    setDone(result.ok);
    setLocal(await pushState());
    setBusy(false);
  }

  const view = pushView(device, local, done);
  if (view === "loading") return null;
  if (busy) {
    // subscribe() can take minutes: after an unsubscribe iOS took over two.
    return html`<div>
      <button class="sc-btn sc-primary sc-block" type="button" disabled>Turning on…</button>
      <p class="sc-small sc-dim">The phone can take a minute or two.</p>
    </div>`;
  }
  if (view === "unsupported") {
    return html`<p class="sc-small sc-dim">Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.</p>`;
  }
  if (view === "denied") {
    return html`<p class="sc-small sc-dim">Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.</p>`;
  }
  if (view === "on") {
    return html`<p class="sc-small sc-dim">Notifications are on for this phone.</p>`;
  }
  return html`<button class="sc-btn sc-primary sc-block" type="button" onClick=${turnOn}>
    ${view === "again" ? "Turn notifications on again" : "Turn on notifications"}
  </button>`;
}

// The switch between the classic app and the new one. The device's choice is
// the __Host-loom-ui cookie: "next" serves the new app's shell, anything else
// the classic one. The worker is registered as /sw.js?ui=<app>, so the choice
// reaches the worker's update whether or not its requests carry the cookie.
const COOKIE = "__Host-loom-ui";

// cookieApp is the app the cookie names: "next", else "classic".
export function cookieApp() {
  const pair = document.cookie.split("; ").find((c) => c.startsWith(COOKIE + "="));
  return pair === COOKIE + "=next" ? "next" : "classic";
}

// switchApp makes app ("next" or "classic") this device's app: it sets the
// cookie, registers the worker again at /sw.js?ui=<app> and reloads when that
// worker takes over. It answers "deferred" when the registration fails
// (offline): the cookie stays set and the next launch finishes the switch.
export async function switchApp(app) {
  document.cookie = COOKIE + "=" + app + "; Secure; Path=/; SameSite=Strict; Max-Age=31536000";
  if (!("serviceWorker" in navigator)) {
    location.reload();
    return "switched";
  }
  const reload = () => location.reload();
  navigator.serviceWorker.addEventListener("controllerchange", reload, { once: true });
  try {
    await navigator.serviceWorker.register("/sw.js?ui=" + app);
  } catch (err) {
    navigator.serviceWorker.removeEventListener("controllerchange", reload);
    return "deferred";
  }
  return "switched";
}

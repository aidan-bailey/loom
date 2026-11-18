// The switch between the new app and the classic one. The device's choice is the __Host-loom-ui
// cookie: "next" serves this app's shell, anything else the classic one. The worker is registered
// as /sw.js?ui=<app>, so the choice reaches the worker's update whether or not its requests carry
// the cookie.
export type AppName = "classic" | "next";

const COOKIE = "__Host-loom-ui";

// page is the one place the module touches the location, so a test can stub the reload.
export const page = {
  reload(): void {
    window.location.reload();
  },
};

// cookieApp is the app the cookie names: "next", else "classic".
export function cookieApp(): AppName {
  const prefix = `${COOKIE}=`;
  const pair = document.cookie.split("; ").find((c) => c.startsWith(prefix));
  return pair === `${prefix}next` ? "next" : "classic";
}

// switchApp makes app this device's app: it sets the cookie, registers the worker again at
// /sw.js?ui=<app> and reloads when that worker takes over (the other tree's worker differs in
// bytes, so it installs and takes over, the path a daemon upgrade already takes). It answers
// "deferred" when the registration fails (offline): the cookie stays set and the next launch
// registers the worker by the cookie and finishes the switch.
export async function switchApp(app: AppName): Promise<"switched" | "deferred"> {
  document.cookie = `${COOKIE}=${app}; Secure; Path=/; SameSite=Strict; Max-Age=31536000`;
  if (!("serviceWorker" in navigator)) {
    page.reload();
    return "switched";
  }
  const workers = navigator.serviceWorker;
  const reload = () => {
    page.reload();
  };
  workers.addEventListener("controllerchange", reload, { once: true });
  try {
    await workers.register(`/sw.js?ui=${app}`);
  } catch {
    workers.removeEventListener("controllerchange", reload);
    return "deferred";
  }
  return "switched";
}

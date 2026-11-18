// Loom's service worker. It caches the app shell and never the API. The
// server writes BUILD and SHELL when it serves this file: BUILD is the
// daemon's build ID, SHELL the URL of every embedded file plus "/".
const BUILD = "__LOOM_BUILD__";
const SHELL = ["__LOOM_SHELL__"];
const PREFIX = "loom-shell-";
const CACHE = PREFIX + BUILD;
// TREE is this worker's app. The server answers a shell file by its ui
// parameter before it looks at the device's cookie, so the worker asks for each
// shell file as ?ui=TREE and stores the answer under the plain path: it never
// caches the other app's shell, whatever cookie its requests carry.
const TREE = "classic";
const SHELL_FILES = ["/", "/index.html", "/sw.js", "/manifest.webmanifest"];
// Both apps' caches are swept on activate, so a switch leaves nothing of the
// other behind.
const PREFIXES = ["loom-shell-", "loom-next-"];

// precache stores every shell URL under its plain path; a shell file is fetched
// as ?ui=TREE (see TREE). Any answer but 200-299 fails the install, as addAll did.
function precache() {
  return caches.open(CACHE).then((cache) =>
    Promise.all(
      SHELL.map((url) => {
        const asked = SHELL_FILES.includes(url) ? `${url}?ui=${TREE}` : url;
        return fetch(new Request(asked, { cache: "reload" })).then((response) => {
          if (!response.ok) throw new Error(`${asked}: ${response.status}`);
          return cache.put(url, response);
        });
      }),
    ),
  );
}

self.addEventListener("install", (event) => {
  event.waitUntil(precache().then(() => self.skipWaiting()));
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys
            .filter((key) => key !== CACHE && PREFIXES.some((prefix) => key.startsWith(prefix)))
            .map((key) => caches.delete(key)),
        ),
      )
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin || url.pathname.startsWith("/api/")) return;
  const key = req.mode === "navigate" ? "/" : url.pathname;
  event.respondWith(caches.open(CACHE).then((cache) => cache.match(key).then((hit) => hit || fetch(req))));
});

// ---- Push ----
// A push is encrypted for this worker alone: the push service carries
// ciphertext. Its JSON is {kind, session, title, body, badge, url}
// (internal/web/triggers.go). iOS drops the subscription of a worker whose
// push shows nothing, so every push shows a notification, even one this code
// cannot read.
self.addEventListener("push", (event) => {
  event.waitUntil(showPush(event));
});

async function showPush(event) {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (err) {
    data = {};
  }
  if (data === null || typeof data !== "object") data = {};

  const title =
    typeof data.title === "string" && data.title
      ? data.title
      : data.kind === "waiting"
        ? "A session needs you"
        : "loom";
  const body =
    typeof data.body === "string" && data.body
      ? data.body
      : "Open loom to see what changed.";
  // One notification per session: a newer push for it replaces the older
  // one, and renotify still alerts.
  const tag = data.session ? "session-" + data.session : "loom";

  await self.registration.showNotification(title, {
    body,
    tag,
    renotify: true,
    icon: "/icons/icon-192.png",
    data: { url: pushTarget(data.url) },
  });
  await setBadge(data.badge);
}

// setBadge puts the count of sessions that need you on the Home Screen icon.
// A push without a count leaves the badge alone.
async function setBadge(count) {
  if (!Number.isInteger(count) || count < 0) return;
  if (!self.navigator || !self.navigator.setAppBadge) return;
  try {
    if (count > 0) await self.navigator.setAppBadge(count);
    else await self.navigator.clearAppBadge();
  } catch (err) {
    // The badge is a courtesy; the notification is already shown.
  }
}

// pushTarget is the page a notification opens: the push's url when it
// stays on this origin, else the app's front page.
function pushTarget(url) {
  try {
    const target = new URL(url, self.location.origin);
    if (target.origin === self.location.origin) return target.href;
  } catch (err) {
    // fall through to the front page
  }
  return new URL("/", self.location.origin).href;
}

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url =
    event.notification.data && event.notification.data.url
      ? event.notification.data.url
      : pushTarget("/");
  event.waitUntil(openApp(url));
});

// openApp brings an open window of the app to the front on the page, or
// opens one.
async function openApp(url) {
  const windows = await self.clients.matchAll({
    type: "window",
    includeUncontrolled: true,
  });
  for (const win of windows) {
    try {
      await win.focus();
      if (win.navigate) await win.navigate(url);
      return;
    } catch (err) {
      // Try the next window, then a new one.
    }
  }
  await self.clients.openWindow(url);
}

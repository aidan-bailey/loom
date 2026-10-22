// Loom's service worker. It caches the app shell and never the API. The
// server writes BUILD and SHELL when it serves this file: BUILD is the
// daemon's build ID, SHELL the URL of every embedded file plus "/".
const BUILD = "__LOOM_BUILD__";
const SHELL = ["__LOOM_SHELL__"];
const PREFIX = "loom-shell-";
const CACHE = PREFIX + BUILD;

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(SHELL.map((url) => new Request(url, { cache: "reload" }))))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(keys.filter((key) => key.startsWith(PREFIX) && key !== CACHE).map((key) => caches.delete(key))),
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

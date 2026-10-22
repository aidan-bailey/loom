// The app: routes, the live snapshot, the stale bar and the toasts. The
// screens are in screens.mjs and pairing in pair.mjs; this file owns the data
// they draw, as the props {snap, stale, route, go, act, toast}.
import { html, render, useState, useEffect, useRef, useCallback } from "../lib/preact-htm.mjs";
import { call, connect } from "./api.mjs";
import { loadSnapshot, saveSnapshot, clearSnapshot, pref } from "./store.mjs";
import { screens } from "./screens.mjs";
import { Pair, AddToHome, needsInstall } from "./pair.mjs";

const buildMeta = document.querySelector('meta[name="loom-build"]');
const shellBuild = buildMeta ? buildMeta.content : "";
const recentNotices = 20;

// parseRoute reads the location's hash: "" is the fleet, "#s/<id>" a session,
// "#new", "#settings", "#pair"; anything else is the fleet. The session's id
// is a number, as in the JSON.
export function parseRoute(hash) {
  const h = (hash || "").replace(/^#\/?/, "");
  if (h === "new") return { name: "new" };
  if (h === "settings") return { name: "settings" };
  if (h === "pair") return { name: "pair" };
  const m = h.match(/^s\/(\d+)$/);
  if (m) return { name: "session", id: Number(m[1]) };
  return { name: "fleet" };
}

let reloading = false;
function reload() {
  if (reloading) return;
  reloading = true;
  location.reload();
}

// upgradeShell runs when the daemon's build differs from the shell's: it asks
// the service worker for the new shell, which installs, takes over and
// reloads the page. Once per build, so a shell that cannot update does not
// reload forever.
function upgradeShell(build) {
  try {
    if (sessionStorage.getItem("loom.upgrade") === build) return;
    sessionStorage.setItem("loom.upgrade", build);
  } catch (err) {
    // no sessionStorage: the reload flag above still allows one reload
  }
  if (!("serviceWorker" in navigator)) {
    reload();
    return;
  }
  navigator.serviceWorker
    .getRegistration()
    .then((reg) => {
      if (!reg) {
        reload();
        return;
      }
      reg.update().catch(() => {});
      setTimeout(reload, 5000);
    })
    .catch(reload);
}

if ("serviceWorker" in navigator) {
  const hadController = Boolean(navigator.serviceWorker.controller);
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    if (hadController) reload();
  });
  navigator.serviceWorker.register("/sw.js").catch(() => {});
}

// The snapshot is saved at most once a second: sessions events come up to
// four a second.
let saveTimer = 0;
function persist(snap) {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => saveSnapshot(snap), 1000);
}

// launched runs once per launch, after the first snapshot, with the app's
// toast: the place for work that needs the device's state once a launch.
function launched(snap, toast) {}
let launchDone = false;

function App() {
  const [snap, setSnap] = useState(null);
  const [stale, setStale] = useState(true);
  const [offline, setOffline] = useState(false);
  const [route, setRoute] = useState(() => parseRoute(location.hash));
  const [toasts, setToasts] = useState([]);
  const [install, setInstall] = useState(() => needsInstall());
  const snapRef = useRef(null);
  const staleRef = useRef(true);
  const routeRef = useRef(route);
  const streamRef = useRef(null);
  const nextToast = useRef(1);
  staleRef.current = stale;
  routeRef.current = route;

  // toast shows text. An error toast, and a sticky one (a notice's, since a
  // kill's or resume's stash notice may be the only record of a SHA), stays
  // until tapped; any other fades after 4 s.
  const dismissToast = useCallback((id) => {
    setToasts((all) => all.filter((t) => t.id !== id));
  }, []);

  const toast = useCallback((text, isError, sticky) => {
    const id = nextToast.current++;
    const stays = Boolean(isError || sticky);
    setToasts((all) => all.concat([{ id, text, error: Boolean(isError) }]));
    if (!stays) setTimeout(() => dismissToast(id), 4000);
  }, []);

  const go = useCallback((hash) => {
    const h = hash.startsWith("#") ? hash : "#" + hash;
    if (location.hash === h || (h === "#" && location.hash === "")) {
      setRoute(parseRoute(h));
      return;
    }
    location.hash = h;
  }, []);

  const stopStream = useCallback(() => {
    if (streamRef.current) {
      streamRef.current.close();
      streamRef.current = null;
    }
  }, []);

  // unpair forgets everything the phone holds and shows pairing; a 401 or the
  // end of the device's own unpairing lands here.
  const unpair = useCallback(() => {
    stopStream();
    clearTimeout(saveTimer);
    snapRef.current = null;
    clearSnapshot();
    setSnap(null);
    setStale(true);
    setOffline(false);
    go("#pair");
  }, []);

  const apply = useCallback((change) => {
    if (!snapRef.current) return;
    const next = change(snapRef.current);
    snapRef.current = next;
    setSnap(next);
    persist(next);
  }, []);

  const startStream = useCallback(() => {
    stopStream();
    streamRef.current = connect({
      snapshot(data) {
        snapRef.current = data;
        setSnap(data);
        setStale(false);
        setOffline(false);
        persist(data);
        if (data.build && shellBuild && data.build !== shellBuild) upgradeShell(data.build);
        if (!launchDone) {
          launchDone = true;
          launched(data, toast);
        }
        if (routeRef.current.name === "pair") go("#");
      },
      sessions(d) {
        apply((s) => ({ ...s, sessions: { ...s.sessions, [String(d.workspace)]: d.sessions } }));
      },
      workspaces(d) {
        apply((s) => ({ ...s, workspaces: d.workspaces }));
      },
      device(d) {
        apply((s) => ({ ...s, device: d }));
      },
      notice(n) {
        apply((s) => ({ ...s, notices: (s.notices || []).concat([n]).slice(-recentNotices) }));
        toast(n.text, n.error, true);
      },
      error() {
        setStale(true);
        setOffline(true);
      },
      unauthorized() {
        unpair();
      },
    });
  }, []);

  // act calls the API for a screen with path as given: it shows the notices
  // (which stay until tapped) and the errors as toasts, treats a 401 as
  // unpairing, shows nothing for a 202 (the screen says the outcome will
  // follow), and refuses while the stream is down. It resolves
  // {ok, status, data}: ok is true for every 2xx, data is null for a 204.
  const act = useCallback(async (method, path, body) => {
    if (staleRef.current) {
      toast("Not connected. Try again when loom is back.", true);
      return { ok: false, status: 0, data: null };
    }
    const { status, data } = await call(method, path, body);
    if (status === 401) {
      unpair();
      return { ok: false, status, data };
    }
    const ok = status >= 200 && status < 300;
    if (data && Array.isArray(data.notices)) {
      for (const n of data.notices) toast(n.text, n.error, true);
    }
    if (!ok) toast((data && data.error) || "That didn't work.", true);
    if (ok && method === "DELETE" && path.endsWith("/device")) unpair();
    return { ok, status, data };
  }, []);

  useEffect(() => {
    const onHash = () => setRoute(parseRoute(location.hash));
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  useEffect(() => {
    if (install) return undefined;
    loadSnapshot().then((cached) => {
      if (cached && !snapRef.current) {
        snapRef.current = cached;
        setSnap(cached);
      }
    });
    startStream();
    return stopStream;
  }, [install]);

  const onPaired = useCallback(() => {
    startStream();
    go("#");
  }, []);

  if (install) {
    return html`<${AddToHome}
      onContinue=${() => {
        pref("browser", true);
        setInstall(false);
      }}
    />`;
  }

  let body;
  if (route.name === "pair") {
    body = html`<${Pair} onPaired=${onPaired} />`;
  } else if (!snap) {
    body = html`<div class="splash">${offline ? "Can't reach loom. Retrying…" : "Connecting…"}</div>`;
  } else {
    const Screen = screens[route.name];
    const props = { snap, stale, route, go, act, toast };
    body = Screen
      ? html`<${Screen} key=${route.name + (route.id === undefined ? "" : route.id)} ...${props} />`
      : html`<div class="splash">Nothing here yet.</div>`;
  }

  return html`
    <div class="shell">
      ${snap && stale && route.name !== "pair"
        ? html`<div class="stale-bar" role="status">Not connected. Showing the last known state.</div>`
        : null}
      <main>${body}</main>
      <div class="toasts" aria-live="polite">
        ${toasts.map(
          (t) => html`<div key=${t.id} class=${"toast" + (t.error ? " error" : "")} role=${t.error ? "alert" : "status"} onClick=${() => dismissToast(t.id)}>${t.text}</div>`,
        )}
      </div>
    </div>
  `;
}

render(html`<${App} />`, document.getElementById("app"));

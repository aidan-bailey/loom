// The app: routes, the live snapshot, the stale bar and the toasts. The
// screens are in screens.mjs and pairing in pair.mjs; this file owns the data
// they draw, as the props {snap, stale, route, go, act, toast, pending}.
import { html, render, useState, useEffect, useRef, useCallback } from "../lib/preact-htm.mjs";
import { call, connect } from "./api.mjs";
import { loadSnapshot, saveSnapshot, clearSnapshot, pref } from "./store.mjs";
import { screens } from "./screens.mjs";
import { Pair, AddToHome, needsInstall } from "./pair.mjs";
import { checkPush } from "./push.mjs";
import { allSessions } from "./fleet.mjs";

const buildMeta = document.querySelector('meta[name="loom-build"]');
const shellBuild = buildMeta ? buildMeta.content : "";
const recentNotices = 20;
// pendingFor is how long a session just created may be missing from the
// fleet: the daemon publishes the new list a moment after the create answers.
const pendingFor = 5000;

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
// lastUpdated is when the app last heard from the daemon (a snapshot or an
// event), in ms; the saved snapshot carries it as `updated`, so a cold start
// can say how old the view is.
let lastUpdated = 0;
function persist(snap) {
  clearTimeout(saveTimer);
  const updated = lastUpdated;
  saveTimer = setTimeout(() => saveSnapshot({ ...snap, updated }), 1000);
}

// setBadge puts the stream's count of sessions that need you on the Home
// Screen icon, and clears it at 0, so answering every session clears what a
// push set. The count is the daemon's (the snapshot, `sessions` and
// `workspaces` events carry it); a browser without the Badging API, or one
// that refuses, is left alone.
function setBadge(count) {
  if (typeof count !== "number") return;
  try {
    const shown = count > 0 ? navigator.setAppBadge && navigator.setAppBadge(count) : navigator.clearAppBadge && navigator.clearAppBadge();
    if (shown && shown.catch) shown.catch(() => {});
  } catch (err) {
    // no Badging API, or it threw: the icon keeps what it had
  }
}

// launched runs once per launch, after the first snapshot: the push check
// tells loom of a subscription the phone has lost (push.mjs).
function launched(snap, toast) {
  checkPush(snap.device, toast, call);
}
let launchDone = false;

// restartGrace is how long after a bye the app keeps saying "loom is
// restarting": a normal restart reconnects within it, so it never flashes
// "Can't reach"; a daemon that stays away longer is plainly unreachable.
const restartGrace = 30000;

// staleText is the stale bar's words: after a bye the daemon is restarting;
// once a connect has failed (offline) it cannot be reached, and the bar says
// since when; before that the stream is still connecting.
function staleText(snap, restarting, offline) {
  if (restarting) return "loom is restarting…";
  if (!offline || !snap.host) return "Not connected. Showing the last known state.";
  let text = "Can't reach loom on " + snap.host;
  if (lastUpdated) {
    text += ". Last updated " + new Date(lastUpdated).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  }
  return text;
}

function App() {
  const [snap, setSnap] = useState(null);
  const [stale, setStale] = useState(true);
  const [offline, setOffline] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [route, setRoute] = useState(() => parseRoute(location.hash));
  const [toasts, setToasts] = useState([]);
  const [install, setInstall] = useState(() => needsInstall());
  // pending is the id of a session whose create answered 201 but which the
  // fleet does not list yet; the session screen says "Loading…" for it. It
  // clears once the fleet lists it, or after pendingFor.
  const [pending, setPending] = useState(undefined);
  const snapRef = useRef(null);
  const staleRef = useRef(true);
  const routeRef = useRef(route);
  const streamRef = useRef(null);
  const generation = useRef(0);
  const byeAt = useRef(0);
  const nextToast = useRef(1);
  const pendingTimer = useRef(0);
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
    clearTimeout(pendingTimer.current);
    setPending(undefined);
    generation.current++;
    launchDone = false;
    lastUpdated = 0;
    snapRef.current = null;
    clearSnapshot();
    setSnap(null);
    setStale(true);
    setOffline(false);
    setRestarting(false);
    go("#pair");
  }, []);

  const apply = useCallback((change) => {
    if (!snapRef.current) return;
    const next = change(snapRef.current);
    lastUpdated = Date.now();
    snapRef.current = next;
    setSnap(next);
    persist(next);
  }, []);

  const startStream = useCallback(() => {
    stopStream();
    streamRef.current = connect({
      snapshot(data) {
        lastUpdated = Date.now();
        snapRef.current = data;
        setSnap(data);
        setStale(false);
        setOffline(false);
        setRestarting(false);
        persist(data);
        setBadge(data.badge);
        if (data.build && shellBuild && data.build !== shellBuild) upgradeShell(data.build);
        if (!launchDone) {
          launchDone = true;
          launched(data, toast);
        }
        if (routeRef.current.name === "pair") go("#");
      },
      sessions(d) {
        setBadge(d.badge);
        apply((s) => ({ ...s, sessions: { ...s.sessions, [String(d.workspace)]: d.sessions } }));
      },
      workspaces(d) {
        setBadge(d.badge);
        // A closed workspace sends no sessions event: drop its sessions here.
        // The keys are strings of numeric IDs.
        apply((s) => {
          const open = new Set(d.workspaces.map((w) => String(w.id)));
          const sessions = {};
          for (const [id, list] of Object.entries(s.sessions || {})) {
            if (open.has(id)) sessions[id] = list;
          }
          return { ...s, workspaces: d.workspaces, sessions };
        });
      },
      device(d) {
        apply((s) => ({ ...s, device: d }));
      },
      notice(n) {
        apply((s) => ({ ...s, notices: (s.notices || []).concat([n]).slice(-recentNotices) }));
        toast(n.text, n.error, true);
      },
      ping() {
        lastUpdated = Date.now();
      },
      bye() {
        byeAt.current = Date.now();
        setRestarting(true);
        setStale(true);
      },
      error() {
        if (Date.now() - byeAt.current > restartGrace) setRestarting(false);
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
    if (data && Array.isArray(data.notices) && data.notices.length > 0) {
      for (const n of data.notices) toast(n.text, n.error, true);
      // Keep them among the recent notices now: the daemon keeps them for the
      // next snapshot, which replaces this list. Not through apply: this is
      // not news from the daemon, so it does not touch lastUpdated.
      if (snapRef.current) {
        const next = {
          ...snapRef.current,
          notices: (snapRef.current.notices || []).concat(data.notices).slice(-recentNotices),
        };
        snapRef.current = next;
        setSnap(next);
        persist(next);
      }
    }
    if (ok && status === 201 && method === "POST" && path === "/api/v1/sessions" && data && data.id !== undefined) {
      clearTimeout(pendingTimer.current);
      setPending(data.id);
      pendingTimer.current = setTimeout(() => setPending(undefined), pendingFor);
    }
    if (!ok) toast((data && data.error) || "That didn't work.", true);
    if (ok && method === "DELETE" && path.endsWith("/device")) unpair();
    return { ok, status, data };
  }, []);

  // The fleet listing a pending session ends the wait.
  useEffect(() => {
    if (pending === undefined || !snap) return;
    if (allSessions(snap).some((x) => x.id === pending)) {
      clearTimeout(pendingTimer.current);
      setPending(undefined);
    }
  }, [snap, pending]);

  useEffect(() => {
    const onHash = () => setRoute(parseRoute(location.hash));
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  useEffect(() => {
    if (install) return undefined;
    const mine = generation.current;
    loadSnapshot().then((cached) => {
      // An unpairing since the load began has cleared the device's fleet.
      if (generation.current !== mine) return;
      if (cached && !snapRef.current) {
        lastUpdated = cached.updated || 0;
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
    body = html`<div class="splash">${restarting ? "loom is restarting…" : offline ? "Can't reach loom. Retrying…" : "Connecting…"}</div>`;
  } else {
    const Screen = screens[route.name];
    const props = { snap, stale, route, go, act, toast, pending };
    body = Screen
      ? html`<${Screen} key=${route.name + (route.id === undefined ? "" : route.id)} ...${props} />`
      : html`<div class="splash">Nothing here yet.</div>`;
  }

  return html`
    <div class="shell">
      ${snap && stale && route.name !== "pair"
        ? html`<div class="stale-bar" role="status">${staleText(snap, restarting, offline)}</div>`
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

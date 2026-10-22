// The API and stream client. Nothing here throws on an HTTP status.

// call sends one request to path, which is the whole path (such as
// "/api/v1/sessions/12/pause"; nothing is prepended), and resolves
// {status, data}: data is the parsed JSON body, or null when there is none (a
// 204). A network failure resolves status 0 with {error: "Can't reach loom."}.
export async function call(method, path, body) {
  const init = { method, credentials: "same-origin", headers: {} };
  if (body !== undefined) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(path, init);
  } catch (err) {
    return { status: 0, data: { error: "Can't reach loom." } };
  }
  let data = null;
  if (res.status !== 204) {
    try {
      data = await res.json();
    } catch (err) {
      data = null;
    }
  }
  return { status: res.status, data };
}

const FIRST_DELAY = 1000;
const MAX_DELAY = 30000;
// The server pings every 20 s; a stream that is silent for more than twice
// that is dead, whatever the phone's network layer believes.
const SILENCE = 45000;

// connect opens the event stream and calls handlers[event](data) for each
// event, handlers.open() once a response arrives and handlers.error() when the
// stream ends or fails. It reconnects with a backoff from 1 s to 30 s, and at
// once when the phone comes online. When the page becomes visible again it
// drops a live stream and opens a fresh one without handlers.error(): a page
// iOS resumes can sit on a stream that delivers nothing, and the fresh
// snapshot replaces the view. On a 401 it calls handlers.unauthorized() and
// stops. It returns {close()}.
export function connect(handlers) {
  let closed = false;
  let running = false;
  let live = false;
  let refreshing = false;
  let delay = FIRST_DELAY;
  let controller = null;
  let timer = 0;
  let watchdog = 0;

  function notify(name, arg) {
    const fn = handlers[name];
    if (typeof fn !== "function") return;
    try {
      fn(arg);
    } catch (err) {
      console.error("loom: handler " + name + " failed", err);
    }
  }

  function dispatch(block) {
    let event = "message";
    const data = [];
    for (const line of block.split("\n")) {
      if (line === "" || line.startsWith(":")) continue;
      const colon = line.indexOf(":");
      const field = colon < 0 ? line : line.slice(0, colon);
      let value = colon < 0 ? "" : line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "event") event = value;
      else if (field === "data") data.push(value);
    }
    if (data.length === 0) return;
    if (event === "open" || event === "error" || event === "unauthorized") return;
    let parsed;
    try {
      parsed = JSON.parse(data.join("\n"));
    } catch (err) {
      return;
    }
    notify(event, parsed);
  }

  async function session() {
    controller = new AbortController();
    const mine = controller;
    const arm = () => {
      clearTimeout(watchdog);
      watchdog = setTimeout(() => mine.abort(), SILENCE);
    };
    try {
      arm();
      const res = await fetch("/api/v1/stream", {
        credentials: "same-origin",
        cache: "no-store",
        headers: { Accept: "text/event-stream" },
        signal: mine.signal,
      });
      if (res.status === 401) {
        closed = true;
        notify("unauthorized");
        return;
      }
      if (!res.ok || !res.body) throw new Error("stream answered " + res.status);
      live = true;
      delay = FIRST_DELAY;
      notify("open");
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        arm();
        buf += decoder.decode(value, { stream: true }).replace(/\r/g, "");
        let end;
        while ((end = buf.indexOf("\n\n")) >= 0) {
          dispatch(buf.slice(0, end));
          buf = buf.slice(end + 2);
        }
      }
    } catch (err) {
      // fall through: the stream is down, the caller reconnects
    } finally {
      clearTimeout(watchdog);
      live = false;
    }
  }

  async function run() {
    if (closed || running) return;
    running = true;
    try {
      await session();
    } finally {
      running = false;
    }
    if (closed) return;
    if (refreshing) {
      // onVisible dropped it on purpose: open the next one now, quietly.
      refreshing = false;
      delay = FIRST_DELAY;
      run();
      return;
    }
    notify("error");
    clearTimeout(timer);
    timer = setTimeout(run, delay);
    delay = Math.min(delay * 2, MAX_DELAY);
  }

  function wake() {
    if (closed || running || live) return;
    clearTimeout(timer);
    delay = FIRST_DELAY;
    run();
  }

  // onVisible refreshes a live stream, which may be dead without a sign the
  // watchdog would see for up to SILENCE, and wakes one that is down.
  function onVisible() {
    if (document.visibilityState !== "visible") return;
    if (live && controller) {
      refreshing = true;
      controller.abort();
      return;
    }
    wake();
  }

  document.addEventListener("visibilitychange", onVisible);
  window.addEventListener("online", wake);
  run();

  return {
    close() {
      closed = true;
      clearTimeout(timer);
      clearTimeout(watchdog);
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("online", wake);
      if (controller) controller.abort();
    },
  };
}

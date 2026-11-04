// The mockup screen: the page the session's brainstorm companion serves now
// (GET /api/v1/sessions/{id}/mockup), in a frame sandboxed twice: by its
// attribute here and by the route's own CSP. A new screen found at a turn's
// end reloads it (its src carries the screen's time); ↻ reads it again. ⤢
// shows it alone (#s/<id>/mockup/full, with no header and no reply box).
// The frame's page can't be read from here, so a read that fails shows its
// own page saying why. It ends in the reply box, as the session's other
// views do, and marks the session seen, since a fresh mockup's row and push
// land here rather than on the session view.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { ago, markSeen, sessionOf } from "./fleet.mjs";
import { actionsFor } from "./session.mjs";
import { ReplyBox, useRun } from "./reply.mjs";

export function Mockup({ snap, stale, route, act, toast }) {
  const id = route.id;
  const s = sessionOf(snap, id);
  const { busy, run } = useRun(act, toast, id);
  const [again, setAgain] = useState(0);

  useEffect(() => {
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  if (!s) {
    return html`<section class="sc-screen"><p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p></section>`;
  }
  const mk = s.mockup;
  const base = "#s/" + s.id + "/mockup";
  const src = mk ? "/api/v1/sessions/" + s.id + "/mockup?v=" + encodeURIComponent(mk.changed) + "&r=" + again : "";
  const frame = mk
    ? html`<iframe class="sc-mockup-frame" key=${src} src=${src} sandbox="allow-scripts" referrerpolicy="no-referrer" title=${"Mockup: " + mk.screen}></iframe>`
    : html`<p class="sc-dim sc-empty">No mockup is showing; the companion may have stopped.</p>`;

  if (route.full) {
    return html`<section class="sc-mockup-full">
      ${frame}
      <a class="sc-mockup-done" href=${base}>✕ done</a>
    </section>`;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  return html`
    <section class="sc-screen sc-mockup">
      <div class="sc-mockup-strip">
        <span class="sc-mockup-name">${mk ? mk.screen + " · " + ago(mk.changed) : "no mockup"}</span>
        ${mk
          ? html`<button class="sc-mockup-act" type="button" aria-label="Read the mockup again" onClick=${() => setAgain((n) => n + 1)}>↻</button>
              <a class="sc-mockup-act" href=${base + "/full"} aria-label="Full screen">⤢</a>`
          : null}
      </div>
      ${frame}
      ${can.send
        ? html`<${ReplyBox} snap=${snap} off=${off} run=${run} id=${s.id} />`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}
    </section>
  `;
}

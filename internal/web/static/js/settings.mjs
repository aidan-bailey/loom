// The settings screen: notifications, an hour's pause, the recent notices
// and unpairing. The device state comes from the stream's snapshot and from
// each PUT's answer, which is the device as stored.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { ConfirmSheet } from "./sheets.mjs";
import { PushButton } from "./push.mjs";
import { ago } from "./fleet.mjs";

export function isPaused(device, now = Date.now()) {
  const until = Date.parse(device && device.notify ? device.notify.paused_until : "");
  return !Number.isNaN(until) && until > now;
}

// pushMessage is what the card says when the push subscription stopped
// working, and "" while it works or was never made.
export function pushMessage(device) {
  const state = (device && device.push) || "";
  if (state === "expired") return "Notifications stopped; turn them on again.";
  if (state.startsWith("failing")) return "Notifications are failing (" + state.replace(/^failing:\s*/, "") + "); turn them on again.";
  return "";
}

function clock(iso) {
  return new Date(iso).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

export function Settings({ snap, stale, act, toast }) {
  const [stored, setStored] = useState(null);
  const [sheet, setSheet] = useState(false);

  // A fresh device from the stream replaces the last PUT's answer.
  useEffect(() => {
    setStored(null);
  }, [snap && snap.device]);

  if (!snap || !snap.device) return html`<section class="sc-screen"><p class="sc-dim">Loading…</p></section>`;

  const device = stored || snap.device;
  const notify = device.notify || {};
  const paused = isPaused(device);
  const message = pushMessage(device);
  const notices = (snap.notices || []).slice().reverse();

  async function put(body) {
    const r = await act("PUT", "/api/v1/device", body);
    if (r.ok && r.data) setStored(r.data);
    return r;
  }

  // The checkbox is uncontrolled between renders, so a refused change puts
  // it back by hand.
  function toggle(key) {
    return async (e) => {
      const el = e.target;
      const r = await put({ [key]: el.checked });
      el.checked = !!((r.ok && r.data ? r.data.notify : notify)[key]);
    };
  }

  // act unpairs on a successful DELETE: it clears the snapshot and goes to
  // pairing.
  async function unpair() {
    await act("DELETE", "/api/v1/device");
  }

  return html`
    <section class="sc-screen">
      <a class="sc-back" href="#">‹ Fleet</a>
      <h1 class="sc-h1 sc-wrap">Settings</h1>

      <section class="sc-section">
        <h2 class="sc-group-title">Notifications</h2>
        <${PushButton} device=${device} act=${act} toast=${toast} />
        ${message ? html`<p class="sc-small sc-err">${message}</p>` : null}
        <label class="sc-row">
          <span>When a session needs you</span>
          <input type="checkbox" checked=${!!notify.waiting} disabled=${stale} onChange=${toggle("waiting")} />
        </label>
        <label class="sc-row">
          <span>When a session finishes</span>
          <input type="checkbox" checked=${!!notify.finished} disabled=${stale} onChange=${toggle("finished")} />
        </label>
        <div class="sc-row">
          <span>${paused ? "Paused until " + clock(notify.paused_until) : "Not paused"}</span>
          ${paused
            ? html`<button class="sc-btn" type="button" disabled=${stale} onClick=${() => put({ pause: "none" })}>Resume now</button>`
            : html`<button class="sc-btn" type="button" disabled=${stale} onClick=${() => put({ pause: "1h" })}>Pause for an hour</button>`}
        </div>
      </section>

      <section class="sc-section">
        <h2 class="sc-group-title">This phone</h2>
        <div class="sc-row"><span>Paired as</span><span>${device.name}</span></div>
        <div class="sc-row"><span>Host</span><span>${snap.host}</span></div>
        <div class="sc-row"><span>Build</span><span class="sc-mono">${snap.build}</span></div>
        <button class="sc-btn sc-danger-text sc-block" type="button" disabled=${stale} onClick=${() => setSheet(true)}>Unpair this phone</button>
      </section>

      <section class="sc-section">
        <h2 class="sc-group-title">Recent notices</h2>
        ${notices.length === 0
          ? html`<p class="sc-dim sc-small">None.</p>`
          : notices.map(
              (n, i) => html`<div key=${i} class=${"sc-notice" + (n.error ? " sc-err" : "")}>
                <span class="sc-age">${ago(n.at)}</span> ${n.text}
              </div>`,
            )}
      </section>

      ${sheet
        ? html`<${ConfirmSheet}
            title="Unpair this phone?"
            confirm="Unpair"
            danger=${true}
            body=${html`<p>This phone stops getting notifications and goes back to pairing. Pair it again with <code>loom web pair</code> on the host.</p>`}
            onConfirm=${unpair}
            onCancel=${() => setSheet(false)}
          />`
        : null}
    </section>
  `;
}

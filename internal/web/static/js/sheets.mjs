// Confirmation sheets. ConfirmSheet is the one sheet the screens use: a
// title, a body (text or markup), and a confirm and a cancel button.
// utf8Len is the screens' shared byte count for the 64 KiB limits.
import { html, useState, useEffect, useRef } from "../lib/preact-htm.mjs";

export function utf8Len(s) {
  return new TextEncoder().encode(s).length;
}

// onConfirm may return a promise; the confirm button stays off until it
// settles, so a double tap sends one request. The caller closes the sheet.
export function ConfirmSheet({ title, body, confirm, danger, onConfirm, onCancel }) {
  const [busy, setBusy] = useState(false);
  const dialog = useRef(null);

  // Focus moves into the dialog on open, so a keyboard or screen reader
  // starts there rather than behind the backdrop.
  useEffect(() => {
    if (dialog.current) dialog.current.focus();
  }, []);

  // Escape cancels, except while the confirmed action runs: the backdrop
  // and the Cancel button are inert then too.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === "Escape" && !busy) onCancel();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel, busy]);

  async function run() {
    if (busy) return;
    setBusy(true);
    try {
      await onConfirm();
    } finally {
      setBusy(false);
    }
  }

  return html`
    <div class="sc-backdrop" onClick=${() => { if (!busy) onCancel(); }}>
      <div
        class="sc-sheet"
        ref=${dialog}
        tabindex="-1"
        role="dialog"
        aria-modal="true"
        aria-label=${title}
        onClick=${(e) => e.stopPropagation()}
      >
        <h2 class="sc-sheet-title">${title}</h2>
        <div class="sc-sheet-body">${body}</div>
        <div class="sc-sheet-actions">
          <button class="sc-btn" type="button" disabled=${busy} onClick=${onCancel}>Cancel</button>
          <button
            class=${"sc-btn " + (danger ? "sc-danger" : "sc-primary")}
            type="button"
            disabled=${busy}
            onClick=${run}
          >${confirm}</button>
        </div>
      </div>
    </div>
  `;
}

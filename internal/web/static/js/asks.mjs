// The asks a waiting session holds, each answered here or in Claude,
// whichever comes first: a question's options, Other and a note, or Chat
// about this; a permission's Allow, its suggested rules and Deny. The
// daemon checks every answer; a refusal comes back as a toast.
import { html, useState } from "../lib/preact-htm.mjs";
import { ConfirmSheet, utf8Len } from "./sheets.mjs";
import { MAX_REPLY } from "./reply.mjs";

// drafts are the unsent answers by ask id: a tab switch remounts the
// screen, and what was chosen stays. They last as long as the page.
const drafts = new Map();

function useDraft(id, initial) {
  const [draft, setShown] = useState(() => drafts.get(id) || initial());
  function set(next) {
    setShown(next);
    drafts.set(id, next);
  }
  return [draft, set];
}

function answerPath(ask) {
  return "asks/" + encodeURIComponent(ask.id) + "/answer";
}

// Asks lists a session's asks, oldest first.
export function Asks({ s, off, run }) {
  const asks = s.asks || [];
  if (asks.length === 0) return null;
  return html`<div class="sc-asks">
    ${asks.map((a) => html`<${AskCard} key=${a.id} ask=${a} off=${off} run=${run} />`)}
  </div>`;
}

function AskCard({ ask, off, run }) {
  const busy = off || ask.answering;
  return html`<div class="sc-ask">
    ${ask.agent ? html`<div class="sc-dim sc-small sc-ask-agent">asked by ${ask.agent}</div>` : null}
    ${ask.kind === "question"
      ? html`<${QuestionAsk} ask=${ask} off=${busy} run=${run} />`
      : html`<${PermissionAsk} ask=${ask} off=${busy} run=${run} />`}
    ${ask.answering ? html`<p class="sc-dim sc-small">Sending…</p>` : null}
  </div>`;
}

function emptyAnswers(ask) {
  const answers = {};
  for (const q of ask.questions) answers[q.question] = { labels: [], other: "", note: "" };
  return { answers, shown: {} };
}

function answered(a) {
  return a.labels.length > 0 || a.other.trim() !== "";
}

function QuestionAsk({ ask, off, run }) {
  const [draft, setDraft] = useDraft(ask.id, () => emptyAnswers(ask));
  const [sheet, setSheet] = useState(false);
  const [chat, setChat] = useState("");

  function update(q, change, shown) {
    const next = { ...draft.answers, [q.question]: { ...draft.answers[q.question], ...change } };
    setDraft({ answers: next, shown: shown === undefined ? draft.shown : { ...draft.shown, [q.question]: shown } });
  }

  function pick(q, o) {
    const a = draft.answers[q.question];
    if (!q.multi) {
      update(q, { labels: [o.label], other: "" }, o.label);
      return;
    }
    const on = a.labels.includes(o.label);
    update(q, { labels: on ? a.labels.filter((l) => l !== o.label) : a.labels.concat(o.label) }, on ? draft.shown[q.question] : o.label);
  }

  function other(q, text) {
    update(q, q.multi || text === "" ? { other: text } : { other: text, labels: [] });
  }

  const ready = ask.questions.every((q) => answered(draft.answers[q.question]));
  const tooLong = Object.values(draft.answers).some((a) => utf8Len(a.other) > MAX_REPLY || utf8Len(a.note) > MAX_REPLY);

  async function send() {
    const answers = {};
    for (const q of ask.questions) {
      const a = draft.answers[q.question];
      answers[q.question] = { labels: a.labels, other: a.other.trim(), note: a.note.trim() };
    }
    const r = await run(answerPath(ask), { answers });
    if (r.ok) drafts.delete(ask.id);
  }

  async function sendChat() {
    const r = await run(answerPath(ask), { chat: true, chat_text: chat.trim() });
    setSheet(false);
    if (r.ok) drafts.delete(ask.id);
  }

  return html`<form class="sc-form sc-question" onSubmit=${(e) => { e.preventDefault(); if (!off && ready && !tooLong) send(); }}>
    ${ask.questions.map((q) => {
      const a = draft.answers[q.question];
      const shown = q.options.find((o) => o.label === draft.shown[q.question] && o.preview);
      return html`<fieldset key=${q.question} class="sc-q" disabled=${off}>
        <legend><span class="sc-q-chip">${q.header}</span> ${q.question}</legend>
        ${q.options.map(
          (o) => html`<label key=${o.label} class="sc-opt">
            <input type=${q.multi ? "checkbox" : "radio"} name=${ask.id + "\u0000" + q.question}
              checked=${a.labels.includes(o.label)} onChange=${() => pick(q, o)} />
            <span><span class="sc-opt-label">${o.label}</span>${o.description ? html`<span class="sc-opt-desc">${o.description}</span>` : null}</span>
          </label>`,
        )}
        ${shown ? html`<pre class="sc-detail-box">${shown.preview}</pre>` : null}
        <input type="text" placeholder="Other…" aria-label=${"Other answer to " + q.question} value=${a.other} onInput=${(e) => other(q, e.target.value)} />
        <input type="text" placeholder="Note (optional)" aria-label=${"Note on " + q.question} value=${a.note} onInput=${(e) => update(q, { note: e.target.value })} />
      </fieldset>`;
    })}
    ${tooLong ? html`<div class="sc-small sc-err">Too long: an answer is at most 64 KiB.</div>` : null}
    <div class="sc-buttons">
      <button class="sc-btn sc-primary" type="submit" disabled=${off || !ready || tooLong}>Answer</button>
      <button class="sc-btn" type="button" disabled=${off} onClick=${() => setSheet(true)}>Chat about this</button>
    </div>
    ${sheet
      ? html`<${ConfirmSheet}
          title="Chat about this"
          confirm="Send"
          body=${html`<p>Claude puts the questions aside and asks what you want to clarify.</p>
            <textarea class="sc-quick-edit" rows="3" placeholder="What to clarify (optional)" value=${chat} onInput=${(e) => setChat(e.target.value)}></textarea>`}
          onConfirm=${sendChat}
          onCancel=${() => setSheet(false)}
        />`
      : null}
  </form>`;
}

function PermissionAsk({ ask, off, run }) {
  const [sheet, setSheet] = useState(null);
  const [message, setMessage] = useState("");

  async function answer(body) {
    await run(answerPath(ask), body);
    setSheet(null);
  }

  const rule = typeof sheet === "number" ? ask.suggestions[sheet - 1] : null;

  return html`<div class="sc-perm">
    <div class="sc-perm-tool">${ask.tool}</div>
    ${(ask.input || []).map(
      (b, i) => html`<div key=${i} class="sc-perm-block">
        <span class="sc-dim sc-small">${b.label}${b.cut ? " (cut)" : ""}</span>
        ${b.mono ? html`<pre class="sc-detail-box">${b.text}</pre>` : html`<p class="sc-perm-text">${b.text}</p>`}
      </div>`,
    )}
    <div class="sc-perm-actions">
      <button class="sc-btn sc-primary sc-block" type="button" disabled=${off} onClick=${() => answer({ allow: true })}>Allow</button>
      ${(ask.suggestions || []).map(
        (g, i) => html`<button key=${i} class="sc-btn sc-block" type="button" disabled=${off}
          onClick=${() => (g.persists ? setSheet(i + 1) : answer({ allow: true, suggestion: i + 1 }))}>${g.label}</button>`,
      )}
      <button class="sc-btn sc-block sc-danger-text" type="button" disabled=${off} onClick=${() => setSheet("deny")}>Deny…</button>
    </div>
    ${rule
      ? html`<${ConfirmSheet}
          title="Save this rule?"
          confirm="Allow and save"
          body=${html`<p>${rule.label}.</p><p>Claude writes it to a settings file, so it lasts past this session.</p>`}
          onConfirm=${() => answer({ allow: true, suggestion: sheet })}
          onCancel=${() => setSheet(null)}
        />`
      : null}
    ${sheet === "deny"
      ? html`<${ConfirmSheet}
          title="Deny this?"
          confirm="Deny"
          danger=${true}
          body=${html`<p>Claude stops this turn, as "No" does in the terminal.</p>
            <textarea class="sc-quick-edit" rows="3" placeholder="What Claude should do instead (optional)" value=${message} onInput=${(e) => setMessage(e.target.value)}></textarea>
            ${utf8Len(message) > MAX_REPLY ? html`<div class="sc-small sc-err">Too long: a message is at most 64 KiB.</div>` : null}`}
          onConfirm=${() => answer({ allow: false, message: message.trim() })}
          onCancel=${() => setSheet(null)}
        />`
      : null}
  </div>`;
}

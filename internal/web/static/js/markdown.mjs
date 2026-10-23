// The last message as the daemon parsed it (GET /api/v1/sessions/{id}/message):
// a tree of plain nodes, each drawn here as its element. The daemon kept no
// markup and no unsafe link, and this file never inserts an HTML string, so
// nothing a message says runs in or restyles the page.
import { html } from "../lib/preact-htm.mjs";

function kids(n) {
  return (n.c || []).map(node);
}

// cell is one table cell, aligned by its column.
function cell(c, k, align) {
  const cls = align[k] ? "md-" + align[k] : undefined;
  return c.t === "th"
    ? html`<th key=${k} class=${cls}>${kids(c)}</th>`
    : html`<td key=${k} class=${cls}>${kids(c)}</td>`;
}

function node(n, i) {
  switch (n.t) {
    case "text":
      return n.v || "";
    case "br":
      return html`<br key=${i} />`;
    case "p":
      return html`<p key=${i}>${kids(n)}</p>`;
    case "h": {
      const Tag = n.level <= 2 ? "h3" : "h4";
      return html`<${Tag} key=${i}>${kids(n)}</${Tag}>`;
    }
    case "ul":
      return html`<ul key=${i}>${kids(n)}</ul>`;
    case "ol":
      return html`<ol key=${i} start=${n.start === undefined ? 1 : n.start}>${kids(n)}</ol>`;
    case "li":
      return html`<li key=${i}>${kids(n)}</li>`;
    case "quote":
      return html`<blockquote key=${i}>${kids(n)}</blockquote>`;
    case "hr":
      return html`<hr key=${i} />`;
    case "code":
      return html`<pre key=${i} class="md-code"><code>${n.v || ""}</code></pre>`;
    case "code_inline":
      return html`<code key=${i}>${n.v || ""}</code>`;
    case "em":
      return html`<em key=${i}>${kids(n)}</em>`;
    case "strong":
      return html`<strong key=${i}>${kids(n)}</strong>`;
    case "del":
      return html`<del key=${i}>${kids(n)}</del>`;
    case "a":
      return n.href
        ? html`<a key=${i} href=${n.href} target="_blank" rel="noopener noreferrer">${kids(n)}</a>`
        : html`<span key=${i}>${kids(n)}</span>`;
    case "table":
      return html`<div key=${i} class="md-table"><table><tbody>
        ${(n.c || []).map((r, j) => html`<tr key=${j}>${(r.c || []).map((c, k) => cell(c, k, n.align || []))}</tr>`)}
      </tbody></table></div>`;
    default:
      // A node this build does not know keeps its content.
      return html`<span key=${i}>${kids(n)}</span>`;
  }
}

// Markdown draws a message's blocks.
export function Markdown({ blocks }) {
  return html`<div class="md">${(blocks || []).map(node)}</div>`;
}

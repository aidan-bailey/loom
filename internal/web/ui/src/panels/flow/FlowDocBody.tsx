// The flow document view (a chunk of its own): one flow document over useFlowDoc, a port of the
// classic document view. It draws the gone, empty, loading and error states, the way back to the
// board, the document's name and path, an index of its sections, its text and the cut note.
import { useCallback, useEffect, useMemo, useRef, type ReactElement } from "react";

import type { MdNode } from "@/api/gen";
import { useFlowDoc } from "@/api/reads";
import type { PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { useScrollPane } from "@/app/scroll";
import { Markdown } from "@/conversation/Markdown";
import { NoSession } from "@/session/NoSession";
import { useSession } from "@/store/fleet";

import { planOf } from "./parts";

interface Heading {
  text: string;
  level: number;
  line: number;
}

// docName words a document's name for the head line.
function docName(doc: string): string {
  if (doc === "drafts-state") return "drafts' state";
  if (doc.startsWith("report:")) return `report · ${doc.slice("report:".length)}`;
  return doc;
}

// textOf is the text a node holds: its text and inline code, in order.
function textOf(node: MdNode | null): string {
  if (node === null) return "";
  if (node.t === "text" || node.t === "code_inline") return node.v ?? "";
  return (node.c ?? []).map(textOf).join("");
}

// headings are the headings of the tree in document order, found through every node with
// children but a code node. A heading's index here is its index among the headings Markdown
// draws.
function headings(blocks: (MdNode | null)[]): Heading[] {
  const found: Heading[] = [];
  const walk = (nodes: (MdNode | null)[]): void => {
    for (const node of nodes) {
      if (node === null) continue;
      if (node.t === "h") {
        found.push({ text: textOf(node), level: node.level ?? 0, line: node.line ?? 0 });
      }
      if (node.t !== "code") walk(node.c ?? []);
    }
  };
  walk(blocks);
  return found;
}

// targetOf is the index among all the headings of the one `at` names, -1 for none: a section
// ("§2", the first heading numbered 2) or a source line (the last heading at or above it).
function targetOf(all: Heading[], at: string): number {
  if (at === "") return -1;
  const section = /^§\s*([\d.]+)/.exec(at);
  if (section !== null) {
    const n = (section[1] ?? "").replace(/\.$/, "");
    return all.findIndex((h) => {
      const text = h.text.trim();
      return (
        text === n ||
        text.startsWith(`${n}.`) ||
        text.startsWith(`${n} `) ||
        text.startsWith(`§${n}`)
      );
    });
  }
  const line = Number(at);
  if (!Number.isFinite(line) || line <= 0) return -1;
  return all.findLastIndex((h) => h.line !== 0 && h.line <= line);
}

// Indent is a heading's whole class string by its level.
const INDENT: Record<number, string> = { 1: "pl-0", 2: "pl-3", 3: "pl-6" };

// Sections is the index of the headings of level 3 or less, drawn for a document with more than 2.
function Sections({
  all,
  jump,
}: {
  all: Heading[];
  jump: (index: number) => void;
}): ReactElement | null {
  const shown = all
    .map((heading, index) => ({ heading, index }))
    .filter((h) => h.heading.level <= 3);
  if (shown.length <= 2) return null;
  return (
    <details className="rounded-xl border border-border bg-card px-3">
      <summary className="flex min-h-[44px] items-center text-sm text-muted-foreground">
        sections
      </summary>
      {shown.map(({ heading, index }) => (
        <button
          key={index}
          type="button"
          onClick={() => {
            jump(index);
          }}
          className={`flex min-h-[44px] w-full items-center text-left text-sm ${INDENT[heading.level] ?? "pl-0"}`}
        >
          {heading.text}
        </button>
      ))}
    </details>
  );
}

const NOTE = "px-4 py-8 text-center text-muted-foreground";

export default function FlowDocBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  const plan = session !== undefined ? planOf(session, props.search.plan ?? "") : "";
  const name = props.params.doc ?? "";
  const { doc, error } = useFlowDoc({ session: props.sessionId ?? 0, plan, doc: name });
  const all = useMemo(() => headings(doc?.blocks ?? []), [doc]);
  const body = useRef<HTMLDivElement>(null);
  const pane = useScrollPane();
  const at = props.search.at ?? "";

  // jump scrolls the index'th heading just below the pinned header: Markdown draws every heading
  // as an h3 or an h4, in document order.
  const jump = useCallback(
    (index: number): void => {
      const el = body.current?.querySelectorAll("h3, h4")[index];
      if (el === undefined) return;
      pane.scrollTo(pane.topFor(el));
    },
    [pane],
  );

  // A held document opens at `at`, once for each document and `at`.
  useEffect(() => {
    if (doc === undefined) return;
    const target = targetOf(all, at);
    if (target >= 0) jump(target);
  }, [doc, all, at, jump]);

  if (session === undefined) return <NoSession sessionId={props.sessionId} />;
  if (plan === "") {
    return (
      <p role="status" className={NOTE}>
        No flow in this session's worktree.
      </p>
    );
  }
  return (
    <section className="flex flex-col gap-3 px-4 pb-6">
      <p className="flex items-center gap-2">
        <a
          href={hrefFor("flow", { id: String(session.id) }, { plan })}
          className="flex min-h-[44px] items-center text-muted-foreground"
        >
          ‹ board
        </a>
        <span className="font-semibold">{docName(name)}</span>
      </p>
      {doc === undefined && error === "" && (
        <p role="status" className={NOTE}>
          Loading…
        </p>
      )}
      {error !== "" && (
        <p role="alert" className="text-error">
          {error}
        </p>
      )}
      {doc !== undefined && (
        <>
          <p className="font-mono text-xs break-all text-faint">{doc.path}</p>
          <Sections all={all} jump={jump} />
          <div ref={body}>
            <Markdown blocks={doc.blocks ?? []} />
          </div>
          {doc.cut && <p className="text-xs text-faint">The document was cut short.</p>}
        </>
      )}
    </section>
  );
}
